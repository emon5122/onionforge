package gateway

import (
	"context"
	"strconv"
	"time"

	"github.com/emon5122/onionforge/internal/config"
	"github.com/emon5122/onionforge/internal/identity"
)

// Background vanity searches.
//
// Long searches (hours to days) must not keep the rest of the gateway
// offline, so they run as jobs: every other service is published right away,
// and when a job finds its key the gateway applies the configuration again,
// which adds the new onion service to Tor (SIGHUP, no restart) and Caddy.
//
// Searches are memoryless. A restart loses no expected progress, so jobs
// don't checkpoint.

const (
	benchmarkDuration = 5 * time.Second
	jobLogInterval    = 15 * time.Minute
)

type vanityJob struct {
	name     string
	key      string // prefixes, to detect configuration changes
	started  time.Time
	cancel   context.CancelFunc
	estimate identity.VanityEstimate
	rate     float64
}

// jobEvent is sent from a job goroutine to the supervision loop.
type jobEvent struct {
	job      *vanityJob
	estimate *identity.VanityEstimate
	rate     float64
	kp       *identity.Keypair
	err      error
	done     bool
}

// syncJobs starts jobs for pending services and cancels jobs that are no
// longer wanted (service removed, prefixes changed, or key imported).
func (g *Gateway) syncJobs(ctx context.Context, cfg *config.Config, pending []*config.Service) {
	want := map[string]*config.Service{}
	for _, svc := range pending {
		want[svc.Name] = svc
	}
	for name, job := range g.jobs {
		if svc, ok := want[name]; !ok || svc.Prefix.String() != job.key {
			job.cancel()
			delete(g.jobs, name)
			g.log.Infof("Service '%s': background vanity search cancelled", name)
		}
	}
	for _, svc := range pending {
		if _, running := g.jobs[svc.Name]; !running {
			g.startJob(ctx, cfg, svc)
		}
	}
}

func (g *Gateway) startJob(ctx context.Context, cfg *config.Config, svc *config.Service) {
	jctx, cancel := context.WithCancel(ctx)
	job := &vanityJob{name: svc.Name, key: svc.Prefix.String(), started: time.Now(), cancel: cancel}
	g.jobs[svc.Name] = job
	opts := vanityOptions(g.paths, cfg, svc, true)
	threads := "all CPUs"
	if opts.Threads > 0 {
		threads = pluralize(opts.Threads, "thread")
	}
	g.log.Infof("Service '%s': searching for vanity prefix '%s' in the background (%s, low priority); the service is published once the key is found",
		svc.Name, svc.Prefix, threads)

	send := func(ev jobEvent) {
		select {
		case g.jobEvents <- ev:
		case <-jctx.Done():
		}
	}
	go func() {
		if rate, err := identity.BenchmarkVanity(jctx, opts, benchmarkDuration); err == nil {
			est := identity.EstimateVanity(opts.Prefixes, rate)
			send(jobEvent{job: job, estimate: &est, rate: rate})
			g.log.Infof("Service '%s': %.0f M keys/s; expected time %s (median), %s (90%% chance)",
				svc.Name, rate/1e6, identity.HumanDuration(est.Median), identity.HumanDuration(est.P90))
		}
		stop := make(chan struct{})
		go func() {
			t := time.NewTicker(jobLogInterval)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-jctx.Done():
					return
				case <-t.C:
					g.log.Infof("Service '%s': still searching for '%s' (%s elapsed)",
						svc.Name, svc.Prefix, identity.HumanDuration(time.Since(job.started)))
				}
			}
		}()
		kp, err := identity.GenerateVanity(jctx, opts)
		close(stop)
		if jctx.Err() != nil {
			return
		}
		send(jobEvent{job: job, kp: kp, err: err, done: true})
	}()
}

// handleJobEvent runs on the supervision loop.
func (g *Gateway) handleJobEvent(ctx context.Context, ev jobEvent) {
	if g.jobs[ev.job.name] != ev.job {
		return // stale job (cancelled or replaced)
	}
	if !ev.done {
		ev.job.estimate = *ev.estimate
		ev.job.rate = ev.rate
		g.updateServiceState()
		_ = writeState(g.paths.StateFile(), &g.state)
		return
	}
	delete(g.jobs, ev.job.name)
	if ev.err != nil {
		g.log.Errorf("Service '%s': vanity search failed: %v (send SIGHUP or restart to retry)", ev.job.name, ev.err)
		g.updateServiceState()
		_ = writeState(g.paths.StateFile(), &g.state)
		return
	}
	g.log.Infof("Service '%s': vanity search finished after %s", ev.job.name, identity.HumanDuration(time.Since(ev.job.started)))
	if _, err := installIdentity(ev.job.name, g.paths.ServiceDir(ev.job.name), ev.kp, g.log); err != nil {
		g.log.Errorf("%v", err)
		return
	}
	if err := g.apply(ctx, g.cfg); err != nil && ctx.Err() == nil {
		g.log.Errorf("Publishing service '%s' failed: %v", ev.job.name, err)
	}
}

func pluralize(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
