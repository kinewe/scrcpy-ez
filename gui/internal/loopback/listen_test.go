package loopback

import (
	"errors"
	"net"
	"testing"
)

type testListener struct {
	net.Listener
	port   int
	closed bool
}

func (l *testListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: l.port}
}

func (l *testListener) Close() error { l.closed = true; return nil }

func TestListenSkipsEntireADBWindowAndClosesRejectedSockets(t *testing.T) {
	var sockets []*testListener
	l, err := listenTCP(func(network, address string) (net.Listener, error) {
		if network != "tcp" || address != "127.0.0.1:0" {
			t.Fatalf("unexpected bind: %s %s", network, address)
		}
		port := 5554 + len(sockets)
		socket := &testListener{port: port}
		sockets = append(sockets, socket)
		return socket, nil
	})
	if err != nil || len(sockets) != 34 || l != sockets[33] || sockets[33].closed {
		t.Fatalf("expected first safe port 5587: listener=%v error=%v attempts=%d", l, err, len(sockets))
	}
	for _, socket := range sockets[:33] {
		if !socket.closed {
			t.Fatalf("rejected port %d leaked", socket.port)
		}
	}
}

func TestListenAllowsPortsOnBothSidesOfWindow(t *testing.T) {
	for _, port := range []int{1024, 5553, 5587, 49152, 65535} {
		socket := &testListener{port: port}
		l, err := listenTCP(func(string, string) (net.Listener, error) { return socket, nil })
		if err != nil || l != socket || socket.closed {
			t.Fatalf("safe port %d rejected: %v", port, err)
		}
	}
}

func TestListenBoundsRetriesAndPreservesBindErrors(t *testing.T) {
	var sockets []*testListener
	l, err := listenTCP(func(string, string) (net.Listener, error) {
		socket := &testListener{port: 5581}
		sockets = append(sockets, socket)
		return socket, nil
	})
	if l != nil || err == nil || len(sockets) != maxListenAttempts {
		t.Fatalf("unbounded retry or unsafe endpoint: %v %v %d", l, err, len(sockets))
	}
	for _, socket := range sockets {
		if !socket.closed {
			t.Fatal("retry exhaustion leaked a listener")
		}
	}
	want := errors.New("bind unavailable")
	l, err = listenTCP(func(string, string) (net.Listener, error) { return nil, want })
	if l != nil || !errors.Is(err, want) {
		t.Fatalf("bind error lost: %v %v", l, err)
	}
}

func TestListenCreatesUsablePrivateEndpoint(t *testing.T) {
	l, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	addr := l.Addr().(*net.TCPAddr)
	if !addr.IP.IsLoopback() || addr.Port >= 5554 && addr.Port <= 5586 {
		t.Fatalf("unsafe internal endpoint: %v", addr)
	}
	c, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	accepted, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	accepted.Close()
}
