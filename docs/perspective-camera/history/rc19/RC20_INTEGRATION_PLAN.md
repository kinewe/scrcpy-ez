> 历史研究记录的脱敏副本。保留当时的实验条件、数值与阶段判断；后续结论以[总记录](../../../透视相机研发全记录_rc17至rc25_20261103.md)为准。历史命令不作为当前通用启动入口，原始日志、APK和图像未随本文件公开。

# RC.20 产品化：双通道默认化 + 全自动 + 启动提速

目标：把 inverse6 双通道（原生 PNG 背景/静态 UI + 实时键控动态区）做成 EZ 正式默认路径，版本 rc.19→rc.20；启动全自动（客户端自动拉起设备端探针，不再依赖 collect.py）；缩短“进入扫一扫→可用”的时间；校准遮罩持续到实时合成就绪。

## 一、目标时间预算（现状 → 目标）

| 阶段 | 现状 | 目标 |
|---|---|---|
| 自动对齐 + 本地 GPU 初始化 | ~1.5s | 不变 |
| 多底色照片校准 | ~2.0s | 不变（用户认可其节奏） |
| 原生采样预热 | 10.7s（固定 10s + ≥16 帧 @2fps） | **~3.5–4.5s**（绑定后前 4s 用 ~150ms 快速采样 + 自适应冻结） |
| 采集启动 | 人工 collect.py（本次 +6.2s） | **自动，~0.5–1s**（客户端拉起探针） |
| 合计 | ~20.5s（含人工） | **~7–8s**（从进入扫一扫算） |

## 二、总体设计

### A. 设备端探针并入 server jar（teammate: probe-server）
- 把 `research/native-ui-alpha-20261028/` 的 `NativeUiProbe/WindowProbe/ViewLayerProbe/ViewLayout` 复制进 `server/src/main/java/com/genymobile/scrcpy/`（包名 `com.genymobile.scrcpy`），以 server jar 为 CLASSPATH 启动：
  `adb shell CLASSPATH=/data/local/tmp/scrcpy-server.jar app_process / com.genymobile.scrcpy.NativeUiProbe <pkg> <socket> <seconds> <fastMs> <steadyMs> layout`
- 取消 display 参数：扫描 WindowInfosListener 的窗口列表，自动定位唯一可见的 `pkg/...LoginScannerActivity` 所在 display。
- 握手不再要求“先有扫描页”：连上后立即进入流；未绑定时发 idle 消息（size=0），绑定后发 layout 元数据 + 帧。
- 采样节奏：绑定后前 4s 用 fastMs=150ms，之后 steadyMs=500ms；总时长最长 3600s。
- 协议 magic 升级为 `EZ_NATIVE_UI_RGBA_V4`（与 C 客户端匹配；collect.py 同步支持 V4 以便测试）。
- 帧数据维持 PNG 字节（客户端不解码，直接落盘给 native_ui 解码）。

### B. 客户端自动化（Lead）
- 新增 `app/src/native_source.c/.h`：
  - 面向前视相机（`--perspective-camera`）默认启用，`SCEZ_NATIVE_UI_AUTO=0` 可关闭；
  - 建临时目录 `<TEMP>/ez-native-ui-<pid>`，`adb forward tcp:<port> localabstract:ez_native_ui_<pid>`，线程内 `sc_adb_execute` 拉起探针；
  - 接收线程按协议写 `latest.png`（原子替换）与 `latest.views`；退出时移除转发、关闭 socket（探针随之退出）、清理目录；
  - 提供 `sc_native_source_active()/frame_path()/regions_path()`。
- `native_ui` 从“环境变量配置”改为“显式路径 + hybrid/inverse 标志”，local_preview 在 `sc_native_source_active()` 时以 `hybrid=true,inverse=true` 初始化（默认启用双通道）；保留环境变量覆盖用于实验。
- 无探针/转发失败/设备不支持时自动回退单键路径，不影响其它功能。

