package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui51：批量管理三件套（删除/改名/名称链）后端测试 ---

func TestGui51ProfileCardNameCustomPriority(t *testing.T) {
	e := DeviceEntry{
		Marketname:     "REDMI K80",
		DisplayName:    "客厅 K80",
		DisplayNameSet: true,
	}
	if got := profileCardName(e, "601c9f08"); got != "客厅 K80" {
		t.Fatalf("自定义名称应优先: %q", got)
	}
	e.DisplayNameSet = false
	if got := profileCardName(e, "601c9f08"); got != "REDMI K80" {
		t.Fatalf("标=0 应回原算法链: %q", got)
	}
}

func TestGui51RenameNonEmptyAndClear(t *testing.T) {
	a, _ := newWirelessApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:5555"}),
	})
	if err := a.RenameDevicesJSON(`{"REDMI K80":"客厅 K80"}`); err != nil {
		t.Fatal(err)
	}
	e, _ := a.profiles.Entry("REDMI K80")
	if !e.DisplayNameSet || e.DisplayName != "客厅 K80" {
		t.Fatalf("非空改名应标=1: %+v", e)
	}
	if err := a.RenameDevicesJSON(`{"REDMI K80":"   "}`); err != nil {
		t.Fatal(err)
	}
	e, _ = a.profiles.Entry("REDMI K80")
	if e.DisplayNameSet || e.DisplayName != "" {
		t.Fatalf("空改名应清标=0: %+v", e)
	}
}

func TestGui51DeleteWirelessDisconnectAndArchiveRemove(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-only：依赖假 adb 脚本记录 disconnect")
	}
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec.txt")
	fake := filepath.Join(dir, "adb")
	script := "#!/bin/sh\nif [ \"$1\" = \"disconnect\" ]; then echo \"$@\" >> \"$G51_REC\"; exit 0; fi\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("G51_REC", rec)

	a := New(Config{AdbPath: fake, ConfigPath: "", ProfilesPath: filepath.Join(dir, "profiles.json"), Version: "test"})
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:5555", "192.168.31.197:45005"}),
	})
	a.profiles.SetDeviceOrder([]string{"REDMI K80"})

	if err := a.DeleteDevices([]string{"REDMI K80"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.profiles.Entry("REDMI K80"); ok {
		t.Fatal("档案设备应被删除")
	}
	if len(a.profiles.DeviceOrder()) != 0 {
		t.Fatalf("deviceOrder 应同步移除: %v", a.profiles.DeviceOrder())
	}
	b, _ := os.ReadFile(rec)
	logs := string(b)
	if !strings.Contains(logs, "disconnect 192.168.31.197:5555") ||
		!strings.Contains(logs, "disconnect 192.168.31.197:45005") {
		t.Fatalf("无线地址应执行 adb disconnect: %s", logs)
	}
}

func TestGui51LegacyProfilesLoadWithoutNewFields(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "profiles.json")
	legacy := `{"devices":{"REDMI K80":{"marketname":"REDMI K80","model":"24117RK2CC","serials":["601c9f08"],"addrs":[],"profiles":{"usb":{"res":2560,"fps":120,"bitrate":60},"wifi":{"res":1920,"fps":60,"bitrate":15}}}}}`
	if err := os.WriteFile(p, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewProfileStore(p)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	e, ok := s.Entry("REDMI K80")
	if !ok || e.DisplayNameSet || e.DisplayName != "" {
		t.Fatalf("旧档案加载应无自定义名称字段: %+v", e)
	}
}

func TestGui51DeleteUsbMarkPreventsRebuildUntilReplug(t *testing.T) {
	a, _ := newWirelessApp()
	ops := &teachfix3Ops{port: "5555"}
	ops.install(a)

	dev := teachfix3DeviceUSB()
	a.applyTrackUpdate([]adb.Device{dev}) // 首次插线：建档
	if _, ok := a.profiles.Entry("REDMI K80"); !ok {
		t.Fatal("首次插线应建档")
	}
	if err := a.DeleteDevices([]string{"REDMI K80"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.profiles.Entry("REDMI K80"); ok {
		t.Fatal("删除后档案应消失")
	}

	// 同一插线周期再次同步（非 added）：删除标记生效，不得重建档。
	dev.Battery = 90
	a.applyTrackUpdate([]adb.Device{dev})
	if _, ok := a.profiles.Entry("REDMI K80"); ok {
		t.Fatal("删除标记期内不得重建档")
	}

	// 新的插线事件（removed→added）：清标记，重新学习入档。
	a.applyTrackUpdate(nil)
	a.applyTrackUpdate([]adb.Device{dev})
	if _, ok := a.profiles.Entry("REDMI K80"); !ok {
		t.Fatal("新插线事件后应重新入档")
	}
}
