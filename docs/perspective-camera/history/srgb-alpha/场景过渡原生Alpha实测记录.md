> 历史研究记录的脱敏副本。保留当时的实验条件、数值与阶段判断；后续结论以[总记录](../../../透视相机研发全记录_rc17至rc25_20261103.md)为准。历史命令不作为当前通用启动入口，原始日志、APK和图像未随本文件公开。

# 平板微信整页原生 alpha：已取得像素证据，重复稳定性未通过

**2026-10-30 性能更新：** 已实际完成无线 ADB 原生 RGBA/alpha 传输与电脑离屏合成。无损 alpha 差分约 66.9 包/秒，其中约 57.3 次/秒是新画面变化；532 帧全部通过原生 CRC 校验，带宽约 0.92 MB/s。完整 RGBA 软件合成约 30/s。见 [吞吐上限实测](原生Alpha吞吐上限实测_20261030.md)。尚未接入 rc20 的真实窗口或 RGB 视频同步，入口迁移稳定性仍未解决。

日期：2026-10-29。目标：小米平板 25091RP04C，Android 16 / SDK 36，微信 `com.tencent.mm`。仅使用已授权 ADB shell（UID 2000），免 root。没有替换、重打包微信或修改其数据；安装的是独立实验 APK `com.scrcpyez.alpharenderlab`。rc20 构建目录未修改。

**后续延时对照（虚拟屏 170）：** 扫一扫先正常运行 15 秒，再启用透明渲染；过渡完成约 31.7 秒仍有逐像素 alpha，约 33 秒时文字主动隐藏/恢复的 alpha 均值为 199.735→9.193→199.735，并完成清理。已排除本轮输出仅存在于初始化前几帧或固定初始快照的解释。详见 [初始化假说_延时与主动重绘验证](初始化假说_延时与主动重绘验证.md)。此前重复退出的原因仍未确认，不能由这轮成功推导为全场景稳定。

## 结论

已经在真实微信扫一扫页面取得一组可核验的、带逐像素 alpha 的原生 RGBA 帧。相机 TextureView 被暂时隐藏，实色背景被清除，控件、渐变遮罩和扫描动画由微信自己的渲染器继续绘制。设备端没有生成 PNG，也没有通过黑白底截图反推 alpha。

**这证明本机存在可行的跨进程渲染入口；尚不能作为稳定可用的 rc20 方案。** 部分重复实验中微信进程主动退出，未拿到场景协调器。另有一轮失败是探针误选 InputSink，与微信拒绝无关，已修正。不能把这些失败混成同一种原因。

最完整的成功记录为虚拟屏 **167**。该轮完成了 8 帧采集、界面属性恢复、恢复画面 A=255 验证，以及正常关闭扫一扫页面。后续重复仍出现原 Activity 消失、进程退出，稳定性未过关。

## 可查看的证据

- `tablet-wechat/evidence-scene-vd167/suppress-scene-headless.log`：完整成功日志，独立保存，不随后续失败覆盖。
- `tablet-wechat/evidence-scene-vd167/scene-frame.argb`：设备导出的未经图像编码的像素；每像素四字节 A/R/G/B，直通道、sRGB、大端顺序。
- `tablet-wechat/native-ui-rgba.png`：在电脑端由上述字节生成的透明预览。
- `tablet-wechat/native-ui-alpha.png`：对应 alpha 灰度图。
- `tablet-wechat/native-alpha-preview.png`：左侧叠棋盘格、右侧 alpha。这里的 PNG 只用于电脑端展示，不是设备采集或传输路径。
- `tablet-wechat/evidence-scene-vd167/native-alpha-metadata.json`：原始字节摘要和 alpha 直方图。

原始帧 SHA-256：`2b29e9e5925704bc90ff7bf336fabd8cf06c6bf6fbf29c6ccf99b7f35c7e4ec2`。

## 为什么这次能得到 alpha

单独改 SurfaceFlinger 的 OPAQUE 标记或 Window 的像素格式，并不等于改变应用内部渲染器。此前这些操作仍输出整页 A=255。

