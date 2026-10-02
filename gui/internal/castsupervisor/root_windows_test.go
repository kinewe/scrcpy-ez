//go:build windows

package castsupervisor

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/deviceevents"
	"scrcpy-ez/gui/internal/sessioncontrol"
)

func waitRootAuth(t *testing.T, f *fixture) {
	t.Helper()
	until := time.Now().Add(10 * time.Second)
	for time.Now().Before(until) {
		if strings.Contains(f.log("adb.log"), "ROOT_AUTH\n") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("root authorization did not start: " + f.log("adb.log"))
}

func rootEnv(t *testing.T, delay int) []string {
	return []string{"SCEZ_TEST_ROOT_FIXED=" + filepath.Join(t.TempDir(), "fixed"), fmt.Sprintf("SCEZ_TEST_ROOT_DELAY=%d", delay)}
}

func TestRootWorkerStopsDuringAuthorization(t *testing.T) {
	h := deviceevents.NewHub()
	publish(h, transport("USB_A", "usb", "device"))
	f := newFixture(t, h, rootEnv(t, 10000)...)
	waitRootAuth(t, f)
	if e := sessioncontrol.SignalStop(f.tag); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-f.done:
		f.done <- e
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Stop blocked on phone root dialog")
	}
	if f.starts() != 0 || strings.Contains(f.log("adb.log"), "ROOT_RESTORE") {
		t.Fatal("stopped authorization launched or repaired")
	}
}

func TestRootWorkerRejectsRemovedTransportResult(t *testing.T) {
	h := deviceevents.NewHub()
	publish(h, transport("USB_A", "usb", "device"))
	f := newFixture(t, h, rootEnv(t, 1200)...)
	waitRootAuth(t, f)
	publish(h, transport("192.0.2.1:5555", "wifi", "device"))
	f.waitStarts(1)
	log := f.log("cast.log")
	if strings.Contains(log, "START USB_A ") || !strings.Contains(log, "START 192.0.2.1:5555 ") {
		t.Fatal("stale root result authorized removed route: " + log)
	}
	if strings.Count(f.log("adb.log"), "ROOT_RESTORE\n") != 1 {
		t.Fatal("removed route mutated: " + f.log("adb.log"))
	}
}

func TestRootWorkerDeniedDoesNotRetryOtherCurrentRoute(t *testing.T) {
	h := deviceevents.NewHub()
	publish(h, transport("USB_A", "usb", "device"), transport("192.0.2.1:5555", "wifi", "device"))
	extra := append(rootEnv(t, 0), "SCEZ_TEST_ROOT_DENY=1")
	f := newFixture(t, h, extra...)
	waitRootAuth(t, f)
	f.assertStable(0, 1800*time.Millisecond)
	if strings.Count(f.log("adb.log"), "ROOT_AUTH\n") != 1 || strings.Contains(f.log("adb.log"), "ROOT_RESTORE") {
		t.Fatal("denied root retried/mutated: " + f.log("adb.log"))
	}
}
