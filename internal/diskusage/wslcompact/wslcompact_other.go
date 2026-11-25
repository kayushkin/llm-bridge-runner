//go:build !windows

package wslcompact

import (
	"errors"

	"github.com/kayushkin/llm-bridge/msg"
)

var errOnlyOnWindows = errors.New("WSL compaction is set up and run only on Windows")

// SetUpCommand is the helper's command the elevated copy runs.
const SetUpCommand = "wsl-compaction-set-up-elevated"

func RequestSetUp(distribution string) error  { return errOnlyOnWindows }
func SetUpElevated(distribution string) error { return errOnlyOnWindows }
func Start() error                            { return errOnlyOnWindows }

func Status() msg.DiskUsageWSLCompaction {
	return msg.DiskUsageWSLCompaction{NotSetUpReason: errOnlyOnWindows.Error()}
}
