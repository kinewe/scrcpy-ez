package adb

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// Explicit opt-in: listen/query mDNS only. No ADB commands, authentication,
// pairing, tcpip changes, or writes to the running application's profiles.
func TestMdnsRecoveryPhysicalBrowse(t *testing.T) {
	if os.Getenv("SCEZ_TEST_MDNS_RECOVERY_SMOKE") != "1" {
		t.Skip("physical LAN opt-in only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	started := time.Now()
	seen := map[string]int{}
	first := false
	err := browseMdns(ctx, func(ev MdnsTrackEvents) {
		first = first || ev.First
		for _, svc := range ev.Added {
			seen[mdnsRecordKey(svc)]++
			t.Logf("+%s candidate %s %s %s observations=%d", time.Since(started).Round(time.Millisecond), svc.Type, svc.Name, svc.Addr, seen[mdnsRecordKey(svc)])
		}
	}, nil, defaultMdnsBrowseOptions())
	if !first || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("browse lifecycle failed: first=%v err=%v", first, err)
	}
	t.Logf("observed service instances=%d (zero means no responding ADB services in this capture)", len(seen))
}
