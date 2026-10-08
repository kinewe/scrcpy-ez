package notifications

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestOrdinaryToastContentBeforeDeviceAndNoCopy(t *testing.T) {
	for _, icon := range []string{"", "file:///C:/test/icon.png"} {
		card := Card{App: "合成应用", Title: "消息主题", Body: strings.Repeat("长正文🙂", 30), Device: "合成平板", Connection: "USB · TEST", IconURI: icon}
		var parsed struct {
			Duration string   `xml:"duration,attr"`
			Launch   string   `xml:"launch,attr"`
			Texts    []string `xml:"visual>binding>text"`
			Footer   []string `xml:"visual>binding>group>subgroup>text"`
		}
		payload := ToastXML(card)
		if err := xml.Unmarshal([]byte(payload), &parsed); err != nil {
			t.Fatal(err)
		}
		if len(parsed.Texts) != 2 || parsed.Texts[0] != card.Title || parsed.Texts[1] != "\n"+strings.Repeat("长正文🙂", 12)+"..." {
			t.Fatal("native title/body alignment or bounded content incorrect")
		}
		if len(parsed.Footer) != 1 || parsed.Footer[0] != card.Device+" · "+card.App+" | "+card.Connection || !strings.Contains(payload, `hint-align="center" hint-wrap="false" hint-maxLines="1"`) || strings.Contains(payload, `hint-minLines="1"`) || strings.Contains(payload, "在线") || strings.Contains(payload, `<subgroup hint-weight=`) {
			t.Fatal("single centered full-width footer missing or contains a spacer/status")
		}
		if parsed.Duration != "short" || parsed.Launch != "" || strings.Contains(payload, "<actions>") || strings.Contains(payload, "......") || strings.Contains(payload, ">合成应用</text>") {
			t.Fatal("ordinary notification has copy action or duplicate app summary")
		}
	}
}

func TestOrdinarySnippetAndTitleFallback(t *testing.T) {
	if ordinarySnippet("短\n正文", 48) != "短 正文" || ordinarySnippet(strings.Repeat("🙂", 49), 48) != strings.Repeat("🙂", 48)+"..." {
		t.Fatal("Unicode truncation or whitespace normalization incorrect")
	}
	for _, c := range []struct {
		card  Card
		title string
	}{{Card{App: "应用", Body: "无标题正文"}, "无标题正文"}, {Card{App: "应用"}, "无文字内容的通知"}, {Card{Title: strings.Repeat("题", 49)}, strings.Repeat("题", 48) + "..."}} {
		var parsed struct {
			Texts []string `xml:"visual>binding>text"`
		}
		if err := xml.Unmarshal([]byte(ToastXML(c.card)), &parsed); err != nil {
			t.Fatal(err)
		}
		if len(parsed.Texts) != 1 || parsed.Texts[0] != c.title {
			t.Fatal("missing title fallback or title truncation")
		}
	}
}
