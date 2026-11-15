package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

const catalogMarker = "SCEZ_APP_CATALOG:"
const appHelperMissingMarker = "SCEZ_APP_HELPER_MISSING"
const maxIconBytes = 512 << 10
const maxIconBatchBytes = 1 << 20
const iconBatchSize = 24

type appCatalog struct {
	Serial    string            `json:"serial"`
	Config    string            `json:"config"`
	Resources []json.RawMessage `json:"resources"`
	Items     []struct {
		Pkg         string `json:"pkg"`
		Name        string `json:"name"`
		Sys         bool   `json:"sys"`
		Version     int64  `json:"version"`
		VersionName string `json:"versionName"`
		Updated     int64  `json:"updated"`
		Icon        int    `json:"icon"`
	} `json:"items"`
	Failed []string `json:"failed"`
}

func parseAppCatalog(out string) (string, []AppListItem, error) {
	serial, items, _, err := parseAppCatalogDetails(out)
	return serial, items, err
}

func parseAppCatalogDetails(out string) (string, []AppListItem, []string, error) {
	start := strings.Index(out, catalogMarker)
	if start < 0 {
		return "", nil, nil, errors.New("app catalog missing")
	}
	var catalog appCatalog
	line := strings.SplitN(out[start+len(catalogMarker):], "\n", 2)[0]
	if len(line) > appCatalogBytesLimit {
		return "", nil, nil, errors.New("app catalog too large")
	}
	if err := json.Unmarshal([]byte(line), &catalog); err != nil {
		return "", nil, nil, err
	}
	if len(catalog.Items) > appListMaxApps {
		return "", nil, nil, errors.New("too many apps")
	}
	if catalog.Config == "" || catalog.Items == nil {
		return "", nil, nil, errors.New("incomplete app catalog")
	}
	resources := make([]string, 0, len(catalog.Resources))
	for _, resource := range catalog.Resources {
		resources = append(resources, string(resource))
	}
	sort.Strings(resources)
	config := catalog.Config + "|" + strings.Join(resources, "|")
	items := make([]AppListItem, 0, len(catalog.Items))
	seen := map[string]bool{}
	for _, item := range catalog.Items {
		if len(item.Pkg) > 255 || !rePkgName.MatchString(item.Pkg) || seen[item.Pkg] || len(item.Name) > 4096 || item.Version < 0 || item.Updated < 0 {
			return "", nil, nil, errors.New("invalid app catalog item")
		}
		seen[item.Pkg] = true
		// Include label: some launchable names/icons depend on configuration or package data.
		stamp := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d|%d|%d", config, item.Pkg, item.Name, item.Version, item.Updated, item.Icon)))
		items = append(items, AppListItem{Pkg: item.Pkg, Name: item.Name, Sys: item.Sys, IconStamp: fmt.Sprintf("%x", stamp), VersionName: item.VersionName, VersionCode: item.Version, LastUpdateTime: item.Updated})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Sys != items[j].Sys {
			return items[i].Sys
		}
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].Pkg < items[j].Pkg
	})
	if len(catalog.Failed) > appListMaxApps {
		return "", nil, nil, errors.New("too many apps")
	}
	for _, pkg := range catalog.Failed {
		if len(pkg) > 255 || !rePkgName.MatchString(pkg) {
			return "", nil, nil, errors.New("invalid app catalog item")
		}
	}
	return adb.StableSerial(catalog.Serial), items, catalog.Failed, nil
}

type appServerHelper struct {
	local   string
	remote  string
	version string
}

var appServerVersionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*([-._][A-Za-z0-9]+)*$`)

func parseAppServerVersion(out string) (string, error) {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "scrcpy" && len(fields[1]) <= 80 && appServerVersionPattern.MatchString(fields[1]) {
			return fields[1], nil
		}
	}
	return "", errors.New("cannot read bundled scrcpy protocol version")
}

func probeAppServerVersion(client string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, client, "--version")
	adb.HideConsole(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read bundled scrcpy version: %w", err)
	}
	return parseAppServerVersion(string(out))
}

func (h appServerHelper) command(options string) string {
	// Android may abort app_process before printing a Java exception when the
	// classpath is absent. Check the content-addressed file in the shell, so
	// first use after an update never depends on vendor-specific error text.
	return "if [ ! -s " + h.remote + " ]; then echo " + appHelperMissingMarker + "; exit 66; fi; " +
		"CLASSPATH=" + h.remote + " app_process / com.genymobile.scrcpy.Server " + h.version + " cleanup=false " + options
}

// Resolve the protocol from the adjacent client once per background catalog job.
// Public and development packages can brand the protocol independently of the GUI.
// A content-addressed helper never replaces the server file used by a live cast.
func (a *App) appCatalogServer() (appServerHelper, error) {
	local := filepath.Join(filepath.Dir(a.cfg.BatPath), "scrcpy-server")
	b, err := os.ReadFile(local)
	if err != nil {
		return appServerHelper{}, err
	}
	version, err := probeAppServerVersion(a.scrcpyExePath())
	if err != nil {
		return appServerHelper{}, err
	}
	hash := sha256.Sum256(b)
	return appServerHelper{local: local, remote: fmt.Sprintf("/data/local/tmp/scrcpy-ez-apps-%x", hash[:8]), version: version}, nil
}

func (a *App) listAppCatalogOnce(identity, serial string) ([]AppListItem, appServerHelper, error) {
	job := a.startAppReadJob(identity, serial)
	if job == nil {
		return nil, appServerHelper{}, errors.New("app read already running")
	}
	items, helper, err := a.listAppCatalogForJob(identity, serial, job)
	a.finishAppReadJob(identity, job, err)
	return items, helper, err
}

func (a *App) listAppCatalogForJob(identity, serial string, job *appReadJob) ([]AppListItem, appServerHelper, error) {
	getHelper := a.appCatalogServer
	if a.appReadOps.helper != nil {
		getHelper = a.appReadOps.helper
	}
	helper, err := getHelper()
	if err != nil {
		return nil, appServerHelper{}, err
	}
	known := 0
	var prior []AppListItem
	if entry, ok := a.profiles.Entry(identity); ok {
		known = len(entry.Apps)
		prior = entry.Apps
	}
	if known > 0 {
		a.updateAppReadState(identity, job, func(s *appReadState) { s.HasCatalog = true; s.AppCount = known })
	}
	var failed []string
	wireless := appReadWireless(serial)
	query := func() (string, []AppListItem, string, error) {
		release, slotErr := takeAppReadSlot(job.ctx)
		if slotErr != nil {
			return "", nil, "", slotErr
		}
		defer release()
		d := newAppReadDeadline(job.ctx, appCatalogBudget(known, wireless, job.performance.CatalogPerApp), 5*time.Minute, wireless, job.performance.CatalogPerApp)
		defer d.close()
		start := time.Now()
		out, err := a.appReadShell(d.ctx, serial, helper.command("app_catalog=true"), func(line string) {
			if p, ok := parseAppReadProgress(line); ok && p.Phase == "catalog" {
				d.progress(p)
				a.updateAppReadState(identity, job, func(s *appReadState) { s.Phase = "catalog"; s.Current = p.Current; s.Total = p.Total })
			}
		})
		if d.ctx.Err() != nil {
			return "", nil, out, d.ctx.Err()
		}
		if err != nil {
			return "", nil, out, err
		}
		physical, items, failedItems, err := parseAppCatalogDetails(out)
		failed = failedItems
		if err == nil && len(items) > 0 {
			job.performance.CatalogPerApp = observeAppReadDuration(job.performance.CatalogPerApp, time.Since(start)/time.Duration(len(items)))
		}
		return physical, items, out, err
	}
	physical, items, output, err := query()
	missing := strings.Contains(output, appHelperMissingMarker) || strings.Contains(output, "ClassNotFoundException") || strings.Contains(output, "Could not find") || strings.Contains(output, "No such file") || strings.Contains(output, "Unable to open")
	if err != nil && missing && a.appReadValid(identity, serial, job) {
		size := int64(512 << 10)
		if info, e := os.Stat(helper.local); e == nil {
			size = info.Size()
		}
		ctx, cancel := context.WithTimeout(job.ctx, appTransferBudget(size, wireless, job.performance.PullBytesPerSecond))
		err = a.appReadPush(ctx, serial, helper.local, helper.remote)
		cancel()
		if err == nil {
			physical, items, _, err = query()
		}
	}
	if err != nil {
		return nil, appServerHelper{}, err
	}
	if !a.appReadValid(identity, serial, job) {
		return nil, appServerHelper{}, errors.New("device owner changed")
	}
	if physical != "" {
		if entry, ok := a.profiles.Entry(identity); !ok || !contains(entry.Serials, physical) {
			return nil, appServerHelper{}, errors.New("app catalog physical identity mismatch")
		}
	}
	if len(failed) > 0 {
		failedSet := map[string]bool{}
		for _, pkg := range failed {
			failedSet[pkg] = true
		}
		current := map[string]bool{}
		for _, item := range items {
			current[item.Pkg] = true
		}
		for _, item := range prior {
			if failedSet[item.Pkg] && !current[item.Pkg] {
				items = append(items, item)
				current[item.Pkg] = true
			}
		}
		a.updateAppReadState(identity, job, func(s *appReadState) {
			s.Partial = true
			s.Error = fmt.Sprintf("%d 个应用读取异常，已保留已有记录", len(failed))
		})
	}
	return items, helper, nil
}

type iconRecord struct {
	Stamp  string `json:"stamp"`
	Digest string `json:"digest"`
}
type iconIndex map[string]iconRecord

func readIconIndex(dir string) iconIndex {
	index := iconIndex{}
	b, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err == nil && len(b) <= 8<<20 {
		if json.Unmarshal(b, &index) != nil {
			index = iconIndex{}
		}
	}
	if index == nil {
		index = iconIndex{}
	}
	return index
}

func writeIconIndex(dir string, index iconIndex) error {
	b, err := json.Marshal(index)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".index-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	_, err = f.Write(b)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, filepath.Join(dir, "index.json"))
}

func validatedIconDigest(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Size() <= 0 || info.Size() > maxIconBytes {
		return "", errors.New("invalid icon size")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 512 || cfg.Height > 512 {
		return "", errors.New("invalid icon dimensions")
	}
	if _, err = png.Decode(bytes.NewReader(b)); err != nil {
		return "", err
	}
	hash := sha256.Sum256(b)
	return fmt.Sprintf("%x", hash), nil
}

// Run only in the background catalog job. No PNG scans on the 700ms state path.
func planAppIcons(dir string, items []AppListItem) (needed, removed []string) {
	index := readIconIndex(dir)
	current := map[string]bool{}
	for _, item := range items {
		current[item.Pkg] = true
		digest, err := validatedIconDigest(filepath.Join(dir, item.Pkg+".png"))
		if record := index[item.Pkg]; err != nil || record.Stamp != item.IconStamp || record.Digest != digest {
			needed = append(needed, item.Pkg)
		}
	}
	// Discover removals from the directory too, including caches predating index.json.
	entries, _ := os.ReadDir(dir)
	removedSet := map[string]bool{}
	for _, entry := range entries {
		pkg := strings.TrimSuffix(entry.Name(), ".png")
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".png") && rePkgName.MatchString(pkg) && !current[pkg] {
			removedSet[pkg] = true
		}
	}
	for pkg := range index {
		if !current[pkg] && rePkgName.MatchString(pkg) {
			removedSet[pkg] = true
		}
	}
	for pkg := range removedSet {
		removed = append(removed, pkg)
	}
	sort.Strings(needed)
	sort.Strings(removed)
	return
}

func sameAppMetadata(old, current []AppListItem) bool {
	if len(old) != len(current) {
		return false
	}
	byPackage := make(map[string]AppListItem, len(old))
	for _, item := range old {
		byPackage[item.Pkg] = item
	}
	for _, item := range current {
		if prior, ok := byPackage[item.Pkg]; !ok || !sameAppAlphaMetadata(prior, item) {
			return false
		}
		delete(byPackage, item.Pkg)
	}
	return len(byPackage) == 0
}

// Only successful, valid PNGs gain a stamp. Interrupted/partial transfers stay dirty for the next check.
func (a *App) commitIconDelta(identity, serial, stage string, items []AppListItem, wanted, removed []string) ([]string, error) {
	if a.appListKeyFor(serial) != identity {
		return nil, nil
	}
	dir := a.iconsDirFor(identity)
	index := readIconIndex(dir)
	stamps := map[string]string{}
	for _, item := range items {
		stamps[item.Pkg] = item.IconStamp
	}
	valid := []string{}
	for _, pkg := range wanted {
		if !rePkgName.MatchString(pkg) {
			continue
		}
		digest, err := validatedIconDigest(filepath.Join(stage, pkg+".png"))
		if err != nil {
			continue
		}
		valid = append(valid, pkg)
		index[pkg] = iconRecord{Stamp: stamps[pkg], Digest: digest}
	}
	committed, err := a.commitAppIcons(identity, serial, stage, valid, removed)
	if err != nil || !committed {
		return nil, err
	}
	for _, pkg := range removed {
		delete(index, pkg)
	}
	if err = writeIconIndex(dir, index); err != nil {
		return valid, err
	}
	return valid, nil
}

type AppIconBatch struct {
	Icons     map[string]string `json:"icons"`
	Remaining []string          `json:"remaining,omitempty"`
}

// At most 24 icons and 1MiB per bridge response. Large batches continue in another response.
func (a *App) GetAppIcons(serial string, pkgs []string) (AppIconBatch, error) {
	result := AppIconBatch{Icons: map[string]string{}}
	if len(pkgs) > iconBatchSize {
		return result, errors.New("icon batch too large")
	}
	identity := a.appListKeyFor(serial)
	if identity == "" {
		return result, nil
	}
	dir := a.iconsDirFor(identity)
	budget := maxIconBatchBytes
	for _, pkg := range pkgs {
		if !rePkgName.MatchString(pkg) {
			continue
		}
		path := filepath.Join(dir, pkg+".png")
		info, err := os.Stat(path)
		if err != nil || info.Size() <= 0 || info.Size() > maxIconBytes {
			continue
		}
		if base64.StdEncoding.EncodedLen(int(info.Size()))+22 > budget {
			result.Remaining = append(result.Remaining, pkg)
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		data := "data:image/png;base64," + base64.StdEncoding.EncodeToString(b)
		if len(data) > budget {
			result.Remaining = append(result.Remaining, pkg)
			continue
		}
		budget -= len(data)
		result.Icons[pkg] = data
	}
	return result, nil
}

func (a *App) markIconsChanged(identity string, pkgs []string) {
	if len(pkgs) == 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.appIconsChanged == nil {
		a.appIconsChanged = map[string][]string{}
	}
	a.appIconsChanged[identity] = append(a.appIconsChanged[identity], pkgs...)
}

// Keep Windows command lines bounded even with many unusually long package names.
func iconExportChunks(pkgs []string) [][]string {
	var chunks [][]string
	var chunk []string
	size := 0
	for _, pkg := range pkgs {
		if len(chunk) > 0 && (size+len(pkg)+1 > 12000 || len(chunk) >= 64) {
			chunks = append(chunks, chunk)
			chunk = nil
			size = 0
		}
		chunk = append(chunk, pkg)
		size += len(pkg) + 1
	}
	if len(chunk) > 0 {
		chunks = append(chunks, chunk)
	}
	return chunks
}

func (a *App) cleanRemoteIconStage(serial, remote string) {
	// remote is generated internally with a fixed prefix and random hexadecimal suffix.
	if !strings.HasPrefix(remote, "/data/local/tmp/scrcpy/icons-job-") {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = a.appReadShell(ctx, serial, "rm -rf "+remote, nil)
}
