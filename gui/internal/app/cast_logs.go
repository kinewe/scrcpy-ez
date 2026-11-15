package app

import (
	"os"
	"sort"
	"time"
)

const castLogKeep = 10 * time.Second

type castLogKey struct {
	device string
	pkg    string // empty for the main cast
}

func castLogKeyFor(serial, identity, pkg string) castLogKey {
	if identity == "" {
		identity = serial
	}
	return castLogKey{device: identity, pkg: pkg}
}

// RecentCastLog is a closed session's output, separate from live cast controls.
type RecentCastLog struct {
	Serial     string   `json:"serial"`
	Identity   string   `json:"identity,omitempty"`
	DeviceName string   `json:"deviceName,omitempty"`
	Pkg        string   `json:"pkg,omitempty"`
	Name       string   `json:"name,omitempty"`
	Log        []string `json:"log"`
	ClosedAt   int64    `json:"closedAt"`
	expiresAt  time.Time
}

func (a *App) retainCastLogLocked(log RecentCastLog, now time.Time) {
	a.pruneCastLogsLocked(now)
	if a.recentCastLogs == nil {
		a.recentCastLogs = make(map[castLogKey]RecentCastLog)
	}
	log.Log = append([]string(nil), log.Log...)
	log.ClosedAt = now.UnixMilli()
	log.expiresAt = now.Add(castLogKeep)
	a.recentCastLogs[castLogKeyFor(log.Serial, log.Identity, log.Pkg)] = log
}

func (a *App) pruneCastLogsLocked(now time.Time) {
	for key, log := range a.recentCastLogs {
		if !now.Before(log.expiresAt) {
			delete(a.recentCastLogs, key)
		}
	}
}

func (a *App) liveCastLogLocked(key castLogKey, lines []string, now time.Time) []string {
	previous := a.recentCastLogs[key]
	if len(previous.Log) == 0 || !now.Before(previous.expiresAt) || len(lines) >= castLogLineLimit-1 {
		return append([]string{}, lines...)
	}
	// Keep space for the separator and give the current session priority.
	keep := min(len(previous.Log), castLogLineLimit-1-len(lines))
	out := make([]string, 0, keep+1+len(lines))
	out = append(out, previous.Log[len(previous.Log)-keep:]...)
	out = append(out, "[EZ] ── 新投屏会话 ──")
	return append(out, lines...)
}

func (a *App) recentCastLogsLocked(now time.Time, live map[castLogKey]bool) []RecentCastLog {
	var logs []RecentCastLog
	for key, log := range a.recentCastLogs {
		if now.Before(log.expiresAt) && !live[key] {
			log.Log = append([]string(nil), log.Log...)
			logs = append(logs, log)
		}
	}
	sort.Slice(logs, func(i, j int) bool {
		if !logs[i].expiresAt.Equal(logs[j].expiresAt) {
			return logs[i].expiresAt.Before(logs[j].expiresAt)
		}
		if logs[i].Serial != logs[j].Serial {
			return logs[i].Serial < logs[j].Serial
		}
		return logs[i].Pkg < logs[j].Pkg
	})
	return logs
}

func (a *App) retainMainCastLogLocked(serial string, st *sessionState, lastLine string, now time.Time) {
	a.retainCastLogLocked(RecentCastLog{
		Serial: serial, Identity: st.identity, DeviceName: st.cast.DevName,
		Log: appendCastLog(st.cast.Log, lastLine),
	}, now)
}

func (a *App) finishAppWinLocked(key string, st *appWinState, lastLine string) {
	if st.scannerReadyFile != "" {
		_ = os.Remove(st.scannerReadyFile)
	}
	a.retainCastLogLocked(RecentCastLog{
		Serial: st.serial, Identity: st.identity, DeviceName: st.deviceName,
		Pkg: st.pkg, Name: st.name, Log: appendCastLog(st.log, lastLine),
	}, time.Now())
	delete(a.appWins, key)
}
