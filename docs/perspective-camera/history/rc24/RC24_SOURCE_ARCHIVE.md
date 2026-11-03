> 历史研究记录的脱敏副本。保留当时的实验条件、数值与阶段判断；后续结论以[总记录](../../../透视相机研发全记录_rc17至rc25_20261103.md)为准。历史命令不作为当前通用启动入口，原始日志、APK和图像未随本文件公开。

# RC24 源码归档 — 2026-10-31

- 版本：`2.4.2-rc.24`，协议 `5.0.1-ez2.4.2-rc.24`，`sourceRevision=rc24`，候选 `rc.24-native-alpha`；规则引擎 Engine 17（含「有界 UI 重绘诊断」）。
- 基线：RC23 只读工作树提交 `6f63b8ec4438ce554c9aa446cebf540499b827fd`（<historical-workspace>/ez-rc23，本轮全程未改动，见下）。
- RC24 仓库：root commit `bcd24c70cc8ddecb4e494e6ce61c7124670ab76d`（= rc23 工作树快照），版本升级提交 `c9209e23c15c542d71e88731ec782b915d04bc63`，Engine 17 提交 `d54b7e0`（有界 UI 重绘诊断 + RC24 规则/档案文档），最终集成提交 `58e0dbc2a818568c8629b329f6b559f6dfd3fb6c`（文档回填与交付收尾）。
- 内容：rc24 独立工作树（复制自 rc23 工作树并升级版本串）、原生 RGBA/A8 读取与无损差分、Windows 叠加、能力匹配及档案，以及新增的 `tools/build-rc24.py` / `tools/package-rc24.py` 与 rc24 版工具脚本。

构建入口：

- `python tools/build-rc24.py [native|server|gui|assets|...]`，输出 `build-rc24/out/rc24/`。
- `python tools/package-rc24.py`，输出 `<runtime-root>/scrcpy-ez-2.4.2-rc.24-native-alpha-precompose-win64/`（依赖 native/gui/server 产物）。
- 相关测试：`python tools/test-rc24-profiles.py`、`test-rc24-codec.py`、`test-rc24-overlay.py`；rc23 不变量：`python tools/verify-rc23-untouched.py`。
- 脚本使用本机工具链固定路径（<java-home>、<msys-root>/mingw64、<drive>/go、<android-sdk>、<historical-workspace>/rc14-perspective 编译模板），迁移环境需按实际安装位置调整。

## 本轮服务端构建复核（可重复性）

命令：`python tools/build-rc24.py server`（本轮由 Lead 先构建一次，build-verify 在同一工作树上重跑一次）。

| 项 | 结果 |
| --- | --- |
| 产物 | `build-rc24/out/rc24/scrcpy-server`，362,821 字节 |
| SHA256 | `5A4A25B5DF0E7D83459933101F44163E6D3A6F321A0D0C32D5758A1F09CEB28C` |
| 容器 | ZIP（PK\x03\x04），仅含 `classes.dex`，661,424 字节，SHA256 `DCCD688DFCBC6C9635F6618293EB67F2F40D81F44E780A31B7DAFC1DDB6DD38C` |
| classes.jar | 1,054,489 字节，SHA256 `3D91F9378B16F95EC04B3F4CE788EB1680F98313A14858567C4F6E9464B35919` |
| 协议串 | `5.0.1-ez2.4.2-rc.24`：True |
| Engine 17 诊断标记 | `NATIVE_ALPHA_UI_REDRAW`：True，`uiRedraw`：True |
| 旧协议标记 | `EZ_NATIVE_UI_RGBA_V4`：False（未泄漏） |
| 新协议标记 | `EZ_NATIVE_ALPHA_RGBA_V2`：True |
| 构建日志 | `SERVER_BUILT 5A4A25B5… PROTOCOL_OK 5.0.1-ez2.4.2-rc.24`，`EXTRA` 断言与 DEX 断言均通过 |
| `build.json` | version/protocol/sourceRevision/candidate 与上述一致；`filesSHA256` 记录 `scrcpy-server=5A4A25B5…`；runtimeAssets 275 文件摘要与重跑前一致 |

结论：Lead 构建与 build-verify 重跑得到**同一 SHA256**，服务端构建可重复；DEX 协议与 Engine 17 诊断标记符合预期，旧 PNG 探针标记不存在。Engine 17 改动已由 Lead 提交为 `d54b7e0`（提交前后两文件内容哈希不变，构建产物即对应此提交）：`server/.../NativeAlphaProbe.java` SHA256 `5F19EDF1CDB7EE22B5E71D6D1A192D992D6A4EF95646DEABE79F397A0C583F29`、`NativeAlphaPolicy.java` SHA256 `1ADDBFC0D6A2F37F689AA430048E1C6522A30E07D920C048F3261B9181E5719E`。

