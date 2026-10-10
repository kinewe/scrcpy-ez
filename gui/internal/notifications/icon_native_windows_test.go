//go:build windows && cgo

package notifications

import (
	"bytes"
	"fmt"
	"image/png"
	"os"
	"testing"
)

func TestWindowsToastDisplayAsset(t *testing.T) {
	source, target := os.Getenv("SCEZ_ICON_SOURCE"), os.Getenv("SCEZ_ICON_OUTPUT")
	if source == "" || target == "" {
		t.Skip("opt-in public artwork and isolated native sender")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	master := IconFromPNG(data)
	if !validIcon(master) {
		t.Fatal("invalid public artwork")
	}
	size := nativeToastIconSize()
	rendered := toastDisplayPNG(toastIconPNG(data), size)
	if err := os.WriteFile(target, rendered, 0600); err != nil {
		t.Fatal(err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(rendered))
	if err != nil || config.Width != size {
		t.Fatal("wrong native DPI asset")
	}
	sink := testNativeSink(t)
	if err := sink.Show(Card{Group: "icon-test", Tag: "icon-test", Title: "ez 图标抗锯齿测试", Body: "仅公开应用图标与合成测试文字", Device: "本机测试", App: "公开图标", Icon: master, Silent: true}); err != nil {
		t.Fatal(err)
	}
	result := sink.call(nativeRequest{op: "contains", value: fmt.Sprintf("%s-%d.png", master.ID, size)})
	if result.err != nil || result.count != 1 {
		t.Fatalf("native history did not reference the DPI asset: %+v", result)
	}
	t.Logf("NATIVE_ICON_SIZE=%d; display asset accepted by Windows toast history", size)
}
