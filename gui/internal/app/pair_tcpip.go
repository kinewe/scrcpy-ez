package app

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/adbparse"
	"scrcpy-ez/gui/internal/discovery"
)

type pendingTcpipProbe struct {
	busy bool
	ok   bool
	at   time.Time
}

func adbAuthorizationPending(output string, err error) bool {
	if err != nil {
		output += " " + err.Error()
	}
	output = strings.ToLower(output)
	return strings.Contains(output, "unauthorized") || strings.Contains(output, "authorizing") ||
		strings.Contains(output, "failed to authenticate")
}

func (a *App) directPairStateFailure(ctx context.Context, addr string) string {
	if a.pairOps.stateFn == nil {
		return ""
	}
	qctx, cancel := context.WithTimeout(ctx, serialReadTimeout)
	defer cancel()
	state, err := a.pairOps.stateFn(qctx, addr)
	if adbAuthorizationPending(state, err) {
		return PairErrAuth
	}
	if qctx.Err() != nil {
		return PairErrTimeout
	}
	if err != nil || strings.TrimSpace(state) != "device" {
		return PairErrConnect
	}
	return ""
}

func validPairPort(port string) bool {
	if len(port) == 0 || len(port) > 5 || !isDigits(port) {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= 65535
}

func validPairIP(ip string) bool {
	p := net.ParseIP(ip)
	return p != nil && p.To4() != nil && p.String() == ip
}

func isTcpipPort(port string) bool {
	n, err := strconv.Atoi(port)
	return err == nil && n == 5555
}

// This fast path probes IPs already reported by ADB mDNS. pair_lan.go supplies
// independent, protocol-verified candidates when the device broadcasts nothing.
// Four probes at a time, cached for ten seconds; results cannot revive removed IPs.
func (a *App) pendingDirectAddrs(candidates []PendingDevice) map[string]string {
	out := map[string]string{}
	if a.pairOps.probeFn == nil {
		return out
	}
	wanted := map[string]bool{}
	for _, p := range candidates {
		wanted[p.IP] = true
	}
	a.mu.Lock()
	if a.pendingTcpip == nil {
		a.pendingTcpip = map[string]*pendingTcpipProbe{}
	}
	for ip := range a.pendingTcpip {
		if !wanted[ip] {
			delete(a.pendingTcpip, ip)
		}
	}
	for ip := range wanted {
		probe := a.pendingTcpip[ip]
		if probe != nil && probe.ok {
			out[ip] = ip + ":5555"
		}
		if probe != nil && (probe.busy || time.Since(probe.at) < 10*time.Second) {
			continue
		}
		if a.pendingProbeRunning >= 4 {
			continue
		}
		if probe == nil {
			probe = &pendingTcpipProbe{}
			a.pendingTcpip[ip] = probe
		}
		probe.busy = true
		a.pendingProbeRunning++
		go a.guard("pending-tcpip-probe", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			ok := a.pairOps.probeFn(ctx, ip+":5555")
			cancel()
			a.mu.Lock()
			a.pendingProbeRunning--
			current := a.pendingTcpip[ip] == probe
			if current {
				probe.busy, probe.ok, probe.at = false, ok, time.Now()
			}
			a.mu.Unlock()
			a.reapplyDisplay("pending tcpip probe")
		})
	}
	a.mu.Unlock()
	return out
}

