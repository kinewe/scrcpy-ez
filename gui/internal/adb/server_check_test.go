package adb

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestServerCheckVerifiesTargetedForward(t *testing.T) {
	failed := errors.New("adb exited with status 1")
	for _, tc := range []struct {
		name, mdns, devices, forward, getState string
		forwardErr                             error
		want                                   ServerCheckState
		forwarded, removed, deferred           bool
	}{
		{"compatible", "mdns daemon unavailable", "usb\tdevice usb:1\n10.0.0.2:5555\tdevice\n", "32768\r\n", "", nil, ServerCompatible, true, true, false},
		{"host lacks discovery", "unknown host service", "", "", "", nil, ServerIncompatible, false, false, false},
		{"host cannot target forward", "mdns ok", "10.0.0.2:5555\tdevice\n", "error: more than one device/emulator", "device", failed, ServerIncompatible, true, false, false},
		{"device left during probe", "mdns ok", "10.0.0.2:5555\tdevice\n", "error: device offline", "offline", failed, ServerUnverified, true, false, false},
		{"selection changed", "mdns ok", "10.0.0.2:5555\tdevice\n", "error: more than one device/emulator", "offline", failed, ServerUnverified, true, false, false},
		{"no online device", "mdns ok", "10.0.0.2:5555\toffline\n", "", "", nil, ServerCompatible, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New("adb-noop", "")
			var forwarded, removed bool
			m.srvCommand = func(_ context.Context, args ...string) (string, error) {
				switch command := strings.Join(args, " "); {
				case command == "mdns check":
					if tc.mdns == "mdns ok" {
						return tc.mdns, nil
					}
					return tc.mdns, failed
				case command == "devices -l":
					return "List of devices attached\n" + tc.devices, nil
				case strings.HasPrefix(command, "-s 10.0.0.2:5555 forward tcp:0 localabstract:ez_forward_check_"):
					forwarded = true
					return tc.forward, tc.forwardErr
				case command == "-s 10.0.0.2:5555 forward --remove tcp:32768":
					removed = true
					return "", nil
				case command == "forward --list":
					return "10.0.0.2:5555 tcp:32769 localabstract:unrelated\n", nil
				case command == "-s 10.0.0.2:5555 get-state":
					return tc.getState, nil
				default:
					t.Fatalf("unexpected ADB command: %s", command)
					return "", failed
				}
			}
			got := m.CheckServer(context.Background())
			if got.State != tc.want || got.ForwardDeferred != tc.deferred || forwarded != tc.forwarded || removed != tc.removed {
				t.Fatalf("got %+v, attempted forward=%t, removed=%t", got, forwarded, removed)
			}
		})
	}
}
