package app

// gui47-fix F1'「先读 IP 后 tcpip + 无条件对齐（不再 connect）」测试。
// 覆盖：
//   - 读 IP 必须先于 tcpip 调用（顺序断言）；
//   - 读 IP 成功 + tcpip 成功 → 档案对齐 <ip>:5555，connect 不再被调用；
//   - 端口已 5555 + 读 IP 成功 → 仍对齐档案；
//   - 读 IP 失败 + tcpip 成功 → 不对齐；
//   - tcpip 失败 → 不对齐。
// 同时保留 gui47 的 TLS 开关感知用例（=0 打 stale / =1 或失败不动 / 无 TLS 不误伤）。

import (
	"context"
	"errors"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

func gui47UsbDev() adb.Device {
	return adb.Device{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "REDMI K80"}
}

// gui47Shell builds a fake shellFn: settings/ip route/ip addr are routed by args.
func gui47Shell(settings, route, addr string) func(ctx context.Context, serial string, args ...string) (string, error) {
	return func(ctx context.Context, serial string, args ...string) (string, error) {
		if len(args) == 0 {
			return "", nil
		}
		switch args[0] {
		case "settings":
			return settings, nil
		case "ip":
			if len(args) >= 2 {
				switch args[1] {
				case "route":
					return route, nil
				case "addr":
					return addr, nil
				}
			}
		}
		return "", nil
	}
}

// gui47Setup 构造一个带档案 identity 的插线学习环境；返回 App、connect 次数和
// 最后 connect 地址（connect 已从学习路径删除，测试用它断言从未调用）。
func gui47Setup(t *testing.T, addrs ...string) (*App, *int, *string) {
	t.Helper()
	a, _ := newTestApp()
	if len(addrs) > 0 {
		seedProfiles(a, map[string]*DeviceEntry{
			"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, addrs),
		})
	}
	connects := 0
	connected := ""
	a.pairOps.connectFn = func(ctx context.Context, addr string) (string, error) {
		connects++
		connected = addr
		return "connected", nil
	}
	return a, &connects, &connected
}

// TestGui47FixOrderReadIPBeforeTcpip：ip route / ip addr 必须先于 tcpip 调用。
func TestGui47FixOrderReadIPBeforeTcpip(t *testing.T) {
	a, connects, _ := gui47Setup(t, "203.0.113.9:5555")
	var calls []string
	a.teachOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		return "5554", nil
	}
	a.teachOps.tcpipFn = func(ctx context.Context, serial, port string) error {
		calls = append(calls, "tcpip")
		return nil
	}
	a.teachOps.shellFn = func(ctx context.Context, serial string, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "ip" {
			if args[1] == "route" {
				calls = append(calls, "route")
				return "192.168.1.0/24 dev wlan0 proto kernel scope link src 192.168.1.100\n", nil
			}
			if args[1] == "addr" {
				calls = append(calls, "addr")
				return "", nil
			}
		}
		return "1", nil
	}
	a.maybeTeachTcpip(context.Background(), []adb.Device{gui47UsbDev()})
	if len(calls) < 2 || calls[0] == "tcpip" {
		t.Fatalf("ip route 必须先于 tcpip 调用: %v", calls)
	}
	if *connects != 0 {
		t.Fatalf("学习路径不应调用 connect: %d", *connects)
	}
	e, ok := a.profiles.Entry("601c9f08")
	if !ok || !gui47HasAddr(e, "192.168.1.100:5555", "tcpip") {
		t.Fatalf("档案应已对齐: %+v", e.Addrs)
	}
}

