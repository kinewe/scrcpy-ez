> 历史研究记录的脱敏副本。保留当时的实验条件、数值与阶段判断；后续结论以[总记录](../../../透视相机研发全记录_rc17至rc25_20261103.md)为准。历史命令不作为当前通用启动入口，原始日志、APK和图像未随本文件公开。

# 平板微信原始 alpha 实验

内部渲染器后续研究见同目录《内部渲染器研究记录.md》。其中已用独立 HardwareRenderer 验证旧 alpha 残留与透明清屏的关系；尚未成功切换微信内部 renderer。

对象：小米平板 25091RP04C，Android 16，微信 com.tencent.mm，扫一扫 BaseScanUI。仅使用 ADB shell，免 root。rc20 仓库与构建文件未修改。

## 已验证

- WindowOrganizer.applySyncTransaction 即使保持任务边界不变，也可向 shell 返回 SurfaceControl.Transaction。
- 从事务中提取 android.Layer.LayerHandle 后，可以在 shell 中构造指向实际窗口层的 SurfaceControl，并用 captureLayers 获取 HardwareBuffer。包装对象必须同时使用真实 layerId；仅替换 Binder、保留模板 layerId 会导致后续事务作用到错误层。
- 实际微信窗口层捕获约 2–5ms，属于单次 GPU 捕获调用耗时，不含 CPU 读回、网络或 PC 显示，也不是持续帧率验收。
- 微信窗口层的 OPAQUE 标记已实测清除：flags 8450→8448，并核对包装对象的 nativeId 与真实层号相同。六帧仍为全部 737,280 像素 alpha=255；每帧 RGBA hash 不同。标记随后恢复。
- 该窗口缓冲层 childrenOnly 捕获全透明；本轮合成器树没有可单独排除的相机 Surface 子层。
- 初始缓冲导入出现描述尺寸 568×1268、导入尺寸 4×1 的不一致，不能作为成功证据。现有探针加入尺寸一致性检查；4×4 自建 HardwareBuffer 对照可以正确往返。

这些结果不证明“所有原始 UI alpha 均不存在”，也不证明原生产者缓冲已经直接映射成功。它们证明的是：取得第三方图层句柄这一步已打通，但当前窗口捕获仍未提供独立透明 UI。

## 新发现：微信内部相机是独立 View

窗口监听需要在移动任务前注册。register 返回的是当前进程缓存；新进程的初始缓存可以为空。之前的空列表不能解读为权限拒绝。

只读 DUMP 得到：

- 一个全屏 android.view.TextureView，576×1280，原 alpha=1.0、opaque=true、背景为空。
- ScanUIRectView 为父容器。
- ScanNewStyleSharedMaskView、ScanProductNewMaskDecorView、ScanScrollTabView 等扫描装饰／按钮位于 TextureView 外。

因此，相机与 UI 在 View 树中可区分，但相机纹理已在应用渲染窗口时合入同一个 Surface。这与之前的合成器观察相符；不能将该结论外推为企业微信或其他扫描器也使用 TextureView。

下一项实验已实现为 suppress-headless：仅用 IWindow.executeCommand(INVOKE_METHOD) 暂时将该 TextureView 的 opaque 设为 false、alpha 设为 0，然后直接捕获 GPU 窗口层；finally 恢复原值。整个路径不调用 CAPTURE、不编码 PNG。

首次运行此模式时，扫一扫已经随返回主屏的配置变化退出，探针在寻找目标窗口时结束，尚未执行相机属性修改。不能将这一轮记为抑制失败或成功。后续已修正：若原任务来自有效虚拟屏，则恢复到原虚拟屏，避免无条件送回尺寸不同的主屏。

## 复现与隔离

构建：python build_wechat_probe.py。

结构检查：python run_wechat_probe.py inspect-headless 0。

相机绘制抑制：python run_wechat_probe.py suppress-headless 0。

执行前需要微信停在扫一扫。探针有 55 秒看门狗，临时显示与任务恢复放在 finally 中，设备临时 ZIP／server 文件由 Python runner 清理。

ADB 调研服务使用独立端口 5038，启动时以 --one-device <device-serial> 限制 USB 范围，随后连接平板的已授权无线地址。避免其他会话重启默认 5037 服务而打断实验，也避免连接手机。

使用的 server 为已发布 rc19 的独立副本，SHA-256 E4878FC2AC53BDAF69A11CFB4C3A38587F3F550BF5F088B22FD145D11F7D8830。不是正在构建的 rc20 server。

日志位于 tablet-wechat/。同模式重跑时旧日志保存为 .previous；阶段标签与统计用于辨别不同实验，不保存相机图像序列。

## 2026-10-29：电脑重启后，同屏实测完成

重新连接平板的无线 ADB，使用独立 5038 服务。独立 rc19 窗口 `WECHAT_TABLET_ALPHA_PROBE` 创建 display 149，微信任务 3229。通过正常微信菜单进入扫一扫，未尝试直接启动不导出的 BaseScanUI。整个实验未切换至企业微信，也未操作 K80 或修改 rc20 文件。

新增 `suppress-existing`：监听注册后只短暂改变任务 focusable 并恢复，以触发窗口信息刷新；不移动显示屏、不改变尺寸。此次顺利读取原页面并完成抑制及恢复。

### 对照结果

