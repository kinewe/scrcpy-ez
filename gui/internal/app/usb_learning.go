package app

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"scrcpy-ez/gui/internal/bridge"
)

const usbLearnRetryDelay = 300 * time.Millisecond

type usbLearningState struct {
	id            string
	ctx           context.Context
	cancel        context.CancelFunc
	candidates    []string
	enabled       bool
	restartIssued atomic.Bool
}

func (a *App) stopUsbLearningLocked(id string) {
	delete(a.plugLearned, id)
	delete(a.plugStableReady, id)
	for serial, state := range a.usbLearning {
		if state.id == id {
			state.cancel()
			a.adb.EventHub().SetLearning(serial, false)
			delete(a.usbLearning, serial)
		}
	}
}

// 首次 device 就绪重设既有兜底 timer；同周期 adbd 重启不续期。
func (a *App) startUsbLearning(_ context.Context, serial string) {
	id := a.plugIDForSerial(serial)
	a.plugStart(id, time.Now(), "device学习")
	a.teachMu.Lock()
	if a.usbLearning[serial] != nil {
		a.teachMu.Unlock()
		return
	}
	now := time.Now()
	ctx, cancel := context.WithDeadline(context.Background(), now.Add(plugShieldTimeout))
	a.adb.EventHub().SetLearning(serial, true)
	state := &usbLearningState{id: id, ctx: ctx, cancel: cancel}
	a.usbLearning[serial] = state
	a.plugging[id] = now
	a.plugLearned[id] = false
	if timer := a.plugTimers[id]; timer != nil {
		timer.Stop()
	}
	a.plugTimers[id] = time.AfterFunc(plugShieldTimeout, func() {
		a.guard("plug-timeout", func() { a.plugTimeout(id) })
	})
	a.teachMu.Unlock()
	// Register before returning, but never do device I/O under the topology lock.
	a.mu.Lock()
	if a.notificationClosing {
		a.mu.Unlock()
		cancel()
		return
	}
	a.learningWG.Add(1)
	a.mu.Unlock()
	go a.guard("usb-learn", func() {
		defer a.learningWG.Done()
		for {
			finished := false
			if !a.learningWork.Do(ctx, func() { finished = a.usbLearnAttempt(serial, state) }) || finished {
				return
			}
			timer := time.NewTimer(usbLearnRetryDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	})
}

func (a *App) usbLearnAttempt(serial string, state *usbLearningState) bool {
	err := a.teachTcpipAttempt(state.ctx, serial, state)
	a.teachMu.Lock()
	current := a.usbLearning[serial] == state && state.ctx.Err() == nil
	if current && err == nil {
		a.plugLearned[state.id] = true
	}
	a.teachMu.Unlock()
	if !current {
		return true
	}
	if err != nil {
		bridge.DebugLog("[app] 插线学习待重试：%s（%v）", serial, err)
		return false
	}
	a.adb.EventHub().SetLearning(serial, false)
	bridge.DebugLog("[app] 插线学习完成：%s（无线已验证并落盘）", serial)
	a.plugStabilityCheck(state.id)
	return true
}

func (a *App) teachTcpipAttempt(ctx context.Context, serial string, state *usbLearningState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !state.enabled {
		gctx, cancel := context.WithTimeout(ctx, teachGetpropTimeout)
		port, err := a.teachOps.getpropFn(gctx, serial, "service.adb.tcp.port")
		cancel()
		if err != nil {
			return fmt.Errorf("读取端口：%w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		a.checkTlsSwitch(ctx, serial)
		state.candidates = a.learnWirelessIPCandidates(ctx, serial)
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.TrimSpace(port) != "5555" {
			state.restartIssued.Store(true)
			tctx, cancel := context.WithTimeout(ctx, teachTcpipTimeout)
			err = a.teachOps.tcpipFn(tctx, serial, "5555")
			cancel()
			if err != nil {
				return fmt.Errorf("开启5555：%w", err)
			}
		}
		state.enabled = true // 重启后只复查/探测，不反复重启 adbd
	} else if ips := a.learnWirelessIPCandidates(ctx, serial); len(ips) > 0 {
		state.candidates = ips // 重试重新学习当前 IP；adbd 暂时不可读时保留重启前候选
	}
	if len(state.candidates) == 0 {
		return fmt.Errorf("尚未读取到无线IP")
	}
	chosen := a.probeWirelessCandidates(ctx, serial, state.candidates)
	if chosen == "" {
		return fmt.Errorf("5555尚未就绪")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if state.id != "" {
		a.applyTrackMu.Lock()
		defer a.applyTrackMu.Unlock()
		a.teachMu.Lock()
		current := a.usbLearning[serial] == state && ctx.Err() == nil
		a.teachMu.Unlock()
		if !current || a.deletedUsbMarked(serial) {
			return context.Canceled
		}
	}
	return a.alignWirelessIP(serial, chosen)
}
