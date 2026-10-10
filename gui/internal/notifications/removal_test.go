package notifications

import (
	"strconv"
	"testing"
	"time"
)

func TestRemovalRepostCancelsOnlyItsOwnWithdrawal(t *testing.T) {
	now := time.Unix(100, 0)
	var removals removalCoalescer
	removals.Shown("phone", "chat", "com.tencent.mm")
	removals.Shown("other-phone", "chat", "com.tencent.mm")
	if !removals.Queue("phone", "chat", now) || !removals.Queue("other-phone", "chat", now) {
		t.Fatal("WeChat withdrawal must be coalesced")
	}
	// New messages are shown immediately; only an already pending removal is
	// canceled. Another phone or another notification is not a replacement.
	if removals.Shown("phone", "different-chat", "com.tencent.mm") {
		t.Fatal("unrelated message canceled a withdrawal")
	}
	if !removals.Shown("phone", "chat", "com.tencent.mm") {
		t.Fatal("same notification did not cancel pending removal")
	}
	due := removals.Due(now.Add(wechatRemovalGrace))
	if len(due) != 1 || due[0] != (toastSlot{"other-phone", "chat"}) {
		t.Fatal("replacement must survive the old deadline without affecting another phone")
	}
	if _, pending := removals.Next(); pending {
		t.Fatal("canceled timer remained armed")
	}
}

func TestRemovalDeadlineIsBoundedAndOtherApplicationsStayImmediate(t *testing.T) {
	now := time.Unix(100, 0)
	var removals removalCoalescer
	removals.Shown("phone", "wechat", "com.tencent.mm")
	removals.Shown("phone", "mail", "com.example.mail")
	if removals.Queue("phone", "mail", now) || removals.Queue("phone", "unknown", now) {
		t.Fatal("non-WeChat removal must remain immediate")
	}
	removals.Queue("phone", "wechat", now)
	removals.Queue("phone", "wechat", now.Add(wechatRemovalGrace-time.Nanosecond))
	next, ok := removals.Next()
	if !ok || !next.Equal(now.Add(wechatRemovalGrace)) {
		t.Fatal("repeated removal extended the deadline")
	}
	if len(removals.Due(next.Add(-time.Nanosecond))) != 0 {
		t.Fatal("removed before the grace period elapsed")
	}
	if len(removals.Due(next)) != 1 || len(removals.cards) != 0 {
		t.Fatal("real withdrawal did not expire at the deadline")
	}
}

func TestRemovalClearAndConsumedActionsCannotRemoveNewCards(t *testing.T) {
	now := time.Unix(100, 0)
	var removals removalCoalescer
	for _, group := range []string{"phone", "other-phone"} {
		removals.Shown(group, "chat", "com.tencent.mm")
		removals.Queue(group, "chat", now)
	}
	removals.Clear("phone")
	removals.Shown("phone", "chat", "com.tencent.mm")
	due := removals.Due(now.Add(wechatRemovalGrace))
	if len(due) != 1 || due[0].group != "other-phone" {
		t.Fatal("clear leaked a stale timer into a new card")
	}
	removals.Queue("phone", "chat", now)
	removals.Remove("phone", "chat")
	if _, ok := removals.Next(); ok {
		t.Fatal("consumed action left a pending visual removal")
	}
}

func TestRemovalMetadataRemainsBoundedAndReplacementRefreshesOrder(t *testing.T) {
	now := time.Unix(100, 0)
	var removals removalCoalescer
	for i := 0; i < maxRemovalCards; i++ {
		removals.Shown("phone", strconv.Itoa(i), "com.tencent.mm")
	}
	removals.Shown("phone", "0", "com.tencent.mm")
	removals.Shown("phone", "new", "com.tencent.mm")
	if len(removals.cards) != maxRemovalCards || removals.Queue("phone", "1", now) || !removals.Queue("phone", "0", now) {
		t.Fatal("bounded cache did not evict the oldest unreplaced slot")
	}
	removals.Shown("phone", "0", "com.example.mail")
	if removals.Queue("phone", "0", now) {
		t.Fatal("slot reassigned to another package retained WeChat delay")
	}
}
