package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

// --- gui27：档案地址记忆极简化（每设备只记「5555 一条 + TLS 一条」；抛弃 fail 拉黑） ---
//
// 主人设计拍板（2026-08-26 20:30）：能用的 IP 就那两个——5555 的一个（包括
// USB 学习的 5555）与无线调试的一个（若有）。记住这两个兜底就好，其他 IP
// 都是噪声。核心原则：
//   ① 单地址记忆：每设备只记「最近成功的 5555」+「最近成功的 TLS」各一条
//      （USB 插线稳的逻辑：记住上一次学习的 IP，仅此而已）；
//   ② 广播 = 真相：mDNS 是设备自报"我现在在这"——广播一到立即替换记忆，
//      旧的即刻弃用、不参与任何运算；
//   ③ 旧地址 = 纯噪声：历史条目不进任何判断/候选/失败累计；
//   ④ 失败不惩罚、只节流：没有"累计失败"概念——失败只记 lastFail 时间戳
//      （60s 内不重试防刷屏），超过即重试；永不拉黑、永不失忆。

// TestGui27CandidatesOnePerClass：多历史 TLS + 两 5555 → 候选恰为
// 「每类一条」（tls 层在前）；其余历史条目（大 fail）一概不参与。
// gui32 语义更新：每类 active 严格优先——tls 类有 active 42449（lastOk 稍旧）
// → 选 42449 而非 history 45005（lastOk 更新的死记忆）；tcpip 类无 active →
// 取 history 中 lastOk 最新 162:5555。
func TestGui27CandidatesOnePerClass(t *testing.T) {
	s := NewProfileStore("")
	gui15Seed(s, "Xiaomi Pad 8 Pro", &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.183:33895", State: AddrStateHistory, Fail: 12, LastOk: 1750000001, Mode: ModeTls},
			{Addr: "192.168.31.183:45005", State: AddrStateHistory, Fail: 8, LastOk: 1750000004, Mode: ModeTls},
			{Addr: "192.168.31.183:42449", State: AddrStateActive, Fail: 0, LastOk: 1750000003, Mode: ModeTls},
			{Addr: "192.168.31.183:5555", State: AddrStateHistory, Fail: 254, LastOk: 1750000002, Mode: ModeTcpip},
			{Addr: "192.168.31.162:5555", State: AddrStateHistory, Fail: 223, LastOk: 1750000006, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	})
	got := s.OrderedAddrs("a743e1df")
	// gui52 二态：active 严格优先（tls 42449）；旧 history 归一为 stale 后
	// 作为离线候选参与（同状态按档案顺序取第一条 → tcpip 183:5555 在前）。
	if len(got) != 2 || got[0].Addr != "192.168.31.183:42449" || got[0].State != AddrStateActive ||
		got[1].Addr != "192.168.31.183:5555" || got[1].State != AddrStateStale {
		t.Fatalf("二态候选应为 active TLS 42449 + stale tcpip 183:5555: %+v", got)
	}
	if got[0].Mode != ModeTls {
		t.Fatalf("层序应 tls 优先: %+v", got)
	}
	// gui52：档案存在 active 地址 = 在线证据 → OfflineCandidateAddrs 无候选
	list := s.OfflineCandidateAddrs(nil)["Xiaomi Pad 8 Pro"]
	if len(list) != 0 {
		t.Fatalf("active 地址=在线证据，不应有离线候选: %+v", list)
	}
}

// TestGui27BigFailStillParticipates：实机档案现场——162:5555 fail=223（lastFail
// 旧）仍参与（拉黑消失，失忆死循环解除）；183:5555 fail=254 是旧记忆、排在
// 162 之后（gui52 判据只认 state 与档案顺序，fail/lastOk 一律不参与）。
func TestGui27BigFailStillParticipates(t *testing.T) {
	s := NewProfileStore("")
	gui15Seed(s, "Xiaomi Pad 8 Pro", &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.162:5555", State: AddrStateActive, Fail: 223, LastOk: 1787744278, LastFail: time.Now().Add(-3600 * time.Second).Unix(), Mode: ModeTcpip},
			{Addr: "192.168.31.183:5555", State: AddrStateActive, Fail: 254, LastOk: 1787744200, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	})
	got := s.OrderedAddrs("a743e1df")
	if len(got) != 1 || got[0].Addr != "192.168.31.162:5555" {
		t.Fatalf("fail=223 但 lastFail 旧的 162 应参与（fail 不拉黑）: %+v", got)
	}
	if s.BestAddr("a743e1df") != "192.168.31.162:5555" {
		t.Fatalf("BestAddr 应恢复 162: %q", s.BestAddr("a743e1df"))
	}
}

