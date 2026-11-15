package adb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

type ServerCheckState uint8

const (
	ServerUnverified ServerCheckState = iota
	ServerCompatible
	ServerIncompatible
)

type ServerCheck struct {
	State           ServerCheckState
	Detail          string
	ForwardDeferred bool
}

// CheckServer checks host discovery support and the same serial-specific dynamic
// forwarding used by the RGBA stream. Transport failures are inconclusive.
func (m *Manager) CheckServer(parent context.Context) ServerCheck {
	if m.srvCheckFn != nil {
		return m.srvCheckFn(parent)
	}
	cmd := m.runCombined
	if m.srvCommand != nil {
		cmd = m.srvCommand
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	mdns, err := cmd(ctx, "mdns", "check")
	mdns = strings.TrimSpace(mdns)
	if ctx.Err() != nil {
		return ServerCheck{Detail: "mDNS 检查超时或取消"}
	}
	if strings.Contains(strings.ToLower(mdns), "unknown host service") {
		return ServerCheck{State: ServerIncompatible, Detail: mdns}
	}
	if err != nil && !strings.Contains(strings.ToLower(mdns), "mdns daemon unavailable") &&
		!strings.Contains(strings.ToLower(mdns), "mdns discovery disabled") {
		return ServerCheck{Detail: "mDNS 检查未完成: " + mdns}
	}
	out, err := cmd(ctx, "devices", "-l")
	if err != nil {
		return ServerCheck{Detail: "设备列表暂不可用: " + strings.TrimSpace(out)}
	}
	devices := ParseDevicesL(out)
	counts := make(map[string]int, len(devices))
	for _, d := range devices {
		counts[d.Serial]++
	}
	var device RawDevice
	for _, d := range devices {
		if d.State == "device" && counts[d.Serial] == 1 && (device.Serial == "" || d.ConnType == "wifi") {
			device = d
			if d.ConnType == "wifi" {
				break
			}
		}
	}
	if device.Serial == "" {
		return ServerCheck{State: ServerCompatible, Detail: "mDNS 命令可用；等待在线设备验证转发", ForwardDeferred: true}
	}
	var id [12]byte
	if _, err := rand.Read(id[:]); err != nil {
		return ServerCheck{Detail: "无法生成 ADB 转发探测标识"}
	}
	remote := "localabstract:ez_forward_check_" + hex.EncodeToString(id[:])
	out, err = cmd(ctx, "-s", device.Serial, "forward", "tcp:0", remote)
	out = strings.TrimSpace(out)
	port, portErr := strconv.ParseUint(out, 10, 16)
	if err == nil && portErr == nil && port != 0 {
		clean, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer stop()
		if _, err := cmd(clean, "-s", device.Serial, "forward", "--remove", "tcp:"+out); err != nil {
			m.cleanServerProbe(clean, cmd, device.Serial, remote)
		}
		return ServerCheck{State: ServerCompatible, Detail: "mDNS 命令和指定设备的动态转发可用"}
	}
	// A lost reply can leave a listener behind. Only remove this probe's target.
	clean, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	installed := m.cleanServerProbe(clean, cmd, device.Serial, remote)
	stop()
	if ctx.Err() == nil {
		msg := strings.ToLower(out)
		if strings.Contains(msg, "unknown host service") || strings.Contains(msg, "more than one device") || (err == nil && installed) {
			// Confirm the same serial remains usable before attributing the error
			// to the host's forwarding service.
			if state, err := cmd(ctx, "-s", device.Serial, "get-state"); err == nil && strings.TrimSpace(state) == "device" {
				return ServerCheck{State: ServerIncompatible, Detail: "指定在线设备的动态转发不兼容: " + out}
			}
		}
	}
	return ServerCheck{Detail: "未能验证指定设备的动态转发: " + out}
}

func (m *Manager) cleanServerProbe(ctx context.Context, cmd func(context.Context, ...string) (string, error), serial, remote string) bool {
	list, err := cmd(ctx, "forward", "--list")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(list, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == serial && fields[2] == remote && strings.HasPrefix(fields[1], "tcp:") {
			_, _ = cmd(ctx, "-s", serial, "forward", "--remove", fields[1])
			return true
		}
	}
	return false
}
