//go:build windows

package bridge

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestInventoryVariantABIAndUnicode(t *testing.T) {
	wantSize := uintptr(16)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		wantSize = 24
	}
	if unsafe.Sizeof(inventoryVariant{}) != wantSize {
		t.Fatal("incorrect Windows VARIANT layout")
	}
	const text = "中文路径 😀 | 参数\n第二行\x00末尾"
	bstr, err := inventoryBSTR(text)
	if err != nil {
		t.Fatal(err)
	}
	value := inventoryVariant{kind: 8, data: [2]uintptr{bstr}}
	defer value.clear()
	if got, err := value.text(); err != nil || got != text {
		t.Fatalf("Unicode BSTR round trip: %q %v", got, err)
	}
	for _, kind := range []uint16{0, 1} {
		if got, err := (&inventoryVariant{kind: kind}).text(); err != nil || got != "" {
			t.Fatalf("null text: %q %v", got, err)
		}
	}
	if _, err := (&inventoryVariant{kind: 3}).text(); err == nil {
		t.Fatal("numeric command line accepted")
	}
	if _, err := (&inventoryVariant{kind: 1}).processID(); err == nil {
		t.Fatal("null PID accepted")
	}
}

func TestInventoryCreationDateOffsetsAndPrecision(t *testing.T) {
	want := time.Date(2026, 10, 10, 12, 30, 45, 123456000, time.UTC).UnixMicro()
	for _, value := range []string{"20261010203045.123456+480", "20261010073045.123456-300", "20261010123045.123456+000"} {
		got, err := inventoryCreationMicros(value)
		if err != nil || got != want {
			t.Fatalf("%q: %d %v", value, got, err)
		}
	}
	for _, value := range []string{"", "20261310123045.123456+000", "20261010123045.******+000", "20261010123045.123456:000", "20261010123045.123456+***"} {
		if _, err := inventoryCreationMicros(value); err == nil {
			t.Fatalf("invalid creation date accepted: %q", value)
		}
	}
}

// This test binary is copied as scrcpy.exe to exercise real WMI, without
// starting any device connection or terminating users' actual clients.
func TestInventoryHelperProcess(t *testing.T) {
	if os.Getenv("SCEZ_INVENTORY_TEST_CHILD") != "1" {
		return
	}
	_, _ = os.Stdout.WriteString("ready\n")
	_, _ = os.Stdin.Read(make([]byte, 1))
	os.Exit(0)
}

func startInventoryHelper(t *testing.T, exe string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(exe, append([]string{"-test.run=^TestInventoryHelperProcess$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "SCEZ_INVENTORY_TEST_CHILD=1")
	cmd.SysProcAttr = &syscallProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if line != "ready\n" {
			t.Fatalf("helper not ready: %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper startup timeout")
	}
	return cmd
}

func TestNativeScrcpyInventoryRealProcessesAndIsolation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "中文 路径")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "scrcpy.exe")
	current, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(current)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.Create(exe)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(target, source)
	target.Close()
	if err != nil {
		t.Fatal(err)
	}
	const title = "标题 😀 | 第一行\n第二行"
	main := startInventoryHelper(t, exe, "--serial", "INVENTORY_TEST_A", "--window-title="+title)
	app := startInventoryHelper(t, exe, "--serial", "INVENTORY_TEST_A", "--new-display=1280x720", "--start-app=+com.test.app")
	otherApp := startInventoryHelper(t, exe, "--serial", "INVENTORY_TEST_A", "--new-display=1280x720", "--start-app=com.test.app.extra")
	otherDevice := startInventoryHelper(t, exe, "--serial", "INVENTORY_TEST_AB")
	// Absolute child paths and system DLLs remain usable, but no shell can be
	// resolved. A hidden PowerShell fallback would fail this inventory test.
	t.Setenv("PATH", dir)
	procs, err := listScrcpyProcs()
	if err != nil {
		t.Fatal(err)
	}
	byPID := make(map[int]scrcpyProc)
	for _, proc := range procs {
		byPID[proc.pid] = proc
	}
	for _, cmd := range []*exec.Cmd{main, app, otherApp, otherDevice} {
		proc, ok := byPID[cmd.Process.Pid]
		if !ok || proc.ppid != os.Getpid() || proc.createdMicros == 0 {
			t.Fatalf("helper missing or incorrect: pid=%d row=%+v", cmd.Process.Pid, proc)
		}
		if proc.cmdline != windows.ComposeCommandLine(cmd.Args) {
			t.Fatalf("command line changed: want %q, got %q", windows.ComposeCommandLine(cmd.Args), proc.cmdline)
		}
		h, err := openInventoryProcess(proc)
		if err != nil {
			t.Fatalf("live identity rejected: %+v: %v", proc, err)
		}
		windows.CloseHandle(h)
		proc.createdMicros++
		if h, err := openInventoryProcess(proc); err == nil {
			windows.CloseHandle(h)
			t.Fatal("reused PID identity accepted")
		}
	}
	if !strings.Contains(byPID[main.Process.Pid].cmdline, title) {
		t.Fatal("Unicode/multiline title changed")
	}
	if got := bringToFrontCandidates(procs, []string{"INVENTORY_TEST_A"}); len(got) != 1 || got[0].pid != main.Process.Pid {
		t.Fatalf("main display isolation failed: %+v", got)
	}
	if got := bringAppWinCandidates(procs, []string{"INVENTORY_TEST_A"}, "com.test.app"); len(got) != 1 || got[0].pid != app.Process.Pid {
		t.Fatalf("app display isolation failed: %+v", got)
	}
	old := byPID[main.Process.Pid]
	_ = main.Process.Kill()
	_ = main.Wait()
	if h, err := openInventoryProcess(old); err == nil {
		windows.CloseHandle(h)
		t.Fatal("exited identity accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), scrcpyInventoryTimeout)
	defer cancel()
	fresh, err := queryScrcpyWMI(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, proc := range fresh {
		if proc.pid == old.pid && proc.createdMicros == old.createdMicros {
			t.Fatal("exited process returned by fresh inventory")
		}
	}
}
