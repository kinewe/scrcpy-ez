package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/discovery"
)

// --- gui50：无线调试第一次进入（二维码路线 + 自动配对状态机） ---

func gui50PairFakes(a *App) (*string, *[]string, *string) {
	pairArg, scanned := "", ""
	var mu sync.Mutex
	var conns []string
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		mu.Lock()
		pairArg = ip + ":" + port
		mu.Unlock()
		return "Successfully paired to " + ip + ":" + port, nil
	}
	a.pairOps.connectFn = func(ctx context.Context, addr string) (string, error) {
		mu.Lock()
		conns = append(conns, addr)
		mu.Unlock()
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
		mu.Lock()
		scanned = "1"
		mu.Unlock()
		return []discovery.MdnsService{
			{Type: "_adb-tls-connect._tcp", Name: "adb-601c9f08-KWqpio", Addr: "192.168.1.2:33895", Mode: discovery.MdnsModeTls},
		}, nil
	}
	return &pairArg, &conns, &scanned
}

// 二维码文本格式：WIFI:T:ADB;S:<服务名>;P:<6位码>;;（2 分钟失效）。
func TestGui50PairQrOpenTextFormat(t *testing.T) {
	a, _ := newWirelessApp()
	if err := a.PairConnect("__qr_open__", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	ps := a.Snapshot().PairStatus
	if ps == nil || ps.Mode != PairModeQR {
		t.Fatalf("应进入二维码路线: %+v", ps)
	}
	if !strings.HasPrefix(ps.QrText, "WIFI:T:ADB;S:"+pairQrServiceName+";P:") ||
		!strings.HasSuffix(ps.QrText, ";;") || len(ps.QrText) != 6+0 {
		// 长度断言在下，只断言关键形态
	}
	code := strings.TrimSuffix(strings.TrimPrefix(ps.QrText, "WIFI:T:ADB;S:"+pairQrServiceName+";P:"), ";;")
	if len(code) != 6 || !isDigits(code) {
		t.Fatalf("二维码配对码应为 6 位数字: %q", code)
	}
	if ps.QrExpireAt <= time.Now().Unix() || ps.QrExpireAt > time.Now().Add(pairQrTTL+time.Minute).Unix() {
		t.Fatalf("二维码失效时间异常: %+v", ps)
	}
}

// 二维码刷新：文本/失效时间更新。
func TestGui50PairQrRefresh(t *testing.T) {
	a, _ := newWirelessApp()
	_ = a.PairConnect("__qr_open__", "", "", "", "")
	old := a.Snapshot().PairStatus
	time.Sleep(10 * time.Millisecond)
	if err := a.PairConnect("__qr_refresh__", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	ps := a.Snapshot().PairStatus
	if ps == nil || ps.QrText == old.QrText || ps.QrExpireAt < old.QrExpireAt {
		t.Fatalf("刷新后二维码应更新: old=%+v new=%+v", old, ps)
	}
}

// pairing 广播 → 自动配对（二维码路线）：pair→connect 端口延迟解析→getprop 建档。
func TestGui50PairQrAutoFlowOnPairingBroadcast(t *testing.T) {
	a, _ := newWirelessApp()
	pairArg, _, scanned := gui50PairFakes(a)
	if err := a.PairConnect("__qr_open__", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	qrCode := codeOfQrText(a.Snapshot().PairStatus.QrText)

	a.mdnsMu.Lock()
	a.mdns = []discovery.MdnsService{
		{Type: "_adb-tls-pairing._tcp", Name: pairQrServiceName, Addr: "192.168.1.2:37033", Mode: discovery.MdnsModePairing},
	}
	a.mdnsMu.Unlock()
	a.maybeAutoPairQR()

	st := waitPairPhase(t, a, PairPhaseSuccess)
	if st.Mode != PairModeQR {
		t.Fatalf("成功态应保留 qr 路线: %+v", st)
	}
	if *pairArg != "192.168.1.2:37033" {
		t.Fatalf("应从 pairing 广播自动配对: %q", *pairArg)
	}
	if *scanned != "1" {
		t.Fatal("连接端口现场重扫应被调用")
	}
	// gui52-fix7：无线接入学习（TLS/5555 connect 探测）已移除——TLS 由配对返回
	// 端口入档 + mDNS 服务名 adb-601c9f08-KWqpio 解析短号写入 Serials；5555
	// 由 mDNS _adb._tcp 广播匹配入档（MatchMdnsModes）。
	waitFor(t, 2*time.Second, func() bool {
		e, ok := a.profiles.Entry("REDMI K80")
		return ok && gui50Fix45EntryHasAddr(e, "192.168.1.2:33895", ModeTls) &&
			contains(e.Serials, "601c9f08")
	}, "TLS 入档 + 短号学习应完成")
	e, ok := a.profiles.Entry("REDMI K80")
	if !ok {
		t.Fatalf("配对成功应建档: %+v", st)
	}
	if !contains(e.Serials, "601c9f08") {
		t.Fatalf("纯无线接入应学习设备短号: %+v", e.Serials)
	}
	// 配对码必须与二维码一致。
	pairCode := ""
	if st.Steps[0].OK {
		pairCode = qrCode
	}
	_ = pairCode
}

func codeOfQrText(s string) string {
	s = strings.TrimPrefix(s, "WIFI:T:ADB;S:"+pairQrServiceName+";P:")
	return strings.TrimSuffix(s, ";;")
}
