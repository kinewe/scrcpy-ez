# 合成前绘制与共享缓冲：底层通道实证

后续：[绘制端入口、GPU层声明实测与最新社区工具核对](RENDER-ENTRYPOINTS-20261103.md)。

2026-11-03晚，接续[更本质获取路径](FOUNDATION-ACCESS-20261103.md)。边界保持免root、不修改目标APK。下述实验全部在自有shell进程中，不接入支付宝、企业微信或任何用户应用，没有打开相机、虚拟显示、Activity或安装APK。

## 新进展及其边界

**共享原生RGBA缓冲跨进程传递、绘制指令序列化后跨进程回放，两条通道都在K80和现代平板验证通过。尚未取得让任意第三方应用交出合成前UI的通用入口。**

更一般的架构应当是：生产UI的进程在绘制阶段导出UI RenderNode/绘制指令，或将UI单独渲染到带Alpha的共享HardwareBuffer。消费者导入这个缓冲或回放指令，不再依赖原窗口是否opaque、是否留下历史像素，以及相机和UI是否另有SurfaceFlinger图层。

本轮验证了后半段底座，不能把自有进程的成功计为目标应用覆盖。仅会创建另一个HardwareRenderer或导入自己拥有的缓冲，并不会得到别的进程的View或RenderNode。

## 公开NDK HardwareBuffer通道

