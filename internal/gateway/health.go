package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/emon5122/onionforge/internal/caddy"
	"github.com/emon5122/onionforge/internal/identity"
)

// Healthcheck verifies that the gateway is up. It deliberately does not
// contact upstreams: a temporarily unavailable backend does not make the
// gateway itself unhealthy.
func Healthcheck(p Paths) error {
	st, err := ReadState(p.StateFile())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: OnionForge has not started", errNotReady)
		}
		return err
	}
	if st.Status != StatusReady {
		return fmt.Errorf("%w: status is %q", errNotReady, st.Status)
	}
	if err := checkProcess(st.TorPID, "tor"); err != nil {
		return err
	}
	if err := checkProcess(st.CaddyPID, "caddy"); err != nil {
		return err
	}
	if len(st.Services) == 0 {
		return errors.New("no services initialized")
	}
	for _, s := range st.Services {
		got, err := identity.ReadHostname(s.Dir)
		if err != nil {
			return fmt.Errorf("service '%s': hostname file missing: %v", s.Name, err)
		}
		if got != s.Hostname {
			return fmt.Errorf("service '%s': hostname file says %s, expected %s", s.Name, got, s.Hostname)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg, err := caddy.Admin{Socket: st.AdminSocket}.Config(ctx)
	if err != nil {
		return fmt.Errorf("caddy admin API: %v", err)
	}
	if s := strings.TrimSpace(string(cfg)); s == "" || s == "null" {
		return errors.New("caddy has no configuration loaded")
	}
	c, err := net.DialTimeout("tcp", st.Listener, 3*time.Second)
	if err != nil {
		return fmt.Errorf("caddy listener %s: %v", st.Listener, err)
	}
	c.Close()
	return nil
}

func checkProcess(pid int, name string) error {
	if pid <= 0 {
		return fmt.Errorf("%s is not running", name)
	}
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return fmt.Errorf("%s (pid %d) is not running", name, pid)
	}
	comm, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err == nil && strings.TrimSpace(string(comm)) != name {
		return fmt.Errorf("%s (pid %d) is not running", name, pid)
	}
	return nil
}
