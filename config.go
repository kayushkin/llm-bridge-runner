package main

import (
	"fmt"
	"os"
	"os/user"
	"runtime"
)

// Config is the runtime configuration for an llm-bridge-runner process.
// Resolved from flags + env vars at startup; immutable thereafter.
type Config struct {
	ServerURL   string // e.g. "wss://bridge.example.com" — the runner appends /api/runner/ws
	Token       string // bearer token, presented in Hello
	MachineName string // user-chosen label, persisted server-side as the machine identity
	WorkingDir  string // default cwd for spawned subprocesses
	Hostname    string // os.Hostname(), reported in Hello
	OS          string // runtime.GOOS
	Arch        string // runtime.GOARCH
	User        string // os/user.Current().Username
}

// LoadConfig fills a Config from process state (hostname, user, …) and the
// caller-provided fields. Validates required fields are non-empty.
func LoadConfig(serverURL, token, machineName, workingDir string) (*Config, error) {
	if serverURL == "" {
		return nil, fmt.Errorf("server URL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if machineName == "" {
		return nil, fmt.Errorf("machine name is required")
	}

	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("hostname: %w", err)
	}

	u, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("user: %w", err)
	}

	if workingDir == "" {
		workingDir = u.HomeDir
	}

	return &Config{
		ServerURL:   serverURL,
		Token:       token,
		MachineName: machineName,
		WorkingDir:  workingDir,
		Hostname:    hostname,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		User:        u.Username,
	}, nil
}
