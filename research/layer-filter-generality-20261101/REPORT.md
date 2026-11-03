# 图层名称过滤泛用性调研报告

2026-11-01。遵循任务书：只研究接口来源、设备/页面覆盖和吞吐波动，不实现回退或兼容方案。条件为免 root、无需修改目标应用，使用已授权 ADB shell（UID 2000）；未安装目标 APK、注入目标进程或调用目标 View setter。

## 结论

- 这是**已在小米 ROM 证实的非 AOSP 厂商扩展**，不是所有 Android 都具备的标准隐藏 flag。尚不能断言小米原创、独占或其他厂商一定没有。
- 三台实机中，K80 和 Pad 8 Pro 的接口、离屏语义与企业微信实际取流均通过；华为 FLA-TL10 / Android 8 缺少接口，本路径不支持。两台成功设备同属 OS3.0.309.0 / Android 16，不能据此外推全部 HyperOS 或全部 API 36。
- 本轮 7 个设备×应用页面组合：2 个企业微信正例、5 个不适用。结合上一轮 Steam，共涉及 **6 个应用、8 个组合，其中 2 个应用的已测页面成功**。这不是随机样本，不能换算成市场覆盖率或承诺整款应用都支持。
- 相机与 UI 在独立 Surface 上提交时可在捕获合成时排除相机；TextureView 或 Flutter 已把二者画进同一 buffer 时，此步骤已经太晚。**独立相机子树才是有效条件，存在 SurfaceView 本身不是充分条件。**
- 低速可在不发流的编码模式复现。当前主要成本是捕获、RGBA 读回和无损编码；发送等待很小。用户现场确认手机发热，清理后台后系统动画恢复。性能、负载、电源和温度状态必须作为变量记录；不能把旧的 24.7→14.7 fps 唯一归因于泄漏或网络。

## 1. 接口来源和实际语义

实机存在 `android.window.ScreenCapture.CaptureArgs.Builder.setExcludeOrIncludeLayerNames(String[])` 及同一 Builder 的 `setCaptureMode(int)`。两台 ROM 的限定类方法、字段与方法代码哈希一致，而整份 framework.jar 的哈希不同。K80 元数据（本地历史证据，未公开）、平板元数据（本地历史证据，未公开）。

