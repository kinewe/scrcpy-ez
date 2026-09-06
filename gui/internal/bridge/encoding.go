package bridge

import (
	"bytes"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// DecodeGBK 把 bat 子进程输出（Windows 代码页 936 / GBK）解码为 UTF-8。
// 非法字节序列按替换符处理，绝不返回错误——日志行丢失永远不该中断投屏。
func DecodeGBK(b []byte) string {
	out, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), b)
	if err != nil && len(out) == 0 {
		// GBK 解码整体失败（极少见）：原样按 Latin-1 兜底，保留可见信息
		var sb bytes.Buffer
		for _, c := range b {
			sb.WriteRune(rune(c))
		}
		return sb.String()
	}
	return string(out)
}
