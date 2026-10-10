package notifications

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

type openEntry struct {
	group, tag string
	until      time.Time
	order      uint64
	open       func(context.Context) error
}
type openStore struct {
	entries map[string]openEntry
	order   uint64
}

func (s *openStore) Remove(group, tag string) {
	for token, entry := range s.entries {
		if entry.group == group && (tag == "" || entry.tag == tag) {
			delete(s.entries, token)
		}
	}
}
func (s *openStore) Sweep(now time.Time) {
	for token, entry := range s.entries {
		if !now.Before(entry.until) {
			delete(s.entries, token)
		}
	}
}
func (s *openStore) Issue(card Card, now time.Time) string {
	s.Remove(card.Group, card.Tag)
	s.Sweep(now)
	if card.Open == nil || card.CopyCode != "" {
		return ""
	}
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return ""
	}
	if s.entries == nil {
		s.entries = make(map[string]openEntry)
	}
	if len(s.entries) >= 512 {
		var oldest string
		var order uint64
		for token, e := range s.entries {
			if oldest == "" || e.order < order {
				oldest = token
				order = e.order
			}
		}
		delete(s.entries, oldest)
	}
	s.order++
	token := hex.EncodeToString(entropy[:])
	s.entries[token] = openEntry{group: card.Group, tag: card.Tag, until: now.Add(24 * time.Hour), order: s.order, open: card.Open}
	return token
}
func (s *openStore) Take(token string, now time.Time) (openEntry, bool) {
	e, ok := s.entries[token]
	delete(s.entries, token)
	return e, ok && now.Before(e.until)
}
