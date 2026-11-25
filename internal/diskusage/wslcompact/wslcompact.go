// Package wslcompact sets up and starts compaction of a Windows machine's WSL
// disk files, which needs administrator rights the runner does not have.
//
// Setting up is done once, elevated, after the machine's owner approves a UAC
// prompt: it writes compact-wsl.ps1 (embedded here) into
// %ProgramData%\llm-bridge\wsl-compact, which it makes writable by
// administrators and SYSTEM only, and registers two tasks under \llm-bridge\:
// "WSL compact", which runs the script with the owner's administrator rights
// and no prompt, and "WSL start", which starts the runner's distribution again
// as the owner without them. After that the runner starts a compaction with
// schtasks, which an owner's ordinary process may do for a task it owns.
//
// What the elevated task runs is fixed: a program without administrator
// rights can start it, but not change the script or the task.
package wslcompact

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"
)

//go:embed compact-wsl.ps1
var compactScript []byte

const (
	// TaskFolder holds both tasks.
	TaskFolder = `\llm-bridge\`
	// CompactTaskName runs the script elevated.
	CompactTaskName = "WSL compact"
	// StartTaskName starts WSL again as the owner.
	StartTaskName = "WSL start"
	// ScriptFileName is the script's name in the folder.
	ScriptFileName = "compact-wsl.ps1"
	// StatusFileName is where the script records each run.
	StatusFileName = "status.json"
)

// ScriptSHA256 is the hash of the script this build installs, so a status can
// say when the installed one is another build's.
func ScriptSHA256() string {
	sum := sha256.Sum256(compactScript)
	return hex.EncodeToString(sum[:])
}

// Script is the script this build installs.
func Script() []byte { return append([]byte(nil), compactScript...) }

var distributionName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// ValidDistributionName refuses a name that could not be a WSL distribution's,
// since it goes into a task's command line.
func ValidDistributionName(name string) error {
	if !distributionName.MatchString(name) {
		return fmt.Errorf("%q is not a WSL distribution name", name)
	}
	return nil
}

// TaskXML is a Task Scheduler definition, in UTF-16 with its byte order mark
// as schtasks /Create /XML wants it.
//
// The compact task runs as userSID with its highest rights (elevated) and
// only on demand; the start task runs as userSID with its least rights and
// starts distribution the way WSL's keepalive does, a console-less wsl.exe
// that keeps the distribution running.
func TaskXML(userSID, scriptPath, distribution string, elevated bool) ([]byte, error) {
	if !strings.HasPrefix(userSID, "S-1-5-") {
		return nil, fmt.Errorf("%q is not a user's SID", userSID)
	}
	if err := ValidDistributionName(distribution); err != nil {
		return nil, err
	}
	runLevel, command, arguments, description := "LeastPrivilege",
		`C:\Windows\System32\conhost.exe`,
		"--headless wsl.exe -d "+distribution+" --exec /bin/sleep infinity",
		"Starts WSL again after llm-bridge compacted its disks. Installed by llm-bridge-disk-usage."
	timeLimit := "PT0S" // the keepalive runs until WSL stops
	if elevated {
		runLevel = "HighestAvailable"
		arguments = `--headless C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "` + scriptPath + `"`
		description = "Shuts WSL down, compacts its disk files and starts it again, for the llm-bridge Machines page. Installed by llm-bridge-disk-usage."
		timeLimit = "PT4H"
	}
	var buffer bytes.Buffer
	fmt.Fprintf(&buffer, `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>%s</Description>
  </RegistrationInfo>
  <Principals>
    <Principal id="Owner">
      <UserId>%s</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>%s</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>false</StartWhenAvailable>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <ExecutionTimeLimit>%s</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Owner">
    <Exec>
      <Command>%s</Command>
      <Arguments>%s</Arguments>
    </Exec>
  </Actions>
</Task>
`, xmlEscape(description), xmlEscape(userSID), runLevel, timeLimit, xmlEscape(command), xmlEscape(arguments))
	encoded := utf16.Encode([]rune(buffer.String()))
	out := []byte{0xFF, 0xFE}
	for _, unit := range encoded {
		out = append(out, byte(unit), byte(unit>>8))
	}
	return out, nil
}

func xmlEscape(text string) string {
	var buffer strings.Builder
	for _, r := range text {
		switch r {
		case '&':
			buffer.WriteString("&amp;")
		case '<':
			buffer.WriteString("&lt;")
		case '>':
			buffer.WriteString("&gt;")
		case '"':
			buffer.WriteString("&quot;")
		default:
			buffer.WriteRune(r)
		}
	}
	return buffer.String()
}
