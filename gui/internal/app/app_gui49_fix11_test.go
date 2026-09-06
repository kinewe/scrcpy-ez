package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui49-fix11：提交摘要诊断 + 名称回退链补 manufacturer ---

func TestGui49Fix11CommitDisplaySummaryLog(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix3SeedK80(a)
	logPath := mdns8StartLogCapture(t)

	a.commitDisplay([]adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "REDMI K80", Identity: "REDMI K80"},
	}, "单元测试提交")
	mdns8LogContains(t, logPath, "[app] 显示提交：单元测试提交")
	mdns8LogContains(t, logPath, "[app] 显示提交摘要：n=1")
	mdns8LogContains(t, logPath, "601c9f08|device|usb|connecting=false|name=REDMI K80")
}

func TestGui49Fix11ProfileCardNameManufacturerModel(t *testing.T) {
	e := DeviceEntry{
		Manufacturer: "HUAWEI",
		Model:        "FLA-TL10",
		Serials:      []string{"FLA123"},
		Profiles:     DefaultProfile(),
	}
	if got := profileCardName(e, "FLA123"); got != "HUAWEI FLA-TL10" {
		t.Fatalf("manufacturer+model 应拼接名称: %q", got)
	}
	e.Marketname = "HUAWEI FLA-TL10"
	if got := profileCardName(e, "FLA123"); got != "HUAWEI FLA-TL10" {
		t.Fatalf("marketname 应优先: %q", got)
	}
}

func TestGui49Fix11SyncDevicesPersistsManufacturer(t *testing.T) {
	a, _ := newWirelessApp()
	a.profiles.SyncDevices([]adb.Device{{
		Serial: "FLA123", State: "device", ConnType: "usb",
		Manufacturer: "HUAWEI", Model: "FLA-TL10",
		Identity: adb.IdentityKey("", "HUAWEI", "FLA-TL10", "FLA123"),
	}})
	e, ok := a.profiles.Entry("HUAWEI FLA-TL10")
	if !ok {
		t.Fatal("档案应按 manufacturer+model identity 建档")
	}
	if e.Manufacturer != "HUAWEI" || e.Model != "FLA-TL10" {
		t.Fatalf("档案应持久化 manufacturer/model: %+v", e)
	}
	if got := profileCardName(e, "FLA123"); got != "HUAWEI FLA-TL10" {
		t.Fatalf("档案来源卡名称应完整: %q", got)
	}
}

func TestGui49Fix11FallbackCardNameUsesManufacturer(t *testing.T) {
	a, _ := newWirelessApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"HUAWEI FLA-TL10": {
			Manufacturer: "HUAWEI",
			Model:        "FLA-TL10",
			Serials:      []string{"FLA123"},
			Addrs:        []AddrEntry{},
			Profiles:     DefaultProfile(),
		},
	})
	d := a.mdns9ProfileFallbackCard("HUAWEI FLA-TL10", DeviceEntry{
		Manufacturer: "HUAWEI",
		Model:        "FLA-TL10",
		Serials:      []string{"FLA123"},
		Profiles:     DefaultProfile(),
	})
	if d.Name != "HUAWEI FLA-TL10" {
		t.Fatalf("离线/补卡名称应含 manufacturer: %+v", d)
	}
}
