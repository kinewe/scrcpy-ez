> 历史研究记录的脱敏副本。保留当时的实验条件、数值与阶段判断；后续结论以[总记录](../../../透视相机研发全记录_rc17至rc25_20261103.md)为准。历史命令不作为当前通用启动入口，原始日志、APK和图像未随本文件公开。

# 合成前 UI 原生 Alpha：第二轮接口研究（RC24，2026-10-31）

日期：2026-10-31。限制：ADB、免 root；不安装/启动桥接 APK，不修改目标 APK；纯 shell 允许在独立 `app_process` 中运行临时 DEX。目标仍是快速、准确取得相机覆盖 UI 的 RGBA/A8，不通过背景颜色反推透明度，不人为降低整个窗口 alpha。

实验目录：`<historical-workspace>/ez-rc24\research\precompose-ui-20261031\`（探针与日志）、`research\aosp-channel-analysis-20261031\`（AOSP 引用摘要；第三方源码不入归档）。设备日志与 AOSP 原文引用见各节。RC23 原工作树未被修改：`tools/verify-rc23-untouched.py` 对照 2403 个文件路径与 SHA-256 PASS。

## 上一轮（RC23 期间）已确认的事实

- Steam 原始 UI 重绘可以输出真实 Alpha（一次根 UI 重绘对照：1440×3200，A=0 3,195,023 像素、0<A<255 389,957 像素；该 PNG 只作诊断对照）。
- 应用窗口/场景捕获在相机活动时不透明（Steam Engine 16 实测 display-mirror、window-direct、system-scene 三阶段 min=max=255、cameraAlphaCoverage=0）。
- 隐藏相机 View 会破坏 UI 天然打孔，随后逐层清背景是在补偿该行为；应在读出阶段分离相机或直接导出 UI 原生重绘。
- 窗口同步事务只交出图层信息（LayerHandle/图层状态），没有像素缓冲区；`what` 为 `0x80100000` 等图层信息，无 buffer 变更位。

## 本轮结论（先给判定）

1. **`ScreenCapture.setExcludeLayers` 排除相机子树：机制成立，但 shell 无合法句柄来源。**
   - 机制：自有层对照证明 `setExcludeLayers` 可精确排除子层、保留父层内容与孔洞（见 `run-capture-lab` 证据）。
   - 句柄：`CaptureArgs.excludeHandles` 是真实 `IBinder[]`（JNI 调 `SurfaceControl::getHandle()`），伪造 Binder 无效。WindowOrganizer 的同步事务回调只包含窗口容器层（窗口根/Task 容器）的 LayerHandle；Steam 现场对照为 56109/56108，而 UI 子层 `Bounds for - <window>#<id>`（现场 56114）与相机 SurfaceView 子层由应用进程 `ViewRootImpl.updateAndGetBoundsLayer`（android16-release L2792-2804）创建，句柄从不跨进程。`InputWindowHandle` 的 `touchableRegionSurfaceControl` 在 native→Java 方向不被填充（`android_hardware_input_InputWindowHandle.cpp` `fromWindowInfo()` L223-301），普通窗口/表面视图也没有 crop 句柄。`SurfaceControl.java` 没有 `getChildren/getLayerHandle/fromLayerHandle` 一类可由父层枚举子层的入口。
   - 判定：在“不 root、不注入应用、不装 APK”的约束下，该方向**不可行**，不再投入。
2. **直接导出 UI 原生重绘：存在两个系统入口，能力与边界不同。**
   - **ViewDebug CAPTURE（`IWindow.executeCommand`，shell 持有 DUMP 权限）**：任意窗口可用，由应用自身重绘根 View 并返回 PNG，A8 真实；但整页重绘 + PNG 编码 + 管道传输实测命令耗时 269–318 ms/帧（中位 289 ms，≈3.1–3.7 fps 上限，见 A 实验），只能做低频诊断/验真。RC24 Engine 17 已把它接为“捕获全不透明时的一次有界诊断”。
   - **ScrollCapture（`IWindow.requestScrollCapture` → 应用内 `ScrollCaptureViewSupport` 回放）**：调用方提供 Surface，应用用 `HardwareRenderer(opaque=false)` 把**整棵根视图 RenderNode** 重绘到该 Surface，再由调用方 `requestImage` 逐帧驱动；没有 PNG 往返，平板实测 requestImage RTT 中位 84 ms（流水化估算约 7–11 fps），帧为原始 `RGBA_8888`。硬门槛：窗口内必须存在可见、`canScrollVertically` 为真的滚动 target，否则返回 “No scrollable targets”；且请求区域受该 target 的 scrollBounds 限制（实测只覆盖窗口 y=187 以下区域，见 C/C2 实验）。
   - 判定：`ScrollCapture` 是目前唯一可用的连续原生 UI 重绘通道，但**不是任意相机页通用，也不是高刷新率方案**；需要目标页存在滚动容器，并且其 scrollBounds 覆盖所需 UI 区域。