本次利用 Android 的 Activity 场景过渡协议，使目标 Activity 在自己的进程中执行 `convertToTranslucent`。AOSP 路径继续调用 `WindowManagerGlobal.changeCanvasOpacity(false)`，进而改变当前根 View 对应的渲染器状态。

关键顺序：

1. 从实际输入窗口信息取得 BaseScanUI 对应的 Activity token。校验 owner UID、应用名、Activity 名、显示屏和 task，拒绝 InputSink 或空窗口 token。
2. 记录并暂时调整相机、实色祖先背景、DecorView 背景/绘制标志和 Surface format，保留可恢复的原值。
3. **先**在服务端将目标 Activity 标为 translucent，再在同一 task、同一显示屏启动独立实验应用的 BridgeActivity。
4. Bridge 通过 SceneTransitionInfo 携带空共享元素列表、非 returning 状态和 ResultReceiver；将 Bridge 转透明并结束，使下面的微信收到场景过渡。
5. 微信返回消息 100，提供自己的 EnterTransitionCoordinator。发送 103/104 一次以完成协议。提前转换服务端状态，使 `mWasOpaque=false`，过渡结束后不自动恢复成不透明渲染。
6. 通过 WindowOrganizer 同步事务取得本任务的真实 SurfaceControl handle 和 layer ID，按实际 BaseScanUI 图层筛选，调用 ScreenCapture.captureLayers，取得 HardwareBuffer/RGBA。

以上不需要把 shell 的 Java 反射能力扩展到微信对象，也不需要给微信注入执行代码。目标应用必须具备并接受此场景过渡功能；不能外推为所有应用、所有 Android 或 ROM 都适用。

