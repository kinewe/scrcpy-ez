package rootrepair

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRootAuthorizationCancellationReport(t *testing.T) {
	f := fakeDevice{t: t, cancel: true}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	r, e := Prepare(ctx, Options{Serial: "192.0.2.1:40075", Identity: "PHONE_A", LogDir: t.TempDir(), Execute: f.exec})
	if !errors.Is(e, context.DeadlineExceeded) || r.Status != "canceled" || len(f.mutations) != 0 {
		t.Fatalf("%+v %v", r, e)
	}
}

// Parse the exact transmitted shell scripts; this does not execute any script.
// Android mksh still requires device validation, but POSIX syntax regressions are
// caught independently of the fake ADB state machine.
func TestRootTransmittedScriptsParse(t *testing.T) {
	shell := os.Getenv("YINMO_TEST_SHELL")
	if shell == "" {
		t.Skip("set YINMO_TEST_SHELL to a POSIX shell for script parsing")
	}
	f := fakeDevice{t: t, healthy: true, pushFails: true}
	r, _ := Prepare(context.Background(), Options{Serial: "192.0.2.1:40075", Identity: "PHONE_A", LogDir: t.TempDir(), Execute: f.exec})
	for _, step := range r.Steps {
		if step.Script == "" {
			continue
		}
		c := exec.Command(shell, "-n")
		c.Stdin = strings.NewReader(step.Script)
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("%s: %v %s", step.Name, e, b)
		}
	}
}