3. **原始 UI GraphicBuffer：仍无合法跨进程入口。**
   - BLASTBufferQueue 路径下，应用 UI buffer 由应用侧 BLAST 直接提交给 SurfaceFlinger；WindowOrganizer/窗口同步事务不携带 BufferData，shell 没有读取生产者缓冲区的合法 Binder/API。`ScreenCapture.captureLayers` 是系统提供的“重新合成图层”路径，不是复制生产者缓冲区（详见第 4 节）。
4. **总体判断**：不 root、不注入应用时，系统不存在“任意页面、原始 UI Alpha、快速高刷新率”的通用导出入口；可继续利用的只有“有滚动 target 的页面 + ScrollCapture”和“低频 ViewDebug 诊断”。要实现任意相机页的实时原生 UI Alpha，只能要求应用侧配合（例如应用自身输出 RGBA/回调）或更高系统权限，这超出本轮约束。

## 本轮设备实验（shell 探针）

环境：手机 Redmi K80（`<device-address>:5555`，Android 16 / SDK 36，1440×3200）；探针以 shell UID 2000 独立 `app_process` 运行，不装 APK、不注入应用、不发送点击/按键/启停；用户手动把 Steam 停在扫一扫页面。探针源码与原始日志：`research/precompose-ui-20261031/`。

已确认的 API 事实（设备反射 + AOSP16 对照）：

- 本机 `android.view.SurfaceControl` 没有 `getChildren`/`fromLayerHandle`/`fromHandle` 一类方法（只有 `nativeGetHandle(long)` 用于 Parcelable 序列化）；
- `IScrollCaptureConnection`/`IScrollCaptureCallbacks`/`ScrollCaptureResponse` 接口与 AOSP16 一致，可按上一节协议反射调用；
- `WindowInfosListener` 可正常取得应用窗口的 `IWindow` token（屏幕点亮且窗口可见时）。

### A. ViewDebug 根 UI 原生重绘（Steam 扫一扫，已测 5 次）

窗口：Steam MainActivity 的扫一扫页面；View 树 111 个节点，其中 2 个 SurfaceView（`com.facebook.react.runtime.ReactSurfaceView` 1440×3200 与 `android.view.SurfaceView@8880892` 1600×1200 相机预览）；根 View `com.android.internal.policy.DecorView@68cd31c`。通过 `IWindow.executeCommand("CAPTURE", <DecorView@hash>)` 由应用自身重绘：

| 项目 | 实测（5 次完全一致） |
|---|---|
| 输出 PNG | 189,273 字节，1440×3200 |
| A=0 | 3,195,023 像素 |
| 0<A<255 | 389,957 像素 |
| A=255 | 1,023,020 像素（min=0, max=255） |
| 命令总耗时 | min 269.3 ms / 中位 289.0 ms / max 318.5 ms |

其中耗时包含应用侧重绘 + PNG 编码 + 管道传输；探针本地的再次 PNG 编码（~232 ms）与解码（~61 ms）只用于统计，不属于产品路径。结论：**Steam 扫一扫页面的 UI 原生重绘确实有稳定真实 Alpha（含相机孔洞）**，但该通道的帧率上限约 3 fps，只能做诊断/低频验真，不能作为高刷新率实时数据源。这也再次证明“窗口/场景捕获全不透明”是捕获路径问题，不是 UI 没有 Alpha。

证据：`research/precompose-ui-20261031/logs/run-20261031-163019-A-0-com.valvesoftware.android.steam.community-5.log`。

其中耗时包含应用侧重绘 + PNG 编码 + 管道传输。命令总耗时中约 80% 与应用侧 PNG 编码/传输量级相当；扣除本机再次 PNG 编码与解码后，纯重绘 + IPC 的粗略上界约 57 ms，该通道理论帧率上限约 3.1–3.7 fps。结论：**Steam 扫一扫页面的 UI 原生重绘确实有稳定真实 Alpha（含相机孔洞）**，但该通道只能做诊断/低频验真，不能作为高刷新率实时数据源。这也再次证明“窗口/场景捕获全不透明”是捕获路径问题，不是 UI 没有 Alpha。

证据：`research/precompose-ui-20261031/logs/run-20261031-163019-A-0-com.valvesoftware.android.steam.community-5.log`。

