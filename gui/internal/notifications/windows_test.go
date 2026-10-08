//go:build windows && cgo

package notifications

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

func testNativeSink(t *testing.T) *WindowsSink {
	sink, _ := testNativeIdentity(t)
	return sink
}

func TestWindowsSenderIconUsesEmbeddedProductArtwork(t *testing.T) {
	dir := t.TempDir()
	path := windowsSenderIcon(dir)
	if path == "" || !strings.HasPrefix(path, dir+string(os.PathSeparator)) {
		t.Fatal("embedded product artwork not cached")
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, senderIconPNG) || !validIcon(IconFromPNG(data)) {
		t.Fatal("registered image differs from the product icon")
	}
	info, _ := os.Stat(path)
	if windowsSenderIcon(dir) != path {
		t.Fatal("sender image location changed on restart")
	}
	after, _ := os.Stat(path)
	if !info.ModTime().Equal(after.ModTime()) {
		t.Fatal("unchanged image rewritten on restart")
	}
	if err := os.WriteFile(path, []byte("invalid image"), 0600); err != nil {
		t.Fatal(err)
	}
	if windowsSenderIcon(dir) != path {
		t.Fatal("corrupt cached artwork not repaired")
	}
	data, _ = os.ReadFile(path)
	if !bytes.Equal(data, senderIconPNG) {
		t.Fatal("corrupt cached artwork registered")
	}
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if windowsSenderIcon(blocked) != "" || windowsSenderIcon("") != "" {
		t.Fatal("unavailable cache must use the shortcut's executable icon")
	}
}

