//go:build windows && cgo

package notifications

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

func TestSourceIdentityUsesPackageAndSafeShortcutNames(t *testing.T) {
	first, label := sourceIdentity(Card{Package: "com.synthetic.alpha", App: "合成甲", Device: "甲设备"})
	second, _ := sourceIdentity(Card{Package: "com.synthetic.alpha", App: "别名", Device: "乙设备"})
	other, _ := sourceIdentity(Card{Package: "com.synthetic.beta", App: "合成甲"})
	if first != second || first != other || label != "scrcpy-ez" || first != WindowsAppID+".Unified.EzBrand" {
		t.Fatal("all packages must use the product sender identity")
	}
	for _, name := range []string{"../逃逸", `A\B:C`, "CON", "nul.txt", " . "} {
		got := sourceShortcutName(name)
		if got == "" || strings.ContainsAny(got, `/\:`) || got == "CON" || got == "nul.txt" || filepath.Base(got) != got {
			t.Fatal("unsafe shortcut label")
		}
	}
}

func TestSourceWindowsSenderNamesLifecycleAndCopy(t *testing.T) {
	if os.Getenv("SCEZ_NOTIFICATION_SOURCE_TEST") != "1" {
		t.Skip("opt-in isolated synthetic sender test")
	}
	baseID := fmt.Sprintf("ScrcpyEZ.NotificationTest.Source.%d.%d", os.Getpid(), time.Now().UnixNano())
	sink, err := newAppWindowsSinkWithID(baseID)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := os.UserConfigDir()
	cache, _ := os.UserCacheDir()
	t.Cleanup(func() {
		_ = sink.Close()
		for id := range sink.children {
			for _, path := range []string{`Software\Classes\AppUserModelId\` + id, `Software\Microsoft\Windows\CurrentVersion\Notifications\Settings\` + id} {
				_ = registry.DeleteKey(registry.CURRENT_USER, path)
			}
			_ = registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\CLSID\`+activationCLSID(id)+`\LocalServer32`)
			_ = registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\CLSID\`+activationCLSID(id))
			_ = os.RemoveAll(filepath.Join(base, "Microsoft", "Windows", "Start Menu", "Programs", "scrcpy-ez 手机通知", id))
			_ = os.RemoveAll(filepath.Join(cache, "scrcpy-ez", "notification-senders", id))
		}
	})
	cards := []Card{{Package: "com.synthetic.alpha", App: "合成甲", Group: "synthetic", Tag: "alpha", Device: "合成设备", Title: "合成标题甲", Body: "合成正文甲", Silent: true, Icon: syntheticAppIcon(t)}, {Package: "com.synthetic.beta", App: "合成乙", Group: "synthetic", Tag: "beta", Device: "合成设备", Title: "合成标题乙", Body: "合成正文乙", Silent: true, Icon: syntheticAppIcon(t)}}
	for _, card := range cards {
		if err := sink.Show(card); err != nil {
			t.Fatal(err)
		}
	}
	if len(sink.children) != 1 {
		t.Fatal("source apps must share the product sender")
	}
	for _, card := range cards {
		id, _ := sourceIdentity(card)
		id = baseID + strings.TrimPrefix(id, WindowsAppID)
		child := sink.children[id]
		result := child.call(nativeRequest{op: "senderLabel"})
		if result.err != nil || result.value != "scrcpy-ez" {
			t.Fatalf("Shell sender label=%q error=%v", result.value, result.err)
		}
		if result := child.call(nativeRequest{op: "count"}); result.err != nil || result.count != 2 {
			t.Fatalf("shared history: %+v", result)
		}
		if !child.copyAvailable {
			t.Fatal("source COM activation unavailable")
		}
		copied := make(chan string, 1)
		if result := child.call(nativeRequest{op: "copyWriter", copier: func(code string, _ uint32) error { copied <- code; return nil }}); result.err != nil {
			t.Fatal(result.err)
		}
		card.CopyCode = "850329"
		card.Body = "【合成服务】验证码850329，5分钟内有效。"
		if err := sink.Show(card); err != nil {
			t.Fatal(err)
		}
		token := child.call(nativeRequest{op: "copyToken", group: card.Group, tag: card.Tag}).token
		if token == "" {
			t.Fatal("source copy token missing")
		}
		if err := child.call(nativeRequest{op: "activate", value: token}).err; err != nil {
			t.Fatal(err)
		}
		select {
		case code := <-copied:
			if code != card.CopyCode {
				t.Fatal("wrong copy value")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("source copy callback missing")
		}
	}
	if len(sink.children) != 1 {
		t.Fatal("sender identity was not reused")
	}
	if err := sink.Remove("synthetic", "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := sink.Clear("synthetic"); err != nil {
		t.Fatal(err)
	}
	for _, child := range sink.children {
		if result := child.call(nativeRequest{op: "count"}); result.err != nil || result.count != 0 {
			t.Fatal("source history not cleared")
		}
	}
}
