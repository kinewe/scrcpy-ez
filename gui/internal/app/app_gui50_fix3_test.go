package app

import (
	"context"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

// --- gui50-fix3：二维码自动配对防误触发 + 失败态二维码保留 ---

func gui50Fix3PairingSvc() discovery.MdnsService {
	return discovery.MdnsService{
		Type: "_adb-tls-pairing._tcp",
		Name: pairQrServiceName,
		Addr: "192.168.1.2:37033",
		Mode: discovery.MdnsModePairing,
	}
}

func gui50Fix3InstallSuccessFakes(a *App) *int {
	pairCalls := 0
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		pairCalls++
		return "Successfully paired to " + ip + ":" + port, nil
	}
	a.pairOps.connectFn = func(ctx context.Context, addr string) (string, error) {
		return "connected to " + addr, nil
	}
	a.pairOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		switch prop {
		case "ro.product.marketname":
			return "REDMI K80", nil
		case "ro.product.manufacturer":
			return "Xiaomi", nil
		case "ro.product.model":
			return "24117RK2CC", nil
		}
		return "", nil
	}
	a.pairOps.mdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return []discovery.MdnsService{
			{Type: "_adb-tls-connect._tcp", Name: "adb-601c9f08-KWqpio", Addr: "192.168.1.2:33895", Mode: discovery.MdnsModeTls},
		}, nil
	}
	return &pairCalls
}

// 稳定 pairing 广播（非本次新增）+ 二维码已生成 → 不得自动配对。
func TestGui50Fix3StablePairingDoesNotAutoPair(t *testing.T) {
	a, _ := newWirelessApp()
	ctx := context.Background()
	svc := gui50Fix3PairingSvc()

	// 首块建立稳定快照基线。
	a.onMdnsTrackEvents(ctx, adb.MdnsTrackEvents{
		Snapshot: []discovery.MdnsService{svc},
		First:    true,
	})

	if err := a.PairConnect("__qr_open__", "", "", "", ""); err != nil {
		t.Fatal(err)
	}

	// 后续快照内容没有变化：本地 diff 的 added 为空，稳定老广播不应触发。
	a.onMdnsTrackEvents(ctx, adb.MdnsTrackEvents{
		Snapshot: []discovery.MdnsService{svc},
		First:    false,
	})
	time.Sleep(50 * time.Millisecond)

	st := a.Snapshot().PairStatus
	if st == nil || st.Phase != PairPhaseIdle || st.Mode != PairModeQR {
		t.Fatalf("稳定 pairing 广播不应触发自动配对: %+v", st)
	}
	if a.pairQR == nil {
		t.Fatal("二维码应仍存活")
	}
}

// added 含新 pairing 广播 + 二维码已生成 → 触发自动配对，且只触发一次。
func TestGui50Fix3NewPairingBroadcastTriggersOnce(t *testing.T) {
	a, _ := newWirelessApp()
	ctx := context.Background()
	pairCalls := gui50Fix3InstallSuccessFakes(a)

	// 先发空首块，避免后续事件被当作首块基线。
	a.onMdnsTrackEvents(ctx, adb.MdnsTrackEvents{Snapshot: nil, First: true})
	if err := a.PairConnect("__qr_open__", "", "", "", ""); err != nil {
		t.Fatal(err)
	}

	svc := gui50Fix3PairingSvc()
	a.onMdnsTrackEvents(ctx, adb.MdnsTrackEvents{
		Snapshot: []discovery.MdnsService{svc},
		First:    false,
	})

	st := waitPairPhase(t, a, PairPhaseSuccess)
	if st.Mode != PairModeQR {
		t.Fatalf("自动配对应保留二维码路线: %+v", st)
	}
	if *pairCalls != 1 {
		t.Fatalf("新增广播应只触发一次 pair，got %d", *pairCalls)
	}

	// 再发一次相同快照（稳定非新增），确认不会二次触发。
	a.onMdnsTrackEvents(ctx, adb.MdnsTrackEvents{
		Snapshot: []discovery.MdnsService{svc},
		First:    false,
	})
	time.Sleep(50 * time.Millisecond)
	if *pairCalls != 1 {
		t.Fatalf("稳定广播不得二次触发 pair，got %d", *pairCalls)
	}
}

// 首块快照（First=true）即使带全量 pairing 且二维码已生成，也不得误触发。
func TestGui50Fix3FirstFullPairingDoesNotAutoPair(t *testing.T) {
	a, _ := newWirelessApp()
	ctx := context.Background()
	svc := gui50Fix3PairingSvc()

	if err := a.PairConnect("__qr_open__", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	// 二维码已开的极端首块：全量列表不视为“新增扫码”。
	a.onMdnsTrackEvents(ctx, adb.MdnsTrackEvents{
		Snapshot: []discovery.MdnsService{svc},
		First:    true,
	})
	time.Sleep(50 * time.Millisecond)

	st := a.Snapshot().PairStatus
	if st == nil || st.Phase != PairPhaseIdle || st.Mode != PairModeQR {
		t.Fatalf("首块全量 pairing 不应触发自动配对: %+v", st)
	}
}

// 自动配对失败后，pairQR 仍存活且失败态 qrText 非空（二维码区不空白）。
func TestGui50Fix3AutoPairFailureKeepsQr(t *testing.T) {
	a, _ := newWirelessApp()
	ctx := context.Background()

	a.onMdnsTrackEvents(ctx, adb.MdnsTrackEvents{Snapshot: nil, First: true})
	if err := a.PairConnect("__qr_open__", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	// 模拟扫码后码不匹配。pairFn 成功返回但输出为 wrong password → PairErrCode。
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		return "Failed: Wrong password or connection was dropped.", nil
	}

	svc := gui50Fix3PairingSvc()
	a.onMdnsTrackEvents(ctx, adb.MdnsTrackEvents{
		Snapshot: []discovery.MdnsService{svc},
		First:    false,
	})

	st := waitPairPhase(t, a, PairPhaseFailed)
	if st.Mode != PairModeQR {
		t.Fatalf("失败态应保留二维码路线: %+v", st)
	}
	if st.ErrCode != PairErrCode {
		t.Fatalf("码不匹配应映射为 PairErrCode: %+v", st)
	}
	if st.QrText == "" {
		t.Fatalf("失败态二维码文本不应为空: %+v", st)
	}
	if st.QrExpireAt <= 0 {
		t.Fatalf("失败态二维码失效时间不应为空: %+v", st)
	}
	a.mu.Lock()
	qrAlive := a.pairQR != nil
	a.mu.Unlock()
	if !qrAlive {
		t.Fatal("自动配对失败后 pairQR 应存活/重新生成")
	}
}
