# v2.2.2-root.2 验证记录

日期：2026-10-03，Asia/Shanghai。以下均是电脑端编译／自动测试，**没有 root 实机验证**。

## root.1 已通过的功能验证（继承）

- 完整 Go 测试：GUI 主包及全部内部包，Windows／CGO 构建通过；adb、app、bridge、castsupervisor、deviceevents、discovery、rootrepair、sessioncontrol、ui、updater 均通过。
- root 核心模拟：健康路径不请求 root、恢复标签、缺工具兜底、DAC 元数据修复、adbd sync 与 shell 权限差异、已有 root adbd、拒绝、未解决错误、异常路径、参数注入拒绝、诊断不可保存则不修改。
- Windows 实际命名互斥锁：已有锁正常打开、同设备排队、等待超时、取消释放。
- root 授权交互模拟：等待时停止、USB 消失后旧结果拒绝、拒绝授权不在当前同设备其他路由重试。
- 使用最终编译 EXE 的假设备验证：上述三种 root 交互及无线／USB 切换、恢复、用户关闭通过（4 项）。
- 实际传输 shell 脚本：收集修复器输出的脚本，使用 POSIX shell `-n` 检查全部语法，通过；不执行特权脚本。
- 前端现有 16 个 JavaScript 测试文件全部通过，包括更新弹窗、设备卡、输入保留、设置、会话绑定和应用窗口状态。
- Windows 资源：`FileVersion=2.2.2-root.1`、`ProductVersion=v2.2.2-root.1`、`IsPreRelease=True`；root.1 当时使用尝试版产品名，root.2 已恢复 `scrcpy-ez`。
- Git `diff --check` 通过。未添加新的运行时 Go 依赖。

## 可复现命令

在 GUI 目录中，使用 Go 1.27.0 和 MinGW GCC 16.1.0（复用机器已安装工具链）：

```powershell
$env:GOMODCACHE = (Resolve-Path '..\build\mod-cache').Path
$env:GOCACHE = (Resolve-Path '..\build\go-cache').Path
$env:CGO_ENABLED = '1'
$env:CC = 'F:/msys64/mingw64/bin/gcc.exe'
$env:CXX = 'F:/msys64/mingw64/bin/g++.exe'
$env:PATH = 'F:\go\bin;F:\msys64\mingw64\bin;' + $env:PATH
$env:GOPROXY = 'off' # 本次复用了已有依赖缓存；新环境须先下载依赖
go test -buildvcs=false -mod=readonly -p 1 ./... -count=1
$env:YINMO_TEST_SHELL = 'F:\msys64\usr\bin\bash.exe'
go test -buildvcs=false -mod=readonly ./internal/rootrepair -count=1 -v
windres -i assets/root-version.rc -O coff -o icon_windows_amd64.syso
go build -buildvcs=false -mod=readonly -trimpath -ldflags='-s -w -H windowsgui' -o dist/scrcpy-ez.exe .
$env:SCEZ_TEST_PRODUCTION_HELPER = (Resolve-Path 'dist\scrcpy-ez.exe').Path
go test -buildvcs=false -mod=readonly ./internal/castsupervisor -run '^TestRootWorker|^TestWorkerSwitchRecoveryAndUserClose$' -count=1 -v
```

`SCEZ_TEST_PRODUCTION_HELPER` 让假设备测试使用实际编译的 EXE 入口，而非测试程序。测试仍只使用临时假 adb／假 scrcpy，不打开主 GUI 或连接实际手机。

在仓库根目录，前端测试使用机器自带的 Node：

```powershell
Get-ChildItem gui/web -File | Where-Object {
  $_.Name -match '(\.test\.js|_test\.js)$'
} | ForEach-Object { node $_.FullName; if ($LASTEXITCODE) { throw $_.Name } }
```

独立包由 `packaging/build_root_package.py` 制作，仅接受与 GitHub API 摘要完全相符的 ez 2.2.2 ZIP。输出必须位于此分支根目录的 `dist`；所有包内文件进行 CRC 和逐字节复核，清单记录 SHA-256。公开压缩包自带 `scrcpy-ez-2.2.2-root` 独立目录，配置不携带开发者设备数据。

## 发现并修正的问题

