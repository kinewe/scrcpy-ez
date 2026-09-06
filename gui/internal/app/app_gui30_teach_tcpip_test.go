package app

// gui30「插线即学习」测试：USB 设备上线自动 adb tcpip 5555（不必等投屏）。
// 覆盖：USB 在线 + 端口非 5555 → getprop/tcpip 各一次；端口 5555 幂等；
// 纯无线/离线不执行；每插线周期节流 + 拔插周期重置；adb tcpip 重启 adbd
// 后设备暂时消失 → 下一轮恢复不炸（自愈覆盖）；getprop 失败不重试轰炸。

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// gui30UsbDev 构造一台 USB 在线设备卡。
func gui30UsbDev() adb.Device {
	return adb.Device{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "REDMI K80"}
}

// TestGui30TeachTcpipLearnsOnUsbPlugged：USB 在线 + service.adb.tcp.port≠5555
// → GetProp 一次 + Tcpip 一次（参数正确：serial + 5555）。
func TestGui30TeachTcpipLearnsOnUsbPlugged(t *testing.T) {
	a, _ := newTestApp()
	var getprops, tcpips int
	var tcpipSerial, tcpipPort string
	a.teachOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		getprops++
		if serial != "601c9f08" || prop != "service.adb.tcp.port" {
			t.Errorf("getprop 参数错误: serial=%q prop=%q", serial, prop)
		}
		return "5554", nil
	}
	a.teachOps.tcpipFn = func(ctx context.Context, serial, port string) error {
		tcpips++
		tcpipSerial, tcpipPort = serial, port
		return nil
	}

	a.maybeTeachTcpip(context.Background(), []adb.Device{gui30UsbDev()})
	if getprops != 1 || tcpips != 1 {
		t.Fatalf("端口≠5555 应 getprop+tcpip 各一次: getprop=%d tcpip=%d", getprops, tcpips)
	}
	if tcpipSerial != "601c9f08" || tcpipPort != "5555" {
		t.Fatalf("tcpip 参数错误: %q %q", tcpipSerial, tcpipPort)
	}
}

// TestGui30TeachTcpipIdempotentWhen5555：端口已是 5555 → 只查一次不 tcpip；
// 且本周期内后续轮连 getprop 都跳过（0 开销幂等）。
func TestGui30TeachTcpipIdempotentWhen5555(t *testing.T) {
	a, _ := newTestApp()
	var getprops, tcpips int
	a.teachOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		getprops++
		return "5555\n", nil
	}
	a.teachOps.tcpipFn = func(ctx context.Context, serial, port string) error {
		tcpips++
		return nil
	}

	devs := []adb.Device{gui30UsbDev()}
	a.maybeTeachTcpip(context.Background(), devs)
	a.maybeTeachTcpip(context.Background(), devs) // 同周期第二轮：0 开销
	if getprops != 1 || tcpips != 0 {
		t.Fatalf("端口=5555 应只 getprop 一次且不 tcpip: getprop=%d tcpip=%d", getprops, tcpips)
	}
}

// TestGui30TeachTcpipSkipsWirelessAndOffline：纯无线设备 / 离线 / 未授权
// USB 设备都不执行（USB 插着=人在场的语义边界；纯无线状态不学习）。
func TestGui30TeachTcpipSkipsWirelessAndOffline(t *testing.T) {
	a, _ := newTestApp()
	var getprops, tcpips int
	a.teachOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		getprops++
		return "", nil
	}
	a.teachOps.tcpipFn = func(ctx context.Context, serial, port string) error {
		tcpips++
		return nil
	}

	devs := []adb.Device{
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi"},
		{Serial: "abc123", State: "offline", ConnType: "usb"},
		{Serial: "abc456", State: "unauthorized", ConnType: "usb"},
	}
	a.maybeTeachTcpip(context.Background(), devs)
	if getprops != 0 || tcpips != 0 {
		t.Fatalf("无线/离线/未授权设备不应学习: getprop=%d tcpip=%d", getprops, tcpips)
	}
}

// TestGui30TeachTcpipThrottlePerPlugCycle：每插线周期只学一次——同周期重复轮
// 不重复；拔线（设备消失）重置周期，再插线再次学习（K80 重启端口丢失 /
// 华为 FLA-TL10 端口随机丢失的再学路径）。
func TestGui30TeachTcpipThrottlePerPlugCycle(t *testing.T) {
	a, _ := newTestApp()
	var getprops, tcpips int
	a.teachOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		getprops++
		return "", nil // 空=端口丢失（K80 重启后 adbd 重置场景）
	}
	a.teachOps.tcpipFn = func(ctx context.Context, serial, port string) error {
		tcpips++
		return nil
	}

	devs := []adb.Device{gui30UsbDev()}
	a.maybeTeachTcpip(context.Background(), devs) // 插线轮 1：学习
	a.maybeTeachTcpip(context.Background(), devs) // 同周期轮 2：节流跳过
	if getprops != 1 || tcpips != 1 {
		t.Fatalf("同周期应只学一次: getprop=%d tcpip=%d", getprops, tcpips)
	}

	a.maybeTeachTcpip(context.Background(), nil) // 拔线：周期结束（清除记忆）
	if getprops != 1 || tcpips != 1 {
		t.Fatalf("拔线轮不应触发学习: getprop=%d tcpip=%d", getprops, tcpips)
	}

	a.maybeTeachTcpip(context.Background(), devs) // 再插线：新周期 → 重新学习
	if getprops != 2 || tcpips != 2 {
		t.Fatalf("再插线应重新学习一次: getprop=%d tcpip=%d", getprops, tcpips)
	}
}

