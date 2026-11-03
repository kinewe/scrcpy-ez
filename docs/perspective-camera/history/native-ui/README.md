> 历史研究记录的脱敏副本。保留当时的实验条件、数值与阶段判断；后续结论以[总记录](../../../透视相机研发全记录_rc17至rc25_20261103.md)为准。历史命令不作为当前通用启动入口，原始日志、APK和图像未随本文件公开。

# 原生透明界面与实时传输实验

实机后续修订：[反选范围固定、模式切换自动刷新与蓝线尾部处理](INVERSE_LIVE_20261028.md)。用户已确认背景痕迹消失，inverse5 的蓝线“尾部更干净，主体也正常”；二维码与纸质名片切换的自动边界刷新已实测，不宣称泛化到所有自绘内容。2026-10-28 晚的 inverse6 改为**通用差分层**：学习彩色键路径自己的静态背景，只把它相对该背景的动态增量加回原生 PNG，去掉蓝色通道特调；本地 D3D11 回归通过（三程序、70 条断言）。经六轮实机迭代，最终修复“清洁掩码被首帧清零”的 bug（掩码=整屏校准相机预览区 ∩ 组件保护），用户确认动态区扫描带完整、背景无痕，中间截断与上方蓝紫残影均消失；采集时长上限已放开到 3600 秒。

后续专项验证：[VirtualDisplay → ImageReader alpha 实测](../virtual-display/README.md)。透明测试窗口可保留 alpha；真实企业微信扫一扫的 378 帧整屏输出全部不透明，同窗 View CAPTURE 仍保留 UI 的半透明度。因此不能直接以整屏 RGBA 替代本实验的独立 UI 来源。

2026-10-28，独立实验分支，未替换正式 RC.18

## 已验证

在 REDMI K80 / Android 16 的企业微信扫一扫中，通过现有 `IWindow.executeCommand` 的 `DUMP` 和 `CAPTURE` 读取 View 层的 PNG RGBA。相机 SurfaceView 的外部画面不在其中，按钮、半透明遮罩及蓝色扫描光带保留真实 Alpha。只使用 shell 已具备的 DUMP 权限，不修改应用、不安装模块、不改变系统安全设置

客户端由 `SCEZ_NATIVE_UI_FRAME_FILE` 显式启用实验入口，后台读取最新一帧，先预乘 Alpha 后缩放，主线程上传纹理。文件缺失、不透明、尺寸比例不符或超过 5 秒未更新时，退回正式单底色方案。已有 D3D11 单元验证覆盖预乘颜色读回、失效、删除、恢复和清理；实机验证过采集结束后回退

| 实测路径 | 结果 |
|---|---|
| 1440×3206 整页 RGBA，原 DPI | 上一轮 196 帧，抓取中位数 613ms，约 1.6fps |
| 本轮相同整页，仅局部动画 View | 整页约 534ms，动画 View 约 519ms，后者仍为整页大小 |
| 两个并行 CAPTURE | 每两帧约 1.01～1.07 秒，未获得明显提速 |
| 64 像素脏块打包为 PNG，逐字节还原 RGBA | 19 个差分帧总量仅减少约 6%，PC 计算中位数约 170ms，不接入运行路径 |
| 小米 `miCaptureLayers(3, package)` | 约 69ms，但返回 1080×2400 的全不透明合成图，拒绝作为原生 Alpha 来源 |
| 576×1280 / 240 DPI 整页 RGBA | 90 秒取得 765 帧，抓取中位数 116ms，P95 141ms，实际接收约 8.49fps |

小尺寸对照与主屏 1440×3200 / 600 DPI 均为约 384×853 dp。用户确认布局正常、动画明显更流畅，背景、蓝色光带、按钮和清晰度满意。此项只验证固定尺寸窗口，未集成 flex 自由缩放，不能据此声称所有应用布局均不变

## 推送和差分的边界

当前 Android 端没有固定 0.5 秒等待，完成一张立即通过常驻 socket 送出。瓶颈是应用调试入口逐帧生成整页 PNG；在 PNG 生成后做差分，仍然必须支付这项耗时。差分测试在 PC 解码后运行，只证明传输量与还原正确性，不代表手机端抓取加速，也不等同于 H.265 的运动补偿编码

Android 的 `ViewDebug.startRenderingCommandsCapture` 有逐帧回调，但需要在目标 View 所属进程和 UI 线程注册。现有外部 `IWindow` 调试命令未提供该注册入口。原始 UI 图层的 SurfaceControl 实时缓冲访问尚未解决，因此本实验仍是连续快照，不是 30/60fps 的合成器帧推送。UI 与相机同属应用 UID，按 UID 过滤不能分离

