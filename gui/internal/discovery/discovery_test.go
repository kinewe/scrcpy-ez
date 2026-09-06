package discovery

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

// 修复"关窗两次弹框"：discovery 的 connect/mdns exec 都必须隐藏控制台
// （adb.exe 是 console 程序，GUI 无控制台直接启动会弹窗）。
// Linux 下 HideConsole 为空实现；Windows 下断言 SysProcAttr 已设置，
// 真实弹窗行为由 Windows 侧编译（build_win）+ 实机验收覆盖。
func TestNewAdbCmdHidesConsole(t *testing.T) {
	cmd := newAdbCmd(context.Background(), "adb.exe", "connect", "192.168.31.162:5555")
	if cmd == nil || len(cmd.Args) != 3 || cmd.Args[1] != "connect" || cmd.Args[2] != "192.168.31.162:5555" {
		t.Fatalf("命令构造错误: %+v", cmd)
	}
	if runtime.GOOS == "windows" && cmd.SysProcAttr == nil {
		t.Fatal("Windows 下 adb 子进程必须设置 SysProcAttr（CREATE_NO_WINDOW 防弹窗）")
	}
	cmd2 := newAdbCmd(context.Background(), "adb.exe", "mdns", "services")
	if cmd2 == nil || len(cmd2.Args) != 3 || cmd2.Args[1] != "mdns" || cmd2.Args[2] != "services" {
		t.Fatalf("mdns 命令构造错误: %+v", cmd2)
	}
	if runtime.GOOS == "windows" && cmd2.SysProcAttr == nil {
		t.Fatal("Windows 下 mdns 子进程必须设置 SysProcAttr（CREATE_NO_WINDOW 防弹窗）")
	}
}

func TestParseMdnsServices(t *testing.T) {
	out := `List of discovered mdns services
_adb-tls-connect._tcp	R58T00WA0YM	192.168.31.162:5555
_adb-tls-pairing._tcp	R58T00WA0YM	local
_adb-tls-connect._tcp	24117RK2CC._adb-tls._tcp	local
_http._tcp	some-web-service	10.0.0.1:80

`
	svcs := ParseMdnsServices(out)
	if len(svcs) != 3 {
		t.Fatalf("应解析 3 条 _adb 服务（_http 忽略）: %+v", svcs)
	}
	if svcs[0].Type != "_adb-tls-connect._tcp" || svcs[0].Name != "R58T00WA0YM" ||
		svcs[0].Addr != "192.168.31.162:5555" {
		t.Fatalf("带地址行解析错误: %+v", svcs[0])
	}
	if svcs[1].Name != "R58T00WA0YM" || svcs[1].Addr != "" {
		t.Fatalf("local（未解析）行应为空地址: %+v", svcs[1])
	}
	// 实例名带 _adb-tls 后缀：名字保留原样（供 serial 匹配），地址为空
	if svcs[2].Name != "24117RK2CC._adb-tls._tcp" || svcs[2].Addr != "" {
		t.Fatalf("实例名后缀行解析错误: %+v", svcs[2])
	}
}

func TestParseMdnsServicesSpaceSeparated(t *testing.T) {
	// 兼容空格分隔输出（非 tab）
	out := "List of discovered mdns services\n" +
		"_adb-tls-connect._tcp a743e1df 192.168.1.5:5555\n" +
		"_adb-tls-pairing._tcp a743e1df\n"
	svcs := ParseMdnsServices(out)
	if len(svcs) != 2 {
		t.Fatalf("空格分隔解析数量错误: %+v", svcs)
	}
	if svcs[0].Addr != "192.168.1.5:5555" || svcs[0].Name != "a743e1df" {
		t.Fatalf("空格分隔解析错误: %+v", svcs[0])
	}
	if svcs[1].Addr != "" {
		t.Fatalf("两列行地址应为空: %+v", svcs[1])
	}
}

func TestParseMdnsServicesEmpty(t *testing.T) {
	if got := ParseMdnsServices(""); got != nil {
		t.Fatalf("空输出应为空: %+v", got)
	}
	if got := ParseMdnsServices("List of discovered mdns services\n"); got != nil {
		t.Fatalf("仅表头应为空: %+v", got)
	}
}

// gui20：adb 37.0.0 `adb mdns services` 输出列序为
// [实例名, 类型, 地址]（老 adb 是 [类型, 名称, 地址]）——
// 修复前 fields[0]=实例名 不匹配 _adb → 整行丢弃，mDNS 快照恒空。
func TestParseMdnsServicesAdb37Format(t *testing.T) {
	out := `List of discovered mdns services
adb-601c9f08-KWqpio	_adb-tls-connect._tcp	192.168.31.197:45005
adb-601c9f08	_adb._tcp	192.168.31.197:5555
`
	svcs := ParseMdnsServices(out)
	if len(svcs) != 2 {
		t.Fatalf("adb 37 新格式应解析 2 条: %+v", svcs)
	}
	if svcs[0].Type != "_adb-tls-connect._tcp" || svcs[0].Name != "adb-601c9f08-KWqpio" ||
		svcs[0].Mode != MdnsModeTls || svcs[0].Addr != "192.168.31.197:45005" {
		t.Fatalf("adb 37 tls 行解析错误: %+v", svcs[0])
	}
	if svcs[1].Type != "_adb._tcp" || svcs[1].Name != "adb-601c9f08" ||
		svcs[1].Mode != MdnsModeTcpip || svcs[1].Addr != "192.168.31.197:5555" {
		t.Fatalf("adb 37 tcpip 行解析错误: %+v", svcs[1])
	}
}