## native / GUI 构建结果（2026-10-31 16:20）

命令：`python tools/build-rc24.py native gui`（Lead 授权的唯一构建窗口内执行；构建日志 `build-rc24/logs/rc24/build-native-gui-console.log`，exit 0，未改源码绕过任何失败）。

| 项 | 结果 |
| --- | --- |
| native | `NATIVE_COMPILED 93 sources`，`NATIVE_BUILT`，`NATIVE_VERSION_OK 5.0.1-ez2.4.2-rc.24 launcher v2.4.2-rc.24` |
| `scrcpy.exe` | 1,251,632 字节，SHA256 `28C2B3708ABAD07857A0E2B8EF3DD82C270F6D3041841C7DB46E64A3BA00E640` |
| GUI | windres `gui/assets/version.rc` → go build → `GUI_BUILT` |
| `scrcpy-ez.exe` | 12,657,664 字节，SHA256 `C16B2B1144D9E648A7D84C30EB861B5FFF0DD304F9537D9BE67C7277BAE29A51`；内嵌版本 `v2.4.2-rc.24`=True、`v2.4.2-rc.23`=False |
| `scrcpy-server` | 未变：362,821 字节，SHA256 `5A4A25B5DF0E7D83459933101F44163E6D3A6F321A0D0C32D5758A1F09CEB28C` |
| `scrcpy.exe --version` | `scrcpy 5.0.1-ez2.4.2-rc.24 <https://github.com/Genymobile/scrcpy> \| launcher v2.4.2-rc.24`；SDL 3.4.18/3.4.18，libavcodec 63.1.102，libavformat 63.1.102，libavutil 61.1.102，libusb -/1.0.30 |
| `build.json` | `filesSHA256` 三文件齐全：scrcpy.exe/scrcpy-server/scrcpy-ez.exe，与上表一致；runtimeAssets 275 文件摘要不变 |

构建后副本自动刷新 runtime assets（275 文件）。构建使用 build.json 记录的 RC.23 release 运行时 DLL 目录（打包时这些 DLL 被组装进 RC.24 运行包，见下）。

## 测试结果

| 测试 | 命令 | 结果 |
| --- | --- | --- |
| 服务端可重复构建 | `python tools/build-rc24.py server` | PASS，SHA256 与 Lead 首次构建一致（同上） |
| native + GUI 构建 | `python tools/build-rc24.py native gui` | PASS，exit 0；产物哈希/版本见上节 |
| rc23 不变量 | `python tools/verify-rc23-untouched.py` | PASS：manifest 2403/2403 文件路径存在且 SHA256 一致；missing=0，extra=0，changed=0；报告 `test-results/rc23-untouched-20261031-161402.txt`（收尾复核 `20261031-161803`、审计内复核 `20261031-163606`，均 PASS） |
| Go 档案测试 | `python tools/test-rc24-profiles.py` | PASS：`internal/app` + `internal/nativealpha` 共 66 个测试（含子测试）通过，0 失败，exit 0；原始 JSONL `test-results/auto-go-tests.jsonl` |
| Go 指定回归 | 在 `gui/` 按 build-rc24.py env（GOCACHE/GOMODCACHE/GOPROXY=off/GOTOOLCHAIN=local/CGO_ENABLED=1/PATH 含 <msys-root>/mingw64/bin;<drive>/go/bin）执行 `go test ./internal/app -run 'NativeAlpha\|Profile\|Native' -count=1` | PASS：`ok scrcpy-ez/gui/internal/app 11.674s` |
| 编解码跨语言 | `python tools/test-rc24-codec.py` | PASS：80 帧（16 全量 + 64 补丁），2,363,200 字节 RGBA 精确一致；epoch/CRC/畸形补丁/关键帧恢复均通过 |
| 叠加单元测试 | `python tools/test-rc24-overlay.py` | PASS：2048 RGBA 像素精确一致；过期源与几何不符被拒绝。native 构建前以从 RC14 模板派生的等价 `config.h` 运行（`test-results/tmp/`）；native 构建后用真实 `out/rc24/config.h` 复测，同样 PASS |
| 打包 | `python tools/package-rc24.py` | PASS，exit 0：53 个交付文件，包 zip SHA256 `8BF4503C9615735DCD7BFC712C19F5F6E7E7F591820CB24D5D35F06614AEFBAD`（见下节） |
| 交付审计 | `python tools/audit-rc24.py` | PASS，exit 0：53/53 文件哈希、版本、DEX Engine 17、rc23 不变量、delivery 文件、zip 内容全部通过；输出 `test-results/final-audit.json` |

