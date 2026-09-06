// Package ui 负责 WebView 窗口与 JS 桥接绑定；html.go 为纯逻辑（占位符内联），
// 可在任意平台单测，方便验证 web 三件套组装。
package ui

import (
	"fmt"
	"strings"
)

const (
	cssPlaceholder = "/*__CSS__*/"
	jsPlaceholder  = "/*__JS__*/"
)

// BuildIndex 把模板 index.html 中的 CSS/JS 占位符替换为内联内容，
// 生成可直接喂给 webview.SetHtml 的自包含页面（无相对资源引用）。
func BuildIndex(template, css, js string) (string, error) {
	if !strings.Contains(template, cssPlaceholder) {
		return "", fmt.Errorf("模板缺少 CSS 占位符 %s", cssPlaceholder)
	}
	if !strings.Contains(template, jsPlaceholder) {
		return "", fmt.Errorf("模板缺少 JS 占位符 %s", jsPlaceholder)
	}
	out := strings.Replace(template, cssPlaceholder, css, 1)
	out = strings.Replace(out, jsPlaceholder, js, 1)
	return out, nil
}
