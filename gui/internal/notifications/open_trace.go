package notifications

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

var openTraceMu sync.Mutex
var tracePackage = regexp.MustCompile(`^[A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)+$`)

// A diagnostic package opts in through this local marker; ordinary packages
// do not contain it. Explicit environment configuration takes precedence.
func ConfigureOpenTrace(directory string) {
	if os.Getenv("SCEZ_NOTIFICATION_TRACE") != "" {
		return
	}
	info, err := os.Stat(filepath.Join(directory, "notification-diagnostic.enabled"))
	if err == nil && info.Mode().IsRegular() {
		_ = os.Setenv("SCEZ_NOTIFICATION_TRACE", filepath.Join(directory, "notification-click.jsonl"))
	}
}

// Opt-in diagnostic records contain only fixed stages/codes and technical IDs.
// No title, body, OTP, token, notification key, contact, URI or Intent is accepted.
type OpenTrace struct {
	Time      string `json:"time"`
	Stage     string `json:"stage"`
	Code      string `json:"code,omitempty"`
	Device    string `json:"device,omitempty"`
	Package   string `json:"package,omitempty"`
	Display   int    `json:"display,omitempty"`
	PID       int    `json:"pid,omitempty"`
	ElapsedMS int64  `json:"elapsedMS,omitempty"`
	Created   bool   `json:"created,omitempty"`
}

func TraceOpen(stage, code, identity, pkg string, display, pid int, elapsed time.Duration, created bool) {
	path := os.Getenv("SCEZ_NOTIFICATION_TRACE")
	if path == "" {
		return
	}
	switch stage {
	case "native-activation", "activation", "begin", "window", "ready", "send", "result", "complete", "failed", "listener-start", "listener-ready", "listener-stop", "listener-retire", "listener-retained", "capability":
	case "notification-post", "notification-show", "notification-drop", "toast-submit":
	case "notification-remove", "toast-remove", "toast-clear", "toast-dismissed", "toast-remove-deferred", "toast-remove-canceled":
	default:
		return
	}
	switch code {
	case "", "opened", "stale", "canceled", "unsupported", "launch", "timeout", "canceled-context", "connection", "unavailable", "unknown", "disabled", "switching", "start", "window-lost", "ready-timeout", "action-timeout", "video-timeout", "expired", "queued":
	case "session-missing", "session-canceled", "capability-invalid", "notification-removed", "notification-updated", "target-offline", "physical-changed", "server-epoch", "transport-changed", "transport-generation", "transport-healthy":
	case "com", "toast", "arguments-unavailable", "duplicate":
	case "normal", "only-alert-once", "banner", "silent", "silent-same-body", "silent-capability", "stale-post", "duplicate-content", "policy-blocked", "send-error":
	case "source-remove", "clear", "user-canceled", "application-hidden", "timed-out", "coalescing", "replacement", "grace-expired":
	default:
		code = "unknown"
	}
	if len(pkg) > 512 || !tracePackage.MatchString(pkg) {
		pkg = ""
	}
	device := ""
	if identity != "" {
		device = ShortID(identity)
	}
	entry := OpenTrace{Time: time.Now().Format(time.RFC3339Nano), Stage: stage, Code: code, Device: device, Package: pkg, Display: display, PID: pid, ElapsedMS: elapsed.Milliseconds(), Created: created}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	openTraceMu.Lock()
	defer openTraceMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size()+int64(len(data))+1 > 2*1024*1024 {
		return
	}
	_, _ = f.Write(append(data, '\n'))
}
