//go:build windows

package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestLoginStartupRegistration(t *testing.T) {
	keyPath := `Software\scrcpy-ez-test-` + filepath.Base(filepath.Dir(t.TempDir()))
	t.Cleanup(func() {
		if err := registry.DeleteKey(registry.CURRENT_USER, keyPath); err != nil && err != registry.ErrNotExist {
			t.Error(err)
		}
	})
	startup := loginStartup{keyPath: keyPath, command: loginStartupCommand(`C:\EZ Tests\投屏\scrcpy-ez.exe`)}
	if startup.command != `"C:\EZ Tests\投屏\scrcpy-ez.exe"` {
		t.Fatalf("startup command = %q", startup.command)
	}
	checkEnabled := func(s loginStartup, want bool) {
		t.Helper()
		if got, err := s.enabled(); err != nil || got != want {
			t.Fatalf("enabled() = %t, %v, want %t", got, err, want)
		}
	}
	setEnabled := func(s loginStartup, want bool) {
		t.Helper()
		if got, err := s.setEnabled(want); err != nil || got != want {
			t.Fatalf("setEnabled(%t) = %t, %v", want, got, err)
		}
	}

	checkEnabled(startup, false)
	setEnabled(startup, false)
	tooLong := loginStartup{keyPath: keyPath, command: loginStartupCommand(`C:\` + strings.Repeat("a", 260))}
	if _, err := tooLong.setEnabled(true); err == nil {
		t.Fatal("expected an error for a command exceeding the Windows Run limit")
	}
	checkEnabled(startup, false)

	setEnabled(startup, true)
	setEnabled(startup, true)
	checkEnabled(startup, true)
	otherCopy := loginStartup{keyPath: keyPath, command: loginStartupCommand(`D:\EZ\scrcpy-ez.exe`)}
	checkEnabled(otherCopy, false)
	setEnabled(otherCopy, false)
	checkEnabled(startup, true)
	setEnabled(otherCopy, true)
	checkEnabled(startup, false)
	checkEnabled(otherCopy, true)
	setEnabled(otherCopy, false)
	setEnabled(otherCopy, false)
	checkEnabled(otherCopy, false)
}
