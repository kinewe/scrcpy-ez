package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const RepoURL = "https://github.com/kinewe/scrcpy-ez"
const LatestURL = RepoURL + "/releases/latest"
const GiteeRepoURL = "https://gitee.com/kinewe/scrcpy-ez"
const GiteeLatestURL = GiteeRepoURL + "/releases/latest"
const UserAgent = "scrcpy-ez/in-app-update"

type DownloadSource struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	URL    string `json:"url"`
	Size   int64  `json:"size,omitempty"`
	Digest string `json:"digest,omitempty"`
	Route  string `json:"route,omitempty"`
}

type ReleaseInfo struct {
	Current        string           `json:"current"`
	Latest         string           `json:"latest"`
	HasNew         bool             `json:"hasNew"`
	Error          string           `json:"error,omitempty"`
	RepoURL        string           `json:"repoUrl"`
	DownloadURL    string           `json:"downloadUrl"`
	Size           int64            `json:"size,omitempty"`
	Digest         string           `json:"digest,omitempty"`
	PageURL        string           `json:"pageUrl,omitempty"`
	Sources        []DownloadSource `json:"sources,omitempty"`
	Notice         string           `json:"notice,omitempty"`
	PackageMissing bool             `json:"packageMissing,omitempty"`
}

var stableVersion = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)
var installedCandidateVersion = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)-rc\.\d+$`)
var sha256Digest = regexp.MustCompile(`(?i)^sha256:[0-9a-f]{64}$`)

func ParseVersion(s string) []int {
	m := stableVersion.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return nil
	}
	out := make([]int, 3)
	for i := range out {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}
func VersionLess(a, b string) bool {
	x, y := ParseVersion(a), ParseVersion(b)
	// Installed candidates can graduate to stable, while remote feeds and URL
	// allowlists still accept only stable tags through ParseVersion.
	candidate := false
	if x == nil {
		if m := installedCandidateVersion.FindStringSubmatch(strings.TrimSpace(a)); m != nil {
			x = ParseVersion(m[1])
			candidate = true
		}
	}
	if x == nil || y == nil {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return candidate
}

func AllowedURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "github.com", "api.github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com", "gitee.com", "foruda.gitee.com":
		return true
	}
	return false
}

// Browser links are a smaller allowlist than the updater's API/CDN requests.
func AllowedPageURL(raw string) bool {
	for _, repo := range []string{RepoURL, GiteeRepoURL} {
		if raw == repo || raw == repo+"/releases/latest" {
			return true
		}
		prefix := repo + "/releases/tag/"
		if strings.HasPrefix(raw, prefix) {
			tag := strings.TrimPrefix(raw, prefix)
			return tag == strings.TrimSpace(tag) && ParseVersion(tag) != nil
		}
	}
	return false
}

func do(ctx context.Context, client *http.Client, method, raw string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	return client.Do(req)
}

func Fetch(ctx context.Context, client *http.Client, current string) (ReleaseInfo, error) {
	return FetchWithClients(ctx, client, nil, current)
}

func fetchGitHub(ctx context.Context, client *http.Client, current string) (ReleaseInfo, error) {
	info := ReleaseInfo{Current: current, RepoURL: RepoURL, DownloadURL: LatestURL}
	resp, err := do(ctx, client, http.MethodGet, "https://api.github.com/repos/kinewe/scrcpy-ez/releases/latest")
	if err == nil {
		var release struct {
			Tag        string `json:"tag_name"`
			Draft      bool   `json:"draft"`
			Prerelease bool   `json:"prerelease"`
			Assets     []struct {
				Name   string `json:"name"`
				URL    string `json:"browser_download_url"`
				Size   int64  `json:"size"`
				Digest string `json:"digest"`
			} `json:"assets"`
		}
		if resp.StatusCode == http.StatusOK {
			err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&release)
		} else {
			err = fmt.Errorf("版本服务返回 %d", resp.StatusCode)
		}
		resp.Body.Close()
		if err == nil && (release.Draft || release.Prerelease) {
			return info, fmt.Errorf("GitHub 尚未提供稳定发行版本")
		}
		if err == nil && !release.Draft && !release.Prerelease && ParseVersion(release.Tag) != nil {
			info.Latest = release.Tag
			info.PageURL = RepoURL + "/releases/tag/" + release.Tag
			info.HasNew = VersionLess(current, release.Tag)
			version := strings.TrimPrefix(release.Tag, "v")
			for _, asset := range release.Assets {
				if (asset.Name == "scrcpy-ez-"+version+".zip" || asset.Name == "scrcpy-ez-v"+version+".zip") && AllowedURL(asset.URL) && asset.URL == RepoURL+"/releases/download/"+release.Tag+"/"+asset.Name && asset.Size > 0 && asset.Size <= MaxDownload {
					info.DownloadURL, info.Size, info.Digest = asset.URL, asset.Size, asset.Digest
					return info, nil
				}
			}
			return info, fmt.Errorf("发行版本尚未提供可用的 Windows 更新包")
		}
	}
	// 保留原产品的免 API 兜底；锁定 tag 后再拼包地址，避免下载途中 latest 改变。
	fallback := *client
	fallback.Timeout = 3 * time.Second
	resp, err = do(ctx, &fallback, http.MethodHead, LatestURL)
	if err != nil {
		return info, fmt.Errorf("无法检查更新，请检查网络或代理后重试")
	}
	loc := resp.Header.Get("Location")
	resp.Body.Close()
	base, _ := url.Parse(LatestURL)
	u, err := base.Parse(loc)
	if err != nil || loc == "" || resp.StatusCode < 300 || resp.StatusCode >= 400 || !AllowedURL(u.String()) || u.Host != base.Host || u.RawQuery != "" || !strings.HasPrefix(u.Path, "/kinewe/scrcpy-ez/releases/tag/") {
		return info, fmt.Errorf("无法获取最新发行版本")
	}
	tag := strings.TrimPrefix(u.Path, "/kinewe/scrcpy-ez/releases/tag/")
	if ParseVersion(tag) == nil {
		return info, fmt.Errorf("发行版本号无效")
	}
	info.Latest, info.HasNew = tag, VersionLess(current, tag)
	info.PageURL = RepoURL + "/releases/tag/" + tag
	// When the API is unavailable, let download probing resolve the two supported
	// asset names. A slow HEAD request must not hide an already discovered tag.
	info.Sources = fixedSources(providers[0], tag)
	info.DownloadURL = info.Sources[0].URL
	return info, nil
}
