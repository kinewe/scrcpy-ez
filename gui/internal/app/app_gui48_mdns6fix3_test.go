package app

import (
	"context"
	"testing"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

// gui48-mdns6fix3：App 尊重 A 层事件 diff（force emit 落地翻 active）。

func TestGui48Mdns6fix3ForceAddedTurnsActive(t *testing.T) {
	a, _ := newTestApp()
	gui31K80Profiles(a)
	a.applyTrackUpdate(nil)

	svc := mdnsTlsSvc("adb-601c9f08-KWqpio", "192.168.31.197", "45005")
	// 先把该 TLS 地址打 stale（模拟此前 Goodbye/无信号）。
	a.profiles.MarkAddrStale("REDMI K80", svc.Addr)
	a.applyMdnsSnapshot([]discovery.MdnsService{svc}, true, nil, nil)

	// A 层 QU 应答强制 emit：Snapshot 与上一份相同，但 Added=全量。
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{
		Snapshot: []discovery.MdnsService{svc},
		Added:    []discovery.MdnsService{svc},
		Removed:  nil,
		First:    false,
	})
	if mdnsTlsAddrStale(a, "REDMI K80", svc.Addr) {
		t.Fatal("A 层 Added=全量（force emit）应驱动 applyMdnsServiceAdded 翻回 active")
	}
}

func TestGui48Mdns6fix3FallbackDiffStillWorks(t *testing.T) {
	a, _ := newTestApp()
	gui31K80Profiles(a)

	svc := mdnsTlsSvc("adb-601c9f08-KWqpio", "192.168.31.197", "45005")
	added, removed := a.applyMdnsSnapshot([]discovery.MdnsService{svc}, false, nil, nil)
	if len(added) != 1 || len(removed) != 0 {
		t.Fatalf("evAdded/evRemoved 为空时应回退本地 diff: added=%+v removed=%+v", added, removed)
	}

	removed2 := []discovery.MdnsService{svc}
	_, removed = a.applyMdnsSnapshot(nil, false, nil, removed2)
	if len(removed) != 1 {
		t.Fatalf("非首块应直接采用 A 层 removed: %+v", removed)
	}
}
