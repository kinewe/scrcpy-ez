package adb

import (
	"bufio"
	"context"
	"fmt"
	"scrcpy-ez/gui/internal/deviceevents"
	"strings"
	"time"
)

// track-devices 协议（实测 2026-08-28，adb 37.0.0）：
// 输出 = 4 位 ASCII hex 长度前缀 + UTF-8 文本块，块内容与 `adb devices -l`
// 同格式（每行 serial + state + 扩展字段）。设备集变化时 adb server 重推整份
// 列表。注意：server 计算长度前缀时把换行按 '\n' 计 1 字节，但实际发送的是
// CRLF（每行多 1 个 CR），因此前缀值可能小于线上文本字节数——读取器按
// “忽略 CR 后计数达到前缀值” 判定块边界，LF/CRLF 两种输出均兼容。
// TrackEvents 是一次 track-devices 列表块解析并 diff 后的事件批次。
// Devices 为本次完整设备列表快照（与 onEvents 同步回调，供上层按需使用）。
type TrackEvents struct {
	Added   []Device
	Removed []Device
	Changed []Device
	Devices []Device
	Raw     *deviceevents.Snapshot
}

// Track 是 `adb track-devices` 长连的状态封装。
type Track struct {
	m        *Manager
	onEvents func(TrackEvents)
	prev     []Device
}

// NewTrack 创建 track-devices 长连（不启动；由 Run 启动）。
func (m *Manager) NewTrack(onEvents func(TrackEvents)) *Track {
	return &Track{m: m, onEvents: onEvents}
}

// TrackDevices uses the shared raw hub; enrichment cannot block ADB stream reads.
func (m *Manager) TrackDevices(ctx context.Context, onEvents func(TrackEvents)) error {
	t := &Track{m: m, onEvents: onEvents}
	return t.run(ctx)
}

func (t *Track) run(ctx context.Context) error {
	hub := t.m.EventHub()
	// The raw reader is independent of property enrichment and GUI callbacks.
	events := hub.Subscribe(ctx)
	go deviceevents.Track(ctx, t.m.adbPath, hub)
	refresh := time.NewTicker(5 * time.Second)
	defer refresh.Stop()
	return t.runSnapshots(ctx, events, refresh.C)
}

// Topology remains event driven. Refresh only the current connected transports'
// cached properties; battery/spec TTLs control actual ADB queries.
func (t *Track) runSnapshots(ctx context.Context, events <-chan deviceevents.Snapshot, refresh <-chan time.Time) error {
	hub := t.m.EventHub()
	var dispatched *deviceevents.Snapshot
	for {
		periodic := false
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-events:
			if !ok {
				return ctx.Err()
			}
		case <-refresh:
			periodic = true
		}
		// Coalesce queued property-query work to the latest raw transport set.
		s := hub.Current()
		if !s.Available {
			continue
		}
		same := dispatched != nil && deviceevents.SameTransports(*dispatched, s)
		if same && !periodic {
			continue // learning notifications do not create device-list changes
		}
		qctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		accepted := t.consumeSnapshotProperties(qctx, s, periodic && same)
		cancel()
		if !accepted {
			continue
		}
		dispatched = &s
	}
}

func (t *Track) consumeSnapshot(ctx context.Context, s deviceevents.Snapshot) bool {
	return t.consumeSnapshotProperties(ctx, s, false)
}

func (t *Track) consumeSnapshotProperties(ctx context.Context, s deviceevents.Snapshot, propertiesOnly bool) bool {
	devs := t.m.DevicesFromSnapshot(ctx, s)
	if ctx.Err() != nil || !deviceevents.SameTransports(s, t.m.EventHub().Current()) {
		return false // USB changed during enrichment; do not replay old topology
	}
	if propertiesOnly {
		added, removed, changed := DiffDevices(t.prev, devs)
		if len(added)+len(removed)+len(changed) == 0 {
			return true
		}
	}
	t.dispatchSnapshot(devs, &s)
	return true
}