参考实现：[ViewDebug](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/android16-release/core/java/android/view/ViewDebug.java)、[IWindow](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/android16-release/core/java/android/view/IWindow.aidl)、[ViewRootImpl](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/android16-release/core/java/android/view/ViewRootImpl.java)

## 复现入口

此目录为工程探针，不是正式用户入口。仅匹配指定虚拟显示中企业微信的 `LoginScannerActivity`；目标缺失时结束，保持单底色。其他应用、旋转、弹出独立窗口和多会话尚未验证

1. `python build.py --sdk <android-sdk> --jdk <java-home> --output <独立构建目录>`
2. 将生成的 `probe.zip` 推送为设备 `/data/local/tmp/ez-rc18-layer-probe.zip`
3. 独立候选客户端用 `--new-display=576x1280/240`、不启用 flex 进行固定尺寸对照。设置 `SCEZ_NATIVE_UI_FRAME_FILE=<会话目录>/latest.png`，保留正式透视相机及其单底色输入
4. 进入扫一扫后，`python collect.py --adb <adb.exe> --serial <目标设备> --display <虚拟显示ID> --seconds 90 --output <会话目录>`
5. 只保留最新 PNG，内存和文件有上限。会话目录内创建 `stop.flag` 可结束；断开或结束后删除最新帧并移除该探针建立的 ADB 转发。使用后删除设备上的探针 jar 和临时目录

下一步优先验证按窗口像素量抓取时的 flex/DPI 联动、退出相机和重新进入的生命周期，再评估进一步提高抓取效率。原生 Alpha 不可用时继续使用单底色

## RC.19 后续实验（2026-10-28）

基线 `def16840`，实验分支 `codex/rc19-native-alpha-flex`。`SCEZ_NATIVE_UI_PIXEL_MATCH=1` 显式启用自动尺寸选择：PNG 目标为投屏传输尺寸和窗口实际物理像素尺寸中较小的一方，保持比例及偶数对齐。手机主屏尺寸和 DPI 只用于保留应用逻辑 dp，不作为 PNG 分辨率上限。正常入口未设置该变量时沿用原策略

启动和 flex 调整同时更新画布像素与 DPI，页面或相机因配置变化重建属于正常行为。辅助读取器 V2 持续监听目标窗口，绑定变化时重新查找 DecorView；等待期间清除过期 PNG，恢复后继续发布。运行期仍只保留最新一帧，不使用桌面/键色逐帧交替实验

新增尺寸策略测试后共 60 个 JUnit 用例通过，另外原有电源租约 32 条、心跳 15 条断言通过。实机自动匹配版本初始为 576×1280 / 240 DPI，用户确认文字清晰，放大缩小及改变长宽比后正常恢复。一次只读 CAPTURE_LAYERS 得到 20 层，总读取 835ms，其中相机取景特效覆盖整页，但多个文字、图标和按钮层具有独立的小边界，可用于后续静态区域标记

曾额外测试 0.75 采样比例，432×960 时抓取约 70–90ms，但用户反馈文字明显模糊，未选用。该档位仅作为历史测试记录，不作为新尺寸选择策略。后续混合实验应在实际显示所需的完整像素量上，低频更新静态原生 PNG，高频合成动态区

## 混合原型与反选候选（2026-10-28）

已在 RC.19 基线上完成第一轮混合实机实验，未修改正式运行包。固定绿底用于保留企业微信的蓝色扫描光带；颜色只在会话启动前确定，不进行运行期逐帧换色。原生 PNG 固定 2fps，真实桌面仍为 1fps，相机请求约 28～29fps。180 秒收到 359 张 576×1280 PNG，实际约 1.994fps，抓取耗时中位数 133ms、P95 155ms。传输约 23MB，但磁盘只保留最新一张，不保存图像序列

`CAPTURE_LAYERS` 一次读取约 700ms，20 个 View 中选出 10 个可见小组件边界，跳过整页层与 1 像素辅助层。第一轮按这些边界保留原生像素，其余区域仍依赖实时色键和原生基线参考。用户明确反馈：纯底色抠图的组件残影弥散到背景，超出 PNG 小块覆盖范围，并有明显色差。该轮未达到可替换整页原生 Alpha 的质量，不得标记为成功或部署为默认方案

用户提出改为反选：除扫描动态区域外，其余界面统一采用 PNG。新增 `SCEZ_NATIVE_UI_INVERSE=1` 候选实现这一方向：

