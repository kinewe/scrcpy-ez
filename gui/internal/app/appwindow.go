package app

// 应用窗口（二期）· Step 1：应用列表（枚举 + 就绪触发 + 入档）。
//
// 【背景】task_vd_appwindow_0919（总纲）+ 主人在线拍板（0919 晚）：
//   - 应用列表是"应用窗口"功能的数据底座（Step 2 面板 / Step 5 应用档案都靠它）；
//   - 枚举时机 = 设备"稳定就绪"边沿（配对完成 / 离线卡→在线卡），不追 adb 早期瞬态；
//   - 图标与列表同批交付（Step 1b：server 导出 + adb pull）——本文件先落列表部分；
//   - 结果入档（设备 identity 键）——离线可读、切换连接不重枚举；
//   - 抗打断（插线学习等会瞬时打断 adb）：失败静默 + 重试一次，不影响任何会话。

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
)

const (
	appListTTL           = 5 * time.Minute         // 读缓存保鲜期
	appListTimeout       = 20 * time.Second        // 单次 --list-apps 超时（实测 ~5s，留 4x 余量）
	appListRetries       = 1                       // 失败重试次数（额外的）
	appListRetryGap      = 6 * time.Second         // 重试间隔（跨 adbd 重启窗口）
	appListMaxApps       = 500                     // 解析上限（防异常输出）
	appListUsbWaitWindow = 2500 * time.Millisecond // 枚举前"等 USB 出现"窗口（v2.1.21 修复：启动瞬间无线卡先到，USB 晚 ~2s）
)

// AppListItem 是应用列表一项（`scrcpy --list-apps` 解析结果）。
type AppListItem struct {
	Pkg       string `json:"pkg"`
	Name      string `json:"name"`
	IconStamp string `json:"iconStamp,omitempty"`
	Sys       bool   `json:"sys"` // * = 系统应用 / - = 第三方
}

// appListEntry 应用列表缓存项（内存态；持久层在档案 profiles.json）。
type appListEntry struct {
	items []AppListItem
	at    time.Time
}

// ---------- 就绪边沿触发 ----------

// devicesWithAppBusyLocked 复制设备列表并填充「应用」按钮的枚举遮罩态（AppBusy）。
// 调用方持 a.mu。仅首次完全无图标缓存时遮罩，使用独立固定期限，进度不会延长它。
func (a *App) devicesWithAppBusyLocked() []adb.Device {
	devs := append([]adb.Device{}, a.devices...)
	now := time.Now()
	for i := range devs {
		id := devs[i].Identity
		if id == "" {
			continue
		}
		devs[i].AppBusy = a.initialAppIconBusyLocked(id, now)
	}
	return devs
}

