package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/emon5122/onionforge/internal/caddy"
	"github.com/emon5122/onionforge/internal/config"
	"github.com/emon5122/onionforge/internal/identity"
	"github.com/emon5122/onionforge/internal/tor"
)

// Timeouts for startup and shutdown.
const (
	hostnameTimeout = 120 * time.Second
	caddyTimeout    = 30 * time.Second
	stopTimeout     = 6 * time.Second
)

// ExitError carries a process exit code out of Run.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

// Gateway is a running OnionForge instance.
type Gateway struct {
	paths Paths
	log   *Logger

	cfg        *config.Config
	identities map[string]*identity.Identity

	tor   *process
	caddy *process
	exits chan exitEvent

	jobs      map[string]*vanityJob
	jobEvents chan jobEvent

	torBootstrapped atomic.Bool
	state           State
}

// Run executes the full lifecycle and blocks until shutdown.
func Run(p Paths, log *Logger, version string) error {
	syscall.Umask(0o077)

	sigs := make(chan os.Signal, 8)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hup := make(chan struct{}, 1)
	go func() {
		for s := range sigs {
			if s == syscall.SIGHUP {
				select {
				case hup <- struct{}{}:
				default:
				}
				continue
			}
			log.Infof("Received %s, shutting down", s)
			cancel()
		}
	}()

	g := &Gateway{paths: p, log: log, exits: make(chan exitEvent, 4),
		jobs: map[string]*vanityJob{}, jobEvents: make(chan jobEvent, 8)}
	g.state = State{
		Status:      StatusStarting,
		PID:         os.Getpid(),
		Listener:    net.JoinHostPort(ListenHost, strconv.Itoa(ListenPort)),
		AdminSocket: p.AdminSocket(),
	}

	log.Raw(rule + " OnionForge " + version + "\n" + rule + "\nConfiguration: " + p.Config + "\n\n")

	err := g.start(ctx)
	if err != nil {
		g.shutdown()
		if ctx.Err() != nil {
			return nil
		}
		return &ExitError{Code: 1, Err: err}
	}
	return g.loop(ctx, hup)
}

const rule = "==================================================\n"

func (g *Gateway) start(ctx context.Context) error {
	p, log := g.paths, g.log

	// 1. Validate configuration before touching anything.
	cfg, err := config.Load(p.Config)
	if err != nil {
		return err
	}
	g.cfg = cfg
	log.Infof("Configuration valid: %d service(s)", len(cfg.Services))

	// 2-3. Directories, ownership, permissions, privilege drop.
	if err := prepareFilesystem(p, log); err != nil {
		return err
	}
	if err := os.MkdirAll(p.CaddyHome(), 0o700); err != nil {
		return err
	}
	os.Remove(p.StateFile())
	if err := writeState(p.StateFile(), &g.state); err != nil {
		return fmt.Errorf("writing state: %w", err)
	}

	// 4. Identities.
	ids, pending, err := ensureIdentities(ctx, p, cfg, log)
	if err != nil {
		return err
	}
	g.identities = ids
	if inactive := inactiveIdentities(p, cfg); len(inactive) > 0 {
		log.Infof("Keeping identities of services no longer configured (not published): %s", strings.Join(inactive, ", "))
	}

	// 5. torrc.
	if err := g.writeTorrc(cfg); err != nil {
		return err
	}
	// Tor rewrites each hostname file from the secret key when it loads a
	// service. Removing the derived files first makes "hostname exists"
	// proof that this Tor process initialized the service.
	for _, id := range ids {
		os.Remove(id.Dir + "/" + identity.HostnameFile)
	}

	// 6. Tor.
	g.tor, err = startProcess(log, "tor", nil, g.exits, g.watchTor, p.TorBin, "-f", p.Torrc())
	if err != nil {
		return err
	}
	g.state.TorPID = g.tor.pid()

	// 7. Wait for hostname files and verify them.
	if err := g.waitHostnames(ctx, cfg); err != nil {
		return err
	}

	// 8-10. Caddy.
	caddyfile := g.renderCaddyfile(cfg)
	if err := g.writeCaddyfile(caddyfile); err != nil {
		return err
	}
	if err := g.validateCaddyfile(); err != nil {
		return err
	}
	g.caddy, err = startProcess(log, "caddy", g.caddyEnv(), g.exits, nil, p.CaddyBin,
		"run", "--config", p.Caddyfile(), "--adapter", "caddyfile")
	if err != nil {
		return err
	}
	g.state.CaddyPID = g.caddy.pid()
	if err := g.waitCaddy(ctx); err != nil {
		return err
	}

	// 11. Ready.
	g.state.Status = StatusReady
	g.updateServiceState()
	if err := writeState(p.StateFile(), &g.state); err != nil {
		return fmt.Errorf("writing state: %w", err)
	}
	g.printServices()

	// 12. Long vanity searches continue in the background.
	g.syncJobs(ctx, cfg, pending)
	return nil
}

