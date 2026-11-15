package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testPE(major, minor, patch uint16) []byte {
	b := make([]byte, 1024)
	copy(b, "MZ")
	binary.LittleEndian.PutUint32(b[60:], 128)
	copy(b[128:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(b[132:], 0x8664)
	binary.LittleEndian.PutUint16(b[134:], 1)
	binary.LittleEndian.PutUint16(b[148:], 240)
	binary.LittleEndian.PutUint16(b[152:], 0x20b)
	binary.LittleEndian.PutUint32(b[260:], 16)
	s := b[392:432]
	copy(s, ".rsrc")
	binary.LittleEndian.PutUint32(s[16:], 512)
	binary.LittleEndian.PutUint32(s[20:], 512)
	copy(b[512:], []byte{0xbd, 4, 0xef, 0xfe, 0, 0, 1, 0})
	binary.LittleEndian.PutUint32(b[520:], uint32(major)<<16|uint32(minor))
	binary.LittleEndian.PutUint32(b[524:], uint32(patch)<<16)
	return b
}
func testZIP(t *testing.T, extra map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, name := range requiredFiles {
		data := []byte("new-" + name)
		if name == "scrcpy-ez.exe" {
			data = testPE(2, 3, 0)
		}
		w, e := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if e != nil {
			t.Fatal(e)
		}
		w.Write(data)
	}
	for name, data := range extra {
		w, e := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if e != nil {
			t.Fatal(e)
		}
		w.Write(data)
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func write(t *testing.T, path string, b []byte) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func testPlan(t *testing.T) Plan {
	t.Helper()
	root := t.TempDir()
	p := Plan{Token: strings.Repeat("a", 32), Version: "v2.3.0", Install: filepath.Join(root, "安装"), Work: filepath.Join(root, "缓存")}
	os.MkdirAll(p.Install, 0700)
	os.MkdirAll(p.Stage(), 0700)
	archive := filepath.Join(p.Work, "package.zip")
	write(t, archive, testZIP(t, map[string][]byte{
		"profiles.json": []byte("wipe"), "settings.json": []byte("wipe"), "config.txt": []byte("wipe"),
		"WebView2Loader.dll": []byte("loader"), "runtime/MicrosoftEdgeWebView2RuntimeInstallerX64.exe": []byte("installer"),
		"scrcpy-ez.exe.WebView2/cached-state": []byte("wipe"),
	}))
	var e error
	p.Files, e = Extract(context.Background(), archive, p.Stage())
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"WebView2Loader.dll", "runtime/MicrosoftEdgeWebView2RuntimeInstallerX64.exe"} {
		if _, err := os.Stat(filepath.Join(p.Stage(), name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(p.Stage(), "scrcpy-ez.exe.WebView2", "cached-state")); !os.IsNotExist(err) {
		t.Fatalf("browser cache was extracted: %v", err)
	}
	return p
}
func waitPhase(t *testing.T, m *Manager, want string) State {
	t.Helper()
	until := time.Now().Add(10 * time.Second)
	for time.Now().Before(until) {
		s := m.State()
		if s.Phase == want {
			return s
		}
		if s.Phase == "error" && want != "error" {
			t.Fatalf("%+v", s)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout %+v", m.State())
	return State{}
}
func TestApplyPreservesAndRollback(t *testing.T) {
	p := testPlan(t)
	for _, f := range p.Files {
		write(t, filepath.Join(p.Install, f.Name), []byte("old-"+f.Name))
	}
	for _, n := range []string{"profiles.json", "settings.json", "config.txt", "user-extra.txt"} {
		write(t, filepath.Join(p.Install, n), []byte("user"))
	}
	j, e := Apply(p)
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range []string{"profiles.json", "settings.json", "config.txt", "user-extra.txt"} {
		b, _ := os.ReadFile(filepath.Join(p.Install, n))
		if string(b) != "user" {
			t.Fatal("user data overwritten", n)
		}
	}
	if e = Rollback(j); e != nil {
		t.Fatal(e)
	}
	for _, f := range p.Files {
		b, _ := os.ReadFile(filepath.Join(p.Install, f.Name))
		if string(b) != "old-"+f.Name {
			t.Fatal("rollback", f.Name)
		}
	}
}
func TestPartialFailureAndTamper(t *testing.T) {
	p := testPlan(t)
	first := p.Files[0].Name
	write(t, filepath.Join(p.Install, first), []byte("old"))
	os.Mkdir(filepath.Join(p.Install, p.Files[1].Name), 0700)
	j, e := Apply(p)
	if e == nil {
		t.Fatal("expected occupied target failure")
	}
	if e = Rollback(j); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(p.Install, first))
	if string(b) != "old" {
		t.Fatal("partial rollback")
	}
	write(t, filepath.Join(p.Stage(), first), []byte("tampered"))
	if e = p.Validate(); e == nil {
		t.Fatal("tamper accepted")
	}
}
func TestArchiveRejectsPathsDuplicatesAndMissing(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "C:/escape", "folder\\..\\escape", "CON.txt", "bad.", "scrcpy-ez.EXE"} {
		t.Run(name, func(t *testing.T) {
			r := t.TempDir()
			a := filepath.Join(r, "a.zip")
			write(t, a, testZIP(t, map[string][]byte{name: []byte("bad")}))
			if _, e := Extract(context.Background(), a, filepath.Join(r, "stage")); e == nil {
				t.Fatal("accepted bad archive")
			}
		})
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, _ := z.Create("hello.txt")
	w.Write([]byte("hi"))
	z.Close()
	r := t.TempDir()
	a := filepath.Join(r, "a.zip")
	write(t, a, b.Bytes())
	if _, e := Extract(context.Background(), a, filepath.Join(r, "s")); e == nil {
		t.Fatal("missing files accepted")
	}
}
func TestDownloadCancelResumeAndCache(t *testing.T) {
	data := testZIP(t, map[string][]byte{"padding.bin": make([]byte, 2<<20)})
	sum := sha256.Sum256(data)
	var resumed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := 0
		if r.Header.Get("Range") != "" {
			fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-", &start)
			resumed.Store(true)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(data)-1, len(data)))
		}
		w.Header().Set("ETag", "\"stable\"")
		w.Header().Set("Content-Length", fmt.Sprint(len(data)-start))
		if start > 0 {
			w.WriteHeader(206)
		}
		for i := start; i < len(data); i += 8192 {
			end := i + 8192
			if end > len(data) {
				end = len(data)
			}
			if _, e := w.Write(data[i:end]); e != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(2 * time.Millisecond)
		}
	}))
	defer server.Close()
	root := t.TempDir()
	opts := Options{Install: root, Cache: filepath.Join(root, "cache"), Version: "v2.2.0", AllowTestHTTP: true, Client: server.Client(), Fetch: func(context.Context) (ReleaseInfo, error) {
		return ReleaseInfo{Current: "v2.2.0", Latest: "v2.3.0", HasNew: true, DownloadURL: server.URL, Size: int64(len(data)), Digest: "sha256:" + hex.EncodeToString(sum[:])}, nil
	}}
	m := New(opts)
	defer m.Close()
	m.Check()
	waitPhase(t, m, "available")
	if e := m.Download(); e != nil {
		t.Fatal(e)
	}
	for m.State().Downloaded < 65536 {
		time.Sleep(10 * time.Millisecond)
	}
	m.Cancel()
	waitPhase(t, m, "canceled")
	// 模拟关闭软件后重开，沿用同一缓存与 ETag。
	m.Close()
	m = New(opts)
	m.Check()
	waitPhase(t, m, "available")
	if e := m.Download(); e != nil {
		t.Fatal(e)
	}
	waitPhase(t, m, "ready")
	if !resumed.Load() {
		t.Fatal("no range resume")
	}
	p, e := m.PrepareInstall()
	if e != nil {
		t.Fatal(e)
	}
	if e = p.Validate(); e != nil {
		t.Fatal(e)
	}
	m2 := New(opts)
	defer m2.Close()
	if !m2.State().CanInstall {
		t.Fatal("ready cache lost")
	}
	m3 := New(Options{Install: root, Cache: opts.Cache, Version: "v2.3.1"})
	if m3.State().CanInstall {
		t.Fatal("downgrade allowed")
	}
}
func TestWrongDigestAndNoDowngrade(t *testing.T) {
	data := testZIP(t, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer server.Close()
	root := t.TempDir()
	m := New(Options{Install: root, Cache: filepath.Join(root, "cache"), Version: "v2.2.0", AllowTestHTTP: true, Client: server.Client(), Fetch: func(context.Context) (ReleaseInfo, error) {
		return ReleaseInfo{Current: "v2.2.0", Latest: "v2.3.0", HasNew: true, DownloadURL: server.URL, Digest: "sha256:wrong"}, nil
	}})
	defer m.Close()
	m.Check()
	waitPhase(t, m, "available")
	if e := m.Download(); e != nil {
		t.Fatal(e)
	}
	s := waitPhase(t, m, "error")
	if s.CanInstall {
		t.Fatal("bad digest installable")
	}
	m = New(Options{Install: root, Version: "v9.0.0", Fetch: func(context.Context) (ReleaseInfo, error) {
		return ReleaseInfo{Current: "v9.0.0", Latest: "v2.3.0"}, nil
	}})
	m.Check()
	s = waitPhase(t, m, "available")
	if e := m.Download(); e == nil {
		t.Fatal("downloaded downgrade")
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestReleaseFallbackLocksTagAndTrust(t *testing.T) {
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		resp := &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}
		if r.URL.String() == LatestURL {
			resp.StatusCode = 302
			resp.Header.Set("Location", RepoURL+"/releases/tag/v2.3.0")
		}
		if strings.Contains(r.URL.Path, "/releases/download/v2.3.0/") {
			resp.StatusCode = 302
			resp.Header.Set("Location", "https://release-assets.githubusercontent.com/test")
		}
		return resp, nil
	})}
	i, e := Fetch(context.Background(), client, "v2.2.6")
	if e != nil {
		t.Fatal(e)
	}
	if !i.HasNew || !strings.Contains(i.DownloadURL, "/download/v2.3.0/") {
		t.Fatal(i)
	}
	for _, u := range []string{"http://github.com/x", "https://github.com.evil/x", "https://user@github.com/x", "https://github.com:123/x"} {
		if AllowedURL(u) {
			t.Fatal(u)
		}
	}
}

