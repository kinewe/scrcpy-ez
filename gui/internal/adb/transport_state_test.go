package adb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

func init() {
	if len(os.Args) == 4 && os.Args[1] == "-s" && os.Args[3] == "get-state" && os.Getenv("SCEZ_TEST_STATE_STDERR") == "1" {
		fmt.Fprintln(os.Stderr, "error: device unauthorized. Please check for a confirmation dialog on your device.")
		os.Exit(1)
	}
}

func TestTransportStatePreservesAuthorizationStderr(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCEZ_TEST_STATE_STDERR", "1")
	state, err := New(exe, "").TransportState(context.Background(), "192.0.2.10:5555")
	if err == nil || !strings.Contains(state, "unauthorized") {
		t.Fatalf("authorization diagnostic lost: state=%q err=%v", state, err)
	}
}