// TestGui27ThrottleWindow：lastFail=now → 60s 节流跳过（无候选）；lastFail=61s
// 前 → 参与；60s 边界（恰好 60s）→ 参与（超时无条件恢复，永不拉黑）。
func TestGui27ThrottleWindow(t *testing.T) {
	seed := func(lastFail int64) *ProfileStore {
		s := NewProfileStore("")
		gui15Seed(s, "Xiaomi Pad 8 Pro", &DeviceEntry{
			Marketname: "Xiaomi Pad 8 Pro",
			Serials:    []string{"a743e1df"},
			Addrs: []AddrEntry{
				{Addr: "192.168.31.162:5555", State: AddrStateActive, Fail: 1, LastOk: 1787744278, LastFail: lastFail, Mode: ModeTcpip},
			},
			Profiles: DefaultProfile(),
		})
		return s
	}
	now := time.Now()
	cases := []struct {
		name     string
		lastFail int64
		want     bool // 是否参与
	}{
		{"刚失败节流跳过", now.Unix(), false},
		{"59s 前仍节流", now.Add(-59 * time.Second).Unix(), false},
		{"60s 边界恢复", now.Add(-60 * time.Second).Unix(), true},
		{"61s 前恢复", now.Add(-61 * time.Second).Unix(), true},
		{"未失败过", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := seed(c.lastFail).OrderedAddrs("a743e1df")
			if c.want && (len(got) != 1 || got[0].Addr != "192.168.31.162:5555") {
				t.Fatalf("应参与（永不拉黑）: %+v", got)
			}
			if !c.want && len(got) != 0 {
				t.Fatalf("节流期内应无候选: %+v", got)
			}
		})
	}
}

// TestGui27ThrottledNewestSkipsWholeClass：本类最新一条被节流 → 本层无候选，
// 不回退旧条目（旧地址=纯噪声）；tcpip 层照常。
func TestGui27ThrottledNewestSkipsWholeClass(t *testing.T) {
	s := NewProfileStore("")
	gui15Seed(s, "Xiaomi Pad 8 Pro", &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.183:45005", State: AddrStateHistory, Fail: 2, LastOk: 1750000009, LastFail: time.Now().Unix(), Mode: ModeTls},
			{Addr: "192.168.31.183:33895", State: AddrStateHistory, Fail: 0, LastOk: 1750000001, Mode: ModeTls},
			{Addr: "192.168.31.162:5555", State: AddrStateActive, Fail: 0, LastOk: 1750000005, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	})
	got := s.OrderedAddrs("a743e1df")
	if len(got) != 1 || got[0].Addr != "192.168.31.162:5555" {
		t.Fatalf("最新 tls 被节流 → tls 层无候选，不回退旧 33895: %+v", got)
	}
}

// TestGui27NewTlsPortReplacesOld：新 TLS 端口经 mDNS 广播同步入档 → 旧 TLS
// 条目转 history 让位、新条目 active、tlsGuid 更新；候选 = 新端口。
func TestGui27NewTlsPortReplacesOld(t *testing.T) {
	s := NewProfileStore("")
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro"},
	})
	// 旧 TLS 端口入档（上次无线调试的端口）
	s.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.183:42357", ModeTls)
	// 重开无线调试 → 新端口 37201 广播（同 serial 新 guid 名）
	matched := s.MatchMdnsModes([]MdnsMatch{
		{Name: "adb-a743e1df-On9v2R", Addr: "192.168.31.183:37201", Mode: discovery.MdnsModeTls},
	})
	if len(matched) != 1 || matched[0].Addr != "192.168.31.183:37201" {
		t.Fatalf("新 TLS 端口应为候选: %+v", matched)
	}
	e, _ := s.Entry("Xiaomi Pad 8 Pro")
	if gui24FindAddr(e, "192.168.31.183:42357") != nil {
		t.Fatalf("旧 TLS 端口应按单记忆删除（不再转 history）: %+v", e.Addrs)
	}
	fresh := gui24FindAddr(e, "192.168.31.183:37201")
	if fresh == nil || fresh.State != AddrStateActive || fresh.Mode != ModeTls {
		t.Fatalf("新 TLS 端口应 active: %+v", e.Addrs)
	}
	if e.TlsGuid != "adb-a743e1df-On9v2R" {
		t.Fatalf("tlsGuid 应更新为新端口服务实例名: %q", e.TlsGuid)
	}
	// 候选：TLS 类只取新端口（旧端口出局）
	got := s.OrderedAddrs("a743e1df")
	if len(got) != 1 || got[0].Addr != "192.168.31.183:37201" {
		t.Fatalf("候选应为新 TLS 端口: %+v", got)
	}
}