- 以整页原生 RGBA 为默认输出，保证未变化背景、文字及装饰不再露出视频色键残留
- 扫描特效 View 本身覆盖整页，不能把它的整个矩形当作动态区。候选比较连续原生 PNG 的实际像素变化，学习一个会话内的运动掩码；小组件 View 边界强制保留原生像素
- 仅运动掩码内允许实时色键结果，并用原生透明基线稳定背景。掩码之外，即使视频像素与 PNG 有色差，也一律输出原生 PNG
- 摄像头或尺寸变化、数据缺失时丢弃运动学习；比例不符、元数据缺失或模型上传失败时优先回到整页原生 PNG，原生来源不可用再回到单底色

反选候选已完成 CPU 运动掩码、D3D11 真实读回及回退检查，**尚未实机验证**。只有原生 PNG 帧间实际观察到的变化才会进入运动掩码，低频采样可能漏掉运动；新出现且不在组件清单内的动态控件也可能误入掩码。它不是完整的 View 语义分离，也没有自动选出最佳底色。下一轮应先验证用户指出的背景色差和弥散残留，再看动画，不应仅凭合成器帧数宣称质量达标

### 混合候选复现

1. 按上述方法编译探针，推送到 `/data/local/tmp/ez-rc19-native-ui-probe.zip`
2. 独立客户端设置 `SCEZ_NATIVE_UI_PIXEL_MATCH=1`、`SCEZ_NATIVE_UI_FRAME_FILE=<session>/latest.png`、`SCEZ_NATIVE_UI_HYBRID=1`、`SCEZ_NATIVE_UI_INVERSE=1`、`SCEZ_NATIVE_UI_REGIONS_FILE=<session>/latest.views`、`SCEZ_PERSPECTIVE_KEY_COLOR=green`，启用原透视相机和 flex，竖屏起始
3. 企业微信进入扫一扫后运行 `collect_regions.py --adb <adb> --serial <设备> --display <虚拟显示ID> --output <session>`，仅生成边界元数据，不落盘组件 PNG
4. 运行 `collect.py --adb <adb> --serial <设备> --display <虚拟显示ID> --seconds 180 --fps 2 --output <session>`，源头限频而非抓满后丢帧
5. 当前 View 清单为一次读取，改变长宽比后需重新运行 `collect_regions.py`。未刷新且比例不符时自动退回整页 PNG，不宣称已实现通用布局跟踪

电脑窗口截图尝试返回空白，当前透视窗口启用了排除捕获；追加原生诊断抓取时扫一扫窗口已经不存在，没有取得有效新截图。空白截图只保留于本地诊断目录，不上传仓库，不作为效果证据。实验采集及临时转发均已结束，测试窗口已关闭。设备随后离线，约 11KB 的探针 ZIP 尚未删除，未安装任何探针应用；下次连接后可删除 `/data/local/tmp/ez-rc19-native-ui-probe.zip`

### inverse6 通用差分层（2026-10-28 晚）

基线 `5398f68f`。用户提出动态区扫描组件也要相对干净且不能按应用特调，并观察到原候选除扩张色差痕迹外扫描线相对干净。量化原生采样后确认：inverse5 的 `blue>=.15` 门限把扫描柔光的上半部（附加 alpha 2%–25%）整片压掉，扫描带被“切平”。

inverse6 改为与应用/颜色无关的差分层：预热期除原生基线外，再逐像素学习彩色键输出 V 自己的静态背景（取 alpha 最小样本、3×3 预平均、双缓冲 ping-pong）；冻结后实时输出 `out = native + active·max(V-model,0)`，`active` 由 5×5 空间支持的 smoothstep(0.0035,0.015) 与运动包络共同决定，受保护组件和相机区外强制原生。`active=1` 处与单键路径 V 逐字节一致（原候选观感、完整柔光），静态处逐字节等于原生 PNG，色差由差分模型抵消；不读颜色通道。只表达“加亮/加 alpha”的增量。

本地三个 D3D11 WARP 测试程序通过，`test_native_ui_hybrid` 重写为 inverse6 验收矩阵（70 条断言、0 失败、三次运行逐字节一致），覆盖键控偏差消除、主体=V、弱柔光保留、噪点抑制、模型冻结、包络/组件/相机隔离、重置与 OFF 回退。候选：`<historical-workspace>/rc19-native-alpha-flex-20261028/out/scrcpy-hybrid-inverse6.exe`（SHA-256 `E9E54B75…CACB660`），启动脚本 `start-hybrid-inverse6.ps1`。经实机确认（第六轮）：中间截断与上方残影消失；仍不宣称消除视频压缩噪声。
