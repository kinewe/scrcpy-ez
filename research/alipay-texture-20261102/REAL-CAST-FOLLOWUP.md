# 支付宝扫一扫：真实 PC 虚拟投屏补充实验

日期：2026-11-02；基线分支及提交同 [REPORT.md](REPORT.md)。本记录承接用户纠正：手机前台扫一扫、独立虚拟设备组件测试、完整 PC 投屏是三种不同实验条件。

## 当前结论

**已在真实 PC 虚拟投屏中取得带原生 Alpha 的精确窗口 RGBA，并对 32 张透明阳性帧逐张完成 GPU/CPU 一致性验证。但尚未证明完整 UI 的稳定高速通用采集。**

透明阳性不是 PNG 解码所得、RGB 反推或颜色抠图。另一方面，扫码页存在大幅蓝色网格动画，不能仅凭整帧 A255 就断定 Alpha 丢失；UI 自身不透明绘制、应用渲染器的 opaque 状态、采集输出必须分别验证。

## 真实投屏条件

- Windows 研究客户端实际创建可见 PC 窗口、虚拟显示屏及产品 `PerspectiveCamera`，桌面图像经 H.264 输入虚拟相机；不是手机自身相机预览，也不是辅助脚本自己生成的测试图。
- 第一完整会话：display245，扫码任务803，虚拟相机 `v193_1194`，支付宝实际连接 PID30820，持续约29–30fps请求。
- 隔离会话：display246，扫码任务808，虚拟相机 `v194_1195`。只停用客户端自动 NativeAlphaProbe，由单个外部探针负责可逆窗口适配；桌面输入及虚拟相机保留。活动、相机归属和 PC 窗口均实际核查。
- 手机仍为 REDMI K80 / Android16 / OS3.0.309.0.WOKCNXM；支付宝12.12.30.8000。没有 root、修改/重签目标 APK 或注入其进程。后续探针对相机 View Alpha、窗口背景和任务窗口模式进行了暂时修改，并在 finally 中恢复；不能沿用初轮“没有修改目标 View”的描述。

