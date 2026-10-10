package notifications

import "time"

const wechatRemovalGrace = 2 * time.Second
const maxRemovalCards = 128

type toastSlot struct {
	group, tag string
}

type removalCard struct {
	order uint64
	until time.Time
}

// Owned by the native sink's event loop. Store only opaque slots, never message
// contents or actions. WeChat can withdraw a notification just before posting
// its replacement; hold the visual removal briefly, without retaining actions.
type removalCoalescer struct {
	cards map[toastSlot]removalCard
	order uint64
}

// Call only after a successful native Show. A failed replacement must leave
// the original removal scheduled. Other packages retain immediate removal.
func (s *removalCoalescer) Shown(group, tag, pkg string) bool {
	key := toastSlot{group, tag}
	previous := s.cards[key]
	delete(s.cards, key)
	if pkg != "com.tencent.mm" || group == "" || tag == "" {
		return false
	}
	if s.cards == nil {
		s.cards = make(map[toastSlot]removalCard)
	}
	if len(s.cards) >= maxRemovalCards {
		var oldest toastSlot
		var order uint64
		for slot, card := range s.cards {
			if order == 0 || card.order < order {
				oldest, order = slot, card.order
			}
		}
		delete(s.cards, oldest)
	}
	s.order++
	s.cards[key] = removalCard{order: s.order}
	return !previous.until.IsZero()
}

func (s *removalCoalescer) Queue(group, tag string, now time.Time) bool {
	key := toastSlot{group, tag}
	card, exists := s.cards[key]
	if !exists {
		return false
	}
	// Repeated withdrawals cannot extend an absent notification indefinitely.
	if card.until.IsZero() {
		card.until = now.Add(wechatRemovalGrace)
		s.cards[key] = card
	}
	return true
}

func (s *removalCoalescer) Next() (time.Time, bool) {
	var next time.Time
	for _, card := range s.cards {
		if !card.until.IsZero() && (next.IsZero() || card.until.Before(next)) {
			next = card.until
		}
	}
	return next, !next.IsZero()
}

func (s *removalCoalescer) Due(now time.Time) []toastSlot {
	var due []toastSlot
	for slot, card := range s.cards {
		if !card.until.IsZero() && !now.Before(card.until) {
			due = append(due, slot)
			delete(s.cards, slot)
		}
	}
	return due
}

func (s *removalCoalescer) Remove(group, tag string) {
	delete(s.cards, toastSlot{group, tag})
}

func (s *removalCoalescer) Clear(group string) {
	for slot := range s.cards {
		if slot.group == group {
			delete(s.cards, slot)
		}
	}
}
