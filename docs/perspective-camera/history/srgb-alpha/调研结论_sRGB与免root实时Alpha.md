> 历史研究记录的脱敏副本。保留当时的实验条件、数值与阶段判断；后续结论以[总记录](../../../透视相机研发全记录_rc17至rc25_20261103.md)为准。历史命令不作为当前通用启动入口，原始日志、APK和图像未随本文件公开。

# sRGB 与 ADB／免 root 原生 alpha 调研

日期：2026-10-28。独立调研目录；未修改 rc20、共享构建目录或正在运行的第三方应用窗口。

**2026-10-29 后续更新：** 已在用户授权的平板微信实验中，通过跨进程场景过渡取得整页原生 alpha 和 8 帧连续像素证据，不依赖设备端 PNG；重复稳定性仍未通过，存在微信主动退出。详见 [场景过渡原生Alpha实测记录](场景过渡原生Alpha实测记录.md)。下文保留 10 月 28 日初稿的调查范围和阶段判断。

## 判断

**不能把结论写成“ADB + 免 root 无法实时获取原生 alpha”。原生透明图层捕获已经实测成功，较快的捕获调用也成立；尚未打通的是从任意第三方应用取得正确的 UI 子层句柄，以及排除它的相机和其他不透明背景层。**

sRGB 本身不提供丢失 alpha 的恢复方法。它规定 RGB 的色彩空间／传递函数；Android DataSpace 文档明确说明，存在的 alpha 始终在线性范围保存，读写 sRGB 时不作相同的 RGB 转换。改成 sRGB、线性 sRGB、RGBA_F16，或给输出分配 RGBA 格式，都不能分离已经合成进去的相机。

在合成所用的颜色域中，`C = P + (1-a)B`，其中 P 是预乘前景颜色。已知 C、B 仍有 P 的三个分量和 a 四个未知量；颜色空间转换不会补回缺少的独立信息。相机为不透明底层时，整屏输出的 `a_out = a_ui + (1-a_ui)*1 = 1`，与上层原始透明度是不同的量。

