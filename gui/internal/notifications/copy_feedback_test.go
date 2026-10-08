package notifications

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestCopyConfirmationIsShortPassiveAndDoesNotExposeCode(t *testing.T) {
	var document struct {
		XMLName  xml.Name   `xml:"toast"`
		Duration string     `xml:"duration,attr"`
		Launch   string     `xml:"launch,attr"`
		Actions  []struct{} `xml:"actions"`
		Audio    struct {
			Silent bool `xml:"silent,attr"`
		} `xml:"audio"`
	}
	payload := copySuccessXML()
	if err := xml.Unmarshal([]byte(payload), &document); err != nil {
		t.Fatal(err)
	}
	if document.Duration != "short" || document.Launch != "" || len(document.Actions) != 0 || !document.Audio.Silent {
		t.Fatal("confirmation must be short, silent and passive")
	}
	if !strings.Contains(payload, "✓ 复制成功") || strings.Contains(payload, "copy:") || strings.Contains(payload, "850329") {
		t.Fatal("incorrect confirmation or leaked activation")
	}
}
