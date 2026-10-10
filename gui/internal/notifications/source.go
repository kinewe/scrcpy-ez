package notifications

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

var ErrUnavailable = errors.New("notification listener unavailable")
var ErrTransport = errors.New("notification connection lost")

// ADB shell joins remote arguments; hardware identities/tags must stay literal shell arguments.
func shellArgument(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

type Target struct {
	Identity     string
	Name         string
	Connection   string
	Serial       string
	Epoch        uint64
	ServerEpoch  uint64
	DeviceSerial string
}

type Source interface {
	Run(context.Context, Target, func(Frame) error) error
}

type ADBSource struct {
	openMu      sync.Mutex
	connections map[string]*openConnection
	ADB         string
	Server      string
	// Tests must use their own shell tag, never read private notifications.
	TestTag   string
	cleanupMu sync.Mutex
	pending   []ownedCleanup
}

type ownedCleanup struct {
	device, session, remote string
	pid                     int
}

func (s *ADBSource) rememberCleanup(item ownedCleanup) {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	for _, old := range s.pending {
		if old.session == item.session {
			return
		}
	}
	if len(s.pending) >= 128 {
		s.pending = s.pending[1:]
	}
	s.pending = append(s.pending, item)
}

// Only this process's exact generated path/PID may be cleaned. A reused PID is
// never killed unless both class and session still match, and no arguments log.
func (s *ADBSource) cleanupOwned(ctx context.Context, serial string, item ownedCleanup) bool {
	if item.pid > 0 {
		check := s.command(ctx, "-s", serial, "shell", "cat", "/proc/"+strconv.Itoa(item.pid)+"/cmdline")
		check.Stderr = io.Discard
		out, err := check.Output()
		arguments := strings.Fields(strings.ReplaceAll(string(out), "\x00", " "))
		ownSession, ownClass := false, false
		for _, arg := range arguments {
			ownSession = ownSession || arg == item.session
			ownClass = ownClass || arg == "com.genymobile.scrcpy.notification.NotificationServer"
		}
		if err == nil && ownSession && ownClass {
			kill := s.command(ctx, "-s", serial, "shell", "kill", strconv.Itoa(item.pid))
			kill.Stdout, kill.Stderr = io.Discard, io.Discard
			if kill.Run() != nil {
				return false
			}
		}
	}
	remove := s.command(ctx, "-s", serial, "shell", "rm", "-f", item.remote)
	remove.Stdout, remove.Stderr = io.Discard, io.Discard
	return remove.Run() == nil
}

// A new server emits hello only after validating the physical device identity.
// This permits cleanup through a replacement transport without trusting an IP
// address that might now belong to another phone. Nothing is stored on disk.
func (s *ADBSource) flushCleanup(serial, expected string) {
	s.cleanupMu.Lock()
	var owned, retained []ownedCleanup
	for _, item := range s.pending {
		if item.device == expected {
			owned = append(owned, item)
		} else {
			retained = append(retained, item)
		}
	}
	s.pending = retained
	s.cleanupMu.Unlock()
	if len(owned) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, item := range owned {
		if !s.cleanupOwned(ctx, serial, item) {
			s.rememberCleanup(item)
		}
	}
}

func (s *ADBSource) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, s.ADB, args...)
	adb.HideConsole(cmd)
	return cmd
}

