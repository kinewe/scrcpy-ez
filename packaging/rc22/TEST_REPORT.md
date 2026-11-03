# RC.22 验证记录（最终候选，已实机验证）

日期：2026-10-29（Asia/Shanghai）。状态：**最终候选构建完成并经用户实机验证**（渐进式 staged pipeline + 快速多底色校准 + 透视相机桌面帧率默认 10 fps + 连续静态层学习 + 扫描带方形缺口修复 + 组件区域元数据缺失修复均已完成；扫码速度根因已定位并由 10 fps 默认缓解；GUI 缩小显示锯齿列为待单独调查项）。完整发布打包与发布验证待执行。

## 最终候选构建（2026-10-29 15:54，冻结树；客户端更新，server/GUI 字节不变）

由 Lead 在冻结树上完成最终构建（同版本多次重构建：`env.c` CRT 环境同步修复 → 连续静态层学习替代一次性冻结回退 → 组件矩形排除修复 + 相机关闭中止修复 → 快速多底色校准 + 透视桌面帧率默认 10 fps → **组件区域元数据 gating 修复 + 诊断日志**）：

- `scrcpy.exe` SHA-256 `788A348EB619AFFAFD0BAAD83D5BA205FDA8CC15CA0161256405E5581EA719D6`
- `scrcpy-server` SHA-256 `B731601E8F29BB1C64910D0E6043A5095D0E9DB0062B38C1620E56ACCA862EE2`（未变化）
- `scrcpy-ez.exe` SHA-256 `CA70B5BBD2F201800ACBE0A81449A0473312E213EA9A909EF359D7BD4A9C64AA`（未变化）
- `build.json` SHA-256 `3E7F7FC79F1B982F95521B047464D23A5EDFA4EF681C922218D64DDF8C07F90C`
  - 版本 `2.4.2-rc.22` / 协议 `5.0.1-ez2.4.2-rc.22` / `sourceRevision=rc22` / 候选 `rc.22-staged`
  - `runtimeAssets`：calibration 7 文件 / icons 164 文件 / scrcpy.png / disconnected.png（共 173 个资源文件）
  - `runtimeDllDir`：`<runtime-root>/scrcpy-ez-2.4.2-rc.19-win64/scrcpy-ez-2.4.2-rc.19`
- 构建自检：`scrcpy.exe --version` → `scrcpy 5.0.1-ez2.4.2-rc.22 … | launcher v2.4.2-rc.22`；PE `2.4.2-rc.22` / `FileVersion 2.4.2.2200`；server `classes.dex` 含四个探针类、`EZ_NATIVE_UI_RGBA_V4`、`5.0.1-ez2.4.2-rc.22` 与通用扫码关键词；`logs/rc22/` 96 个单元日志无错误。
- 二进制标记：`NATIVE_UI static layer updated: adopted`、`inverse learning aborted`、`PERSPECTIVE key colour auto`、`inverse base refined`、`NATIVE_UI worker started`、`native frame decode failed`、`inverse learning: component regions=`、`_putenv` 均在；已移除的 opaque-change 回退日志与 `sc_native_ui_opaque_changed` 均不存在。**诊断注意**：新增的 “NATIVE_UI inverse LIVE but native composite unavailable” 警告条件在编译期恒假（`lp.inverse_live && !native_display`，而 `native_display = !inverse || inverse_live`），该分支/字符串未编入二进制，当前不会输出；已上报，待 RC.23 修正条件。
- 本构建变化：**快速多底色校准**（`SC_PHOTO_SAMPLES` 8→3、相位稳定 350→100ms、采样间隔 33→16ms；实机日志四相位约 1.0 秒完成，上一版约 3.1 秒）；**透视相机桌面帧率默认 1→10 fps**（`options.c` `perspective_input_period` 30→3，`gui/web/app.js` 默认 10，`packaging/投屏启动.bat` 兜底 10，`start-rc22.ps1 -PerspectiveFps` 默认 10；GUI 仍可选 0.5/1/6/10；代价是桌面捕获/编码开销更高）；**组件区域元数据缺失修复**（GUI 会话偶发探针 EVS1 `latest.views` 未生成、`region_count=0`；旧渲染逻辑要求组件区域>0 才显示双通道合成，导致 inverse 已 LIVE 仍只显示单键，即用户报告的“静态层未应用”；现改为合成不再依赖组件区域，仅需原生帧+已学模型+GPU；新增 worker 启动、原生帧解码失败（限频，含路径/大小）、组件区域计数诊断日志）。

