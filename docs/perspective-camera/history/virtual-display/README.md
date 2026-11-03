> 历史研究记录的脱敏副本。保留当时的实验条件、数值与阶段判断；后续结论以[总记录](../../../透视相机研发全记录_rc17至rc25_20261103.md)为准。历史命令不作为当前通用启动入口，原始日志、APK和图像未随本文件公开。

# VirtualDisplay → ImageReader 原生 alpha 验证

2026-10-28。基线为 RC.19 实验分支 `codex/rc19-native-alpha-flex`，提交 `c5b30b913f5ddc5c81e31808879711a1ef20f052`。

## 结论

**采集路径可以保留已有的透明度；任务书提出的“只替换显示数据通路，就得到企业微信扫一扫的独立透明 UI”不能直接成立。**

K80 与小米平板的透明测试窗口，均在 `VirtualDisplayConfig → ImageReader(RGBA_8888)` 路径保留了预期的 alpha=0、64、128、255，排除了 SurfaceFlinger 一律强制 alpha=255 的假设。加入不透明窗口背景或独立 SurfaceView 后，同一路径的全部像素变为 alpha=255。

在用户操作进入的真实企业微信 `LoginScannerActivity` 中，378 帧 ImageReader 输出全部不透明。**同一窗口**通过 RC19 既有 `ViewDebug CAPTURE` 抓取整页 UI 时，4 帧各有 731,363 个半透明像素。两者采集对象不同：前者是相机与 UI 的整屏合成结果，后者是排除外部相机 Surface 的 View 快照。ImageReader 不会自动把相机从整屏合成中分离。

因此，暂不把该路径接入默认传输，也不以 LZ4 替代现有运行通路。下一步若继续此方向，首要前提是验证能够在合成前排除相机 Surface、单独取得 UI 缓冲；压缩与 PC 纹理上传应在此前提成立后再做。

## 实测

两台设备均为 Android 16 / SDK 36 / HyperOS 3.0.309。K80 型号 `24117RK2CC`，平板型号 `25091RP04C`。本次未覆盖 Android 14/15，不能外推为所有 ROM 通用能力。

分辨率 576×1280，240 DPI，有效像素数 737,280，像素步长 4，实际行步长 2,304。

| 场景 | K80 | 小米平板 |
|---|---|---|
| Canvas 直接写 ImageReader，已知 alpha 对照 | 12 帧通过 | 12 帧通过 |
| 透明 Activity，6 组显示 flag | 每组 12 帧通过 | 每组 12 帧通过 |
| 半透明色块叠加不透明品红背景 | 12 帧全部 alpha=255 | 12 帧全部 alpha=255 |
| 半透明色块叠加独立青色 SurfaceView | 12 帧全部 alpha=255 | 12 帧全部 alpha=255 |
| 真实企业微信扫一扫，RC19 普通应用窗口 flag | 378 帧全部 alpha=255 | 本轮未测真实应用 |

共 18 组色块对照、216 帧，各组均无帧超时且时间戳不同；另有 378 帧真实扫一扫证据。每帧检查直方图总数、有效像素数与四个已知色块中心，避免仅凭零值字节判断成功。

透明测试的中心采样均为预乘 RGBA：`(0,0,0,0)`、`(0,64,0,64)`、`(0,0,128,128)`、`(255,0,0,255)`。纯 ImageReader 对照的 alpha=0 / 中间值 / 255 数量分别为 184,320 / 368,640 / 184,320。Activity 有细小动画标记和系统边缘装饰，所以整帧分布会有小幅变化；中心采样在所有 12 帧都匹配预期。

色块测试的请求 flag：8（OWN_CONTENT_ONLY）、9（PUBLIC + OWN_CONTENT_ONLY）、11（再加 PRESENTATION）、25（OWN_CONTENT_ONLY + PUBLIC + AUTO_MIRROR）、17（PUBLIC + AUTO_MIRROR）、64715（RC19 普通 app-window 组合）。色块测试统一另加 256（DESTROY_CONTENT_ON_REMOVAL），仅用于清理测试应用；真实应用不加此位。OWN_CONTENT_ONLY 与 AUTO_MIRROR 同时请求时不能视为独立的镜像模式。

真实应用实验使用普通 DisplayManager 虚拟屏和物理相机，RC19 客户端只负责镜像显示和交互，采样来自原虚拟屏直接连接的 ImageReader。逐帧用该 display 的 `topResumedActivity` 确认扫一扫，而非依据屏幕上的少量透明边角或启动页。**本轮没有验证 RC19 VirtualDevice 虚拟相机显示的输出，也没有实现其相机层排除。**

同窗 View CAPTURE 对照：整页四帧的 `alphaMid=731363`、`alphaZero=0`，大小均为 576×1280；抓取耗时约 101–123ms。独立动画 View 四帧仍为整页尺寸，`alphaMid=737280`。这解释了为何原有 PNG 路径能得到透明 UI，而整屏 ImageReader 路径不能直接替代它。

