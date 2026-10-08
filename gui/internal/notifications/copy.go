package notifications

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

const copyLifetime = 24 * time.Hour

type copyEntry struct {
	group, tag, code string
	until            time.Time
	order            uint64
}
type copyStore struct {
	entries map[string]copyEntry
	order   uint64
}

func (s *copyStore) Remove(group, tag string) {
	for token, entry := range s.entries {
		if entry.group == group && (tag == "" || entry.tag == tag) {
			delete(s.entries, token)
		}
	}
}
func (s *copyStore) Sweep(now time.Time) {
	for token, entry := range s.entries {
		if !now.Before(entry.until) {
			delete(s.entries, token)
		}
	}
}
func (s *copyStore) Issue(card Card, now time.Time) string {
	start := card.CopyIssuedAt
	if start.IsZero() || start.After(now) {
		start = now
	}
	until := start.Add(copyLifetime)
	if card.CopyFallback > 0 {
		until = start.Add(card.CopyFallback)
	}
	if validity := describeOTP(card).duration; validity > 0 {
		until = start.Add(validity)
	}
	// Metadata refreshes must not renew a code that is already on this card.
	for _, entry := range s.entries {
		if entry.group == card.Group && entry.tag == card.Tag && entry.code == card.CopyCode {
			until = entry.until
			break
		}
	}
	s.Remove(card.Group, card.Tag)
	s.Sweep(now)
	if card.CopyCode == "" || len(card.CopyCode) > 10 || !now.Before(until) {
		return ""
	}
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return ""
	}
	token := hex.EncodeToString(entropy[:])
	if s.entries == nil {
		s.entries = make(map[string]copyEntry)
	}
	if len(s.entries) >= 128 {
		var oldest string
		var order uint64
		for key, entry := range s.entries {
			if oldest == "" || entry.order < order {
				oldest, order = key, entry.order
			}
		}
		delete(s.entries, oldest)
	}
	s.order++
	s.entries[token] = copyEntry{group: card.Group, tag: card.Tag, code: card.CopyCode, until: until, order: s.order}
	return token
}
func (s *copyStore) Copy(token string, now time.Time, write func(string) error) bool {
	s.Sweep(now)
	entry, ok := s.entries[token]
	if !ok {
		return false
	}
	// One success per token. A repeated OS activation cannot overwrite a later copy.
	if write(entry.code) != nil {
		return false
	}
	delete(s.entries, token)
	return true
}
