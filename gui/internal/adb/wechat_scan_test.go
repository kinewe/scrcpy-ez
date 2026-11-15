package adb

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseWeChatScanResolution(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{"handler resolved", "priority=0 preferredOrder=0 match=0x108000 specificIndex=-1 isDefault=true\ncom.tencent.mm/.ui.ShortCutDispatchActivity\n", true},
		{"no activity", "No activity found\n", false},
		{"empty output", "", false},
		{"unrelated component", "com.other.app/.Foo\n", false},
		{"extra whitespace", "  \n\tcom.tencent.mm/.ui.ShortCutDispatchActivity  \n", true},
		{"no-activity wins over trailing junk", "No activity found\ncom.tencent.mm/.ui.ShortCutDispatchActivity\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ParseWeChatScanResolution(c.out); got != c.want {
				t.Fatalf("ParseWeChatScanResolution(%q) = %v, want %v", c.out, got, c.want)
			}
		})
	}
}

// fakeWeChatProbe 记录 adb 调用，并按顺序给出 sdk / resolver 行为。
func fakeWeChatProbe(sdkOut string, sdkErr error, resolve func(attempt int) (string, error)) (func(context.Context, ...string) (string, error), *[]string) {
	calls := &[]string{}
	resolveAttempt := 0
	fn := func(_ context.Context, args ...string) (string, error) {
		cmd := strings.Join(args, " ")
		*calls = append(*calls, cmd)
		if strings.Contains(cmd, "getprop ro.build.version.sdk") {
			return sdkOut, sdkErr
		}
		if strings.Contains(cmd, "resolve-activity") {
			resolveAttempt++
			return resolve(resolveAttempt)
		}
		return "", nil
	}
	return fn, calls
}

const weChatHandlerOut = "priority=0\ncom.tencent.mm/.ui.ShortCutDispatchActivity\n"

func TestHasWeChatScanRequiresAndroid10(t *testing.T) {
	// Android 8（SDK 26）实测 server 报 "New virtual display is not supported before
	// Android 10"：即使微信接口存在也必须隐藏入口。
	resolveCalls := 0
	fn, calls := fakeWeChatProbe("26", nil, func(int) (string, error) {
		resolveCalls++
		return weChatHandlerOut, nil
	})
	m := New("unused", "")
	m.runFn = fn
	if m.hasWeChatScan(context.Background(), "SERIAL") {
		t.Fatal("Android 8 must not advertise the WeChat scan entry")
	}
	if resolveCalls != 0 {
		t.Fatalf("resolver must not run when the OS is too old, calls: %#v", *calls)
	}
}

func TestHasWeChatScanHidesWhenSDKProbeFails(t *testing.T) {
	fn, _ := fakeWeChatProbe("", errors.New("adb: device offline"), func(int) (string, error) {
		return weChatHandlerOut, nil
	})
	m := New("unused", "")
	m.runFn = fn
	if m.hasWeChatScan(context.Background(), "SERIAL") {
		t.Fatal("unknown Android version must default to hidden (capability not proven)")
	}
}

func TestHasWeChatScanFallsBackToPmResolver(t *testing.T) {
	fn, calls := fakeWeChatProbe("36", nil, func(attempt int) (string, error) {
		if attempt == 1 {
			return "", errors.New("unknown command: resolve-activity")
		}
		return weChatHandlerOut, nil
	})
	m := New("unused", "")
	m.runFn = fn
	if !m.hasWeChatScan(context.Background(), "SERIAL") {
		t.Fatal("expected WeChat scan capability from pm fallback")
	}
	joined := strings.Join(*calls, "\n")
	if !strings.Contains(joined, "cmd package resolve-activity") || !strings.Contains(joined, "shell pm resolve-activity") {
		t.Fatalf("unexpected probe calls: %#v", *calls)
	}
}

func TestHasWeChatScanStopsWhenCmdResolverAnswers(t *testing.T) {
	resolveCalls := 0
	fn, _ := fakeWeChatProbe("36", nil, func(int) (string, error) {
		resolveCalls++
		return "No activity found\n", nil
	})
	m := New("unused", "")
	m.runFn = fn
	if m.hasWeChatScan(context.Background(), "SERIAL") {
		t.Fatal("device without WeChat dispatch activity must not advertise the scan entry")
	}
	if resolveCalls != 1 {
		t.Fatalf("expected a single resolver probe when cmd answers, got %d", resolveCalls)
	}
}

func TestHasWeChatScanFalseWhenBothProbesFail(t *testing.T) {
	fn, _ := fakeWeChatProbe("36", nil, func(int) (string, error) {
		return "", errors.New("adb: device offline")
	})
	m := New("unused", "")
	m.runFn = fn
	if m.hasWeChatScan(context.Background(), "SERIAL") {
		t.Fatal("unresolvable capability must default to false (hide the button)")
	}
}

func TestEnrichCachesWeChatScanCapability(t *testing.T) {
	m := New("unused", "")
	probes := 0
	m.runFn = func(_ context.Context, args ...string) (string, error) {
		cmd := strings.Join(args, " ")
		switch {
		case strings.HasSuffix(cmd, "ro.product.marketname"):
			return "REDMI K80", nil
		case strings.HasSuffix(cmd, "ro.build.version.sdk"):
			return "36", nil
		case strings.Contains(cmd, "resolve-activity"):
			probes++
			return weChatHandlerOut, nil
		case strings.Contains(cmd, "dumpsys battery"):
			return "level: 90", nil
		case strings.HasSuffix(cmd, "wm size"):
			return "Physical size: 1080x2400", nil
		default:
			return "120", nil
		}
	}
	d := Device{Serial: "SERIAL", State: "device"}
	m.enrich(context.Background(), &d)
	if !d.WeChatScan {
		t.Fatalf("enrichment must expose wechatScan: %+v", d)
	}
	if probes != 1 {
		t.Fatalf("expected one capability probe, got %d", probes)
	}
	// 第二次富化命中 TTL 缓存：不再探测，能力保持。
	m.enrich(context.Background(), &d)
	if !d.WeChatScan || probes != 1 {
		t.Fatalf("capability cache not reused: WeChatScan=%v probes=%d", d.WeChatScan, probes)
	}
}
