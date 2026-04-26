package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"runtime"
	"strings"
	"time"

	"github.com/kayushkin/llm-bridge/msg"
)

// Enroll performs the one-time enrollment flow against an llm-bridge-server,
// trading a single-use passphrase for a durable per-machine runner token.
// On success it writes a SavedConfig to disk and returns it.
func Enroll(serverURL, passphrase, machineName string) (*SavedConfig, error) {
	if serverURL == "" {
		return nil, fmt.Errorf("server URL is required")
	}
	if passphrase == "" {
		return nil, fmt.Errorf("passphrase is required")
	}

	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("hostname: %w", err)
	}
	u, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("user: %w", err)
	}
	if machineName == "" {
		machineName = hostname
	}
	workdir := os.Getenv("LLMBRIDGE_WORKING_DIR")
	if workdir == "" {
		workdir = u.HomeDir
	}

	endpoint, err := buildEnrollURL(serverURL)
	if err != nil {
		return nil, err
	}

	req := msg.EnrollRunnerRequest{
		Passphrase:         passphrase,
		MachineName:        machineName,
		Hostname:           hostname,
		OS:                 runtime.GOOS,
		Arch:               runtime.GOARCH,
		User:               u.Username,
		WorkingDir:         workdir,
		AvailableHarnesses: DetectHarnesses(),
		RunnerVersion:      Version,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("enrollment rejected (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var er msg.EnrollRunnerResponse
	if err := json.Unmarshal(respBody, &er); err != nil {
		return nil, fmt.Errorf("parse enrollment response: %w (body=%q)", err, string(respBody))
	}
	if er.RunnerToken == "" || er.MachineID == "" {
		return nil, fmt.Errorf("enrollment response missing required fields: %s", string(respBody))
	}

	cfg := &SavedConfig{
		ServerURL:   serverURL,
		MachineID:   er.MachineID,
		MachineName: er.MachineName,
		RunnerToken: er.RunnerToken,
	}
	if err := SaveConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// buildEnrollURL appends /api/runner/enroll to the supplied server base.
// Accepts http/https/ws/wss schemes (ws/wss are auto-converted to their
// HTTP counterparts since enrollment is a regular HTTP POST).
func buildEnrollURL(serverURL string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(u.Scheme) {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	case "http", "https":
	default:
		return "", fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/runner/enroll"
	return u.String(), nil
}