// dispatch 对一份完整列表块做 diff 并回调 onEvents。
func (t *Track) dispatch(devs []Device) {
	t.dispatchSnapshot(devs, nil)
}

func (t *Track) dispatchSnapshot(devs []Device, raw *deviceevents.Snapshot) {
	added, removed, changed := DiffDevices(t.prev, devs)
	t.prev = append([]Device(nil), devs...)
	if t.onEvents != nil {
		t.onEvents(TrackEvents{
			Added:   added,
			Removed: removed,
			Changed: changed,
			Devices: append([]Device(nil), devs...),
			Raw:     raw,
		})
	}
}

// DevicesFromSnapshot enriches an already captured event list. It never runs
// `adb devices`; its caller must validate the raw token before committing.
func (m *Manager) DevicesFromSnapshot(ctx context.Context, s deviceevents.Snapshot) []Device {
	var b strings.Builder
	for _, v := range s.Transports {
		fmt.Fprintf(&b, "%s\t%s", v.Serial, v.State)
		if v.Kind == "usb" {
			b.WriteString(" usb:event")
		}
		if v.ID != "" {
			fmt.Fprintf(&b, " transport_id:%s", v.ID)
		}
		if v.Model != "" {
			fmt.Fprintf(&b, " model:%s", v.Model)
		}
		b.WriteByte('\n')
	}
	return m.devicesFromOutput(ctx, b.String())
}

// readTrackBlock 从流中读一个长度前缀块；返回文本块内容（CRLF 已归一化为 LF）。
// 前缀 0000 返回空块（nil error）。
func readTrackBlock(br *bufio.Reader) (string, error) { return deviceevents.ReadBlock(br) }

// ParseTrackDevices 解析一个 track-devices 文本块（长度前缀已剥离）为合并设备
// 列表（纯函数，不执行任何 adb 查询；与 Manager.List 的合并规则一致）。
func ParseTrackDevices(block string) []Device {
	raw := ParseDevicesL(block)
	groups := GroupDevices(raw)
	devs := make([]Device, 0, len(groups))
	for _, g := range groups {
		d := BuildDevice(g)
		d.StableSerial = StableSerial(d.Serial)
		d.Identity = IdentityKey("", "", "", d.Serial)
		devs = append(devs, d)
	}
	return devs
}

// DiffDevices 对两份合并设备列表做 diff（纯函数）。
// key 取 Serial；changed = 同 serial 但任意字段变化。
func DiffDevices(prev, cur []Device) (added, removed, changed []Device) {
	prevIdx := make(map[string]int, len(prev))
	for i := range prev {
		prevIdx[prev[i].Serial] = i
	}
	curIdx := make(map[string]int, len(cur))
	for i := range cur {
		curIdx[cur[i].Serial] = i
	}
	for i := range cur {
		d := cur[i]
		j, ok := prevIdx[d.Serial]
		if !ok {
			added = append(added, d)
			continue
		}
		if !deviceEqual(prev[j], d) {
			changed = append(changed, d)
		}
	}
	for i := range prev {
		if _, ok := curIdx[prev[i].Serial]; !ok {
			removed = append(removed, prev[i])
		}
	}
	return added, removed, changed
}

func deviceEqual(a, b Device) bool {
	return a.Serial == b.Serial &&
		a.StableSerial == b.StableSerial &&
		a.State == b.State &&
		a.ConnType == b.ConnType &&
		a.Name == b.Name &&
		a.Model == b.Model &&
		a.Marketname == b.Marketname &&
		a.Manufacturer == b.Manufacturer &&
		a.Identity == b.Identity &&
		a.Wireless == b.Wireless &&
		a.WirelessRes == b.WirelessRes &&
		a.Battery == b.Battery &&
		a.Res == b.Res &&
		a.FPS == b.FPS &&
		a.Tls == b.Tls &&
		a.WirelessForm == b.WirelessForm &&
		a.Connecting == b.Connecting
}
