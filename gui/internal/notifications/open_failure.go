package notifications

import (
	"context"
	"errors"
)

// Fixed phone result codes only; never retain an Intent or message in an error.
type OpenFailure struct{ Code string }

func (e *OpenFailure) Error() string { return "notification action: " + e.Code }
func (e *OpenFailure) Unwrap() error { return ErrUnavailable }

func OpenFailureCode(err error) string {
	var failure *OpenFailure
	if errors.As(err, &failure) {
		return failure.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled-context"
	}
	if errors.Is(err, ErrTransport) {
		return "connection"
	}
	if errors.Is(err, ErrUnavailable) {
		return "unavailable"
	}
	return "unknown"
}

func openFailureBody(err error) string {
	switch OpenFailureCode(err) {
	case "disabled":
		return "点击通知打开详情已关闭，请在设置中开启后点击新通知"
	case "switching":
		return "应用窗口正在切换，请稍后点击新通知重试"
	case "stale", "canceled":
		return "这条通知已更新或被应用撤回，请点击新收到的通知"
	case "connection", "unavailable":
		return "通知连接已变化，请等待设备连接恢复后点击新通知"
	case "unsupported":
		return "当前应用窗口不可用，请关闭该窗口后点击新通知重试"
	case "launch":
		return "应用详情未能进入投屏窗口，请重试或在手机上查看"
	case "timeout":
		return "打开通知详情超时，请重试或在手机上查看"
	default:
		return "未能打开通知详情，请重试或在手机上查看"
	}
}
