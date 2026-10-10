// Package clientlog parses the client output shared by the GUI and supervisor.
package clientlog

import "regexp"

var texture = regexp.MustCompile(`(?i)\bTexture(?:\s+\((?:D3D11VA|VA-API|VideoToolbox)\))?:\s*(\d{2,5})x(\d{2,5})\b`)

// TextureSize reports decoded frame dimensions for software or hardware interop.
func TextureSize(line string) string {
	if match := texture.FindStringSubmatch(line); match != nil {
		return match[1] + "x" + match[2]
	}
	return ""
}