// 由显式环境变量开启；真实目标只写入 testing 隔离目录。
func TestPublicPackage(t *testing.T) {
	archive := os.Getenv("SCEZ_PUBLIC_PACKAGE")
	if archive == "" {
		t.Skip("public package test is opt-in")
	}
	r := t.TempDir()
	p := Plan{Token: strings.Repeat("b", 32), Version: "v2.2.0", Install: filepath.Join(r, "install"), Work: filepath.Join(r, "work")}
	os.MkdirAll(p.Install, 0700)
	os.MkdirAll(p.Stage(), 0700)
	var e error
	p.Files, e = Extract(context.Background(), archive, p.Stage())
	if e != nil {
		t.Fatal(e)
	}
	v, e := FileVersion(filepath.Join(p.Stage(), "scrcpy-ez.exe"))
	if e != nil {
		t.Fatal(e)
	}
	p.Version = v
	for _, n := range []string{"profiles.json", "settings.json", "config.txt"} {
		write(t, filepath.Join(p.Install, n), []byte("user-data"))
	}
	j, e := Apply(p)
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range []string{"profiles.json", "settings.json", "config.txt"} {
		b, _ := os.ReadFile(filepath.Join(p.Install, n))
		if string(b) != "user-data" {
			t.Fatal("data lost")
		}
	}
	if e = Rollback(j); e != nil {
		t.Fatal(e)
	}
	t.Logf("public package %s; %d runtime files; replace and rollback passed", v, len(p.Files))
}

