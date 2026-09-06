// Package discovery 提供无线设备探测：adb mdns 扫描解析 + 多地址并行 connect。
// 探测不阻塞 UI：所有操作带超时（mDNS 截断等待、connect 窗口 6s 取首个成功），
// 全部失败时由上层标记"未找到"状态。
package discovery

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// mDNS 服务形态与解析已提取到 internal/adb/mdns.go（track-services 与
// `adb mdns services` 共用）。本包保留别名，既有调用方与测试零改动。
const (
	MdnsModeTls     = adb.MdnsModeTls
	MdnsModeTcpip   = adb.MdnsModeTcpip
	MdnsModePairing = adb.MdnsModePairing
)

// MdnsService 是 `adb mdns services` / track-services 的一条解析结果（adb 包类型别名）。
type MdnsService = adb.MdnsService

// MdnsModeOf 按服务类型判定形态（代理 adb.MdnsModeOf）。
func MdnsModeOf(svcType string) string { return adb.MdnsModeOf(svcType) }

// ParseMdnsServices 解析 `adb mdns services` 输出（代理 adb.ParseMdnsServices）。
func ParseMdnsServices(output string) []MdnsService { return adb.ParseMdnsServices(output) }

// Connector 执行 adb connect 探测。ConnectFn/ConnectOutFn/MdnsScanFn/RestartServerFn
// 为测试注入点（nil=真实 adb.exe）。
type Connector struct {
	AdbPath         string
	ConnectFn       func(ctx context.Context, addr string) error
	ConnectOutFn    func(ctx context.Context, addr string) (string, error)
	MdnsScanFn      func(ctx context.Context, maxWait time.Duration) ([]MdnsService, error)
	RestartServerFn func(ctx context.Context) error
	TcpProbeFn      func(ctx context.Context, addr string) bool
}

func New(adbPath string) *Connector {
	return &Connector{AdbPath: adbPath}
}

// newAdbCmd 构造隐藏控制台的 adb 子进程命令。
// adb.exe 是 console 程序，GUI（无控制台）直接启动会弹控制台窗口
// （connect + mdns 各一次 = 用户看到的"两次框"）——所有 exec 入口必须过这里。
func newAdbCmd(ctx context.Context, adbPath string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, adbPath, args...)
	adb.HideConsole(cmd) // Windows: CREATE_NO_WINDOW + HideWindow；其他平台空实现
	return cmd
}

// ConnectOut 执行一次 adb connect 并返回输出。adb connect 失败也以退出码 0 返回
// （输出形如 "cannot connect to ..."），因此必须按输出判成败：
// 输出含 "connected to"（含 "already connected to"）才算成功。
// ConnectOutFn（测试注入）只替换执行，成败判定仍走同一输出规则。
func (c *Connector) ConnectOut(ctx context.Context, addr string) (string, error) {
	var out string
	if c.ConnectOutFn != nil {
		out2, err := c.ConnectOutFn(ctx, addr)
		if err != nil {
			return out2, err
		}
		out = out2
	} else {
		cmd := newAdbCmd(ctx, c.AdbPath, "connect", addr)
		b, err := cmd.Output()
		if err != nil {
			return string(b), err
		}
		out = string(b)
	}
	if !strings.Contains(out, "connected to") {
		return out, fmt.Errorf("connect 未成功: %s", strings.TrimSpace(out))
	}
	return out, nil
}

// Connect 执行一次 adb connect（超时由 ctx 控制）。返回 error 表示失败。
func (c *Connector) Connect(ctx context.Context, addr string) error {
	if c.ConnectFn != nil {
		return c.ConnectFn(ctx, addr)
	}
	_, err := c.ConnectOut(ctx, addr)
	return err
}

