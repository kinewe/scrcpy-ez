//go:build windows

package ui

import (
	"strings"
	"testing"
)

func TestCheckWebViewRuntimeReportsMissingLoader(t *testing.T) {
	err := CheckWebViewRuntime(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "WebView2Loader.dll") {
		t.Fatalf("expected a missing-loader error, got %v", err)
	}
}