func TestPublicReleaseCheck(t *testing.T) {
	if os.Getenv("SCEZ_TEST_PUBLIC_NETWORK") != "1" {
		t.Skip("public network test is opt-in")
	}
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	i, e := Fetch(ctx, client, "v2.2.6")
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("public latest=%s current=%s hasNew=%v fixed tag asset=%s", i.Latest, i.Current, i.HasNew, i.DownloadURL)
}

func TestPublicVersionMismatchRejected(t *testing.T) {
	archive := os.Getenv("SCEZ_PUBLIC_PACKAGE")
	if archive == "" {
		t.Skip("public package test is opt-in")
	}
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	token := strings.Repeat("c", 32)
	work := filepath.Join(cache, "job-"+token)
	if e := os.MkdirAll(work, 0700); e != nil {
		t.Fatal(e)
	}
	if e := copyFile(archive, filepath.Join(work, "package.zip")); e != nil {
		t.Fatal(e)
	}
	info := ReleaseInfo{Current: "v0.0.0", Latest: "v2.2.0", HasNew: true, DownloadURL: RepoURL + "/releases/download/v2.2.0/scrcpy-ez-2.2.0.zip"}
	c := cached{Info: info, Plan: Plan{Token: token, Version: info.Latest, Install: root, Work: work}}
	WriteJSON(filepath.Join(cache, "candidate.json"), c)
	m := New(Options{Install: root, Cache: cache, Version: info.Current, Fetch: func(context.Context) (ReleaseInfo, error) { return info, nil }})
	defer m.Close()
	m.Check()
	waitPhase(t, m, "available")
	if e := m.Download(); e != nil {
		t.Fatal(e)
	}
	s := waitPhase(t, m, "error")
	if s.CanInstall || !strings.Contains(s.Error, "版本与发行版本不一致") {
		t.Fatalf("%+v", s)
	}
	t.Log("public package with stale PE version rejected before installation")
}

func TestRollbackIsRepeatableAndBackupTamperRejected(t *testing.T) {
	p := testPlan(t)
	for _, f := range p.Files {
		write(t, filepath.Join(p.Install, f.Name), []byte("old"))
	}
	j, e := Apply(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = Rollback(j); e != nil {
		t.Fatal(e)
	}
	if e = Rollback(j); e != nil {
		t.Fatal("second rollback", e)
	}
	p = testPlan(t)
	for _, f := range p.Files {
		write(t, filepath.Join(p.Install, f.Name), []byte("old"))
	}
	j, e = Apply(p)
	if e != nil {
		t.Fatal(e)
	}
	write(t, filepath.Join(p.Backup(), "files", p.Files[0].Name), []byte("tampered"))
	if e = Rollback(j); e == nil {
		t.Fatal("tampered backup restored")
	}
}