// gui20：真实 adb 输出多行整体 parse——新老两种格式混排（含表头/空行/
// local 未解析/_http 无关行/两列都无法识别的未知行）只收 _adb 服务。
func TestParseMdnsServicesMixedFormats(t *testing.T) {
	out := `List of discovered mdns services
adb-601c9f08-KWqpio	_adb-tls-connect._tcp	192.168.31.197:45005
adb-601c9f08	_adb._tcp	192.168.31.197:5555
_adb-tls-connect._tcp	R58T00WA0YM	192.168.31.162:5555
adb-601c9f08-NewSuf6	_adb-tls-pairing._tcp	local
_http._tcp	some-web-service	10.0.0.1:80

unknown-line	not-a-type	1.2.3.4:80
`
	svcs := ParseMdnsServices(out)
	if len(svcs) != 4 {
		t.Fatalf("混排应解析 4 条 _adb 服务: %+v", svcs)
	}
	want := []MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-601c9f08-KWqpio", Addr: "192.168.31.197:45005", Mode: MdnsModeTls},
		{Type: "_adb._tcp", Name: "adb-601c9f08", Addr: "192.168.31.197:5555", Mode: MdnsModeTcpip},
		{Type: "_adb-tls-connect._tcp", Name: "R58T00WA0YM", Addr: "192.168.31.162:5555", Mode: MdnsModeTls},
		{Type: "_adb-tls-pairing._tcp", Name: "adb-601c9f08-NewSuf6", Mode: MdnsModePairing},
	}
	for i, w := range want {
		if svcs[i] != w {
			t.Fatalf("服务 %d 解析错误: got %+v want %+v", i, svcs[i], w)
		}
	}
}