// TestGui30TeachTcpipAdbdRestartRace：adb tcpip 后设备端 adbd 重启、设备从
// 列表暂时消失 1-2s → 下一轮轮询恢复；恢复后端口已是 5555 → 不再 tcpip
// （不重复轰炸、不炸），恢复周期内节流照常。
func TestGui30TeachTcpipAdbdRestartRace(t *testing.T) {
	a, _ := newTestApp()
	port := "" // tcpip 前：端口丢失
	var getprops, tcpips int
	a.teachOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		getprops++
		return port, nil
	}
	a.teachOps.tcpipFn = func(ctx context.Context, serial, port string) error {
		tcpips++
		return nil
	}

	devs := []adb.Device{gui30UsbDev()}
	a.maybeTeachTcpip(context.Background(), devs) // 插线：学习（tcpip 重启 adbd）
	if getprops != 1 || tcpips != 1 {
		t.Fatalf("插线轮应学习一次: getprop=%d tcpip=%d", getprops, tcpips)
	}
	port = "5555" // adbd 重启完成，端口生效

	a.maybeTeachTcpip(context.Background(), nil)  // adbd 重启：设备暂时消失
	a.maybeTeachTcpip(context.Background(), devs) // 下一轮：设备恢复 → 查端口=5555，不再 tcpip
	a.maybeTeachTcpip(context.Background(), devs) // 恢复周期内再轮：0 开销
	if getprops != 2 || tcpips != 1 {
		t.Fatalf("adbd 重启竞态恢复后不应重复 tcpip: getprop=%d tcpip=%d", getprops, tcpips)
	}
}

// TestGui30TeachTcpipGetpropErrorOncePerCycle：getprop 失败（未授权/瞬态）
// → 不 tcpip，且本周期不再重试（防 adbd 重启竞态下每 2s 重复轰炸）；
// 新插线周期重新尝试。
func TestGui30TeachTcpipGetpropErrorOncePerCycle(t *testing.T) {
	a, _ := newTestApp()
	var getprops, tcpips int
	a.teachOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		getprops++
		return "", context.DeadlineExceeded
	}
	a.teachOps.tcpipFn = func(ctx context.Context, serial, port string) error {
		tcpips++
		return nil
	}

	devs := []adb.Device{gui30UsbDev()}
	a.maybeTeachTcpip(context.Background(), devs)
	a.maybeTeachTcpip(context.Background(), devs) // 同周期：getprop 失败也不重复试
	if getprops != 1 || tcpips != 0 {
		t.Fatalf("getprop 失败应不 tcpip 且每周期只试一次: getprop=%d tcpip=%d", getprops, tcpips)
	}

	a.maybeTeachTcpip(context.Background(), nil)  // 拔线
	a.maybeTeachTcpip(context.Background(), devs) // 再插：新周期重试一次
	if getprops != 2 || tcpips != 0 {
		t.Fatalf("新周期应重试 getprop 一次: getprop=%d tcpip=%d", getprops, tcpips)
	}
}

// TestGui30TeachTcpipMultiDevice：多台 USB 设备同时插线 → 各自学习一次；
// 已学设备不受其它设备拔插影响。
func TestGui30TeachTcpipMultiDevice(t *testing.T) {
	a, _ := newTestApp()
	got := map[string]int{} // serial -> getprop 次数
	taught := map[string]int{}
	a.teachOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		got[serial]++
		return "", nil
	}
	a.teachOps.tcpipFn = func(ctx context.Context, serial, port string) error {
		taught[serial]++
		return nil
	}

	devs := []adb.Device{
		{Serial: "aaa", State: "device", ConnType: "usb"},
		{Serial: "bbb", State: "device", ConnType: "usb"},
	}
	a.maybeTeachTcpip(context.Background(), devs)
	if got["aaa"] != 1 || got["bbb"] != 1 || taught["aaa"] != 1 || taught["bbb"] != 1 {
		t.Fatalf("两台设备应各学一次: getprop=%v tcpip=%v", got, taught)
	}

	// bbb 拔线、aaa 仍插着 → 只有 bbb 周期重置，再插 bbb 重新学习，aaa 不再动
	a.maybeTeachTcpip(context.Background(), devs[:1])
	a.maybeTeachTcpip(context.Background(), devs)
	if got["aaa"] != 1 || got["bbb"] != 2 || taught["aaa"] != 1 || taught["bbb"] != 2 {
		t.Fatalf("拔插 bbb 应只影响 bbb: getprop=%v tcpip=%v", got, taught)
	}
}

