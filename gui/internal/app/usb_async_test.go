package app

import (
	"scrcpy-ez/gui/internal/adb"
	"testing"
	"time"
)

// Legacy lifecycle assertions await the asynchronous learning result; dedicated
// parallel tests explicitly assert that topology commits do not wait for it.
func rc10TrackAndWait(t *testing.T, a *App, devs []adb.Device) {
	t.Helper()
	t.Cleanup(a.Close)
	a.applyTrackUpdate(devs)
	for _, d := range devs {
		if d.ConnType != "usb" || d.State != "device" {
			continue
		}
		id := a.plugIDForSerial(d.Serial)
		waitFor(t, 3*time.Second, func() bool {
			a.teachMu.Lock()
			defer a.teachMu.Unlock()
			return a.usbLearning[d.Serial] == nil || a.plugLearned[id]
		}, "USB learning must publish its result")
	}
}
