package gateway

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emon5122/onionforge/internal/config"
	"github.com/emon5122/onionforge/internal/identity"
)

// Background vanity searches.
//
// Long searches (hours to days) must not keep the rest of the gateway
// offline. Every service waiting for a vanity identity is served by ONE
// combined search: a single generator process checks each candidate key
// against the prefixes of all pending services at once. Every key is
// therefore useful to every service, so each service's expected time is the
// same as if it had the whole machine to itself, and no matching key is
// thrown away. When a key matches, that service is published immediately
// (Tor SIGHUP, Caddy reload) and the search continues for the rest.
//
// Searches are memoryless. Restarting the generator (because the set of
// pending prefixes changed, or the container restarted) loses no expected
// progress, so nothing is checkpointed.

const (
	benchmarkDuration = 5 * time.Second
	jobLogInterval    = 15 * time.Minute
)

// pendingService is a service waiting for its vanity identity.
type pendingService struct {
	svc    *config.Service
	queued time.Time // first time this prefix set was queued
}

// vanitySearch is the running combined search.
type vanitySearch struct {
	key     string // canonical prefix set, to detect changes
	started time.Time
	cancel  context.CancelFunc
}

// jobEvent is sent from the search goroutine to the supervision loop.
type jobEvent struct {
	search *vanitySearch
	rate   float64 // > 0: speed measured
	kp     *identity.Keypair
	err    error
	done   bool
}

// syncJobs reconciles the combined search with the services that are
// waiting for a vanity identity.
func (g *Gateway) syncJobs(ctx context.Context, cfg *config.Config, pending []*config.Service) {
	want := map[string]*config.Service{}
	for _, svc := range pending {
		want[svc.Name] = svc
	}
	for name, ps := range g.pending {
		svc, ok := want[name]
		if !ok {
			delete(g.pending, name)
			g.log.Infof("Service '%s': vanity search cancelled", name)
		} else if svc.Prefix.String() != ps.svc.Prefix.String() {
			g.log.Infof("Service '%s': prefix changed, searching for '%s' instead", name, svc.Prefix)
			g.pending[name] = &pendingService{svc: svc, queued: time.Now()}
		} else {
			ps.svc = svc
		}
	}
	for _, svc := range pending {
		if _, ok := g.pending[svc.Name]; !ok {
			g.pending[svc.Name] = &pendingService{svc: svc, queued: time.Now()}
		}
	}

	if g.searchThreads != cfg.Vanity.Threads {
		g.vanityRate = 0 // speed depends on the thread count
	}
	key := g.searchKey()
	if g.search != nil && g.search.key == key && g.searchThreads == cfg.Vanity.Threads {
		return // same prefixes: keep the running search
	}
	if g.search != nil {
		g.search.cancel()
		g.search = nil
	}
	if len(g.pending) > 0 {
		g.startSearch(ctx, cfg)
	}
}

