package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const MaxDownload = 512 << 20
const maxUnpacked = 1536 << 20

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.reader.Read(p)
}

var requiredFiles = []string{"scrcpy-ez.exe", "scrcpy.exe", "scrcpy-server", "scrcpy-server-legacy", "adb.exe", "AdbWinApi.dll", "AdbWinUsbApi.dll", "SDL3.dll", "投屏支持.bat"}

type File struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func protected(name string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(name, "\\", "/"))
	switch normalized {
	case "webview2loader.dll", "runtime/microsoftedgewebview2runtimeinstallerx64.exe":
		return false
	}
	for _, part := range strings.Split(normalized, "/") {
		if strings.HasPrefix(part, ".") || part == "profiles.json" || part == "settings.json" || part == "config.txt" || part == "cache" || part == "logs" || part == "backups" || strings.Contains(part, "webviewcache") || strings.Contains(part, "webview2") || strings.Contains(part, ".bak") || strings.HasSuffix(part, ".old") || strings.HasSuffix(part, ".log") || strings.HasSuffix(part, ".zip") || strings.HasSuffix(part, ".flag") {
			return true
		}
	}
	return false
}

func safeName(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimSuffix(name, "/")
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsAny(name, ":\x00<>\"|?*") {
		return "", fmt.Errorf("非法包路径：%q", name)
	}
	for _, p := range strings.Split(name, "/") {
		base := strings.ToUpper(strings.SplitN(p, ".", 2)[0])
		if p == "" || p == "." || p == ".." || strings.TrimRight(p, ". ") != p || base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9') {
			return "", fmt.Errorf("非法包路径：%q", name)
		}
		for _, c := range p {
			if c < 32 {
				return "", fmt.Errorf("非法包路径")
			}
		}
	}
	return filepath.FromSlash(name), nil
}

func fileHash(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func FileVersion(path string) (string, error) {
	p, err := pe.Open(path)
	if err != nil {
		return "", err
	}
	defer p.Close()
	s := p.Section(".rsrc")
	if s == nil {
		return "", fmt.Errorf("更新程序缺少版本资源")
	}
	data, err := s.Data()
	if err != nil {
		return "", err
	}
	magic := []byte{0xbd, 0x04, 0xef, 0xfe, 0x00, 0x00, 0x01, 0x00}
	i := bytes.Index(data, magic)
	if i < 0 || i+52 > len(data) {
		return "", fmt.Errorf("更新程序版本资源无效")
	}
	ms, ls := binary.LittleEndian.Uint32(data[i+8:]), binary.LittleEndian.Uint32(data[i+12:])
	return fmt.Sprintf("v%d.%d.%d", ms>>16, ms&65535, ls>>16), nil
}

func safePath(root, name string) (string, error) {
	rel, err := safeName(name)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(root, rel)
	// 不跟随用户目录里的 junction / symlink，避免覆盖到安装目录之外。
	for cur := dest; ; cur = filepath.Dir(cur) {
		st, err := os.Lstat(cur)
		if err == nil && st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("目录包含链接：%s", cur)
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if strings.EqualFold(cur, root) {
			break
		}
		if filepath.Dir(cur) == cur {
			return "", fmt.Errorf("路径越出安装目录")
		}
	}
	return dest, nil
}

func Extract(ctx context.Context, archive, stage string) ([]File, error) {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return nil, fmt.Errorf("更新包无法解压：%w", err)
	}
	defer z.Close()
	if len(z.File) > 4096 {
		return nil, fmt.Errorf("更新包文件过多")
	}
	prefix := ""
	for _, f := range z.File {
		n := strings.ReplaceAll(f.Name, "\\", "/")
		if strings.HasSuffix(strings.ToLower(n), "/scrcpy-ez.exe") {
			if prefix != "" {
				return nil, fmt.Errorf("更新包包含多个程序目录")
			}
			prefix = n[:len(n)-len("scrcpy-ez.exe")]
		}
	}
	seen := map[string]bool{}
	var files []File
	var total uint64
	for _, f := range z.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, err := safeName(f.Name); err != nil {
			return nil, err
		}
		if f.Mode()&os.ModeSymlink != 0 || (!f.FileInfo().IsDir() && !f.Mode().IsRegular()) {
			return nil, fmt.Errorf("更新包包含非普通文件")
		}
		n := strings.ReplaceAll(f.Name, "\\", "/")
		if prefix != "" {
			if n == strings.TrimSuffix(prefix, "/")+"/" {
				continue
			}
			if !strings.HasPrefix(n, prefix) {
				return nil, fmt.Errorf("更新包目录结构不一致")
			}
			n = strings.TrimPrefix(n, prefix)
		}
		if n == "" || f.FileInfo().IsDir() {
			continue
		}
		name, err := safeName(n)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(name)
		if seen[key] {
			return nil, fmt.Errorf("更新包含重复文件：%s", name)
		}
		seen[key] = true
		total += f.UncompressedSize64
		if total > maxUnpacked || f.UncompressedSize64 > 512<<20 {
			return nil, fmt.Errorf("更新包解压体积过大")
		}
		if protected(name) {
			continue
		}
		dest, err := safePath(stage, name)
		if err != nil {
			return nil, err
		}
		if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return nil, err
		}
		in, err := f.Open()
		if err != nil {
			return nil, err
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			in.Close()
			return nil, err
		}
		h := sha256.New()
		count, copyErr := io.Copy(io.MultiWriter(out, h), contextReader{ctx, io.LimitReader(in, int64(f.UncompressedSize64)+1)})
		closeErr := out.Close()
		in.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if uint64(count) != f.UncompressedSize64 {
			return nil, fmt.Errorf("更新包文件不完整：%s", name)
		}
		files = append(files, File{Name: name, Size: count, SHA256: hex.EncodeToString(h.Sum(nil))})
	}
	for _, name := range requiredFiles {
		if !seen[strings.ToLower(name)] {
			return nil, fmt.Errorf("更新包缺少 %s", name)
		}
	}
	p, err := pe.Open(filepath.Join(stage, "scrcpy-ez.exe"))
	if err != nil {
		return nil, fmt.Errorf("更新包 GUI 不是有效 Windows 程序：%w", err)
	}
	defer p.Close()
	if p.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return nil, fmt.Errorf("更新包不是 Windows 64 位版本")
	}
	return files, nil
}
