package clientlog

import "testing"

func TestTextureSizeFromClientLogs(t *testing.T) {
	for _, line := range []string{
		"INFO: Texture: 1024x768\r\n",
		"INFO: Texture (D3D11VA): 1024x768",
		"[server] INFO: Texture (VA-API): 1024x768",
		"INFO: Texture (VideoToolbox): 1024x768",
	} {
		if got := TextureSize(line); got != "1024x768" {
			t.Fatalf("client frame dimensions %q: %q", line, got)
		}
	}
	for _, line := range []string{"INFO: Texture (D3D11VA):", "INFO: Texture initialization failed", "ABR: 1024x768", "NotTexture: 1024x768"} {
		if got := TextureSize(line); got != "" {
			t.Fatalf("non-frame log reported a decoded image: %q", line)
		}
	}
}