## 实机验证（session `rc22-live-20261029-145521`，WeCom，`key_channel=1` green）

1. **纯键控快速启动**：扫描页可见 → LOCAL_GPU/学习 +1.0 秒 → 多底色校准完成 +4.1 秒，此时校准遮罩已淡出、纯键控画面可用；后台双通道学习仍在继续，显示无 2fps 原生跳变。（该 4.1s 为上一次实机确认数据；本构建已加入快速校准，日志显示四相位约 1.0 秒完成，端到端入口时间待用户确认。）
2. **无缝切换**：支持冻结 +4.1 秒（`inverse support frozen after …`），inverse LIVE/双通道 +4.6 秒（`inverse6 state 1->2`、`hybrid active`）；同一视频帧路径 + 同一 `gpu_matte` 纹理，仅 shader 状态改变。
3. **连续静态层学习**：冻结后立即 `NATIVE_UI static layer updated: adopted 2213 px (frame 1)`；Tab 切换动画与延迟文本更新（用户确认），无需回退。
4. **扫描带方形缺口修复**：inverse 路径不再排除 10 个组件矩形（`native_ui.h` NULL/0；compose 不再强制 `o=n`），用户确认扫描带连续。
5. **中止行为**：相机关闭时日志 `NATIVE_UI camera closed: dual-channel learning stopped` 与 `inverse learning aborted (camera closed or page changed)`（该次运行相机关闭于 +48.8 秒）；显示立即回到 rc19 单键路径。
6. **重进**：重新打开扫一扫自动重新绑定（generation 2/3/4…），开启新学习 epoch（epoch 3→4→6），约 +4.6 秒后重新切到双通道，无旧基座残留。
7. **按应用自动键色**：WeCom `key_channel=1`（green）；微信自动蓝键；`env.c` CRT 环境同步修复保留。
8. **透视相机桌面帧率 10 fps（本构建）**：1 fps 时透视相机 30 fps 中仅约 1/30 帧携带桌面/二维码画面（`perspective.c` input_period 交错），导致扫码 4–5 秒甚至 >20 秒失败；默认 10 fps 后用户确认扫码明显更快。代价是桌面捕获/编码开销更高；GUI 仍可选 0.5/1/6/10 fps。
9. **快速校准（本构建）**：`SC_PHOTO_SAMPLES` 8→3、相位稳定 350→100ms、采样间隔 33→16ms；日志显示 4 个底色相位约 1.0 秒完成（上一版约 3.1 秒）；端到端进入耗时待用户确认。
10. **组件区域元数据缺失修复（本构建）**：GUI 会话中探针 EVS1 `latest.views` 偶发未生成（`NATIVE_UI_LAYOUT metadata_bytes=208`、客户端 `region_count=0`）；旧逻辑要求组件区域>0 才显示双通道合成，导致 inverse 已 LIVE 仍只显示单键（用户报告的“静态层未应用”）。现合成不再依赖组件区域，仅需原生帧+已学模型+GPU；下一次实机应重点确认该场景下是否出现 `NATIVE_UI static layer updated` 与 `hybrid active`。
11. 探针日志统一并入投屏日志；运行资源齐全，无 `Could not load icon` / `Calibration artwork unavailable`。

## 已知问题与后续项

- **扫码识别速度（已定位并缓解）**：根因是 1 fps 桌面帧交错（30 fps 中仅约 1/30 帧携带桌面/二维码）；本构建默认 10 fps 后用户确认明显更快。可选后续（RC.23）：扫码页活跃时临时提速/自动帧率。
- 静态 UI 组件附近轻微、固定位置/周期性蓝色残影（视频色度噪声类）仍继续跟踪。
- 微信原生采集完全不透明（alpha=255）时自动停留单键视频路径（正确回退，非缺陷）。
- **待单独调查（非确认缺陷）**：用户反馈 RC.22 包 GUI 会话中控制件边缘锯齿/aliasing；已确认原生 PNG 为全分辨率 1440x3200，怀疑 GUI 窗口缩小显示所致（单键路径的 h264 平滑掩盖），后续单独调查。此前“静态层未应用”的反馈已由上述组件区域元数据缺失 bug 解释并修复，与本项无关。
- **诊断条件待修正（不影响功能）**：新增的 “NATIVE_UI inverse LIVE but native composite unavailable” 警告条件在编译期恒假（当前不会输出），RC.23 应修正该条件以便排查合成阻塞。

