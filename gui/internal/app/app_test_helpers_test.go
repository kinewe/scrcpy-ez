package app

import (
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// gui34Find 按 Serial 找卡（无则 nil）。历史命名保留（原 gui34 测试文件提供，
// 多个测试文件共用）。
func gui34Find(devs []adb.Device, serial string) *adb.Device {
	for i := range devs {
		if devs[i].Serial == serial {
			return &devs[i]
		}
	}
	return nil
}

// waitForMdns 等待条件满足（最长 3s），超时判失败。原 gui16 测试文件提供，
// 多个测试文件共用。
func waitForMdns(t *testing.T, name string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s：等待条件超时", name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
