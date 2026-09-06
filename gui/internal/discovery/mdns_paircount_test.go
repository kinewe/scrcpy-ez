package discovery

import "testing"

// 复现：adb 37+ 的 mdns services 输出实例名后带计数 "(n)"
func TestParseMdnsServicesReproCount(t *testing.T) {
	out := "List of discovered mdns services\r\n" +
		"adb-a743e1df-On9v2R (2)\t_adb-tls-connect._tcp\t192.168.31.183:43105\r\n" +
		"adb-a743e1df\t_adb._tcp\t192.168.31.183:5555\r\n"
	got := ParseMdnsServices(out)
	for _, s := range got {
		t.Logf("解析到: type=%q name=%q addr=%q mode=%v", s.Type, s.Name, s.Addr, s.Mode)
	}
	if len(got) != 2 {
		t.Fatalf("期望 2 条，实际 %d 条（TLS 行带 (2) 计数被丢弃？）", len(got))
	}
	found := false
	for _, s := range got {
		if s.Mode == MdnsModeTls && s.Addr == "192.168.31.183:43105" {
			found = true
		}
	}
	if !found {
		t.Fatal("TLS 43105 未解析（(2) 计数字段导致的 bug 实锤）")
	}
}
