# RC25 RGBA 获取路径调研（2026-11-01）

最新补充：[性能、分类上下文与近期ROM调研](RC25_性能与近期ROM调研_20261101.md)。同版小红书在普通物理相机与产品虚拟相机会话中分别观察到TextureView与SurfaceView，既有surface-zalpha已重新取流；不能把某一捕获路径的负例当整套产品不支持。编码减分配优化已有同场景ABBA及完整CRC验证。

续查更新：独立研究原型已在这台手机上找到厂商提供的“图层名称过滤”入口，并通过企业微信与 Steam 的只读透明取流及跨 ADB 逐帧校验。它不需要相机子层句柄，也不修改目标 View、背景或 Activity 透明状态。完整实验、支持范围和局限见 [一般化取流实验](../research/general-rgba-20261101/README.md)。以下关于 AOSP 标准句柄接口的分析仍适用，但不能扩大为“厂商系统没有其他读取入口”。该原型没有覆盖正式 rc25 二进制或安装包。

基线是 <historical-workspace>/ez-rc23-UI1；独立源码 <historical-workspace>/ez-rc25。此次没有把 RC24 的实验通道直接合入产品。

用户设想的“在相机与界面合成前介入”方向成立，但介入位置与调用权限决定能否得到独立 UI Alpha。RGBA8888 只说明像素存储格式，不说明 A8 中存在有意义的透明度；已经画平的 RGBA 可以每个像素 A=255。

## 两条实际渲染路径

- SurfaceView：应用 UI buffer（可能有透明孔）和相机子层分别提交给 SurfaceFlinger/HWC，再合成。应读 UI 自身 buffer，或捕获时排除相机子树。
- TextureView / 共享 GLES、Vulkan 渲染：相机纹理先在应用进程内混入窗口 UI。到了 SurfaceFlinger，可能只剩一个已画平的窗口 buffer。只在 eglSwapBuffers 末尾截取也不能保证分离，介入点必须早于相机绘制/混合。