// TestGui27New5555ReplacesOld：tcpip 规则——跨 IP 新 5555 成功 → 旧 5555 转
// history 让位；同一 IP 再成功 → 只更新时间戳（状态保持 active）。
func TestGui27New5555ReplacesOld(t *testing.T) {
	s := NewProfileStore("")
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro"},
	})
	s.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.183:5555", ModeTcpip)
	e, _ := s.Entry("Xiaomi Pad 8 Pro")
	if a := gui24FindAddr(e, "192.168.31.183:5555"); a == nil || a.State != AddrStateActive {
		t.Fatalf("首个 5555 应 active: %+v", e.Addrs)
	}

	// 跨 IP：162 成功（换路由器场景）
	s.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.162:5555", ModeTcpip)
	e, _ = s.Entry("Xiaomi Pad 8 Pro")
	if gui24FindAddr(e, "192.168.31.183:5555") != nil {
		t.Fatalf("跨 IP 替换：旧 5555 应按单记忆删除: %+v", e.Addrs)
	}
	if a := gui24FindAddr(e, "192.168.31.162:5555"); a == nil || a.State != AddrStateActive {
		t.Fatalf("新 5555 应 active: %+v", e.Addrs)
	}
	if got := s.BestAddr("a743e1df"); got != "192.168.31.162:5555" {
		t.Fatalf("BestAddr 应为新 5555: %q", got)
	}

	// 同一 IP 再成功：更新时间戳、状态保持（不来回翻转）
	s.AddrSuccess("Xiaomi Pad 8 Pro", "192.168.31.162:5555")
	e, _ = s.Entry("Xiaomi Pad 8 Pro")
	a162 := gui24FindAddr(e, "192.168.31.162:5555")
	if a162 == nil || a162.State != AddrStateActive || a162.Fail != 0 || a162.LastFail != 0 {
		t.Fatalf("同一 IP 再成功应保持 active 且清 fail/lastFail: %+v", a162)
	}
}

// TestGui27SuccessClearsThrottle：AddrSuccessWithMode 清 fail、清 lastFail、
// 更新 lastOk——节流中的地址成功后立即恢复参与。
func TestGui27SuccessClearsThrottle(t *testing.T) {
	s := NewProfileStore("")
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro"},
	})
	s.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.162:5555", ModeTcpip)
	s.AddrFail("Xiaomi Pad 8 Pro", "192.168.31.162:5555")
	e, _ := s.Entry("Xiaomi Pad 8 Pro")
	if a := gui24FindAddr(e, "192.168.31.162:5555"); a == nil || a.Fail != 1 || a.LastFail == 0 {
		t.Fatalf("失败后应 fail=1 + lastFail=now: %+v", e.Addrs)
	}
	if got := s.OrderedAddrs("a743e1df"); len(got) != 0 {
		t.Fatalf("刚失败应被 60s 节流: %+v", got)
	}
	time.Sleep(1100 * time.Millisecond) // lastOk 秒级区分
	s.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.162:5555", ModeTcpip)
	e, _ = s.Entry("Xiaomi Pad 8 Pro")
	if a := gui24FindAddr(e, "192.168.31.162:5555"); a == nil || a.Fail != 0 || a.LastFail != 0 || a.State != AddrStateActive {
		t.Fatalf("成功后应清 fail/lastFail 并回 active: %+v", e.Addrs)
	}
	if got := s.OrderedAddrs("a743e1df"); len(got) != 1 || got[0].Addr != "192.168.31.162:5555" {
		t.Fatalf("成功后应恢复参与: %+v", got)
	}
}

// TestGui27BroadcastClearsThrottle：广播 = 真相——节流中的地址一旦在 mDNS 广播
// 出现（设备自报"我现在在这"）→ lastFail 归零，立即恢复参与。
func TestGui27BroadcastClearsThrottle(t *testing.T) {
	s := NewProfileStore("")
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro"},
	})
	s.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.162:5555", ModeTcpip)
	s.AddrFail("Xiaomi Pad 8 Pro", "192.168.31.162:5555")
	if got := s.OrderedAddrs("a743e1df"); len(got) != 0 {
		t.Fatalf("失败后应先被节流: %+v", got)
	}
	s.MatchMdnsModes([]MdnsMatch{
		{Name: "adb-a743e1df", Addr: "192.168.31.162:5555", Mode: discovery.MdnsModeTcpip},
	})
	e, _ := s.Entry("Xiaomi Pad 8 Pro")
	if a := gui24FindAddr(e, "192.168.31.162:5555"); a == nil || a.LastFail != 0 {
		t.Fatalf("广播在场应解除节流（lastFail=0）: %+v", e.Addrs)
	}
	if got := s.OrderedAddrs("a743e1df"); len(got) != 1 || got[0].Addr != "192.168.31.162:5555" {
		t.Fatalf("广播解除节流后应恢复参与: %+v", got)
	}
}

