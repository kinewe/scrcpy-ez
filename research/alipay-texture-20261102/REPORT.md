# 支付宝 TextureView 原生 RGBA：社区核查及本机实验

日期：2026-11-02（Asia/Shanghai）。基线：`1db1a333`，`codex/rc25-progress-20261102`。

> 本文件记录初轮组件实验，其 display240–242 数据不是完整 PC 桌面透视投屏验收。随后已修复独立实验运行时的编码器缺失，并在真实 PC 投屏上继续实验；以 [真实投屏补充记录](REAL-CAST-FOLLOWUP.md) 的条件、结果及边界为准。

## 本轮结论

**找到了仍包含原生 Alpha 的支付宝 UI 子树，但尚未找到可从 shell 高速持续导出它的已验证入口。** 没有将 PNG 诊断输出、全不透明窗口帧、静止帧重复捕获或仅存在的 API 算成透明流成功。

实际设备为 REDMI K80 / Android 16 / OS3.0.309.0.WOKCNXM，支付宝 12.12.30.8000（212310）。无线 TLS ADB 已接通。测试使用真正的系统虚拟设备及产品 `PerspectiveCamera`，相机服务确认支付宝拥有本轮虚拟相机。未安装 APK、root、注入支付宝进程、修改支付宝 View、进行支付或验证真实二维码业务。

## 社区及上游源码核查