原实验运行时缺少客户端会尝试的 H.264 编码器。已在独立 `build/rc25-research-20261102/dist-encoders` 使用 ABI 相符的 FFmpeg8.1.2 shared DLL，真实会话选择 `h264_mf`。未替换用户安装目录的 DLL。来源：[Gyan FFmpeg builds](https://www.gyan.dev/ffmpeg/builds/)，固定包及官方页提供的 SHA256 为 `cba748035c21ce1431d0823c7a3a711f38616f89f87a265dceddf9b7f6749d2d`。

## 关键实测

| 条件 | 精确窗口帧 | 非全不透明帧 | 不同 RGBA CRC | 捕获+读回中位数 |
| --- | ---: | ---: | ---: | ---: |
| 真实投屏、隔离控制器、普通全屏1440×3200 |120|0|120|20.092ms|
| 同会话，自由窗口且边界内缩8px，1424×3184 |120|36|120|20.353ms|
| 同一自由窗口方法，逐张审计透明阳性 |180|32|180|19.905ms|

自由窗口是实际读回确认的 mode5，观察边界 Rect(8,8–1432,3192)；并非只发出切换请求。每轮最后恢复 mode1 及原边界。窗口适配使用现有产品策略：透明根背景、相机 TextureView Alpha0；恢复资源证据为 theme-class-match，不能宣称恢复了原 Drawable 对象的全部内部状态。

180帧复测中的32张透明帧，每张均将同一截图 HardwareBuffer 的 GPU RGBA 与 CPU copy 作逐字节比较：Alpha误差0，RGB最大误差0。部分帧有大量中央半透明像素，不只是边缘空白。保存了正反诊断PNG用于检查 UI；PNG编码不属于性能路径。未完成逐元素 UI 完整性验收，也未验证主机无损传输、显示时延或二维码识别业务。

这些中位数**仅为捕获+RGBA读回成本**，不包括全量CRC/逐像素统计、PNG、主机传输或PC显示。不能换算成已实现50/60fps透视效果。初轮辅助场景静止CRC及后续相机0fps也不能据此改写成连续帧成功。

额外对照：

- 原生镜像/精确窗口源各自比较 UID过滤与不带UID过滤，原始全屏条件均全A255，且RGB持续变化、非空；源选择或空截图不能解释全部现象。
- 单独把精确SurfaceControl的 opaque flag置false，或将DecorView elevation增加16，没有获得稳定透明；原flag和elevation恢复。
- Bitmap元数据审计30帧：原截图 `hasAlpha=true`，相机View实际Alpha0；GPU/CPU逐字节一致，强制`setHasAlpha(true)`没有改变结果。该30帧本身全A255，不能算透明保真阳性样本。
- 结构选择大型非相机UI叶节点，实际选中蓝色网格动画所在的`NScaleFinderView`，暂时将其Alpha设0：36帧中15帧透明；恢复原Alpha1后36帧中10帧透明。隐藏该节点没有让输出稳定，所以不能把间歇性全部归因于该动画；这只是诊断，隐藏UI不属于完整UI采集方案。
- 原始实际投屏中，结构上自动选择相机后的大型 UI 兄弟子树，得到真实Alpha0–255；PNG诊断约585ms。没有使用支付宝类名作该选择规则，但自绘、Z顺序和完整性尚未通用验证。
- ScrollCapture仍无可滚动目标。实际设备framework.jar的DEX检查未发现额外raw/SKP远程重绘命令；ViewDebug分派仍是CAPTURE等既有命令。

原始日志、两张诊断图、框架文件、DEX及JSON汇总只保留在被忽略的 `out/`，不加入提交。`summarize-real-trials.py` 和 `summarize-audit.py` 可重新生成统计。

## 上游核查与通用边界

`TextureView`与UI先在应用渲染树内合成，窗口截图没有独立相机层可排除；应用内持有View/RenderNode时，可以另行绘制到透明HardwareRenderer及RGBA ImageReader，但shell自身创建渲染器不会获得其他进程的RenderNode。[AOSP TextureView](https://source.android.com/docs/core/graphics/arch-tv)、[HardwareRenderer](https://developer.android.com/reference/android/graphics/HardwareRenderer)

ViewDebug远程CAPTURE输出PNG；渲染指令采集需要应用内View、UI线程与回调。`INVOKE_METHOD`可传递的值不足以传递Surface/RenderNode和回调对象。[ViewDebug](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/main/core/java/android/view/ViewDebug.java)

`Activity.convertToTranslucent()`有进程内Canvas opacity更新；仅调用服务端转换不等价。ViewRootImpl还区分窗口format、surfaceInsets与渲染器opaque，单纯改SurfaceFlinger元数据不足以证明渲染器透明。[Activity](https://android.googlesource.com/platform/frameworks/base.git/+/master/core/java/android/app/Activity.java)、[ViewRootImpl](https://android.googlesource.com/platform/frameworks/base/+/992097b04ef0/core/java/android/view/ViewRootImpl.java)

`ScreenshotHardwareBuffer.asBitmap()`包装HardwareBuffer；Bitmap的hasAlpha只是绘制提示，不能制造已丢失的Alpha。[ScreenCapture](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/android16-security-release/core/java/android/window/ScreenCapture.java)、[Bitmap](https://android.googlesource.com/platform/frameworks/base/+/master/graphics/java/android/graphics/Bitmap.java)

如放宽到修改目标APK，应用内采集桥接是可行的架构方向，但它需要在目标进程中运行代码、重新打包签名或相应加载环境。rootless不代表修改很小，也不代表现有支付宝能兼容。[LSPatch](https://github.com/LSPosed/LSPatch) 是修改目标APK装载DEX/SO的案例，未在本机支付宝安装或验证，不能列为已解决。

## 构建与复现

先读取本机开发说明并加载 `<project>/dev.ps1 -NoCd`。普通服务端/客户端构建情况见原报告：Java/DEX编译通过，正常发布lint仍有两条既有BlockedPrivateApi错误。研究APK跳过该lint生成，不表示发布检查通过。

1. `build-probe.py` 编译研究Java与现有SceneContext到 `out/probes.zip`。
2. `build-external-alpha-client.py` 从本机标准构建的compile_commands及ninja链接参数生成源文件副本、独立对象及`scrcpy-external-alpha.exe`，只禁用自动Alpha启动；不改产品scrcpy.c。
3. 启动实际研究客户端，读取其报告的显示ID，在该屏打开扫码；等待活动及相机实际就绪，再读取任务ID。不可猜测显示/任务ID。
4. 将探针及服务端产物用本研究专属文件名上传，通过shell app_process运行 `WindowAdaptationProbe package ownedDisplay 120`；自由窗口对照为 `FreeformProbe package ownedDisplay ownedTask 120`。末尾可选`audit`、`positive-audit`或`leaf-control`。这些模式有目标View暂时变更，必须确认本轮虚拟屏归属。
5. 结束时先用 `SceneCleanupProbe package ownedDisplay` 结束已核验的扫码子Activity，再正常关闭PC投屏窗口，防止保留任务迁回手机物理屏重新打开相机。检查本轮虚拟屏、虚拟相机、进程及forward消失，移除本研究专属临时文件。

所有窗口属性改动依赖正常执行finally；主机仍需检查异常退出和设备最终状态。研究探针不是产品稳定性保障。

本轮收尾：先精确结束display246上的扫码子Activity808，再正常关闭PC窗口，客户端退出码0，日志确认关闭相机、虚拟设备与association。相机服务最终无活动客户端；display245/246、扫码Activity、研究探针进程及PC实验窗口均不存在，forward列表为空。原始收尾证据见`out/external-cast-cleanup-*`。辅助场景主机脚本也已用相同的限定扫码子Activity清理替换单纯BACK按键。
