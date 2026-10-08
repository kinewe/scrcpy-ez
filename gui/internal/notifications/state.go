package notifications

import (
	"fmt"
	"slices"
	"sort"
	"time"
)

const maxRecords = 512

type cachedRecord struct {
	value       Record
	fingerprint string
	bodyHash    string
	messageTime int64
	visible     bool
}

type State struct {
	group        string
	device       string
	connection   string
	icons        map[string]AppIcon
	iconOrder    []string
	artwork      func(string) Artwork
	fallback     map[string]cachedArtwork
	preview      bool
	policy       Policy
	cutoff       int64
	started      time.Time
	copyFallback time.Duration
	copyStarts   map[string]copyStart
	sequence     uint64
	hello        bool
	ready        bool
	records      map[string]cachedRecord
}

type copyStart struct {
	hash   string
	issued time.Time
}

type cachedArtwork struct {
	value Artwork
	until time.Time
}

func NewState(identity, device string, preview bool) *State {
	return &State{group: ShortID(identity), device: device, preview: preview, records: make(map[string]cachedRecord), icons: make(map[string]AppIcon)}
}

func fingerprint(r Record) string {
	return ShortID(fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s\x00%s\x00%s\x00%s", r.App, r.Title, r.Body, r.MessageTime, r.IconID, r.Package, r.DisplayPackage, r.VerificationCode))
}

func bodyFingerprint(r Record) string {
	return ShortID(r.Body + "\x00" + r.VerificationCode)
}

func (s *State) show(r Record, silent bool, sink Sink) error {
	card := Card{Package: r.Package, Group: s.group, Tag: ShortID(r.Key), Device: s.device, Connection: s.connection, App: r.App, Title: r.Title, Body: r.Body, Silent: silent, Icon: s.icons[r.IconID]}
	if s.artwork != nil && (card.Icon.ID == "" || card.App == "" || card.App == r.Package) {
		cached, exists := s.fallback[r.Package]
		if !exists || time.Now().After(cached.until) {
			if s.fallback == nil || len(s.fallback) >= 128 {
				s.fallback = make(map[string]cachedArtwork)
			}
			cached = cachedArtwork{value: s.artwork(r.Package), until: time.Now().Add(30 * time.Second)}
			s.fallback[r.Package] = cached
		}
		if card.Icon.ID == "" {
			card.Icon = cached.value.Icon
		}
		if cached.value.App != "" && (card.App == "" || card.App == r.Package) {
			card.App = cached.value.App
		}
	}
	if !s.preview {
		card.Title = "收到新消息"
		card.Body = "打开手机查看消息内容"
	} else {
		card.CopyCode = recordOTP(r)
		card.CopyFallback = s.copyFallback
		// Map the phone's post timestamp to the PC using this session's clock
		// sample. This avoids requiring the two devices to have identical clocks.
		// Clamp implausible timestamps and future samples to receipt time.
		card.CopyIssuedAt = time.Now()
		if delta := r.PostTime - s.cutoff; !s.started.IsZero() && delta > -7*24*60*60*1000 && delta < 7*24*60*60*1000 {
			mapped := s.started.Add(time.Duration(delta) * time.Millisecond)
			if mapped.Before(card.CopyIssuedAt) {
				card.CopyIssuedAt = mapped
			}
		}
		if card.CopyCode != "" {
			hash := ShortID(card.CopyCode)
			if old, exists := s.copyStarts[r.Key]; exists && old.hash == hash {
				card.CopyIssuedAt = old.issued
			}
			if s.copyStarts == nil {
				s.copyStarts = make(map[string]copyStart)
			}
			s.copyStarts[r.Key] = copyStart{hash: hash, issued: card.CopyIssuedAt}
		} else {
			delete(s.copyStarts, r.Key)
		}
	}
	return sink.Show(card)
}

func (s *State) Apply(frame Frame, sink Sink) error {
	if frame.Sequence != s.sequence+1 {
		return ErrProtocol
	}
	s.sequence = frame.Sequence
	if frame.Type == "hello" {
		if s.hello {
			return ErrProtocol
		}
		s.hello = true
		s.cutoff = frame.Cutoff
		s.started = time.Now()
		return nil
	}
	if !s.hello {
		return ErrProtocol
	}
	switch frame.Type {
	case "icon":
		if frame.Icon.ID == "" {
			return nil
		}
		if _, exists := s.icons[frame.Icon.ID]; !exists {
			if len(s.iconOrder) == 128 {
				delete(s.icons, s.iconOrder[0])
				s.iconOrder = s.iconOrder[1:]
			}
			s.iconOrder = append(s.iconOrder, frame.Icon.ID)
		}
		s.icons[frame.Icon.ID] = frame.Icon
		return nil
	case "snapshot":
		if s.ready {
			return ErrProtocol
		}
		if len(s.records) < maxRecords {
			s.records[frame.Record.Key] = cachedRecord{value: frame.Record, fingerprint: fingerprint(frame.Record)}
		}
	case "ready":
		if s.ready {
			return ErrProtocol
		}
		s.ready = true
		keys := make([]string, 0, len(s.records))
		for key := range s.records {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			record := s.records[key]
			if record.value.PostTime > s.cutoff && s.policy.allows(record.value) {
				if err := s.show(record.value, false, sink); err != nil {
					return err
				}
				record.visible = true
			}
			record.bodyHash = bodyFingerprint(record.value)
			record.messageTime = record.value.MessageTime
			record.value = Record{PostTime: record.value.PostTime}
			s.records[key] = record
		}
	case "post":
		if !s.ready {
			return ErrProtocol
		}
		r := frame.Record
		old, exists := s.records[r.Key]
		hash := fingerprint(r)
		if exists && old.value.PostTime > r.PostTime {
			return nil
		}
		if exists && old.fingerprint == hash {
			old.value.PostTime = r.PostTime
			s.records[r.Key] = old
			return nil
		}
		if !exists && len(s.records) >= maxRecords {
			var oldest string
			var oldestTime int64
			for key, value := range s.records {
				if oldest == "" || value.value.PostTime < oldestTime || (value.value.PostTime == oldestTime && key < oldest) {
					oldest = key
					oldestTime = value.value.PostTime
				}
			}
			if err := sink.Remove(s.group, ShortID(oldest)); err != nil {
				return err
			}
			delete(s.records, oldest)
			delete(s.copyStarts, oldest)
		}
		// Chat apps often reuse a key: a new message may alert, metadata updates stay quiet.
		newMessage := r.MessageTime > 0 && r.MessageTime > old.messageTime
		silent := exists && old.visible && (r.OnlyAlertOnce || old.bodyHash == bodyFingerprint(r) && !newMessage)
		allowed := s.policy.allows(r)
		if allowed {
			if err := s.show(r, silent, sink); err != nil {
				return err
			}
		} else if old.visible {
			if err := sink.Remove(s.group, ShortID(r.Key)); err != nil {
				return err
			}
		}
		s.records[r.Key] = cachedRecord{value: Record{PostTime: r.PostTime}, fingerprint: hash, bodyHash: bodyFingerprint(r), messageTime: r.MessageTime, visible: allowed}
	case "remove":
		if !s.ready {
			return ErrProtocol
		}
		if old, exists := s.records[frame.Record.Key]; exists {
			if old.visible {
				if err := sink.Remove(s.group, ShortID(frame.Record.Key)); err != nil {
					return err
				}
			}
			delete(s.records, frame.Record.Key)
			delete(s.copyStarts, frame.Record.Key)
		}
	default:
		return ErrProtocol
	}
	return nil
}

// Changing privacy settings clears old previews without reconnecting the phone or replaying history.
func (s *State) SetPreview(preview bool, sink Sink) error {
	return s.SetPolicy(s.policy, preview, sink)
}

// Policy changes clear visible cards and copy actions, without replaying cached history.
func (s *State) SetPolicy(policy Policy, preview bool, sink Sink) error {
	if s.preview == preview && s.policy.Mode == policy.Mode && s.policy.Other == policy.Other && (policy.Mode != ModeWhitelist || slices.Equal(s.policy.Catalog, policy.Catalog)) && slices.Equal(s.policy.Packages, policy.Packages) {
		return nil
	}
	s.policy = policy.Clone()
	s.preview = preview
	if err := sink.Clear(s.group); err != nil {
		return err
	}
	for key, record := range s.records {
		record.visible = false
		s.records[key] = record
	}
	return nil
}

func (s *State) Clear(sink Sink) error {
	s.records = make(map[string]cachedRecord)
	s.copyStarts = nil
	return sink.Clear(s.group)
}