### B. CAPTURE_LAYERS 逐视图 PNG（Steam 扫一扫）

- 输出 2,013,132 字节、31 个视图、命令耗时 2,420.86 ms；SurfaceView 视图数 = **0**（ViewDebug 对设置 `PFLAG_SKIP_DRAW` 的 SurfaceView 整段跳过，相机 SurfaceView 与 ReactSurfaceView 都不出现）。
- 根 DecorView 单层快照全 A=255（只有自身不透明背景）；各子视图出现真实透明/半透明（合计 a0=215,498、aMid=457,345、a255=26,044,300）。但每层都是“不含子 View 的单层快照”，天然孔洞的 clear op 只在整树绘制时产生，逐层拆分无法重建带孔 UI；带孔 UI 只有整树重绘（A）才有。
- 证据：`research/precompose-ui-20261031/logs/run-20261031-163126-B-0-com.valvesoftware.android.steam.community.log`。

### C. ScrollCapture（Steam 扫一扫：固定页阴性样本）

`IWindow.requestScrollCapture` 响应（13 ms）原文：

```
ScrollCaptureResponse { description = No scrollable targets found in window, connection = null,
  windowTitle = com.valvesoftware.../...MainActivity, packageName = com.valvesoftware...,
  messages = [results: complete:true cancelled:false targets: None] }
```

Steam 扫一扫是固定页，窗口内没有可滚动 target，因此没有 connection，`startCapture/requestImage` 分支未执行。该失败是“无滚动 target”，不是 UI Alpha 阴性。

### C2. ScrollCapture 连接与原始 RGBA 帧（平板 Bilibili HD，已测）

平板 `<device-address>`（横屏 3200×2136），用户把 Bilibili HD 停在详情页：

- response（17 ms）：`description = Connected`，`connection = IScrollCaptureConnection$Stub$Proxy`，`windowTitle = tv.danmaku.bilibilihd/tv.danmaku.bili.MainActivityV2`，`windowBounds = Rect(0,0-3200,2136)`，`boundsInWindow = Rect(0,187-3200,2009)`。
- 选中 target：`com.bilibili.pegasus.widget.PegasusTintRecyclerView`（ScrollCaptureViewSupport），`scrollBounds = [0,0][3200,1822]`，`positionInWindow = [0,187]`。
- `startCapture(Surface, IScrollCaptureCallbacks)` → `onCaptureStarted`；连续 `requestImage` 8 帧，8/8 收到 `onImageRequestCompleted` + ImageReader 帧：
  - `capturedArea` 均为 `Rect(0,0-3200,1778)`；帧 3200×1822、`RGBA_8888`（format=1，**无 PNG 编码**）；
  - 每帧 request→callback RTT：150/84/68/88/87/84/75/81 ms，中位 84 ms；串行循环（含 150 ms sleep + 轮询）帧间隔中位 569 ms（≈1.4–1.8 fps）；按纯 RTT 流水化估算可达约 7–11 fps；
  - A8：capturedArea 内 a0=0 / aMid=0 / a255=5,689,600（该页完全不透明）；8 帧 alpha/RGB CRC 全同；整帧底部 44 行（1822-1778）未渲染 A=0（140,800 像素）。
- 结论：**按需拉取原始 RGBA UI 重绘的通道成立**（无 PNG、帧内容来自应用内 `HardwareRenderer(opaque=false)` 回放），但该页本身不透明、`scrollBounds` 从窗口 y=187 起（播放器/顶部不在捕获范围），没有透明孔可验证，不能当作“保留孔洞”的成功样本；这是页面不透明/范围受限，不是机制失败。
- `endCapture` → `onCaptureEnded`、`close` 正常；探针全程未发送输入。
- **副作用观察（用户盯屏复测）**：17:48 在同一页面按同样 8 帧流程复测，8/8 帧收到，`capturedArea = Rect(0,0-3200,1822)`（本次为完整 scrollBounds 高度；首次为 1778，差 44 行，说明两次页面滚动/布局状态略有不同），RTT 76–100 ms（中位约 84 ms），帧间隔中位 581 ms，A8 仍全不透明（a255=5,830,400）。**用户确认测试期间列表/页面完全没有变化，未观察到可见滚动、跳动或闪动。**
- 证据：`research/precompose-ui-20261031/logs/run-20261031-163359-C-0-tv.danmaku.bilibilihd-8.log`；复测 `run-20261031-174824-C-0-tv.danmaku.bilibilihd-8.log`。

### D. 相机子层句柄与真实排除捕获

#### D(iv) 真实应用的句柄可得性（Steam 扫一扫）

