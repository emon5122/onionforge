package gateway

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/emon5122/onionforge/internal/config"
	"github.com/emon5122/onionforge/internal/identity"
)

// ensureIdentities loads the identity for every configured service and
// creates missing ones. Existing identities are always reused: the service
// name, not the prefix or target, identifies the identity.
//
// Services whose vanity search runs in the background (see
// config.Background) are returned in pending instead of being created.
func ensureIdentities(ctx context.Context, p Paths, cfg *config.Config, log *Logger) (ids map[string]*identity.Identity, pending []*config.Service, err error) {
	ids = map[string]*identity.Identity{}
	for _, svc := range cfg.SortedServices() {
		dir := p.ServiceDir(svc.Name)
		id, err := identity.Load(svc.Name, dir)
		switch {
		case err == nil:
			if !svc.Prefix.Matches(id.Hostname) {
				log.Warnf("Service '%s' already has an existing Onion identity:\n\n  %s\n\nConfiguration requests prefix:\n\n  %s\n\nChanging the Onion identity would change the public address.\nThe existing identity is kept. To intentionally replace it, back up and\nremove %s, then restart OnionForge.",
					svc.Name, id.Hostname, svc.Prefix, dir)
			}
		case errors.Is(err, identity.ErrNotFound):
			if cfg.Background(svc) {
				pending = append(pending, svc)
				continue
			}
			id, err = createIdentity(ctx, p, cfg, svc, dir, log)
			if err != nil {
				return nil, nil, err
			}
		default:
			return nil, nil, fmt.Errorf("service '%s': %w", svc.Name, err)
		}
		if err := fixIdentityPermissions(dir); err != nil {
			return nil, nil, fmt.Errorf("service '%s': setting permissions on %s: %w", svc.Name, dir, err)
		}
		ids[svc.Name] = id
	}
	return ids, pending, nil
}

func createIdentity(ctx context.Context, p Paths, cfg *config.Config, svc *config.Service, dir string, log *Logger) (*identity.Identity, error) {
	var (
		kp  *identity.Keypair
		err error
	)
	if len(svc.Prefix) == 0 {
		log.Infof("Service '%s': creating new random onion identity", svc.Name)
		kp, err = identity.GenerateRandom()
	} else {
		log.Infof("Service '%s': generating vanity onion identity with prefix '%s' (expected time: %s)",
			svc.Name, svc.Prefix, expectedVanityTime(svc.Prefix.Shortest()))
		kp, err = generateVanityInline(ctx, p, cfg, svc, log)
	}
	if err != nil {
		return nil, fmt.Errorf("service '%s': %w", svc.Name, err)
	}
	return installIdentity(svc.Name, dir, kp, log)
}

func installIdentity(name, dir string, kp *identity.Keypair, log *Logger) (*identity.Identity, error) {
	if err := identity.Write(dir, kp); err != nil {
		return nil, fmt.Errorf("service '%s': %w", name, err)
	}
	if err := fixIdentityPermissions(dir); err != nil {
		return nil, fmt.Errorf("service '%s': %w", name, err)
	}
	log.Infof("Service '%s': identity created: %s", name, kp.Hostname)
	return &identity.Identity{Name: name, Dir: dir, Hostname: kp.Hostname}, nil
}

func vanityOptions(p Paths, cfg *config.Config, svc *config.Service, background bool) identity.VanityOptions {
	return identity.VanityOptions{
		Binary:      p.VanityBin,
		Prefixes:    svc.Prefix,
		Threads:     cfg.Vanity.Threads,
		LowPriority: background,
	}
}

func generateVanityInline(ctx context.Context, p Paths, cfg *config.Config, svc *config.Service, log *Logger) (*identity.Keypair, error) {
	start := time.Now()
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				log.Infof("Service '%s': still searching for prefix '%s' (%s elapsed)",
					svc.Name, svc.Prefix, time.Since(start).Round(time.Second))
			}
		}
	}()
	kp, err := identity.GenerateVanity(ctx, vanityOptions(p, cfg, svc, false))
	if err == nil {
		log.Infof("Service '%s': vanity search finished in %s", svc.Name, time.Since(start).Round(time.Millisecond))
	}
	return kp, err
}

// expectedVanityTime is a rough median for a typical multi-core machine.
func expectedVanityTime(n int) string {
	switch {
	case n <= 5:
		return "seconds"
	case n == 6:
		return "under a minute"
	case n == 7:
		return "several minutes"
	case n == 8:
		return "hours"
	case n == 9:
		return "days"
	default:
		return "months"
	}
}

// inactiveIdentities lists identity directories with no configured service.
// They are never deleted automatically.
func inactiveIdentities(p Paths, cfg *config.Config) []string {
	entries, err := os.ReadDir(p.DataDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, ok := cfg.Services[e.Name()]; ok {
			continue
		}
		if _, err := os.Stat(p.ServiceDir(e.Name()) + "/" + identity.SecretKeyFile); err == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}