NDK的AHardwareBuffer支持跨进程共享相同内存区域，而非复制整帧像素；可作为EGL/Vulkan图形资源导入。本轮使用公开的`AHardwareBuffer_sendHandleToUnixSocket`、`recvHandleFromUnixSocket`及Java HardwareBuffer互转，使用`getId`核对两端确实引用同一缓冲。[NDK Native Hardware Buffer](https://developer.android.com/ndk/reference/group/a-hardware-buffer)

路径：

`自有UI RenderNode → HardwareRenderer → RGBA ImageReader → AHardwareBuffer句柄 → Unix socket → 另一进程导入 → RGBA读回`

生产者绘制24个不同帧：空白透明带、白色不透明带、半透明绿色带、移动红色块和编码帧序号的小块。消费者逐帧核对源CRC、缓冲ID、帧序号及Alpha抽样。源CRC按有效RGBA行计算，排除row padding；导入后CRC覆盖完整有效RGBA。

生产者计算参考CRC时先等待GPU内容可读，再发送句柄；消费者验证后ACK，生产者才释放Image允许复用。句柄传输不包含同步fence，因此这个等待和生命周期约束不能省略。验证循环有CPU读取、GPU读回、CRC和串行ACK，**不是全流程零拷贝或已流水化的产品性能测试**。

### 最新两组有效数据

|设备|系统|尺寸|帧数/不同RGBA CRC|源/导入缓冲ID不一致|完整RGBA CRC不一致|帧序号错误|Alpha带抽样错误|
|---|---|---|---|---:|---:|---:|---:|
|K80，24117RK2CC|Android16|1440×2560|24/24|0|0|0|0|
|平板，25091RP04C|Android16|1440×2560|24/24|0|0|0|0|

Alpha带抽样为A=0、255、128；完整CRC是跨进程内容一致性验证，未另做逐字节差异列表或完整UI语义验收。每组只用到1个不同缓冲ID，因为串行ACK后同一底层内存被复用；这不是只重复捕获静帧，24个RGBA CRC和帧序号均不同。

K80消费者最新循环约2064ms，平板约789ms，包括读取及最后一次指令回放。此前K80正确性循环约1145–2212ms，条件/末尾诊断工作也有变化。不可将这些短测换算成支付宝/桌面透视已支持的帧率，也未据此归因发热或资源泄漏。未测长跑、完整目标UI、主机传输和显示时延。

两设备都是小米系；它们不能替代其他厂商实测。通道本身有公开NDK依据，区别于此前小米扩展的图层名称过滤。探针使用HardwareRenderer及getId，实际要求比最早AHardwareBuffer API更高，当前只验证Android16 ARM64。

## HWUI绘制指令：已验证真实内容，非仅回调存在

自有生产者持有自己的HardwareRenderer，显式设置其进程内`setDebuggingEnabled(false)`，注册PictureCapturedCallback。两台设备各收到24次回调。此设置不是修改目标应用debuggable标志，也未改全局系统属性。

直接拿回调Picture回放时，本机两设备均出现getWidth/getHeight为0、软件和硬件Bitmap回放全透明的现象。原始SKP文件却有1440×2560的cull rect。因此不能把回调数量、Picture对象存在或文件生成当作有效UI。

进一步将Picture序列化为SKP字节，再在另一进程反序列化并回放：

|设备|自有场景SKP大小|解码边界|回放Alpha抽样|与对应最后一张源帧完整RGBA CRC|
|---|---:|---|---|---|
|K80|741字节|1440×2560|0/255/128|匹配|
|平板|741字节|1440×2560|0/255/128|匹配|

这组成功证明本轮回调确实导出了可重放的绘制内容，绕过了原始Picture直接回放异常。异常的具体厂商实现/生命周期根因未进一步反编译确认，不宣称Android所有Picture都有同样问题。

本轮场景仅由矩形组成，SKP很小。没有验证图片、字体、自定义shader、WebView/Flutter/Unity绘制或相机纹理排除，也没有验证逐帧SKP编码吞吐。最后一张指令回放匹配不等于已验证24张指令的完整回放流。

Android的HardwareRenderer回调与ViewDebug渲染指令采集需要持有相应对象，属于进程内入口；普通IWindow executeCommand没有因此增加可传递Renderer/回调对象的远程命令。[HardwareRenderer实现](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/main/graphics/java/android/graphics/HardwareRenderer.java)、[ViewDebug](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/main/core/java/android/view/ViewDebug.java)

## 重新核实SKP的root/debug边界

此前文档中的root/setup不能笼统解释成“渲染指令机制本身必需root”。本轮证明：**当调用方已在自己的进程内持有Renderer并能安装回调时，可以不靠root导出有效指令。** AOSP的SkiaPipeline在存在Picture回调时临时启用记录，与属性触发的文件采集路径有区别。[SkiaPipeline](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/main/libs/hwui/pipeline/skia/SkiaPipeline.cpp)

但属性文件采集仍有明确门槛：AOSP Properties.load使用`debuggingEnabled && ...capture_skp_enabled`。因此不能仅改可写输出路径、或只setprop，就认定能采普通发行版应用。正常目标APK的进程内对象/回调仍未取得。[Properties.load](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/main/libs/hwui/Properties.cpp)

SurfaceControlViewHost/SurfacePackage是另一条AOSP跨进程UI承载机制，但也要求内容提供者先setView并导出SurfacePackage。它不是从任意已有窗口反查其全部View/RenderNode的接口。本轮只核对源码及本机签名，未运行SurfaceControlViewHost嵌入测试。[SurfaceControlViewHost](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/main/core/java/android/view/SurfaceControlViewHost.java)

当前更准确的难点是“目标进程内的UI生产/导出端如何可达”，而非“Android缺乏可共享RGBA容器”。对一般TextureView应用，应继续寻找系统框架提供的渲染导出RPC、厂商已存在的同类入口，或明确接受应用/系统配合的方案；不能把自有生产者替换为目标Renderer当作无需实现的一步。

## 未通过的实验与实现注意

- 初次只读清单因WindowInspector类名写错而失败；修正并添加异常输出后，实际签名库存成功。没有把Killed推断成OOM。
- 广播版Surface交接没有收到消费者回调，未送出任何帧。调用返回0或未抛异常不能证明注册/交接成功。该组不用于评价Surface或ROM能力；没有完成注册失败的唯一根因确认。
- NDK第一组已传到缓冲，但导入Bitmap报缺少USAGE_GPU_SAMPLED_IMAGE。为自有ImageReader显式添加GPU采样用途后通过。该问题属于自有缓冲分配条件，不能归因目标应用不支持RGBA。
- C小库仅在自有app_process加载，没有向目标应用注入。新编译器隔离在忽略的out/native-tools，不安装到全局Python；Zig wheel及生成库哈希保存在out/buffer-bridge-build.json。编译器初次沙箱临时文件失败后，将临时/缓存目录限定于研究目录并重新构建通过。

## 代码、复现与收尾

- BufferBridgeProbe.java：双进程校验、Picture/SKP对照及40秒自身时限。
- buffer-bridge.c：公开NDK句柄发送、接收、Java互转与ID查询，ARM64实验库。
- build-buffer-bridge.py：隔离的freestanding C构建，链接设备公开NDK动态库；没有链接目标应用库。
- run-buffer-bridge.py：随机抽象Unix socket命名、双进程主机时限及独立日志。
- summarize-buffer-bridge.py：验证最新两设备48帧CRC/ID/帧序号/Alpha及两次SKP回放结果。
- BridgeApiInventory.java、SurfaceBridgeProbe.java及run-surface-bridge.py保留只读库存/未通过的广播交接对照。

Java/DEX、ARM64动态库构建通过，Python语法及git diff --check通过。这是研究探针构建，不等同正式产品发布检查。原始日志、SKP、编译器、设备动态库和构建产物均只保留在忽略的out中，无远端推送或Release。

北京时间20:57，check-renderbridge-cleanup.py核实两设备无本轮探针进程、专属jar/so/SKP文件已删除。三个全局SKP属性保持空值，本轮没有修改它们；未创建相机、虚拟显示、关联或目标Activity。只停止本轮5053服务，没有对5037/5038执行停止操作。正式运行目录和UI1未改。