## rc23 不变量

- 以 `<historical-workspace>/rc23-worktree-manifest-20261031.json`（2403 文件 path→SHA256）为唯一标准，逐文件校验 `<historical-workspace>/ez-rc23`。
- 排除 `.git/`、`build-rc23/`、`test-results/`（不属工作树快照）；不信任 git assume-unchanged 标记。
- 结果：**PASS** — 2403 个 manifest 文件全部存在且哈希一致，无新增、无缺失、无改动。`docs/handoffs/frame-pacing-20261016/evidence/laptop-20261016/` 下 9 个被 git assume-unchanged 标记的文件同样与 manifest 一致。
- 本轮未对 `<historical-workspace>/ez-rc23` 做任何写操作。

## 工具脚本 rc23→rc24 适配

改名并保持功能（旧文件删除，只保留 rc24 版本）：

| rc23 | rc24 |
| --- | --- |
| audit-rc23.py | audit-rc24.py |
| close-rc23-test.py | close-rc24-test.py |
| rc23-test-nav.py | rc24-test-nav.py |
| rc23-zorder-check.py | rc24-zorder-check.py |
| report-rc23-auto.py | report-rc24-auto.py |
| report-rc23-scene.py | report-rc24-scene.py |
| seed-rc23-alpha-profiles.py | seed-rc24-alpha-profiles.py |
| start-rc23.ps1 | start-rc24.ps1 |
| start-rc23.py | start-rc24.py |
| test-rc23-codec.py | test-rc24-codec.py |
| test-rc23-overlay.py | test-rc24-overlay.py |
| test-rc23-profiles.py | test-rc24-profiles.py |

- 删除 `tools/build-rc23.py`、`tools/package-rc23.py`（由 Lead 的 `build-rc24.py`/`package-rc24.py` 取代），并清理 `tools/__pycache__` 中 rc23 旧字节码。
- 新增 `tools/verify-rc23-untouched.py`（支持 `--quiet`，供 audit 调用）。
- 内容替换：`rc23/RC23/rc.23 → rc24/RC24/rc.24`、`build-rc23/out/rc23 → build-rc24/out/rc24`、窗口标题 `RC24_NATIVE_ALPHA`、`/data/local/tmp/ez-rc24-*`、环境变量 `RC24_*`。
- 路径适配：`start-rc24.ps1/.py`、`test-rc24-overlay.py` 从 `build-rc24/out/rc24/build.json` 的 `runtimeDllDir` 解析运行库目录（复用 RC.23 release 运行时，与 build-rc24.py 一致），不再硬编码旧路径；`audit-rc24.py` 目标改为 `native-alpha-precompose` 交付包并新增 Engine 17 DEX 断言与 rc23 不变量校验；`report-rc24-scene.py`/`seed-rc24-alpha-profiles.py` 支持 env 覆盖目标路径。
- `tools/read-view-structure.py`、`collect-readonly-structure.py`、`generate_calibration_art.py`、`tools/frame_pacing/**` 经检查不含 rc23 路径/版本串，未改动。
- 残留 `rc.23` 字样的合理位置：Lead 所有的 `build-rc24.py`/`package-rc24.py` 中「复用 RC.23 release 运行时」的路径（功能必需，归 Lead 清理），以及 `audit-rc24.py`/`verify-rc23-untouched.py` 中对「rc23 是不可变基线」的说明（有意保留）。所有 rc24 工具脚本可执行性已通过 `py_compile` 与 PowerShell 语法解析。

## 版本一致性

| 位置 | 版本串 |
| --- | --- |
| `meson.build` | `version: '5.0.1-ez2.4.2-rc.24'` |
| `server/build.gradle` | `versionName "5.0.1-ez2.4.2-rc.24"`，`versionCode 50001`，minSdk 21 / targetSdk 36 |
| `app/scrcpy-windows.rc` | `ProductVersion "2.4.2-rc.24"`，`FileVersion "2.4.2.2400"` |
| `gui/assets/version.rc` | `FileVersion "2.4.2.2400"`，`ProductVersion "v2.4.2-rc.24"` |
| `app/src/main.c` | `launcher v2.4.2-rc.24` |
| `gui/main_windows.go` | `version = "v2.4.2-rc.24"` |
| `build-rc24/out/rc24/build.json` | `version 2.4.2-rc.24`，`protocol 5.0.1-ez2.4.2-rc.24`，`filesSHA256` 含 scrcpy.exe/scrcpy-server/scrcpy-ez.exe 三文件哈希 |
| `.gitignore` | 已覆盖 `/build-rc24/` 与 `/test-results/`（`/build-rc23/` 为旧条目，由 Lead 清理） |

