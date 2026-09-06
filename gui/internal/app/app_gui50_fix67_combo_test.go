package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/discovery"
)

// --- gui50-fix6+fix7：connect 先行快路径 + 二维码到期自动刷新 ---

func gui50Fix67InstallGetpropFakes(a *App) {
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
}

// fix6：connect 成功（信任仍在）→ 跳过 adb pair，直接 connect 成功态/建档。
// gui52：成功尾段一次性学习并行探测 TLS+5555（各 1 次 connect），共 3 次。
func TestGui50Fix6ConnectFastPathSkipsPair(t *testing.T) {
	a, _ := newWirelessApp()
	a.pairFastConnect = true
	gui50Fix67InstallGetpropFakes(a)

	var mu sync.Mutex
	pairCalls, connCalls := 0, 0
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		mu.Lock()
		pairCalls++
		mu.Unlock()
		return "", errors.New("fast path 不应调用 pair")
	}
	a.pairOps.connectFn = func(ctx context.Context, addr string) (string, error) {
		mu.Lock()
		connCalls++
		mu.Unlock()
		return "connected to " + addr, nil
	}

	if err := a.PairConnect("", "192.168.1.2", "37033", "33895", "123456"); err != nil {
		t.Fatal(err)
	}
	st := waitPairPhase(t, a, PairPhaseSuccess)
	if st == nil || st.Device == nil || st.Device.Serial != "192.168.1.2:33895" {
		t.Fatalf("快路径成功态异常: %+v", st)
	}
	waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return connCalls == 2
	}, "fix6 快路径 connect + 遮罩注入应完成（学习探测已移除）")
	mu.Lock()
	defer mu.Unlock()
	if pairCalls != 0 {
		t.Fatalf("connect 先行成功时不应调用 adb pair，got %d", pairCalls)
	}
	e, ok := a.profiles.Entry("REDMI K80")
	if !ok || !gui50Fix45EntryHasAddr(e, "192.168.1.2:33895", ModeTls) {
		t.Fatalf("快路径应建立 TLS 档案: %+v", e)
	}
}

// fix6：自动发现目标同样走 connect 先行快路径（删档找回场景）。
// gui52-fix7：5555 地址由 mDNS _adb._tcp 广播匹配入档（不再 connect 学习）。
func TestGui50Fix6AutoConnectFastPathSkipsPair(t *testing.T) {
	a, _ := newWirelessApp()
	a.pairFastConnect = true
	gui50Fix67InstallGetpropFakes(a)
	p := seedPending(t, a, pairConnectSvcs())

	var mu sync.Mutex
	pairCalls, connCalls := 0, 0
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		mu.Lock()
		pairCalls++
		mu.Unlock()
		return "", errors.New("fast path 不应调用 pair")
	}
	a.pairOps.connectFn = func(ctx context.Context, addr string) (string, error) {
		mu.Lock()
		connCalls++
		mu.Unlock()
		return "connected to " + addr, nil
	}

	if err := a.PairConnect(p.Key, "", "", "", "123456"); err != nil {
		t.Fatal(err)
	}
	st := waitPairPhase(t, a, PairPhaseSuccess)
	if st == nil || st.Device == nil || st.Device.Serial != "192.168.31.99:33895" {
		t.Fatalf("自动快路径成功态异常: %+v", st)
	}
	waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return connCalls == 2
	}, "自动快路径 connect + 遮罩注入应完成（学习探测已移除）")
	mu.Lock()
	defer mu.Unlock()
	if pairCalls != 0 {
		t.Fatalf("自动快路径不应调用 adb pair，got %d", pairCalls)
	}
}

// fix6：connect 失败 → 回退原 pair→connect 流程，pair 被调用。
// gui52：成功尾段再并行探测 TLS+5555（额外 2 次 connect），共 4 次。
func TestGui50Fix6ConnectFailFallsBackToPair(t *testing.T) {
	a, _ := newWirelessApp()
	a.pairFastConnect = true
	gui50Fix67InstallGetpropFakes(a)

	var mu sync.Mutex
	pairCalls, connCalls := 0, 0
	failedFast := false
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		mu.Lock()
		pairCalls++
		mu.Unlock()
		return "Successfully paired to " + ip + ":" + port, nil
	}
	a.pairOps.connectFn = func(ctx context.Context, addr string) (string, error) {
		mu.Lock()
		connCalls++
		fail := addr == "192.168.1.2:33895" && !failedFast
		if fail {
			failedFast = true
		}
		mu.Unlock()
		if fail {
			return "connection refused", errors.New("fast connect fail")
		}
		return "connected to " + addr, nil
	}

	if err := a.PairConnect("", "192.168.1.2", "37033", "33895", "123456"); err != nil {
		t.Fatal(err)
	}
	st := waitPairPhase(t, a, PairPhaseSuccess)
	if st == nil || st.Device == nil || st.Device.Serial != "192.168.1.2:33895" {
		t.Fatalf("回退成功态异常: %+v", st)
	}
	waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return connCalls == 3
	}, "fast 失败回退 + 正式 connect + 遮罩注入应完成（学习探测已移除）")
	mu.Lock()
	defer mu.Unlock()
	if pairCalls != 1 {
		t.Fatalf("connect 失败后应回退调用 pair，got %d", pairCalls)
	}
}

// fix7：二维码过期后收到新增 ADBQR 扫码广播 → 后端自动重新生成二维码并忽略旧码，
// 不触发自动配对，新码可重扫。
func TestGui50Fix7ExpiredQrScanBroadcastRefreshesQrOnly(t *testing.T) {
	a, _ := newWirelessApp()
	if err := a.PairConnect("__qr_open__", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	pairCalls := 0
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		pairCalls++
		return "Successfully paired to " + ip + ":" + port, nil
	}

	a.mu.Lock()
	oldQr := a.pairQR.qrText()
	a.pairQR.expireAt = time.Now().Add(-time.Second)
	a.pair.QrExpireAt = a.pairQR.expireAt.Unix()
	a.mu.Unlock()

	svc := discovery.MdnsService{
		Type: "_adb-tls-pairing._tcp",
		Name: pairQrServiceName,
		Addr: "192.168.31.197:45531",
		Mode: discovery.MdnsModePairing,
	}
	a.maybeAutoPairQR([]discovery.MdnsService{svc})

	a.mu.Lock()
	newQr := a.pairQR.qrText()
	newExpire := a.pairQR.expireAt
	a.mu.Unlock()
	if newQr == oldQr {
		t.Fatalf("过期二维码收到扫码广播后应重新生成，old=%q new=%q", oldQr, newQr)
	}
	if time.Now().After(newExpire) {
		t.Fatalf("重新生成的二维码仍应有效: %v", newExpire)
	}
	if pairCalls != 0 {
		t.Fatalf("过期旧码不应触发自动配对，pair called %d", pairCalls)
	}
	st := a.Snapshot().PairStatus
	if st == nil || st.Phase != PairPhaseIdle || st.QrText != newQr {
		t.Fatalf("过期广播后应保持 idle 且展示新码: %+v", st)
	}
}
