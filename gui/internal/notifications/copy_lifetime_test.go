package notifications

import (
	"testing"
	"time"
)

func TestCopyUsesStatedValidityOrLongConfigurableFallback(t *testing.T) {
	now := time.Unix(10000, 0)
	cases := []struct {
		body                    string
		fallback, age, duration time.Duration
	}{
		{"验证码850329，5分钟内有效。", 7 * 24 * time.Hour, 2 * time.Minute, 3 * time.Minute},
		{"验证码850329，请在30秒内输入。", 0, 0, 30 * time.Second},
		{"验证码850329，有效期1小时。", time.Minute, 0, time.Hour},
		{"验证码850329。", 0, 0, 24 * time.Hour},
		{"验证码850329。", 7 * 24 * time.Hour, 0, 7 * 24 * time.Hour},
	}
	for _, c := range cases {
		var store copyStore
		card := Card{Group: "test", Tag: "test", CopyCode: "850329", Body: c.body, CopyFallback: c.fallback, CopyIssuedAt: now.Add(-c.age)}
		token := store.Issue(card, now)
		entry, ok := store.entries[token]
		if !ok || !entry.until.Equal(now.Add(c.duration)) {
			t.Fatalf("wrong expiration for %q", c.body)
		}
		if store.Copy(token, entry.until, func(string) error { return nil }) {
			t.Fatal("deadline accepted")
		}
	}
}

func TestCopyExpiredRefreshDoesNotExtendOriginalDeadline(t *testing.T) {
	now := time.Unix(10000, 0)
	var store copyStore
	card := Card{Group: "test", Tag: "test", CopyCode: "850329", Body: "验证码850329，1分钟内有效。", CopyIssuedAt: now}
	first := store.Issue(card, now)
	card.CopyIssuedAt = now.Add(30 * time.Second)
	second := store.Issue(card, now.Add(30*time.Second))
	if store.Copy(first, now, func(string) error { return nil }) || store.entries[second].until != now.Add(time.Minute) {
		t.Fatal("refresh renewed the same code")
	}
	if store.Issue(card, now.Add(time.Minute)) != "" {
		t.Fatal("expired existing card re-enabled copy")
	}
	card.CopyIssuedAt = now
	store.Sweep(now.Add(2 * time.Minute))
	if store.Issue(card, now.Add(2*time.Minute)) != "" {
		t.Fatal("expired message regained action after sweep")
	}
	card.CopyCode = "039571"
	card.Body = "验证码039571，1分钟内有效。"
	card.CopyIssuedAt = now.Add(2 * time.Minute)
	if store.Issue(card, now.Add(2*time.Minute)) == "" {
		t.Fatal("new code remained disabled")
	}
}

func TestStateRefreshKeepsCodeStartEvenAfterCopyCacheExpires(t *testing.T) {
	s := NewState("test", "平板", true)
	sink := &testSink{}
	frames := []Frame{{Sequence: 1, Type: "hello", Cutoff: 1000}, {Sequence: 2, Type: "ready"}, {Sequence: 3, Type: "post", Record: Record{Key: "key", PostTime: 1010, Body: "验证码850329，1分钟内有效。"}}, {Sequence: 4, Type: "post", Record: Record{Key: "key", PostTime: 121000, Title: "更新", Body: "验证码850329，1分钟内有效。"}}}
	for _, f := range frames {
		if err := s.Apply(f, sink); err != nil {
			t.Fatal(err)
		}
	}
	if len(sink.shown) != 2 || !sink.shown[0].CopyIssuedAt.Equal(sink.shown[1].CopyIssuedAt) {
		t.Fatal("metadata refresh moved the code start")
	}
	var store copyStore
	if store.Issue(sink.shown[1], sink.shown[0].CopyIssuedAt.Add(2*time.Minute)) != "" {
		t.Fatal("expired state refresh offered action")
	}
}
