package notifications

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const maxIconBytes = 512 * 1024
const maxIconDimension = 1024

type Artwork struct {
	App  string
	Icon AppIcon
}

// Preserve the source PNG, including its pixel dimensions and transparency.
// Notification layout controls display size; transport must not pre-shrink it.
func IconFromPNG(data []byte) AppIcon {
	if len(data) == 0 || len(data) > maxIconBytes {
		return AppIcon{}
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > maxIconDimension || config.Height > maxIconDimension {
		return AppIcon{}
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return AppIcon{}
	}
	hash := sha256.Sum256(data)
	return AppIcon{ID: hex.EncodeToString(hash[:]), PNG: base64.StdEncoding.EncodeToString(data)}
}

func iconBytes(icon AppIcon) []byte {
	if len(icon.ID) != 64 || len(icon.PNG) > base64.StdEncoding.EncodedLen(maxIconBytes) {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(icon.PNG)
	if err != nil || len(data) == 0 || len(data) > maxIconBytes {
		return nil
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != icon.ID {
		return nil
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > maxIconDimension || config.Height > maxIconDimension {
		return nil
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return nil
	}
	return data
}

func validIcon(icon AppIcon) bool { return iconBytes(icon) != nil }

// Keep native pixels. Square sources pass through unchanged; rectangular ones
// get transparent padding without resampling. Windows then scales this image
// to appLogoOverride or the existing OTP column at the actual display DPI.
func toastIconPNG(data []byte) []byte {
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > maxIconDimension || config.Height > maxIconDimension {
		return nil
	}
	source, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	size := max(width, height)
	if size > maxIconDimension {
		return nil
	}
	if width == height {
		return data
	}
	output := image.NewNRGBA(image.Rect(0, 0, size, size))
	left, top := (size-width)/2, (size-height)/2
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			output.Set(left+x, top+y, source.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}
	var encoded bytes.Buffer
	if png.Encode(&encoded, output) != nil {
		return nil
	}
	return encoded.Bytes()
}

// Render a separate display asset; keep the received high-resolution PNG intact.
// Some Windows banner paths point-sample appLogoOverride while the notification
// center filters it. Area integration supplies antialiasing before either path
// draws the icon. RGBA() supplies premultiplied channels, avoiding dark fringes.
func toastDisplayPNG(data []byte, size int) []byte {
	if size < 1 || size > 384 {
		return nil
	}
	source, err := png.Decode(bytes.NewReader(data))
	if err != nil || source.Bounds().Dx() != source.Bounds().Dy() {
		return nil
	}
	n := source.Bounds().Dx()
	if n < 1 || n > maxIconDimension {
		return nil
	}
	if size == n {
		return data
	}
	output := image.NewRGBA(image.Rect(0, 0, size, size))
	scale := float64(n) / float64(size)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var channels [4]float64
			var total float64
			add := func(sx, sy int, weight float64) {
				r, g, b, a := source.At(source.Bounds().Min.X+max(0, min(n-1, sx)), source.Bounds().Min.Y+max(0, min(n-1, sy))).RGBA()
				for i, v := range []uint32{r, g, b, a} {
					channels[i] += float64(v) * weight
				}
				total += weight
			}
			if scale > 1 {
				left, top := float64(x)*scale, float64(y)*scale
				right, bottom := left+scale, top+scale
				for sy := int(math.Floor(top)); sy < int(math.Ceil(bottom)); sy++ {
					wy := math.Min(bottom, float64(sy+1)) - math.Max(top, float64(sy))
					for sx := int(math.Floor(left)); sx < int(math.Ceil(right)); sx++ {
						wx := math.Min(right, float64(sx+1)) - math.Max(left, float64(sx))
						add(sx, sy, wx*wy)
					}
				}
			} else {
				fx, fy := (float64(x)+.5)*scale-.5, (float64(y)+.5)*scale-.5
				sx, sy := int(math.Floor(fx)), int(math.Floor(fy))
				wx, wy := fx-float64(sx), fy-float64(sy)
				add(sx, sy, (1-wx)*(1-wy))
				add(sx+1, sy, wx*(1-wy))
				add(sx, sy+1, (1-wx)*wy)
				add(sx+1, sy+1, wx*wy)
			}
			var c [4]uint8
			for i, v := range channels {
				c[i] = uint8(math.Round(v / total / 257))
			}
			output.SetRGBA(x, y, color.RGBA{R: c[0], G: c[1], B: c[2], A: c[3]})
		}
	}
	var encoded bytes.Buffer
	if png.Encode(&encoded, output) != nil {
		return nil
	}
	return encoded.Bytes()
}

// Only app artwork is written to this owned, bounded, temporary directory.
// File URIs are needed because desktop Windows toasts cannot load data URLs.
type iconStore struct {
	dir       string
	files     map[string]string
	pixelSize int // Native toast display size at the current Windows system DPI.
}

func (s *iconStore) URI(icon AppIcon) string {
	key := icon.ID
	if s.pixelSize > 0 {
		key += "-" + fmt.Sprint(s.pixelSize)
	}
	if path := s.files[key]; path != "" {
		return path
	}
	if len(s.files) >= 256 {
		return ""
	}
	data := iconBytes(icon)
	if data == nil {
		return ""
	}
	data = toastIconPNG(data)
	if s.pixelSize > 0 {
		data = toastDisplayPNG(data, s.pixelSize)
	}
	if data == nil {
		return ""
	}
	if s.dir == "" {
		dir, err := os.MkdirTemp("", "scrcpy-ez-notification-icons-")
		if err != nil {
			return ""
		}
		s.dir = dir
		s.files = make(map[string]string)
	}
	path := filepath.Join(s.dir, key+".png")
	if err := os.WriteFile(path, data, 0600); err != nil {
		_ = os.Remove(path)
		return ""
	}
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := (&url.URL{Scheme: "file", Path: uriPath}).String()
	s.files[key] = uri
	return uri
}

func (s *iconStore) Close() {
	if s.dir == "" {
		return
	}
	for id := range s.files {
		_ = os.Remove(filepath.Join(s.dir, id+".png"))
	}
	_ = os.Remove(s.dir)
	s.dir = ""
	s.files = nil
}
