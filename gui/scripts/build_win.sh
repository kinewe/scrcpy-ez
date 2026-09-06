#!/bin/bash
# scrcpy-ez GUI 壳 Windows 构建入口（WSL 内运行）。
# 实际构建委托 scripts/build_win.cmd 在 Windows 侧执行：
# 本环境 WSL 互操作不向 Windows 进程传递 Linux 侧环境变量，
# 所有 go 环境（GOPROXY/CGO_ENABLED/CC/CXX/GOPATH/GOCACHE）必须在 Windows 侧设置。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$ROOT/dist"

cmd.exe /c "D:\\dsh_work\\gui\\scripts\\build_win.cmd"
