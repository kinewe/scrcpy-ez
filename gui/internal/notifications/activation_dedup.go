package notifications

import "time"

// Shell may report one click through both COM and the live ToastNotification.
// Remember only already consumed desktop tokens; this cannot authorize a click
// or keep an Android capability alive. Owned by the single sink event loop.
type activationDeduplicator struct {
	seen map[string]time.Time
}

func (d *activationDeduplicator) Contains(token string, now time.Time) bool {
	until, ok := d.seen[token]
	return ok && now.Before(until)
}

func (d *activationDeduplicator) Record(token string, now time.Time) {
	if d.seen == nil {
		d.seen = make(map[string]time.Time)
	}
	for key, until := range d.seen {
		if !now.Before(until) {
			delete(d.seen, key)
		}
	}
	if len(d.seen) >= 512 {
		var oldest string
		var earliest time.Time
		for key, until := range d.seen {
			if oldest == "" || until.Before(earliest) {
				oldest, earliest = key, until
			}
		}
		delete(d.seen, oldest)
	}
	d.seen[token] = now.Add(30 * time.Second)
}
