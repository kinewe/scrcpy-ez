package rootrepair

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"
)

// Serve is a separate authenticated, loopback-only RPC. Background discovery
// has no callers here. The caller's connection controls its waiter lifetime.
func Serve(ctx context.Context, c *Coordinator, valid func(Request) bool) (string, string, error) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return "", "", e
	}
	var b [32]byte
	if _, e = rand.Read(b[:]); e != nil {
		l.Close()
		return "", "", e
	}
	token := hex.EncodeToString(b[:])
	go func() { <-ctx.Done(); l.Close() }()
	go func() {
		for {
			conn, e := l.Accept()
			if e != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				var hello struct {
					Token   string
					Request Request
				}
				if json.NewDecoder(io.LimitReader(conn, 4096)).Decode(&hello) != nil || subtle.ConstantTimeCompare([]byte(token), []byte(hello.Token)) != 1 {
					return
				}
				_ = conn.SetReadDeadline(time.Time{})
				call, cancel := context.WithTimeout(ctx, Budget)
				defer cancel()
				go func() { var x [1]byte; _, _ = conn.Read(x[:]); cancel() }()
				result := Outcome{Status: "canceled", Error: "连接已变化，请重新投屏"}
				if valid(hello.Request) {
					result = c.Repair(call, hello.Request)
				}
				// A success from an old transport must never authorize its replacement.
				if result.Accepted() && !valid(hello.Request) {
					result = Outcome{Status: "canceled", Error: "连接已变化，请重新投屏"}
				}
				_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
				_ = json.NewEncoder(conn).Encode(result)
			}()
		}
	}()
	return l.Addr().String(), token, nil
}

func Call(ctx context.Context, endpoint, token string, req Request) Outcome {
	host, _, e := net.SplitHostPort(endpoint)
	if e != nil || host != "127.0.0.1" {
		return Outcome{Status: "failed", Error: "修复服务地址无效"}
	}
	conn, e := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", endpoint)
	if e != nil {
		return Outcome{Status: "failed", Error: e.Error()}
	}
	defer conn.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	e = json.NewEncoder(conn).Encode(struct {
		Token   string
		Request Request
	}{token, req})
	if e != nil {
		return Outcome{Status: "failed", Error: e.Error()}
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
	}
	var result Outcome
	if e = json.NewDecoder(io.LimitReader(conn, 4096)).Decode(&result); e != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return Outcome{Status: "canceled", Error: ctx.Err().Error()}
		}
		return Outcome{Status: "failed", Error: e.Error()}
	}
	return result
}
