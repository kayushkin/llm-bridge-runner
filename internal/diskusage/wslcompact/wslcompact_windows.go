//go:build windows

package wslcompact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kayushkin/llm-bridge/msg"
	"golang.org/x/sys/windows"
)

// SetUpCommand is the helper's command the elevated copy runs.
const SetUpCommand = "wsl-compaction-set-up-elevated"

// setUpResultFileName sits beside the helper: the elevated copy writes how
// setting up went there, since nothing waits on it.
const setUpResultFileName = "wsl-compaction-set-up-result.json"

// Folder is where the script, its tasks' definitions and its status live.
func Folder() (string, error) {
	programData := os.Getenv("ProgramData")
	if programData == "" {
		return "", fmt.Errorf("%%ProgramData%% is not set")
	}
	return filepath.Join(programData, "llm-bridge", "wsl-compact"), nil
}

// setUpResult is what the elevated copy records.
type setUpResult struct {
	FinishedAt time.Time `json:"finished_at"`
	Error      string    `json:"error,omitempty"`
}

func setUpResultPath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(executable), setUpResultFileName), nil
}

// RequestSetUp shows the owner the UAC prompt that runs this helper elevated
// to set up compaction for distribution, and returns once Windows has shown
// it and the owner answered: nil when they approved and the elevated copy
// started, an error when they declined or it could not start. How the setup
// itself went is in the status.
func RequestSetUp(distribution string) error {
	if err := ValidDistributionName(distribution); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if resultPath, err := setUpResultPath(); err == nil {
		os.Remove(resultPath) // a result from before this prompt must not read as this one's
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(executable)
	arguments, _ := windows.UTF16PtrFromString(SetUpCommand + " " + distribution)
	directory, _ := windows.UTF16PtrFromString(filepath.Dir(executable))
	if err := windows.ShellExecute(0, verb, file, arguments, directory, windows.SW_HIDE); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return fmt.Errorf("the UAC prompt was declined at the PC")
		}
		return fmt.Errorf("could not show the UAC prompt: %w", err)
	}
	return nil
}

// SetUpElevated installs the script and both tasks. It must run elevated,
// and records how it went beside the helper.
func SetUpElevated(distribution string) error {
	err := setUpElevated(distribution)
	result := setUpResult{FinishedAt: time.Now().UTC()}
	if err != nil {
		result.Error = err.Error()
	}
	if path, pathErr := setUpResultPath(); pathErr == nil {
		encoded, _ := json.Marshal(result)
		os.WriteFile(path, encoded, 0o644)
	}
	return err
}

func setUpElevated(distribution string) error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return fmt.Errorf("setting up WSL compaction needs administrator rights, and this copy of the helper has none")
	}
	if err := ValidDistributionName(distribution); err != nil {
		return err
	}
	folder, err := Folder()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return err
	}
	// Only administrators and SYSTEM may change what the elevated task runs;
	// everyone else may read it, so the runner can read the status.
	if output, err := exec.Command("icacls.exe", folder, "/inheritance:r",
		"/grant:r", "*S-1-5-32-544:(OI)(CI)F", "/grant:r", "*S-1-5-18:(OI)(CI)F", "/grant:r", "*S-1-5-32-545:(OI)(CI)RX",
		"/T", "/Q").CombinedOutput(); err != nil {
		return fmt.Errorf("icacls on %s: %w: %s", folder, err, strings.TrimSpace(string(output)))
	}
	scriptPath := filepath.Join(folder, ScriptFileName)
	if err := os.WriteFile(scriptPath, compactScript, 0o644); err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("read this user's SID: %w", err)
	}
	userSID := user.User.Sid.String()
	for _, task := range []struct {
		name     string
		elevated bool
	}{{CompactTaskName, true}, {StartTaskName, false}} {
		definition, err := TaskXML(userSID, scriptPath, distribution, task.elevated)
		if err != nil {
			return err
		}
		definitionPath := filepath.Join(folder, strings.ReplaceAll(task.name, " ", "-")+".xml")
		if err := os.WriteFile(definitionPath, definition, 0o644); err != nil {
			return err
		}
		if output, err := exec.Command("schtasks.exe", "/Create", "/TN", TaskFolder+task.name, "/XML", definitionPath, "/F").CombinedOutput(); err != nil {
			return fmt.Errorf("schtasks /Create %s: %w: %s", task.name, err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

// Status says whether compaction is set up and how the last run went.
func Status() msg.DiskUsageWSLCompaction {
	var status msg.DiskUsageWSLCompaction
	if path, err := setUpResultPath(); err == nil {
		if encoded, err := os.ReadFile(path); err == nil {
			var result setUpResult
			if json.Unmarshal(encoded, &result) == nil && result.Error != "" {
				status.SetUpError = result.Error
			}
		}
	}
	status.NotSetUpReason = notSetUpReason()
	status.SetUp = status.NotSetUpReason == ""
	if folder, err := Folder(); err == nil {
		if encoded, err := os.ReadFile(filepath.Join(folder, StatusFileName)); err == nil {
			var run msg.DiskUsageWSLCompactionRun
			if err := json.Unmarshal(bytes.TrimPrefix(encoded, []byte("\xef\xbb\xbf")), &run); err != nil {
				status.LastRun = &msg.DiskUsageWSLCompactionRun{State: msg.DiskUsageWSLCompactionFailed, Error: "could not read the last run's status: " + err.Error()}
			} else {
				status.LastRun = &run
			}
		}
	}
	return status
}

func notSetUpReason() string {
	folder, err := Folder()
	if err != nil {
		return err.Error()
	}
	scriptPath := filepath.Join(folder, ScriptFileName)
	installed, err := os.ReadFile(scriptPath)
	if err != nil {
		return "the compact script is not installed"
	}
	sum := sha256.Sum256(installed)
	if hex.EncodeToString(sum[:]) != ScriptSHA256() {
		return "the installed compact script is another build's; set it up again"
	}
	// The script must not be writable without administrator rights, or the
	// elevated task would run whatever an ordinary program wrote there.
	// Opening it for writing changes nothing.
	if file, err := os.OpenFile(scriptPath, os.O_WRONLY, 0); err == nil {
		file.Close()
		return "the compact script can be changed without administrator rights; set it up again"
	}
	for _, name := range []string{CompactTaskName, StartTaskName} {
		if err := exec.Command("schtasks.exe", "/Query", "/TN", TaskFolder+name).Run(); err != nil {
			return fmt.Sprintf("the scheduled task %s%s is missing", TaskFolder, name)
		}
	}
	return ""
}

// Start starts the elevated compact task, which shuts WSL down about ten
// seconds later.
func Start() error {
	if reason := notSetUpReason(); reason != "" {
		return fmt.Errorf("WSL compaction is not set up: %s", reason)
	}
	if output, err := exec.Command("schtasks.exe", "/Run", "/TN", TaskFolder+CompactTaskName).CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks /Run: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
