//go:build windows && cgo

package notifications

/*
#cgo CXXFLAGS: -std=c++17
#cgo LDFLAGS: -lruntimeobject -lole32 -luuid -lshell32 -lpropsys -lshlwapi -lstdc++ -lgdi32
#include <stdlib.h>
#include "native_windows.h"
*/
import "C"

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const WindowsAppID = "ScrcpyEZ.NotificationSync"

// Record tags are hexadecimal hashes, so this reserved tag cannot collide with
// a phone notification. Reuse it per device to keep confirmations bounded.
const copySuccessTag = "copy-success"

type nativeRequest struct {
	op         string
	card       Card
	xml        string // Internal native visual/activation integration fixture only.
	value      string
	copier     func(string, uint32) error // Internal integration seam; nil uses the Windows clipboard.
	group, tag string
	reply      chan nativeResult
}
type nativeResult struct {
	value string
	count int
	err   error
	token string
}
type copyActivation struct {
	token    string
	sequence uint32
}

var nativeCallbacks = struct {
	sync.Mutex
	sinks map[uint64]*WindowsSink
}{sinks: make(map[uint64]*WindowsSink)}
var nativeCallbackID atomic.Uint64

//export scez_notification_clicked
func scez_notification_clicked(handle C.uint64_t, token *C.char, sequence C.uint32_t) {
	value := C.GoString(token)
	if len(value) != 32 {
		return
	}
	nativeCallbacks.Lock()
	sink := nativeCallbacks.sinks[uint64(handle)]
	nativeCallbacks.Unlock()
	if sink == nil {
		return
	}
	select {
	case sink.activations <- copyActivation{token: value, sequence: uint32(sequence)}:
	default:
	}
}

func activationCLSID(appID string) string {
	hash := sha256.Sum256([]byte("ScrcpyEZ.ToastActivation/" + appID))
	hash[6] = (hash[6] & 15) | 0x50
	hash[8] = (hash[8] & 63) | 0x80
	return fmt.Sprintf("{%x-%x-%x-%x-%x}", hash[:4], hash[4:6], hash[6:8], hash[8:10], hash[10:16])
}