// TestGui47FixLearnAlignNoConnect：读 IP 成功 + tcpip 成功 → 直接写档案，零 connect。
func TestGui47FixLearnAlignNoConnect(t *testing.T) {
	a, connects, _ := gui47Setup(t, "203.0.113.9:5555")
	a.teachOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		return "", nil
	}
	a.teachOps.tcpipFn = func(ctx context.Context, serial, port string) error { return nil }
	a.teachOps.shellFn = gui47Shell("1",
		"default via 192.168.1.1 dev wlan0\n192.168.1.0/24 dev wlan0 proto kernel scope link src 192.168.31.77\n",
		"")
	a.maybeTeachTcpip(context.Background(), []adb.Device{gui47UsbDev()})
	if *connects != 0 {
		t.Fatalf("学习路径不应调用 connect: %d", *connects)
	}
	e, ok := a.profiles.Entry("601c9f08")
	if !ok || !gui47HasAddr(e, "192.168.31.77:5555", "tcpip") {
		t.Fatalf("档案应写入读到的 IP: %+v", e.Addrs)
	}
	if gui47HasAddr(e, "203.0.113.9:5555", "tcpip") {
		t.Fatalf("旧同形态条目应让位: %+v", e.Addrs)
	}
}

// TestGui47FixPort5555StillAlign：端口已是 5555（不 tcpip）且读到 IP → 仍对齐档案。
func TestGui47FixPort5555StillAlign(t *testing.T) {
	a, connects, _ := gui47Setup(t, "203.0.113.9:5555")
	tcpips := 0
	a.teachOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		return "5555\n", nil
	}
	a.teachOps.tcpipFn = func(ctx context.Context, serial, port string) error {
		tcpips++
		return nil
	}
	a.teachOps.shellFn = gui47Shell("1",
		"192.168.1.0/24 dev wlan0 proto kernel scope link src 192.168.1.100\n",
		"")
	a.maybeTeachTcpip(context.Background(), []adb.Device{gui47UsbDev()})
	if tcpips != 0 {
		t.Fatalf("端口 5555 不应 tcpip: %d", tcpips)
	}
	if *connects != 0 {
		t.Fatalf("学习路径不应调用 connect: %d", *connects)
	}
	e, ok := a.profiles.Entry("601c9f08")
	if !ok || !gui47HasAddr(e, "192.168.1.100:5555", "tcpip") {
		t.Fatalf("端口已 5555 也应无条件对齐: %+v", e.Addrs)
	}
}

// TestGui47FixIPFailNoAlign：读 IP 失败 + tcpip 成功 → 不写档案（仅日志）。
func TestGui47FixIPFailNoAlign(t *testing.T) {
	a, connects, _ := gui47Setup(t, "203.0.113.9:5555")
	a.teachOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		return "", nil
	}
	a.teachOps.tcpipFn = func(ctx context.Context, serial, port string) error { return nil }
	a.teachOps.shellFn = gui47Shell("1", "", "127.0.0.0/8 dev lo src 127.0.0.1\n169.254.0.0/16 dev eth0 src 169.254.1.2\n")
	a.maybeTeachTcpip(context.Background(), []adb.Device{gui47UsbDev()})
	if *connects != 0 {
		t.Fatalf("学习路径不应调用 connect: %d", *connects)
	}
	e, ok := a.profiles.Entry("601c9f08")
	if !ok || gui47HasAddr(e, "192.168.1.100:5555", "tcpip") {
		t.Fatalf("读 IP 失败不应写档案: %+v", e.Addrs)
	}
}

// TestGui47FixTcpipFailNoAlign：tcpip 失败 → 即使读到 IP 也不对齐。
func TestGui47FixTcpipFailNoAlign(t *testing.T) {
	a, connects, _ := gui47Setup(t, "203.0.113.9:5555")
	a.teachOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		return "5554", nil
	}
	a.teachOps.tcpipFn = func(ctx context.Context, serial, port string) error {
		return errors.New("boom")
	}
	a.teachOps.shellFn = gui47Shell("1",
		"192.168.1.0/24 dev wlan0 proto kernel scope link src 192.168.1.100\n",
		"")
	a.maybeTeachTcpip(context.Background(), []adb.Device{gui47UsbDev()})
	if *connects != 0 {
		t.Fatalf("学习路径不应调用 connect: %d", *connects)
	}
	e, ok := a.profiles.Entry("601c9f08")
	if !ok || gui47HasAddr(e, "192.168.1.100:5555", "tcpip") {
		t.Fatalf("tcpip 失败不应写档案: %+v", e.Addrs)
	}
}

