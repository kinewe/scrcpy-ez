@echo off
rem Build the Windows GUI using Go and a MinGW compiler on PATH.
setlocal
for %%I in ("%~dp0..") do set "ROOT=%%~fI"
if not defined GOEXE set "GOEXE=go"
if not defined CC set "CC=gcc"
if not defined CXX set "CXX=g++"
set "CGO_ENABLED=1"
cd /d "%ROOT%"
if not defined WINDRES set "WINDRES=windres"
"%WINDRES%" -i assets\root-version.rc -O coff -o icon_windows_amd64.syso
if errorlevel 1 exit /b 1
if not exist dist mkdir dist
"%GOEXE%" build -buildvcs=false -mod=readonly -trimpath -ldflags="-s -w -H windowsgui" -o "dist\scrcpy-ez.exe" .
if errorlevel 1 exit /b 1
echo Built dist\scrcpy-ez.exe
exit /b 0