// TcpProbe 纯 TCP 握手探测：DialTimeout 2s = 超时上限（LAN 内正常毫秒级返回），
// 只证明「ip:port 端口活着」——不建立 adb transport、不产生设备流残留。
// TcpProbeFn（测试注入点）非 nil 时直接采用；否则用 net.Dialer 纯 TCP 连接
// 成功后立即关闭。本函数只回答「端口是否活着」，不翻任何状态（状态由调用方定）。
func (c *Connector) TcpProbe(ctx context.Context, addr string) bool {
	if c.TcpProbeFn != nil {
		return c.TcpProbeFn(ctx, addr)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(dctx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// ConnectTiers 分层探测：按 tiers 顺序逐层尝试（层内并行、层间串行），
// 首层成功即返回——用于 TLS 优先策略：tls 层全部失败才回退 tcpip 层。
// 每层内部语义与 ConnectAny 相同（窗口 window 内取首个成功，每地址 ≤ perTimeout）。
func (c *Connector) ConnectTiers(ctx context.Context, tiers [][]string, perTimeout, window time.Duration) (string, bool) {
	wctx, cancel := context.WithTimeout(ctx, window)
	defer cancel()
	for _, tier := range tiers {
		if len(tier) == 0 {
			continue
		}
		if addr, ok := c.ConnectAny(wctx, tier, perTimeout, window); ok {
			return addr, true
		}
	}
	return "", false
}

// ConnectAny 并行探测多个地址（active 优先传入），窗口 window 内取首个成功；
// 每个地址单次 connect 最多 perTimeout。全部失败（含全部超时）→ ok=false。
// 成功时取消其余探测（不阻塞 UI：总耗时 ≤ window）。
func (c *Connector) ConnectAny(ctx context.Context, addrs []string, perTimeout, window time.Duration) (string, bool) {
	if len(addrs) == 0 {
		return "", false
	}
	wctx, cancel := context.WithTimeout(ctx, window)
	defer cancel()

	results := make(chan string, len(addrs))
	for _, addr := range addrs {
		addr := addr
		go func() {
			cctx, stop := context.WithTimeout(wctx, perTimeout)
			defer stop()
			if err := c.Connect(cctx, addr); err == nil {
				results <- addr
			} else {
				results <- ""
			}
		}()
	}
	for range addrs {
		select {
		case <-wctx.Done():
			return "", false
		case r := <-results:
			if r != "" {
				return r, true
			}
		}
	}
	return "", false
}

// MdnsScan 执行 `adb mdns services`（截断等待 maxWait，1-2s 推荐）。
// 返回 (nil, nil) 表示正常空列表（设备没广播）；返回 (nil, err) 表示执行失败。
// gui16：宿主 mDNS daemon 挂死时 adb 以退出码 0 输出 "error: unknown host service"
// 且列表为空——症状在输出文本、不在退出码，必须按文本判定（超时/进程失败是
// 瞬态网络，不算 daemon 挂）。此错误由 app 层连续计数并触发 kill-server 自愈。
func (c *Connector) MdnsScan(ctx context.Context, maxWait time.Duration) ([]MdnsService, error) {
	if c.MdnsScanFn != nil {
		return c.MdnsScanFn(ctx, maxWait)
	}
	cctx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()
	cmd := newAdbCmd(cctx, c.AdbPath, "mdns", "services")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	if mdnsDaemonDownOut(string(out)) {
		return nil, ErrMdnsDaemonDown
	}
	return ParseMdnsServices(string(out)), nil
}

// ErrMdnsDaemonDown 表示宿主 mDNS daemon 挂死（`adb mdns services` 输出错误
// 文本且退出码仍为 0，服务列表为空）——与"设备没广播"（正常空列表）无法从
// 列表区分，只能按输出文本判定。app 层用 errors.Is 识别并连续计数自愈。
var ErrMdnsDaemonDown = errors.New("adb mdns daemon down: unknown host service")

// mdnsDaemonDownOut 按输出文本判定宿主 mDNS daemon 挂死。
// 已知文本变体（Windows/macOS/Linux 的 adb mdnsresponder client 报错）：
//
//	"error: unknown host service"    —— 主判据（音墨本机实测，exit code 仍为 0）
//	"error: cannot resolve service"  —— 其他平台/版本的同类报错，一并识别
//
// 只按文本判定：超时（context deadline）与进程失败是瞬态网络/环境问题，
// 不算 daemon 挂，不参与自愈计数。
func mdnsDaemonDownOut(out string) bool {
	low := strings.ToLower(out)
	return strings.Contains(low, "unknown host service") ||
		strings.Contains(low, "cannot resolve service")
}

// RestartServer 重启共享 adb server：kill-server ×2 + start-server。
// gui16 mDNS daemon 自愈：宿主 mDNS daemon 挂死时 kill-server 重启 adb server
// 即恢复。kill ×2 是双保险（第一次 kill 偶发不生效）。只能由 app 层在**没有
// 活动投屏会话**时调用（杀共享 server 会断掉正在进行的投屏——app 层把关）。
// RestartServerFn（测试注入）替换整个执行序列。
func (c *Connector) RestartServer(ctx context.Context) error {
	if c.RestartServerFn != nil {
		return c.RestartServerFn(ctx)
	}
	for i := 0; i < 2; i++ {
		cmd := newAdbCmd(ctx, c.AdbPath, "kill-server")
		_, _ = cmd.Output() // 已挂死的 server kill 也可能报错，忽略——×2 后由 start-server 兜底
	}
	cmd := newAdbCmd(ctx, c.AdbPath, "start-server")
	if _, err := cmd.Output(); err != nil {
		return err
	}
	return nil
}