// --- gui47 TLS 开关感知用例（保留） ---

// TestGui47TlsSwitchZeroStale：adb_wifi_enabled=0 → TLS active 条目 Stale=true。
func TestGui47TlsSwitchZeroStale(t *testing.T) {
	a, _, _ := gui47Setup(t, "192.168.1.10:37199")
	a.profiles.AddrSuccess("601c9f08", "192.168.1.11:5555")
	a.teachOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		return "5554", nil
	}
	a.teachOps.tcpipFn = func(ctx context.Context, serial, port string) error { return nil }
	a.teachOps.shellFn = gui47Shell("0\n",
		"192.168.1.0/24 dev wlan0 proto kernel scope link src 192.168.1.100\n",
		"")
	a.maybeTeachTcpip(context.Background(), []adb.Device{gui47UsbDev()})
	e, ok := a.profiles.Entry("601c9f08")
	if !ok {
		t.Fatal("档案不存在")
	}
	if !gui47HasStale(e, "192.168.1.10:37199") {
		t.Fatalf("TLS 条目应打 stale: %+v", e.Addrs)
	}
	if gui47HasStale(e, "192.168.1.11:5555") {
		t.Fatalf("tcpip 条目不应被打 stale: %+v", e.Addrs)
	}
}

// TestGui47TlsSwitchOneOrErrorNoStale：开关=1 / 读取失败 → 不碰 TLS stale。
func TestGui47TlsSwitchOneOrErrorNoStale(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings string
		err      error
	}{
		{"switch=1", "1\n", nil},
		{"read-error", "", errors.New("adb shell failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _, _ := gui47Setup(t, "192.168.1.10:37199")
			a.teachOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
				return "5554", nil
			}
			a.teachOps.tcpipFn = func(ctx context.Context, serial, port string) error { return nil }
			a.teachOps.shellFn = func(ctx context.Context, serial string, args ...string) (string, error) {
				if len(args) > 0 && args[0] == "settings" {
					return tc.settings, tc.err
				}
				return "", nil
			}
			a.maybeTeachTcpip(context.Background(), []adb.Device{gui47UsbDev()})
			e, ok := a.profiles.Entry("601c9f08")
			if !ok {
				t.Fatal("档案不存在")
			}
			if gui47HasStale(e, "192.168.1.10:37199") {
				t.Fatalf("开关=1/读取失败不应打 stale: %+v", e.Addrs)
			}
		})
	}
}

// TestGui47TlsSwitchZeroNoTlsEntry：开关=0 但只有 tcpip 条目 → 不误伤。
func TestGui47TlsSwitchZeroNoTlsEntry(t *testing.T) {
	a, _, _ := gui47Setup(t, "192.168.1.11:5555")
	a.teachOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		return "5554", nil
	}
	a.teachOps.tcpipFn = func(ctx context.Context, serial, port string) error { return nil }
	a.teachOps.shellFn = gui47Shell("0\n",
		"192.168.1.0/24 dev wlan0 proto kernel scope link src 192.168.1.100\n",
		"")
	a.maybeTeachTcpip(context.Background(), []adb.Device{gui47UsbDev()})
	e, ok := a.profiles.Entry("601c9f08")
	if !ok {
		t.Fatal("档案不存在")
	}
	if gui47HasStale(e, "192.168.1.11:5555") {
		t.Fatalf("无 TLS 条目时 tcpip 不应被打 stale: %+v", e.Addrs)
	}
}

func gui47HasAddr(e DeviceEntry, addr, mode string) bool {
	for _, a := range e.Addrs {
		if a.Addr == addr && addrEntryClass(a) == mode {
			return true
		}
	}
	return false
}

func gui47HasStale(e DeviceEntry, addr string) bool {
	for _, a := range e.Addrs {
		if a.Addr == addr {
			return a.Stale
		}
	}
	return false
}
