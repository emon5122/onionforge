package tor

import (
	"strings"
	"testing"
)

func TestGenerate(t *testing.T) {
	got := Generate(Options{DataDirectory: "/var/lib/tor/.tor", Listener: "127.0.0.1:8080", ConfigSource: "onionforge.yml"},
		[]Service{{Name: "api", Dir: "/var/lib/tor/api"}, {Name: "web", Dir: "/var/lib/tor/web"}})
	for _, want := range []string{
		"DataDirectory /var/lib/tor/.tor\n",
		"SocksPort 0\n",
		"HiddenServiceDir /var/lib/tor/api\nHiddenServiceVersion 3\nHiddenServicePort 80 127.0.0.1:8080\n",
		"HiddenServiceDir /var/lib/tor/web\nHiddenServiceVersion 3\nHiddenServicePort 80 127.0.0.1:8080\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("torrc missing %q:\n%s", want, got)
		}
	}
}