1. `CreateMutex` 返回已有有效句柄时也可返回 `ERROR_ALREADY_EXISTS`，修正后通过已有锁与并发会话测试。
2. root 授权等待应有单独阶段，避免原 GUI 静默计时误提示卡住；主投屏与应用窗口均可显示 root 准备状态。
3. 授权取消／超时错误必须保留可识别的 context 错误链，否则诊断状态会误记为普通失败；专门测试通过。
4. 首次构建受沙箱子进程执行限制，授权运行本机编译器后构建成功。没有安装新工具或修改工具链。
5. 初次把模块缓存放在 GUI 的 `build` 下，`./...` 误扫第三方缓存测试并触发旧依赖示例校验错误；缓存移至模块外后完整测试通过。没有通过关闭 vet 掩盖错误，也没有保留意外依赖变更。

## 证据与限制

本地 `build/go-test-final.log`、`build/go-test-root-final.log`、`build/go-test-production-helper.log` 保留命令结果；尝试包有 `ROOT-EXPERIMENT.json` 与旁置 SHA-256。测试文件、使用说明和调研说明进入公开 root 独立分支。

未运行 Android 模拟器或真实 root 手机。无法验证 OEM 策略、root 管理器授权界面、远端 stdin/进程取消行为、实际 JAR 上传后执行、画面／声音／控制、通知或剪贴板表现。GUI 主窗口的实际 WebView2 渲染也没有作为 root 实机效果验证；此记录不宣称任何实机修复成功。

## root.2 增量验证

本版恢复原版显示、升级版本标记，并延长 root 授权等待；前述完整功能验证属于 root.1。本版复核结果如下：

- `gui/web` 全目录与基线一致，连同三个原版图标素材共核对 34 个文件；静态界面、样式及交互脚本没有差异。
- 主窗口标题与托盘恢复 `scrcpy-ez`，提示文字恢复原版；root 准备阶段沿用“正在准备 adb…”。版本号为 `v2.2.2-root.2`。
- 最终 EXE 的图标各尺寸、位深和图像载荷与基线一致；Windows 显示清单逐字节一致，保留 DPI 及控件设置。资源编译器对 PNG 图标平面数的元数据表示不同（0／1），图像内容没有改变。
- Windows 文件属性：`FileVersion=2.2.2-root.2`、`ProductVersion=v2.2.2-root.2`、`ProductName=scrcpy-ez`，保留 prerelease 标记。
- 16 个现有 JavaScript 测试文件再次全部通过。
- 5 个既有 app 阶段／更新隔离测试通过：主投屏阶段、应用窗口阶段及生命周期、禁止稳定版安装、独立更新状态。
- rootrepair 包完整测试再次通过，包括实际传输脚本语法、互斥锁、取消及所有修复阶段。新增执行器期限核验确认授权窗口接近 180 秒，没有被旧的总预算截短；测试不实际等待 3 分钟。
- 最终 EXE 的假设备交互复核：授权时停止、USB 消失后拒绝旧结果、拒绝授权不反复换路由请求；结果见 `build/go-test-root2-production-helper.log`。
- `rootAuthorizationTimeoutSeconds=180`、`rootPreparationBudgetSeconds=300` 记录在包内清单；原始输出和使用说明同步。手机管理器主动拒绝或自行超时仍不能由电脑端覆盖。

本版日志另保存在 `build/go-test-root2-app.log` 和 `build/go-test-root2-repair.log`。没有扩大为 Android 实机验证，实际 WebView2 主窗口也未进行屏幕截图核对；外观复核使用基线源文件、图标与显示清单对照。

## 用户反馈与发布核对

2026-10-03，用户确认普通设备投屏测试通过。设备型号及更细的交互情况未提供，不推导为所有普通设备或应用窗口、声音、控制、剪贴板都已实测。root 实机仍未验证；自动测试与本段用户反馈分别记录。

ez 发布包命名 `scrcpy-ez-2.2.2-root.zip`，外层目录同步命名 `scrcpy-ez-2.2.2-root`，内部 GUI／PE 版本仍为 `v2.2.2-root.2`。同名配套 `scrcpy-ez-2.2.2-root.sha256.txt` 校验该 ZIP，原 `SHA256SUMS.txt` 不变。追加 v2.2.2 Release 附件及说明末尾的 root 段落，不移动正式标签、不覆盖正式版附件。打包器进行 CRC、逐文件比对、GUI 哈希、正式组件哈希及验证状态分离检查；发布前后另留远端资产快照以复核原包 ID、大小与摘要。

公开源码位于 ez `root-experimental-v2.2.2`。正式更新器按稳定 tag 的精确包名选择 `scrcpy-ez-2.2.2.zip`；root 后缀包名不会被选作正式更新包。

公开说明修订仅调整文档、清单的源码引用及包来源信息，统一使用可访问的公开 root 分支；GUI 和运行组件不变，版本仍为 `v2.2.2-root.2`。重新制作 root 包并更新配套 SHA-256，原正式包与原校验文件保留。
