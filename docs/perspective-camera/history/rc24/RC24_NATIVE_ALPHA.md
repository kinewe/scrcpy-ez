> 历史研究记录的脱敏副本。保留当时的实验条件、数值与阶段判断；后续结论以[总记录](../../../透视相机研发全记录_rc17至rc25_20261103.md)为准。历史命令不作为当前通用启动入口，原始日志、APK和图像未随本文件公开。

# RC24 原生 Alpha：Engine 17 诊断轮与合成前 UI 读取接口研究

## 基底与版本

- 源码：`<historical-workspace>/ez-rc24`，从 RC23 工作树 `<historical-workspace>/ez-rc23` 复制；**RC23 原树未被修改**（`tools/verify-rc23-untouched.py` 对照 2403 个文件路径与 SHA-256，PASS；证据见 `test-results/rc23-untouched-*.txt`）。
- 版本：`2.4.2-rc.24`；协议 `5.0.1-ez2.4.2-rc.24`；规则引擎 Engine 17。
- git：基线提交 `bcd24c70`（RC23 工作树快照）→ 版本升级 `c9209e2` → Engine 17 `d54b7e0` → 工具链 `093e5a4`。
- 构建入口：`python tools/build-rc24.py server`、`native`、`gui`；打包入口 `python tools/package-rc24.py`；审计 `python tools/audit-rc24.py`。
- 送帧核心文件 `app/src/perspective.c`、`app/src/desktop_capture.c`、`server/src/main/java/com/genymobile/scrcpy/video/PerspectiveCamera.java` 保持 RC22 基底内容；打包脚本会校验。

## 启动

运行本目录的 `scrcpy-ez.exe`，选择已连接设备及应用。`Start-*.cmd` 只是当前实验环境的快捷入口，其地址可以修改，不参与能力判断。不要混用旧目录的客户端或服务文件。

## 原生通道（沿用 RC23）

界面来自应用窗口缓冲层的 RGBA，不使用键色、背景反推或 PNG 与视频动态区混合；桌面背景仍沿用原有送帧流程。首帧发送整页预乘 RGBA，后续按变化行发送无损补丁；桌面端重建后校验整帧 CRC。GPU 读回后端、色域转换、逐像素 A8 精确校验与软件后备规则与 RC23 相同。

## 本轮产品侧变化：Engine 17 有界 UI 重绘诊断

触发条件：某个窗口在已有捕获路径上被判定为全不透明、即将写入 unsupported 档案时，**每个窗口 token 最多执行一次**。动作：

1. 通过正常的 shell DUMP 权限调用 `IWindow.executeCommand("DUMP")` 读取 View 树，取根 View 的 `类名@hash`；
2. 调用 `IWindow.executeCommand("CAPTURE", <根 View 标识>)`，由应用自身重绘根 View 并返回 PNG（上限 24MB、2.5s），设备内存内解码统计 A8 直方图；
3. 输出日志 `NATIVE_ALPHA_UI_REDRAW ... cause=ui_redraw_has_alpha_capture_path_opaque|ui_redraw_opaque`，并把只读诊断字符串写入 Engine 17 档案的 `uiRedraw` 字段。

边界：该诊断不写入产品 RGBA 流、不改变 View 状态、不隐藏相机 View、不新增应用规则、不参与策略选择与成功判定，也不能当作兼容成功。它的用途是把失败原因分类为“UI 原生重绘有 Alpha，但窗口捕获路径不透明”与“UI 本身没有透明孔洞”，为下一轮通道选择提供证据。详见 [RC24_UI_REDRAW_RESEARCH.md](RC24_UI_REDRAW_RESEARCH.md)。

## 本轮研究：合成前 UI 原生 Alpha（第二轮）

上一轮（codex，RC23 期间）已确认：Steam 原始 UI 重绘确有真实 Alpha；隐藏相机 View 可能破坏 UI 天然孔洞；窗口同步事务不交像素缓冲区。本轮把研究方向收敛为两条接口通道的核实：

1. **在读出阶段用 `ScreenCapture.setExcludeLayers` 分离相机 Surface**：机制在自有层对照中逐像素成立（排除相机子层后 UI 孔洞与半透明完整保留），但在免 root、不注入前提下，**没有合法途径取得相机/ Bounds 子层 SurfaceControl 句柄**。Steam 扫一扫实测：同步事务回调只交出窗口容器层 `56360` 与应用窗口根层 `56517`；应用侧 `Bounds`（56520）与相机链（56527/56528/56529）句柄不可得，`SurfaceControl` 也没有从父层枚举子句柄的 API。`CaptureArgs.excludeHandles` 需要真实句柄，伪造 Binder 无效。真实根层捕获仍为全 A=255，失败点是句柄不足而非 Alpha 阴性。
2. **直接导出 UI 原生重绘结果**：存在两个系统入口——
   - `ViewDebug CAPTURE`（shell DUMP 权限）：任意窗口可用、真实 A8。Steam 扫一扫实测 5/5 次一致（A=0 3,195,023、半透明 389,957、A=255 1,023,020），命令耗时 269–318 ms/帧（中位 289 ms，≈3.1–3.7 fps 上限），只适合诊断与低频验真；RC24 Engine 17 已用它做失败分类诊断；
   - `IWindow.requestScrollCapture`（应用内 `ScrollCaptureViewSupport` 用 `opaque=false` HardwareRenderer 回放根视图 RenderNode 到调用方 Surface）：平板 Bilibili HD 实测连接成功、连续 8/8 帧原始 `RGBA_8888`（无 PNG），requestImage RTT 中位 84 ms（流水化估算约 7–11 fps）；但硬门槛是窗口内必须存在可见可滚动 target（Steam 扫一扫返回 “No scrollable targets”），请求范围受 scrollBounds 限制（该样本只覆盖窗口 y=187 以下区域），且样本页不透明、无法验证孔洞保留。

结论：在“不 root、不装 APK、不注入应用”的约束下，目前没有“任意相机页、快速高刷新率”的通用原始 UI Alpha 导出入口。`ScrollCapture` 是唯一可用的连续原生 UI 重绘通道，但只对存在滚动目标且目标范围覆盖所需 UI 的页面成立，实测速率也低于目标刷新率；固定相机页没有通用实时入口。研究证据、实测数字与 AOSP 机制引用见 [RC24_UI_REDRAW_RESEARCH.md](RC24_UI_REDRAW_RESEARCH.md) 与 [分析_原生UI重绘与相机分离通道_20261031.md](handoffs/分析_原生UI重绘与相机分离通道_20261031.md)。

## 能力与边界

沿用 RC23：没有真实目标缓冲区、逐像素 A8 保真、持续新帧、明确的 fence/帧身份、完整 UI、相机业务不因读取被破坏、无页面重建循环这些条件同时成立，不声称成功。历史已验证样本（K80 微信等）保持不变；Steam、支付宝、向日葵、V 次元等未解决问题不因本轮而改变。当前不写入新的成功档案，不扩大应用规则库。

## 验证

- 可重复构建：server 构建两次哈希一致（`scrcpy-server` SHA-256 见 `build-rc24/out/rc24/build.json`）；DEX 内协议、Engine 17 诊断标记与旧 PNG 协议排除断言通过。
- 测试：Go 档案/策略相关 66 项通过；Java 编码器与生产 C 解码器 80 帧（16 全量 + 64 补丁，2,363,200 字节 RGBA）逐字节一致；D3D11 overlay 2048 像素逐通道一致。
- rc23 不变量：manifest 2403/2403 全部一致，missing/extra/changed = 0。
