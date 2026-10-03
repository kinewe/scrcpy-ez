# scrcpy-ez GUI

当前分支产品版号为 **音墨 v2.2.2-root.2**，没有 root 实机验证。根目录的 root 说明与验证记录优先于以下继承文档。`internal/rootrepair` 提供修复与诊断，监管器在每次路由启动前调用；正式版更新被禁用。

基于 Go、WebView2 和 systray 的 Windows 图形界面。GUI 管理设备、主投屏与应用窗口，通过隐藏控制台调用投屏支持脚本。

## 构建

安装 Go 1.23 或更新版本，并将 MinGW 的 gcc/g++ 加入 PATH。在 gui 目录运行：

```powershell
$env:CGO_ENABLED = '1'
$env:CC = 'gcc'
$env:CXX = 'g++'
go build -mod=readonly -trimpath -ldflags="-s -w -H windowsgui" -o dist/scrcpy-ez.exe .
```

也可运行 `scripts/build_win.cmd`；WSL 可通过 `scripts/build_win.sh` 调用。`GOEXE`、`CC`、`CXX` 支持环境变量覆盖。发布构建使用 `-trimpath` 和 Windows GUI 子系统，版本信息由 `icon_windows_amd64.syso` 提供。

此分支的 `scripts/build_win.cmd` 会先用 `windres` 编译 `assets/root-version.rc`，确保 Windows 文件属性也显示 root.2。若手动构建，请先执行同样的资源编译。模块缓存应置于仓库根目录 `build/` 或其他模块外目录，避免 `go test ./...` 扫描缓存中的第三方源码。

## 目录

- `internal/adb`：设备查询与 mDNS 发现。
- `internal/app`：档案、投屏会话、应用窗口、配对与更新状态。
- `internal/bridge`：隐藏进程、脚本输出解析与投屏清理。
- `internal/ui`：WebView2、页面组装和 JS 绑定。
- `internal/updater`：双源下载、包校验、安装事务与回滚。
- `web`：界面与交互；资源随 EXE 内嵌。

## 运行

发布包内 GUI、`投屏支持.bat`、adb、scrcpy 和运行库放在同一目录。系统须安装 WebView2 Runtime。

- `SCEZ_BAT_PATH`：覆盖支持脚本路径；默认取 EXE 所在目录的 `投屏支持.bat`。
- `SCEZ_ADB_PATH`：覆盖 adb 路径；默认取支持脚本所在目录的 `adb.exe`。

设备档案和设置保存到软件目录。发布包只携带空档案，更新保留用户已有档案和设置。更新流程见 [应用内更新](docs/in-app-update.md) 和 [双源选择](docs/dual-source-update.md)。
