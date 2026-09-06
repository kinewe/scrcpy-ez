package adb

import (
	"bufio"
	"strings"
	"testing"
)

// --- track-devices 长度前缀块解析（纯函数） ---

// 实测 adb 37.0.0：块 = 4 位 hex 长度前缀 + 设备列表文本。
// 注意前缀按 LF 计数，线上是 CRLF（每行多 1 个 CR）。
func TestReadTrackBlockNormal(t *testing.T) {
	text := "601c9f08\tdevice\nH9RNW18604002288\tdevice\n192.168.31.162:5555\tdevice\n"
	// 前缀按 LF 计：上面文本去掉 CR 后长度 68？直接用格式化构造一致块。
	payload := strings.ReplaceAll(text, "\n", "\r\n")
	wire := "0043" + payload // 67 = LF 计长度（15+1+23+1+26+1=67）
	br := bufio.NewReader(strings.NewReader(wire))
	got, err := readTrackBlock(br)
	if err != nil {
		t.Fatal(err)
	}
	want := "601c9f08\tdevice\nH9RNW18604002288\tdevice\n192.168.31.162:5555\tdevice\n"
	if got != want {
		t.Fatalf("块解析错误:\n got %q\nwant %q", got, want)
	}
}

func TestReadTrackBlockEmpty(t *testing.T) {
	br := bufio.NewReader(strings.NewReader("0000"))
	got, err := readTrackBlock(br)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("空列表块应返回空串: %q", got)
	}
}

func TestReadTrackBlockMultiplePacketsInOneRead(t *testing.T) {
	// 粘包：两个块一次到达
	first := "aaa\tdevice\n"
	second := "bbb\toffline\n192.168.31.162:5555\tdevice\n"
	wire := "000b" + strings.ReplaceAll(first, "\n", "\r\n") +
		"0027" + strings.ReplaceAll(second, "\n", "\r\n")
	br := bufio.NewReader(strings.NewReader(wire))
	got1, err := readTrackBlock(br)
	if err != nil {
		t.Fatal(err)
	}
	if got1 != first {
		t.Fatalf("第一块错误: %q", got1)
	}
	got2, err := readTrackBlock(br)
	if err != nil {
		t.Fatal(err)
	}
	if got2 != second {
		t.Fatalf("第二块错误: %q", got2)
	}
}

func TestReadTrackBlockHalfPacket(t *testing.T) {
	// 半包：块跨多次 Read——用小 bufio reader 强制多次读
	payload := "aaa\tdevice\r\nbbb\tdevice\r\n"
	// LF 计长度：10+1 + 10+1 = 22 => 0x16
	wire := "0016" + payload
	br := bufio.NewReaderSize(strings.NewReader(wire), 4)
	got, err := readTrackBlock(br)
	if err != nil {
		t.Fatal(err)
	}
	if got != "aaa\tdevice\nbbb\tdevice\n" {
		t.Fatalf("半包解析错误: %q", got)
	}
}

func TestReadTrackBlockInvalidPrefix(t *testing.T) {
	br := bufio.NewReader(strings.NewReader("zzzz"))
	if _, err := readTrackBlock(br); err == nil {
		t.Fatal("非法长度前缀应报错")
	}
}

func TestParseTrackDevicesNormal(t *testing.T) {
	block := "601c9f08\tdevice\n192.168.31.162:5555\tdevice model:24117RK2CC\n"
	devs := ParseTrackDevices(block)
	if len(devs) != 2 {
		t.Fatalf("应解析 2 台设备: %+v", devs)
	}
	if devs[0].Serial != "601c9f08" || devs[0].ConnType != "usb" || devs[0].State != "device" {
		t.Fatalf("USB 条目错误: %+v", devs[0])
	}
	if devs[1].Serial != "192.168.31.162:5555" || devs[1].ConnType != "wifi" || devs[1].Model != "24117RK2CC" {
		t.Fatalf("无线条目错误: %+v", devs[1])
	}
}

func TestParseTrackDevicesEmpty(t *testing.T) {
	if devs := ParseTrackDevices(""); len(devs) != 0 {
		t.Fatalf("空块应无设备: %+v", devs)
	}
}

func TestParseTrackDevicesOffline(t *testing.T) {
	devs := ParseTrackDevices("601c9f08\toffline\n")
	if len(devs) != 1 || devs[0].State != "offline" || devs[0].ConnType != "usb" {
		t.Fatalf("offline 解析错误: %+v", devs)
	}
}

func TestParseTrackDevicesMergesTransportByModel(t *testing.T) {
	block := "192.168.31.162:5555\tdevice model:Xiaomi_Pad_8_Pro\na743e1df\tdevice model:Xiaomi_Pad_8_Pro\n"
	devs := ParseTrackDevices(block)
	if len(devs) != 1 {
		t.Fatalf("同 model 双 transport 应合并为一台设备: %+v", devs)
	}
	d := devs[0]
	if d.Serial != "a743e1df" || d.ConnType != "usb" || d.Wireless != "192.168.31.162:5555" {
		t.Fatalf("USB 优先合并错误: %+v", d)
	}
}

func TestParseTrackDevicesLargeList(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 25; i++ {
		sb.WriteString("serial")
		sb.WriteString(string(rune('A' + i)))
		sb.WriteString("\tdevice\n")
	}
	devs := ParseTrackDevices(sb.String())
	if len(devs) != 25 {
		t.Fatalf("大块应解析 25 台: %d", len(devs))
	}
}

func TestDiffDevices(t *testing.T) {
	prev := []Device{
		{Serial: "a", State: "device", ConnType: "usb"},
		{Serial: "b", State: "device", ConnType: "wifi"},
	}
	cur := []Device{
		{Serial: "b", State: "offline", ConnType: "wifi"},
		{Serial: "c", State: "device", ConnType: "usb"},
	}
	added, removed, changed := DiffDevices(prev, cur)
	if len(added) != 1 || added[0].Serial != "c" {
		t.Fatalf("added 错误: %+v", added)
	}
	if len(removed) != 1 || removed[0].Serial != "a" {
		t.Fatalf("removed 错误: %+v", removed)
	}
	if len(changed) != 1 || changed[0].Serial != "b" || changed[0].State != "offline" {
		t.Fatalf("changed 错误: %+v", changed)
	}
}