参考：[Android DataSpace](https://developer.android.com/reference/android/hardware/DataSpace#DATASPACE_SRGB)。

## 新增 K80 实测

使用 USB ADB 启动 app_process，UID=2000，Android 16 / SDK 36。创建未挂到物理屏的独立 SurfaceControl 图层树：同一个父容器下有不透明绿色“相机”层和已知透明度的 UI 层。它模拟分层合成，**不是新的企业微信测试，也不是端到端虚拟屏帧率验收**。

分辨率 576×1280，737,280 像素；UI 四个等面积色块的 alpha 分别为 0、64、128、255。

| 捕获方式 | 结果 |
|---|---|
| 捕获整个父容器 | 全部 737,280 像素 alpha=255 |
| 捕获父容器，仅过滤 UID=2000 | 仍然全部 alpha=255；两层同 UID，无法分离 |
| captureLayers + setExcludeLayers 排除“相机”句柄 | 四档 alpha 各 184,320 像素，全部精确匹配 |
| 直接 captureLayers 捕获 UI 层 | 同样精确匹配四档 alpha |

每项性能测量先预热 3 次，再记录 24 次调用：

| 路径 | 中位数 | P95 |
|---|---:|---:|
| GPU 捕获并取得 HardwareBuffer | 7.17ms | 9.04ms |
| GPU 捕获 + CPU ARGB_8888 拷贝 | 23.69ms | 31.55ms |
| 上述路径 + PNG 编码 | 83.81ms | 99.09ms |

这里重复捕获相同的简单色块图，不含动画生产、PC 传输、上传或最终显示，因此不能把 7.17ms 换算后宣布已经实现 60fps。PNG 数值也不等价于复杂真实 UI 的耗时。实验说明：**存在值得继续研究的非 PNG 通路，目标层访问是前置问题。**

探针、构建与运行脚本：`LayerAlphaProbe.java`、`build_probe.py`、`run_probe.py`。原始统计：`layer-alpha-k80.log`。探针结束已删除设备 ZIP，释放自己的 Surface／图层；未安装 APK。

## Android 16 源码确认

下载核对 AOSP `android-16.0.0_r2`，源文件及 SHA-256 在 `aosp16/` 与 `source_manifest.json`。

1. `SurfaceFlinger.cpp`：`validateScreenshotPermissions` 接受 READ_FRAME_BUFFER 等能力；AOSP Shell manifest 声明了 READ_FRAME_BUFFER、ACCESS_SURFACE_FLINGER、DUMP 等权限。此处不存在“必须 UID=0”的统一要求，实测也以 shell 成功。
2. `SurfaceFlinger.cpp:7991`：captureLayers 的清底 alpha=0；captureDisplay／displayId 捕获的清底 alpha=1。**这是截图 API 的差异，不能外推成所有 VirtualDisplay→Surface 输出都强制 alpha=1。**
3. `CaptureArgs.aidl`：排除列表是 `IBinder[] excludeHandles`，不是图层名称或整数 ID。`LayerCaptureArgs` 的根同样需要有效 layerHandle。dumpsys 看到名称和 ID，不等于取得对应 Binder 句柄。
4. `IWindowManager.mirrorDisplay` 返回显示窗口树的镜像根，是合法的根入口；它不会自动过滤相机。只获得显示根、任务 leash 或应用 UID，仍不等于获得独立 UI。
5. `ViewDebug` 的外部 CAPTURE 路径固定返回 PNG；原生逐帧渲染回调 `startRenderingCommandsCapture` 要在目标进程内用实际 View、Executor 和回调对象注册。现有 IWindow 命令没有直接提供这一注册协议。
6. ViewDebug 的 INVOKE_METHOD 可以调用受支持参数的 public View 方法，但返回协议不传 SurfaceControl／Binder 对象；调用 getSurfaceControl 并不能直接把句柄带回 shell。
7. Android 16 的默认 Z-below SurfaceView，setAlpha 改变的是窗口挖洞区域。setAlpha(0) 不直接把相机 Surface 的内容 alpha 设为零；setVisibility 还可能触发 Surface 生命周期变化。不能未经实测当成通用分离办法。
8. 无障碍窗口截图也使用窗口图层树，并按应用 UID 捕获；同 UID 的相机不会因此自动排除，且接口有调用间隔检查。常规 attach-agent 仍检查目标可调试状态，不能把 shell 身份等同于可任意注入正式应用。

对应源码：[SurfaceFlinger](https://android.googlesource.com/platform/frameworks/native/+/android-16.0.0_r2/services/surfaceflinger/SurfaceFlinger.cpp)、[CaptureArgs](https://android.googlesource.com/platform/frameworks/native/+/android-16.0.0_r2/libs/gui/aidl/android/gui/CaptureArgs.aidl)、[WindowManagerService](https://android.googlesource.com/platform/frameworks/base/+/android-16.0.0_r2/services/core/java/com/android/server/wm/WindowManagerService.java)、[ViewDebug](https://android.googlesource.com/platform/frameworks/base/+/android-16.0.0_r2/core/java/android/view/ViewDebug.java)、[SurfaceView](https://android.googlesource.com/platform/frameworks/base/+/android-16.0.0_r2/core/java/android/view/SurfaceView.java)。

## 与之前真实应用证据的关系

既有 K80／小米平板 VirtualDisplay→ImageReader 测试已经证明：透明 Activity 可以保留 alpha=0／64／128／255；加入不透明背景或 SurfaceView 后变为全不透明。真实企业微信扫一扫的 378 帧整屏输出全为 alpha=255；同窗 View CAPTURE 却保留大量半透明像素。因此它的透明 UI 信息在 View 绘制结果中存在，整屏采集没有直接提供这份独立结果。

已有 576×1280 原生 PNG 实测约 8.49fps，抓取中位数 116ms。小米 miCaptureLayers／captureSpecialLayers 的既有测试也未得到独立半透明 UI，不能仅凭接口名称当成已解决。

本地证据：[VirtualDisplay alpha 报告](../virtual-display/README.md)、[原生 UI 采集报告](../native-ui/README.md)、`<historical-workspace>/rc18-layers-20261027/vendor-probe.log`。

## 后续优先级

**首选：第三方 UI 子层句柄的生命周期与来源。** 有两个需要在独立测试任务上进一步验证的入口，均不能现在宣称打通：

- WindowOrganizer／BLAST 同步事务回调：AOSP 接口可以向拥有相应 shell 权限的调用者返回 SurfaceControl.Transaction。需验证事务中是否能取得实际 UI 缓冲层的有效句柄，以及相机／SurfaceView 背景是否可单独排除。先用专用测试 Activity，不能用接管全部任务的方式直接替换系统 TaskOrganizer。
- 厂商窗口服务是否另有真正的子层捕获／句柄返回入口。现有小米合成截图结果为阴性证据，不能扩大为所有厂商接口的穷尽结论。

取得句柄后再验证：captureLayers→HardwareBuffer→GPU 提取 A8→独立无损传输 alpha。576×1280 的 A8 原始量约 0.737MB/帧，60fps 约 44.2MB/s；RGBA 原始量约 2.95MB/帧，60fps 约 176.9MB/s。均不含协议开销，未验证 USB／无线持续吞吐。继续使用有损 RGB 视频仍可能保留 RGB 压缩颗粒，独立真实 alpha 不等于颜色也无损。

**sRGB 应作为质量校验项处理**：核对源 dataspace、预乘／非预乘约定、混合工作域、视频矩阵与量化范围。需要色块对照后决定是否转换，不能盲目把所有输入线性化。它有助于解释／修复色差，不能替代分层捕获。

若某应用把相机纹理和 UI 直接画入同一个不透明缓冲区，合成器侧不存在可单独捕获的透明 UI 子层；这类情形需要应用配合、可用的进程内绘制捕获，或接受估算方案。对当前企业微信，已有 View CAPTURE 证明存在独立 UI 绘制结果，但对应实时合成器访问仍未完成。

最终定位：**“已知正确句柄 → 原生 alpha”已确认；“任意正式第三方应用 → 正确子层句柄 → 稳定实时传输”尚未完成，不能判定为绝对不可能。**
