package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/discovery"
)

// gui52-fix8：无线配对成功后开启设备 tcpip 5555 —— 幂等检测 + 失败不阻断。
// 只开端口；不做 connect 探测/写档（地址权威=mDNS）。

type fix8Recorder struct {
	mu         sync.Mutex
	getprop    []string
	tcpip      []string
	tcpipErr   error
	defaultPort string
}

func (r *fix8Recorder) getpropFn(ctx context.Context, serial, prop string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.getprop = append(r.getprop, serial+":"+prop)
	if prop == "service.adb.tcp.port" {
		// 默认模拟「未开」（空），测试可覆盖
		return r.defaultPort, nil
	}
	return "", nil
}

func (r *fix8Recorder) tcpipFn(ctx context.Context, serial, port string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tcpip = append(r.tcpip, serial+":"+port)
	return r.tcpipErr
}

func TestGui52Fix8TcpipAlreadyOpenSkips(t *testing.T) {
	a, _ := newWirelessApp()
	rec := &fix8Recorder{defaultPort: "5555"}
	a.pairOps.getpropFn = rec.getpropFn
	a.pairOps.tcpipFn = rec.tcpipFn
	// 前置：配对尾段已完成建档（生产语义：PairArchive 先于 ensure）
	a.profiles.PairArchive("REDMI K80", "601c9f08", "192.168.1.2:33895", "adb-601c9f08-KWqpio", "REDMI K80", "24117RK2CC")

	a.ensurePairTcpip5555("REDMI K80", "192.168.1.2", "192.168.1.2:33895")
	time.Sleep(50 * time.Millisecond)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.tcpip) != 0 {
		t.Fatalf("已开 5555 时不应再执行 adb tcpip: %v", rec.tcpip)
	}
	if len(rec.getprop) != 1 {
		t.Fatalf("应执行一次 getprop 检测: %v", rec.getprop)
	}
	// gui52-fix9：已开 → 直接写档 ip:5555 active
	e, ok := a.profiles.Entry("REDMI K80")
	if !ok || !gui50Fix45EntryHasAddr(e, "192.168.1.2:5555", ModeTcpip) {
		t.Fatalf("已开 5555 应直写 ip:5555 入档: %+v", e)
	}
	for i := range e.Addrs {
		if e.Addrs[i].Addr == "192.168.1.2:5555" && e.Addrs[i].State != AddrStateActive {
			t.Fatalf("5555 应 active: %+v", e.Addrs[i])
		}
	}
}

func TestGui52Fix8TcpipNotOpenEnableOnce(t *testing.T) {
	a, _ := newWirelessApp()
	rec := &fix8Recorder{defaultPort: ""}
	a.pairOps.getpropFn = rec.getpropFn
	a.pairOps.tcpipFn = rec.tcpipFn
	a.profiles.PairArchive("REDMI K80", "601c9f08", "192.168.1.2:33895", "adb-601c9f08-KWqpio", "REDMI K80", "24117RK2CC")

	a.ensurePairTcpip5555("REDMI K80", "192.168.1.2", "192.168.1.2:33895")
	time.Sleep(50 * time.Millisecond)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.tcpip) != 1 || rec.tcpip[0] != "192.168.1.2:33895:5555" {
		t.Fatalf("未开时应执行一次 adb tcpip 5555: %v", rec.tcpip)
	}
	// gui52-fix9：开启成功 → 直写 ip:5555 active
	e, ok := a.profiles.Entry("REDMI K80")
	if !ok || !gui50Fix45EntryHasAddr(e, "192.168.1.2:5555", ModeTcpip) {
		t.Fatalf("tcpip 成功应写 ip:5555 入档: %+v", e)
	}
}

func TestGui52Fix8TcpipFailDoesNotBlock(t *testing.T) {
	a, _ := newWirelessApp()
	rec := &fix8Recorder{defaultPort: "", tcpipErr: errors.New("device offline")}
	a.pairOps.getpropFn = rec.getpropFn
	a.pairOps.tcpipFn = rec.tcpipFn

	a.ensurePairTcpip5555("REDMI K80", "192.168.1.2", "192.168.1.2:33895") // 不 panic 即通过；失败仅日志
	time.Sleep(50 * time.Millisecond)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.tcpip) != 1 {
		t.Fatalf("应尝试一次 tcpip: %v", rec.tcpip)
	}
}

func TestGui52Fix8TcpipNilOpsNoop(t *testing.T) {
	// Version=test 时 tcpipFn/getpropFn 不挂（newTestApp 默认）→ no-op 不 panic。
	a, _ := newWirelessApp()
	a.ensurePairTcpip5555("REDMI K80", "192.168.1.2", "192.168.1.2:33895")
}

func TestGui52Fix8PairFlowTriggersTcpip(t *testing.T) {
	// 配对成功尾段应触发 ensurePairTcpip5555（异步）——用真实配对链验证调用挂钩。
	a, _ := newWirelessApp()
	rec := &fix8Recorder{defaultPort: ""}
	a.pairOps.getpropFn = rec.getpropFn
	a.pairOps.tcpipFn = rec.tcpipFn
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		return "Successfully paired to 192.168.1.2:33895", nil
	}
	a.pairOps.connectFn = func(ctx context.Context, addr string) (string, error) {
		return "", nil
	}
	a.pairOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		if prop == "ro.product.marketname" {
			return "REDMI K80", nil
		}
		rec.mu.Lock()
		defer rec.mu.Unlock()
		rec.getprop = append(rec.getprop, serial+":"+prop)
		return "", nil
	}
	a.pairOps.mdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return pairConnectSvcs(), nil
	}
	// 注入 fake tcpip（覆盖上面的 getpropFn 后仍保留）
	a.pairOps.tcpipFn = rec.tcpipFn

	svc := seedPending(t, a, pairConnectSvcs())
	if svc.Addr != "192.168.31.99:33895" {
		t.Fatalf("待配对卡应为 TLS 连接服务: %+v", svc)
	}
	if err := a.PairConnect(svc.Key, "192.168.1.2", "37033", "33895", "123456"); err != nil {
		t.Fatal(err)
	}
	waitPairPhase(t, a, PairPhaseSuccess)

	waitFor(t, 2*time.Second, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return len(rec.tcpip) == 1 && strings.HasPrefix(rec.tcpip[0], "192.168.1.2:")
	}, "配对成功应触发 tcpip 5555 开启")
	// gui52-fix9：配对成功 → 5555 立即入档 active
	e2, ok2 := a.profiles.Entry("REDMI K80")
	if !ok2 || !gui50Fix45EntryHasAddr(e2, "192.168.1.2:5555", ModeTcpip) {
		t.Fatalf("配对成功后 5555 应立即入档: %+v", e2)
	}
}