已对比 AOSP `android-16.0.0_r1`、`android16-security-release` 和 `android16-qpr2-release` 三份 `ScreenCapture.java`，均没有名称过滤字段/方法。AOSP 标准入口是 `setExcludeLayers(SurfaceControl[])`，要求具体图层句柄。[官方源码 r1](https://android.googlesource.com/platform/frameworks/base/+/android-16.0.0_r1/core/java/android/window/ScreenCapture.java)、[security 分支](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/android16-security-release/core/java/android/window/ScreenCapture.java)、源码文件和 SHA256 对照（本地历史证据，未公开）。

限定方法的反汇编显示 Java setter 将 mode 和名称数组写入新增字段，并在 CaptureArgs 中提供相应取值入口。最小证据摘录（本地历史证据，未公开）。这是从实机框架重建的 smali，**不是获得了厂商原生 C++ 源码**。名称的原生匹配算法、相关厂商仓库和内部属性门控仍待验证。

两台小米均经过自建离屏图层实测：

| 项目 | 已观察语义 |
| --- | --- |
| mode=0 / 默认 | 名称数组不产生本轮过滤效果 |
| mode=1 | 排除名称匹配图层 |
| mode=2 | 只包含名称匹配图层 |
| 名称匹配 | 子串匹配；完整实时名称连同 `#layerId` 可缩小匹配范围 |
| 父子关系 | 不自动递归；只排除容器不等于排除 BLAST/背景后代 |
| 完整子树对照 | 256 级 Alpha 测试图中，完整名称子树排除与实际句柄排除 RGBA 差异 0 |

平板语义原始日志（本地历史证据，未公开）；K80 对照保留于上一轮证据。其他类中的同名 `setCaptureMode` 不能据名称或整数值推断相同含义。

## 2. 设备 / ROM 覆盖

| 设备 | 实测系统 | 名称接口 | 离屏语义 | 实际应用 |
| --- | --- | --- | --- | --- |
| REDMI K80 / 24117RK2CC / zorn | Android 16 / SDK 36 / OS3.0.309.0.WOKCNXM | 有 | 通过 | 企业微信、上一轮 Steam 通过 |
| Xiaomi Pad 8 Pro / 25091RP04C / piano | Android 16 / SDK 36 / OS3.0.309.0.WPYCNXM | 有，限定类代码相同 | 通过 | 企业微信 5.0.3 通过 |
| 华为 FLA-TL10 / HWFLA-H | Android 8.0.0 / SDK 26 / EmotionUI_8.0.0 / 168(C01) | 无 | 不适用 | 本路径不可用，未伪造应用成功测试 |

公开构建属性（本地历史证据，未公开）、K80 能力（本地历史证据，未公开）、平板能力（本地历史证据，未公开）、华为能力（本地历史证据，未公开）。华为仍有旧式 SurfaceControl screenshot 方法，但没有本次 ScreenCapture/CaptureArgs 名称入口；这不代表新款华为或其他取流路线的结论。

成功设备共同属性包含 `ro.mi.os.version.name=OS3.0`、`ro.miui.ui.version.name=V816`、`ro.miui.ui.version.code=816`。这些只是已测构建的标识，**没有证据证明它们是开关或充分条件**；也没有得到可宣称的最低 Android API。AOSP 同为 Android 16 而缺少此扩展，说明只看 SDK 等级不足以判断。

本路径已证实的必要条件是：对应构建实际提供捕获/镜像、UID 范围和名称参数；参数有本轮过滤语义；实际页面有可分离的相机子树；排除后仍保留有效 UI Alpha。其他 ROM、其他 HyperOS 版本、不同应用版本均按待验证处理，不制定品牌白名单。

## 3. 实际相机页面

本轮相同测试几何为 1440×3200 / 600dpi 的普通应用虚拟显示，未启用旧的透视 View 修改流程。对实际窗口读取 View 层级和两次 SurfaceFlinger 身份快照，建立 UID/父子关系后创建自有离屏镜像。未更改目标图层 Alpha、Z、背景或 Activity 状态。

| 设备 | 应用版本 / code | 实际渲染结构 | 本路径结果 | 证据 |
| --- | --- | --- | --- | --- |
| K80 | 小红书 9.43.0 / 9430803 | TextureView | 不适用：整窗已合成 | k80-xhs-camera-confirm（本地历史证据，未公开） |
| K80 | 支付宝 12.12.30.8000 / 212310 | APTextureView | 不适用：整窗已合成 | k80-alipay-camera（本地历史证据，未公开） |
| K80 | 向日葵 15.5.3.81450 / 146 | HMS ScanKit + TextureView | 不适用：整窗已合成 | k80-sun-camera（本地历史证据，未公开） |
| K80 | V次元 5.0.3 / 7093 | Flutter 共享 SurfaceView | 不适用：排除后 UI 一起消失 | k80-v-camera（本地历史证据，未公开） |
| K80 | 企业微信 5.0.11 / 81980 | 独立相机 SurfaceView | 通过 | k80-wecom-camera（本地历史证据，未公开） |
| Pad 8 Pro | 小红书 9.49.1 / 9491803 | TextureView | 不适用：整窗已合成 | pad-xhs-camera（本地历史证据，未公开） |
| Pad 8 Pro | 企业微信 5.0.3 / 63936 | 独立相机 SurfaceView | 通过 | pad-wecom-camera（本地历史证据，未公开） |
| K80（上一轮已验） | Steam 3.10.11 / 10928360 | 独立相机 SurfaceView | 通过，240 帧完整 CRC | 上一轮 `general-rgba-20261101` 原始证据 |

小红书、支付宝、向日葵的已测扫码页基线均为 4,608,000 个 A255 像素，未找到独立相机 Surface；排除窗口根层得到全 A0，UI 同时丢失。这是过滤粒度不足的负例，不是透明取流成功。小红书两台均出现 `ResizableSurfaceView`，但为 **GONE、0×0**，实际预览是可见 TextureView。旧 profile 中的 `cameraKind=surface` 不能单独拿来计算支持率，也不能反推历史页面一定与本轮相同。

V次元实际进入扫码页，其原生窗口只有一个承载应用内容的独立 SurfaceView。已安装 APK 含 `libflutter.so` 和 152 个 Flutter 资产项；实际大 View `t6.i` 的父类为 `android.view.SurfaceView`。引擎和继承证据（本地历史证据，未公开）。排除该 SurfaceView 的容器、BLAST、背景后，4,608,000 像素全 A0，扫码框、说明和其他 UI 一起消失；仅排除普通窗口根层则独立 Surface 仍然存活。它是**共享渲染表面**负例，不能把该表面当独立相机。

企业微信两台排除相机子树后，手机 A8=76–255、半透明像素 4,565,764，平板 A8=76–255、半透明像素 4,570,901；UI 保留。GPU 读取路径使用同一捕获帧参考校验 A8；P3→sRGB 的 RGB 最大差异 1，不能宣称源 RGB 逐字节无损。传输 CRC 验证的是编码前 RGBA 与主机重建帧一致。

CameraService 的活动相机归属另外核实了 V次元、平板小红书及两台企业微信；向日葵也现场观察到其活动相机客户端。负例不是仅在启动页观察占位控件。

Android 官方说明 TextureView 按普通 View 参与绘制；Flutter 的 Surface/Texture renderer 绘制的是 Flutter UI。因此应用内部确实可能有合成步骤，但它发生在交给 SurfaceFlinger 之前。仅凭最终 buffer 的图层名称，不能重新拆出内部 UI 与相机。[TextureView 文档](https://developer.android.com/reference/android/view/TextureView)、[FlutterRenderer 文档](https://api.flutter.dev/javadoc/io/flutter/embedding/engine/renderer/FlutterRenderer.html)。

## 4. 帧率波动与分段实测

新增计时仅用于研究，产品默认关闭。拆分捕获、HardwareRenderer 提交/等待、ImageReader 获取、RGBA 拷贝、CRC+抽样源校验、行块哈希、补丁构造、Deflater 与发送；长跑还记录主线程 CPU 时间。下表耗时是 p50 毫秒，readback 包含其子阶段，encode 包含行哈希/补丁/Deflater，不能把父子项重复相加。capture/encode 行无主机流，fps 为设备循环；wire 行 fps 为主机接收、重建、CRC，不含 GUI 显示。

测试期间保留同一普通 H264 监视链路（30fps、12M），是额外固定负载。ADB TLS 与 TCP 均为同一 Wi-Fi 手机，非 USB 与 Wi-Fi 对比。

| 实验 | 模式 | 帧数 | fps | 捕获 | 读回 | 编码 | 发送 |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| k80-capture（本地历史证据，未公开） | capture | 360 | 30.33 | 12.76 | 12.52 | 0.00 | 0.00 |
| k80-encode（本地历史证据，未公开） | encode | 360 | 16.08 | 15.03 | 12.47 | 24.73 | 0.00 |
| k80-tls-a1（本地历史证据，未公开） | wire | 480 | 14.71 | 15.47 | 14.06 | 23.52 | 0.37 |
| k80-tcp-b1（本地历史证据，未公开） | wire | 480 | 14.72 | 15.12 | 13.20 | 24.61 | 0.37 |
| k80-tcp-b2（本地历史证据，未公开） | wire | 480 | 11.36 | 18.23 | 16.67 | 32.26 | 0.44 |
| k80-tls-a2（本地历史证据，未公开） | wire | 480 | 11.45 | 18.37 | 17.17 | 32.88 | 0.45 |
| k80-awake-wire（本地历史证据，未公开） | wire | 480 | 11.43 | 18.23 | 16.03 | 29.76 | 0.43 |
| k80-screenlock-wire（本地历史证据，未公开） | wire | 240 | 9.89 | 22.95 | 20.93 | 39.14 | 0.52 |
| k80-long（本地历史证据，未公开） | wire | 2400 | 13.59 | 16.93 | 14.89 | 23.49 | 0.42 |
| k80-recovery（本地历史证据，未公开） | wire | 120 | 16.31 | 14.92 | 14.32 | 20.96 | 0.40 |
| pad-wecom-wire（本地历史证据，未公开） | wire | 240 | 29.03 | 8.45 | 12.26 | 10.57 | 0.20 |

未插桩的原始 DEX + 正式原始 server 同场景另测 240 帧，为 **10.73 fps**，全 CRC 通过，说明低速不是新增计时代码才产生。原始构建对照（本地历史证据，未公开）。本轮完成 **5640 帧**主机完整 RGBA 重建 CRC；结合上一轮共 **8280 帧**。只统计正常完成且验证通过的实流。

### 能确定的部分

- 仅捕获读回 30.33 fps，加编码不发流 16.08 fps，低速可在网络之外出现。长跑编码平均墙钟 28.38 ms，编码线程 CPU 平均 22.39 ms，确有可观计算开销。RGBA 编码为行补丁+zlib，不是视频 MediaCodec。
- TLS→TCP→TCP→TLS 的 ABBA 结果约 14.71、14.72、11.36、11.45 fps，时间漂移大于可见传输差异。发送 p50 约 0.37–0.45 ms；本轮没有证据把 TLS 或网络发送列为主要瓶颈。
- 同步读回确有成本。长跑 render/present p50 4.09 ms、acquireImage 2.42 ms、平面拷贝 7.07 ms；不能把 readback 的全部时间都叫 GPU 同步等待。官方也指出 wait-for-present 会阻碍流水并降低吞吐。[HardwareRenderer 文档](https://developer.android.com/reference/android/graphics/HardwareRenderer.FrameRenderRequest)。本轮没有擅自修改同步策略。
- 长跑实际完成 2400 帧 / 176.9 秒，CRC 全通过，之后相机和研究投屏已关闭。过程中 interactive 从 false 变成 true，120 帧区间吞吐曾从约 10–11 提升至约 18–21 fps；它不是恒定电源状态的纯时长实验。分区间记录（本地历史证据，未公开）。
- 用户现场确认手机发热，随后清理后台并报告系统动画恢复高帧率、但仍有余热。短测重新建立同类窗口、同版本企业微信、同分辨率及编码/传输参数，得到 **16.31 fps / 120 帧 CRC 通过**。这是“清理后台后的余热状态”，不是冷机。前后 CPU 频率快照也发生变化，但快照不是持续频率或因果证明。短测（本地历史证据，未公开）、复测前电源/频率（本地历史证据，未公开）。

### 不能下定论的部分

- **历史 24.7→14.7 的唯一根因待验证。** 原记录没有逐段计时、频率和热状态；两次企业微信每帧压缩量约 182KB 与 178KB，接近，不能仅说第二次数据更大。当前数据支持“系统性能/负载状态改变，捕获读回与无损编码一起变慢”，尚不能拆分厂商温控、CPU 调度、其他后台负载及电源策略各自贡献。
- 普通 WAKEUP 后很快回到 non-interactive，不能叫全程亮屏对照；另一次临时屏幕唤醒锁确实全程 interactive，却未恢复高帧率。它与时间/发热漂移交织，不能据此认定或排除 Dozing 的影响。唤醒锁均已释放，未修改系统电源设置。
- thermalStatus 记录始终为 0，**不能据此排除厂商内部温控**。热服务存在缓存温度与当前 HAL 温度，后续采集已保留分节标题；早期平铺温度列表不用于温度定量因果判断。
- **尚不能排除所有资源泄漏。** 长跑从第 120 帧起 PSS 在约 164.5–197.8 MiB 之间随 GC 回落，未见大幅持续积累；但 nativeAllocated 到结束增加约 0.97 MiB，其缓存/JIT/待回收对象/泄漏来源尚未定位。有限时长不够证明绝无泄漏。完整内存及 GC 曲线数据（本地历史证据，未公开）。
- 行补丁路径中 `patch.toByteArray()` 每帧分配数组，长跑分配/GC 很活跃；blocking-gc-count=0 不能证明没有 GC 停顿，累计 GC 时间也不能直接当逐帧主线程停顿。本轮只定位，不改编码算法。
- 动态 UI 的 CRC 持续变化，已有生命周期离开扫码页负例验证，未观察到永久冻结的假成功；ImageReader timestamp 代表读回提交，不等于目标应用的新帧时间，重复 CRC 也不自动等于缓存错误。缓存是否参与特定慢帧仍待验证。

## 5. 建议与交付边界

1. 将本路径结论限定为“**厂商扩展存在 + 语义实测通过 + 独立相机子树**”，不能承诺免 root 的全应用 RGBA 通道。SurfaceView 独立预览有价值，TextureView/共享 Flutter 表面是明确边界。
2. 已测设备保留精确 ROM、版本与页面矩阵；其他 ROM 最低版本/门控条件继续待验证，不据两台小米编造覆盖率。
3. 下一次性能攻关应在充分降温、后台负载固定、供电/interactive 状态明确的条件下短测，并同步观察频率与阶段 CPU/墙钟。当前停止持续负载，避免把热机变化误判为代码退化。本轮不实现回退或兼容路径。

修改产品源码仅涉及 `NativeAlphaReadback.java`、`NativeAlphaFrameCodec.java` 默认关闭的研究计时。[可审阅差异](product-profiling.patch)。server 构建成功，40 个全帧/补丁/静态/重置/尺寸变化编码案例与修改前输出逐字节一致。校验日志（本地历史证据，未公开）。研究 server SHA256 为 `C96886946D75B149B2DA619AE46D04051186A3B575CC41623D61F2D247E62FDC`，未覆盖已验收 rc25 runtime、ZIP 或 UI1。

研究脚本、精简元数据、原始日志、受控自建图层试验与构建哈希随本目录保存。完整 OEM framework.jar、DEX 和目标 APK 仅留本地核验，不进入交付归档。报告没有把自建图层、启动页或全透明输出计为实际相机成功。
