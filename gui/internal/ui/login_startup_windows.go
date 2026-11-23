//go:build windows

package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows/registry"
)

const (
	loginStartupKey   = `Software\Microsoft\Windows\CurrentVersion\Run`
	loginStartupValue = "scrcpy-ez"
)

type loginStartup struct {
	keyPath string
	command string
}

func currentLoginStartup() loginStartup {
	exe, err := os.Executable()
	if err != nil {
		return loginStartup{keyPath: loginStartupKey}
	}
	return loginStartup{keyPath: loginStartupKey, command: loginStartupCommand(exe)}
}

func loginStartupCommand(executable string) string {
	return `"` + filepath.Clean(executable) + `"`
}

func (s loginStartup) matches(key registry.Key) (bool, error) {
	command, _, err := key.GetStringValue(loginStartupValue)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	return strings.EqualFold(s.command, command), err
}

func (s loginStartup) enabled() (bool, error) {
	if s.command == "" {
		return false, errors.New("无法读取 EZ 的安装路径")
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, s.keyPath, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer key.Close()
	return s.matches(key)
}

func (s loginStartup) setEnabled(enabled bool) (bool, error) {
	if s.command == "" {
		return false, errors.New("无法读取 EZ 的安装路径")
	}
	if enabled {
		// Windows limits commands in its Run key to 260 UTF-16 code units.
		if len(utf16.Encode([]rune(s.command))) > 260 {
			return false, errors.New("EZ 的安装路径过长，无法设置开机自启")
		}
		key, _, err := registry.CreateKey(registry.CURRENT_USER, s.keyPath, registry.SET_VALUE)
		if err != nil {
			return false, err
		}
		defer key.Close()
		err = key.SetStringValue(loginStartupValue, s.command)
		return err == nil, err
	}

	key, err := registry.OpenKey(registry.CURRENT_USER, s.keyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer key.Close()
	// A portable copy should only remove the entry that points to itself.
	matches, err := s.matches(key)
	if err != nil || !matches {
		return false, err
	}
	err = key.DeleteValue(loginStartupValue)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	return false, err
}
