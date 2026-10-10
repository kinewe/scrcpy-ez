package app

import (
	"context"
	"errors"
	"time"

	"scrcpy-ez/gui/internal/bridge"
	"scrcpy-ez/gui/internal/notifications"
)

type notificationOpener interface {
	Current(notifications.OpenRequest) bool
	Open(context.Context, notifications.OpenRequest, int) error
}

func (a *App) notificationOpenAllowed(req notifications.OpenRequest) bool {
	settings := a.settings.Get()
	if !rePkgName.MatchString(req.Package) {
		return false
	}
	policy := settings.NotificationPolicy(req.Identity)
	entry, ok := a.profiles.Entry(req.Identity)
	if !ok {
		return false
	}
	for _, app := range entry.Apps {
		policy.Catalog = append(policy.Catalog, app.Pkg)
	}
	if !policy.AllowsOpen(req.OwnerPackage, req.DisplayPackage) {
		return false
	}
	a.mu.RLock()
	closing := a.notificationClosing
	a.mu.RUnlock()
	return !closing
}

// A notification supplies the destination itself. Starting the app home page
// (especially with '+') would force-stop it and discard the pending detail.
func (a *App) OpenNotification(parent context.Context, req notifications.OpenRequest, source notificationOpener) (result error) {
	started := time.Now()
	stage := "begin"
	created := false
	displayID, clientPID := 0, 0
	trace := func(event, code string) {
		notifications.TraceOpen(event, code, req.Identity, req.Package, displayID, clientPID, time.Since(started), created)
	}
	trace("begin", "")
	defer func() {
		if result != nil {
			code := notifications.OpenFailureCode(result)
			if errors.Is(result, context.DeadlineExceeded) {
				if stage == "window" {
					code = "ready-timeout"
				} else if stage == "action" {
					code = "action-timeout"
				} else if stage == "sent" {
					code = "video-timeout"
				}
			}
			if stage == "start" && code == "unknown" {
				code = "start"
			}
			trace("failed", code)
		} else {
			trace("complete", "opened")
		}
	}()
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	a.notificationOpenMu.Lock()
	defer a.notificationOpenMu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !a.notificationOpenAllowed(req) {
		return &notifications.OpenFailure{Code: "disabled"}
	}
	if !source.Current(req) {
		return &notifications.OpenFailure{Code: "stale"}
	}
	a.mu.RLock()
	var window *appWinState
	for _, candidate := range a.appWins {
		if candidate.identity == req.Identity && candidate.pkg == req.Package {
			if candidate.closing || candidate.restarting {
				a.mu.RUnlock()
				return &notifications.OpenFailure{Code: "switching"}
			}
			window = candidate
			break
		}
	}
	a.mu.RUnlock()
	created = window == nil
	if created {
		stage = "start"
		if err := a.startAppWin(req.Identity, req.Package, req.App, true); err != nil {
			return err
		}
		a.mu.RLock()
		window = a.appWins[appWinKey(req.Identity, req.Package)]
		a.mu.RUnlock()
	}
	if window == nil {
		return notifications.ErrUnavailable
	}
	stage = "window"
	trace("window", "")
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	sent := false
	failure := notifications.ErrUnavailable
	for {
		// Apps commonly remove their notification while opening its detail.
		// Once the original action was accepted, wait for video independently.
		if !a.notificationOpenAllowed(req) {
			failure = &notifications.OpenFailure{Code: "disabled"}
			break
		}
		if !sent && !source.Current(req) {
			failure = &notifications.OpenFailure{Code: "stale"}
			break
		}
		a.mu.RLock()
		current := a.appWins[appWinKey(window.serial, window.pkg)]
		usable := current == window && !window.closing && !window.restarting
		ready := usable && window.displayReady && window.displayID > 0
		display := window.displayID
		hasVideo := window.displayHasVideo
		clientPID = window.clientPID
		a.mu.RUnlock()
		displayID = display
		if !usable {
			failure = &notifications.OpenFailure{Code: "window-lost"}
			break
		}
		if ready && !sent {
			stage = "action"
			trace("ready", "")
			a.mu.Lock()
			window.phase, window.phaseText = "notification-opening", "正在打开通知详情…"
			a.mu.Unlock()
			if err := source.Open(ctx, req, display); err != nil {
				failure = err
				break
			}
			sent = true
			stage = "sent"
			continue
		}
		if sent && hasVideo {
			a.mu.Lock()
			if window.phase == "notification-opening" {
				window.phase, window.phaseText = "", ""
			}
			a.mu.Unlock()
			// Fronting is asynchronous; it does not launch or restart the application.
			a.mu.RLock()
			pid := window.clientPID
			a.mu.RUnlock()
			if pid > 0 {
				go bridge.BringClientToFront(pid)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			failure = ctx.Err()
			goto failed
		case <-ticker.C:
		}
	}
failed:
	if created {
		a.mu.RLock()
		same := a.appWins[appWinKey(window.serial, window.pkg)] == window
		a.mu.RUnlock()
		if same {
			_ = a.StopAppWin(window.serial, window.pkg)
		}
	}
	if !created {
		a.mu.Lock()
		if window.phase == "notification-opening" {
			window.phase, window.phaseText = "", ""
		}
		a.mu.Unlock()
	}
	return failure
}
