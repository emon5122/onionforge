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
// creates the missing ones. Existing identities are always reused: the
// service name, not the prefix or target, identifies the identity.
func ensureIdentities(ctx context.Context, p Paths, cfg *config.Config, log *Logger) (map[string]*identity.Identity, error) {
	out := map[string]*identity.Identity{}
	for _, svc := range cfg.SortedServices() {
		dir := p.ServiceDir(svc.Name)
		id, err := identity.Load(svc.Name, dir)
		switch {
		case err == nil:
			if svc.Prefix != "" && !identity.HasPrefix(id.Hostname, svc.Prefix) {
				log.Warnf("Service '%s' already has an existing Onion identity:\n\n  %s\n\nConfiguration requests prefix:\n\n  %s\n\nChanging the Onion identity would change the public address.\nThe existing identity is kept. To intentionally replace it, back up and\nremove %s, then restart OnionForge.",
					svc.Name, id.Hostname, svc.Prefix, dir)
			}
		case errors.Is(err, identity.ErrNotFound):
			id, err = createIdentity(ctx, p, svc, dir, log)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("service '%s': %w", svc.Name, err)
		}
		if err := fixIdentityPermissions(dir); err != nil {
			return nil, fmt.Errorf("service '%s': setting permissions on %s: %w", svc.Name, dir, err)
		}
		out[svc.Name] = id
	}
	return out, nil
}

func createIdentity(ctx context.Context, p Paths, svc *config.Service, dir string, log *Logger) (*identity.Identity, error) {
	var (
		kp  *identity.Keypair
		err error
	)
	if svc.Prefix == "" {
		log.Infof("Service '%s': creating new random onion identity", svc.Name)
		kp, err = identity.GenerateRandom()
	} else {
		log.Infof("Service '%s': generating vanity onion identity with prefix '%s' (expected time: %s)",
			svc.Name, svc.Prefix, expectedVanityTime(len(svc.Prefix)))
		kp, err = generateVanity(ctx, p, svc, log)
	}
	if err != nil {
		return nil, fmt.Errorf("service '%s': %w", svc.Name, err)
	}
	if err := identity.Write(dir, kp); err != nil {
		return nil, fmt.Errorf("service '%s': %w", svc.Name, err)
	}
	log.Infof("Service '%s': identity created: %s", svc.Name, kp.Hostname)
	return &identity.Identity{Name: svc.Name, Dir: dir, Hostname: kp.Hostname}, nil
}

func generateVanity(ctx context.Context, p Paths, svc *config.Service, log *Logger) (*identity.Keypair, error) {
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
	kp, err := identity.GenerateVanity(ctx, p.VanityBin, svc.Prefix)
	if err == nil {
		log.Infof("Service '%s': found prefix '%s' in %s", svc.Name, svc.Prefix, time.Since(start).Round(time.Millisecond))
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
