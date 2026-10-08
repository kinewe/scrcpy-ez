package notifications

import (
	"errors"
	"fmt"
)

// Native failures carry only a bounded stage and numeric system code.
type sinkFailure struct {
	stage string
	code  uint32
}

func (e *sinkFailure) Error() string {
	return fmt.Sprintf("Windows notification %s (0x%08X)", e.stage, e.code)
}

func sinkStatus(err error) string {
	var failure *sinkFailure
	if errors.As(err, &failure) {
		labels := map[string]string{
			"ownership":    "电脑通知身份已被另一实例占用",
			"directory":    "电脑通知注册目录无法写入",
			"registration": "电脑通知身份注册失败",
			"com":          "电脑通知线程初始化失败",
			"shortcut":     "电脑通知快捷方式注册失败",
			"sender":       "Windows 尚未识别通知发送者",
			"service":      "电脑通知服务创建失败",
			"history":      "电脑通知历史初始化失败",
			"render":       "电脑通知内容转换失败",
			"setting":      "Windows 通知权限查询失败",
			"disabled":     "Windows 已关闭此程序的通知",
			"send":         "Windows 接收通知失败",
			"remove":       "电脑通知撤回失败",
			"clear":        "电脑通知清理失败",
		}
		label := labels[failure.stage]
		if label == "" {
			label = "电脑通知处理失败"
		}
		return fmt.Sprintf("%s（0x%08X）", label, failure.code)
	}
	return "电脑通知处理失败，请重新开启同步"
}

// Only fixed reason codes are shown. Foreign message data and exceptions never enter the UI.
type sourceFailure struct {
	code string
	err  error
}

func (e *sourceFailure) Error() string { return "notification source: " + e.code }
func (e *sourceFailure) Unwrap() error { return e.err }

func sourceStatus(err error) string {
	var failure *sourceFailure
	if errors.As(err, &failure) {
		switch failure.code {
		case "identity":
			return "设备身份未确认，请刷新设备后重试"
		case "server":
			return "手机服务文件缺失，请使用完整软件包"
		case "busy":
			return "手机通知监听被另一实例占用，正在重试"
		case "user":
			return "通知同步仅支持手机主用户空间"
		case "permission":
			return "手机系统拒绝通知监听权限"
		case "unsupported":
			return "手机系统不支持当前通知监听接口"
		case "timeout":
			return "手机通知监听启动超时，正在重试"
		case "startup":
			return "手机通知监听未启动，请关闭同步后重新开启"
		}
	}
	if errors.Is(err, ErrProtocol) {
		return "通知数据格式不兼容，请使用同版本完整包"
	}
	if errors.Is(err, ErrUnavailable) {
		return "手机通知监听未启动，请关闭同步后重新开启"
	}
	return "通知连接已中断，正在自动重连"
}
