package adb

import (
	"context"
	"strings"
	"time"

	"scrcpy-ez/gui/internal/deviceevents"
	"scrcpy-ez/gui/internal/parallel"
)

type connectionTokensKey struct{}

type connectionToken struct {
	epoch, generation uint64
	id                string
}

func tokenFor(s deviceevents.Snapshot, t deviceevents.Transport) connectionToken {
	return connectionToken{s.Epoch, t.Generation, t.ID}
}

func snapshotContext(ctx context.Context, s deviceevents.Snapshot) context.Context {
	tokens := make(map[string]connectionToken, len(s.Transports))
	for _, t := range s.Transports {
		tokens[t.Serial] = tokenFor(s, t)
	}
	return context.WithValue(ctx, connectionTokensKey{}, tokens)
}

func (m *Manager) cacheFor(ctx context.Context, d *Device) *cachedSpec {
	identity := IdentityKey("", "", "", d.Serial)
	if m.identityResolver != nil {
		if confirmed, _ := m.identityResolver(d.Serial); confirmed != "" {
			identity = confirmed
		} else if strings.HasPrefix(identity, "pending:") {
			identity = ""
		}
	}
	tokens, _ := ctx.Value(connectionTokensKey{}).(map[string]connectionToken)
	token := tokens[d.Serial]
	empty := connectionToken{}
	if ctx.Err() != nil {
		return &cachedSpec{}
	}
	if token != empty {
		current := m.EventHub().Current()
		if current.Epoch != 0 {
			matches := false
			for _, t := range current.Transports {
				matches = matches || current.Available && t.Serial == d.Serial && token == tokenFor(current, t)
			}
			if !matches {
				return &cachedSpec{}
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cache == nil {
		m.cache = map[string]*cachedSpec{}
	}
	c := m.cache[d.Serial]
	if c != nil && token != empty && (c.connection.epoch > token.epoch || c.connection.epoch == token.epoch && c.connection.generation > token.generation) {
		return &cachedSpec{}
	}
	if c == nil || identity != "" && c.identity != identity || token != empty && (c.unscoped || c.connection != empty && c.connection != token) {
		c = &cachedSpec{identity: identity, connection: token, unscoped: token == empty}
		m.cache[d.Serial] = c
		d.Name = ""
	} else if token != empty {
		c.connection = token
	}
	return c
}

// Cache peeks never wait behind a slow command for the same device.
func (m *Manager) peekDevice(ctx context.Context, d *Device) {
	if d.State != "device" {
		return
	}
	c := m.cacheFor(ctx, d)
	if !c.mu.TryLock() {
		return
	}
	defer c.mu.Unlock()
	if c.name != "" {
		d.Name = c.name
	}
	d.Model, d.Marketname, d.Manufacturer = c.model, c.marketname, c.manufacturer
	d.Battery, d.Res, d.FPS = c.battery, SortWideFirst(c.res), c.fps
	d.WeChatScan = c.weChatScan
	d.WirelessRes = ScaleForWireless(c.res)
}

func (m *Manager) annotateDevice(d *Device) {
	d.StableSerial = StableSerial(d.Serial)
	if m.identityResolver != nil {
		if key, stable := m.identityResolver(d.Serial); key != "" {
			d.Identity, d.StableSerial = key, stable
		}
	}
}

func (m *Manager) devicesFromOutputProgress(ctx context.Context, out string, progress func([]Device)) []Device {
	groups := m.deviceGroups(out)
	devs := make([]Device, len(groups))
	for i, g := range groups {
		devs[i] = BuildDevice(g)
		m.peekDevice(ctx, &devs[i])
		m.annotateDevice(&devs[i])
	}
	publish := func() {
		if progress != nil && ctx.Err() == nil {
			progress(append([]Device(nil), devs...))
		}
	}
	publish() // Raw topology and available cached metadata precede any device I/O.
	type result struct {
		index  int
		device Device
	}
	results := make(chan result, len(groups))
	go func() {
		defer close(results)
		parallel.For(ctx, len(groups), parallel.Devices, func(i int) {
			qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			d := m.buildDevice(qctx, groups[i])
			m.annotateDevice(&d)
			if qctx.Err() == nil {
				select {
				case results <- result{i, d}:
				case <-ctx.Done():
				}
			}
		})
	}()
	for r := range results {
		devs[r.index] = r.device
		publish()
	}
	return devs
}

func (m *Manager) acquireEnrichment(ctx context.Context) (func(), bool) {
	m.mu.Lock()
	if m.enrichSlots == nil {
		m.enrichSlots = make(chan struct{}, parallel.Devices)
	}
	slots := m.enrichSlots
	m.mu.Unlock()
	select {
	case slots <- struct{}{}:
		if ctx.Err() == nil {
			return func() { <-slots }, true
		}
		<-slots
	case <-ctx.Done():
	}
	return nil, false
}
