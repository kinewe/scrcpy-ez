package bridge

import (
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func gbkBytes(t *testing.T, s string) []byte {
	t.Helper()
	b, _, err := transform.Bytes(simplifiedchinese.GBK.NewEncoder(), []byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDecodeGBK(t *testing.T) {
	in := "[OK] 检测到 USB 设备：Redmi K80（24117RK2CC）"
	got := DecodeGBK(gbkBytes(t, in))
	if got != in {
		t.Fatalf("GBK 往返失败: %q", got)
	}
}

func TestDecodeGBKLineEndings(t *testing.T) {
	in := "===== 开始投屏：X =====\r\n"
	got := DecodeGBK(gbkBytes(t, in))
	if !strings.HasSuffix(got, "\r\n") {
		t.Fatalf("行尾丢失: %q", got)
	}
}

func TestDecodeGBKGarbage(t *testing.T) {
	// 非法 GBK 字节不应 panic / 不应返回错误
	got := DecodeGBK([]byte{0xff, 0xfe, 0x81, 0x40})
	if got == "" {
		t.Fatal("非法输入应返回兜底字符串")
	}
}
