package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/kayushkin/llm-bridge/msg"
)

// WSL compaction from the runner's side: the Windows helper's package
// wslcompact does the work. Setting up shows the owner one UAC prompt, which
// can wait on them for minutes, so it runs apart from the bridge's request
// and its outcome shows in the status. Compacting shuts WSL down, this
// runner with it, so the runner answers first; the script waits ten seconds
// before it stops WSL, and records how it went for the runner to read once
// WSL is back.

// wslCompactionSetUpTimeout is how long the runner waits on the owner to
// answer the UAC prompt.
const wslCompactionSetUpTimeout = 15 * time.Minute

// wslCompactionSetUp is what the runner knows of its last UAC prompt.
type wslCompactionSetUp struct {
	running bool
	shownAt *time.Time
	err     string
}

func (d *DiskUsage) wslCompactionStatus(command string) (*msg.DiskUsageWSLCompaction, error) {
	ctx, cancel := context.WithTimeout(context.Background(), diskUsageAnswerTimeout)
	defer cancel()
	output, err := diskUsageHelper(ctx, command, "wsl-compaction-status").Output()
	if err != nil {
		return nil, fmt.Errorf("read WSL compaction's status: %w", helperError(err))
	}
	var status msg.DiskUsageWSLCompaction
	if err := json.Unmarshal(output, &status); err != nil {
		return nil, fmt.Errorf("read WSL compaction's status: %w", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	status.SetUpPromptShownAt = d.compactionSetUp.shownAt
	if d.compactionSetUp.err != "" {
		status.SetUpError = d.compactionSetUp.err
	}
	return &status, nil
}

func (d *DiskUsage) setUpWSLCompaction(command string) error {
	distribution, err := runnerDistribution()
	if err != nil {
		return err
	}
	d.mu.Lock()
	if d.compactionSetUp.running {
		d.mu.Unlock()
		return fmt.Errorf("the UAC prompt to set up WSL compaction is already waiting at the PC")
	}
	shownAt := time.Now().UTC()
	d.compactionSetUp = wslCompactionSetUp{running: true, shownAt: &shownAt}
	d.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), wslCompactionSetUpTimeout)
		defer cancel()
		_, err := diskUsageHelper(ctx, command, "wsl-compaction-set-up", distribution).Output()
		d.mu.Lock()
		d.compactionSetUp.running = false
		if err != nil {
			d.compactionSetUp.err = helperError(err).Error()
			log.Printf("[disk-usage] setting up WSL compaction: %s", d.compactionSetUp.err)
		}
		d.mu.Unlock()
	}()
	log.Printf("[disk-usage] showed the UAC prompt that sets up WSL compaction for %s", distribution)
	return nil
}

func (d *DiskUsage) startWSLCompaction(command string) error {
	d.mu.Lock()
	switch {
	case d.deletion != nil && d.deletion.State == msg.DiskUsageDeletionRunning:
		d.mu.Unlock()
		return fmt.Errorf("a deletion of %s is running; compacting would stop it halfway", d.deletion.Path)
	case d.scans[msg.DiskUsageWSLDrive] != nil && d.scans[msg.DiskUsageWSLDrive].state.State == msg.DiskUsageScanRunning:
		d.mu.Unlock()
		return fmt.Errorf("WSL is being scanned; wait for the scan to finish")
	}
	d.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), diskUsageAnswerTimeout)
	defer cancel()
	if _, err := diskUsageHelper(ctx, command, "wsl-compaction-start").Output(); err != nil {
		return fmt.Errorf("start WSL compaction: %w", helperError(err))
	}
	log.Printf("[disk-usage] started WSL compaction; WSL, and this runner, stop in about ten seconds")
	return nil
}
