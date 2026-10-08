package gateway

import (
	"testing"

	"github.com/emon5122/onionforge/internal/config"
)

func TestAssignKey(t *testing.T) {
	pending := []*config.Service{
		{Name: "a", Prefix: config.Prefixes{"ab"}},
		{Name: "b", Prefix: config.Prefixes{"abc", "zz"}},
		{Name: "c", Prefix: config.Prefixes{"qq"}},
	}
	for host, want := range map[string]string{
		"abcxyz.onion": "b", // matches a and b: longest prefix wins
		"abxyz.onion":  "a",
		"zzxyz.onion":  "b", // any alternative counts
		"qqxyz.onion":  "c",
		"xxxxx.onion":  "", // no match (stale result)
	} {
		if got := assignKey(host, pending); got != want {
			t.Errorf("assignKey(%s) = %q, want %q", host, got, want)
		}
	}
}
