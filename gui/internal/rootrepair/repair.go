// Package rootrepair performs opt-in repair after a confirmed server upload denial.
// It repairs metadata, then verifies access through ordinary ADB again.
package rootrepair

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

const Version = "v2.2.3-rc.1"
const RepoURL = "https://github.com/kinewe/scrcpy-ez"
const Branch = "codex/unified-root-v2.2.3-rc.1"

// AuthorizationWait is a PC-side limit, not a root-manager policy. The total
// budget also includes the per-device mutex and ordinary ADB verification.
const AuthorizationWait = 3 * time.Minute
const Budget = 5 * time.Minute

var safeID = regexp.MustCompile(`^[A-Za-z0-9_.:\[\]-]+$`)
var fingerprint = regexp.MustCompile(`^[0-9]+:[0-9]+$`)

// Execute must honor cancellation. stdin contains a script, never host shell code.
type Execute func(context.Context, []string, string) (string, error)

type Options struct {
	ADB, Serial, Identity, LogDir string
	Execute                       Execute
	Say                           func(string)
}

type Step struct {
	Name   string   `json:"name"`
	Root   bool     `json:"root"`
	Args   []string `json:"args"`
	Script string   `json:"script,omitempty"`
	Output string   `json:"output"`
	Error  string   `json:"error,omitempty"`
}

type Report struct {
	Version  string    `json:"version"`
	Serial   string    `json:"transport"`
	Identity string    `json:"identity"`
	Started  time.Time `json:"started"`
	Status   string    `json:"status"`
	Steps    []Step    `json:"steps"`
	Error    string    `json:"error,omitempty"`
}

type attempt struct {
	o          Options
	r          *Report
	rootDirect bool
}

func (a attempt) say(s string) {
	if a.o.Say != nil {
		a.o.Say(s)
	}
}