// loop supervises children until a signal or an unexpected exit.
func (g *Gateway) loop(ctx context.Context, hup <-chan struct{}) error {
	for {
		select {
		case <-ctx.Done():
			g.shutdown()
			g.log.Infof("Shutdown complete")
			return nil
		case <-hup:
			g.log.Infof("Received SIGHUP, reloading configuration")
			if err := g.reload(ctx); err != nil {
				if ctx.Err() != nil {
					continue
				}
				g.log.Errorf("Reload failed, keeping previous configuration:\n%v", err)
			}
		case ev := <-g.jobEvents:
			g.handleJobEvent(ctx, ev)
		case ev := <-g.exits:
			if ctx.Err() != nil {
				continue
			}
			err := fmt.Errorf("%s %s unexpectedly", ev.proc.name, ev.proc.describeExit())
			g.log.Errorf("%v", err)
			g.shutdown()
			return &ExitError{Code: 1, Err: err}
		}
	}
}

// shutdown stops Caddy first (no new requests reach upstreams), then Tor.
// Identities are never touched.
func (g *Gateway) shutdown() {
	if g.state.Status == StatusStopping {
		return
	}
	g.state.Status = StatusStopping
	_ = writeState(g.paths.StateFile(), &g.state)
	if g.caddy != nil && !g.caddy.exited() {
		g.log.Infof("Stopping Caddy")
		g.caddy.stop(stopTimeout)
	}
	if g.tor != nil && !g.tor.exited() {
		g.log.Infof("Stopping Tor")
		g.tor.stop(stopTimeout)
	}
	os.Remove(g.paths.StateFile())
}

func (g *Gateway) watchTor(line string) {
	if strings.Contains(line, "Bootstrapped 100%") && !g.torBootstrapped.Swap(true) {
		g.log.Infof("Tor is connected to the Tor network; onion services are being published")
	}
}

func (g *Gateway) writeTorrc(cfg *config.Config) error {
	var services []tor.Service
	for _, svc := range cfg.SortedServices() {
		if _, ok := g.identities[svc.Name]; !ok {
			continue // vanity search still running
		}
		services = append(services, tor.Service{Name: svc.Name, Dir: g.paths.ServiceDir(svc.Name)})
	}
	torrc := tor.Generate(tor.Options{
		DataDirectory: g.paths.TorDataDir(),
		Listener:      g.state.Listener,
		ConfigSource:  g.paths.Config,
	}, services)
	if err := writeFileAtomic(g.paths.Torrc(), []byte(torrc)); err != nil {
		return fmt.Errorf("writing torrc: %w", err)
	}
	var out bytes.Buffer
	cmd := exec.Command(g.paths.TorBin, "--verify-config", "-f", g.paths.Torrc())
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("generated torrc failed verification: %v\n%s", err, strings.TrimSpace(out.String()))
	}
	return nil
}