- 窗口 task 611，bounds 1440×3200。`applySyncTransaction(setBounds)` 同步回调（3692 B，两轮一致）只交出 2 个 LayerHandle：`56360`（窗口容器层，uid=1000，WM shell 的 `…<window>#56360`）与 `56517`（应用 UI 根层，uid=10360，`<window>#56517`）；`rawBuffers=0`，无像素 buffer。
- Perfetto 图层快照（182 层；uid=10360 共 5 层）：`56517`（app 根）→ `56520`（`Bounds for - <window>#56520`）→ `56527`（SurfaceView 容器，relative=56517，z=-2）→ `56528`（BLAST 子层）→ `56529`（`Background for …SurfaceView[…]#56529`，相机实际子层）。
- 句柄可得性：root `56517` **可得**；bounds `56520`、camera `56527/56528/56529` **不可得**。与本轮 AOSP 结论一致：系统只在同步回调里交出窗口容器/根，不交 Bounds/相机子层。
- 真实只读捕获：`wrap(56517)` + `captureLayers(childrenOnly=false, 1440×3200)` → 全 A=255（a255=4,608,000），复现“根层捕获把相机子树一起合成→不透明”。因相机句柄不可得，未用猜测 id 冒充，真实排除捕获未打通——失败点是句柄不足，不是 UI 没有 Alpha。
- 证据：`research/precompose-ui-20261031/logs/run-20261031-163053-D-0-com.valvesoftware.android.steam.community.log`。

#### D(iii) 自有层排除语义（已通过）

在 shell 自有 SurfaceControl 层上复刻“不透明相机子层 + 带天然透明孔的 UI 层”结构，用真实 `ScreenCapture.captureLayers` 捕获并逐像素统计：

| 模式 | 捕获结果（A8 直方图） | 判定 |
|---|---|---|
| mode2：container root + 48×48 不透明相机子层 + 带孔 UI 层，不排除 | a0=1088, mid=704, a255=2304 | 基线 |
| mode2：`setExcludeLayers(cameraChild)` | a0=2816, mid=1280, a255=0 | 与逐像素预测完全一致：排除相机子树后 UI 原始孔洞与半透明像素完整保留 |
| mode1：不透明父层底 + 相机子层 + 半透明 UI 层，不排除/排除 | 两次 alpha 全 255；排除后相机区域 2304 像素 RGB 改变 | 机制成立 |

结论：`setExcludeLayers` + `wrap(LayerHandle)` 的捕获链路本身可用；瓶颈只在真实应用里拿不到相机子层的合法句柄（见 D(iv)）。

证据：`research/precompose-ui-20261031/logs/run-20261031-162740-D-0-com.valvesoftware.android.steam.community.log`。

## AOSP 机制核查

### 句柄来源（对应结论 1）

- `android.view.SurfaceControl` Java 层没有子层枚举或按 layer id 反查句柄的方法；`mirrorSurface(SurfaceControl)` 需要已有 SurfaceControl 对象。
- `LayerCaptureArgs.aidl`：`IBinder layerHandle`（root）与 `childrenOnly`；排除项在 `CaptureArgs.excludeHandles` 中以 `IBinder[]` 传输，native 侧取真实 handle。伪造 Binder 无法满足。
- `WindowOrganizerController` 的同步回调 Parcel 只序列化事务中被移动/改动的窗口容器 SurfaceControl；应用窗口内 `Bounds` 容器与 SurfaceView 子层不参与 WindowContainerTransaction。
- `InputWindowHandle` 的 `touchableRegionSurfaceControl` 是 `WeakReference<SurfaceControl>`，默认 null；JNI 从 native `WindowInfo` 转 Java 时不写入该字段；普通窗口没有 crop。
- 参考：`android_view_SurfaceControl.cpp`、`CaptureArgs.aidl`、`LayerCaptureArgs.aidl`、`android_hardware_input_InputWindowHandle.cpp`、`ViewRootImpl.java`（`updateAndGetBoundsLayer`）。

### ScrollCapture 回放（对应结论 2）

以 AOSP16 `android16-release` 为准：

