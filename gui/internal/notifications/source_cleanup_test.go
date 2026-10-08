package notifications

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPushFailureIsRecoverableButMissingServerIsNot(t *testing.T) {
	dir := t.TempDir()
	source := &ADBSource{ADB: filepath.Join(dir, "missing-adb.exe"), Server: filepath.Join(dir, "server")}
	target := Target{Serial: "synthetic", DeviceSerial: "SYNTHETIC_SERIAL"}
	emit := func(Frame) error { t.Fatal("failed setup delivered a notification"); return nil }
	if err := source.Run(context.Background(), target, emit); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing server must not create a transport retry loop")
	}
	if err := os.WriteFile(source.Server, []byte("synthetic server"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := source.Run(context.Background(), target, emit); err != ErrTransport {
		t.Fatal("temporary ADB push failure permanently disabled notification sync")
	}
}

func TestDeferredCleanupIsBoundedAndWaitsForMatchingDevice(t *testing.T) {
	s := &ADBSource{}
	for i := 0; i < 256; i++ {
		s.rememberCleanup(ownedCleanup{device: "phone-a", session: fmt.Sprint(i)})
	}
	if len(s.pending) != 128 || s.pending[0].session != "128" || s.pending[127].session != "255" {
		t.Fatal("deferred cleanup bound/order differs")
	}
	s.rememberCleanup(s.pending[127])
	if len(s.pending) != 128 {
		t.Fatal("duplicate cleanup consumed capacity")
	}
	// No executable is configured: selecting an unrelated device must not even
	// attempt to run a cleanup command, or consume another phone's ownership.
	s.flushCleanup("unrelated-transport", "phone-b")
	if len(s.pending) != 128 {
		t.Fatal("unrelated device consumed cleanup ownership")
	}
}