func testNativeIdentity(t *testing.T) (*WindowsSink, string) {
	t.Helper()
	id := fmt.Sprintf("ScrcpyEZ.NotificationTest.%d.%d", os.Getpid(), time.Now().UnixNano())
	sink, err := newWindowsSink(id, "音墨通知功能测试")
	base, _ := os.UserConfigDir()
	t.Cleanup(func() {
		if sink != nil {
			_ = sink.Close()
		}
		_ = os.Remove(filepath.Join(base, "Microsoft", "Windows", "Start Menu", "Programs", id+".lnk"))
		for _, path := range []string{`Software\Classes\AppUserModelId\` + id, `Software\Microsoft\Windows\CurrentVersion\Notifications\Settings\` + id} {
			if err := registry.DeleteKey(registry.CURRENT_USER, path); err != nil && err != registry.ErrNotExist {
				t.Errorf("test identity cleanup failed: %v", err)
			}
		}
		for _, path := range []string{`Software\Classes\CLSID\` + activationCLSID(id) + `\LocalServer32`, `Software\Classes\CLSID\` + activationCLSID(id)} {
			if err := registry.DeleteKey(registry.CURRENT_USER, path); err != nil && err != registry.ErrNotExist {
				t.Errorf("activation test registration cleanup failed: %v", err)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return sink, id
}

func TestNativeCopyActivationAndMetadata(t *testing.T) {
	if os.Getenv("SCEZ_NOTIFICATION_NATIVE_TEST") != "1" {
		t.Skip("opt-in native COM activation test")
	}
	sink, appID := testNativeIdentity(t)
	if result := sink.call(nativeRequest{op: "count"}); result.err != nil || result.count != 0 {
		t.Fatal("registration must not leave a visible initialization card")
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\AppUserModelId\`+appID, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	activator, _, err := key.GetStringValue("CustomActivator")
	_ = key.Close()
	if err != nil || activator != activationCLSID(appID) {
		t.Fatal("COM notification identity missing")
	}
	base, _ := os.UserConfigDir()
	if _, err := os.Stat(filepath.Join(base, "Microsoft", "Windows", "Start Menu", "Programs", appID+".lnk")); err != nil {
		t.Fatal("dedicated notification shortcut missing")
	}
	if !sink.copyAvailable {
		t.Fatal("native copy activation is not available")
	}
	copied := make(chan string, 4)
	if err := sink.call(nativeRequest{op: "copyWriter", copier: func(code string, _ uint32) error { copied <- code; return nil }}).err; err != nil {
		t.Fatal(err)
	}
	card := Card{Group: "synthetic", Tag: "copy", Device: "我的平板", Connection: "USB · SYNTHETIC_SERIAL", App: "合成邮件", Title: "验证码", Body: "合成消息", CopyCode: "850329", Icon: syntheticAppIcon(t), Silent: true}
	if err := sink.Show(card); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"我的平板", "USB · SYNTHETIC_SERIAL", "合成邮件", `<image src=`, `arguments="copy:`, `launch="copy:`} {
		await(t, func() bool {
			result := sink.call(nativeRequest{op: "contains", value: text})
			return result.err == nil && result.count == 1
		})
	}
	token := sink.call(nativeRequest{op: "copyToken", group: card.Group, tag: card.Tag}).token
	if len(token) != 32 {
		t.Fatal("opaque copy token missing")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	activationCtx, stopActivation := context.WithTimeout(context.Background(), 12*time.Second)
	defer stopActivation()
	activation := exec.CommandContext(activationCtx, exe, "-test.run=^TestNativeExternalActivationChild$", "-test.timeout=10s")
	activation.Env = append(os.Environ(), "SCEZ_ACTIVATION_TEST_ID="+appID, "SCEZ_ACTIVATION_TEST_TOKEN="+token)
	if output, err := activation.CombinedOutput(); err != nil {
		t.Fatalf("separate-process COM activation: %v\n%s", err, output)
	}
	select {
	case got := <-copied:
		if got != card.CopyCode {
			t.Fatal("wrong synthetic code")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("COM activation did not reach copy writer")
	}
	_ = sink.call(nativeRequest{op: "activate", value: token})
	select {
	case <-copied:
		t.Fatal("duplicate activation wrote clipboard twice")
	case <-time.After(100 * time.Millisecond):
	}
	if err := sink.Show(card); err != nil {
		t.Fatal(err)
	}
	stale := sink.call(nativeRequest{op: "copyToken", group: card.Group, tag: card.Tag}).token
	if err := sink.Remove(card.Group, card.Tag); err != nil {
		t.Fatal(err)
	}
	_ = sink.call(nativeRequest{op: "activate", value: stale})
	select {
	case <-copied:
		t.Fatal("removed notification copied stale code")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestNativeExternalActivationChild(t *testing.T) {
	id, token := os.Getenv("SCEZ_ACTIVATION_TEST_ID"), os.Getenv("SCEZ_ACTIVATION_TEST_TOKEN")
	if id == "" {
		t.Skip("separate-process synthetic activation helper")
	}
	if !strings.HasPrefix(id, "ScrcpyEZ.NotificationTest.") || len(token) != 32 {
		t.Fatal("invalid test activation identity")
	}
	if err := nativeExternalActivation(id, token); err != nil {
		t.Fatal(err)
	}
}

func TestNativeSenderRegistrationReuse(t *testing.T) {
	if os.Getenv("SCEZ_NOTIFICATION_NATIVE_TEST") != "1" {
		t.Skip("opt-in native registration reuse test")
	}
	started := time.Now()
	sink, appID := testNativeIdentity(t)
	cold := time.Since(started)
	base, _ := os.UserConfigDir()
	path := filepath.Join(base, "Microsoft", "Windows", "Start Menu", "Programs", appID+".lnk")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = sink.Close()
	started = time.Now()
	reopened, err := newWindowsSink(appID, "音墨通知功能测试")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	warm := time.Since(started)
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("valid sender shortcut rewritten on restart")
	}
	if failures := reopened.call(nativeRequest{op: "failures"}); failures.err != nil || failures.count != 0 {
		t.Fatal("new sender has unexpected failure metadata")
	}
	t.Logf("sender registration: cold=%s; reuse=%s; valid shortcut unchanged", cold, warm)
}

func TestNativeClipboardRoundtripPreservesUserFormats(t *testing.T) {
	if os.Getenv("SCEZ_NOTIFICATION_CLIPBOARD_TEST") != "1" {
		t.Skip("opt-in bounded clipboard preservation test")
	}
	sink := testNativeSink(t)
	result := sink.call(nativeRequest{op: "clipboardRoundtrip"})
	if result.err != nil {
		t.Fatal(result.err)
	}
	if result.count == 0 {
		t.Skip("clipboard contains unknown/object formats; left untouched")
	}
	t.Log("native Unicode copy verified; original supported clipboard formats restored without logging contents")
}

func TestWindowsNativeToastLifecycle(t *testing.T) {
	if os.Getenv("SCEZ_NOTIFICATION_NATIVE_TEST") != "1" {
		t.Skip("opt-in native integration test")
	}
	sink := testNativeSink(t)
	card := Card{Group: "synthetic", Tag: "own-test", App: "Synthetic", Device: "K80", Title: "音墨通知测试", Body: "仅合成内容 762184，无账号", Silent: true}
	if err := sink.Show(card); err != nil {
		t.Fatal(err)
	}
	await(t, func() bool {
		result := sink.call(nativeRequest{op: "count"})
		return result.err == nil && result.count == 1
	})
	card.Body = "更新合成内容 A7B9C2"
	if err := sink.Show(card); err != nil {
		t.Fatal(err)
	}
	await(t, func() bool { return sink.call(nativeRequest{op: "count"}).count == 1 })
	if err := sink.Remove(card.Group, card.Tag); err != nil {
		t.Fatal(err)
	}
	await(t, func() bool { return sink.call(nativeRequest{op: "count"}).count == 0 })
	if err := sink.Show(card); err != nil {
		t.Fatal(err)
	}
	if err := sink.Clear(card.Group); err != nil {
		t.Fatal(err)
	}
	await(t, func() bool { return sink.call(nativeRequest{op: "count"}).count == 0 })
}

func TestNativeSenderRepairsMovedExecutable(t *testing.T) {
	if os.Getenv("SCEZ_NOTIFICATION_NATIVE_TEST") != "1" {
		t.Skip("opt-in isolated relocation test")
	}
	first, appID := testNativeIdentity(t)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	current, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "moved-sender.exe")
	if err := os.WriteFile(moved, data, 0700); err != nil {
		t.Fatal(err)
	}
	sink, err := newWindowsSinkWithExecutable(appID, "音墨通知功能测试", "", "", moved)
	if err != nil {
		t.Fatal("repairing a shortcut after executable relocation:", err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	base, _ := os.UserConfigDir()
	link := filepath.Join(base, "Microsoft", "Windows", "Start Menu", "Programs", appID+".lnk")
	script := filepath.Join(t.TempDir(), "check-sender-icon.ps1")
	if err := os.WriteFile(script, []byte(`param([string]$Link,[string]$Executable,[switch]$ClearIcon)
$ErrorActionPreference='Stop'
$shortcut=(New-Object -ComObject WScript.Shell).CreateShortcut($Link)
if ($ClearIcon) { $shortcut.IconLocation=($Executable+',1'); $shortcut.Save(); exit 0 }
if ($shortcut.TargetPath -ne $Executable) { Write-Output 'shortcut target mismatch'; exit 1 }
if ($shortcut.IconLocation -notmatch '^(.*),\s*0$' -or $Matches[1].Trim('"') -ne $Executable) { Write-Output ('icon mismatch: '+$shortcut.IconLocation); exit 1 }
`), 0600); err != nil {
		t.Fatal(err)
	}
	checkIcon := func(clear bool) {
		t.Helper()
		args := []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script, "-Link", link, "-Executable", moved}
		if clear {
			args = append(args, "-ClearIcon")
		}
		if output, err := exec.Command("powershell.exe", args...).CombinedOutput(); err != nil {
			t.Fatalf("native sender shortcut icon metadata: %v; %s", err, output)
		}
	}
	checkIcon(false)
	_ = sink.Close()
	checkIcon(true) // Same target, old shortcut with stale icon metadata.
	sink, err = newWindowsSinkWithExecutable(appID, "音墨通知功能测试", "", "", moved)
	if err != nil {
		t.Fatal("repairing a shortcut with stale product icon:", err)
	}
	checkIcon(false)
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\AppUserModelId\`+appID, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	icon, _, err := key.GetStringValue("IconUri")
	_ = key.Close()
	registered, readErr := os.ReadFile(icon)
	if err != nil || readErr != nil || !bytes.Equal(registered, senderIconPNG) {
		t.Fatal("notification sender did not register embedded ez artwork")
	}
	card := Card{Group: "synthetic", Tag: "moved", Title: "合成迁移测试", Body: "仅合成消息", Silent: true}
	if err := sink.Show(card); err != nil {
		t.Fatal(err)
	}
	await(t, func() bool {
		result := sink.call(nativeRequest{op: "count"})
		return result.err == nil && result.count == 1
	})
}

func ownDeviceTestTarget(t *testing.T) Target {
	t.Helper()
	if os.Getenv("SCEZ_NOTIFICATION_DEVICE_TEST") != "1" && os.Getenv("SCEZ_NOTIFICATION_K80_TEST") != "1" {
		t.Skip("opt-in own-tag-only device integration test")
	}
	serial, deviceSerial := os.Getenv("SCEZ_TEST_SERIAL"), os.Getenv("SCEZ_TEST_DEVICE_SERIAL")
	if serial == "" || deviceSerial == "" {
		t.Fatal("explicit authorized transport and physical device serial required")
	}
	name := os.Getenv("SCEZ_TEST_DEVICE_NAME")
	if name == "" {
		name = "Authorized test device"
	}
	connection := "USB · " + serial
	if strings.Contains(serial, ":") {
		connection = "无线 · " + serial
	}
	return Target{Identity: "device-synthetic", Name: name, Connection: connection, Serial: serial, DeviceSerial: deviceSerial}
}

func TestDeviceWithoutCastingToWindows(t *testing.T) {
	target := ownDeviceTestTarget(t)
	adb, server, probe := os.Getenv("SCEZ_TEST_ADB"), os.Getenv("SCEZ_TEST_SERVER"), os.Getenv("SCEZ_TEST_PROBE")
	if adb == "" || server == "" || probe == "" {
		t.Fatal("explicit test paths required")
	}
	serial := target.Serial
	tag := "scez_notification_lab_20261004"
	remote := "/data/local/tmp/scrcpy-ez-notification-test-" + strconv.Itoa(os.Getpid()) + ".jar"
	command := func(args ...string) *exec.Cmd { return exec.Command(adb, append([]string{"-s", serial}, args...)...) }
	if err := command("shell", "test", "!", "-e", remote).Run(); err != nil {
		t.Fatal("owned probe path already exists")
	}
	if out, err := command("push", probe, remote).CombinedOutput(); err != nil {
		t.Fatalf("push synthetic probe: %v %s", err, out)
	}
	t.Cleanup(func() { _ = command("shell", "rm", "-f", remote).Run() })
	sink := testNativeSink(t)
	ctx, cancel := context.WithCancel(context.Background())
	sourceExited := make(chan struct{})
	ready := make(chan struct{})
	done := make(chan error, 1)
	state := NewState(target.Identity, target.Name, true)
	state.connection = target.Connection
	var mu sync.Mutex
	posts, removals, icons := 0, 0, 0
	latencies := []time.Duration{}
	start := time.Now()
	source := &ADBSource{ADB: adb, Server: server, TestTag: tag}
	go func() {
		defer close(sourceExited)
		done <- source.Run(ctx, target, func(frame Frame) error {
			if frame.Type == "ready" {
				close(ready)
			}
			before := time.Now()
			if err := state.Apply(frame, sink); err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			if frame.Type == "post" {
				if frame.Record.IconID == "" || state.icons[frame.Record.IconID].ID == "" {
					return fmt.Errorf("synthetic notification application artwork missing")
				}
				posts++
				latencies = append(latencies, time.Since(before))
			}
			if frame.Type == "remove" {
				removals++
			}
			if frame.Type == "icon" {
				icons++
			}
			return nil
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-sourceExited:
		case <-time.After(8 * time.Second):
			t.Error("listener cleanup timed out")
		}
	})
	select {
	case <-ready:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(15 * time.Second):
		t.Fatal("listener not ready")
	}
	t.Logf("background listener startup=%s", time.Since(start))
	var castDone chan error
	var castLog *textureOutput
	if client := os.Getenv("SCEZ_TEST_CAST_CLIENT"); client != "" {
		castCtx, stopCast := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCast()
		// Wait for an actual texture, then deliver notifications during casting.
		cast := exec.CommandContext(castCtx, client, "-s", serial, "--max-size=1280", "--max-fps=60", "--time-limit=12", "--no-audio", "--no-control", "--window-x=-2000", "--window-y=-2000")
		cast.Env = append(os.Environ(), "ADB="+adb, "SCRCPY_SERVER_PATH="+server)
		castLog = &textureOutput{ready: make(chan struct{})}
		cast.Stdout, cast.Stderr = castLog, castLog
		if err := cast.Start(); err != nil {
			t.Fatal(err)
		}
		castDone = make(chan error, 1)
		go func() { castDone <- cast.Wait() }()
		t.Cleanup(func() { stopCast() })
		select {
		case <-castLog.ready:
		case err := <-castDone:
			t.Fatalf("cast exited before Texture: %v\n%s", err, castLog.String())
		case <-time.After(10 * time.Second):
			t.Fatalf("cast Texture timed out\n%s", castLog.String())
		}
	}
	// Probe classpath includes the listener's own server build but only synthetic notifications are selected.
	// The probe process does not enable casting, root or notification-access settings.
	probeServer := "/data/local/tmp/scrcpy-ez-notification-test-server-" + strconv.Itoa(os.Getpid()) + ".jar"
	if err := command("shell", "test", "!", "-e", probeServer).Run(); err != nil {
		t.Fatal("owned server path exists")
	}
	if err := command("push", server, probeServer).Run(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command("shell", "rm", "-f", probeServer).Run() })
	job := command("shell", "CLASSPATH="+remote+":"+probeServer, "app_process", "/", "lab.NotifyProbe")
	if castDone != nil {
		select {
		case err := <-castDone:
			t.Fatalf("cast exited before synthetic messages: %v", err)
		default:
		}
	}
	output, err := job.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "OWN_NOTIFICATION_AND_CHANNEL_REMOVED") {
		t.Fatalf("synthetic probe failed: %v", err)
	}
	await(t, func() bool { mu.Lock(); defer mu.Unlock(); return posts == 2 && removals == 1 })
	await(t, func() bool { return sink.call(nativeRequest{op: "count"}).count == 0 })
	if castDone != nil {
		select {
		case err := <-castDone:
			t.Fatalf("cast exited during synthetic messages: %v", err)
		default:
		}
		select {
		case err := <-castDone:
			if err != nil {
				t.Fatalf("cast failed: %v\n%s", err, castLog.String())
			}
		case <-time.After(20 * time.Second):
			t.Fatal("cast did not exit within its time limit")
		}
		t.Log("Texture confirmed before probe; casting stayed active during both notifications and removal, then exited successfully")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("listener cleanup timed out")
	}
	if icons != 1 {
		t.Fatalf("artwork not sent once per application: %d", icons)
	}
	t.Logf("own events: 2 posts, 1 removal, 1 application icon; Go post handling including Windows Show=%v; native history=0", latencies)
}

type textureOutput struct {
	sync.Mutex
	buffer bytes.Buffer
	ready  chan struct{}
	once   sync.Once
}

func (o *textureOutput) Write(data []byte) (int, error) {
	o.Lock()
	defer o.Unlock()
	if o.buffer.Len()+len(data) <= 65536 {
		_, _ = o.buffer.Write(data)
	}
	if bytes.Contains(o.buffer.Bytes(), []byte("INFO: Texture:")) {
		o.once.Do(func() { close(o.ready) })
	}
	return len(data), nil
}
func (o *textureOutput) String() string { o.Lock(); defer o.Unlock(); return o.buffer.String() }

func TestWindowsToastVisualPreview(t *testing.T) {
	if os.Getenv("SCEZ_NOTIFICATION_VISUAL_TEST") != "1" {
		t.Skip("opt-in synthetic visual fixture")
	}
	sink := testNativeSink(t)
	var artwork iconStore
	defer artwork.Close()
	card := Card{Group: "visual", Tag: "layout", Device: "工作平板", Connection: "无线 · 192.0.2.10:5555", App: "合成邮件", Title: "登录验证码", Body: "测试验证码 762184，5 分钟有效。这是虚构消息，用于确认设备标识与图标布局。", Icon: syntheticAppIcon(t)}
	card.IconURI = artwork.URI(card.Icon)
	xml := strings.Replace(ToastXML(card), `duration="short"`, `duration="long"`, 1)
	if err := sink.call(nativeRequest{op: "show", card: card, xml: xml, group: card.Group, tag: card.Tag}).err; err != nil {
		t.Fatal(err)
	}
	t.Log("SYNTHETIC_PREVIEW_READY")
	time.Sleep(35 * time.Second)
}

func TestDeviceRejectsUnexpectedDeviceIdentity(t *testing.T) {
	target := ownDeviceTestTarget(t)
	adb, server := os.Getenv("SCEZ_TEST_ADB"), os.Getenv("SCEZ_TEST_SERVER")
	if adb == "" || server == "" {
		t.Fatal("explicit authorized test paths required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	source := &ADBSource{ADB: adb, Server: server, TestTag: "scez_notification_lab_20261004"}
	frames := 0
	target.Identity = "unexpected"
	target.DeviceSerial = "synthetic-wrong-device-identity"
	err := source.Run(ctx, target, func(Frame) error { frames++; return nil })
	if !errors.Is(err, ErrUnavailable) || frames != 0 {
		t.Fatal("foreign identity reached notification registration/protocol")
	}
}

type monitoredDeviceSource struct {
	Source
	ready                   chan string
	starts, active, maximum atomic.Int32
}

func (s *monitoredDeviceSource) Run(ctx context.Context, target Target, emit func(Frame) error) error {
	s.starts.Add(1)
	active := s.active.Add(1)
	defer s.active.Add(-1)
	for old := s.maximum.Load(); active > old && !s.maximum.CompareAndSwap(old, active); old = s.maximum.Load() {
	}
	return s.Source.Run(ctx, target, func(frame Frame) error {
		if err := emit(frame); err != nil {
			return err
		}
		if frame.Type == "ready" {
			s.ready <- target.Serial
		}
		return nil
	})
}

type monitoredDeviceSink struct {
	Sink
	cards chan Card
}

func (s *monitoredDeviceSink) Show(card Card) error {
	if err := s.Sink.Show(card); err != nil {
		return err
	}
	s.cards <- card
	return nil
}

func TestDeviceUSBWiFiHandoverWithoutDuplicateListener(t *testing.T) {
	usb := ownDeviceTestTarget(t)
	wifi := usb
	wifi.Serial = os.Getenv("SCEZ_TEST_WIFI_SERIAL")
	if wifi.Serial == "" {
		t.Skip("explicit second authorized transport required")
	}
	wifi.Connection = "无线 · " + wifi.Serial
	adb, server, probe := os.Getenv("SCEZ_TEST_ADB"), os.Getenv("SCEZ_TEST_SERVER"), os.Getenv("SCEZ_TEST_PROBE")
	if adb == "" || server == "" || probe == "" {
		t.Fatal("explicit synthetic test paths required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := func(serial string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, adb, append([]string{"-s", serial}, args...)...)
	}
	remote := "/data/local/tmp/scrcpy-ez-notification-handover-" + strconv.Itoa(os.Getpid())
	for suffix, local := range map[string]string{"-probe.jar": probe, "-server.jar": server} {
		path := remote + suffix
		if err := command(usb.Serial, "shell", "test", "!", "-e", path).Run(); err != nil {
			t.Fatal("owned fixture path exists")
		}
		if err := command(usb.Serial, "push", local, path).Run(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			_ = exec.CommandContext(cleanup, adb, "-s", usb.Serial, "shell", "rm", "-f", path).Run()
		})
	}
	source := &monitoredDeviceSource{Source: &ADBSource{ADB: adb, Server: server, TestTag: "scez_notification_lab_20261004"}, ready: make(chan string, 8)}
	native := testNativeSink(t)
	sink := &monitoredDeviceSink{Sink: native, cards: make(chan Card, 8)}
	manager := NewManager(ctx, source, func() (Sink, error) { return sink, nil })
	defer manager.Close()
	for _, target := range []Target{usb, wifi, usb} {
		started := time.Now()
		manager.Reconcile([]Target{target}, Options{Preview: true})
		select {
		case transport := <-source.ready:
			if transport != target.Serial {
				t.Fatal("wrong handover transport")
			}
		case <-time.After(12 * time.Second):
			t.Fatal("handover listener not ready")
		}
		await(t, func() bool { statuses := manager.Status(); return len(statuses) == 1 && statuses[0].State == "active" })
		t.Logf("selected %s listener ready in %s", target.Connection, time.Since(started))
		// Changing only the display label must affect future cards without restarting Android.
		target.Name = "我的合成测试平板"
		manager.Reconcile([]Target{target}, Options{Preview: true})
		job := command(target.Serial, "shell", "CLASSPATH="+remote+"-probe.jar:"+remote+"-server.jar", "app_process", "/", "lab.NotifyProbe")
		out, err := job.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "OWN_NOTIFICATION_AND_CHANNEL_REMOVED") {
			t.Fatalf("synthetic handover probe: %v", err)
		}
		for _, code := range []string{"762184", "A7B9C2"} {
			select {
			case card := <-sink.cards:
				if card.Connection != target.Connection || card.Device != target.Name || !validIcon(card.Icon) || card.CopyCode != code {
					t.Fatal("incorrect device/transport/artwork/code metadata")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("handover notification not delivered")
			}
		}
		await(t, func() bool { return native.call(nativeRequest{op: "count"}).count == 0 })
	}
	manager.Reconcile(nil, Options{})
	await(t, func() bool { return source.active.Load() == 0 && len(manager.Status()) == 0 })
	if source.starts.Load() != 3 || source.maximum.Load() != 1 {
		t.Fatal("listener restart/overlap violated physical device ownership")
	}
	t.Log("USB → Wi-Fi → USB: 3 listeners, maximum 1 active; 6 synthetic cards with icons/codes; rename did not restart; disable cleaned up")
}
