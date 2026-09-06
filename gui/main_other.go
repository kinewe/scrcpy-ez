//go:build !windows

package main

import "fmt"

// 非 Windows 平台：GUI 壳仅支持 Windows（WebView2 + bat 桥接均为 Win32 专属）。
// 此入口保证 Linux 下 `go test ./...` 可编译主包。
func main() {
	fmt.Println("scrcpy-ez GUI 壳仅支持 Windows。请在 Windows（或 WSL 交叉编译）构建。")
}
