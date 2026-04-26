package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kayushkin/llm-bridge/msg"
)

// fetchHarnessBinary downloads a wrapper binary for the given harness
// type from the bridge server, installs it under ~/.local/bin, and
// returns its absolute path. Used by the SubprocessRegistry when a Spawn
// arrives for a harness whose binary isn't on PATH yet — keeps "deploy
// any llm-bridge" a zero-touch operation on the remote.
func fetchHarnessBinary(serverURL string, harness msg.Harness) (string, error) {
	short := msg.HarnessShortName(harness)
	if short == "" {
		return "", fmt.Errorf("unknown harness type: %s", harness)
	}

	endpoint, err := buildBinaryURL(serverURL, short)
	if err != nil {
		return "", err
	}

	dest, err := harnessBinaryPath(harness)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return "", fmt.Errorf("create bin dir: %w", err)
	}

	httpClient := &http.Client{Timeout: 60 * time.Second}
	resp, err := httpClient.Get(endpoint)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("download %s: HTTP %d: %s", endpoint, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Write to a sibling tmp file then rename, so a crashed download
	// doesn't leave a half-written binary on PATH.
	tmp, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".part-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("download body: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("install %s: %w", dest, err)
	}
	return dest, nil
}

// harnessBinaryPath returns the canonical install path for a harness
// wrapper binary on this runner. ~/.local/bin so it doesn't need root
// and lands inside the user's PATH on most distros.
func harnessBinaryPath(harness msg.Harness) (string, error) {
	bin := msg.HarnessBinaryName(harness)
	if bin == "" {
		return "", fmt.Errorf("unknown harness type: %s", harness)
	}
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return filepath.Join(u.HomeDir, ".local", "bin", bin), nil
}

// buildBinaryURL composes the canonical /api/runner/binary URL for a
// short harness name (e.g. "claudecode") on this runner's GOOS/GOARCH.
func buildBinaryURL(serverURL, shortName string) (string, error) {
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
	u.Path = strings.TrimRight(u.Path, "/") + "/api/runner/binary"
	q := u.Query()
	q.Set("name", shortName)
	q.Set("os", runtime.GOOS)
	q.Set("arch", runtime.GOARCH)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
