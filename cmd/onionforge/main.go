// Command onionforge publishes HTTP services as persistent Tor v3 onion
// services. It is the container entrypoint.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/emon5122/onionforge/internal/config"
	"github.com/emon5122/onionforge/internal/gateway"
)

var version = "dev"

const usage = `OnionForge — publish HTTP services as persistent Tor onion services.

Usage:
  onionforge [run]          Start the gateway (default)
  onionforge validate       Validate the configuration and exit
  onionforge list           List onion identities and their status
  onionforge healthcheck    Exit 0 if the running gateway is healthy
  onionforge version        Print the version

Options:
  -config PATH              Configuration file (default $ONIONFORGE_CONFIG or
                            /etc/onionforge/onionforge.yml)

Signals (to the running gateway):
  SIGHUP                    Reload onionforge.yml (identities are kept)
  SIGTERM, SIGINT           Graceful shutdown
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	cmd := "run"
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	paths := gateway.DefaultPaths()
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	fs.StringVar(&paths.Config, "config", paths.Config, "configuration file")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	switch cmd {
	case "run":
		err := gateway.Run(paths, gateway.NewLogger(), version)
		if err == nil {
			return 0
		}
		var ve *config.ValidationError
		if errors.As(err, &ve) {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 2
		}
		fmt.Fprintf(os.Stderr, "\n[onionforge] ERROR: %v\n", err)
		var ee *gateway.ExitError
		if errors.As(err, &ee) {
			return ee.Code
		}
		return 1
	case "validate":
		cfg, err := config.Load(paths.Config)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		fmt.Printf("Configuration %s is valid (%d service(s)):\n", cfg.Path, len(cfg.Services))
		for _, s := range cfg.SortedServices() {
			prefix := s.Prefix
			if prefix == "" {
				prefix = "(random)"
			}
			fmt.Printf("  %-20s prefix=%-12s target=%s host_header=%s\n", s.Name, prefix, s.Target, s.EffectiveHostHeader())
		}
		return 0
	case "list", "status":
		if err := gateway.List(paths, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "ERROR:", err)
			return 1
		}
		return 0
	case "healthcheck":
		if err := gateway.Healthcheck(paths); err != nil {
			fmt.Fprintln(os.Stderr, "unhealthy:", err)
			return 1
		}
		fmt.Println("healthy")
		return 0
	case "version":
		fmt.Println(version)
		return 0
	case "help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}
