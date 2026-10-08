package notifications

import (
	"regexp"
	"strings"
	"time"
	"unicode"
)

var otpSignature = regexp.MustCompile(`^\s*(?:【([^】\r\n]{1,48})】|\[([^\]\r\n]{1,48})\])`)
var otpValidityCN = regexp.MustCompile(`(?:有效期(?:为)?\s*[:：]?\s*([0-9]{1,3})\s*(秒|分钟|小时)|([0-9]{1,3})\s*(秒|分钟|小时)\s*(?:内有效|有效|后(?:过期|失效))|(?:请在|须在|需在)\s*([0-9]{1,3})\s*(秒|分钟|小时)\s*内(?:使用|输入|完成(?:验证|登录)?))`)
var otpValidityEN = regexp.MustCompile(`(?i)\b(?:valid\s+for|expires?\s+in|expires?\s+after)\s+([0-9]{1,3})\s*(seconds?|minutes?|hours?)\b`)
var otpGenericTitle = regexp.MustCompile(`(?i)验证码|验证|校验|登录|安全|新消息|通知|短信|邮件|\b(?:verification|login|security|notification|message|email|otp|passcode)\b`)
var otpCountTitle = regexp.MustCompile(`(?i)^(?:[0-9]+|[一二三四五六七八九十百千万两]+)\s*(?:条|则|封|个)(?:\s*(?:新)?(?:消息|短信|通知|邮件))?$|^[0-9]+\s+(?:new\s+)?(?:messages?|notifications?|emails?|texts?)$`)

type otpPresentation struct {
	sender, validity string
	duration         time.Duration
}

func otpSenderName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > 48 || otpGenericTitle.MatchString(value) || otpCountTitle.MatchString(normalizeOTP(value)) {
		return ""
	}
	letters := false
	for _, r := range value {
		if unicode.IsLetter(r) {
			letters = true
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) && !unicode.IsSpace(r) && !strings.ContainsRune(".&_@+-", r) {
			return ""
		}
	}
	if !letters {
		return ""
	}
	return value
}

// Display only what the original notification states. This does not infer the
// server's actual expiration, or turn a sender signature into a verified identity.
func describeOTP(card Card) otpPresentation {
	result := otpPresentation{}
	for _, raw := range []string{card.Body, card.Title} {
		text := normalizeOTP(raw)
		if match := otpSignature.FindStringSubmatch(text); len(match) > 0 {
			result.sender = otpSenderName(match[1] + match[2])
			if result.sender != "" {
				break
			}
		}
	}
	if result.sender == "" {
		result.sender = otpSenderName(card.Title)
	}
	if result.sender == "" {
		result.sender = toastSnippet(card.App, 48)
	}
	text := normalizeOTP(card.Title + "\n" + card.Body)
	values := make(map[time.Duration]string)
	add := func(number, unit string) {
		number = strings.TrimLeft(number, "0")
		if number == "" {
			return
		}
		count := 0
		for _, digit := range number {
			count = count*10 + int(digit-'0')
		}
		scale := time.Second
		if unit == "分钟" {
			scale = time.Minute
		} else if unit == "小时" {
			scale = time.Hour
		}
		values[time.Duration(count)*scale] = number + unit
	}
	for _, match := range otpValidityCN.FindAllStringSubmatch(text, 16) {
		for i := 1; i < len(match); i += 2 {
			if match[i] != "" {
				add(match[i], match[i+1])
			}
		}
	}
	for _, match := range otpValidityEN.FindAllStringSubmatch(text, 16) {
		unit := strings.ToLower(match[2])
		switch {
		case strings.HasPrefix(unit, "second"):
			unit = "秒"
		case strings.HasPrefix(unit, "minute"):
			unit = "分钟"
		case strings.HasPrefix(unit, "hour"):
			unit = "小时"
		}
		add(match[1], unit)
	}
	if len(values) == 1 {
		for duration, value := range values {
			result.validity, result.duration = value, duration
		}
	}
	return result
}
