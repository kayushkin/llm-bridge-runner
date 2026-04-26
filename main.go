// llm-bridge-runner is a long-lived daemon that registers a remote machine
// with an llm-bridge-server and accepts harness-spawn requests over a
// single outbound WebSocket. It enables the server to drive harness
// subprocesses on machines behind NAT without inbound SSH or a VPN.
//
// Usage:
//
//	llm-bridge-runner -server https://bridge.example.com -token TOKEN -name laptop
//
// All flags can also be supplied via env vars: LLMBRIDGE_SERVER, LLMBRIDGE_TOKEN,
// LLMBRIDGE_MACHINE_NAME, LLMBRIDGE_WORKING_DIR.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	var (
		serverURL   = flag.String("server", envOr("LLMBRIDGE_SERVER", ""), "llm-bridge-server base URL (http/https/ws/wss)")
		token       = flag.String("token", envOr("LLMBRIDGE_TOKEN", ""), "bearer token for runner auth")
		machineName = flag.String("name", envOr("LLMBRIDGE_MACHINE_NAME", ""), "human label for this machine, e.g. wsl-laptop")
		workingDir  = flag.String("workdir", envOr("LLMBRIDGE_WORKING_DIR", ""), "default cwd for spawned harness subprocesses (defaults to $HOME)")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(Version)
		return
	}

	cfg, err := LoadConfig(*serverURL, *token, *machineName, *workingDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		flag.Usage()
		os.Exit(2)
	}

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("[runner] starting version=%s machine=%s server=%s", Version, cfg.MachineName, cfg.ServerURL)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	client := NewClient(cfg)
	client.Run(ctx)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
