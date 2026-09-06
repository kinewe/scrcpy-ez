package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// gui52fix5：unauthorized transport 在显示层一律视同 offline ——
// 撤权后 adb server 残留的 unauthorized 条目不得显示为在线（绿点）。
func TestGui52Fix5UnauthorizedShownOffline(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "Xiaomi Pad 8 Pro", &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		Addrs:      []AddrEntry{{Addr: "192.168.31.162:5555", State: AddrStateStale, Mode: ModeTcpip}},
		Profiles:   DefaultProfile(),
	})
	devs := []adb.Device{
		{Serial: "192.168.31.162:5555", State: "unauthorized", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro"},
	}
	out := a.unifyProfileCards(devs)
	if len(out) == 0 {
		t.Fatalf("unify 不应丢弃条目（离线卡保留）: %v", out)
	}
	found := false
	for _, d := range out {
		if d.Serial == "192.168.31.162:5555" || d.Identity != "" {
			if d.State == "device" {
				t.Fatalf("unauthorized transport 不得显示为 device: %+v", d)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("未找到对应卡: %v", out)
	}
}

// gui52fix5：正常 device transport 不受影响。
func TestGui52Fix5DeviceUnaffected(t *testing.T) {
	a, _ := newWirelessApp()
	devs := []adb.Device{
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80"},
	}
	out := a.unifyProfileCards(devs)
	for _, d := range out {
		if d.Serial == "192.168.31.197:5555" && d.State != "device" {
			t.Fatalf("device transport 不被改动: %+v", d)
		}
	}
}