func registerActivation(appID, exe string) string {
	clsid := activationCLSID(appID)
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\CLSID\`+clsid+`\LocalServer32`, registry.SET_VALUE)
	if err != nil {
		return ""
	}
	err = key.SetStringValue("", `"`+exe+`" --notification-passive`)
	_ = key.Close()
	if err != nil {
		return ""
	}
	key, _, err = registry.CreateKey(registry.CURRENT_USER, `Software\Classes\AppUserModelId\`+appID, registry.SET_VALUE)
	if err != nil {
		return ""
	}
	err = key.SetStringValue("CustomActivator", clsid)
	_ = key.Close()
	if err != nil {
		return ""
	}
	return clsid
}

type WindowsSink struct {
	requests      chan nativeRequest
	done          chan struct{}
	closeOnce     sync.Once
	ownership     windows.Handle
	activations   chan copyActivation
	copyAvailable bool
}

func NewWindowsSink() (Sink, error) { return newAppWindowsSink() }

// Exact 256px PNG frame from the product's assets/icon.ico. Embed branding so
// moving/updating the executable cannot substitute the original scrcpy icon.
//
//go:embed assets/scrcpy-ez.png
var senderIconPNG []byte

// IconUri requires an image file. Keep this public artwork in a stable cache
// across restarts; per-message artwork has a separate, temporary lifecycle.
// If the cache is unavailable, the shortcut still uses the executable's icon.
func windowsSenderIcon(cacheDir string) string {
	if cacheDir == "" || !validIcon(IconFromPNG(senderIconPNG)) {
		return ""
	}
	dir := filepath.Join(cacheDir, "scrcpy-ez", "notification-brand")
	path := filepath.Join(dir, fmt.Sprintf("%x.png", sha256.Sum256(senderIconPNG)))
	if data, err := os.ReadFile(path); err == nil && bytes.Equal(data, senderIconPNG) {
		return path
	}
	if os.MkdirAll(dir, 0755) != nil {
		return ""
	}
	file, err := os.CreateTemp(dir, "icon-*.tmp")
	if err != nil {
		return ""
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	_, err = file.Write(senderIconPNG)
	closeErr := file.Close()
	if err != nil || closeErr != nil || os.Rename(tmp, path) != nil {
		return ""
	}
	return path
}

func newWindowsSink(appID, displayName string) (*WindowsSink, error) {
	return newWindowsSinkWithIdentity(appID, displayName, "", "")
}

func newWindowsSinkWithIdentity(appID, displayName, shortcutName, senderIcon string) (*WindowsSink, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, sinkSystemError("registration", err)
	}
	return newWindowsSinkWithExecutable(appID, displayName, shortcutName, senderIcon, exe)
}

func newWindowsSinkWithExecutable(appID, displayName, shortcutName, senderIcon, exe string) (*WindowsSink, error) {
	// Only one GUI may own this user's notification identity/history at a time.
	name, err := windows.UTF16PtrFromString(`Local\` + appID)
	if err != nil {
		return nil, sinkSystemError("ownership", err)
	}
	ownership, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		if ownership != 0 {
			_ = windows.CloseHandle(ownership)
		}
		return nil, sinkSystemError("ownership", err)
	}
	owned := false
	defer func() {
		if !owned {
			_ = windows.CloseHandle(ownership)
		}
	}()
	base, err := os.UserConfigDir()
	if err != nil {
		return nil, sinkSystemError("directory", err)
	}
	// Dedicated shortcut: never replace the user's existing product shortcut.
	shortcut := filepath.Join(base, "Microsoft", "Windows", "Start Menu", "Programs", appID+".lnk")
	if shortcutName != "" {
		shortcut = filepath.Join(base, "Microsoft", "Windows", "Start Menu", "Programs", "scrcpy-ez 手机通知", appID, shortcutName+".lnk")
	}
	if err := os.MkdirAll(filepath.Dir(shortcut), 0755); err != nil {
		return nil, sinkSystemError("directory", err)
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\AppUserModelId\`+appID, registry.SET_VALUE)
	if err != nil {
		return nil, sinkSystemError("registration", err)
	}
	err = key.SetStringValue("DisplayName", displayName)
	if err == nil {
		icon := senderIcon
		if icon == "" {
			cacheDir, _ := os.UserCacheDir()
			icon = windowsSenderIcon(cacheDir)
		}
		if icon != "" {
			err = key.SetStringValue("IconUri", icon)
		} else {
			err = key.DeleteValue("IconUri")
			if err == registry.ErrNotExist {
				err = nil
			}
		}
	}
	_ = key.Close()
	if err != nil {
		return nil, sinkSystemError("registration", err)
	}
	s := &WindowsSink{requests: make(chan nativeRequest), done: make(chan struct{}), ownership: ownership, activations: make(chan copyActivation, 32)}
	owned = true
	initialized := make(chan error, 1)
	go s.run(appID, exe, shortcut, initialized)
	if err = <-initialized; err != nil {
		return nil, err
	}
	return s, nil
}

func nativeError(code C.int32_t) error {
	return nativeStageError("send", code)
}

func nativeStageError(stage string, code C.int32_t) error {
	if code < 0 {
		return &sinkFailure{stage: stage, code: uint32(code)}
	}
	return nil
}

func sinkSystemError(stage string, err error) error {
	var errno syscall.Errno
	var code uint32
	if errors.As(err, &errno) {
		code = 0x80070000 | uint32(errno)
	}
	return &sinkFailure{stage: stage, code: code}
}

// Separate-process integration seam; never called by notification delivery.
func nativeExternalActivation(appID, token string) error {
	app, clsid, value := C.CString(appID), C.CString(activationCLSID(appID)), C.CString(token)
	defer C.free(unsafe.Pointer(app))
	defer C.free(unsafe.Pointer(clsid))
	defer C.free(unsafe.Pointer(value))
	return nativeError(C.scez_toast_test_external_activate(app, clsid, value))
}

func (s *WindowsSink) run(appID, exe, shortcut string, initialized chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(s.done)
	defer windows.CloseHandle(s.ownership)
	handle := nativeCallbackID.Add(1)
	nativeCallbacks.Lock()
	nativeCallbacks.sinks[handle] = s
	nativeCallbacks.Unlock()
	defer func() { nativeCallbacks.Lock(); delete(nativeCallbacks.sinks, handle); nativeCallbacks.Unlock() }()
	id, executable, link := C.CString(appID), C.CString(exe), C.CString(shortcut)
	defer C.free(unsafe.Pointer(id))
	defer C.free(unsafe.Pointer(executable))
	defer C.free(unsafe.Pointer(link))
	var context unsafe.Pointer
	var icons iconStore
	defer icons.Close()
	clsid := C.CString(registerActivation(appID, exe))
	defer C.free(unsafe.Pointer(clsid))
	var copyReady C.int
	var openPhase C.int
	openCode := C.scez_toast_open(id, executable, link, clsid, C.uint64_t(handle), &copyReady, &context, &openPhase)
	openStages := map[C.int]string{1: "com", 2: "shortcut", 3: "sender", 4: "service", 5: "history"}
	if err := nativeStageError(openStages[openPhase], openCode); err != nil {
		initialized <- err
		return
	}
	defer C.scez_toast_close(context)
	s.copyAvailable = copyReady != 0
	initialized <- nil
	var copies copyStore
	var testCopy func(string, uint32) error
	var feedbackGroup string
	var feedbackTicks <-chan time.Time
	var feedbackTicker *time.Ticker
	stopFeedback := func() {
		C.scez_copy_feedback_hide(context)
		if feedbackTicker != nil {
			feedbackTicker.Stop()
		}
		feedbackTicker, feedbackTicks, feedbackGroup = nil, nil, ""
	}
	defer stopFeedback()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		var request nativeRequest
		select {
		case <-feedbackTicks:
			if C.scez_copy_feedback_tick(context) == 0 {
				stopFeedback()
			}
			continue
		case click := <-s.activations:
			entry := copies.entries[click.token]
			copied := copies.Copy(click.token, time.Now(), func(code string) error {
				if testCopy != nil {
					return testCopy(code, click.sequence)
				}
				text := C.CString(code)
				defer C.free(unsafe.Pointer(text))
				return nativeError(C.scez_clipboard_copy(context, text, C.uint32_t(click.sequence)))
			})
			if copied {
				// The OS normally dismisses a clicked card. Remove it explicitly
				// as well, so a consumed action never remains in our native cache.
				// This is a new passive confirmation, never a restoration of the
				// original card. Notification failures do not undo a real copy.
				group, tag := C.CString(entry.group), C.CString(entry.tag)
				_ = C.scez_toast_remove(context, group, tag)
				C.free(unsafe.Pointer(tag))
				tag = C.CString(copySuccessTag)
				xml := C.CString(copySuccessXML())
				stopFeedback()
				// Notification Center can suppress a native banner even though
				// Show succeeds. Keep a quiet native history entry and show a
				// three-second nonactivating confirmation independently of Shell.
				silent := C.int(0)
				if C.scez_copy_feedback_show(context) >= 0 {
					silent = 1
					feedbackGroup = entry.group
					feedbackTicker = time.NewTicker(50 * time.Millisecond)
					feedbackTicks = feedbackTicker.C
				}
				var feedbackPhase C.int
				_ = C.scez_toast_show(context, xml, group, tag, silent, &feedbackPhase)
				C.free(unsafe.Pointer(xml))
				C.free(unsafe.Pointer(tag))
				C.free(unsafe.Pointer(group))
			}
			continue
		case now := <-ticker.C:
			copies.Sweep(now)
			continue
		case request = <-s.requests:
		}
		result := nativeResult{}
		group, tag := C.CString(request.group), C.CString(request.tag)
		switch request.op {
		case "show":
			request.card.Token = ""
			if s.copyAvailable {
				request.card.Token = copies.Issue(request.card, time.Now())
			} else {
				copies.Remove(request.group, request.tag)
			}
			request.card.IconURI = icons.URI(request.card.Icon)
			payload := ToastXML(request.card)
			if request.xml != "" {
				payload = request.xml
			}
			xml := C.CString(payload)
			silent := C.int(0)
			if request.card.Silent {
				silent = 1
			}
			var showPhase C.int
			showCode := C.scez_toast_show(context, xml, group, tag, silent, &showPhase)
			showStages := map[C.int]string{1: "render", 2: "setting", 3: "disabled", 4: "send"}
			result.err = nativeStageError(showStages[showPhase], showCode)
			if result.err != nil {
				copies.Remove(request.group, request.tag)
			}
			C.free(unsafe.Pointer(xml))
		case "remove":
			copies.Remove(request.group, request.tag)
			result.err = nativeStageError("remove", C.scez_toast_remove(context, group, tag))
		case "clear":
			if feedbackGroup == request.group {
				stopFeedback()
			}
			copies.Remove(request.group, "")
			result.err = nativeStageError("clear", C.scez_toast_clear(context, group))
		case "count":
			var count C.int
			result.err = nativeError(C.scez_toast_count(context, &count))
			result.count = int(count)
		case "senderLabel":
			var label [512]C.char
			result.err = nativeError(C.scez_toast_sender_label(context, &label[0], C.int(len(label))))
			if result.err == nil {
				result.value = C.GoString(&label[0])
			}
		case "failures":
			var count C.int
			var code C.int32_t
			result.err = nativeError(C.scez_toast_failures(context, &count, &code))
			if result.err == nil {
				result.err = nativeError(code)
			}
			result.count = int(count)
		case "feedbackState":
			result.count = int(C.scez_copy_feedback_state(context))
		case "contains":
			value := C.CString(request.value)
			var found C.int
			result.err = nativeError(C.scez_toast_contains(context, value, &found))
			result.count = int(found)
			C.free(unsafe.Pointer(value))
		case "copyWriter":
			testCopy = request.copier
		case "copyToken":
			for token, entry := range copies.entries {
				if entry.group == request.group && entry.tag == request.tag {
					result.token = token
					break
				}
			}
		case "activate":
			token := C.CString(request.value)
			result.err = nativeError(C.scez_toast_test_activate(context, token))
			C.free(unsafe.Pointer(token))
		case "clipboardRoundtrip":
			var tested C.int
			result.err = nativeError(C.scez_clipboard_test_roundtrip(context, &tested))
			result.count = int(tested)
		}
		C.free(unsafe.Pointer(group))
		C.free(unsafe.Pointer(tag))
		request.reply <- result
		if request.op == "close" {
			return
		}
	}
}

func (s *WindowsSink) call(request nativeRequest) nativeResult {
	request.reply = make(chan nativeResult, 1)
	select {
	case s.requests <- request:
	case <-s.done:
		return nativeResult{err: ErrUnavailable}
	}
	select {
	case result := <-request.reply:
		return result
	case <-s.done:
		return nativeResult{err: ErrUnavailable}
	}
}
func (s *WindowsSink) Show(card Card) error {
	return s.call(nativeRequest{op: "show", card: card, group: card.Group, tag: card.Tag}).err
}
func (s *WindowsSink) Remove(group, tag string) error {
	return s.call(nativeRequest{op: "remove", group: group, tag: tag}).err
}
func (s *WindowsSink) Clear(group string) error {
	return s.call(nativeRequest{op: "clear", group: group}).err
}
func (s *WindowsSink) Close() error {
	s.closeOnce.Do(func() { _ = s.call(nativeRequest{op: "close"}); <-s.done })
	return nil
}

// Passive toast activation/shortcut must never launch a second GUI or start casting.
func RunIfPassiveRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--notification-passive" {
			return true
		}
	}
	return false
}
