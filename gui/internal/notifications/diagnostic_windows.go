//go:build windows && cgo

package notifications

import (
	"encoding/json"
	"os"
)

// Explicit command-line diagnostic: only synthetic data, never reads a phone.
// The caller stops the GUI first so the product's real sender can be exercised.
func RunDiagnosticIfRequested(args []string) bool {
	if len(args) != 2 || args[0] != "--notification-diagnostic-file" {
		return false
	}
	results := make(map[string]string)
	sink, err := NewWindowsSink()
	if err != nil {
		results["open"] = sinkStatus(err)
	} else {
		results["open"] = "ok"
		card := Card{Group: ShortID("own-native-diagnostic"), Tag: ShortID("own-native-diagnostic"), App: "通知测试", Device: "本机诊断", Title: "scrcpy-ez 通知诊断", Body: "仅合成数据，不含手机消息", Silent: true}
		for _, step := range []struct {
			name string
			run  func() error
		}{
			{"show", func() error { return sink.Show(card) }},
			{"remove", func() error { return sink.Remove(card.Group, card.Tag) }},
			{"removeAgain", func() error { return sink.Remove(card.Group, card.Tag) }},
			{"clear", func() error { return sink.Clear(card.Group) }},
		} {
			if err := step.run(); err != nil {
				results[step.name] = sinkStatus(err)
			} else {
				results[step.name] = "ok"
			}
		}
		_ = sink.Close()
	}
	data, _ := json.MarshalIndent(results, "", "  ")
	_ = os.WriteFile(args[1], data, 0600)
	return true
}
