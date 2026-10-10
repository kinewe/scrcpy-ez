package notifications

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/url"
	"os"
	"testing"
)

func encodeScaleFixture(t *testing.T, source image.Image) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, source); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestToastDisplayIntegratesEdgesWithoutTransparentColorFringes(t *testing.T) {
	// A half-covered pixel must carry half alpha and pure red. Transparent blue
	// must not contaminate the visible color, including after PNG serialization.
	source := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		source.SetNRGBA(0, y, color.NRGBA{R: 255, A: 255})
		source.SetNRGBA(1, y, color.NRGBA{B: 255})
	}
	data := encodeScaleFixture(t, source)
	scaled, err := png.Decode(bytes.NewReader(toastDisplayPNG(data, 1)))
	if err != nil {
		t.Fatal(err)
	}
	c := color.NRGBAModel.Convert(scaled.At(0, 0)).(color.NRGBA)
	if c.R < 254 || c.G != 0 || c.B != 0 || c.A < 127 || c.A > 128 {
		t.Fatalf("colored fringe or lost coverage: %+v", c)
	}
	// Check a high-frequency opaque source averages rather than point-samples.
	source.SetNRGBA(1, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	source.SetNRGBA(0, 0, color.NRGBA{A: 255})
	source.SetNRGBA(0, 1, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	source.SetNRGBA(1, 1, color.NRGBA{A: 255})
	scaled, err = png.Decode(bytes.NewReader(toastDisplayPNG(encodeScaleFixture(t, source), 1)))
	if err != nil {
		t.Fatal(err)
	}
	c = color.NRGBAModel.Convert(scaled.At(0, 0)).(color.NRGBA)
	if c.R < 127 || c.R > 128 || c.R != c.G || c.G != c.B || c.A != 255 {
		t.Fatalf("point sampled: %+v", c)
	}
}

func TestToastDisplayCacheTracksDPISizeAndPreservesMaster(t *testing.T) {
	icon := syntheticAppIcon(t)
	master := iconBytes(icon)
	store := iconStore{pixelSize: 48}
	defer store.Close()
	first := store.URI(icon)
	if first == "" || store.URI(icon) != first {
		t.Fatal("missing/recreated display asset")
	}
	for _, size := range []int{48, 60, 72, 96, 192} {
		store.pixelSize = size
		uri := store.URI(icon)
		u, err := url.Parse(uri)
		if err != nil {
			t.Fatal(err)
		}
		path := u.Path
		if len(path) > 2 && path[0] == '/' && path[2] == ':' {
			path = path[1:]
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width != size || cfg.Height != size {
			t.Fatalf("wrong DPI dimensions: %+v", cfg)
		}
	}
	if !bytes.Equal(master, iconBytes(icon)) {
		t.Fatal("source PNG altered")
	}
	if toastDisplayPNG(master, 0) != nil || toastDisplayPNG(master, 385) != nil {
		t.Fatal("unbounded display asset")
	}
}
