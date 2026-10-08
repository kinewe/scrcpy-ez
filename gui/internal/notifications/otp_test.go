package notifications

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestOTPIndependentSyntheticCorpus(t *testing.T) {
	// Evaluation messages are synthetic and include the research counterexamples.
	cases := []struct{ text, want string }{
		{"【示例服务】验证码为530719，请在5分钟内使用。", "530719"},
		{"校验码：0048。", "0048"},
		{"登录码是87492016。", "87492016"},
		{"Your verification code is 628540. Expires in 10 minutes.", "628540"},
		{"OTP: ٧٣٩٥١٢", "739512"}, {"验证码：８５０３２９", "850329"},
		{"Your one-time password: G5h8J1", "G5h8J1"},
		{"动态密码为d7R2p9。", "d7R2p9"},
		{"Security code: 817-503", "817503"}, {"验证码: 817 503", "817503"},
		{"850329 是您的验证码。", "850329"},
		{"850329 是您的登录验证码。", "850329"},
		{"验证码\n850329\n请勿告诉他人。", "850329"},
		{"订单编号590481。您的验证码：850329", "850329"},
		{"验证码为G5h8J1，设备编号X5Y6Z7。", "G5h8J1"},
		{"验证码为850329，重复提示验证码为850329。", "850329"},
		{"Your authentication code is A123456789", "A123456789"},
		{"验证码邮件将在20261004发送。", ""},
		{"验证码稍后送达，客户编号为850329。", ""},
		{"验证码已发送。设备编号X5Y6Z7。", ""},
		{"验证码：850329，验证码：004857。", ""},
		{"验证码 850329 / 004857", ""}, {"OTP: 850329 or 004857", ""},
		{"OTP: 850329, 004857", ""}, {"验证码850329\n004857", ""},
		{"旧验证码850329，新验证码004857。", ""},
		{"验证码请访问 https://example.invalid/code/850329", ""},
		{"验证码请联系 850329@example.invalid", ""},
		{"验证码将在2026-10-04发送。", ""},
		{"验证码将在2026年发送。", ""},
		{"验证码已发送，卡尾号8503。", ""},
		{"验证码已发，余额850329。", ""},
		{"验证码已发。客服电话123456。", ""},
		{"金额为850329元，验证码稍后发送。", ""},
		{"验证码为850329元。", ""},
		{"Your verification code is unavailable. USD 850329", ""},
		{"Your verification code is expired", ""},
		{"验证码: AbCdEf", ""},
		{"验证码: 13800000000", ""}, {"验证码: 123456789012", ""},
		{"验证码已脱敏：******", ""},
		{"验证码：85", ""}, {"今天见面，门口写着850329。", ""},
		{"验证码 20261004", ""}, {"", ""},
		{"验证码发送至手机13800000000，登录码为039571。", "039571"},
	}
	for i, c := range cases {
		if got := ExtractOTP("", c.text); got != c.want {
			t.Errorf("synthetic case %d: got %q want %q", i, got, c.want)
		}
	}
	if got := ExtractOTP("Verification code", "850329\nSynthetic email body"); got != "850329" {
		t.Fatal("email subject/body boundary failed")
	}
}

func TestCopyTokensExpireReplaceAndCannotRepeat(t *testing.T) {
	now := time.Unix(100, 0)
	var store copyStore
	card := Card{Group: "device", Tag: "notification", CopyCode: "850329"}
	first := store.Issue(card, now)
	if first == "" || strings.Contains(first, card.CopyCode) {
		t.Fatal("missing opaque token")
	}
	writes := 0
	writer := func(code string) error {
		writes++
		if code != card.CopyCode {
			t.Fatal("wrong code")
		}
		return nil
	}
	if !store.Copy(first, now, writer) || store.Copy(first, now, writer) || writes != 1 {
		t.Fatal("repeated activation wrote again")
	}
	stale := store.Issue(card, now)
	latest := store.Issue(card, now.Add(time.Second))
	if store.Copy(stale, now, writer) || !store.Copy(latest, now, writer) {
		t.Fatal("notification refresh did not revoke old token")
	}
	expired := store.Issue(card, now)
	if store.Copy(expired, now.Add(copyLifetime), writer) {
		t.Fatal("expired copy accepted")
	}
	removed := store.Issue(card, now)
	store.Remove(card.Group, "")
	if store.Copy(removed, now, writer) {
		t.Fatal("disabled device retained copy action")
	}
	busy := store.Issue(card, now)
	if store.Copy(busy, now, func(string) error { return errors.New("clipboard busy") }) || !store.Copy(busy, now, writer) {
		t.Fatal("clipboard failure/retry did not preserve intentional copy")
	}
}

func TestHiddenPreviewDoesNotOfferCopyAction(t *testing.T) {
	s := NewState("a", "device", false)
	sink := &testSink{}
	for _, f := range []Frame{{Sequence: 1, Type: "hello", Cutoff: 1}, {Sequence: 2, Type: "ready"}, {Sequence: 3, Type: "post", Record: Record{Key: "a", PostTime: 2, Body: "验证码850329"}}} {
		if err := s.Apply(f, sink); err != nil {
			t.Fatal(err)
		}
	}
	if sink.shown[0].CopyCode != "" {
		t.Fatal("hidden notification offered code copy")
	}
	card := Card{Token: strings.Repeat("a", 32), CopyCode: "850329", Device: "device"}
	xml := ToastXML(card)
	if !strings.Contains(xml, `arguments="copy:`) || !strings.Contains(xml, `launch="copy:`) || strings.Contains(xml, `copy:`+card.CopyCode) {
		t.Fatal("code leaked into activation payload")
	}
}

func TestOTPBodyClickSharesButtonActionAndOrdinaryCardsStayPassive(t *testing.T) {
	card := Card{Token: strings.Repeat("a", 32), CopyCode: "530719", Device: "平板", Body: "合成消息"}
	xml := ToastXML(card)
	if !strings.Contains(xml, `launch="copy:`+card.Token+`"`) || !strings.Contains(xml, `arguments="copy:`+card.Token+`"`) {
		t.Fatal("body and button must share one revocable opaque action")
	}
	if strings.Contains(xml, `copy:`+card.CopyCode) {
		t.Fatal("verification code leaked into activation arguments")
	}
	card.CopyCode = ""
	xml = ToastXML(card)
	if strings.Contains(xml, "copy:") || strings.Contains(xml, "<actions>") || strings.Contains(xml, "launch=") {
		t.Fatal("ordinary notification must not copy on body clicks")
	}
	card.CopyCode, card.Token = "530719", strings.Repeat("g", 32)
	if strings.Contains(ToastXML(card), "copy:") {
		t.Fatal("invalid action token accepted")
	}
}
