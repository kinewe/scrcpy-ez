@echo off
rem scrcpy-ez GUI 壳 Windows 侧构建脚本（可由 WSL 互操作或直接在 Windows 运行）
rem 环境变量必须在 Windows 侧设置：WSL 互操作不向 Windows 进程传递 Linux 环境变量。
setlocal

set "ROOT=D:\dsh_work\gui"
set "GOEXE=%ROOT%\toolchain\win\go\bin\go.exe"

set "PATH=F:\msys64\mingw64\bin;%PATH%"
set "GOPROXY=https://goproxy.cn,direct"
set "CGO_ENABLED=1"
set "CC=F:\msys64\mingw64\bin\gcc.exe"
set "CXX=F:\msys64\mingw64\bin\g++.exe"
set "GOPATH=%ROOT%\toolchain\win\gopath"
set "GOMODCACHE=%ROOT%\toolchain\win\gopath\pkg\mod"
set "GOCACHE=%ROOT%\toolchain\win\cache"
set "GOFLAGS=-mod=mod"

cd /d "%ROOT%"
if not exist dist mkdir dist

"%GOEXE%" mod tidy
if errorlevel 1 exit /b 1

"%GOEXE%" build -ldflags="-s -w -H windowsgui" -o "%ROOT%\dist\scrcpy-ez-gui.exe" .
if errorlevel 1 exit /b 1

copy /y "%ROOT%\vendor_tmp\WebView2Loader.dll" "%ROOT%\dist\WebView2Loader.dll" >nul

echo == 产物 ==
dir "%ROOT%\dist"
exit /b 0
