// Package config loads and validates onionforge.yml, the single source of
// truth for an OnionForge instance.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/emon5122/onionforge/internal/validation"
)

// DefaultPath is where the container expects the configuration file.
const DefaultPath = "/etc/onionforge/onionforge.yml"

// Host header modes.
const (
	HostPreserve = "preserve"
	HostUpstream = "upstream"
)

// Config is the parsed onionforge.yml.
type Config struct {
	Security Security            `yaml:"security"`
	Logging  Logging             `yaml:"logging"`
	Vanity   Vanity              `yaml:"vanity"`
	Services map[string]*Service `yaml:"services"`

	// Path is the file the configuration was read from.
	Path string `yaml:"-"`
}

// Security holds the SSRF policy for targets.
type Security struct {
	AllowPrivateTargets   bool `yaml:"allow_private_targets"`
	AllowLoopbackTargets  bool `yaml:"allow_loopback_targets"`
	AllowLinkLocalTargets bool `yaml:"allow_link_local_targets"`
}

// Logging controls Caddy request logging.
type Logging struct {
	// AccessLog enables one log line per proxied request.
	AccessLog bool `yaml:"access_log"`
}

// Vanity background modes.
const (
	VanityAuto   = "auto"
	VanityAlways = "always"
	VanityNever  = "never"
)

// BackgroundThreshold is the shortest prefix length that the "auto" mode
// searches in the background (7 characters take minutes, 8 hours, 9 days).
const BackgroundThreshold = 7

// Vanity controls vanity address generation.
type Vanity struct {
	// Threads limits the CPU threads used by the search; 0 uses all CPUs.
	Threads int `yaml:"threads"`
	// Background selects whether searches block startup: "auto" (default)
	// runs searches for prefixes of 7+ characters in the background while
	// other services start, "always" never blocks, "never" always blocks.
	Background string `yaml:"background"`
}

// Prefixes is one vanity prefix or a list of alternatives. In YAML it
// accepts either a string or a sequence of strings; the search stops at the
// first address matching any of them.
type Prefixes []string

// UnmarshalYAML accepts a scalar or a sequence.
func (p *Prefixes) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag == "!!null" || n.Value == "" {
			*p = nil
			return nil
		}
		*p = Prefixes{n.Value}
		return nil
	case yaml.SequenceNode:
		var list []string
		if err := n.Decode(&list); err != nil {
			return err
		}
		*p = list
		return nil
	}
	return fmt.Errorf("line %d: prefix must be a string or a list of strings", n.Line)
}

// Shortest returns the length of the shortest prefix.
func (p Prefixes) Shortest() int {
	n := 0
	for i, s := range p {
		if i == 0 || len(s) < n {
			n = len(s)
		}
	}
	return n
}

// Matches reports whether an onion hostname starts with any prefix.
func (p Prefixes) Matches(hostname string) bool {
	for _, s := range p {
		if strings.HasPrefix(hostname, s) {
			return true
		}
	}
	return len(p) == 0
}

// String renders the prefixes for logs.
func (p Prefixes) String() string { return strings.Join(p, " | ") }

// Service maps one onion identity to one upstream.
type Service struct {
	Prefix Prefixes `yaml:"prefix"`
	Target string   `yaml:"target"`
	// HostHeader is "preserve" (send the .onion host), "upstream" (send the
	// target's host) or a literal host. Empty selects a default based on the
	// target; see EffectiveHostHeader.
	HostHeader string `yaml:"host_header"`
	// RewriteRedirects rewrites Location response headers that point at the
	// target's own origin so they point at the onion address instead.
	RewriteRedirects bool `yaml:"rewrite_redirects"`
	TLS              TLS  `yaml:"tls"`

	Name     string            `yaml:"-"`
	Upstream validation.Target `yaml:"-"`
}

// TLS configures the connection to an https:// target.
type TLS struct {
	// InsecureSkipVerify disables upstream certificate verification.
	InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
	// ServerName overrides the SNI / verification name.
	ServerName string `yaml:"server_name"`
	// CAFile is a PEM bundle (inside the container) trusted for this target
	// in addition to nothing else: when set, only these CAs are trusted.
	CAFile string `yaml:"ca_file"`
}

func (t TLS) isZero() bool { return t == TLS{} }

// EffectiveHostHeader resolves the Host header mode for the service.
//
// Single-label Docker service names (http://backend:8000) and IP targets
// receive the original .onion Host header, so applications that build
// absolute URLs from Host produce onion URLs. FQDN and https:// targets
// receive their own hostname, which virtual hosting, CDNs and TLS need.
func (s *Service) EffectiveHostHeader() string {
	if s.HostHeader != "" {
		return s.HostHeader
	}
	if s.Upstream.Scheme == "https" || s.Upstream.External {
		return HostUpstream
	}
	return HostPreserve
}