func (g *Gateway) waitHostnames(ctx context.Context, cfg *config.Config) error {
	deadline := time.After(hostnameTimeout)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		pending := 0
		for _, svc := range cfg.SortedServices() {
			id, ok := g.identities[svc.Name]
			if !ok {
				continue
			}
			got, err := identity.ReadHostname(id.Dir)
			if err != nil || got == "" {
				pending++
				continue
			}
			if got != id.Hostname {
				return fmt.Errorf("service '%s': Tor reports hostname %s but the identity key corresponds to %s", svc.Name, got, id.Hostname)
			}
		}
		if pending == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-g.exits:
			// Re-queue so the supervision loop still sees the exit (a
			// failed reload must not leave the gateway running without Tor).
			g.exits <- ev
			return fmt.Errorf("%s %s while initializing onion services", ev.proc.name, ev.proc.describeExit())
		case <-deadline:
			return fmt.Errorf("timed out after %s waiting for Tor to initialize %d onion service(s)", hostnameTimeout, pending)
		case <-tick.C:
		}
	}
}

func (g *Gateway) routes(cfg *config.Config) []caddy.Route {
	var routes []caddy.Route
	for _, svc := range cfg.SortedServices() {
		if id, ok := g.identities[svc.Name]; ok {
			routes = append(routes, caddy.Route{Hostname: id.Hostname, Service: svc})
		}
	}
	return routes
}

func (g *Gateway) renderCaddyfile(cfg *config.Config) string {
	return caddy.Generate(caddy.Options{
		ListenHost:   ListenHost,
		ListenPort:   ListenPort,
		AdminSocket:  g.paths.AdminSocket(),
		AccessLog:    cfg.Logging.AccessLog,
		ConfigSource: g.paths.Config,
	}, g.routes(cfg))
}

func (g *Gateway) writeCaddyfile(content string) error {
	if err := writeFileAtomic(g.paths.Caddyfile(), []byte(content)); err != nil {
		return fmt.Errorf("writing Caddyfile: %w", err)
	}
	return nil
}

func (g *Gateway) caddyEnv() []string {
	h := g.paths.CaddyHome()
	return []string{"HOME=" + h, "XDG_CONFIG_HOME=" + h, "XDG_DATA_HOME=" + h}
}

func (g *Gateway) validateCaddyfile() error {
	return validateCaddyfileAt(g.paths.CaddyBin, g.paths.Caddyfile(), g.caddyEnv())
}

func validateCaddyfileAt(bin, path string, env []string) error {
	var out bytes.Buffer
	cmd := exec.Command(bin, "validate", "--config", path, "--adapter", "caddyfile")
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("generated Caddyfile failed validation: %v\n%s", err, strings.TrimSpace(out.String()))
	}
	return nil
}

