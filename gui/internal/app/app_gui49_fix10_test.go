package app

import (
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui49-fix10：plugging 活跃期无条件盖遮罩卡（删除 realUsbDevice 豁免） ---

func TestGui49Fix10PluggingAlwaysShieldsRealUsbDevice(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix3SeedK80(a)
	a.plugStart("REDMI K80", time.Now(), "测试插线")

	devs := []adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb",
			Name: "REDMI K80", Identity: "REDMI K80", Battery: 90, Res: "2560x1440", FPS: 120},
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi",
			Name: "REDMI K80", Identity: "REDMI K80"},
	}
	out := a.shieldUsbLearning(devs)
	if len(out) != 1 {
		t.Fatalf("plugging 活跃期应只输出一张遮罩卡: %+v", out)
	}
	d := out[0]
	if d.Serial != "601c9f08" || d.ConnType != "usb" || d.State != "device" || !d.Connecting {
		t.Fatalf("plugging 活跃期必须是连接中遮罩卡: %+v", d)
	}
	if d.Battery != 0 || d.Res != "" || d.FPS != 0 {
		t.Fatalf("遮罩卡不得携带真卡规格/电量: %+v", d)
	}
	if d.Wireless != "192.168.31.197:5555" {
		t.Fatalf("在线无线地址应并入遮罩卡副行: %+v", d)
	}

	// 清遮罩后（稳定 device 2s 判定已确认）真卡才露出。
	a.plugClear("REDMI K80", "测试稳定结算")
	out = a.shieldUsbLearning(devs)
	if len(out) != 2 {
		t.Fatalf("清遮罩后应恢复原始卡列表: %+v", out)
	}
	foundReal := false
	for i := range out {
		if out[i].Serial == "601c9f08" && !out[i].Connecting {
			foundReal = true
		}
	}
	if !foundReal {
		t.Fatalf("清遮罩后真 USB 卡应露出: %+v", out)
	}
}
