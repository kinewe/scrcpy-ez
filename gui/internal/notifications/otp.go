package notifications

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var otpCue = regexp.MustCompile(`(?i)(?:(?:Google|谷歌)\s*)?(?:(?:短信|邮箱|手机)?(?:登录|注册|支付)?验证码|校验码|验证代码|动态密码|动态码|一次性密码|一次性口令|一次性代码|安全码|认证码|登录码|\b(?:verification\s+code|security\s+code|one[- ]time\s+(?:password|passcode|code)|login\s+code|authentication\s+code|otp|passcode)\b)`)
var otpToken = regexp.MustCompile(`[A-Za-z]-[0-9]{4,8}|[0-9]{3}[ -][0-9]{3}|[A-Za-z0-9]{4,10}`)
var otpAfterCue = regexp.MustCompile(`(?i)^[\s:：=()（）【】"'“”‘’—-]*(?:(?:为|是|is)[\s:：=()（）【】"'“”‘’—-]*)?$`)
var otpBeforeCue = regexp.MustCompile(`(?i)^[\s:：=()（）【】"'“”‘’—-]*(?:(?:是您的|是你的|为您的|为你的|是|为|is\s+your|is\s+the|is)[\s:：=()（）【】"'“”‘’—-]*)?$`)
var otpExcluded = regexp.MustCompile(`(?i)https?://[^\s，。；]+|[\w.+-]+@[\w.-]+|[0-9]{4}[-/][0-9]{1,2}[-/][0-9]{1,2}|[0-9]{1,2}:[0-9]{2}`)
var otpNegative = regexp.MustCompile(`(?i)(?:订单(?:号|编号)?|运单号|单号|手机(?:号|号码)|客服(?:电话)?|电话(?:号码)?|尾号|客户(?:编号|号|ID)|设备(?:编号|号|ID)|金额|余额|价格|日期|年份|order\s+(?:number|id)|invoice\s+(?:number|id)|customer\s+(?:number|id)|device\s+(?:number|id)|phone|tel|amount|price|balance|date|year)[\s:：=为是]*$`)
var otpUnits = regexp.MustCompile(`(?i)^\s*(?:元|美元|人民币|年|月|日|USD\b|CNY\b|RMB\b)`)
var otpListGap = regexp.MustCompile(`(?i)^(?:\s*(?:/|,|，|;|；|或|或者|和|、|or|and)\s*|\s+)$`)

func validVerificationCode(record Record) bool {
	if record.Package != "com.android.mms" || len(record.VerificationCode) < 4 || len(record.VerificationCode) > 8 {
		return false
	}
	for _, digit := range record.VerificationCode {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func recordOTP(record Record) string {
	textCode := ExtractOTP(record.Title, record.Body)
	if !validVerificationCode(record) {
		return textCode
	}
	if textCode != "" && textCode != record.VerificationCode {
		return "" // Conflicting content must not offer a potentially stale copy action.
	}
	return record.VerificationCode
}

func asciiWord(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_'
}

func normalizeOTP(text string) string {
	if len(text) > 32768 {
		text = text[:32768]
	}
	text = norm.NFKC.String(text)
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		for _, span := range unicode.Digit.R16 {
			if r >= rune(span.Lo) && r <= rune(span.Hi) && (uint32(r)-uint32(span.Lo))%uint32(span.Stride) == 0 {
				return '0' + (r-rune(span.Lo))/rune(span.Stride)%10
			}
		}
		for _, span := range unicode.Digit.R32 {
			if r >= rune(span.Lo) && r <= rune(span.Hi) && (uint32(r)-span.Lo)%span.Stride == 0 {
				return '0' + (r-rune(span.Lo))/rune(span.Stride)%10
			}
		}
		return r
	}, text)
}

// A button is offered only for one unambiguous code directly attached to an OTP
// phrase. Numeric context/proximity alone is insufficient. No network or ML call.
func ExtractOTP(title, body string) string {
	text := normalizeOTP(title + "\n" + body)
	cues := otpCue.FindAllStringIndex(text, 64)
	if len(cues) == 0 {
		return ""
	}
	excluded := otpExcluded.FindAllStringIndex(text, 64)
	type candidate struct {
		span  []int
		code  string
		bound bool
	}
	var tokens []candidate
	for _, span := range otpToken.FindAllStringIndex(text, 128) {
		if span[0] > 0 && asciiWord(text[span[0]-1]) || span[1] < len(text) && asciiWord(text[span[1]]) {
			continue
		}
		code := text[span[0]:span[1]]
		if !(len(code) >= 6 && code[1] == '-' && (code[0] >= 'A' && code[0] <= 'Z' || code[0] >= 'a' && code[0] <= 'z')) {
			code = strings.ReplaceAll(strings.ReplaceAll(code, " ", ""), "-", "")
		}
		digits := 0
		for _, r := range code {
			if r >= '0' && r <= '9' {
				digits++
			}
		}
		if digits == 0 || len(code) > 10 || digits == len(code) && (len(code) < 4 || len(code) > 8) {
			continue
		}
		bad := false
		for _, s := range excluded {
			if span[0] < s[1] && span[1] > s[0] {
				bad = true
				break
			}
		}
		if bad {
			continue
		}
		if len(code) == 8 && digits == 8 {
			if date, err := time.Parse("20060102", code); err == nil && date.Year() >= 1900 && date.Year() <= 2099 {
				continue
			}
		}
		prefix := text[max(0, span[0]-96):span[0]]
		if otpNegative.MatchString(prefix) || otpUnits.MatchString(text[span[1]:]) || strings.HasSuffix(strings.TrimSpace(prefix), "￥") || strings.HasSuffix(strings.TrimSpace(prefix), "$") {
			continue
		}
		bound := false
		for _, cue := range cues {
			if cue[1] <= span[0] && span[0]-cue[1] <= 40 && otpAfterCue.MatchString(text[cue[1]:span[0]]) || span[1] <= cue[0] && cue[0]-span[1] <= 40 && otpBeforeCue.MatchString(text[span[1]:cue[0]]) {
				bound = true
				break
			}
		}
		tokens = append(tokens, candidate{span: span, code: code, bound: bound})
	}
	for i := 1; i < len(tokens); i++ {
		if (tokens[i-1].bound || tokens[i].bound) && otpListGap.MatchString(text[tokens[i-1].span[1]:tokens[i].span[0]]) {
			tokens[i-1].bound = true
			tokens[i].bound = true
		}
	}
	code := ""
	for _, token := range tokens {
		if !token.bound {
			continue
		}
		if code != "" && code != token.code {
			return ""
		}
		code = token.code
	}
	return strings.Clone(code)
}