// TestPollOnceGui30TeachTcpipFullChain（Linux-only，假 adb 脚本全链路）：
// pollOnce 对 USB 在线设备自动查端口 + tcpip 5555；同周期幂等；adb tcpip
// 重启 adbd 设备暂时消失 → 恢复轮不重复 tcpip；端口随机丢失后拔插 →
// 新周期重新学习。
func TestPollOnceGui30TeachTcpipFullChain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-only：依赖可执行的假 adb 脚本")
	}
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec.txt")
	portFile := filepath.Join(dir, "port.txt")
	fake := filepath.Join(dir, "adb")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"$G30_REC\"\n" +
		"if [ \"$1\" = \"devices\" ]; then printf 'List of devices attached\\n%s\\n' \"$G30_DEV\"; exit 0; fi\n" +
		"if [ \"$3\" = \"tcpip\" ]; then echo '5555' > \"$G30_PORT\"; echo \"restarting in TCP mode port: $4\"; exit 0; fi\n" +
		"if [ \"$3\" = \"shell\" ]; then\n" +
		"  case \"$4\" in\n" +
		"    getprop)\n" +
		"      case \"$5\" in\n" +
		"        ro.product.marketname) echo 'REDMI K80';;\n" +
		"        ro.product.manufacturer) echo 'Xiaomi';;\n" +
		"        ro.product.model) echo '24117RK2CC';;\n" +
		"        service.adb.tcp.port) cat \"$G30_PORT\" 2>/dev/null;;\n" +
		"      esac\n" +
		"      ;;\n" +
		"    dumpsys) echo 'level: 94';;\n" +
		"    wm) echo 'Physical size: 2560x1600';;\n" +
		"    settings) echo '120';;\n" +
		"  esac\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	os.Setenv("G30_REC", rec)
	os.Setenv("G30_PORT", portFile)
	os.Setenv("G30_DEV", "601c9f08\tdevice")
	defer os.Unsetenv("G30_DEV")
	defer os.Unsetenv("G30_PORT")
	defer os.Unsetenv("G30_REC")

	// K80 重启后端口丢失（非 5555）
	if err := os.WriteFile(portFile, []byte("5554\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := New(Config{AdbPath: fake, ConfigPath: "", ProfilesPath: filepath.Join(dir, "profiles.json"), Version: "test"})

	counts := func() (getprop, tcpip int) {
		b, err := os.ReadFile(rec)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(b), "-s 601c9f08 shell getprop service.adb.tcp.port"),
			strings.Count(string(b), "-s 601c9f08 tcpip 5555")
	}

	// 插线轮 1：自动学习
	a.pollOnce(context.Background())
	if g, tp := counts(); g != 1 || tp != 1 {
		t.Fatalf("插线轮应 getprop+tcpip 各一次: getprop=%d tcpip=%d", g, tp)
	}
	devs := a.Snapshot().Devices
	if len(devs) != 1 || devs[0].Serial != "601c9f08" || devs[0].State != "device" {
		t.Fatalf("插线轮设备卡错误: %+v", devs)
	}

	// 同周期轮 2：节流幂等（不重复 getprop/tcpip）
	a.pollOnce(context.Background())
	if g, tp := counts(); g != 1 || tp != 1 {
		t.Fatalf("同周期应不重复学习: getprop=%d tcpip=%d", g, tp)
	}

	// adb tcpip 重启 adbd：设备暂时消失（轮 3）→ 恢复（轮 4），端口已 5555
	// → 只查一次不再 tcpip；恢复周期内再轮（轮 5）0 开销
	os.Setenv("G30_DEV", "")
	a.pollOnce(context.Background()) // 设备暂时消失
	os.Setenv("G30_DEV", "601c9f08\tdevice")
	a.pollOnce(context.Background()) // 恢复
	if g, tp := counts(); g != 2 || tp != 1 {
		t.Fatalf("adbd 重启恢复后不应重复 tcpip: getprop=%d tcpip=%d", g, tp)
	}
	a.pollOnce(context.Background())
	if g, tp := counts(); g != 2 || tp != 1 {
		t.Fatalf("恢复周期内再轮应 0 开销: getprop=%d tcpip=%d", g, tp)
	}
	if devs = a.Snapshot().Devices; len(devs) != 1 || devs[0].State != "device" {
		t.Fatalf("恢复后设备卡错误: %+v", devs)
	}

	// 端口随机丢失（华为 FLA-TL10 场景）→ 拔线再插 = 新周期 → 重新学习
	if err := os.WriteFile(portFile, []byte("5554\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Setenv("G30_DEV", "")
	a.pollOnce(context.Background()) // 拔线
	os.Setenv("G30_DEV", "601c9f08\tdevice")
	a.pollOnce(context.Background()) // 再插线：端口又丢了 → 再学一次
	if g, tp := counts(); g != 3 || tp != 2 {
		t.Fatalf("再插线（端口丢失）应重新学习: getprop=%d tcpip=%d", g, tp)
	}
}
