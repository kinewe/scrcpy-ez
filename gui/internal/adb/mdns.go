package adb

import (
	"regexp"
	"strings"
)

// mDNS 服务形态（与档案 wireless/mode 字段同一语义）：
// tls = 无线调试加密连接（_adb-tls-connect._tcp）；tcpip = 经典明文 5555（_adb._tcp）；
// pairing = 配对服务（_adb-tls-pairing._tcp，仅配对弹窗打开期间短暂广播）。
const (
	MdnsModeTls     = "tls"
	MdnsModeTcpip   = "tcpip"
	MdnsModePairing = "pairing"
)

// MdnsService 是一条 mDNS 服务解析结果。
// `adb mdns services` 有两代输出格式（见 ParseMdnsServices 列序判定）：
// 老 adb：<service-type> \t <instance-name> \t <resolved-addr>；
// adb 37.0.0：<instance-name> \t <service-type> \t <resolved-addr>。
// 地址列可能是 IP:port（已解析）或 "local"（未解析，无地址）。
// 自管 zeroconf 浏览（mdns_zeroconf.go）也映射为本结构。
type MdnsService struct {
	Type string `json:"type"` // _adb-tls-connect._tcp 等
	Name string `json:"name"` // 服务实例名（tls 形如 adb-<serial>-XXXXXX）
	Addr string `json:"addr"` // 解析出的 IP:port；未解析为空
	Mode string `json:"mode"` // tls / tcpip / pairing（按服务类型判定）
}

var (
	reHeader    = regexp.MustCompile(`(?i)list of`)
	reIPPort    = regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}:\d+$`)
	reAdbType   = regexp.MustCompile(`_adb`)
	rePairCount = regexp.MustCompile(`^\(\d+\)$`) // adb 37+ 输出实例名后的服务计数 "(n)"
)

// MdnsModeOf 按服务类型判定形态：tls 连接/配对服务归 tls/pairing，其余 _adb 服务归 tcpip。
func MdnsModeOf(svcType string) string {
	switch {
	case strings.Contains(svcType, "-tls-connect"):
		return MdnsModeTls
	case strings.Contains(svcType, "-tls-pairing"):
		return MdnsModePairing
	default:
		return MdnsModeTcpip
	}
}

// ParseMdnsServices 解析 `adb mdns services` 输出。
// 跳过表头/空行；行内按空白（tab/空格）切列；只收 _adb 相关服务类型。
// 地址列仅接受 IP:port（"local"/主机名等未解析值忽略）。
func ParseMdnsServices(output string) []MdnsService {
	var out []MdnsService
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		line = strings.TrimSpace(line)
		if line == "" || reHeader.MatchString(line) {
			continue
		}
		fields := strings.Fields(line)
		// adb 37+ 的 mdns services 输出实例名后带服务计数 "(n)"
		// （如 "adb-601c9f08-KWqpio (2)"）——计数字段不参与列序判定，
		// 先剥离；旧 adb 输出无计数，剥离后原样。
		stripped := fields[:0]
		for _, f := range fields {
			if rePairCount.MatchString(f) {
				continue
			}
			stripped = append(stripped, f)
		}
		fields = stripped
		if len(fields) < 2 {
			continue
		}
		// 列序判定（两代 adb 输出格式不兼容，按第一列内容区分）：
		// 老 adb：<type> <name> <addr>——第一列以 _adb 开头（服务类型）。
		// adb 37.0.0：<name> <type> <addr>——第一列是实例名。
		var svcType, name string
		if strings.HasPrefix(fields[0], "_adb") {
			svcType, name = fields[0], strings.TrimSuffix(fields[1], ".")
		} else {
			svcType, name = strings.TrimSuffix(fields[1], "."), fields[0]
		}
		if !reAdbType.MatchString(svcType) {
			continue
		}
		s := MdnsService{Type: svcType, Name: name, Mode: MdnsModeOf(svcType)}
		if len(fields) >= 3 {
			addr := strings.TrimSuffix(fields[2], ".")
			if reIPPort.MatchString(addr) {
				s.Addr = addr
			}
		}
		out = append(out, s)
	}
	return out
}

// MdnsTrackEvents 是一份 mdns 全量快照（First=true 仅首个快照一次）。
// 事件处理链对快照做 diff；自管 zeroconf 浏览按此模型对外输出
// 都按此模型对外输出。
type MdnsTrackEvents struct {
	Added    []MdnsService
	Removed  []MdnsService
	Snapshot []MdnsService
	First    bool
}

// DiffMdnsServices 对两份 mdns 服务快照做 diff（纯函数）。
// key = Addr（空 Addr 用 Name）；同 key 视为同一服务。
func DiffMdnsServices(prev, cur []MdnsService) (added, removed []MdnsService) {
	prevIdx := map[string]int{}
	for i := range prev {
		prevIdx[mdnsServiceKey(prev[i])] = i
	}
	curIdx := map[string]int{}
	for i := range cur {
		curIdx[mdnsServiceKey(cur[i])] = i
	}
	for i := range cur {
		if _, ok := prevIdx[mdnsServiceKey(cur[i])]; !ok {
			added = append(added, cur[i])
		}
	}
	for i := range prev {
		if _, ok := curIdx[mdnsServiceKey(prev[i])]; !ok {
			removed = append(removed, prev[i])
		}
	}
	return added, removed
}

func mdnsServiceKey(s MdnsService) string {
	if s.Addr != "" {
		return "addr:" + s.Addr
	}
	if s.Name != "" {
		return "name:" + s.Name
	}
	return "type:" + s.Type
}
