# 社区与本机框架复核：透明化后旧帧残留

2026-11-03下午补充：[更本质获取路径、Deferred配置故障独立复现及双背景重建实验](FOUNDATION-ACCESS-20261103.md)。该轮已区分启动失败与透明残影，仍未取得通用动态高速RGBA。

2026-11-03午间补充：[真实动态支付宝三候选复测与限时收尾](DEADLINE-VALIDATION-20261103.md)。本次扫码已持续请求25–28fps，但透明帧仍不稳定，三个候选均未形成修复；下文保留较早无效场景与独立Camera2对照的历史证据。

接续 `TEMPORAL-AND-TRAILS.md`。本轮仍没有找到已验证的免root、无需修改目标APK的稳定通用RGBA方案。新增价值是核实接口边界、排除无效场景，并补齐实验准入与异常输出。后续已完成独立Camera2对照：仅针对shell自身虚拟设备内授权，3秒收到70张图像；这是相机链路验证，不是支付宝或透明UI流验收。

## 上游与社区发现

1. Android16的ViewRootImpl根据窗口format、surfaceInsets、resize状态等更新surface opaque状态；`changeCanvasOpacity(boolean)`则操作ThreadedRenderer的opaque状态。它们不是简单等价的开关，SurfaceControl元数据变化不能单独证明渲染器已正确透明清除。[Android16 ViewRootImpl](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/android16-release/core/java/android/view/ViewRootImpl.java)