### C. 启动提速（Lead）
- 探针快速采样（绑定后 4s @~150ms）；`native_ui` 自适应冻结：至少 8–10 个样本、≥3.5s、运动包围盒连续 ~1.2s 不再扩张即冻结；硬上限保留 10s 兜底。可用 `SCEZ_NATIVE_UI_WARMUP_MS` 覆盖以便对比。

### D. 校准遮罩延长（teammate: overlay-mask）
- `calibration_mask_state.h` 增加 `hold` 语义：`AWAITING_PREVIEW` 阶段在 hold 时不因 750ms grace 自动淡出，等 `preview_ready`。
- `screen.c`：`hold = local_preview_inverse_warming()`（Lead 提供 API）；其余不变。这样校准动画/遮罩一直持续到 inverse6 实时合成就绪，再 180ms 淡出，遮住预热期的 2fps 原生 PNG 跳变。

### E. 版本号 +1 与候选构建（teammate: release-version）
- 版本字符串 rc.19 → rc.20：`app/src/main.c`、`gui/main_windows.go`、`packaging/`（新增 rc20 脚本/说明）；候选构建脚本 `<historical-workspace>/rc19-native-alpha-flex-20261028/build-rc20.py`（由 build-inverse6.py 克隆，版本注入 rc.20），启动脚本 `start-rc20.ps1`（默认不再需要任何 SCEZ_NATIVE_UI_* 环境变量）。
- 完整发布打包等实机确认后再做。

### F. 验证
- 本地：`build-tests.py` 三程序回归；native_ui 自适应预热有待测试覆盖的用例由 Lead 补充。
- 实机：新脚本默认启动（无 collect.py、无实验环境变量）→ 用户进入扫一扫 → 测量自动启动延迟、预热时长、遮罩行为与观感；确认后打包 RC.20。

## 三、文件写作用域

| owner | 文件 |
|---|---|
| Lead | `app/src/native_source.c/.h`（新）、`app/src/native_ui.h`、`app/src/local_preview.c/.h`、`app/src/server.h/.c`（常量）、`app/src/scrcpy.c` |
| probe-server | `server/src/main/java/com/genymobile/scrcpy/{NativeUiProbe,WindowProbe,ViewLayerProbe,ViewLayout}.java`（新）、`<historical-workspace>/rc19-native-alpha-flex-20261028/probe-integration/` |
| overlay-mask | `app/src/screen.c`、`app/src/calibration_mask_state.h`、`app/src/calibration_mask.c` |
| release-version | `app/src/main.c`、`gui/main_windows.go`、`packaging/**`（rc20）、`<historical-workspace>/rc19-native-alpha-flex-20261028/build-rc20.py`、`start-rc20.ps1` |

## 四、风险
- 自适应预热冻结过早 → 扫描范围学习不全 → 边缘残影；用实机对比 + 环境变量兜底（改回 10s）。
- 探针随客户端自动拉起占用设备 CPU；空闲时窗口轮询 ~10Hz，绑定后 2–6fps PNG 抓取（原流程相同）。
- 默认开启后其它应用/无 shell 权限场景必须可靠回退单键路径。
- 版本号+1 涉及协议字符串（client/server 必须同版本构建）。

## 五、结果（用户已接受，RC.20 定稿）

- 版本提升：rc.19 → rc.20（client/server/GUI/PE/packaging 全部更新）。
- 默认路径：`--perspective-camera` 会话自动启用双通道；客户端自动拉起设备探针（专用 jar），V4 协议接收 PNG+EVS1；无 collect.py、无 `SCEZ_NATIVE_UI_*` 环境变量；失败回退单键。
- 启动时间：进入扫一扫 → 校准遮罩淡出 **约 5.0 秒**（此前人工流程 20.5 秒 / 实验版 12.4 秒），低分辨率布局 576×1280/240 使原生 PNG 抓取从 ~320ms 降到 ~100ms。
- 校准遮罩持续到实时合成就绪（20s 硬上限），素材随候选部署。
- 静态层保护组件改用 min-alpha 清洁；合成端低透明度色彩残差抑制。
- 已知限制：静态 UI 附近轻量固定位置周期性蓝紫虚影仍在（视频色度噪声类），用户接受为 RC.20 已知问题继续跟进。