func (s *ADBSource) Run(ctx context.Context, target Target, emit func(Frame) error) error {
	TraceOpen("listener-start", "", target.Identity, "", 0, 0, 0, false)
	defer TraceOpen("listener-stop", "", target.Identity, "", 0, 0, 0, false)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	expected := adb.StableSerial(target.DeviceSerial)
	if expected == "" || len(expected) > 256 || strings.ContainsAny(expected, "\x00\r\n") {
		return &sourceFailure{code: "identity", err: ErrUnavailable}
	}
	if info, err := os.Stat(s.Server); err != nil || !info.Mode().IsRegular() {
		return &sourceFailure{code: "server", err: ErrUnavailable}
	}
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return ErrUnavailable
	}
	session := hex.EncodeToString(entropy[:])
	remote := "/data/local/tmp/scrcpy-ez-notification-" + session + ".jar"
	pid := 0
	defer func() {
		// Fresh cleanup context: cancellation must not skip removing the owned file.
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		item := ownedCleanup{device: expected, session: session, remote: remote, pid: pid}
		if !s.cleanupOwned(cleanup, target.Serial, item) {
			s.rememberCleanup(item)
		}
	}()
	setup, cancelSetup := context.WithTimeout(ctx, 8*time.Second)
	cmd := s.command(setup, "-s", target.Serial, "push", s.Server, remote)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	err := cmd.Run()
	cancelSetup()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrTransport
	}
	// Use non-interactive shell pipes. Stdin stays open until this particular worker stops.
	args := []string{"-s", target.Serial, "shell", "-T", "CLASSPATH=" + remote, "app_process", "/", "com.genymobile.scrcpy.notification.NotificationServer", session, shellArgument(expected)}
	if s.TestTag != "" {
		if len(s.TestTag) > 128 || strings.ContainsAny(s.TestTag, "\x00 \t\n\r'\"$`|;&<>()\\") {
			return ErrProtocol
		}
		args = append(args, shellArgument(s.TestTag))
	}
	process := s.command(context.Background(), args...)
	process.Stderr = io.Discard
	stdout, err := process.StdoutPipe()
	if err != nil {
		return ErrTransport
	}
	stdin, err := process.StdinPipe()
	if err != nil {
		return ErrTransport
	}
	if err = process.Start(); err != nil {
		return ErrTransport
	}
	s.openMu.Lock()
	if s.connections == nil {
		s.connections = make(map[string]*openConnection)
	}
	connection := &openConnection{ctx: ctx, identity: target.Identity, stdin: stdin, tokens: make(map[string]string), replies: make(map[string]chan string)}
	s.connections[session] = connection
	s.openMu.Unlock()
	defer func() { s.openMu.Lock(); delete(s.connections, session); s.openMu.Unlock() }()
	finished := make(chan struct{})
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			_ = stdin.Close()
			go func() {
				timer := time.NewTimer(1500 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-finished:
				case <-timer.C:
					_ = process.Process.Kill()
				}
			}()
		})
	}
	defer func() {
		stop()
		// Wait only after the stdout reader is done; Wait closes StdoutPipe.
		_ = process.Wait()
		close(finished)
	}()
	// A capability probe has a deadline; no timer/polling after ready.
	ready := make(chan struct{})
	var timedOut atomic.Bool
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		timer := time.NewTimer(25 * time.Second)
		defer timer.Stop()
		select {
		case <-ready:
		case <-ctx.Done():
			stop()
			return
		case <-watchDone:
			return
		case <-timer.C:
			timedOut.Store(true)
			stop()
			return
		}
		select {
		case <-ctx.Done():
			stop()
		case <-watchDone:
		}
	}()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), MaxFrame)
	hasHello, hasReady := false, false
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := scanner.Bytes()
		// scrcpy's optional Workarounds may write status lines before the protocol hello.
		if !hasHello && (len(line) == 0 || line[0] != '{') {
			continue
		}
		frame, parseErr := ParseFrame(line, session)
		if parseErr != nil {
			return ErrProtocol
		}
		if frame.Type == "error" {
			cause := ErrUnavailable
			if frame.Code == "busy" {
				cause = ErrTransport
			}
			return &sourceFailure{code: frame.Code, err: cause}
		}
		if frame.Type == "hello" {
			if hasHello {
				return ErrProtocol
			}
			hasHello = true
			pid = frame.PID
			s.flushCleanup(target.Serial, expected)
		}
		if frame.Type == "ready" {
			if hasReady {
				return ErrProtocol
			}
			hasReady = true
			TraceOpen("listener-ready", "", target.Identity, "", 0, pid, 0, false)
			close(ready)
		}
		s.openMu.Lock()
		switch frame.Type {
		case "snapshot", "post":
			if len(connection.tokens) >= 512 {
				connection.tokens = make(map[string]string)
			}
			connection.tokens[frame.Record.Key] = frame.Record.OpenToken
		case "remove":
			delete(connection.tokens, frame.Record.Key)
		case "open-result":
			if reply := connection.replies[frame.Request]; reply != nil {
				select {
				case reply <- frame.Code:
				default:
				}
			}
		}
		s.openMu.Unlock()
		if emitErr := emit(frame); emitErr != nil {
			return emitErr
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !hasReady {
		if timedOut.Load() {
			return &sourceFailure{code: "timeout", err: ErrTransport}
		}
		return &sourceFailure{code: "startup", err: ErrUnavailable}
	}
	if scanner.Err() != nil {
		return ErrProtocol
	}
	return ErrTransport
}
