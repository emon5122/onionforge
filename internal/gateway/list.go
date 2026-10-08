package gateway

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/emon5122/onionforge/internal/config"
	"github.com/emon5122/onionforge/internal/identity"
)

// List prints every identity in the data directory alongside the configured
// services. Only hostnames are shown, never key material.
func List(p Paths, w io.Writer) error {
	cfg, cfgErr := config.Load(p.Config)
	pending := map[string]PendingState{}
	if st, err := ReadState(p.StateFile()); err == nil {
		for _, ps := range st.Pending {
			pending[ps.Name] = ps
		}
	}

	names := map[string]bool{}
	if cfg != nil {
		for name := range cfg.Services {
			names[name] = true
		}
	}
	entries, err := os.ReadDir(p.DataDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			names[e.Name()] = true
		}
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVICE\tSTATUS\tONION\tTARGET")
	for _, name := range sorted {
		var svc *config.Service
		if cfg != nil {
			svc = cfg.Services[name]
		}
		hostname, status := "-", ""
		id, err := identity.Load(name, p.ServiceDir(name))
		switch {
		case err == nil:
			hostname = id.Hostname
			if svc != nil {
				status = "active"
				if !svc.Prefix.Matches(id.Hostname) {
					status = "active (prefix mismatch)"
				}
			} else {
				status = "inactive (not configured)"
			}
		case errors.Is(err, identity.ErrNotFound):
			if svc == nil {
				continue
			}
			status = "pending (no identity yet)"
			if ps, ok := pending[name]; ok {
				status = describePending(ps)
			}
		default:
			status = "error: " + err.Error()
		}
		target := "-"
		if svc != nil {
			target = svc.Target
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", name, status, hostname, target)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if cfgErr != nil {
		fmt.Fprintf(w, "\nNote: configuration could not be loaded, showing identities only:\n%v\n", cfgErr)
	}
	return nil
}

func describePending(ps PendingState) string {
	s := "searching for '" + strings.Join(ps.Prefixes, " | ") + "'"
	if ps.Started.IsZero() {
		return s
	}
	s += ", running " + identity.HumanDuration(time.Since(ps.Started))
	if ps.MedianSeconds > 0 {
		s += fmt.Sprintf(", expected %s (median) / %s (90%%)",
			identity.HumanDuration(time.Duration(ps.MedianSeconds*float64(time.Second))),
			identity.HumanDuration(time.Duration(ps.P90Seconds*float64(time.Second))))
	}
	return s
}
