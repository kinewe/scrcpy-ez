package adb

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// track-devices 协议（实测 2026-08-28，adb 37.0.0）：
// 输出 = 4 位 ASCII hex 长度前缀 + UTF-8 文本块，块内容与 `adb devices -l`
// 同格式（每行 serial + state + 扩展字段）。设备集变化时 adb server 重推整份
// 列表。注意：server 计算长度前缀时把换行按 '\n' 计 1 字节，但实际发送的是
// CRLF（每行多 1 个 CR），因此前缀值可能小于线上文本字节数——读取器按
// “忽略 CR 后计数达到前缀值” 判定块边界，LF/CRLF 两种输出均兼容。
const (
	trackReconnectMin    = 1 * time.Second
	trackReconnectMax    = 30 * time.Second
	trackReadBufferBytes = 64 * 1024
)

// TrackEvents 是一次 track-devices 列表块解析并 diff 后的事件批次。
// Devices 为本次完整设备列表快照（与 onEvents 同步回调，供上层按需使用）。
type TrackEvents struct {
	Added   []Device
	Removed []Device
	Changed []Device
	Devices []Device
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

// TrackDevices 启动 `adb track-devices` 长连并阻塞读取直到 ctx 取消。
// 流断（EOF/进程退出/读错误）后按指数退避重连（1s→2s→4s…上限 30s），
// 退避用 timer 链，不用 time.Sleep。仅 ctx 取消才终止。
// 首次成功读到列表块后重置退避。设备稳定期流静默是 track-devices 的正常状态，
// 不设静默看门狗（gui49-fix3：曾每 60s 误杀稳定流，实际断连由 EOF/退出链处理）。
func (m *Manager) TrackDevices(ctx context.Context, onEvents func(TrackEvents)) error {
	t := &Track{m: m, onEvents: onEvents}
	return t.run(ctx)
}

func (t *Track) run(ctx context.Context) error {
	backoff := trackReconnectMin
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		gotBlock, err := t.runOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			// 进程退出/读错误：按退避重连。退避计时用 timer，不用 Sleep。
		}
		if gotBlock {
			backoff = trackReconnectMin // 有数据即证明链路健康，重置退避
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if backoff < trackReconnectMax {
			backoff *= 2
			if backoff > trackReconnectMax {
				backoff = trackReconnectMax
			}
		}
	}
}

// runOnce 启动单个 track-devices 子进程并读块，返回是否成功收到至少一块。
// 进程退出/读错误直接返回 error；ctx 取消返回 ctx.Err()。
func (t *Track) runOnce(ctx context.Context) (bool, error) {
	c := exec.CommandContext(ctx, t.m.adbPath, "track-devices")
	HideConsole(c)
	stdout, err := c.StdoutPipe()
	if err != nil {
		return false, err
	}
	if err := c.Start(); err != nil {
		return false, err
	}
	// 子进程退出后关闭 stdout，读取侧自然 EOF。stderr 不转发（adb 错误日志
	// 对 GUI 无意义，且可能阻塞管道）。
	gotBlock := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		br := bufio.NewReaderSize(stdout, trackReadBufferBytes)
		for {
			block, rerr := readTrackBlock(br)
			if rerr != nil {
				return
			}
			if block == "" {
				// 空列表块（前缀 0000）：也算一次有效链路活动
				gotBlock = true
				devs := ParseTrackDevices(block)
				t.dispatch(devs)
				continue
			}
			gotBlock = true
			devs := t.m.devicesFromOutput(ctx, block)
			t.dispatch(devs)
		}
	}()

	<-done
	err = c.Wait()
	if ctx.Err() != nil {
		return gotBlock, ctx.Err()
	}
	if err != nil {
		return gotBlock, err
	}
	return gotBlock, nil
}

// dispatch 对一份完整列表块做 diff 并回调 onEvents。
func (t *Track) dispatch(devs []Device) {
	added, removed, changed := DiffDevices(t.prev, devs)
	t.prev = append([]Device(nil), devs...)
	if t.onEvents != nil {
		t.onEvents(TrackEvents{
			Added:   added,
			Removed: removed,
			Changed: changed,
			Devices: append([]Device(nil), devs...),
		})
	}
}

// readTrackBlock 从流中读一个长度前缀块；返回文本块内容（CRLF 已归一化为 LF）。
// 前缀 0000 返回空块（nil error）。
func readTrackBlock(br *bufio.Reader) (string, error) {
	var prefix [4]byte
	if _, err := io.ReadFull(br, prefix[:]); err != nil {
		return "", err
	}
	n, err := strconv.ParseUint(string(prefix[:]), 16, 32)
	if err != nil {
		return "", fmt.Errorf("adb track-devices 非法长度前缀 %q: %w", string(prefix[:]), err)
	}
	if n == 0 {
		return "", nil
	}
	// 前缀长度按 LF 计数（见文件头注释），忽略 CR 后收集 n 字节即到块尾。
	var sb strings.Builder
	sb.Grow(int(n) + 32)
	norm := 0
	for norm < int(n) {
		b, err := br.ReadByte()
		if err != nil {
			return "", err
		}
		if b == '\r' {
			continue
		}
		sb.WriteByte(b)
		norm++
	}
	return sb.String(), nil
}

// ParseTrackDevices 解析一个 track-devices 文本块（长度前缀已剥离）为合并设备
// 列表（纯函数，不执行任何 adb 查询；与 Manager.List 的合并规则一致）。
func ParseTrackDevices(block string) []Device {
	raw := ParseDevicesL(block)
	groups := GroupDevices(raw)
	devs := make([]Device, 0, len(groups))
	idxByName := map[string]int{}
	for _, g := range groups {
		d := BuildDevice(g)
		hasModel := g[0].Model != ""
		if !hasModel && d.State == "device" && d.Name != "" && d.Name != d.Serial {
			if j, ok := idxByName[d.Name]; ok {
				mergeDevice(&devs[j], d)
				continue
			}
			idxByName[d.Name] = len(devs)
		}
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