func marker(out, key string) string {
	prefix := "SCEZ_ROOT_" + key + "="
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func (a attempt) execute(ctx context.Context, name string, root bool, timeout time.Duration, args []string, script string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := a.o.Execute(ctx, append([]string{"-s", a.o.Serial}, args...), script)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	s := Step{Name: name, Root: root, Args: append([]string{"-s", a.o.Serial}, args...), Script: script, Output: out}
	if err != nil {
		s.Error = err.Error()
	}
	a.r.Steps = append(a.r.Steps, s)
	return out, err
}

func (a attempt) shell(ctx context.Context, name string, root bool, timeout time.Duration, script string) (string, error) {
	args := []string{"shell", "-T", "sh"}
	if root && !a.rootDirect {
		args = []string{"shell", "-T", "su", "-c", "sh"}
	}
	return a.execute(ctx, name, root, timeout, args, script)
}

// Every remote operation rechecks identity; every privileged operation also checks
// uid and inode. No inherited transport, quoted IP typed by a user, or model match.
func guard(identity, inode string, root bool) string {
	s := `actual=$(getprop ro.serialno)
[ -n "$actual" ] || actual=$(getprop ro.boot.serialno)
[ "$actual" = '` + identity + `' ] || { echo SCEZ_ROOT_UNSAFE=identity; exit 41; }
p=/data/local/tmp
f=$p/scrcpy-server.jar
for ancestor in /data /data/local "$p"; do
  [ ! -L "$ancestor" ] || { echo SCEZ_ROOT_UNSAFE=symlink; exit 42; }
done
[ -d "$p" ] || { echo SCEZ_ROOT_UNSAFE=missing-directory; exit 43; }
if [ -e "$f" ] || [ -L "$f" ]; then
  [ ! -L "$f" ] && [ -f "$f" ] && [ "$(stat -c %h "$f")" = 1 ] || { echo SCEZ_ROOT_UNSAFE=server-file; exit 44; }
fi
`
	if root {
		s = `[ "$(id -u)" = 0 ] || { echo SCEZ_ROOT_UNSAFE=not-root; exit 45; }
` + s
	}
	if inode != "" {
		s += `[ "$(stat -c %d:%i "$p")" = '` + inode + `' ] || { echo SCEZ_ROOT_UNSAFE=mount-or-directory-changed; exit 46; }
`
	}
	return s
}

func inspect(identity string) string {
	return guard(identity, "", false) + `echo SCEZ_ROOT_FINGERPRINT=$(stat -c %d:%i "$p")
echo SCEZ_ROOT_DIR_META=$(stat -c '%u:%g:%a' "$p")
echo SCEZ_ROOT_DIR_LABEL=$(ls -Zd "$p")
echo SCEZ_ROOT_UID=$(id -u)
id
getenforce
ls -ldZ /data/local "$p"
df -k "$p"
if [ -e "$f" ]; then
  echo SCEZ_ROOT_FILE_META=$(stat -c '%u:%g:%a' "$f")
  echo SCEZ_ROOT_FILE_LABEL=$(ls -Zd "$f")
fi
if [ -w "$p" ] && [ -x "$p" ] && { [ ! -e "$f" ] || [ -w "$f" ]; }; then
  echo SCEZ_ROOT_WRITABLE=yes
else
  echo SCEZ_ROOT_WRITABLE=no
fi
`
}

func (a attempt) verify(ctx context.Context, inode string) (bool, error) {
	out, err := a.shell(ctx, "ordinary-inspection", false, 5*time.Second, inspect(a.o.Identity))
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if marker(out, "UNSAFE") != "" {
		return false, fmt.Errorf("设备身份或路径检查失败：%s", marker(out, "UNSAFE"))
	}
	if err != nil {
		return false, err
	}
	if inode != "" && marker(out, "FINGERPRINT") != inode {
		return false, errors.New("目录或挂载视图发生变化，停止尝试")
	}
	if marker(out, "WRITABLE") != "yes" {
		return false, nil
	}
	// shell access alone is not proof that the adbd sync service can upload.
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return false, err
	}
	token := hex.EncodeToString(nonce[:])
	remote := "/data/local/tmp/.scrcpy-ez-root-probe-" + token
	local, err := os.CreateTemp("", "scrcpy-ez-root-probe-*")
	if err != nil {
		return false, err
	}
	name := local.Name()
	defer os.Remove(name)
	_, writeErr := local.WriteString(token)
	closeErr := local.Close()
	if writeErr != nil {
		return false, writeErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	// Check the randomized destination before the sync upload; never overwrite a
	// known file. All deletion is limited to this attempt's own random probe.
	check := guard(a.o.Identity, inode, false) + `[ ! -e '` + remote + `' ] && [ ! -L '` + remote + `' ]
`
	if _, err = a.shell(ctx, "probe-destination", false, 5*time.Second, check); err != nil {
		return false, err
	}
	_, pushErr := a.execute(ctx, "ordinary-adb-push", false, 5*time.Second, []string{"push", name, remote}, "")
	check = guard(a.o.Identity, inode, false) + `if [ -L '` + remote + `' ]; then exit 47; fi
if [ -f '` + remote + `' ]; then
  value=$(cat '` + remote + `')
  rm -f '` + remote + `' || exit 48
  [ "$value" = '` + token + `' ] || exit 49
  echo SCEZ_ROOT_PROBE=ok
else
  exit 50
fi
`
	out, cleanupErr := a.shell(ctx, "probe-read-and-cleanup", false, 5*time.Second, check)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if pushErr != nil {
		return false, nil
	}
	return cleanupErr == nil && marker(out, "PROBE") == "ok", nil
}

// Prepare never starts a root server, disables SELinux, resets adbd, deletes the
// server JAR, or recursively changes a directory. Ordinary push is the acceptance
// condition. A failed attempt is not retried until a new transport or manual start.
func Prepare(ctx context.Context, o Options) (r Report, err error) {
	r = Report{Version: Version, Serial: o.Serial, Identity: o.Identity, Started: time.Now(), Status: "failed"}
	defer func() {
		if err != nil {
			r.Error = err.Error()
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			r.Status = "canceled"
		}
		if o.LogDir != "" && r.Status != "healthy" {
			defer trimReports(o.LogDir)
			if e := os.MkdirAll(o.LogDir, 0700); e == nil {
				data, _ := json.MarshalIndent(r, "", "  ")
				file, e := os.CreateTemp(o.LogDir, "root-"+r.Started.Format("20060102-150405")+"-*.json")
				if e == nil {
					_, e = file.Write(data)
					closeErr := file.Close()
					if e == nil {
						e = closeErr
					}
					if e == nil && o.Say != nil {
						o.Say("诊断报告：" + file.Name())
					}
				}
				if e != nil && o.Say != nil {
					o.Say("诊断报告保存失败：" + e.Error())
				}
			} else if o.Say != nil {
				o.Say("诊断目录不可写：" + e.Error())
			}
		}
	}()
	if !safeID.MatchString(o.Serial) || !safeID.MatchString(o.Identity) || strings.HasPrefix(o.Serial, "-") || o.Execute == nil {
		return r, errors.New("缺少安全的目标设备标识")
	}
	ctx, cancel := context.WithTimeout(ctx, Budget)
	defer cancel()
	a := attempt{o: o, r: &r}
	ok, e := a.verify(ctx, "")
	if e != nil {
		return r, e
	}
	if ok {
		r.Status = "healthy"
		a.say("普通 ADB 上传检查通过")
		return r, nil
	}
	inode := marker(r.Steps[0].Output, "FINGERPRINT")
	if !fingerprint.MatchString(inode) {
		return r, errors.New("无法核对目录 inode，停止自动修复")
	}
	a.rootDirect = marker(r.Steps[0].Output, "UID") == "0"
	if a.rootDirect {
		a.say("检测到已有 root adbd，校验 root 权限（不会重启 adbd）")
	} else {
		a.say("上传检查失败；请在手机解锁后允许 Shell 的 root 授权（最多等待 3 分钟）")
	}
	base := guard(o.Identity, inode, true)
	// Separate root authorization/inspection from any mutation, preserving the
	// before state on disk before changing metadata.
	out, e := a.shell(ctx, "root-authorization", true, AuthorizationWait, base+inspect(o.Identity)+"echo SCEZ_ROOT_AUTH=ok\n")
	if e != nil {
		return r, fmt.Errorf("root 授权或目录校验失败，未执行修复：%w（%s）", e, strings.TrimSpace(out))
	}
	if marker(out, "AUTH") != "ok" {
		return r, fmt.Errorf("root 授权未返回通过标记，未执行修复：%s", strings.TrimSpace(out))
	}
	// First persist the pre-mutation report. If evidence cannot be saved, stop.
	if o.LogDir == "" {
		return r, errors.New("诊断目录未设置，停止修复")
	}
	if e = os.MkdirAll(o.LogDir, 0700); e != nil {
		return r, e
	}
	data, _ := json.MarshalIndent(r, "", "  ")
	before, e := os.CreateTemp(o.LogDir, "before-*.json")
	if e != nil {
		return r, e
	}
	_, e = before.Write(data)
	ce := before.Close()
	if e != nil {
		return r, e
	}
	if ce != nil {
		return r, ce
	}
	stages := []struct{ name, script string }{
		{"restore-policy-label", `command -v restorecon >/dev/null || exit 51
restorecon -F "$p" || exit 52
if [ -e "$f" ]; then restorecon -F "$f" || exit 53; fi
`},
		// Only a known wrong generic data label is eligible for a temporary
		// AOSP-label fallback. OEM/custom labels are not overwritten blindly.
		{"repair-generic-label", `changed=0
for path in "$p" "$f"; do
  [ -e "$path" ] || continue
  label=$(ls -Zd "$path")
  case "$label" in
    *u:object_r:system_data_file:s0[[:space:]]*) chcon u:object_r:shell_data_file:s0 "$path" || exit 54; changed=1 ;;
  esac
done
echo SCEZ_ROOT_CHCON=$changed
`},
		{"repair-dac-metadata", `label=$(ls -Zd "$p")
case "$label" in *u:object_r:shell_data_file:s0[[:space:]]*) ;; *) echo SCEZ_ROOT_UNSAFE=custom-label; exit 55 ;; esac
if [ "$(stat -c '%u:%g:%a' "$p")" != 2000:2000:771 ]; then
  chown 2000:2000 "$p" && chmod 0771 "$p" || exit 56
fi
if [ -e "$f" ]; then
  label=$(ls -Zd "$f")
  case "$label" in *u:object_r:shell_data_file:s0[[:space:]]*) ;; *) echo SCEZ_ROOT_UNSAFE=custom-file-label; exit 57 ;; esac
  if [ "$(stat -c '%u:%g:%a' "$f")" != 2000:2000:644 ]; then
    chown 2000:2000 "$f" && chmod 0644 "$f" || exit 58
  fi
fi
`},
	}
	for _, stage := range stages {
		if ctx.Err() != nil {
			return r, ctx.Err()
		}
		a.say("尝试步骤：" + stage.name)
		out, e = a.shell(ctx, stage.name, true, 8*time.Second, base+stage.script)
		if marker(out, "UNSAFE") != "" {
			return r, fmt.Errorf("安全校验失败：%s", marker(out, "UNSAFE"))
		}
		// A missing tool or policy denial is recorded, never treated as success.
		ok, e = a.verify(ctx, inode)
		if e != nil {
			return r, e
		}
		if ok {
			r.Status = "repaired"
			a.say("普通 ADB 上传复检通过，继续按原 ADB 身份投屏；仍需实机确认画面和控制")
			return r, nil
		}
	}
	return r, errors.New("root 尝试后普通 ADB 仍不可上传；请查看诊断报告，可能涉及只读挂载、空间不足、模块/ROM 策略或 adbd 域权限")
}