| 方向 | 核查结果及适用边界 |
| --- | --- |
| SurfaceFlinger / PixelCopy / 窗口 HardwareBuffer | 可以快速读取已经提交的窗口像素；TextureView 已在 HWUI 中与 UI 合成，复制窗口不会自动分离相机。[AOSP TextureView](https://source.android.com/docs/core/graphics/arch-tv)、[TextureView API](https://developer.android.com/reference/android/view/TextureView) |
| RenderNode → HardwareRenderer → ImageReader RGBA_8888 | 有效的原生渲染和读回结构；但要求持有目标内容的 RenderNode。shell 自己创建渲染器不会因此取得支付宝进程的 UI 树。[HardwareRenderer](https://developer.android.com/reference/android/graphics/HardwareRenderer) |
| ViewDebug CAPTURE / CAPTURE_LAYERS | 上游实现以 PNG 输出。实际设备能重绘支付宝独立 UI 子树，但当前测得约 331–355 ms，不能作为高速流方案。[ViewDebug 源码](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/main/core/java/android/view/ViewDebug.java) |
| startRenderingCommandsCapture / SKP | API 存在，但调用需要应用进程内的 attached View、所属 UI 线程及回调。普通 IWindow executeCommand 没有直接导出该回调的命令；INVOKE_METHOD 的基本值/字节数组协议不能传递 RenderNode、Surface 或 Java 回调。标准 SKP 采集文档还要求 root/setup，属于调试采集路径。[ViewDebug 源码](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/main/core/java/android/view/ViewDebug.java)、[Skia 采集文档](https://skia.org/docs/dev/tools/android-capture/) |
| Layout Inspector | 官方工具连接 debuggable 进程；设备上的支付宝未声明 debuggable，不能直接套用其实时重绘方案。[Layout Inspector](https://developer.android.com/studio/debug/layout-inspector) |
| ScrollCapture → 调用方 Surface | 有希望绕过 PNG，必须实际有目标回调。本轮两次有效扫码页均返回 connected=false、No scrollable targets found in window。[ScrollCaptureCallback](https://developer.android.com/reference/android/view/ScrollCaptureCallback) |
| setOpaque(false) / 透明窗口 | 需要区分 TextureView、SurfaceFlinger 图层和应用 HardwareRenderer 的 opaque 状态。Filament 的透明示例是控制自身应用的渲染及窗口，不能当作 shell 控制其他应用的现成入口。[Filament UiHelper](https://github.com/google/filament/blob/main/android/filament-android/src/main/java/com/google/android/filament/android/UiHelper.java) |

另核查了 [Rajawali TextureView 透明问题](https://github.com/Rajawali/Rajawali/issues/1908) 及 scrcpy 的相关检索，没有找到可直接复用且已证明满足本场景的免 root 跨进程高速原生 Alpha 实现。社区问题中的建议不能替代目标设备验证。

关于窗口转换的源码推断：`Activity.convertToTranslucent()` 除调用服务端外，还在应用进程内调用 `WindowManagerGlobal.changeCanvasOpacity()`。因此，仅从 shell 调用服务端转换，不能直接推断应用渲染器也已切为非 opaque。本轮未绕过既有 sceneFeature 保护、强行执行系统场景转换。[Activity 实现](https://android.googlesource.com/platform/frameworks/base.git/+/master/core/java/android/app/Activity.java)

## 有效实测

### 原生捕获及 PNG 诊断

| 实验 | 结果 |
| --- | --- |
| display240 / MainCaptureActivity / APTextureView / v188_1189 | 相机服务确认支付宝实际拥有虚拟相机；60 次原生捕获均全 A255；捕获加读回中位数 17.16 ms；包括逐像素统计和 CRC 的整体循环 16.32 次/秒，不含主机传输或显示 |
| display242 / MainCaptureActivity / APTextureView / v190_1191 | 再次确认实际相机归属；30 次原生捕获全 A255；捕获加读回中位数 23.55 ms；完整统计循环 11.62 次/秒 |
| 整窗 ViewDebug 重绘 | 两轮各 3 次，均全 A255；约 319–377 ms，包含请求、PNG、解码及统计 |
| `NScanTopView` 独立 UI 重绘，1440×3200 | A=165–255；半透明 4,567,910 像素，不透明 40,090；355.44 ms |
| `NScanMaskLayerLayout` 子树重绘，1440×3200 | A=165–255；半透明 4,580,879 像素，不透明 27,121；331.46 ms。它比上层 UI 更小，不能单独当作完整界面 |
| ScrollCapture | 两轮扫码页均收到回调，connected=false，没有可滚动目标；未开始图像会话 |

`NScanTopView` 是本轮从实际 View 树观察到的相机兄弟 UI 容器；类名仅用于诊断取样，未增加产品包名规则。其 Alpha 证明“独立重绘存在半透明 UI”，不证明完整 UI 已逐元素验收，也不证明持续更新或二维码识别正常。

原生捕获两轮都只得到 1 个不同 RGBA CRC；虚拟相机请求日志后续为 0 fps。因此这些数据仅说明捕获/读回调用成本，不能称为连续新帧吞吐，更不能将 17 ms 换算后宣称已支持 60 fps。

### 未纳入扫码结论的实验

- 首次 Windows 完整产品会话成功建立 display239 和 v187_1188，支付宝连接相机后桌面输入退出，客户端退出码 2；Alpha 恢复日志为 restored=true、0 有效帧。另用 `avcodec_find_encoder_by_name` 检查本机实验运行时：h264_nvenc、h264_mf、libx264 全部不存在。产品当前只尝试前两个编码器。本轮未更换系统或安装目录的 FFmpeg。
- 为避开桌面编码器，新增独立 `VirtualInputScene`：手机 `c2.qti.avc.encoder` 生成确定性画面，送入现有产品虚拟相机解码器；不是桌面内容透视端到端验证。
- display241 的第二轮停在 LauncherActivity.alias，没有进入扫码页；该轮数据排除。探针随后加了 MainCaptureActivity 和可见 TextureView 双重准入，防止把主页面测量当扫码结果。

## 构建、复现及清理

本机 C 客户端构建通过，独立目录：`<project>/build/rc25-research-20261102`。仅移除 `app/meson.build` 文件头的 UTF-8 BOM，修复 Meson 拒绝读取的问题。

服务端 Java/DEX 编译通过；正常 assembleRelease 被两条既有 BlockedPrivateApi lint 错误阻断（AppTaskLauncher / VirtualDisplayTaskRestorer）。研究 APK 使用 `:server:assembleRelease -x :server:lintVitalRelease` 生成，未修改 lint 配置，不能记作正常发布检查通过。

先加载本机 `dev.ps1 -NoCd`；服务端产物生成后执行 `python research/alipay-texture-20261102/build-probe.py`。构建脚本从本机 Android 36 SDK 和 Gradle 生成类编译，未使用旧 F/D 盘工具。

两份 Java 位于 `com.genymobile.scrcpy` 包。将探针 ZIP 和本机服务端 APK 用独占文件名上传到 `/data/local/tmp/`，通过 `CLASSPATH=探针:服务端 app_process / com.genymobile.scrcpy.VirtualInputScene com.eg.android.AlipayGphone` 启动场景。另一个 ADB 会话运行 `TexturePathProbe com.eg.android.AlipayGphone 30`。

**复现必须配套主机 finally 清理**：场景输出 owned association MAC、PID、虚拟显示 ID 和停止文件路径；场景有 90 秒上限，但本设备退出时出现 Killed，没有走完 Java 清理确认。主机应等待进程退出，按本轮输出的精确 MAC 解除自身创建的 companiondevice association，并移除本轮临时文件，核查相机、显示及端口；不可删除其他会话关联。禁止将 Java finally 视为已验证可靠的清理保证。

本次已额外从主机清理三个独占 association（均为本实验由 PID 派生），并核查没有本轮虚拟相机、display239–242 或转发端口。原始日志及汇总位于本目录 `out/`，已被仓库忽略，未推送。最终清理检查见 `out/cleanup-final.log`。

## 下一项可验收工作

应解决的是“把相机兄弟 UI 子树在应用实际绘制上下文中回放到可共享 Surface/HardwareBuffer”的跨进程入口，而非继续给 SurfaceFlinger 增加图层名称规则。需证明 shell 可调用、目标 UI 来源真实、无需 PNG、逐像素 Alpha 保真、持续新帧、完整 UI 和恢复均通过。

设备私有 `HardwareRenderer.createHardwareBitmapWithFlag(RenderNode,int,int,int)` 已枚举到，但它仍要求进程本地 RenderNode；参数语义及远程可达性尚未验证。不能仅凭方法存在宣称方案成立。应用配合、可调试构建或有额外权限的进程内采集是不同前提，不能混入当前免 root shell 方案的成功范围。
