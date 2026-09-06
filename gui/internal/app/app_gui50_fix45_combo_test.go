package app

import (
	"context"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

// --- gui50-fix4+fix5 组合：扫码服务名匹配 + 配对成功顺带 tcpip 5555 ---

func gui50Fix45ManualPairingSvc() discovery.MdnsService {
	return discovery.MdnsService{
		Type: "_adb-tls-pairing._tcp",
		Name: "adb-601c9f08-KWqpio",
		Addr: "192.168.1.2:37033",
		Mode: discovery.MdnsModePairing,
	}
}

func gui50Fix45QrPairingSvc() discovery.MdnsService {
	return discovery.MdnsService{
		Type: "_adb-tls-pairing._tcp",
		Name: pairQrServiceName,
		Addr: "192.168.1.2:37033",
		Mode: discovery.MdnsModePairing,
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待超时：%s", msg)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func gui50Fix45EntryHasAddr(e DeviceEntry, addr, mode string) bool {
	for i := range e.Addrs {
		if e.Addrs[i].Addr == addr && (mode == "" || e.Addrs[i].Mode == mode) {
			return true
		}
	}
	return false
}

// fix5：手动配对界面广播（adb-xxx 命名），即使本次新增也绝不触发自动配对。
func TestGui50Fix5ManualPairingBroadcastDoesNotAutoPair(t *testing.T) {
	a, _ := newWirelessApp()
	ctx := context.Background()
	a.onMdnsTrackEvents(ctx, adb.MdnsTrackEvents{Snapshot: nil, First: true})
	if err := a.PairConnect("__qr_open__", "", "", "", ""); err != nil {
		t.Fatal(err)
	}

	svc := gui50Fix45ManualPairingSvc()
	a.onMdnsTrackEvents(ctx, adb.MdnsTrackEvents{
		Snapshot: []discovery.MdnsService{svc},
		First:    false,
	})
	time.Sleep(50 * time.Millisecond)

	st := a.Snapshot().PairStatus
	if st == nil || st.Phase != PairPhaseIdle || st.Mode != PairModeQR {
		t.Fatalf("手动界面 pairing 广播不得触发自动配对: %+v", st)
	}
}

// fix5：ADBQR 扫码广播（服务名==pairQrServiceName）触发且只一次。
func TestGui50Fix5QrPairingBroadcastTriggersOnce(t *testing.T) {
	a, _ := newWirelessApp()
	ctx := context.Background()
	pairCalls := gui50Fix3InstallSuccessFakes(a)

	a.onMdnsTrackEvents(ctx, adb.MdnsTrackEvents{Snapshot: nil, First: true})
	if err := a.PairConnect("__qr_open__", "", "", "", ""); err != nil {
		t.Fatal(err)
	}

	svc := gui50Fix45QrPairingSvc()
	a.onMdnsTrackEvents(ctx, adb.MdnsTrackEvents{
		Snapshot: []discovery.MdnsService{svc},
		First:    false,
	})
	st := waitPairPhase(t, a, PairPhaseSuccess)
	if st.Mode != PairModeQR {
		t.Fatalf("扫码广播应走二维码自动配对: %+v", st)
	}
	if *pairCalls != 1 {
		t.Fatalf("扫码广播应只触发一次 pair，got %d", *pairCalls)
	}
}

// fix4：配对成功路径（pair→connect→建档）后会异步 adb tcpip 5555，
// 档案同时保留 TLS 地址，并新增 ip:5555 tcpip 地址。