// searchKey canonically identifies the pending prefix set.
func (g *Gateway) searchKey() string {
	var parts []string
	for name, ps := range g.pending {
		parts = append(parts, name+"="+ps.svc.Prefix.String())
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

func (g *Gateway) pendingNames() []string {
	names := make([]string, 0, len(g.pending))
	for name := range g.pending {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (g *Gateway) startSearch(ctx context.Context, cfg *config.Config) {
	sctx, cancel := context.WithCancel(ctx)
	search := &vanitySearch{key: g.searchKey(), started: time.Now(), cancel: cancel}
	g.search = search
	g.searchThreads = cfg.Vanity.Threads

	var prefixes []string
	var desc []string
	for _, name := range g.pendingNames() {
		ps := g.pending[name]
		prefixes = append(prefixes, ps.svc.Prefix...)
		desc = append(desc, name+" '"+ps.svc.Prefix.String()+"'")
	}
	opts := identity.VanityOptions{
		Binary:      g.paths.VanityBin,
		Prefixes:    prefixes,
		Threads:     cfg.Vanity.Threads,
		LowPriority: true,
	}
	threads := "all CPUs"
	if opts.Threads > 0 {
		threads = pluralize(opts.Threads, "thread")
	}
	g.log.Infof("Vanity search running in the background for %s: %s (%s, low priority); each service is published as soon as its key is found",
		pluralize(len(g.pending), "service"), strings.Join(desc, ", "), threads)

	send := func(ev jobEvent) {
		select {
		case g.jobEvents <- ev:
		case <-sctx.Done():
		}
	}
	cachedRate := g.vanityRate
	go func() {
		rate := cachedRate
		if rate == 0 {
			if r, err := identity.BenchmarkVanity(sctx, opts, benchmarkDuration); err == nil {
				rate = r
			}
		}
		if rate > 0 {
			send(jobEvent{search: search, rate: rate})
		}
		stop := make(chan struct{})
		go func() {
			t := time.NewTicker(jobLogInterval)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-sctx.Done():
					return
				case <-t.C:
					g.log.Infof("Vanity search still running for %s (this run: %s)",
						strings.Join(desc, ", "), identity.HumanDuration(time.Since(search.started)))
				}
			}
		}()
		kp, err := identity.GenerateVanity(sctx, opts)
		close(stop)
		if sctx.Err() != nil {
			return
		}
		send(jobEvent{search: search, kp: kp, err: err, done: true})
	}()
}

// estimateFor returns the expected search time for one pending service in
// the combined search: every key is checked against its prefixes, so it is
// the same as searching for that service alone.
func (g *Gateway) estimateFor(ps *pendingService) identity.VanityEstimate {
	return identity.EstimateVanity(ps.svc.Prefix, g.vanityRate)
}

// handleJobEvent runs on the supervision loop.
func (g *Gateway) handleJobEvent(ctx context.Context, ev jobEvent) {
	if ev.search != g.search {
		return // stale search (cancelled or replaced)
	}
	if !ev.done {
		first := g.vanityRate == 0
		g.vanityRate = ev.rate
		if first {
			var lines []string
			for _, name := range g.pendingNames() {
				est := g.estimateFor(g.pending[name])
				lines = append(lines, "  "+name+" '"+g.pending[name].svc.Prefix.String()+"': "+
					identity.HumanDuration(est.Median)+" (median), "+identity.HumanDuration(est.P90)+" (90% chance)")
			}
			g.log.Infof("Vanity search speed: %.0f M keys/s. Expected time per service:\n%s", ev.rate/1e6, strings.Join(lines, "\n"))
		}
		g.updateServiceState()
		_ = writeState(g.paths.StateFile(), &g.state)
		return
	}
	g.search = nil
	if ev.err != nil {
		g.log.Errorf("Vanity search failed: %v (send SIGHUP or restart to retry)", ev.err)
		g.updateServiceState()
		_ = writeState(g.paths.StateFile(), &g.state)
		return
	}

	winner := assignKey(ev.kp.Hostname, g.pendingServices())
	if winner == "" {
		g.syncJobs(ctx, g.cfg, g.pendingServices())
		return
	}
	ps := g.pending[winner]
	g.log.Infof("Service '%s': vanity key found after %s", winner, identity.HumanDuration(time.Since(ps.queued)))
	if _, err := installIdentity(winner, g.paths.ServiceDir(winner), ev.kp, g.log); err != nil {
		g.log.Errorf("%v", err)
		g.syncJobs(ctx, g.cfg, g.pendingServices())
		return
	}
	delete(g.pending, winner)
	// apply publishes the new service and restarts the search for the
	// remaining services.
	if err := g.apply(ctx, g.cfg); err != nil && ctx.Err() == nil {
		g.log.Errorf("Publishing service '%s' failed: %v", winner, err)
	}
}

// assignKey picks the pending service a found key belongs to. If the
// hostname matches several services (for example prefixes "ab" and "abc"),
// the longest, rarest match wins; ties go to the first service by name.
func assignKey(hostname string, pending []*config.Service) string {
	winner, best := "", -1
	for _, svc := range pending {
		for _, p := range svc.Prefix {
			if identity.HasPrefix(hostname, p) && len(p) > best {
				winner, best = svc.Name, len(p)
			}
		}
	}
	return winner
}

func (g *Gateway) pendingServices() []*config.Service {
	var out []*config.Service
	for _, name := range g.pendingNames() {
		out = append(out, g.pending[name].svc)
	}
	return out
}

func pluralize(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
