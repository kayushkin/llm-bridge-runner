package main

import (
	"bytes"
	"context"
	"log"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kayushkin/llm-bridge/msg"
)

// syncBuffer is a log sink the test can poll while Run writes to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestRunKillsManagedServicesWhenCancelledDuringBackoff pins the shutdown
// contract at the one moment it used to be violated.
//
// Run has two return paths. The one taken when the context is cancelled while
// runOnce is in flight killed the subprocess registry AND the managed services.
// The one taken when the context is cancelled during the reconnect backoff
// returned without touching the services at all — so every managed daemon the
// runner had started outlived it. That is not an edge case: the backoff is
// exactly where the runner sits for as long as the server is unreachable, which
// is when an operator is most likely to stop it.
//
// The test asserts the property directly — the service process must actually be
// dead — rather than checking the shutdown log line, which is a proxy that a
// future refactor could keep printing while dropping the kill.
func TestRunKillsManagedServicesWhenCancelledDuringBackoff(t *testing.T) {
	logs := &syncBuffer{}
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
	})

	// A stand-in for a managed service: a child that will not exit on its own
	// inside the test's lifetime, so "still alive" is unambiguous.
	service := exec.Command("sleep", "300")
	if err := service.Start(); err != nil {
		t.Fatalf("start stand-in service: %v", err)
	}
	serviceExited := make(chan error, 1)
	go func() { serviceExited <- service.Wait() }()
	t.Cleanup(func() {
		if service.Process != nil {
			_ = service.Process.Kill()
		}
	})

	// Port 1 is reserved and nothing listens on it, so runOnce fails its dial
	// immediately and Run drops straight into the backoff we want to cancel in.
	client := NewClient(&Config{
		ServerURL:   "http://127.0.0.1:1",
		MachineID:   "mach_test",
		MachineName: "test-machine",
		RunnerToken: "tok_test",
	})

	// Register the service by hand rather than through Ensure: Ensure blocks up
	// to 30s waiting for a health probe to come up green, and startup is not
	// what is under test here — shutdown is.
	client.services.mu.Lock()
	client.services.services["stand-in"] = &managedService{
		manifest: msg.HarnessService{Name: "stand-in"},
		cmd:      service,
	}
	client.services.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	runReturned := make(chan struct{})
	go func() {
		client.Run(ctx)
		close(runReturned)
	}()

	// Cancel only once Run has reported the disconnect, which is the last thing
	// it logs before entering the backoff select. Waiting for the log rather
	// than sleeping a guessed interval is what makes this test exercise the
	// backoff path every time instead of most of the time.
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(logs.String(), "[runner] disconnected") {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("Run never reached the reconnect backoff; logs:\n%s", logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	select {
	case <-runReturned:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return within 10s of cancellation")
	}

	select {
	case <-serviceExited:
		// Killed, as it must be.
	case <-time.After(10 * time.Second):
		t.Fatal("Run returned but the managed service is STILL RUNNING: cancelling " +
			"during the reconnect backoff orphaned it. Every daemon the runner " +
			"started outlives the runner itself.")
	}

	if !strings.Contains(logs.String(), "shutting down") {
		t.Errorf("Run exited without logging a shutdown; logs:\n%s", logs.String())
	}
}
