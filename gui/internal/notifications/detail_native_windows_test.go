//go:build windows && cgo

package notifications

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"scrcpy-ez/gui/internal/adb"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Opt-in laboratory: reads only its generated shell notification tag and
// targets only a newly created display. Never activates a user's notification.
func TestNativeNotificationDetailEndToEnd(t *testing.T) {
	runtime := os.Getenv("SCEZ_DETAIL_RUNTIME")
	fixture := os.Getenv("SCEZ_DETAIL_FIXTURE")
	serial := os.Getenv("SCEZ_DETAIL_SERIAL")
	if runtime == "" || fixture == "" || serial == "" {
		t.Skip("opt-in physical tablet / native COM detail test")
	}
	expected := os.Getenv("SCEZ_DETAIL_DEVICE_ID")
	if expected == "" {
		t.Fatal("the laboratory requires an explicitly authorized physical device identity")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	command := func(args ...string) *exec.Cmd {
		c := exec.CommandContext(ctx, filepath.Join(runtime, "adb.exe"), append([]string{"-s", serial}, args...)...)
		adb.HideConsole(c)
		return c
	}
	run := func(args ...string) string {
		t.Helper()
		out, err := command(args...).CombinedOutput()
		if err != nil {
			t.Fatalf("lab ADB failed (%s): %v", args[0], err)
		}
		return strings.TrimSpace(string(out))
	}
	if run("shell", "getprop", "ro.serialno") != expected {
		t.Fatal("unexpected physical tablet")
	}
	tag := "scez_detail_" + strconv.FormatInt(time.Now().UnixNano(), 16)
	prefix := "/data/local/tmp/" + tag
	run("push", fixture, prefix+".jar")
	run("push", filepath.Join(runtime, "scrcpy-server"), prefix+"-server.jar")
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		c := exec.CommandContext(cleanup, filepath.Join(runtime, "adb.exe"), "-s", serial, "shell", "rm", "-f", prefix+".jar", prefix+"-server.jar")
		adb.HideConsole(c)
		_ = c.Run()
	}()
	client := exec.CommandContext(ctx, filepath.Join(runtime, "scrcpy.exe"), "-s", serial, "--new-display=1280x854/180", "--no-vd-system-decorations", "--no-vd-destroy-content", "--no-audio", "--no-clipboard-sync", "--no-clipboard-autosync", "--no-clipboard-push-on-start", "--time-limit=65", "--window-title=ez synthetic notification detail", "-V", "debug")
	client.Env = append(os.Environ(), "SCRCPY_SERVER_PATH="+filepath.Join(runtime, "scrcpy-server"))
	adb.HideConsole(client)
	clientOut, err := client.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	client.Stderr = client.Stdout
	if err = client.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		close := exec.Command("taskkill.exe", "/PID", strconv.Itoa(client.Process.Pid))
		adb.HideConsole(close)
		_ = close.Run()
		ended := make(chan error, 1)
		go func() { ended <- client.Wait() }()
		select {
		case <-ended:
		case <-time.After(5 * time.Second):
			_ = client.Process.Kill()
			<-ended
		}
	}()
	displays := make(chan int, 1)
	ready := make(chan struct{}, 1)
	go func() {
		scanner := bufio.NewScanner(clientOut)
		pattern := regexp.MustCompile(`New display: .*\(id=([0-9]+)\)`)
		for scanner.Scan() {
			line := scanner.Text()
			if m := pattern.FindStringSubmatch(line); len(m) == 2 {
				id, _ := strconv.Atoi(m[1])
				select {
				case displays <- id:
				default:
				}
			}
			if strings.Contains(line, "SCRCPY_EZ_READY") {
				select {
				case ready <- struct{}{}:
				default:
				}
			}
		}
	}()
	var display int
	select {
	case display = <-displays:
	case <-ctx.Done():
		t.Fatal("display did not initialize")
	}
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("client did not initialize")
	}
	t.Logf("owned virtual display: %d", display)
	source := &ADBSource{ADB: filepath.Join(runtime, "adb.exe"), Server: filepath.Join(runtime, "scrcpy-server"), TestTag: tag}
	sink, _ := testNativeIdentity(t)
	state := NewState("lab-pad", "合成通知测试", true)
	opened := make(chan error, 4)
	var openCount atomic.Int32
	state.opener = func(openCtx context.Context, request OpenRequest) error {
		openCount.Add(1)
		err := source.Open(openCtx, request, display)
		opened <- err
		return err
	}
	cards := make(chan Record, 8)
	listenerReady := make(chan struct{}, 1)
	sourceCtx, stopSource := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- source.Run(sourceCtx, Target{Identity: "lab-pad", Serial: serial, DeviceSerial: expected}, func(frame Frame) error {
			if err := state.Apply(frame, sink); err != nil {
				return err
			}
			if frame.Type == "ready" {
				listenerReady <- struct{}{}
			}
			if frame.Type == "post" {
				cards <- frame.Record
			}
			return nil
		})
	}()
	defer func() {
		stopSource()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("listener cleanup timeout")
		}
	}()
	select {
	case <-listenerReady:
	case err := <-done:
		t.Fatalf("listener failed: %v", err)
	case <-ctx.Done():
		t.Fatal("listener readiness timeout")
	}
	helper := command("shell", "-T", "CLASSPATH="+prefix+".jar:"+prefix+"-server.jar", "app_process", "/", "lab.DetailNotificationFixture", tag)
	helperOut, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	helper.Stderr = io.Discard
	input, err := helper.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = helper.Start(); err != nil {
		t.Fatal(err)
	}
	helperReady := make(chan struct{}, 1)
	go func() {
		scanner := bufio.NewScanner(helperOut)
		for scanner.Scan() {
			if scanner.Text() == "FIXTURE_READY" {
				helperReady <- struct{}{}
			}
		}
	}()
	defer func() {
		_ = input.Close()
		waited := make(chan error, 1)
		go func() { waited <- helper.Wait() }()
		select {
		case <-waited:
		case <-time.After(4 * time.Second):
			_ = helper.Process.Kill()
		}
	}()
	select {
	case <-helperReady:
	case <-ctx.Done():
		t.Fatal("fixture readiness timeout")
	}
	var copied atomic.Int32
	var copyMu sync.Mutex
	var copiedText string
	if err := sink.call(nativeRequest{op: "copyWriter", copier: func(code string, _ uint32) error {
		copyMu.Lock()
		copiedText = code
		copyMu.Unlock()
		copied.Add(1)
		return nil
	}}).err; err != nil {
		t.Fatal(err)
	}
	settingsPID := ""
	for id := 1; id <= 3; id++ {
		fmt.Fprintln(input, id)
		var record Record
		select {
		case record = <-cards:
		case <-ctx.Done():
			t.Fatal("synthetic notification not delivered")
		}
		group, notificationTag := ShortID("lab-pad"), ShortID(record.Key)
		if id == 3 {
			action := sink.call(nativeRequest{op: "copyToken", group: group, tag: notificationTag}).token
			if action == "" || sink.call(nativeRequest{op: "openToken", group: group, tag: notificationTag}).token != "" {
				t.Fatal("OTP did not stay copy-only")
			}
			if err := sink.call(nativeRequest{op: "activate", value: action}).err; err != nil {
				t.Fatal(err)
			}
			until := time.Now().Add(3 * time.Second)
			for copied.Load() == 0 && time.Now().Before(until) {
				time.Sleep(20 * time.Millisecond)
			}
			copyMu.Lock()
			text := copiedText
			copyMu.Unlock()
			if text != "123456" || openCount.Load() != 2 {
				t.Fatal("OTP native click routed to detail")
			}
			continue
		}
		action := sink.call(nativeRequest{op: "openToken", group: group, tag: notificationTag}).token
		t.Logf("original activity destination: %s", record.OpenPackage)
		if action == "" {
			t.Fatal("ordinary notification missing native action")
		}
		if err := sink.call(nativeRequest{op: "openActivate", value: action}).err; err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-opened:
			if err != nil {
				activity := run("shell", "dumpsys", "activity", "activities")
				for _, line := range strings.Split(activity, "\n") {
					if strings.Contains(line, "Display #") || strings.Contains(line, "Resumed:") {
						t.Log(strings.TrimSpace(line))
					}
				}
				t.Fatalf("original action rejected: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("native detail activation timeout")
		}
		landed := false
		for attempt := 0; attempt < 30; attempt++ {
			activity := run("shell", "dumpsys", "activity", "activities")
			parts := strings.SplitN(activity, "Display #"+strconv.Itoa(display)+" ", 2)
			if len(parts) == 2 {
				block := strings.SplitN(parts[1], "\nDisplay #", 2)[0]
				if strings.Contains(block, "topResumedActivity=") && strings.Contains(block, "ApplicationsDetailsActivity") {
					landed = true
					break
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !landed {
			t.Fatal("detail did not land on owned virtual display")
		}
		currentPID := run("shell", "pidof", "com.android.settings")
		if id == 1 {
			settingsPID = currentPID
		} else if settingsPID == "" || currentPID != settingsPID {
			t.Fatal("detail click replaced the settings process")
		}
	}
	if err := sink.Clear(ShortID("lab-pad")); err != nil {
		t.Fatal(err)
	}
	t.Logf("native COM → original immutable PendingIntent → detail on owned display %d; repeated click preserves app process; OTP copies only", display)
}
