package notifications

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/png"
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

// Only app artwork is written to this owned, bounded, temporary directory.
// File URIs are needed because desktop Windows toasts cannot load data URLs.
type iconStore struct {
	dir   string
	files map[string]string
}

func (s *iconStore) URI(icon AppIcon) string {
	if path := s.files[icon.ID]; path != "" {
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
	path := filepath.Join(s.dir, icon.ID+".png")
	if err := os.WriteFile(path, data, 0600); err != nil {
		_ = os.Remove(path)
		return ""
	}
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := (&url.URL{Scheme: "file", Path: uriPath}).String()
	s.files[icon.ID] = uri
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
