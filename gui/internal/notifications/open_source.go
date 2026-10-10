package notifications

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"sync"
)

// Only the original PendingIntent kept inside this live phone session can be
// sent. No URI, Intent, package or shell command crosses the inbound channel.
type openConnection struct {
	ctx      context.Context
	identity string
	stdin    io.WriteCloser
	writeMu  sync.Mutex
	tokens   map[string]string
	replies  map[string]chan string
}

func (s *ADBSource) currentLocked(req OpenRequest) *openConnection {
	c, _ := s.currentLockedReason(req)
	return c
}

func (s *ADBSource) currentLockedReason(req OpenRequest) (*openConnection, string) {
	c := s.connections[req.Session]
	if c == nil {
		return nil, "session-missing"
	}
	if c.ctx.Err() != nil {
		return nil, "session-canceled"
	}
	if c.identity != req.Identity || !validActionToken(req.Token) {
		return nil, "capability-invalid"
	}
	token, exists := c.tokens[req.Key]
	if !exists {
		return nil, "notification-removed"
	}
	if token != req.Token {
		return nil, "notification-updated"
	}
	return c, ""
}

func (s *ADBSource) Current(req OpenRequest) bool {
	s.openMu.Lock()
	defer s.openMu.Unlock()
	c, reason := s.currentLockedReason(req)
	if c == nil {
		TraceOpen("capability", reason, req.Identity, req.Package, 0, 0, 0, false)
	}
	return c != nil
}

func (s *ADBSource) Open(ctx context.Context, req OpenRequest, displayID int) error {
	if displayID <= 0 || ctx.Err() != nil {
		return ErrUnavailable
	}
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return ErrUnavailable
	}
	id := hex.EncodeToString(entropy[:])
	reply := make(chan string, 1)
	s.openMu.Lock()
	c, reason := s.currentLockedReason(req)
	if c == nil {
		s.openMu.Unlock()
		TraceOpen("capability", reason, req.Identity, req.Package, displayID, 0, 0, false)
		return &OpenFailure{Code: "stale"}
	}
	if len(c.replies) >= 16 {
		s.openMu.Unlock()
		return ErrUnavailable
	}
	c.replies[id] = reply
	s.openMu.Unlock()
	defer func() { s.openMu.Lock(); delete(c.replies, id); s.openMu.Unlock() }()
	data, err := json.Marshal(struct {
		Version int    `json:"v"`
		Session string `json:"session"`
		Type    string `json:"type"`
		Request string `json:"request"`
		Key     string `json:"key"`
		Token   string `json:"token"`
		Display int    `json:"display"`
	}{ProtocolVersion, req.Session, "open", id, req.Key, req.Token, displayID})
	if err != nil {
		return ErrProtocol
	}
	c.writeMu.Lock()
	TraceOpen("send", "", req.Identity, req.Package, displayID, 0, 0, false)
	_, err = c.stdin.Write(append(data, '\n'))
	c.writeMu.Unlock()
	if err != nil {
		return ErrTransport
	}
	select {
	case code := <-reply:
		TraceOpen("result", code, req.Identity, req.Package, displayID, 0, 0, false)
		if code == "opened" {
			return nil
		}
		return &OpenFailure{Code: code}
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ctx.Done():
		return ErrTransport
	}
}
