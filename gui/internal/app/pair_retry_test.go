package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

func TestPairMissingRuntimeCanRetryIdenticalFailure(t *testing.T) {
	a, _ := newWirelessApp()
	p := seedPending(t, a, pairConnectSvcs())
	a.pairOps.pairFn = adb.New(filepath.Join(t.TempDir(), "missing-adb.exe"), "").Pair
	var previous string
	for attempt := 0; attempt < 3; attempt++ {
		if err := a.PairConnect(p.Key, "", "", "", "123456"); err != nil {
			t.Fatalf("retry %d rejected after terminal failure: %v", attempt, err)
		}
		status := waitPairPhase(t, a, PairPhaseFailed)
		if status.ErrCode != PairErrRuntime || !strings.Contains(status.ErrText, "adb.exe") {
			t.Fatalf("missing component was reported as a generic failure: %+v", status)
		}
		data, err := json.Marshal(status)
		if err != nil {
			t.Fatal(err)
		}
		if attempt > 0 && string(data) != previous {
			t.Fatal("fixture must reproduce identical failed snapshots across attempts")
		}
		previous = string(data)
	}
}

func TestPairNonzeroExitKeepsKnownFailureClassification(t *testing.T) {
	for _, tc := range []struct{ output, code string }{
		{"Failed: Wrong password or connection was dropped.", PairErrCode},
		{"Failed: Unable to start pairing client.", PairErrPort},
		{"Failed to parse address for pairing", PairErrAddr},
		{"Successfully paired to 192.0.2.99:37033", PairErrInternal},
	} {
		t.Run(tc.code, func(t *testing.T) {
			a, _ := newWirelessApp()
			p := seedPending(t, a, pairConnectSvcs())
			a.pairOps.pairFn = func(context.Context, string, string, string) (string, error) {
				return tc.output, errors.New("exit status 1")
			}
			if err := a.PairConnect(p.Key, "", "", "", "123456"); err != nil {
				t.Fatal(err)
			}
			if status := waitPairPhase(t, a, PairPhaseFailed); status.ErrCode != tc.code {
				t.Fatalf("nonzero exit masked known failure: %+v", status)
			}
		})
	}
}