// kickAppListOnReadyChange 检查"就绪边沿"：某设备（identity）从"未就绪"变为
// "就绪"（State==device 且非 Connecting/Pairing 合成卡）时触发一次后台枚举。
//
// 【语义】（主人 0919 拍板）：设备"稳定"的两个入口——配对完成 / 离线卡→在线卡——
// 在显示层都表现为"该 identity 的就绪翻转"；本函数挂在 commitDisplay 末尾（显示
// 提交后），一处覆盖全部场景。多 transport 卡（USB+无线）按 identity 聚合（任一
// 就绪=设备就绪），避免顺序抖动导致重复触发。
func (a *App) kickAppListOnReadyChange(devs []adb.Device) {
	type job struct{ identity, serial string }
	var jobs []job

	a.mu.Lock()
	if a.appListLastReady == nil {
		a.appListLastReady = map[string]bool{}
	}
	if a.appListBusy == nil {
		a.appListBusy = map[string]time.Time{}
	}
	if a.appListSilentChanged == nil {
		a.appListSilentChanged = map[string]bool{}
	}
	// 聚合：每 identity 的"就绪"（任一 transport 卡就绪）+ 一个可用 serial。
	readyMap := map[string]bool{}
	serialOf := map[string]string{}
	for i := range devs {
		d := &devs[i]
		if d.Identity == "" || strings.HasPrefix(d.Identity, "pending:") || d.Serial == "" {
			continue
		}
		if d.State == "device" && !d.Connecting && !d.Pairing {
			readyMap[d.Identity] = true
			// serial 选取（v2.1.21 修复）：优先 USB——无线 transport 可能是
			// stale 档案地址（换网/关闭无线调试后该 transport 仍显示 device
			// 但实际 connect 超时）→ 曾出现"GUI 重启后应用列表读不出（枚举
			// 超时）"。USB 物理连接必通；首个就绪卡兜底。
			cur, ok := serialOf[d.Identity]
			if !ok || (strings.Contains(cur, ":") && !strings.Contains(d.Serial, ":")) {
				serialOf[d.Identity] = d.Serial
			}
		} else if _, ok := readyMap[d.Identity]; !ok {
			readyMap[d.Identity] = false
		}
	}
	for id, ready := range readyMap {
		if ready && !a.appListLastReady[id] {
			if _, busy := a.appListBusy[id]; !busy {
				a.appListBusy[id] = time.Now() // 值=开始时刻（「应用」按钮遮罩 10s 兜底用）
				a.appListSilentChanged[id] = false
				delete(a.appIconsChanged, id)
				jobs = append(jobs, job{identity: id, serial: serialOf[id]})
			}
		}
		a.appListLastReady[id] = ready
	}
	// 清掉已消失设备的记录（防 map 无限增长）。
	for k := range a.appListLastReady {
		if _, ok := readyMap[k]; !ok {
			delete(a.appListLastReady, k)
		}
	}
	a.mu.Unlock()

	for _, j := range jobs {
		a.prepareInitialAppIconGate(j.identity)
		bridge.DebugLog("[appwin] 就绪边沿 → 应用列表枚举 serial=%q identity=%q", j.serial, j.identity)
		go a.runAppListEnum(j.identity, j.serial)
		// v2.1.32：虚拟屏 dpi 参数预热（后台）——设备就绪时顺手把 wm size/density
		// 查好缓存，开窗路径只读缓存（GUI 消息循环线程不被慢查询冻结）。
		go a.guard("appwin-phys-warm", func() { a.devicePhys(j.serial) })
	}
}

// bestEnumSerial 复核该 identity 当前最佳的枚举 serial（USB 优先）：
// 无线 transport 可能是 stale 档案地址（换网/无线调试关闭后仍显示 device
// 但 connect 超时）；USB 物理连接必通。未找到时用 fallback。
func (a *App) bestEnumSerial(identity, fallback string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	wifi := ""
	for i := range a.devices {
		d := &a.devices[i]
		if d.Identity != identity || d.State != "device" || d.Connecting || d.Pairing || d.Serial == "" {
			continue
		}
		if !strings.Contains(d.Serial, ":") {
			return d.Serial // USB 最优
		}
		if wifi == "" {
			wifi = d.Serial
		}
	}
	if wifi != "" {
		return wifi
	}
	return fallback
}

// waitBestEnumSerial 在"USB 可能尚未出现"的窗口内短暂等待（每 250ms 复核）：
// 拿到 USB 立即返回；超时返回当前最佳。纯无线设备最多多等 timeout。
func (a *App) waitBestEnumSerial(identity, fallback string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	best := a.bestEnumSerial(identity, fallback)
	for strings.Contains(best, ":") && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
		if s := a.bestEnumSerial(identity, fallback); s != best {
			best = s
			if !strings.Contains(best, ":") {
				return best
			}
		}
	}
	return best
}

