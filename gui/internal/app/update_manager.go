package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"scrcpy-ez/gui/internal/rootrepair"
	"scrcpy-ez/gui/internal/updater"
)

// 安装用写锁等待正在启动的投屏；各投屏用读锁，仍可并行启动。
type appUpdater struct {
	once       sync.Once
	manager    *updater.Manager
	mu         sync.RWMutex
	installing bool
}

func (a *App) updates() *updater.Manager {
	a.update.once.Do(func() {
		exe, _ := os.Executable()
		install := filepath.Dir(exe)
		// Root experiment must not consume stable candidates or contact stable
		// release feeds. No cache means no previously staged install can resume.
		a.update.manager = updater.New(updater.Options{Install: install, Version: a.cfg.Version,
			Fetch: func(context.Context) (updater.ReleaseInfo, error) {
				return updater.ReleaseInfo{Current: a.cfg.Version, Latest: a.cfg.Version,
					RepoURL: rootrepair.RepoURL, Notice: "root 尝试版独立分支；未实机验证。请单独解压后使用，正式版自动更新已停用。"}, nil
			}})
	})
	return a.update.manager
}
func (a *App) GetUpdateState() updater.State   { return a.updates().State() }
func (a *App) Version() string                 { return a.cfg.Version }
func (a *App) BeginUpdateCheck() updater.State { return a.updates().Check() }
func (a *App) DownloadUpdate() error {
	return fmt.Errorf("root 尝试版不支持正式版自动更新")
}
func (a *App) CancelUpdate()        { a.updates().Cancel() }
func (a *App) DismissUpdateResult() { a.updates().DismissResult() }

type InstallReply struct {
	NeedsConfirm  bool `json:"needsConfirm"`
	ActiveWindows int  `json:"activeWindows"`
}

func (a *App) InstallUpdate(confirmed bool, quit func()) (InstallReply, error) {
	return InstallReply{}, fmt.Errorf("root 尝试版不支持正式版自动更新")
}

// 整个 Start 持锁：确认安装后不会漏掉仍在连接中的新会话。
func (a *App) guardUpdateStart() (func(), error) {
	a.update.mu.RLock()
	if a.update.installing {
		a.update.mu.RUnlock()
		return nil, fmt.Errorf("正在重启更新，请稍后再投屏")
	}
	return a.update.mu.RUnlock, nil
}
