> 历史研究记录的脱敏副本。保留当时的实验条件、数值与阶段判断；后续结论以[总记录](../../../透视相机研发全记录_rc17至rc25_20261103.md)为准。历史命令不作为当前通用启动入口，原始日志、APK和图像未随本文件公开。

# Inverse6 设计：用“视频背景模型 + 差分层”通用地还原扫描组件

日期：2026-10-28。基线：`5398f68f`（inverse5）。仅实验分支，正式默认入口不启用。

## 1. 目标

用户要求：**动态区的扫描组件也要相对干净，且不是针对某个应用特调**。用户观察：原候选（inverse1，`c5b30b91`）除“随扫描形状扩张的色差痕迹”外，扫描线看起来相对干净，应从那里入手。

即：保留原候选里扫描组件的观感（明亮主体 + 完整柔和光晕），同时保留 inverse2 之后“背景不随扫描扩张、不留痕”的成果；把 inverse5 里针对企业微信蓝色扫描线的 `blue=max(b-max(r,g))` 特调改为与应用/颜色无关的通用规则。

## 2. 证据：inverse5 为什么把扫描组件做“脏”

`light-diagnostic/native-blue-best.png`（576×1280，ViewCapture 原生 RGBA，直通 alpha；背景为 30% 黑色遮罩 alpha≈76/255=0.298）量化结果：

| 位置 | 光晕附加 alpha | 预乘蓝 b | b−max(r,g) | inverse5 门限 |
|---|---|---|---|---|
| 行 528（光晕上缘） | 0.023 | 0.0215 | 0.0126 | 门限 .025/.15 → 基本被抹掉 |
| 行 576 | 0.097 | 0.0756 | 0.0637 | support≈.064 → active≈0.1，仍被抹掉 |
| 行 600 | 0.211 | 0.1525 | 0.1359 | 接近 .15，部分通过 |
| 行 624 | 0.384 | 0.2718 | 0.2448 | 主体，通过 |
| 行 648（最亮） | 0.513 | 0.3808 | 0.3311 | 主体，原样通过 |

inverse5 的规则：`blue>=.15` 原样；否则 5×5 加权支持后 `smoothstep(.025,.15,support)`。因此**光晕上半部（额外 alpha 2%–25%）整片被压掉**，只剩亮主体，边界停在 blue≈0.15 的等值线附近，观感是扫描带被“切掉/硬化”，这正是“动态区扫描组件不干净”的来源。原候选没有这道门限，整个光晕都由彩色键路径的 RGBA 呈现，因此相对干净。

关键结论：**彩色键路径的 V（手机上 UI over 绿底、把绿底去掉后的预乘 RGBA）本身已经包含完整的扫描组件；问题只是“动态区背景该用原生 PNG，扫描组件该用 V”，而 inverse1/2 的判据（视频与原生预测的色差、蓝色通道）都不够通用。**

## 3. 新方案：学习 V 的背景，输出“原生 PNG + V 的差分层”

在 10 秒预热期内同时学习两份模型：

1. 原生 PNG 基线 `n`（已有）：运动包络内按 alpha 最小取无光背景，包络外取最新 PNG。
2. **视频背景模型 `model`（新增）**：对每一帧彩色键输出 `V` 逐像素学习“没有动态组件时 V 的样子”，规则同基线：取 alpha 最小样本（先对 V 做 3×3/5 抽头空间平均以降低噪底），学习期间 V 仍每秒 60 帧可用。
3. 冻结后进入实时：`diff = max(V - model, 0)`（逐通道、预乘空间），`d = max(diff.rgb, diff.a)`，5×5 加权（[1,4,6,4,1]²/256）空间支持 `support`，`active = smoothstep(LO, HI, support) * insideMotionEnvelope`。
4. 合成：`out = n + active * diff`，随后做预乘合法性钳制并保证受保护区（组件 rect、相机区外）严格等于 `n`。

性质：

- `active=1` 时 `out = n + V - model ≈ V`：**扫描组件与彩色键路径逐字节一致**，即原候选里的观感；不再有 per-pixel 阈值抖动。
- 静态背景处 `V ≈ model` → `active≈0` → `out = n` 逐字节原生 PNG，**色差痕迹被差分模型抵消**，且不再依赖“蓝色/某颜色”判断。
- 光晕：以真实数据行 576 为例 `d≈0.097 ≫ HI` → active=1，完整保留柔和光晕；孤立噪点经 5×5 支持后低于 LO 被抑制。
- 与应用无关：不读颜色通道、不做颜色分类；对任意颜色/亮度的动态叠加层同样成立（只要是“加在稳定背景上的动态层”）。限制：只表达“加亮/加 alpha”的增量；动态地“变暗/移除”的内容仍由 2fps 原生 PNG 承载。

## 4. 阈值

