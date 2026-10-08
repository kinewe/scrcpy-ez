package notifications

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func syntheticAppIcon(t *testing.T) AppIcon {
	t.Helper()
	bitmap := image.NewNRGBA(image.Rect(0, 0, 96, 96))
	for y := 0; y < 96; y++ {
		for x := 0; x < 96; x++ {
			bitmap.Set(x, y, color.NRGBA{R: 50, G: 130, B: 220, A: 255})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, bitmap); err != nil {
		t.Fatal(err)
	}
	return IconFromPNG(data.Bytes())
}

func TestArtworkValidationOwnedFilesAndCleanup(t *testing.T) {
	icon := syntheticAppIcon(t)
	if !validIcon(icon) {
		t.Fatal("valid catalog artwork was rejected")
	}
	bad := icon
	bad.ID = strings.Repeat("0", 64)
	if validIcon(bad) {
		t.Fatal("content hash mismatch accepted")
	}
	bad.ID = "../outside"
	if validIcon(bad) {
		t.Fatal("non-content path accepted")
	}
	var store iconStore
	uri := store.URI(icon)
	if !strings.HasPrefix(uri, "file:///") || store.URI(icon) != uri || len(store.files) != 1 {
		t.Fatal("local artwork was not reused")
	}
	data, err := os.ReadFile(filepath.Join(store.dir, icon.ID+".png"))
	if err != nil {
		t.Fatal(err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != 96 || config.Height != 96 || !validIcon(icon) || !bytes.Equal(data, iconBytes(icon)) {
		t.Fatal("square source artwork must retain its original pixels and PNG bytes")
	}
	dir := store.dir
	store.Close()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("owned artwork directory survived close")
	}
}

func TestNotificationArtworkKeepsAspectAndTransparency(t *testing.T) {
	bitmap := image.NewNRGBA(image.Rect(0, 0, 16, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 16; x++ {
			bitmap.Set(x, y, color.NRGBA{R: 50, G: 130, B: 220, A: 128})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, bitmap); err != nil {
		t.Fatal(err)
	}
	result, err := png.Decode(bytes.NewReader(toastIconPNG(encoded.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	if result.Bounds() != image.Rect(0, 0, 16, 16) {
		t.Fatal("rectangular artwork must get padding without being resampled")
	}
	for _, point := range []struct {
		x, y  int
		alpha uint32
	}{{8, 3, 0}, {8, 4, 128 * 257}, {8, 11, 128 * 257}, {8, 12, 0}} {
		_, _, _, alpha := result.At(point.x, point.y).RGBA()
		if alpha != point.alpha {
			t.Fatal("aspect ratio, centered padding or source alpha changed")
		}
	}
}

func TestHighResolutionArtworkPreservesSourceAndFitsTransport(t *testing.T) {
	bitmap := image.NewNRGBA(image.Rect(0, 0, 234, 234))
	seed := uint32(1)
	for index := range bitmap.Pix {
		seed = seed*1664525 + 1013904223
		bitmap.Pix[index] = byte(seed >> 24)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, bitmap); err != nil {
		t.Fatal(err)
	}
	icon := IconFromPNG(encoded.Bytes())
	if !bytes.Equal(iconBytes(icon), encoded.Bytes()) {
		t.Fatal("source resolution, alpha or PNG bytes were changed before display")
	}
	session := strings.Repeat("a", 32)
	payload, err := json.Marshal(Frame{Version: ProtocolVersion, Session: session, Sequence: 1, Type: "icon", Icon: icon})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) <= 64*1024 || len(payload) >= MaxFrame {
		t.Fatal("test must exercise artwork larger than the previous frame limit")
	}
	parsed, err := ParseFrame(payload, session)
	if err != nil || !bytes.Equal(iconBytes(parsed.Icon), encoded.Bytes()) {
		t.Fatal("native artwork did not survive the notification wire protocol")
	}
}

func TestOversizedArtworkRemainsOptional(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, maxIconDimension+1, 1))); err != nil {
		t.Fatal(err)
	}
	if IconFromPNG(encoded.Bytes()).ID != "" || toastIconPNG(encoded.Bytes()) != nil {
		t.Fatal("oversized image dimensions were accepted")
	}
	if IconFromPNG(make([]byte, maxIconBytes+1)).ID != "" {
		t.Fatal("oversized encoded artwork was accepted")
	}
}

func TestMalformedOptionalArtworkDoesNotDisableText(t *testing.T) {
	session := strings.Repeat("a", 32)
	frame := Frame{Version: ProtocolVersion, Session: session, Sequence: 2, Type: "icon", Icon: AppIcon{ID: "malformed", PNG: "not a PNG"}}
	data, _ := json.Marshal(frame)
	parsed, err := ParseFrame(data, session)
	if err != nil || parsed.Icon.ID != "" {
		t.Fatal("optional artwork did not degrade gracefully")
	}
	s := NewState("device:a", "平板", true)
	s.connection = "无线 · 192.0.2.10:5555"
	sink := &testSink{}
	for _, f := range []Frame{{Sequence: 1, Type: "hello", Cutoff: 1}, parsed, {Sequence: 3, Type: "ready"}, {Sequence: 4, Type: "post", Record: Record{Key: "one", Package: "com.synthetic", App: "Synthetic", Title: "hello", PostTime: 2}}} {
		if err := s.Apply(f, sink); err != nil {
			t.Fatal(err)
		}
	}
	if len(sink.shown) != 1 || sink.shown[0].Connection != s.connection {
		t.Fatal("text or device identity lost with missing artwork")
	}
}

func TestArtworkReferenceAndFallbackBoundaries(t *testing.T) {
	icon := syntheticAppIcon(t)
	s := NewState("a", "设备", true)
	sink := &testSink{}
	lookups := 0
	s.artwork = func(string) Artwork { lookups++; return Artwork{App: "Catalog fallback", Icon: icon} }
	frames := []Frame{{Sequence: 1, Type: "hello", Cutoff: 1}, {Sequence: 2, Type: "icon", Icon: icon}, {Sequence: 3, Type: "ready"}, {Sequence: 4, Type: "post", Record: Record{Key: "a", App: "Current app", Package: "com.synthetic", IconID: icon.ID, PostTime: 2}}, {Sequence: 5, Type: "post", Record: Record{Key: "b", App: "com.synthetic", Package: "com.synthetic", PostTime: 3}}, {Sequence: 6, Type: "post", Record: Record{Key: "c", App: "com.synthetic", Package: "com.synthetic", PostTime: 4}}}
	for _, f := range frames {
		if err := s.Apply(f, sink); err != nil {
			t.Fatal(err)
		}
	}
	if lookups != 1 || sink.shown[0].App != "Current app" || sink.shown[0].Icon.ID != icon.ID || sink.shown[1].App != "Catalog fallback" {
		t.Fatal("current artwork/fallback/cache election failed")
	}
}

func TestToastDeviceLineAndOptionalAppArtwork(t *testing.T) {
	card := Card{Device: "我的平板 & 工作", Connection: "无线 · 192.0.2.10:5555", App: "Mail", Title: "验证 <消息>", Body: "正文", IconURI: "file:///C:/owned/icon.png"}
	xml := ToastXML(card)
	for _, want := range []string{"我的平板 &amp; 工作 · Mail | 无线 · 192.0.2.10:5555</text>", ">验证 &lt;消息&gt;</text>", `placement="appLogoOverride"`, `hint-align="center" hint-wrap="false" hint-maxLines="1"`} {
		if !strings.Contains(xml, want) {
			t.Fatalf("missing visual field %s", want)
		}
	}
	if strings.LastIndex(xml, "正文") > strings.Index(xml, "我的平板") || strings.Index(xml, "</text>") > strings.Index(xml, "<image ") {
		t.Fatal("native title/body must precede artwork and bottom device identity")
	}
	card.App = ""
	card.IconURI = "https://external.invalid/icon.png"
	xml = ToastXML(card)
	if strings.Contains(xml, "external.invalid") || !strings.Contains(xml, "我的平板") {
		t.Fatal("app-name omission changed device identity or allowed remote artwork")
	}
}
