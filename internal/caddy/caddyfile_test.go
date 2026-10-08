package caddy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emon5122/onionforge/internal/config"
)

const (
	hostA = "apiaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaad.onion"
	hostB = "extbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbd.onion"
	hostC = "intcccccccccccccccccccccccccccccccccccccccccccccccccccccd.onion"
)

func testRoutes(t *testing.T) []Route {
	t.Helper()
	cfg, err := config.Parse([]byte(`
services:
  api:
    target: http://backend:8000
  ext:
    target: https://example.com/app/
    rewrite_redirects: true
  int:
    target: https://backend:8443
    host_header: backend.internal
    tls:
      ca_file: /etc/onionforge/ca.pem
      server_name: backend.internal
`))
	if err != nil {
		t.Fatal(err)
	}
	hosts := map[string]string{"api": hostA, "ext": hostB, "int": hostC}
	var routes []Route
	for _, s := range cfg.SortedServices() {
		routes = append(routes, Route{Hostname: hosts[s.Name], Service: s})
	}
	return routes
}

func generate(t *testing.T) string {
	return Generate(Options{ListenHost: "127.0.0.1", ListenPort: 8080, AdminSocket: "/run/onionforge/caddy-admin.sock", ConfigSource: "test"}, testRoutes(t))
}

func TestGenerate(t *testing.T) {
	got := generate(t)
	for _, want := range []string{
		"admin unix//run/onionforge/caddy-admin.sock",
		"auto_https off",
		"http_port 8080",
		"default_bind 127.0.0.1",
		"http://" + hostA + " {\n\treverse_proxy http://backend:8000 {\n\t}\n}",
		"http://" + hostB + " {\n\trewrite * /app{uri}\n\treverse_proxy https://example.com:443 {\n\t\theader_up Host {upstream_hostport}\n",
		"header_down Location `(?i)^https?://example\\.com(?::\\d+)?/app(/|$)` `http://{http.request.host}/`",
		"header_up Host backend.internal",
		"tls_server_name backend.internal",
		"tls_trust_pool file /etc/onionforge/ca.pem",
		"respond \"Unknown onion service\" 421",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Caddyfile missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "tls_insecure_skip_verify") {
		t.Error("TLS verification must not be disabled unless configured")
	}
}

// No upstream address may contain a placeholder: destinations come only
// from static configuration (open-proxy prevention).
func TestUpstreamsAreStatic(t *testing.T) {
	for _, line := range strings.Split(generate(t), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "reverse_proxy") && strings.Contains(line, "{") && !strings.HasSuffix(line, " {") {
			t.Errorf("dynamic upstream: %q", line)
		}
		if strings.HasPrefix(line, "reverse_proxy") && strings.Count(line, "{") > 1 {
			t.Errorf("dynamic upstream: %q", line)
		}
	}
}

// TestCaddyValidate runs the real caddy binary when available.
func TestCaddyValidate(t *testing.T) {
	bin, err := exec.LookPath("caddy")
	if err != nil {
		t.Skip("caddy not installed")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "Caddyfile")
	content := strings.ReplaceAll(generate(t), "/run/onionforge/caddy-admin.sock", filepath.Join(dir, "admin.sock"))
	content = strings.ReplaceAll(content, "tls_trust_pool file /etc/onionforge/ca.pem", "tls_insecure_skip_verify")
	os.WriteFile(p, []byte(content), 0o600)
	out, err := exec.Command(bin, "validate", "--config", p, "--adapter", "caddyfile").CombinedOutput()
	if err != nil {
		t.Fatalf("caddy validate: %v\n%s", err, out)
	}
	fmtOut, _ := exec.Command(bin, "fmt", p).Output()
	if string(fmtOut) != content {
		t.Errorf("generated Caddyfile is not caddy-fmt formatted")
	}
}
