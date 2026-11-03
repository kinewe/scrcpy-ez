# 接手记录：32eba6c5 后续

后续已开始真实设备复测，新增结果见 [时序判据与扫描残留对照](TEMPORAL-AND-TRAILS.md)。下文“尚未设备采样”保留为接手准备时点记录。

2026-11-02，当前接手环境为 <drive>/dsh_work；交接报告的 <drive>/Dev 路径在本机不存在。已 fetch 并 fast-forward 到用户指定的 `32eba6c56b0088db003a2f788e1c13346ed7b22c`，未回退到早期全不透明结论。

## 当前证据与下一项问题

交接中的自由窗口实测已取得32张同一HardwareBuffer的GPU/CPU逐字节一致透明阳性帧。这推翻“支付宝此场景绝对无法取得原生Alpha”的判断，但不等于已取得完整、稳定、高速的UI流。原始输出未随Git提交，本轮不能从报告重建完整逐帧时序。

读取产品源码发现：`NativeAlphaProbe` 的初次无效帧观察窗口约650ms；已支持后的无效帧至少连续3帧、持续350ms才进入恢复。还会尝试其他源。最终 `opaque_or_blank` / `opaque_camera_region` 失败通常由 `NativeAlphaPolicy.result` 写入6小时负缓存。**动画使这些分支误触发是待验证假设，不是已证实根因。** 单纯延长窗口也不能使不透明图像恢复Alpha。

此前研究将 `minAlpha<255` 作为审计阳性条件，产品另外要求空间Alpha分布及相机区域透明覆盖率至少0.6，因此阳性数量不能直接当作产品会接受的帧数。

## 本轮已做的工作

- 在精确窗口实验采样中新增 `captureStartedNs` 和 `readbackFinishedNs`，将捕获成本和跨样本间隔分开。
- `WindowAdaptationProbe` 将当前Plan交给 `CaptureSourceMatrix`。逐帧调用现有产品的 `spatialAlpha` 与 `cameraAlphaCoverage`，记录 `spatialAlpha`、`cameraAlphaCoverage`、`pixelPredicatesPass`。
- 后者仅回放像素条件。精确窗口与产品显示镜像的几何/裁剪可能不同；连续帧、漂移、源身份和完整UI仍需独立验证，不能据此宣称产品会话成功。
- 为探针构建增加SDK、JDK、产品class目录和D8 classpath参数，保留原机默认路径。Java和DEX编译通过。没有修改产品自动判断、缓存期限或正式运行文件。

本机验证命令：

```powershell
python research/alipay-texture-20261102/build-probe.py --sdk-root <android-sdk> --java-home <java-home> --product-classes <historical-workspace>/ez-rc25/build-rc25/out/rc25/classes --product-classpath <historical-workspace>/ez-rc25/build-rc25/out/rc25/classes.jar
```

本次编译使用本机已有产品class作为依赖，不等于从交接提交重新构建整套产品；不改变原报告发布lint未通过的记录。

## 设备核对与后续实验

K80已在ADB5037可用。5038仅看到老华为，不能据此要求用户重接K80。只读核对时K80没有透视相机服务、实验探针或虚拟显示；存在用户通知同步服务，本轮没有终止或修改它。

本轮未新开相机、改目标View或切窗口模式，未产生设备临时文件。新的探针尚未进行设备采样。

下一轮保持真实PC输入与单一适配控制器，对原全屏、自由窗口做同场景对照，记录带时间戳的像素条件和图层/窗口身份。在透明和不透明样本间做UI兄弟子树的时间邻近诊断，明确不是同一帧对照；PNG不计入流吞吐。先证明哪些失败是内容变化、哪些是采集或几何问题，再决定产品判断是否应改成“证据不足/可重试”。继续使用原报告的限定显示/任务清理，不触碰其他会话。