2. DecorView背景变化会涉及默认窗口格式、padding、layout及invalidate。因此重新设置背景是值得对照的候选，但现有产品本来就为根View使用`android.R.color.transparent`资源，不能把“改用透明资源”重复算作新方案。[DecorView](https://android.googlesource.com/platform/frameworks/base/+/master/core/java/com/android/internal/policy/DecorView.java)

3. ViewDebug有`setLayoutParameter(View,String,int)`静态帮助方法，但常规远程分派没有相应命令。INVOKE_METHOD接收的是View接收者上的方法，不会因帮助方法存在就自动获得任意对象调用能力。[ViewDebug分派与序列化](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/main/core/java/android/view/ViewDebug.java)

4. Filament的UiHelper将自身surface格式、TextureView opaque状态与引擎swapchain配置配套管理；这是应用内控制自身渲染的方案，未提供shell取得第三方应用RenderNode或渲染器的跨进程桥接。[Filament UiHelper](https://github.com/google/filament/blob/main/android/filament-android/src/main/java/com/google/android/filament/android/UiHelper.java)

5. Babylon React Native明确区分TextureView、SurfaceView及Z-order的透明行为，说明要按实际渲染类型分类；它的透明参数针对自己创建的EngineView，并不等于能从其他应用导出UI透明流。[Babylon React Native](https://github.com/BabylonJS/BabylonReactNative/blob/master/Modules/@babylonjs/react-native/README.md)

社区搜索还命中了旧版Rajawali透明TextureView问题及无关项目，没有找到可直接复用、满足当前部署边界的已验证跨进程实现。没有把旧设备问题、营销描述或应用内示例当作近期非小米设备实测证据；本轮也没有新增其他厂商设备结论。

## 当前K80的实际框架核对

只读反射签名与framework.jar DEX引用检查均成功。framework.jar SHA256：`587d6cdf350c59cbf122e694fd6f7c4df7c59e7353bcd9c6c0cc620531b8ac78`。原始框架只保留在忽略的out中。

- `ViewDebug.dispatchCommand`实际命令包括CAPTURE、CAPTURE_LAYERS、DUMP、DUMP_ENCODED、DUMP_THEME、INVALIDATE、INVOKE_METHOD、OUTPUT_DISPLAYLIST、PROFILE、REQUEST_LAYOUT；没有设置LayoutParams命令。
- `View.setLayerType(int,Paint)`虽然公开，但Paint不是现有基本值调用协议能直接提供的对象。`getViewRootImpl()`返回的是进程内对象，不会转换成可继续调用的远程对象句柄。
- `ViewRootImpl.changeCanvasOpacity(boolean)`存在但非public，接收者也不是View。它的DEX引用确实调用ThreadedRenderer.setOpaque。
- `ViewRootImpl.updateOpacity`引用SurfaceControl.Transaction.setOpaque及ThreadedRenderer.setSurfaceControlOpaque；ThreadedRenderer.setup另外引用setOpaque。当前厂商实现也有这些不同路径，不能仅将surface标记与绘制清除状态混为一谈。

这些结论只限制当前ViewDebug通道，不构成对所有可能系统接口“不可能”的证明。`OpaqueApiInventory.java`只读取框架签名，没有取得或修改目标应用对象。

## 三候选实验与排除原因

独立真实PC客户端display249，虚拟相机v197_1198，支付宝12.12.30.8000、PID29607、扫码任务820。与前一轮PID30820不同，进程变化是实验条件差异，未归因为任何单一原因。本轮扫码直接进入249，没有沿用早前移屏操作。

新增`PrimitiveRefreshProbe`比较：整窗INVALIDATE、根Alpha临时0.999、根背景黑色资源→透明资源重新设置并请求layout。每项24帧，另各保存3张同缓冲原始窗口PNG。日志确认根Alpha恢复1.0、原背景由Session恢复、freeform恢复mode1及原边界。

**这三组不纳入候选有效性结论**：每组只有1个不同RGBA CRC，原始图是“摄像头启动失败”错误页，未出现正的虚拟相机请求帧率。虽然最初相机服务曾记录支付宝连接v197_1198，这不足以证明相机配置和持续请求成功。因此不能写成“三方案均在有效动态扫码场景失败”，也没有新透明阳性。

返回后重开扫码一次，限时10秒仍没有新出现的正请求帧率事件。主机封装现在要求变更窗口的实验显式传入`--live-log`，并观察新的正请求帧率才执行。当前错误状态下复测此保护得到`LIVE_CAST_NOT_STREAMING`，未向目标发送变更命令。这个前置条件也不是完整验收，之后仍须检查帧变化、Alpha、UI与恢复。

独立Camera2对照探针仅允许命名的本轮虚拟相机，不回退物理相机。第一次只得到Killed/137；补充默认异常输出后，看到`owned display absent`。进一步核对表明研究客户端已经记录Device disconnected、退出2，显示249和虚拟设备已消失。因此**独立对照没有完成实际相机配置/输出验证**，不能用于断言HAL支持或不支持。也不能仅凭Killed推断系统内存回收。异常处理同样补给了SceneCleanupProbe，以便今后区分Java异常和其他退出。

上述段落记录初次对照。随后新会话display250/v198_1000也出现Device disconnected、退出2。用户确认没有其他ADB任务，不能将断连归因于其他工具。观察到5037 transport ID重新编号，但没有确认触发原因。

## 独立端口和设备内Camera2对照

确认端口5053未被占用后，仅本轮创建独立本地ADB服务，连接K80既有5555入口；保留5037/5038。display251/v199_1001会话保持到主动结束。这是一次隔离观察，不足以证明端口本身或外部程序就是断连根因。

对照探针的ID错误已修正：HAL/日志中的`v199_1001`与虚拟设备内应用看到的`0`不是同一个命名空间。探针验证非默认deviceId、设备的CAMERA策略为CUSTOM、可见ID只有0后，才使用映射的0。该机制有[AOSP VirtualDeviceCameraIdMapper](https://android.googlesource.com/platform/frameworks/av/+/refs/heads/main/services/camera/libcameraservice/utils/VirtualDeviceCameraIdMapper.h)与[CameraManager设备上下文逻辑](https://android.googlesource.com/platform/frameworks/base/+/android16-release/core/java/android/hardware/camera2/CameraManager.java)支撑，本机也读回了映射与策略。这是标准机制，不是图层名称过滤API的跨厂商证明。

在面向支付宝授权的虚拟设备199中，shell请求被明确拒绝：没有该虚拟设备内CAMERA权限。默认设备上shell的CAMERA权限原本已授予，不能将两种授权混为一谈。该次拒绝属于探针前提，不是支付宝/HAL失败证据。

最后建立目标为`com.android.shell`的独占对照会话display252/v200_1002，沿用产品已有的`grantRuntimePermission(package,CAMERA,persistentDeviceId)`设备内授权机制，未修改默认设备授权、支付宝授权或目标APK。shell没有Launcher导致正常警告，但虚拟设备和相机正常创建。

通过校验的设备200内后摄像头0，Camera2成功配置1920×1080 YUV预览，3秒收到70张Image，前65536字节的Y采样CRC有2种；客户端同时记录虚拟相机实际请求约22fps。没有保存相机帧图像。出现非致命的MIUI TipHelpersManager类缺失警告，探针最终配置成功、退出0并关闭CameraDevice/session/ImageReader。

这个对照证明：此时、此条件下，真实PC输入→本轮虚拟相机→独立Camera2客户端可以输出图像。它没有证明完整逐帧CRC、无损RGBA、PC显示、支付宝相机API路径或扫码业务。也不能据此唯一归因支付宝失败为Camera1/API兼容、缓存或权限。后续应在这些变量之间做有效对照，不能只靠包名或CAMERA已连接就判断就绪。

探针源码只在显式校验通过后打开非默认虚拟设备的映射相机，不调用默认设备的Camera.open(0)，不回退物理相机。成功记录见out/dsh-camera-authorized.log；前面的ID和权限失败也保留，以便审查实验条件。

## 复现与收尾

构建仍用本机SDK/JDK和已验证rc25 class依赖，Java/DEX编译通过，不等于重新通过完整产品发布检查。动态场景重新就绪后，示例：

```powershell
python research/alipay-texture-20261102/run-dsh-probe.py <unique-label> FreeformProbe <owned-display> <owned-task> 12 primitive-refresh --live-log <absolute-path-to-current-cast-log-in-out>
```

必须使用现场读回的显示/任务ID。上述249/820已失效，不可重用。本轮数据见out/dsh-refresh-summary.json，其中每行已标记`dynamicSceneValid=false`和`includedForEffectiveness=false`。

最终独立核对：无活动相机客户端、无实验服务/探针、无显示249、无扫码Activity，无本实验02:53关联。删除本轮专属jar、探针和诊断图；保留用户已有Companion关联、通知转发及原有deviceidle白名单。使用源码中的准确大写键`MILLET_NO_RESTRICT_APP`确认支付宝临时条目已不在名单。证据见out/dsh-refresh-cleanup.json。

正式运行目录、UI1和目标APK没有改动。本轮新增代码与报告保留本地，原始日志、截图、框架和构建产物未加入Git，也没有推送新提交。

独立端口收尾补充：对照完成后只终止核验过路径的研究客户端，已核实无活动相机客户端、无实验进程、无display250/251/252、无02:53实验关联。默认设备的shell CAMERA授权仍保持原先granted状态；未声称内部权限存储的历史条目已逐条审计。专属文件已删，只关闭本轮5053服务，未停止5037/5038；用户原有Companion关联保留。记录为out/dsh-scoped-cleanup.json。

后续仍优先寻找可恢复的应用渲染器透明/清除入口，并在真实动态预览恢复后重测三候选。此前“源残留与透明化相关”的对照证据保留；具体HWUI根因及通用修复尚未证实。
