# 绘制端入口攻关：GPU层、进程注入与厂商投屏回调

2026-11-03晚间，接续[跨进程RGBA与SKP验证](RENDER-BRIDGE-20261103.md)。保持免root、无需修改目标APK。本轮新增两台Android16设备的只读清单实验，以及K80实际框架的调用引用检索；没有新增正式产品修复。

## 当前结论

难点仍是让目标进程提供合成前UI。此前自有进程的缓冲共享、SKP导出和回放成功，不代表已能访问支付宝的Renderer。本轮没有找到可直接用于当前安装版本的通用获取入口，也没有证明所有可能入口均不存在。

新增证据缩小三个方向：

- 标准GPU层支持发行版应用主动授权的例外，不能笼统说“所有GPU捕获都必须debuggable”。但两台设备合计9个已安装包样本，均不可调试且未声明该授权。本轮没有做实际注入失败实验。
- K80投屏通知分发给进程内回调；本轮框架中找到的具体实现是SurfaceView更新自身surface。没有发现由此导出TextureView窗口RGBA或HWUI指令的调用。
- 最新APA、GFXReconstruct及社区JVMTI工具没有提供已证实满足当前部署边界的目标绘制端入口。系统性能追踪、整屏截图和合成前UI流须分别判断。

## 社区与当前工具核对

|方向|实际条件与能力|对本任务的判断|
|---|---|---|
|标准GLES/Vulkan调试层|支持可调试目标；也支持发行版目标清单主动声明injectLayers授权，另有root条件|机制比包名规则一般，但当前样本缺少标准声明前提|
|RenderDoc|作者Android指南要求目标包可调试|不能直接套用于当前支付宝；未安装或运行|
|GFXReconstruct|Android说明要求目标可调试，再通过GPU debug settings加载Vulkan层|存在命令捕获/回放能力，不代表层可装入任意发行版进程；未安装或运行|
|Android Performance Analyzer（APA）|2026年推出；System Profiler记录系统数据，Vulkan额外数据建议可调试；截图在Vulkan Layers功能下，依赖标准VK_KHR_swapchain拦截|没有证据证明可直接取当前目标的合成前UI；截图也不等于自动拆分相机和UI；未安装或实测|
|agent-injector-for-android|作者说明一般免root条件下仅能注入debug包，release需全局调试等额外系统条件|没有得到满足当前边界的新入口；未执行其代码|
|Shizuku及相关录屏项目|Shizuku提供ADB/root身份的系统API中介；RainyScreenShot说明使用shell的screencap/screenrecord|没有因此取得目标Renderer对象；后者未提供合成前UI分离实现|

