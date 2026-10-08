package notifications

import (
	"encoding/xml"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestOTPScreenshotPatternsUseSyntheticCodes(t *testing.T) {
	cases := []struct{ body, code, sender, validity string }{
		{"【哔哩哔哩】531827短信登录验证码，5分钟内有效，请勿泄露。", "531827", "哔哩哔哩", "5分钟"},
		{"【南方智运】0048为您的手机验证码，您正在登录WarmCar。", "0048", "南方智运", ""},
		{"【中山大学】验证码：628540，1分钟内有效。", "628540", "中山大学", "1分钟"},
		{"【Gitee】您的验证码是874920，识别码是32，请核对识别码再输入验证码，有效期10分钟。", "874920", "Gitee", "10分钟"},
		{"【百度】验证码：039571，请勿转发或泄漏。", "039571", "百度", ""},
		{"【谷歌信息】G-739512 是您的 Google 验证码，请勿分享。", "G-739512", "谷歌信息", ""},
		{"【像素蛋糕】验证码850329，您正在登录。", "850329", "像素蛋糕", ""},
		{"【米哈游】验证码：817503（10分钟内有效）。", "817503", "米哈游", "10分钟"},
		{"[Example] Your verification code is 628540. Expires in 10 minutes.", "628540", "Example", "10分钟"},
	}
	for i, c := range cases {
		if got := ExtractOTP("", c.body); got != c.code {
			t.Errorf("pattern %d: code=%q want=%q", i, got, c.code)
		}
		info := describeOTP(Card{Title: "新短信", App: "短信", Body: c.body})
		if info.sender != c.sender || info.validity != c.validity {
			t.Errorf("pattern %d: presentation=%+v", i, info)
		}
	}
}

func TestOTPPresentationDoesNotInventSenderOrValidity(t *testing.T) {
	cases := []struct{ title, body, sender, validity string }{
		{"登录验证码", "验证码850329，订单将在5分钟后完成。", "合成邮件", ""},
		{"Example", "验证码850329，有效期为：05分钟。", "Example", "5分钟"},
		{"验证码", "验证码850329，5分钟内有效，旧验证码10分钟内有效。", "合成邮件", ""},
		{"验证码", "【<伪造>】验证码850329", "合成邮件", ""},
		{"验证码", "验证码850329，有效期0分钟。", "合成邮件", ""},
		{"验证码", "验证码850329，请在30秒内输入。", "合成邮件", "30秒"},
		{"4条", "验证码850329，5分钟内有效。", "合成邮件", "5分钟"},
		{"４ 条", "验证码850329，5分钟内有效。", "合成邮件", "5分钟"},
		{"四条", "验证码850329，5分钟内有效。", "合成邮件", "5分钟"},
		{"4 messages", "验证码850329，5分钟内有效。", "合成邮件", "5分钟"},
		{"4条", "【示例服务】验证码850329，5分钟内有效。", "示例服务", "5分钟"},
	}
	for i, c := range cases {
		info := describeOTP(Card{Title: c.title, Body: c.body, App: "合成邮件"})
		if info.sender != c.sender || info.validity != c.validity {
			t.Errorf("case %d: %+v", i, info)
		}
	}
}

func TestOTPToastExpandedCodeStyleAndBottomDeviceIdentity(t *testing.T) {
	card := Card{Device: "我的平板", Connection: "USB · TEST", App: "短信", Title: "新短信", Body: "【示例服务】验证码850329，5分钟内有效。<action/>\x00", CopyCode: "850329", Token: strings.Repeat("a", 32), IconURI: "file:///C:/test/icon.png"}
	payload := ToastXML(card)
	var parsed struct {
		Duration string   `xml:"duration,attr"`
		Launch   string   `xml:"launch,attr"`
		Texts    []string `xml:"visual>binding>text"`
		Images   []struct {
			Placement string `xml:"placement,attr"`
			Source    string `xml:"src,attr"`
		} `xml:"visual>binding>image"`
		Footer  []string `xml:"visual>binding>group>subgroup>text"`
		Actions []struct {
			Arguments string `xml:"arguments,attr"`
		} `xml:"actions>action"`
	}
	if err := xml.Unmarshal([]byte(payload), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Duration != "long" || len(parsed.Texts) != 1 || parsed.Texts[0] != "示例服务 · 验证码：850329" {
		t.Fatal("collapsed code summary missing")
	}
	if len(parsed.Images) != 0 || !strings.Contains(payload, `<subgroup hint-weight="3"><image src="`+card.IconURI+`" hint-align="stretch" hint-removeMargin="true"/>`) || strings.Contains(payload, `<subgroup hint-weight="1"/>`) || !strings.Contains(payload, `<subgroup hint-weight="17">`) || !strings.Contains(payload, `hint-style="subtitle"`) {
		t.Fatal("bounded adaptive icon column, compact gap or chosen code style missing")
	}
	if len(parsed.Footer) != 3 || parsed.Footer[0] != card.CopyCode || parsed.Footer[1] != "有效期：5分钟" || parsed.Footer[2] != "我的平板 · 短信 | "+card.Connection || strings.Contains(payload, `hint-minLines="1"`) || !strings.Contains(payload, `</subgroup></group><group><subgroup><text hint-style="captionSubtle" hint-align="center"`) || strings.Contains(payload, "在线") {
		t.Fatal("code, validity-only summary or full-width centered footer without spacer missing")
	}
	if strings.Contains(payload, "<action/>") || strings.Contains(payload, "\x00") || strings.Contains(payload, "新短信") {
		t.Fatal("original OTP title/body leaked into concise layout")
	}
	if parsed.Launch != "copy:"+card.Token || len(parsed.Actions) != 1 || parsed.Actions[0].Arguments != parsed.Launch {
		t.Fatal("body/button action differs")
	}
	card.Silent = true
	if err := xml.Unmarshal([]byte(ToastXML(card)), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Duration != "short" || parsed.Launch != "copy:"+card.Token {
		t.Fatal("silent metadata update must retain short duration and the same action")
	}
}

func TestOTPCountTitleAndUnknownValidityPresentation(t *testing.T) {
	for _, icon := range []string{"", "file:///C:/test/icon.png"} {
		for _, body := range []string{"验证码850329，5分钟内有效。", "验证码850329。", "验证码850329，5分钟内有效，旧验证码10分钟内有效。"} {
			card := Card{App: "短信", Device: "合成手机", Title: "4条", Body: body, CopyCode: "850329", IconURI: icon}
			payload := ToastXML(card)
			var parsed struct {
				Texts  []string `xml:"visual>binding>text"`
				Footer []string `xml:"visual>binding>group>subgroup>text"`
			}
			if err := xml.Unmarshal([]byte(payload), &parsed); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(payload, "4条") || len(parsed.Texts) != 1 || parsed.Texts[0] != "短信 · 验证码：850329" {
				t.Fatal("notification count was treated as a sender")
			}
			if body == "验证码850329，5分钟内有效。" {
				if parsed.Footer[1] != "有效期：5分钟" {
					t.Fatal("validity row contains a sender or count")
				}
			} else if len(parsed.Footer) != 2 || strings.Contains(payload, "有效期") {
				t.Fatal("missing or ambiguous validity was invented")
			}
		}
	}
}

func TestCopyCapacityKeepsNewestEligible(t *testing.T) {
	var store copyStore
	now := time.Unix(100, 0)
	first := ""
	for i := 0; i < 256; i++ {
		token := store.Issue(Card{Group: "capacity", Tag: fmt.Sprint(i), CopyCode: "850329"}, now)
		if token == "" {
			t.Fatalf("new notification %d lost its copy action", i)
		}
		if i == 0 {
			first = token
		}
		if len(store.entries) > 128 {
			t.Fatal("copy cache exceeded bound")
		}
		if i == 255 && !store.Copy(token, now, func(string) error { return nil }) {
			t.Fatal("newest token unusable")
		}
	}
	if store.Copy(first, now, func(string) error { return nil }) {
		t.Fatal("oldest token retained after capacity eviction")
	}
}
