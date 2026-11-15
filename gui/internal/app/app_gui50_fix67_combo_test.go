package app

import (
	"context"
	"errors"
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
			return "12345TESTA", nil
		}
		return "", nil
	}
}

// A supplied code must never use the historical trusted-connect shortcut.
func TestExplicitCodeAlwaysPairsBeforeConnect(t *testing.T) {
	for _, auto := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual", true: "auto"}[auto], func(t *testing.T) {
			a, _ := newWirelessApp()
			var p PendingDevice
			if auto {
				p = seedPending(t, a, pairConnectSvcs())
			}
			pairCalls, connCalls := 0, 0
			a.pairOps.pairFn = func(context.Context, string, string, string) (string, error) {
				pairCalls++
				return "Failed: Wrong password or connection was dropped.", errors.New("wrong code")
			}
			a.pairOps.connectFn = func(context.Context, string) (string, error) { connCalls++; return "already connected", nil }
			if err := a.PairConnect(p.Key, "192.0.2.99", "37033", "33895", "999999"); err != nil {
				t.Fatal(err)
			}
			st := waitPairPhase(t, a, PairPhaseFailed)
			if st.ErrCode != PairErrCode || pairCalls != 1 || connCalls != 0 {
				t.Fatalf("code bypassed: %+v pair=%d connect=%d", st, pairCalls, connCalls)
			}
		})
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
		Addr: "192.0.2.197:45531",
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
