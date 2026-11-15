package adb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// Run this test executable as a tiny adb stand-in on Windows and Unix alike.
func init() {
	if len(os.Args) == 4 && os.Args[1] == "pair" {
		switch os.Getenv("SCRCPYEZ_TEST_PAIR_STDERR") {
		case "1":
			fmt.Fprintln(os.Stderr, "Failed: Wrong password or connection was dropped.")
			os.Exit(1)
		case "success":
			fmt.Fprintln(os.Stderr, "* daemon not running; starting now at tcp:5037")
			fmt.Fprintln(os.Stdout, "Successfully paired to 192.0.2.10:37000")
			os.Exit(0)
		}
	}
}

func TestPairCapturesStderrFromFailedProcess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCRCPYEZ_TEST_PAIR_STDERR", "1")
	out, err := New(exe, "").Pair(context.Background(), "192.0.2.10", "37000", "123456")
	if err == nil || !strings.Contains(out, "Wrong password") || PairErrKind(out) != "code" {
		t.Fatalf("failed process lost its diagnostic output: out=%q err=%v", out, err)
	}
}

func TestPairDaemonStartupDoesNotMaskSuccessfulPairing(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCRCPYEZ_TEST_PAIR_STDERR", "success")
	out, err := New(exe, "").Pair(context.Background(), "192.0.2.10", "37000", "123456")
	if err != nil || !strings.Contains(out, "daemon not running") || PairErrKind(out) != "" {
		t.Fatalf("daemon startup masked success: out=%q err=%v", out, err)
	}
}
