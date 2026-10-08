package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustParse(t *testing.T, y string) *Config {
	t.Helper()
	cfg, err := Parse([]byte(y))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return cfg
}

func parseErr(t *testing.T, y string) string {
	t.Helper()
	_, err := Parse([]byte(y))
	if err == nil {
		t.Fatalf("expected error for:\n%s", y)
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	return err.Error()
}

func TestParseMultipleServices(t *testing.T) {
	cfg := mustParse(t, `
services:
  web:
    prefix: WEB
    target: http://frontend:3000
  api:
    prefix: api
    target: http://backend:8000
  company:
    target: https://company.com
`)
	svcs := cfg.SortedServices()
	if len(svcs) != 3 || svcs[0].Name != "api" || svcs[1].Name != "company" || svcs[2].Name != "web" {
		t.Fatalf("unexpected services: %+v", svcs)
	}
	if svcs[2].Prefix != "web" {
		t.Errorf("prefix not lowercased: %q", svcs[2].Prefix)
	}
	if got := svcs[0].EffectiveHostHeader(); got != HostPreserve {
		t.Errorf("docker target host header = %q, want preserve", got)
	}
	if got := svcs[1].EffectiveHostHeader(); got != HostUpstream {
		t.Errorf("https target host header = %q, want upstream", got)
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct{ name, yaml, want string }{
		{"empty", ``, "empty"},
		{"no services", `services: {}`, "no services"},
		{"syntax", "services:\n  api:\n target: x", "YAML"},
		{"unknown field", "services:\n  api:\n    target: http://a:1\n    tagret: x", "tagret"},
		{"unknown top-level", "servics:\n  api:\n    target: http://a:1", "servics"},
		{"duplicate service", "services:\n  api:\n    target: http://a:1\n  api:\n    target: http://b:1", "already defined"},
		{"duplicate prefix", "services:\n  a:\n    prefix: web\n    target: http://a:1\n  b:\n    prefix: WEB\n    target: http://b:1", "same prefix"},
		{"missing target", "services:\n  api:\n    prefix: api", "missing required field 'target'"},
		{"null service", "services:\n  api:", "no settings"},
		{"bad scheme", "services:\n  radiolens:\n    target: ftp://example.com", "Service 'radiolens' has invalid target:\n         ftp://example.com"},
		{"bad prefix", "services:\n  api:\n    prefix: api1\n    target: http://a:1", "a-z and 2-7"},
		{"bad name", "services:\n  Api:\n    target: http://a:1", "invalid service name"},
		{"private ip", "services:\n  api:\n    target: http://10.1.2.3", "private"},
		{"tls on http", "services:\n  api:\n    target: http://a:1\n    tls:\n      insecure_skip_verify: true", "only apply to https"},
		{"tls conflict", "services:\n  api:\n    target: https://a:1\n    tls:\n      insecure_skip_verify: true\n      ca_file: /ca.pem", "mutually exclusive"},
		{"bad host header", "services:\n  api:\n    target: http://a:1\n    host_header: '{host}'", "host_header"},
		{"multi doc", "services:\n  api:\n    target: http://a:1\n---\nservices: {}", "multiple documents"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if msg := parseErr(t, tt.yaml); !strings.Contains(msg, tt.want) {
				t.Errorf("error %q does not contain %q", msg, tt.want)
			}
		})
	}
}

func TestAllErrorsReported(t *testing.T) {
	msg := parseErr(t, "services:\n  a:\n    target: ftp://x\n  b:\n    prefix: '0'\n    target: http://b:1")
	if strings.Count(msg, "ERROR:") != 2 {
		t.Errorf("expected 2 errors, got:\n%s", msg)
	}
}

func TestSecurityPolicy(t *testing.T) {
	mustParse(t, `
security:
  allow_private_targets: true
  allow_loopback_targets: true
  allow_link_local_targets: true
services:
  a:
    target: http://192.168.1.50:8080
  b:
    target: http://127.0.0.1:9000
  c:
    target: http://169.254.1.1
`)
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(filepath.Join(dir, "missing.yml")); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing file error = %v", err)
	}
	p := filepath.Join(dir, "onionforge.yml")
	os.WriteFile(p, []byte("services:\n  api:\n    target: ftp://x\n"), 0o644)
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), p) {
		t.Errorf("error should mention path: %v", err)
	}
}

func TestExampleConfig(t *testing.T) {
	if _, err := Load("../../onionforge.example.yml"); err != nil {
		t.Fatal(err)
	}
}
