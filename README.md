# scrcpy-ez GUI 壳（第一阶段）

基于 Go + WebView2 + systray 的投屏启动壳。界面视觉按音墨设计稿还原
（素白底 #fafafa / 15px 中文 / 卡片圆角 / scrcpy 绿 #43A884 / 橙 #F59E0B）。
核心策略：**壳包 bat 隐藏窗口**——GUI 只做界面层，`投屏启动.bat` 以隐藏
控制台（CREATE_NO_WINDOW + SW_HIDE）作为引擎运行，业务逻辑零重建。

## 目录结构

```
gui/
├── main_windows.go        # 入口：systray + webview 装配（仅 Windows）
├── main_other.go          # 非 Windows 占位入口（保 Linux 测试可编译）
├── internal/
│   ├── adb/               # adb 设备发现与属性查询（市场名/型号/电量/分辨率/Hz）
│   ├── app/               # 状态中枢：设备轮询、投屏会话、输入状态机
│   ├── bridge/            # bat 桥接：GBK 解码、echo 行→事件分类、CreateProcess 隐藏窗口
│   └── ui/                # WebView 窗口与 JS 绑定；HTML 组装（占位符内联）
├── web/                   # 界面三件套（style.css 源自 mockup 设计稿）
├── assets/icon.ico        # 托盘图标（scripts/make_icon.py 生成）
├── scripts/
│   ├── build_win.sh       # Windows exe 构建（WSL 互操作 + msys64 工具链）
│   ├── test_linux.sh      # WSL 单元测试
│   └── make_icon.py
└── dist/                  # 构建产物（scrcpy-ez-gui.exe + WebView2Loader.dll）
```

## 构建

```bash
# 1. WSL 单元测试（纯逻辑）
bash scripts/test_linux.sh

# 2. Windows exe（WSL 内互操作调用 Windows Go 工具链 + msys64 gcc）
bash scripts/build_win.sh
```

> 构建说明：本环境 WSL 互操作不向 Windows 进程传递 Linux 侧环境变量，
> 因此 build_win.sh 委托 `scripts/build_win.cmd`（可直接双击）在 Windows 侧
> `set` 全部 go 环境（GOPROXY/CGO_ENABLED/CC/CXX/GOPATH/GOCACHE）。
> 首次构建还会把模块缓存等配置写入 `C:\Users\<user>\AppData\Roaming\go\env`
> 与 `toolchain/win/`（缓存目录），属构建工具链的正常占用。

产出 `dist/scrcpy-ez-gui.exe`（需与 `dist/WebView2Loader.dll` 同目录运行；
Win10/11 自带 WebView2 Runtime，win7 需另装）。

## 配置（开发期常量 + 环境变量覆盖）

- `SCEZ_BAT_PATH`：bat 路径，默认 exe 同目录（`appDir()` 运行时解析，可用环境变量覆盖）
- `SCEZ_ADB_PATH`：adb.exe 路径，默认 bat 同目录 `adb.exe`

## 桥接要点

- 隐藏启动：`cmd.exe /c "bat"` + `CREATE_NO_WINDOW | CREATE_NEW_PROCESS_GROUP` + `HideWindow`
- 输出：stdout/stderr 管道逐行 → **GBK(936) 解码**（x/text simplifiedchinese）→ 关键词分类
  （`[OK]/[提示]/[失败]/[键盘模式]/[规格]/开始投屏 banner`…）→ 投屏视图状态
- 输入：仅在解析到 choice/pause 提示后开放 stdin 写入（按钮 → `1/2/3`、`r/q`、任意键）
- 退出：taskkill /T 杀进程树（scrcpy.exe + watcher）→ 补 `adb kill-server`
- 退出码：0=关窗正常结束；非 0=异常/被停止（界面区分显示）

## 已知限制（第一阶段）

1. **不向 bat 传设备**：点哪个设备的"投屏"只记录展示名；实际选路由 bat 自身逻辑决定
   （USB 优先取第一台）。多设备场景的精确指定留给第二阶段（壳内嵌 bat 逻辑或参数透传）。
2. 投屏会话是"翻译层"：bat 的每条 echo 都有映射，识别不了的行只进原始日志折叠区，不阻塞投屏。
3. systray"显示主窗口"依赖页面 load 回调取 HWND；极早期点击可能无效果（再点一次即可）。
4. 未做代码签名：SmartScreen 会警告；量产前建议签名。
5. 界面视觉由音墨截图验收（dsh 无多模态，未做截图自证）。

## 下一步（第二阶段建议）

- StartCast 传目标设备（bat 支持 `%*` 透传或壳内选路参数）
- 设置页（开机自启 HKCU Run、日志导出、bat 路径可配置化）
- WebView2Loader 静态链接或嵌入、单 exe 发行
- Inno Setup 安装器（壳稳定后配套）
