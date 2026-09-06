package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// gui52-fix14：配对遮罩——配对成功 → 「连接中…」→ 任一 transport 连续 device
// 满 2s 清遮罩（波动重置）｜10s 兜底｜清除时并行 connect 档案地址刷 active/stale。

type fix14Recorder struct {
	mu    sync.Mutex
	conns []string
	fail  map[string]error
	failSuppress bool
}

func (r *fix14Recorder) connectFn(ctx context.Context, addr string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.conns = append(r.conns, addr)
	if err, ok := r.fail[addr]; ok {
		return "", err
	}
	return "", nil
}

func (r *fix14Recorder) last() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.conns...)
}

func seedPadArchive(t *testing.T, a *App) {
	t.Helper()
	a.profiles.mu.Lock()
	a.profiles.data.Devices["Xiaomi Pad 8 Pro"] = &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		TlsGuid:    "adb-a743e1df-On9v2R",
		Addrs: []AddrEntry{
			{Addr: "192.168.31.162:5555", State: AddrStateActive, Mode: ModeTcpip},
			{Addr: "192.168.31.162:40989", State: AddrStateStale, Mode: ModeTls},
		},
		Profiles: DefaultProfile(),
	}
	a.profiles.data.DeviceOrder = []string{"Xiaomi Pad 8 Pro"}
	a.profiles.mu.Unlock()
}

func TestGui52Fix14ShieldStartConnectInjection(t *testing.T) {
	a, _ := newWirelessApp()
	rec := &fix14Recorder{fail: map[string]error{}}
	a.pairOps.connectFn = rec.connectFn
	seedPadArchive(t, a)

	a.pairShieldStart("Xiaomi Pad 8 Pro", "192.168.31.162")
	time.Sleep(80 * time.Millisecond)
	if got := rec.last(); len(got) == 0 || got[0] != "192.168.31.162:5555" {
		t.Fatalf("遮罩开始应 connect 注入 5555: %v", got)
	}
	// 遮罩应已置位
	a.teachMu.Lock()
	_, active := a.pairing["Xiaomi Pad 8 Pro"]
	a.teachMu.Unlock()
	if !active {
		t.Fatal("遮罩应已置位")
	}
}

func TestGui52Fix14ShieldStableClear(t *testing.T) {
	a, _ := newWirelessApp()
	rec := &fix14Recorder{fail: map[string]error{}}
	a.pairOps.connectFn = rec.connectFn
	seedPadArchive(t, a)

	a.pairShieldStart("Xiaomi Pad 8 Pro", "192.168.31.162")
	// 任一 transport device → 2s 稳定 → 清遮罩
	d := adb.Device{Serial: "192.168.31.162:5555", State: "device", ConnType: "wifi"}
	if !a.pairWirelessStable([]adb.Device{d}, "Xiaomi Pad 8 Pro") {
		t.Fatal("pairWirelessStable 应识别 5555 device")
	}
	if a.pairWirelessStable([]adb.Device{{Serial: "192.168.31.162:5555", State: "offline", ConnType: "wifi"}}, "Xiaomi Pad 8 Pro") {
		t.Fatal("offline 不应稳定")
	}
	a.pairStabilityUpdate([]adb.Device{d})
	waitFor(t, 4*time.Second, func() bool {
		a.teachMu.Lock()
		_, active := a.pairing["Xiaomi Pad 8 Pro"]
		a.teachMu.Unlock()
		return !active
	}, "连续 device 满 2s 应清遮罩")
	// 清除后应触发 probe（并行 connect 档案地址：5555+40989）
	waitFor(t, 3*time.Second, func() bool {
		got := rec.last()
		found := false
		for _, c := range got {
			if c == "192.168.31.162:40989" {
				found = true
			}
		}
		return found
	}, "清除后应 probe 档案地址")
}

func TestGui52Fix14ShieldWaveResets(t *testing.T) {
	a, _ := newWirelessApp()
	rec := &fix14Recorder{fail: map[string]error{}}
	a.pairOps.connectFn = rec.connectFn
	seedPadArchive(t, a)

	a.pairShieldStart("Xiaomi Pad 8 Pro", "192.168.31.162")
	// 波动序列：device → offline（tcpip 重置期）→ device —— 遮罩保持
	a.pairStabilityUpdate([]adb.Device{{Serial: "192.168.31.162:5555", State: "device", ConnType: "wifi"}})
	time.Sleep(300 * time.Millisecond)
	a.pairStabilityUpdate([]adb.Device{{Serial: "192.168.31.162:5555", State: "offline", ConnType: "wifi"}})
	time.Sleep(300 * time.Millisecond)
	a.pairStabilityUpdate([]adb.Device{{Serial: "192.168.31.162:5555", State: "device", ConnType: "wifi"}})
	time.Sleep(1500 * time.Millisecond) // 不足 2s 连续稳定（波动重置了：总 device 时间 <2s 连续）
	a.teachMu.Lock()
	_, active := a.pairing["Xiaomi Pad 8 Pro"]
	a.teachMu.Unlock()
	if !active {
		t.Fatal("波动期遮罩应保持（未满 2s 连续稳定）")
	}
	waitFor(t, 4*time.Second, func() bool {
		a.teachMu.Lock()
		_, active := a.pairing["Xiaomi Pad 8 Pro"]
		a.teachMu.Unlock()
		return !active
	}, "波动后重新满 2s 应清遮罩")
}

func TestGui52Fix14ShieldProbeStatus(t *testing.T) {
	a, _ := newWirelessApp()
	rec := &fix14Recorder{fail: map[string]error{"192.168.31.162:40989": errors.New("dead")}}
	a.pairOps.connectFn = rec.connectFn
	seedPadArchive(t, a)

	a.pairProbeAllAddrs("Xiaomi Pad 8 Pro")
	waitFor(t, 3*time.Second, func() bool {
		e, ok := a.profiles.Entry("Xiaomi Pad 8 Pro")
		if !ok {
			return false
		}
		for i := range e.Addrs {
			if e.Addrs[i].Addr == "192.168.31.162:5555" && e.Addrs[i].State != AddrStateActive {
				return false
			}
			if e.Addrs[i].Addr == "192.168.31.162:40989" && e.Addrs[i].State != AddrStateStale {
				return false
			}
		}
		return true
	}, "probe 应刷状态：通→active，不通→stale")
}

func TestGui52Fix14ShieldSynthCard(t *testing.T) {
	a, _ := newWirelessApp()
	rec := &fix14Recorder{fail: map[string]error{}}
	a.pairOps.connectFn = rec.connectFn
	seedPadArchive(t, a)

	a.pairShieldStart("Xiaomi Pad 8 Pro", "192.168.31.162")
	out := a.shieldPairing([]adb.Device{
		{Serial: "192.168.31.162:5555", State: "device", ConnType: "wifi"},
	})
	if len(out) != 1 {
		t.Fatalf("遮罩期应合成单卡: %+v", out)
	}
	if !out[0].Connecting || !out[0].Pairing || out[0].Name != "Xiaomi Pad 8 Pro" {
		t.Fatalf("合成卡应 Connecting=true + Pairing=true + 档案名: %+v", out[0])
	}
}