## 本阶段已完成（静态准备与版本升号）

- 版本字符串 rc.21 -> rc.22：`app/src/main.c`（launcher v2.4.2-rc.22）、`gui/main_windows.go`（v2.4.2-rc.22）、`app/scrcpy-windows.rc` 与 `gui/assets/version.rc`（`2,4,2,2200` / FileVersion `2.4.2.2200`）、`server/build.gradle`（`5.0.1-ez2.4.2-rc.22`）、`packaging/README.txt`、根 `README.md`/`README_EN.md`。
- `build-rc22.py`（由 build-rc21.py 克隆）：注入 `5.0.1-ez2.4.2-rc.22` / `SC_SOURCE_REVISION "rc22"`，输出 `out/rc22/`，构建后幂等复制运行资源；`python -m py_compile` 通过。
- `start-rc22.ps1`（由 start-rc21.ps1 克隆）：`RC22_STAGED` 窗口、`rc22-live-*` 会话、候选 `rc.22-staged`、`-Package`/`-WeChat`/`-KeyColor`/`-PerspectiveFps`（默认 10，可选 0.5/1/6/10）、`--new-display=576x1280/240 --flex-display`、`SCEZ_VD_AUTO_DPI=1`、无 `SCEZ_NATIVE_UI_*`；PowerShell 解析通过（387 tokens）。
- `packaging/rc22/` 脚手架（由 rc21 克隆）：`build/check/package/regression/verify.py` 版本与工作目录升级为 RC.22（`<historical-workspace>/rc22-release-20261029`），PE 断言 `2.4.2.2200`；`package.py` 基线 ZIP 保持 **RC.20**（RC.21 未发布不作为基线），`check.py` 的 `NATIVE_CHECKS` 已写入最终确认结果（staged pipeline 各键 `confirmed`、`fastCalibration: confirmed`、`componentRegionMetadataFix: confirmed`、`perspectiveFpsDefault: 10`、`stagedTimings`（含 `calibrationPhasesSeconds: 1.0`）、`knownIssue`、`followUpObservation`；`package.py`/`verify.py` 同步记录 `componentRegionMetadataFix`、`perspectiveFpsDefault`、诊断说明与后续调查项）。
- 静态验证：`py_compile` 全部通过；`windres` 编译两个 `.rc` 通过；注入/源码发现 dry-run 通过（`EXTRA_SOURCES` 含 `native_source.c`）；候选版本位点无 rc.21 残留。

## 发布打包前待办

- `package.py` 的 `BASE` 仍为 RC.18 开发保存点占位，需替换为冻结的 RC.20 发布提交；`regression.py`/`verify.py` 的旧基线断言同步更新。
- RC.22 设计/实测说明写入 `docs/` 后加入运行包文件清单。
- 保留“扫码速度根因已定位（1 fps 桌面帧交错）并由 10 fps 默认缓解、可选 RC.23 扫码页活跃时提速/自动帧率”“轻微固定位置蓝色残影为已知限制”“组件区域元数据缺失导致双通道合成被跳过的问题已修复（合成仅需原生帧+已学模型+GPU）”“GUI 缩小显示锯齿待单独调查（非确认缺陷）”“合成阻塞告警条件恒假待 RC.23 修正”的表述；分阶段启动数据沿用上次实机确认（纯键控 ~4.1s、双通道 ~4.6s），并注明本构建已含快速校准（四相位约 1.0s，端到端入口待用户确认）。
- 确认 RC.22 运行包内 `calibration/`、`scrcpy.png`、`disconnected.png` 与 `scrcpy.exe` 同目录（`verify.py` 已断言），然后按顺序执行 `build.py`、`regression.py`、`check.py`、`package.py`、`verify.py`。

## 说明

最终候选构建与实机验证已完成；历史 RC.20 的实机结论不等同于 RC.22。RC.21 为未发布实验，其草稿原样保留在 `packaging/rc21/`。RC.22 的扫码识别速度问题已由 10 fps 默认缓解；组件区域元数据缺失导致的“静态层未应用”问题已修复；剩余后续项（GUI 缩小显示锯齿调查、合成阻塞告警条件修正）记录在上文。
