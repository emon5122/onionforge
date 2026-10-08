package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Status values recorded in the state file.
const (
	StatusStarting = "starting"
	StatusReady    = "ready"
	StatusStopping = "stopping"
)

// State is the runtime state shared with `onionforge healthcheck`. It holds
// hostnames and targets only, never key material.
type State struct {
	Status          string         `json:"status"`
	PID             int            `json:"pid"`
	TorPID          int            `json:"tor_pid,omitempty"`
	CaddyPID        int            `json:"caddy_pid,omitempty"`
	TorBootstrapped bool           `json:"tor_bootstrapped"`
	Listener        string         `json:"listener"`
	AdminSocket     string         `json:"admin_socket"`
	Services        []ServiceState `json:"services"`
	Updated         time.Time      `json:"updated"`
}

// ServiceState describes one active onion service.
type ServiceState struct {
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
	Target   string `json:"target"`
	Dir      string `json:"dir"`
}

func writeState(path string, s *State) error {
	s.Updated = time.Now().UTC()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), ".state.json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadState loads the state file.
func ReadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