// TCP reachability is only an affordance. Success requires an authenticated ADB
// shell and an actual serial; mDNS alone must never authorize archive creation.
func (a *App) runDirectPairFlow(p PendingDevice, ip, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), connectPairTimout+3*verifyTimeout+mdnsTimeout)
	defer cancel()
	addr := ip + ":5555"
	cctx, ccancel := context.WithTimeout(ctx, connectPairTimout)
	out, err := a.pairOps.connectFn(cctx, addr)
	ccancel()
	if err != nil {
		code := PairErrConnect
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(cctx.Err(), context.DeadlineExceeded) {
			code = PairErrTimeout
		} else if errors.Is(err, os.ErrNotExist) || errors.Is(err, exec.ErrNotFound) {
			code = PairErrRuntime
		} else if adbAuthorizationPending(out, err) {
			code = PairErrAuth
		}
		a.setPairFailed(code, "连接失败："+strings.TrimSpace(out), p.Key, ip)
		return
	}
	if code := a.directPairStateFailure(ctx, addr); code != "" {
		a.setPairFailed(code, "5555 连接尚未获得可用的 ADB 授权，请在设备上允许调试或改用无线调试配对码", p.Key, ip)
		return
	}
	a.mu.Lock()
	if a.pair != nil {
		a.pair.Phase = PairPhaseVerify
		a.pair.Steps[0].OK = true
		a.pair.Steps = append(a.pair.Steps, PairStep{Name: PairStepDone, Text: "正在验证设备信息…"})
	}
	a.mu.Unlock()
	serial := a.readWirelessDeviceSerial(ctx, addr)
	if serial == "" || p.Serial != "" && p.Serial != serial {
		if serial == "" {
			if code := a.directPairStateFailure(ctx, addr); code != "" {
				a.setPairFailed(code, "设备调试授权尚未生效，请在设备上允许调试或改用无线调试配对码", p.Key, ip)
				return
			}
		}
		a.setPairFailed(PairErrSerial, "连接后无法确认设备身份：请检查手机上的 ADB 授权提示后重试", p.Key, ip)
		return
	}
	vctx, vcancel := context.WithTimeout(ctx, verifyTimeout)
	market, _ := a.pairOps.getpropFn(vctx, addr, "ro.product.marketname")
	manufacturer, _ := a.pairOps.getpropFn(vctx, addr, "ro.product.manufacturer")
	model, _ := a.pairOps.getpropFn(vctx, addr, "ro.product.model")
	vcancel()
	if err := a.profiles.TcpipArchive(serial, addr, market, model); err != nil {
		a.setPairFailed(PairErrInternal, "设备已连接，但档案保存失败："+err.Error(), p.Key, ip)
		return
	}
	// Fresh scan supplements the resilient live listener. Merge only services
	// whose advertised serial agrees with the device read through port 5555.
	a.archiveDirectMdns(ctx, ip, serial)
	identity := a.profiles.ResolveKey(serial)
	d := &adb.Device{Serial: addr, State: "device", ConnType: "wifi", Name: market,
		Marketname: market, Manufacturer: manufacturer, Model: model, Identity: identity,
		StableSerial: serial, WirelessForm: ModeTcpip}
	if d.Name == "" {
		d.Name = serial
	}
	a.clearDeletedForProfile(ip, serial, "")
	a.mu.Lock()
	if a.pair != nil {
		a.pair.Phase, a.pair.Name, a.pair.Addr, a.pair.Device = PairPhaseSuccess, d.Name, addr, d
		a.pair.Steps[len(a.pair.Steps)-1].OK = true
	}
	for i := len(a.pending) - 1; i >= 0; i-- {
		if a.pending[i].IP == ip {
			a.pending = append(a.pending[:i], a.pending[i+1:]...)
		}
	}
	a.mu.Unlock()
}

func (a *App) archiveDirectMdns(ctx context.Context, ip, serial string) {
	var services []discovery.MdnsService
	if a.pairOps.mdnsScanFn != nil {
		sctx, cancel := context.WithTimeout(ctx, mdnsTimeout)
		fresh, err := a.pairOps.mdnsScanFn(sctx, mdnsTimeout)
		cancel()
		if err == nil {
			services = fresh
		}
	}
	// The listener's current values win when a port changed during the scan.
	services = append(services, a.mdnsSnapshot()...)
	matched := map[string]discovery.MdnsService{}
	for _, s := range services {
		if (s.Mode != ModeTls && s.Mode != ModeTcpip) || !adbparse.IsIPEndpoint(s.Addr) ||
			ipOfAddr(s.Addr) != ip || SerialFromServiceName(s.Name) != serial {
			continue
		}
		matched[s.Mode+"|"+s.Name] = s
	}
	var matches []discovery.MdnsService
	for _, m := range matched {
		matches = append(matches, m)
	}
	// A scan may return cached TLS while the newly verified 5555 channel is
	// working. Authenticate every candidate separately before archiving it.
	if key := a.profiles.ResolveKey(serial); key != "" {
		a.checkProfileChannels(ctx, key, matches)
	}
}
