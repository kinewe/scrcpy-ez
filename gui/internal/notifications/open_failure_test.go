package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type resultWriter struct {
	source     *ADBSource
	connection *openConnection
	code       string
}

func (w *resultWriter) Close() error { return nil }
func (w *resultWriter) Write(data []byte) (int, error) {
	var value struct {
		Request string `json:"request"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return 0, err
	}
	w.source.openMu.Lock()
	reply := w.connection.replies[value.Request]
	w.source.openMu.Unlock()
	reply <- w.code
	return len(data), nil
}

func TestPhoneDetailFailureKeepsSpecificReason(t *testing.T) {
	for _, code := range []string{"stale", "canceled", "unsupported", "launch"} {
		t.Run(code, func(t *testing.T) {
			req := OpenRequest{Identity: "lab", Session: "session", Key: "key", Token: strings.Repeat("a", 32)}
			c := &openConnection{ctx: context.Background(), identity: req.Identity, tokens: map[string]string{req.Key: req.Token}, replies: make(map[string]chan string)}
			s := &ADBSource{connections: map[string]*openConnection{req.Session: c}}
			c.stdin = &resultWriter{source: s, connection: c, code: code}
			err := s.Open(context.Background(), req, 17)
			if !errors.Is(err, ErrUnavailable) || OpenFailureCode(err) != code {
				t.Fatalf("lost phone result: %v", err)
			}
			if openFailureBody(err) == openFailureBody(errors.New("unknown")) {
				t.Fatal("specific result replaced by generic body")
			}
		})
	}
}

func TestOpenTraceExcludesForeignDataAndStopsAtLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	t.Setenv("SCEZ_NOTIFICATION_TRACE", path)
	secret := "private-message-token-123456\n"
	TraceOpen("result", secret, "192.0.2.1:5555", secret, 17, 123, time.Second, false)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "192.0.2.1") {
		t.Fatal("trace retained private input")
	}
	var value OpenTrace
	if json.Unmarshal(data, &value) != nil || value.Code != "unknown" || value.Package != "" || value.Device == "" {
		t.Fatal("trace sanitization failed")
	}
	if err = os.WriteFile(path, make([]byte, 2*1024*1024), 0600); err != nil {
		t.Fatal(err)
	}
	TraceOpen("begin", "", "lab", "com.example.app", 0, 0, 0, false)
	info, _ := os.Stat(path)
	if info.Size() != 2*1024*1024 {
		t.Fatal("trace exceeded its bound")
	}
}

func TestLocalTraceRequiresMarkerAndRespectsExplicitPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SCEZ_NOTIFICATION_TRACE", "")
	ConfigureOpenTrace(dir)
	if os.Getenv("SCEZ_NOTIFICATION_TRACE") != "" {
		t.Fatal("ordinary package enabled tracing")
	}
	if err := os.WriteFile(filepath.Join(dir, "notification-diagnostic.enabled"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	ConfigureOpenTrace(dir)
	if os.Getenv("SCEZ_NOTIFICATION_TRACE") != filepath.Join(dir, "notification-click.jsonl") {
		t.Fatal("diagnostic marker ignored")
	}
	t.Setenv("SCEZ_NOTIFICATION_TRACE", "explicit.jsonl")
	ConfigureOpenTrace(dir)
	if os.Getenv("SCEZ_NOTIFICATION_TRACE") != "explicit.jsonl" {
		t.Fatal("explicit diagnostic path replaced")
	}
}
