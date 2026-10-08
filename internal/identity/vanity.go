package identity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultVanityBinary is the vanity generator bundled in the image
// (github.com/offset/onion-vanity-address).
const DefaultVanityBinary = "onion-vanity-address"

// VanityOptions configures a vanity search.
type VanityOptions struct {
	// Binary is the generator executable (DefaultVanityBinary if empty).
	Binary string
	// Prefixes are alternatives; the first address matching any wins.
	Prefixes []string
	// Threads limits CPU threads (GOMAXPROCS); 0 uses all CPUs.
	Threads int
	// LowPriority runs the search with the lowest CPU priority (nice 19) so
	// long searches don't compete with Tor and Caddy.
	LowPriority bool
}

func (o VanityOptions) command(ctx context.Context, args ...string) *exec.Cmd {
	bin := o.Binary
	if bin == "" {
		bin = DefaultVanityBinary
	}
	var cmd *exec.Cmd
	if nice, err := exec.LookPath("nice"); err == nil && o.LowPriority {
		cmd = exec.CommandContext(ctx, nice, append([]string{"-n", "19", bin}, args...)...)
	} else {
		cmd = exec.CommandContext(ctx, bin, args...)
	}
	cmd.Env = os.Environ()
	if o.Threads > 0 {
		cmd.Env = append(cmd.Env, "GOMAXPROCS="+strconv.Itoa(o.Threads))
	}
	return cmd
}

// GenerateVanity runs the bundled vanity generator and returns the
// resulting keypair. The generator's output is not trusted blindly: the
// public key and hostname are re-derived from the secret key and checked
// against the requested prefixes.
func GenerateVanity(ctx context.Context, o VanityOptions) (*Keypair, error) {
	if len(o.Prefixes) == 0 {
		return nil, errors.New("no prefix given")
	}
	var stdout, stderr bytes.Buffer
	cmd := o.command(ctx, o.Prefixes...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("vanity generator failed: %v: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil, fmt.Errorf("running vanity generator: %w", err)
	}
	defer clear(stdout.Bytes())

	var encoded string
	sc := bufio.NewScanner(&stdout)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), SecretKeyFile+":"); ok {
			encoded = strings.TrimSpace(v)
		}
	}
	if encoded == "" {
		return nil, errors.New("vanity generator produced no secret key")
	}
	secret, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("vanity generator produced a malformed secret key")
	}
	defer clear(secret)
	kp, err := ParseSecretKey(secret)
	if err != nil {
		return nil, fmt.Errorf("vanity generator output: %w", err)
	}
	for _, p := range o.Prefixes {
		if HasPrefix(kp.Hostname, p) {
			return kp, nil
		}
	}
	return nil, fmt.Errorf("vanity generator returned %s, which matches none of %q", kp.Hostname, o.Prefixes)
}

var rateRe = regexp.MustCompile(`\(([0-9]+) attempts/s\)`)

// BenchmarkVanity measures the generator's key rate (keys per second) on
// this machine with the given options.
func BenchmarkVanity(ctx context.Context, o VanityOptions, d time.Duration) (float64, error) {
	var stderr bytes.Buffer
	// A prefix that is practically impossible to find within d.
	cmd := o.command(ctx, "--timeout", d.String(), "zzzzzzzzzzzzzzzz")
	cmd.Stderr = &stderr
	_ = cmd.Run() // exits non-zero when the timeout is reached
	m := rateRe.FindStringSubmatch(stderr.String())
	if m == nil {
		return 0, fmt.Errorf("could not measure vanity generator speed: %s", strings.TrimSpace(stderr.String()))
	}
	rate, _ := strconv.ParseFloat(m[1], 64)
	if rate <= 0 {
		return 0, errors.New("vanity generator reported no progress")
	}
	return rate, nil
}

// VanityEstimate describes the expected search duration. Searches are
// memoryless: the expected remaining time never decreases with time spent,
// and restarting a search loses nothing on average.
type VanityEstimate struct {
	Median time.Duration // 50% chance of finishing within this time
	P90    time.Duration // 90% chance
}

// EstimateVanity computes search-time estimates for prefixes at rate
// keys/second.
func EstimateVanity(prefixes []string, rate float64) VanityEstimate {
	p := 0.0 // probability that one key matches any prefix
	for _, s := range prefixes {
		p += math.Pow(32, -float64(len(s)))
	}
	if p <= 0 || rate <= 0 {
		return VanityEstimate{}
	}
	mean := 1 / (p * rate) // seconds
	sec := func(f float64) time.Duration {
		v := f * mean
		if v > float64(math.MaxInt64/int64(time.Second)) {
			return time.Duration(math.MaxInt64)
		}
		return time.Duration(v * float64(time.Second))
	}
	return VanityEstimate{Median: sec(math.Ln2), P90: sec(math.Log(10))}
}

// HumanDuration renders long durations compactly ("2.1 days", "45 min").
func HumanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.0f s", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%.0f min", d.Minutes())
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1f hours", d.Hours())
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%.1f days", d.Hours()/24)
	case d < 2*365*24*time.Hour:
		return fmt.Sprintf("%.1f months", d.Hours()/24/30.4)
	default:
		return fmt.Sprintf("%.0f years", d.Hours()/24/365)
	}
}