## 打包与交付审计结果（2026-10-31 16:36）

打包命令：`python tools/package-rc24.py`（exit 0；日志 `build-rc24/logs/rc24/package-console.log`），目标目录 `<runtime-root>/scrcpy-ez-2.4.2-rc.24-native-alpha-precompose-win64/`。

| 项 | 结果 |
| --- | --- |
| 交付包目录 | `…/scrcpy-ez-2.4.2-rc.24`（54 文件 = 53 个交付文件 + `SOURCE_MANIFEST.json`） |
| 包 zip | `scrcpy-ez-2.4.2-rc.24-native-alpha-precompose-win64.zip`，18,115,683 字节，54 个条目（53 个交付文件 + `SOURCE_MANIFEST.json`）。注意：zip 内含新生成的 `SOURCE_MANIFEST.json` 时间戳，**重跑打包会改变 zip 哈希**；最终大小/哈希以 `test-results/package.json` 为准 |
| changes zip | `scrcpy-ez-2.4.2-rc.24-native-alpha-precompose-changes.zip`：含 `rc24.patch`、`BASE.json`（root `bcd24c70`）与源码文件清单。该归档在最终文档定稿前生成；**最终大小/哈希以 `test-results/package.json` 为准**（重跑 `tools/package-rc24.py` 会刷新，包 zip 内容不变） |
| `test-results/package.json` | `files=53`，zip SHA256 同上，记录 package/zip/changes 三个路径 |

审计命令：`python tools/audit-rc24.py`（exit 0；日志 `build-rc24/logs/rc24/audit-console.log`；结果 `test-results/final-audit.json`）：

| 检查项 | 结果 |
| --- | --- |
| `SOURCE_MANIFEST.json` 逐文件 SHA256 | 53/53 通过（scrcpy.exe/scrcpy-ez.exe/scrcpy-server 哈希与 `build.json` 一致，未混入旧二进制） |
| 版本 | `scrcpy.exe --version` = `scrcpy 5.0.1-ez2.4.2-rc.24 … launcher v2.4.2-rc.24`；`scrcpy-ez.exe` 内嵌 `v2.4.2-rc.24`；`--native-alpha-worker` 非法参数路径 exit 0 |
| DEX（包内 scrcpy-server 的 classes.dex） | `EZ_NATIVE_ALPHA_RGBA_V2` ✓、`NATIVE_ALPHA_UI_REDRAW` ✓、`uiRedraw` ✓；旧协议 `EZ_NATIVE_UI_RGBA_V4`/`NativeUiProbe`/`ViewLayerProbe` 均不存在 |
| 包内文档 | README（RC24_NATIVE_ALPHA）、AUTO_ALPHA_PROFILES、CAPABILITY_REPORT、RULES_DESIGN、UI_REDRAW_RESEARCH 均存在 |
| zip 内容隔离 | 无 `.apk`/`profiles.json`/`settings.json`、无 `icons/` 目录、无 `__pycache__` |
| rc23 不变量 | PASS：2403/2403，报告 `test-results/rc23-untouched-20261031-163606.txt` |
| delivery 文件 | `app/src/perspective.c`、`app/src/desktop_capture.c`、`server/.../PerspectiveCamera.java` 相对 root `bcd24c70` 未变；`git diff --check` 通过 |

结论：rc24 win64 运行包与 changes 归档已生成并通过审计，可交付；包内不含 APK、profiles.json、settings.json、图标目录与构建缓存。运行时 DLL 复用 RC.23 release 集合（打包时复制进包内），本轮未占用任何设备，未执行 `git add/commit/push`。

## 边界声明

本归档只记录可追溯的 rc24 源码与验证快照，不代表兼容性完成或正式发布声明。本轮未修改 `app/`、`server/`、`gui/` 产品源码（含 Engine 17 改动为 Lead 写入），未修改 `research/`，未对 `<historical-workspace>/ez-rc23` 做任何写入，未执行 `git add/commit/push`。个人 profiles/settings、设备原始采集、APK、运行包 ZIP、构建缓存不进入本次提交。
