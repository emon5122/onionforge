// Package validation contains the low-level checks applied to values from
// onionforge.yml: service names, vanity prefixes, upstream targets and the
// SSRF policy for target addresses.
//
// Every value that ends up in a generated torrc or Caddyfile passes through
// this package first, so the character sets accepted here are deliberately
// narrow: nothing user-supplied may be able to inject Caddyfile or torrc
// syntax.
package validation

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// OnionBase32Charset is the alphabet used by v3 onion addresses.
const OnionBase32Charset = "abcdefghijklmnopqrstuvwxyz234567"

// MaxPrefixLength is the longest vanity prefix OnionForge accepts. Every extra
// character multiplies the expected search time by 32; 10 characters is
// already measured in years on a single machine.
const MaxPrefixLength = 10

var (
	serviceNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	// RFC 1123 labels; underscores are tolerated because Docker Compose
	// service and container names commonly contain them.
	hostLabelRe = regexp.MustCompile(`^[A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?$`)
	// Conservative URL path character set: unreserved, sub-delims that are
	// safe in a Caddyfile token, ':' '@' and percent escapes.
	pathRe = regexp.MustCompile(`^(/[A-Za-z0-9._~!$&*+,;=:@%-]*)*$`)
	// Absolute file path without whitespace, quotes or braces.
	filePathRe = regexp.MustCompile(`^/[A-Za-z0-9._/+@-]+$`)
)

// reservedNames are directory names inside the data directory that are not
// available as service names.
var reservedNames = map[string]bool{
	"lost-found": true,
}

// ServiceName validates a logical service name. The name doubles as the
// on-disk identity directory, so it is restricted to a filesystem- and
// torrc-safe subset.
func ServiceName(name string) error {
	if !serviceNameRe.MatchString(name) {
		return fmt.Errorf("invalid service name %q: use 1-63 lowercase letters, digits, '-' or '_', starting with a letter or digit", name)
	}
	if reservedNames[name] {
		return fmt.Errorf("service name %q is reserved", name)
	}
	return nil
}

// Prefix validates a vanity prefix (already lowercased by the caller).
func Prefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if bad := strings.Trim(prefix, OnionBase32Charset); bad != "" {
		return fmt.Errorf("invalid prefix %q: onion addresses only contain the characters a-z and 2-7 (found %q)", prefix, invalidChars(prefix))
	}
	if len(prefix) > MaxPrefixLength {
		return fmt.Errorf("prefix %q is too long (%d characters, maximum %d): each character multiplies generation time by 32", prefix, len(prefix), MaxPrefixLength)
	}
	return nil
}

