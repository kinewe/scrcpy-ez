package app

import (
	"context"
	"testing"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

// App respects forced Added events, but still verifies the responding endpoint.

func TestGui48Mdns6fix3ForceAddedTurnsActive(t *testing.T) {
	a, _ := newTestApp()
	installMdnsVerificationFakes(t, a)
	gui31K80Profiles(a)
	a.applyTrackUpdate(nil)

	svc := mdnsTlsSvc("adb-TEST0001-KWqpio", "192.0.2.197", "45005")
	// 先把该 TLS 地址打 stale（模拟此前 Goodbye/无信号）。
	a.profiles.MarkAddrStale(fixtureArchiveKey(a.profiles, "REDMI K80"), svc.Addr)
	a.applyMdnsSnapshot([]discovery.MdnsService{svc}, true, nil, nil)

	// QU 应答强制 emit：Snapshot 不变，Added 仅含实际应答的服务。
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{
		Snapshot: []discovery.MdnsService{svc},
		Added:    []discovery.MdnsService{svc},
		Removed:  nil,
		First:    false,
	})
	waitMdnsVerified(t, a, fixtureArchiveKey(a.profiles, "REDMI K80"), svc.Addr)
	if mdnsTlsAddrStale(a, "REDMI K80", svc.Addr) {
		t.Fatal("forced Added should restore active after endpoint verification")
	}
}

func TestGui48Mdns6fix3FallbackDiffStillWorks(t *testing.T) {
	a, _ := newTestApp()
	gui31K80Profiles(a)

	svc := mdnsTlsSvc("adb-TEST0001-KWqpio", "192.0.2.197", "45005")
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
