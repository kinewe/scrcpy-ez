package app

import (
	"testing"
)

// --- gui41 Fix B 单记忆（history 退役）补测 ---
//
// B1：retireSameClassLocked 从“转 history”改为“直接删除”。
// B2：newestOfClassLocked 不再接受 history 兜底。
// B3：normalizeLocked 加载旧档案时清理存量 history + 同形态多 active 只留最新。

// TestGui41HistoryRetireDeletes：同形态旧 active 条目在成功写入新同形态地址后
// 被直接删除（不再转 history），档案保持每形态一条。
func TestGui41HistoryRetireDeletes(t *testing.T) {
	s := NewProfileStore("")
	gui15Seed(s, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Serials:    []string{"601c9f08"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.197:33895", State: AddrStateActive, LastOk: 100, Mode: ModeTls},
			{Addr: "192.168.31.197:40000", State: AddrStateActive, LastOk: 200, Mode: ModeTls},
			{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 50, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	})

	// 对已存在的新 TLS 地址成功复位：B1 应删除同形态旧 33895
	s.AddrSuccessWithMode("REDMI K80", "192.168.31.197:40000", ModeTls)

	e, ok := s.Entry("REDMI K80")
	if !ok {
		t.Fatal("档案应可解析")
	}
	if len(e.Addrs) != 2 {
		t.Fatalf("同形态应只保留一条（40000）+ tcpip 5555，got %+v", e.Addrs)
	}
	if gui24FindAddr(e, "192.168.31.197:33895") != nil {
		t.Fatalf("旧同形态条目应被删除（history 退役）: %+v", e.Addrs)
	}
	if gui24FindAddr(e, "192.168.31.197:40000") == nil {
		t.Fatalf("新同形态条目应保留: %+v", e.Addrs)
	}
	if gui24FindAddr(e, "192.168.31.197:5555") == nil {
		t.Fatalf("其他形态条目不受影响: %+v", e.Addrs)
	}
}

// TestGui41NormalizeClearsHistory：加载/规整旧档案后——history 全部删除；
// 同形态多条 active 只留 lastOk 最新一条。
func TestGui41NormalizeClearsHistory(t *testing.T) {
	s := NewProfileStore("")
	gui15Seed(s, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Serials:    []string{"601c9f08"},
		Addrs: []AddrEntry{
			// history 旧端口
			{Addr: "192.168.31.197:33895", State: AddrStateHistory, LastOk: 150, Mode: ModeTls},
			// 同形态两条 active：只应保留 lastOk 最新 45000
			{Addr: "192.168.31.197:40000", State: AddrStateActive, LastOk: 100, Mode: ModeTls},
			{Addr: "192.168.31.197:45000", State: AddrStateActive, LastOk: 200, Mode: ModeTls},
			{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 50, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	})

	s.normalizeLocked()

	e, _ := s.Entry("REDMI K80")
	if len(e.Addrs) != 2 {
		t.Fatalf("规整后应每条形态最多一条（TLS+5555），got %+v", e.Addrs)
	}
	if gui24FindAddr(e, "192.168.31.197:33895") != nil {
		t.Fatalf("history 条目应被清理: %+v", e.Addrs)
	}
	tls := gui24FindAddr(e, "192.168.31.197:45000")
	if tls == nil {
		t.Fatalf("应保留 lastOk 最新的 TLS 45000: %+v", e.Addrs)
	}
	if gui24FindAddr(e, "192.168.31.197:40000") != nil {
		t.Fatalf("同形态旧 active 40000 应被删除: %+v", e.Addrs)
	}
}

// TestGui41NewestOfClassNoHistoryFallback（gui52 语义更新）：旧 history 归一为
// stale=离线候选 → newestOfClassLocked 在无 active 时返回 stale 条目。
func TestGui41NewestOfClassNoHistoryFallback(t *testing.T) {
	e := DeviceEntry{Addrs: []AddrEntry{
		{Addr: "192.168.31.197:33895", State: AddrStateHistory, LastOk: 150, Mode: ModeTls},
	}}

	a, ok := newestOfClassLocked(&e, ModeTls)
	if !ok || a.Addr != "192.168.31.197:33895" || a.State != AddrStateStale {
		t.Fatalf("旧 history 应归一为 stale 离线候选: %+v (ok=%v)", e.Addrs, ok)
	}
}
