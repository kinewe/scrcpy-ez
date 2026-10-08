//go:build windows && cgo

package notifications

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type policyWirelessSource struct {
	base   *ADBSource
	starts atomic.Int32
	active atomic.Int32
	posts  atomic.Int32
	mu     sync.Mutex
	hellos []Frame
}

func (s *policyWirelessSource) Run(ctx context.Context, target Target, emit func(Frame) error) error {
	s.starts.Add(1)
	s.active.Add(1)
	defer s.active.Add(-1)
	return s.base.Run(ctx, target, func(frame Frame) error {
		if frame.Type == "hello" {
			s.mu.Lock()
			s.hellos = append(s.hellos, frame)
			s.mu.Unlock()
		}
		err := emit(frame)
		if frame.Type == "post" {
			s.posts.Add(1)
		}
		return err
	})
}

// Explicitly opt in with the authorized tablet and own-tag probe; never accepts private events.
func TestAuthorizedWirelessNotificationPolicies(t *testing.T) {
	if os.Getenv("EZ_NOTIFICATION_POLICY_WIRELESS_TEST") != "1" {
		t.Skip("requires explicitly authorized wireless tablet")
	}
	adb, serial, physical := os.Getenv("EZ_TEST_ADB"), os.Getenv("EZ_TEST_TABLET"), os.Getenv("EZ_TEST_PHYSICAL")
	server, probe := os.Getenv("EZ_TEST_SERVER"), os.Getenv("EZ_TEST_POLICY_PROBE")
	if adb == "" || serial == "" || physical == "" || server == "" || probe == "" {
		t.Fatal("authorized test paths required")
	}
	command := func(args ...string) *exec.Cmd { return exec.Command(adb, append([]string{"-s", serial}, args...)...) }
	tag := "ez_policy_test_" + strconv.Itoa(os.Getpid())
	prefix := "/data/local/tmp/" + tag
	for suffix, local := range map[string]string{"-probe.jar": probe, "-server.jar": server} {
		remote := prefix + suffix
		if command("shell", "test", "!", "-e", remote).Run() != nil {
			t.Fatal("test path already exists")
		}
		if command("push", local, remote).Run() != nil {
			t.Fatal("probe push failed")
		}
		t.Cleanup(func() {
			if command("shell", "rm", "-f", remote).Run() != nil {
				t.Error("owned device file cleanup failed")
			}
		})
	}
	job := command("shell", "-T", "CLASSPATH="+prefix+"-probe.jar:"+prefix+"-server.jar", "app_process", "/", "lab.PolicyProbe", tag)
	input, err := job.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := job.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	job.Stderr = io.Discard
	if err := job.Start(); err != nil {
		t.Fatal(err)
	}
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			_ = input.Close()
			done := make(chan struct{})
			go func() { _ = job.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(8 * time.Second):
				_ = job.Process.Kill()
				<-done
			}
		})
	}
	defer stop()
	ready := make(chan bool, 1)
	go func() { scanner := bufio.NewScanner(output); ready <- scanner.Scan() && scanner.Text() == "READY" }()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("probe did not initialize")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("probe readiness timeout")
	}
	source := &policyWirelessSource{base: &ADBSource{ADB: adb, Server: server, TestTag: tag}}
	sink := &testSink{}
	manager := NewManager(context.Background(), source, func() (Sink, error) { return sink, nil })
	defer manager.Close()
	identity := "device:" + physical
	target := Target{Identity: identity, DeviceSerial: physical, Serial: serial, Name: "授权无线平板", Connection: "无线", Epoch: 1}
	options := Options{Preview: true, CopyFallback: 10 * time.Minute, Policies: map[string]Policy{identity: {Mode: ModeOTP}}}
	wait := func(label string, condition func() bool) {
		t.Helper()
		until := time.Now().Add(15 * time.Second)
		for !condition() {
			if time.Now().After(until) {
				t.Fatal("timeout: " + label)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	active := func() bool {
		statuses := manager.Status()
		return len(statuses) == 1 && statuses[0].State == "active" && source.active.Load() == 1
	}
	count := func() int { sink.mu.Lock(); defer sink.mu.Unlock(); return len(sink.shown) }
	last := func() Card { sink.mu.Lock(); defer sink.mu.Unlock(); return sink.shown[len(sink.shown)-1] }
	clears := func() int { sink.mu.Lock(); defer sink.mu.Unlock(); return sink.cleared }
	post := func(kind string, id int) {
		t.Helper()
		if _, err := fmt.Fprintf(input, "%s %d\n", kind, id); err != nil {
			t.Fatal("owned publish failed")
		}
	}
	set := func(policy Policy) {
		t.Helper()
		previous := clears()
		options.Policies[identity] = policy
		manager.Reconcile([]Target{target}, options)
		wait("policy clear", func() bool { return clears() > previous })
	}
	manager.Reconcile([]Target{target}, options)
	wait("wireless listener", active)
	post("O", 1)
	post("C", 2)
	wait("OTP allowed", func() bool { return count() >= 1 })
	if count() != 1 || last().CopyCode != "824613" || last().Package != "com.android.shell" {
		t.Fatal("OTP filter leaked ordinary message or lost copy data")
	}
	set(Policy{Mode: ModeAll})
	post("O", 3)
	wait("all mode", func() bool { return count() >= 2 })
	if last().CopyCode != "" {
		t.Fatal("ordinary message became a copy action")
	}
	set(Policy{Mode: ModeWhitelist, Packages: []string{"com.example.not.installed"}})
	beforePosts := source.posts.Load()
	post("O", 4)
	wait("blocked raw event received", func() bool { return source.posts.Load() > beforePosts })
	time.Sleep(300 * time.Millisecond)
	if count() != 2 {
		t.Fatal("whitelist delivered an unselected source")
	}
	set(Policy{Mode: ModeWhitelist, Packages: []string{"com.android.shell"}})
	post("O", 5)
	wait("whitelist allowed", func() bool { return count() >= 3 })
	if count() != 3 || last().Title != "ez policy test #5" {
		t.Fatal("whitelist allowed an unselected source")
	}
	set(Policy{Mode: ModeWhitelist, Other: true, Catalog: []string{"com.example.visible"}})
	post("O", 9)
	wait("complement allowed", func() bool { return count() >= 4 })
	if count() != 4 || last().Title != "ez policy test #9" {
		t.Fatal("complement lost a source outside the visible catalog")
	}
	set(Policy{Mode: ModeWhitelist, Other: true, Catalog: []string{"com.android.shell"}})
	beforePosts = source.posts.Load()
	post("O", 10)
	wait("visible unselected event received", func() bool { return source.posts.Load() > beforePosts })
	time.Sleep(300 * time.Millisecond)
	if count() != 4 {
		t.Fatal("complement bypassed an unselected visible application")
	}
	hidden := false
	set(Policy{Mode: ModeOTP, Preview: &hidden})
	post("O", 6)
	post("C", 7)
	wait("hidden OTP", func() bool { return count() >= 5 })
	if count() != 5 || last().CopyCode != "" || last().Body != "打开手机查看消息内容" {
		t.Fatal("hidden OTP privacy failed")
	}
	if source.starts.Load() != 1 {
		t.Fatal("rule changes restarted the phone listener")
	}
	manager.Reconcile(nil, options)
	wait("off stops listener", func() bool { return source.active.Load() == 0 && len(manager.Status()) == 0 })
	options.Policies[identity] = Policy{Mode: ModeAll}
	manager.Reconcile([]Target{target}, options)
	wait("reenable", active)
	post("O", 8)
	wait("new message after reenable", func() bool { return count() >= 6 })
	if count() != 6 || last().Title != "ez policy test #8" {
		t.Fatal("reenable replayed history")
	}
	manager.Reconcile(nil, options)
	wait("final listener cleanup", func() bool { return source.active.Load() == 0 })
	stop()
	source.mu.Lock()
	hellos := append([]Frame(nil), source.hellos...)
	source.mu.Unlock()
	for _, hello := range hellos {
		remote := "/data/local/tmp/scrcpy-ez-notification-" + hello.Session + ".jar"
		if command("shell", "test", "-e", remote).Run() == nil {
			t.Fatal("owned listener JAR remains")
		}
		if command("shell", "test", "-e", "/proc/"+strconv.Itoa(hello.PID)).Run() == nil {
			t.Fatal("owned listener process remains")
		}
	}
	t.Logf("WIRELESS_POLICY_PASS: own synthetic messages only; OTP/all/whitelist/complement/hidden/off/reenable; starts=%d; history quiet; listener cleanup passed; no clipboard writes", source.starts.Load())
}

// Exercise the production baseline path but discard every record in memory.
// Only protocol readiness is asserted; no sink, private logging, or clipboard is involved.
func TestAuthorizedWirelessProductionReadiness(t *testing.T) {
	if os.Getenv("EZ_NOTIFICATION_POLICY_WIRELESS_TEST") != "1" {
		t.Skip("requires explicitly authorized wireless tablet")
	}
	adb, serial, physical, server := os.Getenv("EZ_TEST_ADB"), os.Getenv("EZ_TEST_TABLET"), os.Getenv("EZ_TEST_PHYSICAL"), os.Getenv("EZ_TEST_SERVER")
	if adb == "" || serial == "" || physical == "" || server == "" {
		t.Fatal("authorized test paths required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	ready := false
	err := (&ADBSource{ADB: adb, Server: server}).Run(ctx, Target{DeviceSerial: physical, Serial: serial}, func(frame Frame) error {
		if frame.Type == "ready" {
			ready = true
			cancel()
		}
		return nil
	})
	if !ready {
		t.Fatalf("production readiness failed: %s", sourceStatus(err))
	}
	t.Log("PRODUCTION_READY_PASS: unfiltered baseline accepted; all records discarded; no Windows notification or clipboard writes")
}