// appListDiff compares package, display name and system-app flag; order is ignored.
// v2.1.16 列表 diff（联合判据·列表侧）：same 且未超兜底期 → 跳过图标导出。
func appListDiff(oldList, cur []AppListItem) (same bool, added, removed, renamed []string) {
	om := make(map[string]AppListItem, len(oldList))
	for _, e := range oldList {
		om[e.Pkg] = e
	}
	cm := make(map[string]AppListItem, len(cur))
	for _, e := range cur {
		cm[e.Pkg] = e
	}
	for pkg, item := range cm {
		if oldItem, ok := om[pkg]; !ok {
			added = append(added, pkg)
		} else if oldItem.Name != item.Name || oldItem.Sys != item.Sys {
			renamed = append(renamed, pkg)
		}
	}
	for pkg := range om {
		if _, ok := cm[pkg]; !ok {
			removed = append(removed, pkg)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(renamed)
	same = len(added) == 0 && len(removed) == 0 && len(renamed) == 0
	return
}

// appListUpdateRequired 入口静默检测仅在有差异时提交；设备就绪枚举继续沿用原流程。
func appListUpdateRequired(silentDiff, same bool) bool {
	return !silentDiff || !same
}

// Read cached UI data immediately; reconcile package metadata and icon files in the background.
func (a *App) runAppListEnum(identity, serial string) {
	a.runAppListEnumMode(identity, serial, false)
}

func (a *App) runAppListEnumMode(identity, serial string, silentDiff bool) {
	defer a.clearAppBusy(identity)
	a.prepareInitialAppIconGate(identity)
	a.waitSrvReady(context.Background())
	// Prefer available USB immediately. Do not delay every WiFi-only check by 2.5 seconds.
	serial = a.bestEnumSerial(identity, serial)
	var items []AppListItem
	var helper appServerHelper
	var err error
	for attempt := 0; attempt <= appListRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(appListRetryGap)
			serial = a.bestEnumSerial(identity, serial)
		}
		if a.appListKeyFor(serial) != identity {
			return
		}
		items, helper, err = a.listAppCatalogOnce(identity, serial)
		if err == nil {
			break
		}
		bridge.DebugLog("[appwin] catalog attempt=%d identity=%q err=%v", attempt+1, identity, err)
	}
	if err != nil || a.appListKeyFor(serial) != identity {
		return
	}
	var old []AppListItem
	if entry, ok := a.profiles.Entry(identity); ok {
		old = entry.Apps
	}
	same, _, _, _ := appListDiff(old, items)
	if err = a.profiles.SetApps(identity, items); err != nil {
		bridge.DebugLog("[appwin] catalog persist identity=%q err=%v", identity, err)
		return
	}
	a.mu.Lock()
	if a.appListCache == nil {
		a.appListCache = map[string]appListEntry{}
	}
	a.appListCache[identity] = appListEntry{items: items, at: time.Now()}
	a.mu.Unlock()
	a.markAppListCheckChanged(identity, !same)
	if !same {
		a.reconcileNotifications()
	}
	needed, removed := planAppIcons(a.iconsDirFor(identity), items)
	bridge.DebugLog("[appwin] catalog identity=%q apps=%d iconDelta=%d removed=%d", identity, len(items), len(needed), len(removed))
	if len(needed) == 0 && len(removed) == 0 {
		return
	}
	a.runAppIconDelta(identity, serial, helper, items, needed, removed)
}

// clearAppBusy 清「应用」按钮遮罩态（幂等）。
func (a *App) clearAppBusy(identity string) {
	a.mu.Lock()
	delete(a.appListBusy, identity)
	delete(a.appListSilent, identity)
	a.releaseInitialAppIconGateLocked(identity)
	a.mu.Unlock()
}

func (a *App) markAppListCheckChanged(identity string, changed bool) {
	a.mu.Lock()
	if a.appListSilentChanged == nil {
		a.appListSilentChanged = map[string]bool{}
	}
	a.appListSilentChanged[identity] = changed
	a.mu.Unlock()
}

// touchAppBusy 记录后台作业进展；不会延长首次无图标遮罩的固定时限。
func (a *App) touchAppBusy(identity string) {
	a.mu.Lock()
	if _, ok := a.appListBusy[identity]; ok {
		a.appListBusy[identity] = time.Now()
	}
	a.mu.Unlock()
}

// ---------- 二期 Step 1c：图标导出与入库 ----------

const (
	iconExportTimeout = 90 * time.Second // 起 server 导出图标（实测 ~6s，大余量防老设备）
	iconPushTimeout   = 30 * time.Second // push server
	iconPullTimeout   = 60 * time.Second // pull 图标目录
)

