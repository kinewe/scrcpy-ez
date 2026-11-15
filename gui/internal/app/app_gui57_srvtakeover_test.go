package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/deviceevents"
)

const (
	srvOwnStaleOut = "error: unknown host service\r\n"
	srvOwn37Out    = "mdns daemon version [adb discovery 0.0.0]\r\n"
)

// srvOwnRec 记录 srvOps fake 的调用与完成信号（并发安全——钩子跑在后台 goroutine）。
type srvOwnRec struct {
	mu           sync.Mutex
	checkN       int
	takeoverN    int
	checkOut     string
	deferForward bool
	takeoverOut  string
	takeoverErr  error
	checked      chan struct{} // checkFn 首次调用时 close（nil=不通知）
	tookover     chan struct{} // takeoverFn 首次调用时 close（nil=不通知）
	onceC        sync.Once
	onceT        sync.Once
}

func (r *srvOwnRec) inject(a *App) {
	a.srvOps.checkFn = func(ctx context.Context) adb.ServerCheck {
		r.mu.Lock()
		r.checkN++
		r.mu.Unlock()
		if r.checked != nil {
			r.onceC.Do(func() { close(r.checked) })
		}
		state := adb.ServerCompatible
		if r.checkOut == srvOwnStaleOut {
			state = adb.ServerIncompatible
		}
		return adb.ServerCheck{State: state, Detail: r.checkOut, ForwardDeferred: r.deferForward}
	}
	a.srvOps.takeoverFn = func(ctx context.Context) (string, error) {
		r.mu.Lock()
		r.takeoverN++
		r.mu.Unlock()
		if r.tookover != nil {
			r.onceT.Do(func() { close(r.tookover) })
		}
		return r.takeoverOut, r.takeoverErr
	}
}

func (r *srvOwnRec) counts() (check, takeover int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.checkN, r.takeoverN
}

// 检测"坏"（≤29 旧版坐庄）→ 抢庄调用一次（同步直调路径，成功静默）。
func TestGui57EnsureServerOwnerTakeoverOnStale(t *testing.T) {
	a, _ := newTestApp()
	rec := &srvOwnRec{checkOut: srvOwnStaleOut, takeoverOut: srvOwn37Out}
	rec.inject(a)
	a.ensureServerOwner(context.Background(), "启动", false)
	if c, k := rec.counts(); c != 1 || k != 1 {
		t.Fatalf("坏环境应检测 1 次 + 抢庄 1 次：check=%d takeover=%d", c, k)
	}
}

// 检测"好"（37 / 31–36 / 空输出）→ 零动作（抢庄不被调用）。
func TestGui57EnsureServerOwnerHealthyNoAction(t *testing.T) {
	for _, out := range []string{srvOwn37Out, "ERROR: mdns daemon unavailable\r\n", ""} {
		a, _ := newTestApp()
		rec := &srvOwnRec{checkOut: out}
		rec.inject(a)
		a.ensureServerOwner(context.Background(), "启动", false)
		if c, k := rec.counts(); c != 1 || k != 0 {
			t.Fatalf("好环境应零动作：out=%q check=%d takeover=%d", out, c, k)
		}
	}
}

// 抢庄失败 → 静默（不 panic、不阻断调用方；检测与抢庄各 1 次）。
func TestGui57EnsureServerOwnerTakeoverFailureSilent(t *testing.T) {
	a, _ := newTestApp()
	rec := &srvOwnRec{checkOut: srvOwnStaleOut, takeoverErr: errors.New("抢庄失败：未能在 5037 上取得 37 坐庄")}
	rec.inject(a)
	a.ensureServerOwner(context.Background(), "配对", false)
	if c, k := rec.counts(); c != 1 || k != 1 {
		t.Fatalf("失败路径应检测 1 次 + 抢庄 1 次：check=%d takeover=%d", c, k)
	}
}

// 测试 App 未挂 srvOps（旧世界零值）→ 零动作、不 panic。
func TestGui57EnsureServerOwnerUnmountedNoAction(t *testing.T) {
	a, _ := newTestApp()
	a.ensureServerOwner(context.Background(), "启动", false) // srvOps 零值：checkFn==nil 直接返回
}

