package bridge

// 桥接调试日志（实证优先）：SCEZ_BRIDGE_LOG=1 → 默认写 exe 同目录
// bridge-YYYYMMDD.log；SCEZ_BRIDGE_LOG=<路径> → 写指定文件；未设置 → 完全关闭。
// 关闭时 DebugLog 只做一次原子读，零分配、零文件开销（单测锁定）。
//
// 记录事件：bat stdout/stderr 逐行（带行号）、进程启动/退出/退出码、停止杀树、
// app 层会话级决策（用户重启/无输出提示）。只读架构：GUI 不写 stdin，无写 stdin 事件。
//
// 异步设计（gui6）：DebugLog 热路径 = 一次 atomic 读 + 一次 atomic 指针载入 +
// 一次非阻塞 channel 发送——绝不触碰磁盘（webview RPC 回调在主线程执行，
// 同步文件 IO 会让"点停止"卡顿）。文件由 EnableDebugLog 打开一次，单写协程
// 逐行写（同一 channel 顺序消费 → 保序、每行完整）；队列满（4096）丢行并计数。
// FlushDebugLog 关闭通道并等待写协程排空（程序退出用，防丢尾行/goroutine 泄漏）。

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const debugLogQueueCap = 4096

var (
	debugMu      sync.Mutex
	debugEnabled atomic.Bool
	debugFile    *os.File
	debugChPtr   atomic.Pointer[chan string] // 热路径锁自由；nil=未启用/已 flush
	debugDone    chan struct{}               // 写协程退出信号（close 后等待排空）
	debugDropped atomic.Int64                // 队列满被丢弃的行数（诊断用）
)

func init() {
	if v := os.Getenv("SCEZ_BRIDGE_LOG"); v != "" {
		p := v
		if p == "1" {
			p = defaultBridgeLogPath()
		}
		EnableDebugLog(p)
	}
}

func defaultBridgeLogPath() string {
	name := "bridge-" + time.Now().Format("20060102") + ".log"
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), name)
	}
	return name
}

// EnableDebugLog 打开调试日志（幂等）。文件打不开时静默保持关闭。
func EnableDebugLog(path string) {
	debugMu.Lock()
	defer debugMu.Unlock()
	if debugEnabled.Load() {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	debugFile = f
	ch := make(chan string, debugLogQueueCap)
	debugChPtr.Store(&ch)
	debugDropped.Store(0)
	debugEnabled.Store(true)
	done := make(chan struct{})
	debugDone = done
	// 单写协程：同一 channel 顺序消费 → 日志按时间顺序落盘、每行完整
	go func() {
		defer close(done)
		for line := range ch {
			if f := debugFile; f != nil {
				_, _ = f.WriteString(line)
			}
		}
	}()
}

// DisableDebugLog 关闭调试日志（测试用；生产由环境变量在 init 决定）。
// 先停收新行、再关闭通道并等待写协程排空（不丢已入队行、不泄漏 goroutine），
// 最后才关文件。
func DisableDebugLog() {
	debugMu.Lock()
	if !debugEnabled.Load() {
		debugMu.Unlock()
		return
	}
	debugEnabled.Store(false)
	var nilCh *chan string
	old := debugChPtr.Swap(nilCh)
	f := debugFile
	done := debugDone
	debugMu.Unlock()

	if old != nil {
		close(*old)
		if done != nil {
			<-done // join：写协程已排空（debugFile 在此之前保持非 nil，写不落空）
		}
	}

	debugMu.Lock()
	debugFile = nil
	debugDone = nil
	debugMu.Unlock()
	if f != nil {
		_ = f.Close()
	}
}

// FlushDebugLog 关闭日志通道并等待写协程排空（程序退出前调用：防丢尾行）。
// 幂等；flush 后 DebugLog 直接丢弃（退出场景，无后续消费方）。
func FlushDebugLog() {
	debugMu.Lock()
	var nilCh *chan string
	old := debugChPtr.Swap(nilCh)
	done := debugDone
	debugMu.Unlock()
	if old == nil {
		return
	}
	close(*old)
	if done != nil {
		<-done
	}
}

// DebugLog 追加一行调试日志：`HH:MM:SS.mmm 内容`。
// 异步写（缓冲通道 + 单写协程）；队列满时丢行并计数，绝不阻塞调用方。
// 热路径无锁（atomic 读 + atomic 指针载入 + 非阻塞 send）——webview 主线程零磁盘 IO。
func DebugLog(format string, args ...any) {
	if !debugEnabled.Load() {
		return
	}
	line := time.Now().Format("15:04:05.000 ") + fmt.Sprintf(format, args...) + "\n"
	ch := debugChPtr.Load()
	if ch == nil {
		return
	}
	select {
	case *ch <- line:
	default: // 队列满：丢弃并计数，保证零阻塞
		debugDropped.Add(1)
	}
}

// debugLogDroppedN 返回被丢弃的行数（测试用）。
func debugLogDroppedN() int64 {
	return debugDropped.Load()
}

// debugLogPathForTest 返回当前日志文件路径（测试用）。
func debugLogPathForTest() string {
	debugMu.Lock()
	defer debugMu.Unlock()
	if debugFile == nil {
		return ""
	}
	return debugFile.Name()
}