- 调用链：`IWindow.requestScrollCapture(IScrollCaptureResponseListener)` → `ViewRootImpl.W.requestScrollCapture`（L11962-11967）→ `ViewRootImpl.handleScrollCaptureRequest`（L11521）→ `dispatchScrollCaptureSearch`/`ScrollCaptureSearchResults` → `ScrollCaptureResponse`（含 `IScrollCaptureConnection` Binder、窗口/包名、`boundsInWindow`、messages）。
- 该路径**没有** `WMS.requestScrollCapture` 的 `READ_FRAME_BUFFER` 权限检查；shell 以 IWindow token 即可发起（设备实测见 C 实验）。
- 连接协议：`IScrollCaptureConnection.startCapture(in Surface, in IScrollCaptureCallbacks)` / `requestImage(in Rect)` / `endCapture()` / `close()`；回调 `onCaptureStarted()`、`onImageRequestCompleted(int flags, in Rect capturedArea)`、`onCaptureEnded()`。
- 回放实现 `ScrollCaptureViewSupport.ViewRenderer`：`HardwareRenderer.setOpaque(false)`（L340）；`updateRootNode()`（L379-393）取 target 的 `source.getRootView()` 的 RenderNode，`updateDisplayListIfDirty()` 后整棵树绘制进尺寸=请求区域的 capture RenderNode，并 `canvas.translate(-mTempRect.left, -mTempRect.top)`；`renderView()`（L396-405）`syncAndDraw()` 产生 buffer。因此输出是**整窗 UI 重绘在该请求矩形内的裁剪**；SurfaceView 在 RenderNode 中只有透明孔/清屏记录，相机 buffer 不在其中。
- 限制：必须先有 target（可见、可滚动、通过 `ScrollCaptureInternal.detectScrollingType`）；请求矩形超出 scrollBounds 的部分返回空 `capturedArea`；每帧由调用方 `requestImage` 驱动，不是持续推流；目标必须是应用内可见 View，页面生命周期变化会让连接失效。

### 原始 UI 缓冲区（对应结论 3）

- 窗口同步事务（BLASTSyncEngine/TransactionState）只携带 layer state 与 handle；BufferData 仅在包含真正 buffer 的事务中出现，窗口 UI 的 buffer 由应用侧 BLASTBufferQueue 直接提交给 SurfaceFlinger，不经过 WindowOrganizer。
- 因此 shell 无法通过“窗口同步”取得像素；`ScreenCapture.captureLayers` 是新合成结果（即使只传一个图层子树），不是生产者缓冲区的复制。
- 参考：`LayerState.cpp`、`TransactionState.cpp`、`BLASTBufferQueue.java`、`SurfaceComposerClient.cpp`、`android_window_ScreenCapture.cpp`；具体行号与 URL 见 `docs/handoffs/分析_原生UI重绘与相机分离通道_20261031.md`。

## 对上一轮方向的修正

- “优先在读出阶段用 setExcludeLayers 排除相机子树”降级：句柄来源问题使该方向在不 root/不注入前提下不可行；继续尝试会被迫使用伪造/猜测 id，必须停止。
- “直接导出 UI 原生重绘”细化为两个入口：ViewDebug PNG（通用但慢）与 ScrollCapture 回放（快但要求滚动 target）。不再笼统认为“系统没有 UI 重绘入口”。
- “隐藏相机 View / 逐层清背景”继续保持上一轮结论：会破坏天然孔洞，不再作为默认补救方向。

## 后续可投入方向（需用户确认约束是否放宽）

1. 针对**存在滚动容器**的相机叠加页面，把 ScrollCapture 接入产品读路径（新 capture source），先验证逐像素 A8、持续帧、相机业务不破坏；接入前必须有真实页面实测。
2. 对固定相机页（无滚动 target），在现有约束下没有通用实时入口；可选方向是低频 ViewDebug 诊断 + 系统合成路径（当前已证伪），或放宽约束（应用侧配合/系统权限）。
3. 继续收集“UI 原生重绘有 Alpha 但捕获路径不透明”的 Engine 17 诊断样本，作为下一轮通道选择依据；不扩大应用规则库。

## 成功条件（沿用上一轮，不降低）

真实目标缓冲区/绘制来源、逐像素 A8 保真、持续新帧、明确的 fence/帧身份、完整 UI、相机业务不因读取被破坏、无页面重建循环。仅有 metadata 回调、RGBA8888 格式、非零 Alpha 或 shell 自有图像都不能算目标应用成功。

## 文件与恢复

- 探针：`research/precompose-ui-20261031/PreComposeUiProbe.java`、`run_probe.py`、`logs/`、`verified-summary.json`。
- AOSP 分析：`docs/handoffs/分析_原生UI重绘与相机分离通道_20261031.md`、`research/aosp-channel-analysis-20261031/README.md`（第三方源码目录不入归档）。
- 所有探针以 shell UID 2000 独立进程运行，创建的 Surface/HardwareBuffer/ImageReader/HardwareRenderer 均在使用后释放；不安装 APK、不修改目标应用、不发送点击/返回/启停命令；未保存目标应用 RGB 截图文件。