依据：[AOSP TextureView 架构](https://source.android.com/docs/core/graphics/arch-tv)、[AOSP SurfaceView 架构](https://source.android.com/docs/core/graphics/arch-sv-glsv)。上述对“应在哪一级读取”的判断为据此推导，并非任意应用已测试兼容。

## 社区项目与系统接口核查

| 方案 | 能得到什么 | 对本项目的结论 |
| --- | --- | --- |
| scrcpy / DeviceFarmer minicap | 显示捕获或虚拟显示经视频/JPEG 输出 | 加速屏幕输出不等于取得合成前 UI Alpha；不是可直接替换的通用透明流 |
| 常驻 SurfaceFlinger 抓取 | 避免每帧 screencap 进程启动，输出四字节像素 | 来源仍是系统重新合成；当前查到的 Android 12 项目自述尚未完成真机验证，不能认定支持 Android 16 或高帧率透明分离 |
| ScreenCapture.captureLayers + setExcludeLayers | 具备真实句柄时可在系统重合成时排除相机子树 | 独立 SurfaceView 的正确方向；shell 仍需真实子层 Binder，图层编号不能代替句柄 |
| PixelCopy(Window / Surface) | 复制源 Surface 最近提交的 buffer | 应用内有价值；调用要求真实 Window/Surface，普通跨进程 shell 没有目标应用的这些 Java 对象/生产者接口 |
| RenderDoc / 图形 API hook | 进程内捕获 GPU 资源和绘制阶段 | 证明能在图形管线介入；官方工作流要求可调试包，属于开发/注入路线，不是免 root shell 通用导出，也不保证存在独立 UI 渲染目标 |
| Xposed 虚拟相机项目 | 在目标进程 hook Camera 预览与输入 | 解决相机输入替换，不能据此推导 UI RGBA 已被导出；需框架与目标进程作用域，不作为本轮实现 |

来源：

- [scrcpy 开发文档](https://github.com/Genymobile/scrcpy/blob/master/doc/develop.md)
- [DeviceFarmer/minicap](https://github.com/DeviceFarmer/minicap)（新 Android 路线已转为 app_process 运行 apk 容器，不能只引用旧版共享库的支持范围）
- [Android-persistent-screen-capture](https://github.com/hustuhao/Android-persistent-screen-capture)（实验性、自述未验证）
- [AOSP captureLayers 排除与透明测试](https://android.googlesource.com/platform/frameworks/native/+/716acde0fa2cd40a587ac88657c1ab48697e4a48/services/surfaceflinger/tests/ScreenCapture_test.cpp)
- [PixelCopy 官方 API](https://developer.android.com/reference/android/view/PixelCopy)
- [RenderDoc Android 工作流](https://github.com/baldurk/renderdoc/blob/v1.x/docs/how/how_android_capture.rst)
- [Xposed virtual camera](https://github.com/Xposed-Modules-Repo/com.example.vcam)

## 与既有本机实验交叉核验

以下数值来自 RC24 的 2026-10-31 实验记录，本轮没有重复测量。完整证据：<historical-workspace>/ez-rc24/docs/RC24_UI_REDRAW_RESEARCH.md。

1. 排除自有相机子层能逐像素保留透明孔；真实 Steam 窗口同步事务只给出窗口根等句柄，未给出相机子层句柄。真正失败点是句柄取得，而非 RGBA 格式不能承载 Alpha。
2. ViewDebug CAPTURE 的整树 UI 重绘能保留真实 A8，但 Steam 1440×3200 命令中位约 289ms，约 3fps 量级，只适合诊断。
3. ScrollCapture 可由应用 HardwareRenderer(opaque=false) 向调用方 Surface 回放原始 RGBA，平板有滚动容器页面 requestImage RTT 中位约 84ms；不是实测 60fps。固定扫码页无滚动 target，且捕获范围受 scrollBounds 限制，不能作为全页通用通道。
4. 高频流还要处理传输与同步。1440×3200×4×60 约 1.106 GB/s，直接读回并跨 ADB 发送完整帧不等于低延迟。若后续取得真正源 buffer，应优先 GPU/fence 管线与变化区域，再单独评估颜色编码及无损 A8 传输。

## 本轮手机企业微信补充实测

2026-11-01，企业微信 5.0.11，真实 rc25 GUI 启动，修复主窗口/Panel 歧义后进入捕获。display-mirror、window-direct、system-scene 均 A=255、相机中央透明覆盖为 0。失败恢复后，对相同扫码 Activity 的主窗口做一次只读根 UI 重绘：1440×3206，半透明 4,574,404 像素、全不透明 42,236 像素，中央半透明占比约 99.55%，命令耗时 575ms。原生 UI Alpha 存在，但该读法约 1.7fps 量级，且只是单帧统计，不能当作高速连续流。

以上为首轮失败对照，后续继续实验已经取得正常连续透明流，不能再把“三种来源全不透明”作为本次修复后的状态。

### 继续实验的结果

企业微信此次无需引入另一套 RGBA 来源。导致既有路径失败的是通用背景证据误判：View.isOpaque=false 不能证明 background Drawable 不存在。真实根节点仍有 LayerDrawable；相机 SurfaceView 从窗口下方移到上方后，不再执行原有透明孔绘制，留下的根背景就把 UI 画成全不透明。修正证据判断、在已验证可恢复的扫码子 Activity 范围清理根背景后，既有 display-mirror + GPU 路径取得了真实 A8。

依据：[AOSP SurfaceView draw/dispatchDraw/clearSurfaceViewPort](https://android.googlesource.com/platform/frameworks/base/+/HEAD/core/java/android/view/SurfaceView.java)、[AOSP Android 16 View.isOpaque](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/android16-release/core/java/android/view/View.java)。上述机制解释与本机矩阵相互吻合；不是根据单一不透明截图推测。

- 根背景实验后，系统捕获 Alpha=76–255，中央区域 100% 有透明度，与相同条件的 UI 重绘 Alpha 直方图一致。它没有证明异步帧逐像素一致。
- 正常 GUI 链路在 1440×3206 下首轮连续输出超过 4100 帧、超过 4 分钟；首次判定 1802ms，再次进入 1389ms。日志采样约十几到二十 fps，不是 60fps。精确分布、读回/编码耗时见 test-results/rc25/verification.json。
- GPU 校验 alphaExact=true；P3→sRGB 转换 rgbaExact=false、maxRgbError=1。这里的 A8 精确指同一捕获帧 GPU 读回与参考复制一致，不宣称未修改应用 UI 和适配后的 UI 逐像素完全等价。
- 首轮帧率中位 16.65fps（P10/P90 12.77/19.63），读回中位 29.45ms、编码 19.46ms、发送 0.37ms。此次优先优化对象应是读回和编码；不能仅凭这次局域网发送耗时断言其他链路没有带宽瓶颈。
- 桌面本地二维码已经通过企业微信真实识别，结果页文本与测试内容一致，返回后可再次自动适配。

这次修复没有新增企业微信包名分支。按渲染路径与现场能力做计划，能覆盖同类背景证据错误；应用将相机和 UI 预先混在同一纹理中的情况仍需另行处理。

### 合成前介入还缺哪一步

当前 AOSP ScreenCapture.setExcludeLayers 接收 SurfaceControl[]，而不是图层编号。SurfaceView.getSurfaceControl() 在应用内可以返回对象，但现有 ViewDebug 调用协议只传回有限的值类型，不能据此把目标进程的 SurfaceControl/Binder 导出给 shell。知道图层 ID 或调用到 getter，不等于取得可用于排除捕获的真实句柄。[AOSP ScreenCapture](https://android.googlesource.com/platform/frameworks/base.git/+/main/core/java/android/window/ScreenCapture.java)。

因此独立 SurfaceView 最值得继续实验的是取得真实相机子层句柄后直接排除，保留原始 UI buffer/透明孔，减少对页面属性的修改；共享 TextureView/GLES/Vulkan 则需要应用内在相机混合前导出 UI 渲染目标。这两个介入位置不能合成一条“任意应用 RGBA 开关”。本轮没有取得商业应用相机子层的可用跨进程句柄，也没有把常驻截图项目标注为已经解决此问题。

## 本轮决策

本轮检索没有发现可以在现有免 root、无应用注入的工作方式下，直接替换现有引擎、适用于任意应用且保留原始 A8 的高速现成方案。这个结论是本轮检索与现有实测的范围性判断，不声称从理论上证明所有系统接口都不可能。

后续研究应围绕“独立 UI buffer / 独立相机层 / 共享纹理”这几种渲染路径展开，不继续以应用名堆规则。若允许应用侧配合或系统级组件，可以围绕源 buffer 镜像/复制建立稳定协议；共享纹理仍须在应用渲染合成前导出 UI，增加权限本身不会从已画平结果恢复原始 Alpha。

rc25 已修复主窗口识别、背景证据和时间预算问题，并通过这台手机企业微信的连续透明流及真实扫码验收。未把上述尚未完成的源 buffer/句柄研究包装成已实现的通用通道。
