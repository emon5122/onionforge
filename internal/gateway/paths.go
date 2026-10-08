// Package gateway runs an OnionForge instance: it provisions identities,
// generates Tor and Caddy configuration, supervises both processes, and
// implements reload, health checking and graceful shutdown.
package gateway

import (
	"os"
	"path/filepath"

	"github.com/emon5122/onionforge/internal/config"
	"github.com/emon5122/onionforge/internal/identity"
)

// Listener is where Tor delivers onion traffic and Caddy listens. It is
// loopback-only: nothing is published outside the container.
const (
	ListenHost = "127.0.0.1"
	ListenPort = 8080
)

// Paths groups every filesystem location and binary OnionForge uses. Values
// default to the container layout and can be overridden through environment
// variables for development and tests.
type Paths struct {
	Config    string // onionforge.yml (source of truth)
	DataDir   string // persistent volume: identities + Tor state
	RunDir    string // disposable generated files, sockets, state
	User      string // unprivileged user to drop to when started as root
	TorBin    string
	CaddyBin  string
	VanityBin string
}

// DefaultPaths returns the container defaults, honoring ONIONFORGE_* overrides.
func DefaultPaths() Paths {
	return Paths{
		Config:    env("ONIONFORGE_CONFIG", config.DefaultPath),
		DataDir:   env("ONIONFORGE_DATA_DIR", "/var/lib/tor"),
		RunDir:    env("ONIONFORGE_RUN_DIR", "/run/onionforge"),
		User:      env("ONIONFORGE_USER", "onionforge"),
		TorBin:    env("ONIONFORGE_TOR_BIN", "tor"),
		CaddyBin:  env("ONIONFORGE_CADDY_BIN", "caddy"),
		VanityBin: env("ONIONFORGE_VANITY_BIN", identity.DefaultVanityBinary),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// TorDataDir is Tor's own DataDirectory. It lives in the volume (so guard
// state persists) under a dot-name that can never collide with a service
// name.
func (p Paths) TorDataDir() string         { return filepath.Join(p.DataDir, ".tor") }
func (p Paths) Torrc() string              { return filepath.Join(p.RunDir, "torrc") }
func (p Paths) Caddyfile() string          { return filepath.Join(p.RunDir, "Caddyfile") }
func (p Paths) AdminSocket() string        { return filepath.Join(p.RunDir, "caddy-admin.sock") }
func (p Paths) StateFile() string          { return filepath.Join(p.RunDir, "state.json") }
func (p Paths) CaddyHome() string          { return filepath.Join(p.RunDir, "caddy") }
func (p Paths) ServiceDir(n string) string { return identity.Dir(p.DataDir, n) }