// All deltas, including an initially empty archive, use a private remote staging directory.
// A single directory transfer replaces the old one-adb-process-per-package path.
func (a *App) runAppIconDelta(identity, serial string, helper appServerHelper, items []AppListItem, wanted, removed []string) {
	if a.appListKeyFor(serial) != identity {
		return
	}
	dir := a.iconsDirFor(identity)
	if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
		return
	}
	stage, err := os.MkdirTemp(filepath.Dir(dir), ".export-")
	if err != nil {
		return
	}
	defer os.RemoveAll(stage)
	start := time.Now()
	if len(wanted) > 0 {
		token := make([]byte, 12)
		if _, err = rand.Read(token); err != nil {
			return
		}
		remote := fmt.Sprintf("/data/local/tmp/scrcpy/icons-job-%x", token)
		defer a.cleanRemoteIconStage(serial, remote)
		for _, chunk := range iconExportChunks(wanted) {
			if a.appListKeyFor(serial) != identity {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), iconExportTimeout)
			out, exportErr := a.adb.ShellOut(ctx, serial, helper.command("export_app_icons="+strings.Join(chunk, ",")+" export_app_icons_dir="+remote))
			cancel()
			if exportErr != nil || !strings.Contains(out, "Exported icons:") || !a.iconExportOwnerMatches(identity, out) {
				bridge.DebugLog("[appwin] icon export identity=%q err=%v ownerValid=%v", identity, exportErr, a.iconExportOwnerMatches(identity, out))
				return
			}
			a.touchAppBusy(identity)
		}
		if a.appListKeyFor(serial) != identity {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), iconPullTimeout)
		err = a.adb.PullDir(ctx, serial, remote+"/.", stage)
		cancel()
		if err != nil {
			bridge.DebugLog("[appwin] icon bulk pull identity=%q err=%v", identity, err)
			return
		}
	}
	changed, err := a.commitIconDelta(identity, serial, stage, items, wanted, removed)
	a.markIconsChanged(identity, changed)
	a.mu.Lock()
	a.releaseInitialAppIconGateLocked(identity)
	a.mu.Unlock()
	bridge.DebugLog("[appwin] icon delta identity=%q requested=%d committed=%d removed=%d total=%dms err=%v", identity, len(wanted), len(changed), len(removed), time.Since(start).Milliseconds(), err)
}

func (a *App) iconExportOwnerMatches(identity, out string) bool {
	const marker = "SCEZ_ICON_OWNER:"
	pos := strings.Index(out, marker)
	if pos < 0 {
		return false
	}
	physical := adb.StableSerial(strings.TrimSpace(strings.SplitN(out[pos+len(marker):], "\n", 2)[0]))
	if physical == "" {
		return true
	} // property may be inaccessible; transport ownership guard still applies
	entry, ok := a.profiles.Entry(identity)
	return ok && contains(entry.Serials, physical)
}

// A delayed pull must not write a new IP owner's icons into the old archive.
func (a *App) commitAppIcons(identity, serial, stageDir string, onlyPkgs, removedPkgs []string) (bool, error) {
	if a.appListKeyFor(serial) != identity {
		return false, nil
	}
	dir := a.iconsDirFor(identity)
	if onlyPkgs == nil {
		entries, err := os.ReadDir(stageDir)
		if err != nil {
			return false, err
		}
		onlyPkgs = []string{}
		for _, entry := range entries {
			pkg := strings.TrimSuffix(entry.Name(), ".png")
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".png") && rePkgName.MatchString(pkg) {
				onlyPkgs = append(onlyPkgs, pkg)
			}
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	for _, pkg := range onlyPkgs {
		if !rePkgName.MatchString(pkg) {
			continue
		}
		from := filepath.Join(stageDir, pkg+".png")
		if _, err := os.Stat(from); os.IsNotExist(err) {
			continue // keep the prior icon when a single pull failed
		}
		if err := os.Rename(from, filepath.Join(dir, pkg+".png")); err != nil {
			return false, err
		}
	}
	for _, pkg := range removedPkgs {
		if !rePkgName.MatchString(pkg) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, pkg+".png")); err != nil && !os.IsNotExist(err) {
			return false, err
		}
	}
	return true, nil
}

// countPNGs 数目录内 .png 文件（日志用）。
func countPNGs(dir string) int {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".png") {
			n++
		}
	}
	return n
}

// GetAppIcon 读应用图标（data URL；找不到=空串，前端回退彩块）。
// "满射"口径：每个应用都查得到结果（有 PNG 给 PNG，无则前端兜底）。
func (a *App) GetAppIcon(serial, pkg string) (string, error) {
	if pkg == "" || !rePkgName.MatchString(pkg) {
		return "", nil // 非法包名（顺带防路径穿越）
	}
	key := a.appListKeyFor(serial)
	if key == "" {
		return "", nil
	}
	b, err := os.ReadFile(filepath.Join(a.iconsDirFor(key), pkg+".png"))
	if err != nil {
		if legacy := a.legacyIconsDirFor(key); legacy != "" {
			b, err = os.ReadFile(filepath.Join(legacy, pkg+".png"))
		}
	}
	if err != nil {
		return "", nil
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b), nil
}

