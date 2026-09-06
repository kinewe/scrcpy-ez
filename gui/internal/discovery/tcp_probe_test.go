package discovery

import (
	"context"
	"net"
	"testing"
)

// TcpProbe 是纯 TCP 握手探测：端口活着 → true；连接被拒/失败 → false。
// 不调用 adb.exe（Connector.AdbPath 留空也不影响），不建立 adb transport。
func TestTcpProbeDialResult(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("本地监听失败: %v", err)
	}
	addr := ln.Addr().String()
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	c := New("")
	if !c.TcpProbe(context.Background(), addr) {
		t.Fatalf("端口在监听时应探测通: %s", addr)
	}
	_ = ln.Close()
	<-acceptDone
	if c.TcpProbe(context.Background(), addr) {
		t.Fatalf("监听已关闭时应探测不通: %s", addr)
	}
}

// TcpProbeFn 注入点：测试注入直接采用，不触达真实网络。
func TestTcpProbeFnInjection(t *testing.T) {
	c := New("")
	called := false
	c.TcpProbeFn = func(ctx context.Context, addr string) bool {
		called = true
		return addr == "192.168.31.1:5555"
	}
	if !c.TcpProbe(context.Background(), "192.168.31.1:5555") {
		t.Fatal("注入函数应直接返回 true")
	}
	if !called {
		t.Fatal("注入函数应被调用")
	}
	c.TcpProbeFn = func(ctx context.Context, addr string) bool { return false }
	if c.TcpProbe(context.Background(), "192.168.31.1:5555") {
		t.Fatal("注入函数应直接返回 false")
	}
}

// 已取消 ctx：不发起无意义拨号，直接返回 false。
func TestTcpProbeCanceledContext(t *testing.T) {
	c := New("")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c.TcpProbe(ctx, "192.168.31.1:5555") {
		t.Fatal("已取消 ctx 应返回 false")
	}
}
