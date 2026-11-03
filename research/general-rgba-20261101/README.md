# 免 root 的应用 UI RGBA 取流：名称过滤实验

2026-11-01；以已验收 rc25 的辅助类和无损编码器开展独立研究，没有覆盖正式 rc25 二进制或安装包。

本轮找到一条比逐页面改背景更一般的路径：系统已提供的图层名称过滤可以在捕获时排除相机子树，读取原本就独立提交的 UI。手机企业微信和 Steam 均实测通过。整个读取过程没有调用目标 View 的 setter，没有改变相机 Z 序、Alpha、背景或 Activity 的透明状态，也没有 root、安装 APK、注入目标进程。

## 接口与实际机制

手机实际存在 `ScreenCapture.CaptureArgs.Builder.setExcludeOrIncludeLayerNames(String[])` 和 `setCaptureMode(int)`。离屏实测：此构建中 mode=1 排除名称匹配的层，mode=2 仅包含名称匹配的层；名称按子串匹配，过滤不会自动沿相机父层递归到所有后代。只排除 SurfaceView 容器仍会留下 BLAST 和背景子层。

可复用的完整步骤是：

1. 只读识别当前应用唯一主窗口及独立相机 SurfaceView；用两次图层快照核验 UID、父子关系、相对根层和图层身份。
2. 根据实时父子关系收集完整相机子树的全名，包含当次 `#layerId`，不保存机型、应用页面或旧图层编号配方。
3. 创建自有显示镜像，用 UID 限定应用层，并在捕获参数中排除这批相机层。无需取得相机子层句柄，也无需窗口同步事务。
4. 校验透明源和 GPU A8 保真度，再使用原 rc25 无损 RGBA 编码器传回主机，逐帧重建并验证完整 RGBA CRC。
5. 无相机层、全透明初始化帧、全不透明结果或页面失效均不能当作成功。读取器退出时只释放自有镜像、渲染器和通信资源。

还验证过“只包含 UI 根层”入口；它在两个样本中有效，但可能漏掉应用独立提交的其他 UI 子层，因此传输原型采用排除完整相机子树的方式。

## 真机结果

设备：REDMI K80，Android 16，`OS3.0.309.0.WOKCNXM`。系统指纹见机器验证报告。企业微信 5.0.11 / 81980；Steam 3.10.11 / 10928360。分辨率均为 1440×3200。

| 实验 | 完整帧数 | 主机接收、重建及 CRC 校验吞吐 | 结果 |
| --- | ---: | ---: | --- |
| 企业微信动态扫码 UI | 600 | 24.72fps | 全帧 CRC 通过 |
| Steam 静态扫码 UI | 240 | 31.34fps | 全帧 CRC 通过 |
| 企业微信再次进入，持续约两分钟 | 1800 | 14.70fps | 全帧 CRC 通过，新图层重新匹配 |

以上包含捕获、GPU 读回、无损编码、真实 ADB、主机重建和校验，不包含 GUI 渲染。不同时间及连接状态的测量不能当作受控提速对比，也不能用 31.34fps 代表所有动态页面。Steam UI 在此测试中静止，相同 CRC 不表示持续产生了不同内容；企业微信记录了持续变化的 UI CRC。

企业微信 A8 为 76–255，半透明像素 4,565,764，全不透明像素 42,236；Steam A8 为 0–255，全透明 3,425,423、半透明 389,957、全不透明 792,620。两者都与各自 UI 重绘的 Alpha 直方图一致。Steam 静态 UI 的 Alpha CRC 也一致；企业微信异步动画帧的 CRC 不同，未宣称异步帧逐像素等价。

同一捕获帧的 GPU A8 校验精确。镜像路径的 Display P3→sRGB 转换记录 RGB 最大误差为 1，不能宣称原始 RGB 也逐字节完全一致。传输 CRC 表示主机重建的 RGBA 与编码前帧一致，不等于消除了前面的颜色空间转换。

自有 256 级 Alpha 图层对照中，完整子树名称排除与真实句柄排除的逐像素 RGBA 差异为 0。真实应用帧与自有对照分别记录，未混作一个成功样本。

无相机主页面的负例：0 帧、明确拒绝。读取过程中正常返回主页面的负例：前 25 帧完成校验，随后透明源校验失效，原型拒绝继续报成功并释放资源。早先一次主页面测试遇到 350ms 视图读取超时，未算作“无相机拒绝”成功；独立诊断读取改用有界 1500ms 后再测通过。正式产品超时逻辑未改。

## 支持边界与后续实现

这是当前 ROM 的私有扩展能力，不是所有 Android 都有的通道。AOSP 标准捕获仍使用图层句柄排除；仅按 AOSP 源码推断这台手机不存在其他入口，会漏掉本轮发现。相同方法名或 mode=1 在其他构建中也不能直接等同于本轮语义，应通过反射和自有离屏对照实际探测，不用品牌或机型白名单代替验证。

这条路线适用于能稳定识别独立相机子树的布局。本轮没有打通相机和 UI 已在 TextureView/GLES/Vulkan 中混成同一 buffer 的情况；按层排除无法从那一个 buffer 再分离两者。多预览歧义、图层重建及其他 ROM 仍需明确拒绝或重新绑定。UID 限定只保留应用层，不包含系统输入法等其他 UID 的界面。

建议后续接入为独立的只读捕获源：先做接口及语义自检，再对实际源完成两帧透明验证，生命周期变化时重新取得身份；名称过滤不可用时，才按现有有界策略选择其他来源。本次原型未接入正式 GUI，不把研究吞吐直接标作成品窗口帧率。

## 重现实验

在同一个普通投屏虚拟显示中打开目标扫码页，再执行 `build-probe.py` 和 `run-wire-benchmark.py 包名 帧数 可选标签`。读取不会替应用进入扫码页。只能自动选择唯一在线设备；多台设备时用 `RC25_RESEARCH_SERIAL` 显式指定。ADB 和本机 SDK 路径按本工作区配置，编译时读取现有 `build-rc25/out/rc25`。

`GraphicsCapabilitiesProbe` 是只读接口枚举；`NameFilterLab` 仅操作自建离屏图层；`AppNameFilterProbe` 是实际应用读取器。`verify-negative.py` 需要应用停在普通主页面；`verify-lifecycle.py` 会在已工作的实验扫码页正常执行一次返回。结束后运行 `cleanup-research.py` 清理专属临时文件、进程及转发。

依据包括本目录 `out` 的设备接口、逐帧校验及源属性日志，以及 [AOSP LayerCaptureArgs](https://android.googlesource.com/platform/frameworks/native/+/refs/heads/android16-release/libs/gui/aidl/android/gui/LayerCaptureArgs.aidl)、[AOSP ScreenCapture 实现](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/android16-release/core/java/android/window/ScreenCapture.java)。厂商名称过滤的实际支持与语义以本轮设备实验为证据。
