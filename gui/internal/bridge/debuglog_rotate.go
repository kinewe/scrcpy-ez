package bridge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	debugLogFileLimit = 8 * 1024 * 1024
	debugLogArchives  = 3
	debugLogRetention = 7 * 24 * time.Hour
	debugLogStamp     = "20060102T150405.000000000Z"
)

type rotatingDebugLog struct {
	path       string
	file       *os.File
	size       int64
	limit      int64
	retryAfter time.Time
}

func (l *rotatingDebugLog) open() error {
	if _, err := regularLogExists(l.path); err != nil {
		return err
	}
	file, err := openBridgeLogFile(l.path)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	l.file = file
	l.size = info.Size()
	return nil
}

func (l *rotatingDebugLog) close() {
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
}

func regularLogExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("log path is not a regular file: %s", path)
	}
	return true, nil
}

func (l *rotatingDebugLog) rotate(now time.Time) error {
	archive := l.path + ".ez-" + now.UTC().Format(debugLogStamp)
	if _, err := os.Lstat(archive); !os.IsNotExist(err) {
		return fmt.Errorf("archive path unavailable: %s", archive)
	}
	// Windows requires closing the current log before renaming it.
	l.close()
	if err := os.Rename(l.path, archive); err != nil {
		return err
	}
	if err := l.prune(now, false); err != nil {
		return err
	}
	return l.open()
}

func (l *rotatingDebugLog) write(line string) error {
	if int64(len(line)) > l.limit {
		return fmt.Errorf("log line exceeds file limit")
	}
	if l.file == nil || l.size+int64(len(line)) > l.limit {
		now := time.Now()
		if now.Before(l.retryAfter) {
			return fmt.Errorf("log rotation retry pending")
		}
		if l.file == nil {
			if err := l.prune(now, false); err != nil {
				l.retryAfter = now.Add(time.Second)
				return err
			}
			if err := l.open(); err != nil {
				l.retryAfter = now.Add(time.Second)
				return err
			}
		}
		if l.size+int64(len(line)) > l.limit {
			if err := l.rotate(now); err != nil {
				l.retryAfter = now.Add(time.Second)
				return err
			}
		}
		if l.size+int64(len(line)) > l.limit {
			return fmt.Errorf("current log has no space after rotation")
		}
	}
	n, err := l.file.WriteString(line)
	l.size += int64(n)
	return err
}

func (l *rotatingDebugLog) prune(now time.Time, cleanLegacy bool) error {
	cutoff := now.Add(-debugLogRetention)
	dir := filepath.Dir(l.path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	archivePrefix := filepath.Base(l.path) + ".ez-"
	kept := 0
	// ReadDir sorts by name. The UTC timestamp keeps newest archives first here.
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		name := entry.Name()
		path := filepath.Join(dir, name)
		if stamp, ok := strings.CutPrefix(name, archivePrefix); ok {
			if _, err := time.Parse(debugLogStamp, stamp); err != nil {
				continue
			}
			info, err := entry.Info()
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				continue
			}
			if kept < debugLogArchives && !info.ModTime().Before(cutoff) {
				kept++
			} else if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		} else if cleanLegacy && isDatedBridgeLog(name) {
			// Dated files belong to the old default layout, never to custom paths.
			if info, err := entry.Info(); err == nil && info.Mode().IsRegular() && info.ModTime().Before(cutoff) {
				_ = os.Remove(path)
			}
		}
	}
	return nil
}

func isDatedBridgeLog(name string) bool {
	date, ok := strings.CutPrefix(name, "bridge-")
	if !ok {
		return false
	}
	date, ok = strings.CutSuffix(date, ".log")
	if !ok || len(date) != len("20060102") {
		return false
	}
	_, err := time.Parse("20060102", date)
	return err == nil
}