`LO=0.0035`（≈0.9/255）、`HI=0.015`（≈3.8/255）。依据：背景键控/压缩噪底逐像素约 1–2/255，5×5 支持后约 0.4–0.8/255，低于 LO；可见光晕最弱处 d≈0.013–0.02 已经高于 HI 的下缘。测试与离线仿真验证后如需微调只改 `gpu_matte.c` 常量，不引入应用相关参数。

## 5. GPU 管线（`app/src/gpu_matte.c`）

主 shader 保持现有单 pass 行为不变（正式路径逐字节不变）；另编译一个宏变体 `INVERSE_VIDEO`：

- 宏变体在算出彩色键输出 `V=output` 后写第二 UAV `video:register(u1)`；相机区外写 `(0,0,0,0)`；最终 `result=output`（不动 z>0.5 的混合逻辑；inverse 走独立合成 pass）。
- 新增 `learn` pass：读 `video`(t0)+旧 `model`(t1) → 写 `model'(u0)`；首帧复制，之后取 alpha 最小样本（先 5 抽头平均）；`model` 双缓冲 ping-pong。
- 新增 `compose` pass：读 `video`(t0)、`model`(t1)、`nativeUi`(t2)、`nativeMotion`(t3) + 与主 shader 相同布局的 Params(b0) + 小常量 b1 → 写 `result`(u0)，按第 3 节公式；相机区外写 `n`。
- 新增纹理：`video`、`model[2]`（RGBA8，SRV+UAV），惰性创建（仅 inverse 实验启用时）；新增常量缓冲 b1。

API（`gpu_matte.h`）：

```c
void gpu_matte_set_inverse_state(struct gpu_matte *, int state); /* 0=off 1=learn 2=live */
void gpu_matte_reset_video_model(struct gpu_matte *);
bool gpu_matte_inverse_ready(struct gpu_matte *); /* 学习帧数达到阈值 */
```

状态机由 `local_preview.c` 驱动：每次取到新的原生帧后，读取 `native_ui` 发布的 `inverse_epoch/inverse_phase`；epoch 变化 → reset + learn；phase=learning → state 1；phase=frozen 且 ready → state 2；原生来源失效 → state 0。

`native_ui.h`：worker 在开始新学习会话时 `++epoch`，冻结成功时 `phase=1`；`pick()` 把 `epoch/phase` 发布到主线程（与现有 generation 一致，主线程读）。

## 6. 测试验收（`app/tests/test_native_ui_hybrid.c`，WARP）

1. 学习阶段注入 alpha 比原生低 3/255 的键控偏差 → 实时背景像素输出必须**逐字节等于原生 n**（旧 inverse1 会露色差）。
2. 亮主体像素：输出等于彩色键路径 V（±1），即原候选 RGBA，不做 alpha 抬升/模糊。
3. 弱光晕像素（附加 alpha≈0.05–0.10）：输出必须保留（`> n` 且接近 `n+V-model`），旧 inverse5 会抹掉。
4. 孤立单像素噪点 + 稀疏噪声：输出等于 n（5×5 支持抑制）。
5. 冻结包络外的运动：输出等于 n；组件 rect 内：输出等于 n；相机区外：输出等于 n。
6. 实时阶段光照到新位置后移开：立刻回到 n（模型不被实时帧污染）。
7. `reset` 后 ready=false，重新学习若干帧后 ready=true；状态 0 时走原单 pass 行为（旧测试保持通过）。
8. `test_gpu_key_color`、`test_native_ui` 回归不变。

## 7. 文件与命令

- 实现 owner（Lead）：`app/src/gpu_matte.c`、`gpu_matte.h`、`local_preview.c`、`native_ui.h`。
- 测试/构建 owner（test-builder）：`app/tests/test_native_ui_hybrid.c`；`<historical-workspace>/rc19-native-alpha-flex-20261028/build-inverse6.py`、`start-hybrid-inverse6.ps1`、`build-tests.py`。
- 仿真 owner（sim-analyst）：`<historical-workspace>/rc19-native-alpha-flex-20261028/inverse6-sim/`。
- 构建：`python build-inverse6.py`（native/server/gui 或 native-only）。
- 测试：`python build-tests.py`（用源码重新编译 gpu_matte.c，避免陈旧 obj）。

## 8. 风险与已知限制

- 只表达“加亮/加 alpha”的动态层；变暗/移除类动态内容仍只有 2fps 原生分辨率。
- 差分模型在固定 10 秒预热学习；之后若在包络内出现新的静态内容且没有结构指纹变化触发重学，会出现旧模型残留（与前几轮相同的已知限制）。
- 视频侧压缩噪底若明显高于 2/255，需要重新评估 LO/HI；不排除按噪底自适应。
- PNG 2fps；组件最终观感仍以实机用户确认为准，本次不宣称消除全部视频压缩误差。

## 9. 落地结果（2026-10-28 晚）