源码：本地 `aosp16/EnterTransitionCoordinator.java`、`Activity.java`、`ActivityTransitionState.java`、`ActivityClientController.java`、`ActivityRecord.java`、`WindowManagerGlobal.java`。归档来源为 Android 16 AOSP；URL 和校验摘要见 `source_manifest.json`。主要路径可参阅 [AOSP EnterTransitionCoordinator](https://android.googlesource.com/platform/frameworks/base/+/android-16.0.0_r2/core/java/android/app/EnterTransitionCoordinator.java)。

## 两级验证

### 独立实验应用

同一台平板上，实验 APK 的 TargetActivity 最初确认为 `rendererOpaque=true`。清除绘制背景后，捕获仍全部 A=255。由 shell 发起上述跨进程场景过渡后，应用内实际读取到 `rendererOpaque=false`，三帧均为 A=0 的 491,520 像素、A=128 的 245,760 像素、A=255 的 0 像素。

对独立实验应用再次执行恢复流程，应用内验证 `rendererOpaque=true`。此为真实 renderer 状态对照，不只依据屏幕观感。见 `tablet-wechat/scene-control-headless.log`（虚拟屏 154）。最早初始 renderer 已为 false 的样本不是有效对照，未用于结论。

### 真实微信：虚拟屏 167

分辨率 576×1280，密度 120 dpi，逻辑短边 768 dp。物理平板为 2136×3200 / 440 dpi，逻辑短边约 777 dp。早期使用的 240 dpi 会把逻辑短边降到 384 dp；已改为保留平板布局，但**改变密度没有解决全部退出问题**。

取样前校验：相机 TextureView alpha=0；目标 SurfaceControl 的整体 alpha=1。微信返回了自己的场景协调器。随后复用已经取得的目标图层，连续取得 8 帧；每帧都有变化的 RGBA hash。

| 项目 | 成功样本结果 |
| --- | --- |
| 总像素 | 737,280 |
| A=0 | 0 |
| 0<A<255 | 737,184 |
| A=255 | 96 |
| 导出首帧 alpha 范围/不同值数量 | 1–255 / 254 种 |
| Bitmap 色彩空间 | sRGB IEC61966-2.1 |
| 原始帧字节数 | 2,949,120 |

页面渐变遮罩覆盖整页，因此没有 A=0 不代表不能透视。棋盘格预览能直接核对按钮、文字、渐变和绿色扫描光带各自的透明度。这里得到的是“隐藏相机并清除实色底后，微信原生重绘的整页 UI alpha”，不是未修改的相机合成画面里原本不存在的可分离 alpha。

## 耗时与实时链路的边界

虚拟屏 167 的 8 帧，captureLayers 调用耗时为 **2.408–3.255 ms，中位数 2.560 ms**。日志字段虽然名为 `gpuMs`，实际是捕获调用的墙钟耗时，不是 GPU 时间戳计时。

加上 Bitmap 拷贝、getPixels、直方图和 hash 统计，排除首帧写文件后为 **10.663–12.672 ms**。首帧另写约 3 MB 原始字节，总计约 66 ms；没有 PNG 编码。ADB 拉取、电脑解码、窗口呈现不包含在上述时间内。

本实验按约 120 ms 间隔取样，**没有完成 60fps 或端到端实时投屏验收**。HardwareBuffer 已经可获取，但 rc20 尚未接入这条流。

原始 RGBA 直接传输带宽较大。后续可以研究 RGB 与无损 alpha 的同步传输，或在 GPU 上导出 alpha 平面。仅作电脑端样本压缩估算：737,280 字节 alpha 平面，zlib level 1 为 120,128 字节，level 6 为 80,427 字节；这不是 Android 端压缩性能实测。

GPU/Bitmap 常用预乘颜色，而这里的 getPixels 导出是直通道 ARGB。后续合成必须明确预乘约定和 RGB 色彩空间；alpha 本身不是经 sRGB gamma 编码的颜色通道。不能通过再改 sRGB 设置恢复已经合成丢失的透明度。

## 清理和重复失败

成功轮：恢复相机 alpha=1 / opaque=true、原背景、原 Surface format 和 DecorView 绘制标志；服务端恢复原来的遮挡状态；重新取得当前图层，确认 737,280 像素全部 A=255；随后正常返回主页面，确认原 BaseScanUI token 已离开任务。原页面销毁也释放了此次调整的渲染器。最后移走任务并释放实验虚拟屏。

早期在同一微信页面重复人工恢复场景过渡，会造成额外生命周期变化，现已改用“恢复可见属性后正常关闭原页面”。即便如此，后续重复中仍有微信退出，不能宣称稳定性已修复。

系统日志有微信 `System.exit called, status: 0` 和 ExitInfo `EXIT_SELF`。用户确认只看到直接退出，没有明确的外挂或环境异常提示。现有证据**无法认定是外挂检测**，也无法认定仅由密度、配置变化或某一个过渡步骤造成。失败发生前后的生命周期、重新登录状态和原 Activity 是否仍存在，都应继续单独记录。

最后一次修正了 WindowInfosListener 将 InputSink 误选为真实窗口的问题；该轮在修改页面前失败，不属于 alpha 机制失败。修改后再次实验仍有原微信 Activity 消失，因此不能用探针修复掩盖微信退出问题。

## 复现入口与下一步

代码在独立研究目录内：`SceneResearch.java`、`WechatLayerProbe.java`、`RendererResearch.java`、`ViewNavigator.java`。实验 APK 源码位于 `scene-helper/`，其构建脚本为 `build_scene_helper.py`。探针构建还只读复用了 `<historical-workspace>/ez-mdns-rc15/research/native-ui-alpha-20261028/WindowProbe.java`，没有修改该文件。

```powershell
python build_wechat_probe.py
# 需要独立实验 APK 已安装；微信已登录。
# 此模式会将平板微信移入临时虚拟屏，自动从正常入口进入扫一扫，修改并恢复测试页面。
python run_wechat_probe.py suppress-scene-headless 0
# 只在电脑上生成可查看的 PNG。
python preview_native_alpha.py
```

runner 固定平板型号和已授权序列号，使用独立 ADB 5038。不会操作 K80 或 rc20 构建目录。失败不要无限自动重试或自动重新登录。

建议先隔离微信的生命周期/退出问题，取得多次和长时间稳定输出，再接入 rc20 的 GPU 采集、颜色与无损 alpha 同步传输和 Windows 合成。已有像素证据足以推翻“虚拟屏中免 root 一定得不到整页原生 alpha”的绝对结论，但不足以证明方案已经生产可用。
