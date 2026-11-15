// Package loopback allocates private TCP endpoints for GUI/worker IPC.
package loopback

import (
	"fmt"
	"net"
)

const maxListenAttempts = 64

// Listen keeps internal JSON services outside ADB's default emulator scan
// window. On hosts with a low dynamic-port range, bind(:0) can otherwise make
// ADB register our service as an offline emulator before its handshake fails.
// The adjacent console/boundary ports are reserved here too.
func Listen() (net.Listener, error) {
	return listenTCP(net.Listen)
}

func listenTCP(listen func(string, string) (net.Listener, error)) (net.Listener, error) {
	for attempt := 0; attempt < maxListenAttempts; attempt++ {
		l, err := listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		addr, ok := l.Addr().(*net.TCPAddr)
		if !ok {
			l.Close()
			return nil, fmt.Errorf("internal loopback listener has a non-TCP address")
		}
		if addr.Port < 5554 || addr.Port > 5586 {
			return l, nil
		}
		if err := l.Close(); err != nil {
			return nil, fmt.Errorf("close ADB scan-window listener: %w", err)
		}
	}
	return nil, fmt.Errorf("cannot allocate an internal loopback port outside ADB's scan window after %d attempts", maxListenAttempts)
}
