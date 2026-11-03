# 更本质的RGBA获取路径：边界与实测

晚间新增：[两台现代设备的共享RGBA缓冲与绘制指令跨进程实证](RENDER-BRIDGE-20261103.md)。自有生产者两条底层通道均通过，目标应用导出入口仍未打通；并进一步区分了属性SKP采集与进程内回调的debug边界。

日期：2026-11-03下午，接续午间限时测试。部署边界仍为免root、不修改目标APK。可修改研究及产品代码，但本轮没有部署正式版变更。

## 回答：合成步骤存在，不等于存在可从外部导出的独立RGBA流

独立SurfaceView的相机缓冲交给SurfaceFlinger，UI和相机有不同图层，可以在最终合成之前排除相机层。TextureView则由应用的UI工具包/GPU先与其他View合成，再将窗口交给SurfaceFlinger。其UI可能以多个RenderNode/绘制指令存在，并未先生成一条独立、完整、可对外订阅的UI RGBA流。[AOSP TextureView架构](https://source.android.com/docs/core/graphics/arch-tv)

因此，对后者真正直接的方案，是在目标进程的绘制阶段取得UI RenderNode/指令，将相机绘制排除后回放到透明Surface/HardwareBuffer。它比窗口透明化更接近问题本身，也能避免依赖原窗口是否清除。但当前发现的ViewDebug渲染指令回调需要目标进程内的attached View、线程和回调；shell没有因此得到可传递这些对象的远程桥接。ViewDebug CAPTURE是现有慢速诊断通道，不等于这样的流接口。[ViewDebug](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/main/core/java/android/view/ViewDebug.java)

Android Studio/运行时代理不是无需条件的桥接：官方JVMTI attach只允许debuggable应用。目标支付宝不满足此前核对的debuggable条件。本轮没有尝试绕过限制、注入代码或重签APK。[Debug.attachJvmtiAgent](https://developer.android.com/reference/android/os/Debug#attachJvmtiAgent(java.lang.String,java.lang.String,java.lang.ClassLoader))、[ART TI](https://source.android.com/docs/core/runtime/art-ti)

本轮未发现满足当前边界的通用合成前跨进程入口。这个结论不是对所有厂商扩展或未来系统能力的“不可能”证明。尚未新增现代非小米设备验证。

## 直接向虚拟相机送透明RGBA，能否让相机区域自然透明？

要分别检查输入和输出。AOSP虚拟相机接受RGBA输入的着色器将`rgbaColor.xyz`转为YUV，未把输入Alpha变成目标应用的UI Alpha；公开输出集合为IMPLEMENTATION_DEFINED、YUV_420_888和BLOB。将输入格式称为RGBA，不能推导应用能收到透明预览。[EglProgram](https://android.googlesource.com/platform/frameworks/av/+/refs/heads/main/services/camera/virtualcamera/util/EglProgram.cc)、[VirtualCameraDevice](https://android.googlesource.com/platform/frameworks/av/+/refs/heads/main/services/camera/virtualcamera/VirtualCameraDevice.cc)

本机独立控制也拒绝1920×1080 RGBA_8888 ImageReader输出：configuration_failed、0张图像。同一虚拟设备的YUV单流则3秒收到87张。该对照没有实际输入Alpha0，也没有穷尽所有PRIVATE纹理内部格式；它验证的是此注册方式的RGBA输出不可用，不应夸大为一切透明输入路径均已实验排除。

## 新定位：支付宝启动失败发生在延迟绑定输出配置

### 真实支付宝A→B对照

会话display254、相机v202_1004、任务865、PID24384，K80 Android16/HyperOS3，支付宝12.12.30.8000。使用真实PC研究客户端和产品虚拟相机。没有改变目标View Alpha、背景、窗口格式或任务模式。

|条件|实际配置/结果|
|---|---|
|1440×3200，20:9虚拟显示|Deferred预览1600×720 + YUV1920×1080；UNCONFIGURED，0帧；应用日志明确endConfigure拒绝Unsupported set of inputs/outputs，页面“摄像头启动失败”|
|仅将本轮显示临时覆盖为1440×2560，16:9，再重开扫码|预览1280×720 + YUV1920×1080；ACTIVE，持续约29fps，数百张图像已产生|

此时虚拟相机实际公布的PRIVATE/YUV/JPEG输出尺寸只有640×360、1024×576、1280×720、1920×1080，未公布1600×720。两阶段没有重启支付宝进程或更换虚拟相机。没有完成A→B→A，也没有证明此前每次失败都是这一原因。

AOSP的组合检查要求各输出宽高比近似一致，且匹配注册输入配置。因此不能只加一个1600×720输入配置，就认为能接受1600×720和1920×1080的混合输出。[VirtualCameraDevice::isStreamCombinationSupported](https://android.googlesource.com/platform/frameworks/av/+/refs/heads/main/services/camera/virtualcamera/VirtualCameraDevice.cc)

### 为什么普通Surface对照一开始没有复现失败

独立shell对照将CameraManager明确绑定到非默认虚拟设备，验证CUSTOM camera policy及本地camera ID0，沿用产品仅授予自身虚拟设备内CAMERA权限的机制。没有回退物理相机。结果：

|会话/控制|配置|结果|
|---|---|---|
|device203/display255|普通PRIVATE1600×720 + YUV1920×1080|成功，3秒75张YUV、77张PRIVATE；这轮尚未记录PRIVATE实际尺寸|
|同上|普通PRIVATE1280×720 + YUV1920×1080|成功，87张YUV、89张PRIVATE|
|同上|RGBA_8888单流1920×1080|配置失败，0张|
|同上|YUV单流1920×1080|成功，87张|
|device204/display256|Deferred PRIVATE1600×720 + YUV1920×1080|配置失败，0张，复现支付宝配置阶段失败|
|同上|Deferred PRIVATE1280×720 + YUV1920×1080|配置成功，3秒81张YUV；Deferred预览没有绑定Surface，未宣称其产生预览帧|
|同上|普通PRIVATE请求1600×720 + YUV1920×1080|成功，89张YUV、91张PRIVATE；PRIVATE图像实际为1280×720|

普通Surface请求的尺寸可以被系统调整，实际到达HAL的组合不能靠请求参数猜。延迟绑定的条件不同，独立对照必须保留这一差异。上述实测解释了初步对照的“反例”；不能用初轮普通Surface成功来否定后续Deferred失败。[相机服务中的flexible consumer尺寸调整](https://android.googlesource.com/platform/frameworks/av/+/e1368e4257fa747e78eee204f136e67e176fbef9/services/camera/libcameraservice/api2/CameraDeviceClient.cpp)

此故障发生在相机配置，而非RGBA读回/分类器。因此扩大UI采样窗口或补充包名规则不能修复这一次失败。16:9是本轮有效的配置对照，尚未作为所有应用的产品布局方案发布。为什么应用选择未公布尺寸——缓存、显示比例或应用相机库策略——未唯一确认。扫描残影的具体HWUI根因仍未修复。

## 更一般的替代方向：已知双背景合成重建

这是重建Alpha，不是原生Alpha导出。对于同一不变UI的预乘前景U及Alpha a，两次已知背景可写成：

`C0 = U + (1-a)B0`，`C1 = U + (1-a)B1`。

黑/白背景在理想线性模型下可求a及U。它不依赖包名、View类型或图层名称，但要求背景响应可标定、UI在两次观测间一致；不同颜色变换、动画和采集延迟会破坏这个前提。[Smith与Blinn原论文](https://alvyray.com/Papers/CG/blusig96.pdf)

仓库此前已有未被现有启动路径调用的photo_calibration.h及输入phase字段。这里没有把双背景思想当作新发明或已接通的产品能力。本轮新增的是不修改目标View的真实虚拟相机实验、原始窗口数据、独立颜色验证及UI区域误差统计。

### 实验与结果

在上述有效16:9支付宝预览中，利用已有工程输入控制依次送黑、白、灰(0.5)、暗红(0.34,0,0)、重复黑场。每阶段取得3张原始窗口RGBA，共15张，各阶段3个CRC不同。全部原始窗口Alpha为255。只使用黑/白计算候选前景和Alpha；灰、红、重复黑均不更新模型，用于独立验证。

采样跨度约37.62秒，单阶段三帧跨度0.34–0.54秒。15张主机原始文件的长度及CRC均与设备记录一致；这只验证文件传输，不验证重建Alpha。文件写入和拉取是诊断流程，没有完成交替逐帧的实时流，更没有完成PC最终透视输出或二维码业务。候选预览看得到静态文字、按钮及圆形半透明底，不能替代完整UI/动态验收。

下面以encoded-RGB模型统计每像素RGB最大通道绝对误差，单位为8bit码值：

|验证|全图平均误差|全图误差<=8比例|可见非黑UI证据像素平均误差|该UI子集误差<=8比例|
|---|---:|---:|---:|---:|
|未参与拟合的灰场|2.72|96.03%|36.44|23.65%|
|未参与拟合的暗红场|6.87|96.20%|33.71|34.26%|
|重复黑场|1.71|96.77%|41.44|31.59%|

“可见非黑UI证据”仅是初次黑场RGB最大值>16的65,672像素，不是语义级完整UI掩码。它包含动画，且可能遗漏黑色/很暗UI。全图通过率被大片背景和静态区域抬高；不能只报告96%作为成功。训练期内部一致的该UI子集有20,675像素，其中红场/重复黑场误差<=8分别约99.96%/99.48%，但灰场只有74.33%。不能据此宣称其余UI可舍弃，或训练筛选必能排除时序错误。

重复黑场有119,024像素变化超过8。对比linear-sRGB模型后，UI动态误差仍在；没有通过改颜色空间就解决问题。还缺少相机颜色响应标定，因此不能把全部误差归因动画。没有原生Alpha真值，未验证Alpha逐像素精度；RGB重投影一致也不是独立Alpha真值。

**判定：可作静态UI重建研究，尚不能作为通用动态高速RGBA方案。** 本轮未隐藏扫描动画、未将未知像素静默当透明，也没有将漂亮的棋盘背景预览当验收。若继续这条路径，应先验证相邻帧编码/回传时序、颜色响应标定和动态UI误差，再谈GPU化吞吐。

## 当前取舍

|路径|是否在合成前直接取UI|目前证据|主要下一关|
|---|---|---|---|
|独立相机Surface图层排除|是，窗口UI与相机本来分开|此前企业微信/小红书特定实际场景有效|实际结构、句柄/API的跨ROM覆盖|
|在应用绘制上下文回放UI RenderNode/指令|是，更接近TextureView根问题|渲染机制明确，未找到当前部署边界下的通用远程桥|合法可达的系统/应用内采集接口|
|原窗口透明化|仍依赖原窗口绘制|有透明阳性但残影、稳定性未通过|Canvas opacity/清除状态的可控入口与恢复|
|透明虚拟相机输入|假设能保留相机纹理Alpha|AOSP转换路径不保留输入Alpha；本机RGBA输出被拒|未验证其他PRIVATE格式，但没有当前可用依据|
|已知双背景重建|否，合成结果反求|本轮静态元素可见，动态UI误差明显|时序、颜色响应、完整动态质量；属于重建，不能称原生流|

对现有“按实际渲染结构分类”的信心增加了：Deferred输出、普通Surface、独立相机层和共享窗口确实必须区分。对“无需额外进程内接口就能普遍直接导出TextureView完整原生RGBA”的信心没有增加。应继续把这两种判断分开。

## 实现、证据与清理

新增CompositeResponseProbe、run-composite-test.py、analyze-composite-response.py；VirtualCameraControlProbe增加普通双流/Deferred双流/RGBA控制及实际PRIVATE图像尺寸读回。构建探针Java/DEX通过，Python语法检查通过，git diff --check通过。不等同正式发布全套检查。

原始证据仅在忽略的out中：composite-camera-failure.txt、composite-camera-resized.txt、composite-response-index.json、composite-analysis.json、foundation-control-summary.json、foundation-deferred-summary.json及研究日志。候选图文件名明确含unvalidated。最初控制摘要的advertisedYuvSizes因JSONObject无法直接包装Size而显示null，已在后续改为显式字符串；以v2摘要为准，不据null推断无可用尺寸。

17:46独立清理核对通过：显示254/255/256、相机客户端、任务865和研究进程均不存在；恢复254的尺寸覆盖后才结束会话；18个本轮远端诊断文件删除；原关联58和白名单保留，支付宝/shell的临时MILLET条目撤销。仅停止本轮5053服务器，5037/5038保留。研究客户端分别为主动terminate或时限terminate，未标为正常关闭回归通过。shell临时虚拟设备CAMERA授权沿用现有产品机制，未改默认设备授权。

未安装目标APK、未root、未注入目标进程，未改UI1或正式运行目录；本轮代码和报告在本地，无Release、无新远端推送。
