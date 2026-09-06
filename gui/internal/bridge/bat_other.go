//go:build !windows

package bridge

// BatRunner 的非 Windows 桩实现：仅保证包可编译、可在 WSL 下单测纯逻辑。
// 真实实现见 bat_windows.go（CreateProcess 隐藏窗口 + 管道桥接）。
type BatRunner struct {
	batPath string
	adbPath string
	onLine  func(string)
	onExit  func(int)
}

func NewBatRunner(batPath, adbPath string, onLine func(string), onExit func(int)) *BatRunner {
	return &BatRunner{batPath: batPath, adbPath: adbPath, onLine: onLine, onExit: onExit}
}

func (r *BatRunner) Start(_ string, _ CastParams) error { return nil }
func (r *BatRunner) Stop() error                        { return nil }
func (r *BatRunner) ExitCode() int                      { return -1 }
