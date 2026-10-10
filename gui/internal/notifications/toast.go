package notifications

import (
	"bytes"
	"encoding/hex"
	"encoding/xml"
	"strings"
)

func xmlText(value string) string {
	value = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || r >= 0x20 && r <= 0xD7FF || r >= 0xE000 && r <= 0xFFFD || r >= 0x10000 && r <= 0x10FFFF {
			return r
		}
		return -1
	}, value)
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(value))
	return out.String()
}

func toastSnippet(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return value
}

func ordinarySnippet(value string, limit int) string {
	runes := []rune(strings.Join(strings.Fields(value), " "))
	if len(runes) > limit {
		return string(runes[:limit]) + "..."
	}
	return string(runes)
}

// A confirmation is a passive, short notification. It contains neither the
// code nor an activation token, and cannot trigger another copy.
func copySuccessXML() string {
	return `<toast duration="short"><visual><binding template="ToastGeneric"><text hint-maxLines="1">✓ 复制成功</text><text hint-maxLines="2">验证码已复制到剪贴板</text></binding></visual><audio silent="true"/></toast>`
}

// One full-width footer below either content layout, without spacer rows.
func toastDeviceFooter(card Card) string {
	identity := make([]string, 0, 2)
	for _, value := range []string{card.Device, card.App} {
		if value = toastSnippet(strings.Join(strings.Fields(value), " "), 48); value != "" {
			identity = append(identity, value)
		}
	}
	parts := make([]string, 0, 2)
	if len(identity) != 0 {
		parts = append(parts, strings.Join(identity, " · "))
	}
	if connection := strings.Join(strings.Fields(card.Connection), " "); connection != "" {
		parts = append(parts, toastSnippet(connection, 96))
	}
	footer := strings.Join(parts, " | ")
	if footer == "" {
		return ""
	}
	return `<group><subgroup><text hint-style="captionSubtle" hint-align="center" hint-wrap="false" hint-maxLines="1">` + xmlText(footer) + `</text></subgroup></group>`
}

// The shell displays the source application's identity above the card.
// Root title/body use its native icon alignment, with no duplicate app summary.
func ToastXML(card Card) string {
	activation, actions := "", ""
	if card.CopyCode != "" && len(card.Token) == 32 {
		if _, err := hex.DecodeString(card.Token); err == nil {
			activation = ` launch="copy:` + card.Token + `"`
			actions = `<actions><action content="复制验证码" arguments="copy:` + card.Token + `" activationType="foreground"/></actions>`
		}
	}
	if card.CopyCode == "" && card.Open != nil && validActionToken(card.OpenToken) {
		activation = ` launch="open:` + card.OpenToken + `"`
	}
	title, body := ordinarySnippet(card.Title, 48), ordinarySnippet(card.Body, 48)
	if card.CopyCode != "" {
		info := describeOTP(card)
		icon, weight := "", ""
		if strings.HasPrefix(card.IconURI, "file:///") {
			// The shell's 320-DIP content width needs a 48-DIP icon: 3/20,
			// matching appLogoOverride rather than the former 1/6 (53.3 DIP).
			// Stretch retains the high-resolution source while sizing its column.
			icon = `<subgroup hint-weight="3"><image src="` + xmlText(card.IconURI) + `" hint-align="stretch" hint-removeMargin="true"/></subgroup>`
			weight = ` hint-weight="17"`
		}
		collapsed := "验证码：" + toastSnippet(card.CopyCode, 10)
		if info.sender != "" {
			collapsed = toastSnippet(info.sender, 16) + " · " + collapsed
		}
		content := `<text hint-style="subtitle" hint-wrap="true" hint-maxLines="1">` + xmlText(toastSnippet(card.CopyCode, 10)) + `</text>`
		if info.validity != "" {
			content += `<text hint-style="body" hint-wrap="true" hint-maxLines="2">` + xmlText("有效期："+info.validity) + `</text>`
		}
		duration := "long"
		if card.Silent {
			duration = "short"
		}
		return `<toast duration="` + duration + `"` + activation + `><visual><binding template="ToastGeneric"><text hint-maxLines="2">` + xmlText(collapsed) + `</text><group>` + icon + `<subgroup` + weight + `>` + content + `</subgroup></group>` + toastDeviceFooter(card) + `</binding></visual>` + actions + `</toast>`
	} else if title == "" {
		title, body = body, ""
		if title == "" {
			title = "无文字内容的通知"
		}
	}
	content := `<text hint-maxLines="2">` + xmlText(title) + `</text>`
	if body != "" {
		content += `<text hint-maxLines="3">` + xmlText("\n"+body) + `</text>`
	}
	icon := ""
	if strings.HasPrefix(card.IconURI, "file:///") {
		icon = `<image placement="appLogoOverride" src="` + xmlText(card.IconURI) + `"/>`
	}
	return `<toast duration="short"` + activation + `><visual><binding template="ToastGeneric">` + content + icon + toastDeviceFooter(card) + `</binding></visual>` + actions + `</toast>`
}