// iconsDirFor 用完整档案键的哈希隔离图标，避免大小写和非法文件名替换造成碰撞。
func (a *App) iconsDirFor(identity string) string {
	digest := sha256.Sum256([]byte(identity))
	return filepath.Join(filepath.Dir(a.profiles.Path()), "icons", fmt.Sprintf("device-%x", digest))
}

// Legacy name directories are readable only when their name identifies exactly
// one old archive. New identities never read a same-model device's cached icons.
func (a *App) legacyIconsDirFor(identity string) string {
	if strings.HasPrefix(identity, "device:") || strings.HasPrefix(identity, "pending:") {
		return ""
	}
	a.profiles.mu.Lock()
	defer a.profiles.mu.Unlock()
	if a.profiles.data.Devices[identity] == nil {
		return ""
	}
	name := sanitizeFileName(identity)
	for key := range a.profiles.data.Devices {
		if key != identity && strings.EqualFold(sanitizeFileName(key), name) {
			return ""
		}
	}
	return filepath.Join(filepath.Dir(a.profiles.Path()), "icons", name)
}

// sanitizeFileName 去掉 Windows 文件名非法字符（空格保留，保持可读）。
func sanitizeFileName(s string) string {
	s = strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_").Replace(s)
	s = strings.TrimRight(s, ". ")
	if s == "" {
		s = "device"
	}
	return s
}

// ---------- 执行与解析 ----------

func (a *App) scrcpyExePath() string {
	return filepath.Join(filepath.Dir(a.cfg.BatPath), "scrcpy.exe")
}

// listAppsWithRetry 跑 --list-apps（失败重试；间隔跨 adbd 重启窗口）。
func listAppsWithRetry(exe, serial string) ([]AppListItem, error) {
	var lastErr error
	for attempt := 0; attempt <= appListRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(appListRetryGap)
		}
		items, err := listAppsOnce(exe, serial)
		if err == nil {
			return items, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// listAppsOnce 单次执行（带超时；超时即杀，不留残余）。
func listAppsOnce(exe, serial string) ([]AppListItem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), appListTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--serial", serial, "--list-apps")
	adb.HideConsole(cmd) // GUI 无控制台：禁止子进程新建控制台窗口（防闪窗）
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, errors.New("读取应用列表超时")
	}
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("读取应用列表失败: %w", err)
	}
	items := ParseAppList(string(out))
	if len(items) == 0 {
		return nil, errors.New("未解析到任何应用（设备可能未就绪）")
	}
	if len(items) > appListMaxApps {
		items = items[:appListMaxApps]
	}
	return items, nil
}

