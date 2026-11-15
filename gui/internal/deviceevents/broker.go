package deviceevents

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"scrcpy-ez/gui/internal/loopback"
)

// Serve exposes an authenticated loopback stream to this GUI's cast workers.
// Only the GUI owns the ADB subscription; workers never reset its server.
func Serve(ctx context.Context, h *Hub) (endpoint, token string, err error) {
	l, err := loopback.Listen()
	if err != nil {
		return "", "", err
	}
	var key [32]byte
	if _, err = rand.Read(key[:]); err != nil {
		l.Close()
		return "", "", err
	}
	token = hex.EncodeToString(key[:])
	endpoint = l.Addr().String()
	go func() { <-ctx.Done(); l.Close() }()
	go func() {
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			go serveConnection(ctx, h, c, token)
		}
	}()
	return endpoint, token, nil
}

func serveConnection(ctx context.Context, h *Hub, c net.Conn, token string) {
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var hello struct{ Token string }
	if json.NewDecoder(c).Decode(&hello) != nil || subtle.ConstantTimeCompare([]byte(hello.Token), []byte(token)) != 1 {
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { var b [1]byte; _, _ = c.Read(b[:]); cancel() }()
	enc := json.NewEncoder(c)
	for s := range h.Subscribe(cctx) {
		_ = c.SetWriteDeadline(time.Now().Add(3 * time.Second))
		if enc.Encode(s) != nil {
			return
		}
	}
}

// Follow receives the GUI's stream. EOF invalidates observer availability.
func Follow(ctx context.Context, endpoint, token string, h *Hub) error {
	c, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return err
	}
	defer c.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			c.Close()
		case <-done:
		}
	}()
	if err = json.NewEncoder(c).Encode(struct{ Token string }{token}); err != nil {
		return err
	}
	dec := json.NewDecoder(c)
	for {
		var s Snapshot
		if err = dec.Decode(&s); err != nil {
			h.Publish(Snapshot{Error: fmt.Sprint(err)})
			return err
		}
		h.publish(s, true, false)
	}
}