// 并行 connect：多地址并发探测，6s 窗口取首个成功（假 connect 注入）。
func TestConnectAnyFirstSuccess(t *testing.T) {
	c := New("adb")
	var mu sync.Mutex
	var calls []string
	c.ConnectFn = func(ctx context.Context, addr string) error {
		mu.Lock()
		calls = append(calls, addr)
		mu.Unlock()
		// 第三个地址"最慢"但唯一成功；其余失败
		if addr == "192.168.31.99:5555" {
			select {
			case <-time.After(150 * time.Millisecond):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		select {
		case <-time.After(10 * time.Second):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	start := time.Now()
	addr, ok := c.ConnectAny(context.Background(),
		[]string{"192.168.31.1:5555", "192.168.31.2:5555", "192.168.31.99:5555"},
		3*time.Second, 6*time.Second)
	elapsed := time.Since(start)
	if !ok || addr != "192.168.31.99:5555" {
		t.Fatalf("应返回首个成功地址: addr=%q ok=%v", addr, ok)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("并行探测耗时异常（应 ~150ms 而非串行阻塞）: %v", elapsed)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 3 {
		t.Fatalf("全部地址都应被尝试: %v", calls)
	}
}

// 全部失败 → ok=false（"未找到"状态来源）。
func TestConnectAnyAllFail(t *testing.T) {
	c := New("adb")
	c.ConnectFn = func(ctx context.Context, addr string) error {
		select {
		case <-time.After(10 * time.Second):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	addr, ok := c.ConnectAny(context.Background(),
		[]string{"192.168.31.1:5555", "192.168.31.2:5555"},
		500*time.Millisecond, 2*time.Second)
	if ok || addr != "" {
		t.Fatalf("全失败应返回 false: addr=%q ok=%v", addr, ok)
	}
}

// 窗口超时：成功晚于窗口 → 视为未找到（不阻塞 UI）。
func TestConnectAnyWindowCutoff(t *testing.T) {
	c := New("adb")
	c.ConnectFn = func(ctx context.Context, addr string) error {
		select {
		case <-time.After(800 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	addr, ok := c.ConnectAny(context.Background(),
		[]string{"192.168.31.1:5555"}, 2*time.Second, 300*time.Millisecond)
	if ok || addr != "" {
		t.Fatalf("窗口截止后应返回未找到: addr=%q ok=%v", addr, ok)
	}
}

// 空地址列表：直接未找到，不发 goroutine。
func TestConnectAnyEmpty(t *testing.T) {
	c := New("adb")
	c.ConnectFn = func(ctx context.Context, addr string) error {
		t.Fatal("空列表不应发起 connect")
		return nil
	}
	if addr, ok := c.ConnectAny(context.Background(), nil, time.Second, time.Second); ok || addr != "" {
		t.Fatalf("空列表应返回 false: addr=%q ok=%v", addr, ok)
	}
}

// --- TLS 优先探测（gui12） ---

// 服务类型 → 形态判定：tls 连接/tls 配对/经典 tcpip。
func TestParseMdnsServicesModes(t *testing.T) {
	out := `List of discovered mdns services
_adb-tls-connect._tcp	adb-R58T00WA0YM-Ab12Cd	192.168.31.162:33895
_adb-tls-pairing._tcp	adb-R58T00WA0YM-Ab12Cd	192.168.31.162:37033
_adb._tcp	adb-R58T00WA0YM	192.168.31.162:5555
`
	svcs := ParseMdnsServices(out)
	if len(svcs) != 3 {
		t.Fatalf("应解析 3 条: %+v", svcs)
	}
	want := []string{MdnsModeTls, MdnsModePairing, MdnsModeTcpip}
	for i, s := range svcs {
		if s.Mode != want[i] {
			t.Fatalf("服务 %d 形态错误: %+v", i, s)
		}
	}
}

// ConnectOut 按输出判成败：adb connect 失败也退出 0（"cannot connect to ..."），
// 无 "connected to" 标记的输出必须判失败（TLS 优先回退依赖成败判定）。
func TestConnectOutRequiresSuccessOutput(t *testing.T) {
	c := New("adb")
	c.ConnectOutFn = func(ctx context.Context, addr string) (string, error) {
		if addr == "192.168.31.1:5555" {
			return "cannot connect to 192.168.31.1:5555: Connection refused", nil
		}
		if addr == "192.168.31.3:5555" {
			return "already connected to 192.168.31.3:5555", nil
		}
		return "connected to " + addr, nil
	}
	if _, err := c.ConnectOut(context.Background(), "192.168.31.1:5555"); err == nil {
		t.Fatal("拒绝连接但退出码 0 的输出应判失败")
	}
	if _, err := c.ConnectOut(context.Background(), "192.168.31.2:5555"); err != nil {
		t.Fatalf("connected to 输出应判成功: %v", err)
	}
	if _, err := c.ConnectOut(context.Background(), "192.168.31.3:5555"); err != nil {
		t.Fatalf("already connected 语义（含 connected to）应判成功: %v", err)
	}
}

// ConnectTiers：tls 层成功 → 直接返回，tcpip 层不尝试（TLS 优先，不并行抢 5555）。
func TestConnectTiersTlsFirst(t *testing.T) {
	c := New("adb")
	var mu sync.Mutex
	var calls []string
	c.ConnectFn = func(ctx context.Context, addr string) error {
		mu.Lock()
		calls = append(calls, addr)
		mu.Unlock()
		if addr == "192.168.31.2:5555" {
			t.Error("tls 层成功后不应再试 tcpip 层")
		}
		return nil
	}
	addr, ok := c.ConnectTiers(context.Background(),
		[][]string{{"192.168.31.1:33895"}, {"192.168.31.2:5555"}},
		time.Second, 3*time.Second)
	if !ok || addr != "192.168.31.1:33895" {
		t.Fatalf("应返回 tls 层首个成功地址: addr=%q ok=%v", addr, ok)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || calls[0] != "192.168.31.1:33895" {
		t.Fatalf("tls 层成功不应尝试 tcpip 层: %v", calls)
	}
}

// ConnectTiers：tls 层失败 → 回退 tcpip 层成功（层间串行，顺序可观测）。
func TestConnectTiersFallbackToTcpip(t *testing.T) {
	c := New("adb")
	var mu sync.Mutex
	var calls []string
	c.ConnectFn = func(ctx context.Context, addr string) error {
		mu.Lock()
		calls = append(calls, addr)
		mu.Unlock()
		if addr == "192.168.31.1:33895" {
			return context.DeadlineExceeded
		}
		return nil
	}
	addr, ok := c.ConnectTiers(context.Background(),
		[][]string{{"192.168.31.1:33895"}, {"192.168.31.2:5555"}},
		time.Second, 3*time.Second)
	if !ok || addr != "192.168.31.2:5555" {
		t.Fatalf("tls 失败应回退 5555: addr=%q ok=%v", addr, ok)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 || calls[0] != "192.168.31.1:33895" || calls[1] != "192.168.31.2:5555" {
		t.Fatalf("应先试 tls 再回退 5555: %v", calls)
	}
}

// ConnectTiers：全层失败 → ok=false；空层跳过。
func TestConnectTiersAllFail(t *testing.T) {
	c := New("adb")
	c.ConnectFn = func(ctx context.Context, addr string) error { return context.DeadlineExceeded }
	addr, ok := c.ConnectTiers(context.Background(),
		[][]string{nil, {"192.168.31.1:33895"}, {}},
		500*time.Millisecond, 2*time.Second)
	if ok || addr != "" {
		t.Fatalf("全失败应返回 false: addr=%q ok=%v", addr, ok)
	}
}