// Background reports whether a missing identity for svc is generated in
// the background rather than before startup.
func (c *Config) Background(svc *Service) bool {
	if len(svc.Prefix) == 0 {
		return false
	}
	switch c.Vanity.Background {
	case VanityAlways:
		return true
	case VanityNever:
		return false
	}
	return svc.Prefix.Shortest() >= BackgroundThreshold
}

// SortedServices returns services ordered by name.
func (c *Config) SortedServices() []*Service {
	out := make([]*Service, 0, len(c.Services))
	for _, s := range c.Services {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Policy returns the SSRF policy derived from the security section.
func (c *Config) Policy() validation.Policy {
	return validation.Policy{
		AllowPrivate:   c.Security.AllowPrivateTargets,
		AllowLoopback:  c.Security.AllowLoopbackTargets,
		AllowLinkLocal: c.Security.AllowLinkLocalTargets,
	}
}

// ValidationError aggregates every problem found in a configuration file.
type ValidationError struct {
	Path     string
	Problems []string
}

func (e *ValidationError) Error() string {
	var b strings.Builder
	if e.Path != "" {
		fmt.Fprintf(&b, "Invalid configuration %s:\n", e.Path)
	} else {
		b.WriteString("Invalid configuration:\n")
	}
	for i, p := range e.Problems {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("\nERROR: ")
		for j, line := range strings.Split(p, "\n") {
			if j > 0 {
				b.WriteString("\n")
				if line != "" {
					b.WriteString("       ")
				}
			}
			b.WriteString(line)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Load reads and validates the configuration at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("configuration file %s not found: mount your onionforge.yml there (or set ONIONFORGE_CONFIG)", path)
		}
		return nil, fmt.Errorf("reading configuration: %w", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			ve.Path = path
		}
		return nil, err
	}
	cfg.Path = path
	return cfg, nil
}

// Parse parses and validates configuration bytes.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, &ValidationError{Problems: []string{"configuration is empty: define at least one service under 'services:'"}}
		}
		return nil, &ValidationError{Problems: []string{"YAML: " + strings.TrimPrefix(err.Error(), "yaml: ")}}
	}
	// Reject multi-document files: a second document would be silently ignored.
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, &ValidationError{Problems: []string{"YAML: multiple documents found; onionforge.yml must contain a single document"}}
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if len(c.Services) == 0 {
		add("no services defined: add at least one entry under 'services:'")
	}

	names := make([]string, 0, len(c.Services))
	for name := range c.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	prefixes := map[string]string{}
	for _, name := range names {
		svc := c.Services[name]
		if err := validation.ServiceName(name); err != nil {
			add("%v", err)
			continue
		}
		if svc == nil {
			add("Service '%s' has no settings: 'target' is required", name)
			continue
		}
		svc.Name = name

		if len(svc.Prefix) > 0 {
			seen := map[string]bool{}
			for i, raw := range svc.Prefix {
				lower := strings.ToLower(strings.TrimSpace(raw))
				if lower == "" {
					add("Service '%s': empty prefix", name)
					continue
				}
				if err := validation.Prefix(lower); err != nil {
					add("Service '%s': %v", name, err)
					continue
				}
				svc.Prefix[i] = lower
				if seen[lower] {
					continue
				}
				seen[lower] = true
				if other, dup := prefixes[lower]; dup {
					add("Services '%s' and '%s' request the same prefix %q: prefixes must be unique", other, name, lower)
				}
				prefixes[lower] = name
			}
		}

		if strings.TrimSpace(svc.Target) == "" {
			add("Service '%s' is missing required field 'target'", name)
		} else if t, err := validation.ParseTarget(svc.Target, c.Policy()); err != nil {
			add("Service '%s' has invalid target:\n  %s\n\n%v", name, svc.Target, err)
		} else {
			svc.Upstream = t
		}

		if err := validation.HostHeader(svc.HostHeader); err != nil {
			add("Service '%s': %v", name, err)
		}

		if !svc.TLS.isZero() && svc.Upstream.Scheme == "http" {
			add("Service '%s': 'tls' settings only apply to https:// targets", name)
		}
		if err := validation.ServerName(svc.TLS.ServerName); err != nil {
			add("Service '%s': %v", name, err)
		}
		if svc.TLS.CAFile != "" {
			if err := validation.FilePath(svc.TLS.CAFile); err != nil {
				add("Service '%s': tls.ca_file: %v", name, err)
			}
		}
		if svc.TLS.InsecureSkipVerify && svc.TLS.CAFile != "" {
			add("Service '%s': tls.insecure_skip_verify and tls.ca_file are mutually exclusive", name)
		}
	}

	switch c.Vanity.Background {
	case "", VanityAuto, VanityAlways, VanityNever:
	default:
		add("vanity.background must be \"auto\", \"always\" or \"never\" (got %q)", c.Vanity.Background)
	}
	if c.Vanity.Threads < 0 {
		add("vanity.threads must be 0 (all CPUs) or a positive number")
	}

	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}