// TestGui27LegacyJsonMigration：旧 JSON（无 lastFail 字段 / 带大 fail / 多 TLS
// 端口）载入后恢复参与——实机"失忆死循环"立即解除，无需主动清理档案。
func TestGui27LegacyJsonMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	data := `{
  "devices": {
    "Xiaomi Pad 8 Pro": {
      "marketname": "Xiaomi Pad 8 Pro",
      "serials": ["a743e1df"],
      "wireless": "tls",
      "tlsGuid": "adb-a743e1df-KWqpio",
      "addrs": [
        {"addr": "192.168.31.183:42357", "state": "active", "fail": 2, "lastOk": 1787744000, "mode": "tls"},
        {"addr": "192.168.31.183:5555", "state": "history", "fail": 254, "lastOk": 1787744100, "mode": "tcpip"},
        {"addr": "192.168.31.162:5555", "state": "history", "fail": 223, "lastOk": 1787744278, "mode": "tcpip"},
        {"addr": "192.168.31.183:45005", "state": "history", "fail": 5, "lastOk": 0, "mode": "tls"}
      ],
      "profiles": {"usb": {}, "wifi": {}}
    }
  }
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewProfileStore(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	// gui52 迁移：tls 类 active 42357 保留（fail=2 不入判据）；tcpip 类旧
	// history 按 lastOk 最新者 162:5555 迁移为 stale（183 丢弃）；45005
	// 与 42357 同形态且更旧 → 丢弃。
	got := s.OrderedAddrs("a743e1df")
	if len(got) != 2 || got[0].Addr != "192.168.31.183:42357" || got[0].State != AddrStateActive ||
		got[1].Addr != "192.168.31.162:5555" || got[1].State != AddrStateStale {
		t.Fatalf("旧档案迁移应为 active TLS 42357 + stale tcpip 162:5555: %+v", got)
	}
	if s.BestAddr("a743e1df") != "192.168.31.183:42357" {
		t.Fatalf("BestAddr 应为 active 42357: %q", s.BestAddr("a743e1df"))
	}
	e, _ := s.Entry("a743e1df")
	for _, legacy := range []string{"192.168.31.183:5555", "192.168.31.183:45005"} {
		if gui24FindAddr(e, legacy) != nil {
			t.Fatalf("旧字段折叠后不应残留 %s: %+v", legacy, e.Addrs)
		}
	}

	// lastOk=0 且 lastFail=0 的旧档案条目（如仅剩 45005）→ 视为可用（参与）
	s2 := NewProfileStore("")
	gui15Seed(s2, "Xiaomi Pad 8 Pro", &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.183:45005", State: AddrStateActive, Fail: 0, LastOk: 0, Mode: ModeTls},
		},
		Profiles: DefaultProfile(),
	})
	if got := s2.OrderedAddrs("a743e1df"); len(got) != 1 || got[0].Addr != "192.168.31.183:45005" {
		t.Fatalf("lastOk=0 且 lastFail=0 的旧档案条目应视为可用: %+v", got)
	}
}

// TestGui27ResetFailThrottle：手动刷新只清 LastFail（60s 节流），不清 gui41c
// 失败打标 Stale——失败后 Stale=true，需成功/mDNS 才复活。
func TestGui27ResetFailThrottle(t *testing.T) {
	s := NewProfileStore("")
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro"},
	})
	s.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.162:5555", ModeTcpip)
	s.AddrFail("Xiaomi Pad 8 Pro", "192.168.31.162:5555")
	if got := s.OrderedAddrs("a743e1df"); len(got) != 0 {
		t.Fatalf("失败后应先被节流: %+v", got)
	}
	s.ResetFailThrottle()
	// gui52：ResetFailThrottle 只清内存态节流；state 仍是 stale——
	// stale=离线候选，节流解除后立即恢复参与（不靠成功/广播才能翻回）。
	if got := s.OrderedAddrs("a743e1df"); len(got) != 1 || got[0].Addr != "192.168.31.162:5555" || got[0].State != AddrStateStale {
		t.Fatalf("节流解除后 stale 应恢复为离线候选: %+v", got)
	}
	e, _ := s.Entry("Xiaomi Pad 8 Pro")
	a := gui24FindAddr(e, "192.168.31.162:5555")
	if a == nil || a.LastFail != 0 || a.State != AddrStateStale {
		t.Fatalf("ResetFailThrottle 后 lastFail 应归零但 state 保持 stale: %+v", e.Addrs)
	}
	// 成功路径 → state=active
	s.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.162:5555", ModeTcpip)
	if got := s.OrderedAddrs("a743e1df"); len(got) != 1 || got[0].Addr != "192.168.31.162:5555" || got[0].State != AddrStateActive {
		t.Fatalf("成功后应翻回 active: %+v", got)
	}
}