- 已实现并提交（`dd43b912`）：正常单 pass 路径逐字节未变；inverse 实验新增 `INVERSE_VIDEO` 主 shader 变体、`learn` 与 `compose` 两个 compute pass、三张惰性创建的 RGBA8 纹理（video + 双缓冲 model）、独立常量缓冲 b1。`local_preview.c` 按 `native_ui` 发布的 epoch/phase 驱动 OFF/LEARN/LIVE，`native_ui.h` 只新增发布字段。
- 设计常量保持 `LO=0.0035`、`HI=0.015`、5×5 支持（r=2），未按应用/颜色调整。
- 回归：`build-tests.py` 三个 D3D11 WARP 程序全部通过；重写的 `test_native_ui_hybrid` 70 条断言 0 失败、三次运行逐字节一致（`logs/inverse6-tests-report.txt`、`logs/test_native_ui_hybrid.log`）；独立 `inverse6-smoke/smoke.c` 通过同类检查。候选 exe `out/scrcpy-hybrid-inverse6.exe` SHA-256 `E9E54B75…CACB660`，构建自 `gpu_matte.c` SHA-256 `D7BFCC5B…D2A7`。
- 真实原生采样反事实量化（`inverse6-visual/`，不含压缩噪声）：扫描柔光 59,302 像素上 inverse5 平均启用权重 0.391、inverse6 1.000；弱柔光（b−max(r,g) 0.013–0.10，40,720 像素）inverse5 0.140、inverse6 1.000。即 inverse5 压掉约六成扫描柔光、约 86% 弱柔光，inverse6 完整保留。
- 状态：**本地验证完成，尚未实机由用户确认**。若实机发现动态层视频噪声仍明显，可调 `EZ_INVERSE_LO/HI`（`gpu_matte.c`）或支持半径（`kernel` 循环），改后必须重跑 `build-tests.py`；离线仿真只作辅助，不替代实机观感。
- 已知限制不变：只表达加亮/加 alpha 的增量；固定 10 秒预热后模型冻结；2fps 原生 PNG；视频压缩误差未被消除。
### 9.1 离线仿真复核（`inverse6-sim/REPORT.md`）

- 方向性结论一致：按光晕 alpha 0.02–0.25 统计，inverse5 光晕增益 0.246（丢 75%）、最外缘 0.5%–2% 增益 0.000（全丢）、光晕 MAE 10.53/255；inverse6 设计常量增益 1.000（外缘 0.881）、光晕 MAE 1.42/255，已到原候选显示整段 V 的噪声底（1.48/255）。
- 仿真噪声模型是刻意保守的（逐像素 sigma 1.5/2.0 ULP、静态 8×8 块偏 ±1.5、每帧 40 个 10–30 ULP 孤立尖点）；按更接近真实 15Mbps/平绿底路径的 ~1 ULP 级噪声估算，设计常量下背景实际逐字节干净。仿真建议**保持 LO=0.0035/HI=0.015/r=2**，不要为提高背景指标抬高 HI（会丢 41% 最外缘柔光）。
- 条件性改进（本次未实施，留作实机反馈后的备选）：若实机出现孤立闪点，在 5×5 支持前加 **3×3 min 预滤波 on d**（仿真把背景最大偏差 11→1 ULP、背景字节差异 37.8%→0.28%，光晕带不变，最外缘再降约一半）；不要优先调 HI。
- 实机需要留意的两项：①若编码 8×8 直流逐帧变化（非时间稳定），最小 alpha 模型无法抵消，背景可能出现约 30/255 偏差，需实机检查背景时间稳定性；②预热帧数越多，最小 alpha 偏差单调增大（15 帧约 −0.7/255，理论数百帧 −1.7/255），对光晕约 +4% alpha、主体 <1%。

### 9.2 实机迭代与最终修复（用户已确认）

第三轮把清洁范围从 10 秒运动包络改成校准相机预览区；但实机出现“扫描动画中间截断、下方继续”与上方蓝紫残影。第五轮加入诊断（记录相机预览区坐标、转储实际掩码 `mask-*.png` 与原生背景模型 `model-*.png`；设备探针与采集时长上限从 180 秒放开到 3600 秒，避免 3 分钟回退造成误判）后定位：`sc_native_ui_inverse_model` 在第一帧会清零整个运动掩码，而预览区掩码在其之前预填 → 实际掩码只剩运动检测的稀疏区域（覆盖率 36.1%），掩码外退回 2fps 原生旧光带，产生截断与残影。修复：把预览区掩码填充移到第一帧模型调用之后。验证：掩码覆盖率 93.2%（bbox 0..1279，其余为受保护组件）、单次预热无重学无回退，用户确认两个问题均消失。候选 exe SHA-256 `18636C384FEC09AC0B166E6D61FF839B2A3A5DD5FCD81438470C412CC77C8424`。