| 实验 | 已确认的修改 | 捕获结果 |
| --- | --- | --- |
| suppress-existing | 相机 TextureView opaque=false、alpha=0；真实窗口层 OPAQUE 位清除并核验 | 737,280 个像素仍全部 A=255 |
| suppress-transparent | 再清除 DecorView 实色背景和相机前的全宽 FrameLayout 黑底 | 仍全部 A=255 |
| suppress-format | 再调用 DecorView.setSurfaceFormat(TRANSLUCENT) | dumpsys 确认窗口 format=-3；仍全部 A=255 |
| suppress-freeform | 保持同屏与原边界，任务 windowingMode 从 1 改为 5，再执行上述透明处理 | 实测 mode=5；相机、父容器和背景均按方案处理；仍全部 A=255 |
| calibrate-alpha | 在真实窗口 SurfaceControl 上暂设整层 alpha=0.5 | 三次捕获的全部像素均满足 0<A<255；随后核验整层 alpha 恢复为 1 |

校准是整层人工半透明，**不等于取得扫描控件原始 alpha**。它证实了本机 shell→captureLayers→HardwareBuffer→统计这条路径能够保留 alpha，排除了“捕获函数或统计代码始终强制 A=255”的解释。

本轮稳定后的单次 GPU 捕获调用大多约 3–6ms；每个新进程首次调用约 28–40ms。它们不含 Bitmap CPU 读回、编码、ADB 传输或 PC 呈现，不能换算成已实现的端到端延迟或持续帧率。探针目前是诊断程序，并未接入实时客户端。

所有新实验均未调用 ViewDebug CAPTURE，也未生成或编码 PNG。对 HardwareBuffer 的 CPU 复制仅用于 alpha 直方图统计。

### 核验与无效证据

`suppress-root-calibration` 在上述处理后将 DecorView alpha 临时设为 0。二次只读 DUMP 确认：

- DecorView alpha=0、isOpaque=false、背景颜色=0。
- 相机 TextureView alpha=0、isOpaque=false。
- 父 FrameLayout isOpaque=false、背景颜色=0。
- WindowManager 中 format=-3。

但是捕获仍为含有非黑像素、hash 完全相同的旧帧。不能将它描述为“已捕获新的空画布且其 alpha 仍不透明”，也不能仅凭这组数据证明 HWUI 的清屏 alpha 行为。该轮只用于确认属性修改确实落到目标 View；根 View 隐藏后的新帧产生情况仍未解决。

最后执行 `inspect-existing`，确认任务仍在 display 149 的 BaseScanUI，mode=1、bounds=(0,0)-(576,1280)、format=-1，根 View alpha=1、背景=-328966，相机 alpha=1、opaque=true，父 FrameLayout 背景=-16777216。整层 alpha 校准也已恢复并核验为 1。当前测试窗口保留。

### 当前结论与待查范围

1. **免 root、仅 ADB 的无 PNG GPU 图层捕获已经可用。** 它可以读取真实第三方窗口，也能够保留合成器中已经存在的透明度。
2. **微信扫一扫独立 UI 的原始 alpha 尚未取得。** 相机与 UI 合入同一应用窗口；以上 View/背景/格式调整，没有让当前窗口捕获出现 UI 自身的非平坦 alpha。
3. 外部清除 OPAQUE 位、修改窗口格式和切换小窗，不能据此认定会同步改变已经创建的应用渲染器。AOSP ThreadedRenderer 构造时执行 `setOpaque(!translucent)`，ViewRootImpl 在创建渲染器时读取 format/insets；这是下一步检查方向，属于源码支持的假设，尚未在微信进程内直接读到或改变其 HardwareRenderer opaque 状态。
4. 这组结果不证明全部免 root 方法都不可能，也不证明其他应用的独立 SurfaceView 路径不可行。它只收窄了本机微信 TextureView 场景的可行范围。已经压平进不透明窗口缓冲的 UI alpha，不能因为更快地抓取同一缓冲就自动出现。

主要证据：`tablet-wechat/suppress-existing.log`、`suppress-transparent.log`、`suppress-format.log`、`suppress-freeform.log`、`calibrate-alpha.log`、`suppress-root-calibration.log`、`inspect-existing.log`。同名历史轮次保存在 `.previous` 文件中，不能混用其窗口状态。

源码依据保存在 `aosp16/DecorView.java`、`ThreadedRenderer.java`、`ViewRootImpl.java`、`WindowManager.java`。WindowManager.LayoutParams 的打印实现只在 format != OPAQUE 时输出 fmt，因此本轮首次 dumpsys 没有 fmt 并不意味着格式未知。WindowManager.java 来自 AOSP 官方 GitHub 镜像，来源与 SHA-256 记录在 source_manifest.json。

### 当前复现入口

先在独立 rc19 微信窗口进入扫一扫，读取当次 display ID；不要照抄 149，也不要再依赖创建/搬移 headless 显示来识别窗口。

```powershell
python build_wechat_probe.py
python run_wechat_probe.py inspect-existing <display_id>
python run_wechat_probe.py suppress-existing <display_id>
python run_wechat_probe.py suppress-transparent <display_id>
python run_wechat_probe.py suppress-format <display_id>
python run_wechat_probe.py calibrate-alpha <display_id>
```

模式均包含目标平板型号检查，并限制为指定虚拟屏的微信任务；每轮有超时和属性恢复。`suppress-freeform` 额外改变任务模式，属于可选对照。`suppress-root-calibration` 的根 View 隐藏结果存在上述旧帧局限，不作为原始 alpha 成功或失败的判据。