var rePkgName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+$`)

// ParseAppList 解析 `scrcpy --list-apps` 输出（纯函数，便于单测）：
//
//   - <名称（30 列对齐，含中文）>  <包名>
//   - <名称>                       <包名>
//
// 末尾 token=包名（形如 a.b.c，正则校验），其余为展示名（多空格折叠）。
func ParseAppList(out string) []AppListItem {
	var items []AppListItem
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimLeft(line, " \t")
		var sys bool
		switch {
		case strings.HasPrefix(trimmed, "* "):
			sys = true
		case strings.HasPrefix(trimmed, "- "):
			sys = false
		default:
			continue
		}
		body := strings.TrimSpace(trimmed[2:])
		fields := strings.Fields(body)
		if len(fields) < 2 {
			continue
		}
		pkg := fields[len(fields)-1]
		if !rePkgName.MatchString(pkg) || seen[pkg] {
			continue
		}
		name := strings.TrimSpace(strings.TrimSuffix(body, pkg))
		if name == "" {
			name = pkg
		}
		seen[pkg] = true
		items = append(items, AppListItem{Pkg: pkg, Name: name, Sys: sys})
	}
	return items
}

// ---------- RPC 支撑（Step 2 面板使用；此处先备好） ----------

// GetAppList 读应用列表：内存缓存（TTL 内）→ 档案回退；都没有=空数组（前端可触发刷新）。
func (a *App) GetAppList(serial string) ([]AppListItem, error) {
	key := a.appListKeyFor(serial)
	if key == "" {
		return nil, errors.New("未指定设备")
	}
	a.mu.RLock()
	if it, ok := a.appListCache[key]; ok && time.Since(it.at) < appListTTL {
		items := it.items
		a.mu.RUnlock()
		return items, nil
	}
	a.mu.RUnlock()
	if e, ok := a.profiles.Entry(key); ok && len(e.Apps) > 0 {
		return e.Apps, nil
	}
	return []AppListItem{}, nil
}

// RefreshAppList 手动刷新（前端按钮）：异步跑一次（busy 防重），立即返回。
func (a *App) RefreshAppList(serial string) error {
	key := a.appListKeyFor(serial)
	if key == "" {
		return errors.New("未指定设备")
	}
	a.prepareInitialAppIconGate(key)
	a.mu.Lock()
	if a.appListBusy == nil {
		a.appListBusy = map[string]time.Time{}
	}
	if _, busy := a.appListBusy[key]; busy {
		a.mu.Unlock()
		return nil // 已在跑：幂等
	}
	a.appListBusy[key] = time.Now()
	a.resetAppListCheckResultLocked(key)
	a.mu.Unlock()
	go a.runAppListEnum(key, serial)
	return nil
}

func (a *App) resetAppListCheckResultLocked(key string) {
	if a.appListSilentChanged == nil {
		a.appListSilentChanged = map[string]bool{}
	}
	a.appListSilentChanged[key] = false
	delete(a.appIconsChanged, key)
}

// beginAppListCheckLocked 复用枚举 busy 作为设备级防重闸；静默标志独立于 busy，
// 避免点击检测在设备快照中呈现为「正在读取」遮罩。调用方须持 a.mu。
func (a *App) beginAppListCheckLocked(key string) bool {
	if a.appListBusy == nil {
		a.appListBusy = map[string]time.Time{}
	}
	if a.appListSilent == nil {
		a.appListSilent = map[string]bool{}
	}
	if a.appListSilentChanged == nil {
		a.appListSilentChanged = map[string]bool{}
	}
	if _, busy := a.appListBusy[key]; busy {
		if _, known := a.appListSilentChanged[key]; !known {
			a.appListSilentChanged[key] = false
		}
		a.appListSilent[key] = true
		return false
	}
	a.appListBusy[key] = time.Now()
	a.appListSilent[key] = true
	a.resetAppListCheckResultLocked(key)
	return true
}

// CheckAppList 每次应用列表入口打开时发起静默差分检测。返回 watching=true 表示
// 有检测或已有枚举正在运行，前端可等它结束后读取最新缓存；设备级 busy 防止重复启动。
func (a *App) CheckAppList(serial string) (bool, error) {
	key := a.appListKeyFor(serial)
	if key == "" {
		return false, errors.New("未指定设备")
	}
	a.prepareInitialAppIconGate(key)
	a.mu.Lock()
	start := a.beginAppListCheckLocked(key)
	a.mu.Unlock()
	if start {
		go a.runAppListEnumMode(key, serial, true)
	}
	return true, nil
}

type AppListCheckStatus struct {
	Busy            bool     `json:"busy"`
	Changed         bool     `json:"changed"`
	Icons           []string `json:"icons,omitempty"`
	InitialIconBusy bool     `json:"initialIconBusy,omitempty"`
}

// IsAppListCheckBusy 用于前端静默等待列表及图标均处理完毕，并确认是否真的需要换列表。
func (a *App) IsAppListCheckBusy(serial string) (AppListCheckStatus, error) {
	key := a.appListKeyFor(serial)
	if key == "" {
		return AppListCheckStatus{}, errors.New("未指定设备")
	}
	a.mu.RLock()
	status := AppListCheckStatus{Busy: a.appListSilent[key], Changed: a.appListSilentChanged[key], Icons: append([]string(nil), a.appIconsChanged[key]...), InitialIconBusy: a.initialAppIconBusyLocked(key, time.Now())}
	a.mu.RUnlock()
	return status, nil
}

// appListKeyFor 把"活的 serial"归一为设备身份键（identity）：优先当前设备列表，
// 回退档案 ResolveKey（覆盖刚断开/离线场景）；都找不到=以 serial 兜底。
func (a *App) appListKeyFor(serial string) string {
	if serial == "" {
		return ""
	}
	a.mu.RLock()
	for i := range a.devices {
		d := &a.devices[i]
		if d.Serial == serial || d.Wireless == serial {
			if id := a.identityOf(d); id != "" {
				a.mu.RUnlock()
				return id
			}
		}
	}
	a.mu.RUnlock()
	if k := a.profiles.ResolveKey(serial); k != "" {
		return k
	}
	return serial
}