func TestADBServerOwnershipCheckOnTrackReconnect(t *testing.T) {
	for _, tc := range []struct {
		name          string
		output        string
		wantTakeovers int
	}{
		{"compatible server", srvOwn37Out, 0},
		{"old server", srvOwnStaleOut, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestApp()
			rec := &srvOwnRec{checkOut: tc.output, takeoverOut: srvOwn37Out}
			rec.inject(a)
			events := make(chan deviceevents.Snapshot, 7)
			events <- deviceevents.Snapshot{Epoch: 1, Sequence: 1, Available: true}
			events <- deviceevents.Snapshot{Epoch: 1, Sequence: 2, Available: true, Transports: []deviceevents.Transport{{Serial: "usb", State: "device"}}}
			events <- deviceevents.Snapshot{Epoch: 1, Error: "EOF"}
			events <- deviceevents.Snapshot{Epoch: 2, Error: "connection refused"}
			events <- deviceevents.Snapshot{Epoch: 3, Sequence: 1, Available: true}
			events <- deviceevents.Snapshot{Epoch: 3, Sequence: 2, Available: true}
			close(events)
			a.watchServerReconnects(context.Background(), events)
			if checks, takeovers := rec.counts(); checks != 1 || takeovers != tc.wantTakeovers {
				t.Fatalf("after track reconnect: %d checks and %d takeovers, want 1 and %d", checks, takeovers, tc.wantTakeovers)
			}
		})
	}
}

// GUI 启动钩子：异步执行（调用方立即返回），坏环境触发抢庄。
func TestGui57StartupHookAsyncTakeover(t *testing.T) {
	a, _ := newTestApp()
	rec := &srvOwnRec{checkOut: srvOwnStaleOut, takeoverOut: srvOwn37Out, tookover: make(chan struct{})}
	rec.inject(a)
	start := time.Now()
	a.StartServerOwnershipCheck()
	select {
	case <-rec.tookover:
	case <-time.After(3 * time.Second):
		t.Fatal("启动钩子未触发抢庄")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("调用方被阻塞 %v（应异步立即返回）", d)
	}
	if c, k := rec.counts(); c != 1 || k != 1 {
		t.Fatalf("启动钩子应检测 1 次 + 抢庄 1 次：check=%d takeover=%d", c, k)
	}
}

// 启动钩子：环境已完整（37）→ 检测发生、抢庄不触发。
func TestGui57StartupHookHealthyNoTakeover(t *testing.T) {
	a, _ := newTestApp()
	rec := &srvOwnRec{checkOut: srvOwn37Out, checked: make(chan struct{})}
	rec.inject(a)
	a.StartServerOwnershipCheck()
	select {
	case <-rec.checked:
	case <-time.After(3 * time.Second):
		t.Fatal("启动钩子未执行检测")
	}
	// check 已完成：给潜在的误触发留一个小窗口，再断言抢庄未被调用。
	time.Sleep(100 * time.Millisecond)
	if c, k := rec.counts(); c != 1 || k != 0 {
		t.Fatalf("完整环境应零抢庄：check=%d takeover=%d", c, k)
	}
}

// 进配对流程（pairQrOpen 必经）→ 第二道检测触发抢庄（只掀，不带私有兜底）。
func TestGui57PairQrOpenTriggersTakeover(t *testing.T) {
	a, _ := newWirelessApp()
	rec := &srvOwnRec{checkOut: srvOwnStaleOut, takeoverOut: srvOwn37Out, tookover: make(chan struct{})}
	rec.inject(a)
	if err := a.PairConnect("__qr_open__", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-rec.tookover:
	case <-time.After(3 * time.Second):
		t.Fatal("配对打开未触发归属检测+抢庄")
	}
	if c, k := rec.counts(); c != 1 || k != 1 {
		t.Fatalf("配对打开应检测 1 次 + 抢庄 1 次：check=%d takeover=%d", c, k)
	}
}
