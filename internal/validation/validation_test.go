package validation

import (
	"strings"
	"testing"
)

func TestParseTarget(t *testing.T) {
	strict := Policy{}
	tests := []struct {
		raw      string
		policy   Policy
		wantErr  string
		wantURL  string
		wantBase string
		external bool
	}{
		{raw: "http://backend:8000", wantURL: "http://backend:8000"},
		{raw: "http://backend", wantURL: "http://backend:80"},
		{raw: "https://example.com", wantURL: "https://example.com:443", external: true},
		{raw: "https://Example.COM./", wantURL: "https://example.com:443", external: true},
		{raw: "https://example.com/app/", wantURL: "https://example.com:443", wantBase: "/app", external: true},
		{raw: "http://my_backend:8000", wantURL: "http://my_backend:8000"},
		{raw: "http://8.8.8.8:8080", wantURL: "http://8.8.8.8:8080"},
		{raw: "http://[2001:db8::1]:80", wantURL: "http://[2001:db8::1]:80"},
		{raw: "http://192.168.1.50:8080", policy: Policy{AllowPrivate: true}, wantURL: "http://192.168.1.50:8080"},
		{raw: "http://127.0.0.1:9000", policy: Policy{AllowLoopback: true}, wantURL: "http://127.0.0.1:9000"},

		{raw: "", wantErr: "required"},
		{raw: "ftp://example.com", wantErr: "unsupported scheme \"ftp://\""},
		{raw: "backend:8000", wantErr: "unsupported scheme"},
		{raw: "//backend:8000", wantErr: "missing scheme"},
		{raw: "http://", wantErr: "missing host"},
		{raw: "http://user:pw@backend", wantErr: "credentials"},
		{raw: "http://backend/?url=x", wantErr: "query"},
		{raw: "http://backend/#x", wantErr: "fragment"},
		{raw: "http://backend:0", wantErr: "invalid port"},
		{raw: "http://backend:99999", wantErr: "invalid port"},
		{raw: "http://backend/{http.request.uri}", wantErr: "braces"},
		{raw: "http://backend/a b", wantErr: "whitespace"},
		{raw: "http://backend/../etc", wantErr: "'..'"},
		{raw: "http://127.0.0.1:8080", wantErr: "loopback"},
		{raw: "http://localhost:8080", wantErr: "loopback"},
		{raw: "http://[::1]:8080", wantErr: "loopback"},
		{raw: "http://[::ffff:127.0.0.1]:8080", wantErr: "loopback"},
		{raw: "http://169.254.169.254/latest", wantErr: "link-local"},
		{raw: "http://10.0.0.5", wantErr: "private"},
		{raw: "http://172.16.0.1", wantErr: "private"},
		{raw: "http://192.168.1.1", wantErr: "private"},
		{raw: "http://100.64.0.1", wantErr: "private"},
		{raw: "http://[fd00::1]", wantErr: "private"},
		{raw: "http://0.0.0.0", wantErr: "unspecified"},
		{raw: "http://224.0.0.1", wantErr: "multicast"},
		{raw: "http://2130706433", wantErr: "invalid hostname"},
		{raw: "http://abc.onion", wantErr: ".onion targets"},
		{raw: "http://bad..host", wantErr: "invalid hostname"},
		{raw: "http://-bad", wantErr: "invalid hostname"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			p := tt.policy
			if p == (Policy{}) {
				p = strict
			}
			got, err := ParseTarget(tt.raw, p)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseTarget(%q) error = %v, want containing %q", tt.raw, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTarget(%q) unexpected error: %v", tt.raw, err)
			}
			if got.URL() != tt.wantURL {
				t.Errorf("URL() = %q, want %q", got.URL(), tt.wantURL)
			}
			if got.BasePath != tt.wantBase {
				t.Errorf("BasePath = %q, want %q", got.BasePath, tt.wantBase)
			}
			if got.External != tt.external {
				t.Errorf("External = %v, want %v", got.External, tt.external)
			}
		})
	}
}

func TestPrefix(t *testing.T) {
	for _, ok := range []string{"", "a", "api", "radiolens", "abc234567"} {
		if err := Prefix(ok); err != nil {
			t.Errorf("Prefix(%q) unexpected error: %v", ok, err)
		}
	}
	for _, bad := range []string{"api1", "web0", "a-b", "a b", "abcdefghijk", "ab8"} {
		if err := Prefix(bad); err == nil {
			t.Errorf("Prefix(%q) expected error", bad)
		}
	}
}

func TestServiceName(t *testing.T) {
	for _, ok := range []string{"api", "my-app", "my_app", "a1", "0day"} {
		if err := ServiceName(ok); err != nil {
			t.Errorf("ServiceName(%q) unexpected error: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "API", ".tor", "-x", "a/b", "a b", "../x", strings.Repeat("a", 64), "lost-found"} {
		if err := ServiceName(bad); err == nil {
			t.Errorf("ServiceName(%q) expected error", bad)
		}
	}
}

func TestHostHeader(t *testing.T) {
	for _, ok := range []string{"", "preserve", "upstream", "example.com", "example.com:8443", "10.0.0.1"} {
		if err := HostHeader(ok); err != nil {
			t.Errorf("HostHeader(%q) unexpected error: %v", ok, err)
		}
	}
	for _, bad := range []string{"{host}", "a b", "x\ny", "example.com:0"} {
		if err := HostHeader(bad); err == nil {
			t.Errorf("HostHeader(%q) expected error", bad)
		}
	}
}

func TestFilePath(t *testing.T) {
	if err := FilePath("/etc/onionforge/ca.pem"); err != nil {
		t.Error(err)
	}
	for _, bad := range []string{"ca.pem", "/etc/a b.pem", "/etc/../x", "/x{y}"} {
		if err := FilePath(bad); err == nil {
			t.Errorf("FilePath(%q) expected error", bad)
		}
	}
}
