//go:build windows

package castsupervisor

import (
	"os"
	"path/filepath"
	"scrcpy-ez/gui/internal/deviceevents"
	"scrcpy-ez/gui/internal/sessioncontrol"
	"strings"
	"testing"
	"time"
)

func repairEnv(t *testing.T, enabled bool) []string {
	e := []string{"SCEZ_TEST_UPLOAD_DENIED=1", "SCEZ_TEST_ROOT_FIXED=" + filepath.Join(t.TempDir(), "fixed")}
	if enabled {
		e = append(e, "SCEZ_TEST_ROOT_ENABLE=1")
	}
	return e
}
func waitAuth(t *testing.T, f *fixture) {
	t.Helper()
	until := time.Now().Add(8 * time.Second)
	for time.Now().Before(until) {
		if strings.Contains(f.log("adb.log"), "ROOT_AUTH\n") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("authorization missing: " + f.log("adb.log"))
}
func TestNormalWorkerDoesNotProbeEvenWhenRepairEnabled(t *testing.T) {
	h := deviceevents.NewHub()
	publish(h, transport("USB_A", "usb", "device"))
	f := newFixture(t, h, "SCEZ_TEST_ROOT_ENABLE=1")
	f.waitStarts(1)
	f.assertStable(1, 250*time.Millisecond)
	log := f.log("adb.log")
	for _, forbidden := range []string{"shell -T", ".scrcpy-ez-root-probe-", "ROOT_AUTH", "ROOT_RESTORE"} {
		if strings.Contains(log, forbidden) {
			t.Fatal("normal path did repair I/O: " + log)
		}
	}
	if _, e := os.Stat(filepath.Join(f.dir, "root-repair-logs")); !os.IsNotExist(e) {
		t.Fatal("normal path created root reports")
	}
}
func TestPermissionFailureRequiresConsentAndGeneralFailureDoesNotRepair(t *testing.T) {
	for _, permission := range []bool{true, false} {
		h := deviceevents.NewHub()
		publish(h, transport("USB_A", "usb", "device"))
		extra := []string{"SCEZ_TEST_CLIENT_RC=1", "SCEZ_TEST_ROOT_ENABLE=1"}
		if permission {
			extra = repairEnv(t, false)
		}
		f := newFixture(t, h, extra...)
		if permission {
			f.assertStable(0, 1800*time.Millisecond)
		} else {
			time.Sleep(1800 * time.Millisecond)
		}
		if strings.Contains(f.log("adb.log"), "shell -T") || strings.Contains(f.log("adb.log"), "ROOT_AUTH") {
			t.Fatal("unconsented or unrelated failure requested root")
		}
	}
}
func TestPermissionRepairThenRealRetry(t *testing.T) {
	h := deviceevents.NewHub()
	publish(h, transport("USB_A", "usb", "device"))
	f := newFixture(t, h, repairEnv(t, true)...)
	f.waitStarts(1)
	log := f.log("adb.log")
	if strings.Count(log, "SERVER_UPLOAD_DENIED\n") != 1 || strings.Count(log, "ROOT_AUTH\n") != 1 || strings.Count(log, "ROOT_RESTORE\n") != 1 {
		t.Fatal(log)
	}
}

func TestUnconsentedPermissionFailurePausesExistingAlternateRoute(t *testing.T) {
	h := deviceevents.NewHub()
	publish(h, transport("USB_A", "usb", "device"), transport("192.0.2.1:5555", "wifi", "device"))
	f := newFixture(t, h, repairEnv(t, false)...)
	f.assertStable(0, 1700*time.Millisecond)
	if strings.Count(f.log("adb.log"), "SERVER_UPLOAD_DENIED\n") != 1 {
		t.Fatal("failure switched to existing alternate route: " + f.log("adb.log"))
	}
	_ = sessioncontrol.SignalStop(f.tag)
	select {
	case e := <-f.done:
		f.done <- e
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("paused upload failure did not stop promptly")
	}
}
func TestStopCancelsPermissionRepair(t *testing.T) {
	h := deviceevents.NewHub()
	publish(h, transport("USB_A", "usb", "device"))
	f := newFixture(t, h, append(repairEnv(t, true), "SCEZ_TEST_ROOT_DELAY=10000")...)
	waitAuth(t, f)
	_ = sessioncontrol.SignalStop(f.tag)
	select {
	case e := <-f.done:
		f.done <- e
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop waited for root dialog")
	}
	if f.starts() != 0 || strings.Contains(f.log("adb.log"), "ROOT_RESTORE") {
		t.Fatal("canceled repair resumed cast")
	}
}
func TestDeniedRootDoesNotRetryOtherRoute(t *testing.T) {
	h := deviceevents.NewHub()
	publish(h, transport("USB_A", "usb", "device"), transport("192.0.2.1:5555", "wifi", "device"))
	f := newFixture(t, h, append(repairEnv(t, true), "SCEZ_TEST_ROOT_DENY=1")...)
	waitAuth(t, f)
	f.assertStable(0, 1700*time.Millisecond)
	if strings.Count(f.log("adb.log"), "ROOT_AUTH\n") != 1 {
		t.Fatal(f.log("adb.log"))
	}
}
func TestRemovedRouteCannotResumeAfterRepair(t *testing.T) {
	h := deviceevents.NewHub()
	publish(h, transport("USB_A", "usb", "device"))
	f := newFixture(t, h, append(repairEnv(t, true), "SCEZ_TEST_ROOT_DELAY=1200")...)
	waitAuth(t, f)
	publish(h, transport("192.0.2.1:5555", "wifi", "device"))
	f.assertStable(0, 1800*time.Millisecond)
	if strings.Contains(f.log("cast.log"), "START USB_A") || strings.Contains(f.log("adb.log"), "ROOT_RESTORE") {
		t.Fatal("stale repair proceeded")
	}
}

func TestFailedRealUploadAfterProbeDoesNotLoop(t *testing.T) {
	h := deviceevents.NewHub()
	publish(h, transport("USB_A", "usb", "device"))
	f := newFixture(t, h, append(repairEnv(t, true), "SCEZ_TEST_UPLOAD_ALWAYS_DENIED=1")...)
	waitAuth(t, f)
	f.assertStable(0, 1500*time.Millisecond)
	if strings.Count(f.log("adb.log"), "ROOT_AUTH\n") != 1 || strings.Count(f.log("adb.log"), "SERVER_UPLOAD_DENIED\n") != 2 {
		t.Fatal(f.log("adb.log"))
	}
}

func TestEstablishedCastCanRepairANewFailure(t *testing.T) {
	h := deviceevents.NewHub()
	publish(h, transport("USB_A", "usb", "device"))
	env := repairEnv(t, true)
	fixed := strings.TrimPrefix(env[1], "SCEZ_TEST_ROOT_FIXED=")
	f := newFixture(t, h, env...)
	f.waitStarts(1)
	time.Sleep(5200 * time.Millisecond)
	if e := os.Remove(fixed); e != nil {
		t.Fatal(e)
	}
	publish(h, transport("192.0.2.1:5555", "wifi", "device"))
	f.waitStarts(2)
	if strings.Count(f.log("adb.log"), "ROOT_AUTH\n") != 2 {
		t.Fatal(f.log("adb.log"))
	}
}
