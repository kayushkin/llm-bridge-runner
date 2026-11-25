package wslcompact

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func decodeUTF16(t *testing.T, data []byte) string {
	t.Helper()
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xFE {
		t.Fatal("no UTF-16 byte order mark")
	}
	units := make([]uint16, 0, len(data)/2)
	for i := 2; i+1 < len(data); i += 2 {
		units = append(units, uint16(data[i])|uint16(data[i+1])<<8)
	}
	return string(utf16.Decode(units))
}

func TestTaskXML(t *testing.T) {
	compact, err := TaskXML("S-1-5-21-1-2-3-1001", `C:\ProgramData\llm-bridge\wsl-compact\compact-wsl.ps1`, "Ubuntu-22.04", true)
	if err != nil {
		t.Fatal(err)
	}
	text := decodeUTF16(t, compact)
	for _, want := range []string{"<RunLevel>HighestAvailable</RunLevel>", "<UserId>S-1-5-21-1-2-3-1001</UserId>", `-File &quot;C:\ProgramData\llm-bridge\wsl-compact\compact-wsl.ps1&quot;`, "<LogonType>InteractiveToken</LogonType>"} {
		if !strings.Contains(text, want) {
			t.Errorf("compact task lacks %q:\n%s", want, text)
		}
	}
	start, err := TaskXML("S-1-5-21-1-2-3-1001", `C:\x.ps1`, "Ubuntu-22.04", false)
	if err != nil {
		t.Fatal(err)
	}
	text = decodeUTF16(t, start)
	for _, want := range []string{"<RunLevel>LeastPrivilege</RunLevel>", "--headless wsl.exe -d Ubuntu-22.04 --exec /bin/sleep infinity", "<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>"} {
		if !strings.Contains(text, want) {
			t.Errorf("start task lacks %q:\n%s", want, text)
		}
	}
	for _, bad := range []string{"Ubuntu; calc", "a b", `x" & y`, "", strings.Repeat("a", 65)} {
		if _, err := TaskXML("S-1-5-21-1", `C:\x.ps1`, bad, false); err == nil {
			t.Errorf("took distribution %q", bad)
		}
	}
	if _, err := TaskXML("not-a-sid", `C:\x.ps1`, "Ubuntu", true); err == nil {
		t.Error("took a user that is no SID")
	}
}

func TestTheEmbeddedScriptIsTheOneOnDisk(t *testing.T) {
	if len(Script()) == 0 || !strings.Contains(string(Script()), "compact vdisk") || len(ScriptSHA256()) != 64 {
		t.Fatal("the compact script is not embedded")
	}
}
