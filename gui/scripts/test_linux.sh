#!/bin/bash
# WSL 侧单元测试（纯逻辑包：bridge 行分类/GBK 解码、adb 解析、app 状态机、ui HTML 组装）
# 注意：bat_windows.go / ui_windows.go / main_windows.go 带 //go:build windows，Linux 下自动跳过。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
export GOPROXY='https://goproxy.cn,direct'
export GOFLAGS='-mod=mod'
export GOCACHE="$ROOT/toolchain/linux-cache"
export GOMODCACHE="$ROOT/toolchain/linux-gopath/pkg/mod"
export GOPATH="$ROOT/toolchain/linux-gopath"

"$ROOT/toolchain/go/bin/go" mod tidy
"$ROOT/toolchain/go/bin/go" vet ./...
"$ROOT/toolchain/go/bin/go" test ./... -v
