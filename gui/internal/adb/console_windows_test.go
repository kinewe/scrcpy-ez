//go:build windows

package adb

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestConsoleProbeChild(t *testing.T) {
	if os.Getenv("SCEZ_CONSOLE_PROBE") != "1" {
		return
	}
	window, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
	fmt.Printf("OWN_CONSOLE=%d\n", window)
	if window != 0 {
		t.Fatal("background child owns a console window")
	}
}

// Checks the child's own console API, including shell grandchildren. Merely
// counting conhost processes would also count headless Windows consoles.
func TestHiddenDirectCmdAndPowerShellChildrenHaveNoConsoleWindow(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	commands := []*exec.Cmd{
		exec.Command(executable, "-test.run=^TestConsoleProbeChild$"),
		exec.Command("cmd.exe", "/d", "/s", "/c", `""`+executable+`" -test.run=^TestConsoleProbeChild$"`),
		exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", "& '"+strings.ReplaceAll(executable, "'", "''")+"' '-test.run=^TestConsoleProbeChild$'"),
	}
	for _, command := range commands {
		command.Env = append(os.Environ(), "SCEZ_CONSOLE_PROBE=1")
		HideConsole(command)
		if strings.EqualFold(filepath.Base(command.Path), "cmd.exe") {
			command.SysProcAttr.CmdLine = `cmd.exe /d /s /c ""` + executable + `" -test.run=TestConsoleProbeChild"`
		}
		output, err := command.CombinedOutput()
		if err != nil || !strings.Contains(string(output), "OWN_CONSOLE=0") {
			t.Fatalf("%s: child console suppression failed: %v %s", command.Path, err, output)
		}
	}
}