## 任务书需要修正的判读

1. 按 `Image.getCropRect()`、`rowStride`、`pixelStride` 和 `buffer.limit()` 遍历有效像素。不能按 `buffer.capacity()` 连续每四字节统计，否则行尾填充可能制造 alpha=0 的假阳性。
2. `alphaMid > 0` 只证明该帧某处存在透明度。还必须验证目标 UI 区域，以及相机是否已被合成进输出；不能直接推导“EZ 可用”。
3. 全部 alpha=255 不能直接归因于 SurfaceFlinger 强制不透明。此次透明窗口成功、不透明底图和 SurfaceView 对照失败，说明生产者内容与合成关系是决定因素。
4. Android 36 SDK 中没有 `VirtualDisplayConfig.Builder#setBackgroundColor(int)`；两台设备运行期反射也未找到此 public 方法。测试应用通过透明窗口背景和 TRANSLUCENT 格式配置透明度。不能假定存在一个设置显示背景透明即可恢复原始 UI alpha 的通用开关。
5. 本次完成连续读帧与内容正确性验证，未做 30/60fps、LZ4、无线传输或 PC 端解码的性能验收。原始 RGBA 每帧约 2.95MB，30/60fps 分别约 88.5/176.9MB/s；LZ4 对最终不透明合成图的压缩无法恢复 UI 原始 alpha。

## 修改内容与复现

所有新增代码位于本目录，未修改正式 server/client、协议或默认入口：

- `AlphaStats.java` / `AlphaStatsTest.java`：有效区域统计及行填充、裁剪、截断末行、非零 buffer position 等回归检查。
- `AlphaActivity.java` / `AndroidManifest.xml`：无权限的临时已知 alpha 测试应用，包含透明、背景、SurfaceView 三种场景。
- `VirtualDisplayAlphaProbe.java`：使用 RC19 未改动的 shell context 初始化代码，创建配置式虚拟显示并采样。
- `build.py` / `run.py`：离线构建、安装、flag 矩阵、逐帧校验和自动清理。APK 只用于已知 alpha 对照；真实应用实验无需安装 APK。
- `RealAppAlphaProbe.java` / `real_session.py`：显式启动真实应用、开放镜像交互、绑定 Activity 的统计与任务恢复。
- `summarize.py` / `evidence/`：从原始日志复核结果，生成不含画面、账户内容或设备 IP 的证据摘要。

构建：

```powershell
python research/virtual-display-alpha-20261028/build.py --sdk <android-sdk> --jdk <java-home> --output <historical-workspace>/rc19-vd-alpha-20261028/build
```

色块矩阵（将 serial 替换为目标设备）：

```powershell
python research/virtual-display-alpha-20261028/run.py --adb <android-sdk>/platform-tools/adb.exe --serial SERIAL --server <historical-workspace>/rc19-native-alpha-flex-20261028/out/scrcpy-server --build <historical-workspace>/rc19-vd-alpha-20261028/build --output <historical-workspace>/rc19-vd-alpha-20261028/DEVICE
```

真实应用（进入窗口后由用户打开扫一扫，最长 180 秒；保留任务）：

```powershell
python research/virtual-display-alpha-20261028/real_session.py --adb <android-sdk>/platform-tools/adb.exe --serial SERIAL --probe <historical-workspace>/rc19-vd-alpha-20261028/build/probe.zip --server <historical-workspace>/rc19-native-alpha-flex-20261028/out/scrcpy-server --client <historical-workspace>/rc19-native-alpha-flex-20261028/out/scrcpy.exe --runtime <runtime-root>/scrcpy-ez-2.4.2-rc.19-win64/scrcpy-ez-2.4.2-rc.19 --output <historical-workspace>/rc19-vd-alpha-20261028/real-k80-explicit --seconds 150
```

本次正常完成后，两台测试 APK 均已卸载，测试显示及本次设备临时文件已删除。真实企业微信任务通过 RC19 `VirtualDisplayTaskRestorer.restoreToBackground` 返回主显示后台，主显示前台仍为桌面。没有清理应用数据。第一次真实启动因隐式 Intent 未解析成功而退出，改用 PackageManager 实际解析的显式 Activity 后重试成功；失败轮不计入有效样本。

完整本地日志与构建目录：`<historical-workspace>/rc19-vd-alpha-20261028`。仓库中仅保存统计摘要，不保存相机帧或桌面截图。

参考：[VirtualDisplayConfig.Builder API](https://developer.android.com/reference/android/hardware/display/VirtualDisplayConfig.Builder)、[SurfaceFlinger 合成机制](https://source.android.com/docs/core/graphics/surfaceflinger-windowmanager)。