依据：[GLES层](https://developer.android.com/ndk/guides/rootless-debug-gles)、[Vulkan层](https://developer.android.com/ndk/guides/graphics/validation-layer)、[RenderDoc Android指南](https://github.com/baldurk/renderdoc/blob/v1.x/docs/how/how_android_capture.rst)、[GFXReconstruct Android说明](https://github.com/LunarG/gfxreconstruct/blob/dev/USAGE_android.md)、[APA发布说明](https://android-developers.googleblog.com/2026/05/introducing-android-performance-analyzer.html)、[APA Quickstart](https://developer.android.com/android-performance-analyzer/quickstart)、[APA截图条件](https://developer.android.com/android-performance-analyzer/run)、[agent-injector作者说明](https://github.com/wuyr/agent-injector-for-android)、[Shizuku作者说明](https://github.com/RikkaApps/Shizuku)、[RainyScreenShot作者说明](https://github.com/CATMIAOZHI/RainyScreenShot/blob/main/README_EN.md)。

APA系统追踪不严格要求debuggable，不能把整个工具判作不可用于发行版。这里否定的是“已有证据表明它能直接导出当前目标合成前UI”这一推论；没有真实帧捕获实验，不声称工具实测失败。

## 只读实机实验

新增GraphicsCaptureCapabilityProbe.java，shell UID2000读取PackageManager的ApplicationInfo与GET_META_DATA，只输出包名、targetSdk、debuggable及两个图形键，不保存其他元数据。程序自限20秒，不启动目标、不安装APK、不注入或写settings。

|设备|应用|targetSdk|debuggable|injectLayers.enable|developerdriver.enable|
|---|---|---:|---|---|---|
|K80，24117RK2CC，API36|支付宝|35|false|未声明|未声明|
|同上|企业微信|31|false|未声明|未声明|
|同上|小红书|35|false|未声明|未声明|
|同上|向日葵|35|false|未声明|未声明|
|同上|微信|34|false|未声明|未声明|
|小米平板，25091RP04C，API36|企业微信|31|false|未声明|未声明|
|同上|小红书|36|false|未声明|未声明|
|同上|向日葵|35|false|未声明|未声明|
|同上|微信|34|false|未声明|未声明|

平板未安装支付宝，不计入阴性样本。合计5类应用、9个已安装包样本，描述的是检查时的安装版本，不覆盖后续更新。

完整键为com.android.graphics.injectLayers.enable、com.android.graphics.developerdriver.enable。后一个是驱动选择相关键，不是前者授权的替代条件。GLES清单授权适用于targetSdk30及以上。AOSP GraphicsEnvironment.debugLayerEnabled检查isDebuggable/canInjectLayers；后者读取injectLayers声明并调用本进程PR_SET_DUMPABLE。本机DEX也引用相同键与方法。[Java条件](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/main/core/java/android/os/GraphicsEnvironment.java)、[native实现](https://android.googlesource.com/platform/frameworks/native/+/refs/heads/main/libs/graphicsenv/GraphicsEnv.cpp)

没有在shell调用isDebuggable后将结果当成目标状态，也没有读取目标内存、审计目标是否自行改变dumpable。结论限定为标准声明前提未满足，未证明所有厂商或目标自身例外不存在。

## 本机框架引用检索

只读取得K80的三份系统JAR，在本地遍历有代码的方法，按Dalvik指令边界收集直接调用引用和字符串。inspect-render-entrypoints.py输出保存在忽略的out中。

|文件|有代码方法数|解码异常数|SHA256|
|---|---:|---:|---|
|framework.jar|251819|0|587d6cdf350c59cbf122e694fd6f7c4df7c59e7353bcd9c6c0cc620531b8ac78|
|miui-framework.jar|32968|0|3cb85c1080586de8851577dc5d60ce59c3a04571d412a29c5473813068bdecd1|
|miui-framework.thirdappopt.jar|254|0|38e5d020d64c0bc35e9aaead4251b9605366bbfb86958e8ac0f300c9b191957c|

合计285041个有代码方法。检索HardwareRenderer的Picture回调、createHardwareBitmap相关名称、setContentRoot，以及ViewDebug的capture/startRenderingCommandsCapture/setLayoutParameter。Picture回调的直接调用者是ViewDebug内部PictureCallbackHandler、StreamingPictureCallbackHandler及其生命周期方法，三份JAR中未检索出新增厂商调用者。

这是有范围的引用检索，不是全ROM可达性证明：未覆盖其他系统JAR、目标APK、原生库、反射与运行时注册；方法引用按类与名称归组，未区分重载签名。零解码异常也不等于独立验证了解码器所有语义，不能据此声称所有系统导出入口均不存在。

厂商投屏通知的实际引用链：

```text
ViewRootImpl.W.notifyCastMode / notifyProjectionMode
 → ViewRootImpl.notify…（Message发送）
 → ViewRootHandler → handleCastModeChange / handleProjectionModeChange
 → CastProjectionCallback.castModeChanged / projectionModeChanged
 → 本轮找到的具体实现SurfaceView.updateSurface
```

SurfaceView.updateSurface还引用setCastLayer、getIsProjectionMode以及小米IMiuiScreenProjectionStub.getExtraScreenProjectFlag。回调注册/删除是本进程列表操作。证据支持这与SurfaceView投屏状态有关，未支持它是通用RGBA订阅口；未穷尽完整分支或原生行为。本轮没有盲目触发通知、修改厂商投屏状态。

## 对一般性与覆盖面的影响

按渲染结构区分比堆包名规则可靠：SurfaceView相机独立图层可在合成端处理，TextureView在应用绘制阶段采样进窗口。不能预设此前一定存在独立、完整的UI RGBA流；它可能只有待执行的绘制指令。[AOSP TextureView架构](https://source.android.com/docs/core/graphics/arch-tv)

本轮提高的是对入口条件和失败边界的把握，**没有增加已验证支持的应用/厂商数量**。两台实机仍都是小米系。GPU调试层是Android标准机制，和小米图层名称过滤不同；标准能力存在，不代表当前目标允许使用，也不代表已通过其他现代OEM验证。

后续候选需要说明shell如何触达目标绘制端、如何分开相机与UI、如何保留完整Alpha和动态更新。新系统RPC或厂商桥接入口值得验证；重复自有进程的缓冲/SKP导出不能解决当前缺口。

## 复现与收尾

Java/DEX构建、Python语法检查及git diff --check通过。这是研究探针验证，不是完整产品发布检查。

run-graphics-capabilities.py在两设备运行探针，删除准确专属路径/data/local/tmp/rc25_graphics_capability_20261103.jar后，以返回成功的远程检查确认ABSENT；清理结果随包清单保存在out/graphics-capabilities.json。读取失败不计作文件已删除。

本轮没有创建相机、虚拟显示或目标Activity，没有修改全局图形属性/settings、目标APK、正式运行目录或UI1。未启动独立ADB5053，未停止5037/5038。框架JAR、原始输出及构建产物仅保留在被忽略的out中。新增代码和报告是本地未提交进展，本轮没有推送或发布Release。