func invalidChars(s string) string {
	var b strings.Builder
	for _, r := range s {
		if !strings.ContainsRune(OnionBase32Charset, r) && !strings.ContainsRune(b.String(), r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Policy is the SSRF policy applied to target hosts.
type Policy struct {
	AllowPrivate   bool
	AllowLoopback  bool
	AllowLinkLocal bool
}

// HostKind classifies a target host.
type HostKind int

const (
	// HostName is a DNS name (Docker service name or public name).
	HostName HostKind = iota
	// HostIP is an IP literal.
	HostIP
)

// Target is a parsed and validated upstream target.
type Target struct {
	Scheme   string // "http" or "https"
	Host     string // hostname or IP, without brackets
	Port     int    // explicit port, or the scheme default
	BasePath string // path prefix with trailing slash removed; "" for none
	Kind     HostKind
	// External reports whether the target looks like a public/FQDN host
	// rather than a single-label Docker service name.
	External bool
}

// HostPort returns host:port suitable for a Caddy upstream address.
func (t Target) HostPort() string {
	return net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
}

// URL returns the normalized upstream URL without base path.
func (t Target) URL() string {
	return t.Scheme + "://" + t.HostPort()
}

// ErrUnsupportedScheme is returned for targets that are not http or https.
var ErrUnsupportedScheme = errors.New("unsupported scheme")

// ParseTarget validates raw against the SSRF policy and returns the parsed
// target. Error messages describe the problem without repeating raw; callers
// add the service name and target for context.
func ParseTarget(raw string, policy Policy) (Target, error) {
	var t Target
	if strings.TrimSpace(raw) == "" {
		return t, errors.New("target is required")
	}
	if strings.ContainsAny(raw, " \t\r\n\"'`{}\\") {
		return t, errors.New("contains whitespace, quotes, braces or backslashes")
	}
	u, err := url.Parse(raw)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return t, fmt.Errorf("not a valid URL: %v", err)
	}
	switch u.Scheme {
	case "http", "https":
	case "":
		return t, fmt.Errorf("missing scheme\n\n%s", supportedSchemes)
	default:
		return t, fmt.Errorf("%w %q\n\n%s", ErrUnsupportedScheme, u.Scheme+"://", supportedSchemes)
	}
	t.Scheme = u.Scheme
	if u.Opaque != "" || u.Host == "" {
		return t, errors.New("missing host")
	}
	if u.User != nil {
		return t, errors.New("must not contain credentials")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return t, errors.New("must not contain a query string")
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return t, errors.New("must not contain a fragment")
	}

	host := u.Hostname()
	if host == "" {
		return t, errors.New("missing host")
	}
	if strings.Contains(host, "%") {
		return t, errors.New("IPv6 zone identifiers are not supported")
	}
	t.Host = host

	if p := u.Port(); p != "" {
		port, err := strconv.Atoi(p)
		if err != nil || port < 1 || port > 65535 {
			return t, fmt.Errorf("invalid port %q", p)
		}
		t.Port = port
	} else if t.Scheme == "https" {
		t.Port = 443
	} else {
		t.Port = 80
	}

	path := u.EscapedPath()
	if path != "" && path != "/" {
		if !pathRe.MatchString(path) {
			return t, errors.New("path contains unsupported characters")
		}
		for _, seg := range strings.Split(path, "/") {
			if seg == "." || seg == ".." {
				return t, errors.New("path must not contain '.' or '..' segments")
			}
		}
		t.BasePath = strings.TrimRight(path, "/")
	}

	if addr, err := netip.ParseAddr(host); err == nil {
		t.Kind = HostIP
		return t, checkAddr(addr.Unmap(), policy)
	}

	t.Kind = HostName
	if err := checkHostname(host, policy); err != nil {
		return t, err
	}
	t.Host = strings.ToLower(strings.TrimSuffix(host, "."))
	t.External = strings.Contains(t.Host, ".")
	return t, nil
}

const supportedSchemes = "Supported target schemes:\n  http://\n  https://"

func checkHostname(host string, policy Policy) error {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if len(h) > 253 {
		return errors.New("hostname is too long")
	}
	for _, label := range strings.Split(h, ".") {
		if !hostLabelRe.MatchString(label) {
			return fmt.Errorf("invalid hostname %q", host)
		}
	}
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		if !policy.AllowLoopback {
			return errors.New("loopback targets are not allowed (set security.allow_loopback_targets: true to permit them)")
		}
	}
	if strings.HasSuffix(h, ".onion") {
		return errors.New(".onion targets are not supported: OnionForge publishes services to Tor, it does not proxy into Tor")
	}
	// A purely numeric "hostname" (e.g. 2130706433) is resolved as an IPv4
	// address by some resolvers; refuse it rather than guess.
	if strings.Trim(h, "0123456789.") == "" {
		return fmt.Errorf("invalid hostname %q", host)
	}
	return nil
}

func checkAddr(addr netip.Addr, policy Policy) error {
	switch {
	case addr.IsUnspecified():
		return errors.New("unspecified addresses (0.0.0.0, ::) are not valid targets")
	case addr.IsMulticast():
		return errors.New("multicast addresses are not valid targets")
	case addr.IsLoopback():
		if !policy.AllowLoopback {
			return errors.New("loopback targets are not allowed (set security.allow_loopback_targets: true to permit them)")
		}
	case addr.IsLinkLocalUnicast():
		if !policy.AllowLinkLocal {
			return errors.New("link-local targets (169.254.0.0/16, fe80::/10) are not allowed (set security.allow_link_local_targets: true to permit them)")
		}
	case addr.IsPrivate() || isSharedOrReserved(addr):
		if !policy.AllowPrivate {
			return errors.New("private IP targets are not allowed (use a Docker service name, or set security.allow_private_targets: true)")
		}
	}
	return nil
}

var extraPrivate = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),  // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),   // reserved
}

func isSharedOrReserved(addr netip.Addr) bool {
	for _, p := range extraPrivate {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// HostHeader validates a host_header value: "preserve", "upstream" or a
// literal host[:port].
func HostHeader(v string) error {
	switch v {
	case "", "preserve", "upstream":
		return nil
	}
	host := v
	if h, p, err := net.SplitHostPort(v); err == nil {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid host_header %q", v)
		}
		host = h
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if !hostLabelRe.MatchString(label) {
			return fmt.Errorf("invalid host_header %q: use \"preserve\", \"upstream\" or a hostname", v)
		}
	}
	return nil
}

// ServerName validates a TLS server name override.
func ServerName(v string) error {
	if v == "" {
		return nil
	}
	if _, err := netip.ParseAddr(v); err == nil {
		return nil
	}
	for _, label := range strings.Split(v, ".") {
		if !hostLabelRe.MatchString(label) {
			return fmt.Errorf("invalid tls.server_name %q", v)
		}
	}
	return nil
}

// FilePath validates an absolute file path used in generated configuration.
func FilePath(v string) error {
	if !filePathRe.MatchString(v) || strings.Contains(v, "/../") || strings.HasSuffix(v, "/..") {
		return fmt.Errorf("invalid path %q: must be an absolute path without spaces, quotes or '..'", v)
	}
	return nil
}