func (g *Gateway) waitCaddy(ctx context.Context) error {
	admin := caddy.Admin{Socket: g.paths.AdminSocket()}
	deadline := time.Now().Add(caddyTimeout)
	for {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, err := admin.Config(cctx)
		cancel()
		if err == nil {
			if c, err := net.DialTimeout("tcp", g.state.Listener, time.Second); err == nil {
				c.Close()
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for Caddy to start: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-g.exits:
			return fmt.Errorf("%s %s during startup", ev.proc.name, ev.proc.describeExit())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// reload re-reads onionforge.yml and applies it.
func (g *Gateway) reload(ctx context.Context) error {
	cfg, err := config.Load(g.paths.Config)
	if err != nil {
		return err
	}
	return g.apply(ctx, cfg)
}

// apply makes cfg the running configuration. Caddy is reloaded gracefully;
// Tor is only reconfigured (SIGHUP, no restart) when the set of published
// onion services changed. Identities of existing services are always kept,
// so changing a target keeps the address. It is used for SIGHUP reloads and
// when a background vanity search completes.
func (g *Gateway) apply(ctx context.Context, cfg *config.Config) error {
	ids, pending, err := ensureIdentities(ctx, g.paths, cfg, g.log)
	if err != nil {
		return err
	}
	oldIDs := g.identities
	g.identities = ids

	caddyfile := g.renderCaddyfile(cfg)
	if err := g.writeCaddyfile(caddyfile); err != nil {
		g.identities = oldIDs
		return err
	}
	if err := g.validateCaddyfile(); err != nil {
		g.identities = oldIDs
		return err
	}

	if !sameKeys(oldIDs, ids) {
		for name, id := range ids {
			if _, ok := oldIDs[name]; !ok {
				os.Remove(id.Dir + "/" + identity.HostnameFile)
			}
		}
		if err := g.writeTorrc(cfg); err != nil {
			g.identities = oldIDs
			return err
		}
		g.log.Infof("Onion services changed; reloading Tor")
		if err := g.tor.signal(syscall.SIGHUP); err != nil {
			return fmt.Errorf("signalling Tor: %w", err)
		}
		if err := g.waitHostnames(ctx, cfg); err != nil {
			return err
		}
		for _, name := range removedServices(g.cfg, cfg) {
			g.log.Infof("Service '%s' removed from configuration; its identity is kept in %s", name, g.paths.ServiceDir(name))
		}
	} else {
		g.log.Infof("Onion services unchanged; Tor is not restarted")
	}

	lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := (caddy.Admin{Socket: g.paths.AdminSocket()}).Load(lctx, caddyfile); err != nil {
		return fmt.Errorf("reloading Caddy: %w", err)
	}
	g.cfg = cfg
	g.syncJobs(ctx, cfg, pending)
	g.updateServiceState()
	if err := writeState(g.paths.StateFile(), &g.state); err != nil {
		return err
	}
	g.log.Infof("Configuration applied")
	g.printServices()
	return nil
}

func sameKeys(a, b map[string]*identity.Identity) bool {
	if len(a) != len(b) {
		return false
	}
	for name := range a {
		if _, ok := b[name]; !ok {
			return false
		}
	}
	return true
}

func removedServices(old, cur *config.Config) []string {
	var out []string
	for _, svc := range old.SortedServices() {
		if _, ok := cur.Services[svc.Name]; !ok {
			out = append(out, svc.Name)
		}
	}
	return out
}

func (g *Gateway) updateServiceState() {
	g.state.Services = nil
	g.state.Pending = nil
	for _, svc := range g.cfg.SortedServices() {
		if id, ok := g.identities[svc.Name]; ok {
			g.state.Services = append(g.state.Services, ServiceState{
				Name:     svc.Name,
				Hostname: id.Hostname,
				Target:   svc.Target,
				Dir:      id.Dir,
			})
			continue
		}
		ps := PendingState{Name: svc.Name, Prefixes: svc.Prefix, Target: svc.Target}
		if job, ok := g.jobs[svc.Name]; ok {
			ps.Started = job.started
			ps.KeysPerSecond = job.rate
			ps.MedianSeconds = job.estimate.Median.Seconds()
			ps.P90Seconds = job.estimate.P90.Seconds()
		}
		g.state.Pending = append(g.state.Pending, ps)
	}
}

func (g *Gateway) printServices() {
	var b strings.Builder
	b.WriteString("\nServices:\n")
	for _, s := range g.state.Services {
		fmt.Fprintf(&b, "\n  %s\n    Onion:  http://%s\n    Target: %s\n", s.Name, s.Hostname, s.Target)
	}
	for _, s := range g.state.Pending {
		fmt.Fprintf(&b, "\n  %s\n    Onion:  (searching for vanity prefix '%s'; published automatically when found)\n    Target: %s\n",
			s.Name, strings.Join(s.Prefixes, " | "), s.Target)
	}
	b.WriteString("\n" + rule + " OnionForge is ready\n" + rule)
	g.log.Raw(b.String())
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// errNotReady is returned by health checks before startup completes.
var errNotReady = errors.New("not ready")
