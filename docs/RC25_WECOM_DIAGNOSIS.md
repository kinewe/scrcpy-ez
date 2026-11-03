# scrcpy-ez 2.4.2-rc.25：手机企业微信透视相机修复

日期：2026-11-01。基线：2.4.2-rc.23-UI1。源码：<historical-workspace>/ez-rc25。

## 已证实的原因

REDMI K80（Android 16），企业微信 5.0.11 / versionCode 81980，扫码页 LoginScannerActivity。UI1 使用透视相机，RGBA worker 也正常运行，但没有进入相机页面适配。

只读探针对 UI1 原始 select() 进行现场调用，得到：

- 主窗口：c5fa3ca com.tencent.wework/com.tencent.wework.login.controller.LoginScannerActivity，display=220，UID=10351，visible=true。
- 附属窗口：3594650 Panel:com.tencent.wework/com.tencent.wework.login.controller.LoginScannerActivity，同 display/UID，visible=true，inputConfig=4（NOT_FOCUSABLE）。
- 两个名字均被旧 activity() 用包名子串解析成同一 Activity，而它们的 IWindow token 不同。
- 原 select() 要求候选数量严格等于 1，所以返回 SELECT target=null。RGBA 适配尚未开始，既不是 Alpha 校验失败，也不是缺企业微信规则。

当前这些窗口编号、UID、显示编号只是现场证据，不写入生产规则。证据：test-results/rc25/ui1-selection-before.log。企业微信旧平板成功记录来自 5.0.3，与这次手机现场不同，不能据此宣称本次版本已经兼容。

## 初次修复后的失败记录（已由后续实验解决）

修复选择器后，从真正 rc25 GUI 点击手机企业微信启动，在 VD222 成功选中唯一主窗口并绑定 SurfaceView。现有 display-mirror、window-direct、system-scene 三阶段的结果均为 min=max=255、partial=0、cameraAlphaCoverage=0，最终记为 opaque_or_blank。未取得任何通过验证的透明帧。

失败后属性恢复到相机 View alpha=1.0。独立只读探针对精确主窗口发起一次 ViewDebug 根 UI 重绘，1440×3206，A=0 为 0 像素，0<A<255 为 4,574,404 像素，A=255 为 42,236 像素；中央区域 1,149,023 / 1,154,160（约 99.55%）有透明度。捕获命令耗时 575ms，不包括后续解码统计。RGB 图像未落盘。证据：test-results/rc25/wecom-ui-redraw.log。

这证明 UI 本身有原生 Alpha。该失败记录保留作为对照；后续实验已定位到下面的背景证据误判，而不是三种来源都先天不能取得透明度。

## 本次继续实验定位的根因

旧逻辑把 View.isOpaque() 返回 false 当作背景不存在。手机现场的 DecorView 实际有 LayerDrawable，但 drawing:opaque=false，旧 DUMP 没导出 bg_ 值，因此被误判为无须清理根背景。

SurfaceView 原来位于窗口下方，会在 UI 中打透明孔；改到上方并设置 alpha=0 后，这个透明孔不再产生。旧逻辑留下的根背景于是把 UI 重新画成全不透明。这解释了为什么原始 UI 重绘有 Alpha，而适配后的三个捕获源都只得到 A=255。

有界实验矩阵：

| 条件 | 系统捕获 Alpha | 中央区域有透明度 |
| --- | --- | --- |
| 原始相机与 UI 的系统合成 | 全部 255 | 0% |
| 相机保持下层、alpha=0 | 全部 255 | 0% |
| 相机改到上层、alpha=0，留下原根背景 | 全部 255 | 0% |
| 相机上层 alpha=0，并清理根背景 | 76–255 | 100% |

最后一项系统捕获有 4,570,901 个半透明像素、37,099 个不透明像素，和同条件 UI 重绘的 Alpha 直方图一致。系统单次捕获及复制约 102ms，UI 重绘命令约 580ms；这些是诊断单次测量，不能替代正常流性能。证据：test-results/rc25/isolation/root-trial.log。直方图一致不代表两个异步渲染帧逐像素完全相同。

正常链路随后还暴露一个时间预算问题：能力发现结束后恢复了已过期的绝对截止时间，导致后续属性设置立即超时。现在能力发现仍使用各自有界 IPC 超时，并保留调用方剩余的属性设置预算。

## 修复

1. NativeAlphaWindowIdentity 按实际组件窗口命名解析：允许“包名/Activity”和其前置十六进制窗口身份；Panel、PopupWindow、SurfaceView、Background、ActivityRecordInputSink 等标签不再冒充主 Activity 窗口。
2. 继续要求当前 UID、非主屏、可见性、topActivity 和真实 IWindow token 归属检查。真正多个独立候选仍保持歧义拒绝。
3. 增加 NATIVE_ALPHA_SELECTION 的 waiting_for_window / selected / ambiguous_windows 状态日志，仅状态变化输出。
4. GUI、Windows 资源、native launcher 和 client/server 协议统一为 rc.25。保留 UI1 三页签、未保存橙点、连接切换、更新白光、通知详情与占用提示修复。
5. 新增 NativeAlphaBackgroundEvidence，分开记录“明确没有背景”和“背景信息未知”。有 Drawable 类名时不能判为没有背景；缺失字段也不再被 drawing:opaque=false 覆盖。手机导出的空背景类名用来识别实际无背景的祖先 View。
6. 未知根背景沿用经过窗口、UID、任务和子 Activity 归属检查的 closeScanner 恢复路径。只结束本次精确绑定的扫码子 Activity，返回应用主界面；扫码结果的新 Activity 保留。没有使用强制停止应用来冒充恢复。
7. 修复能力发现消耗属性设置预算的问题，观测引擎版本更新为 18。

没有新增企业微信包名适配规则，没有放宽原生 Alpha 的逐像素与两帧验证。

## 验证状态

- Java 主窗口/Panel/Popup/SurfaceView/InputSink/其他应用回归：PASS。
- Java 实际 LayerDrawable 根背景、空背景祖先、冲突与缺失元数据回归：PASS。
- Android 设备端 NativeAlphaPolicy 自检：PASS。
- UI1 设置交互与更新白光回归：PASS。
- 真实手机同页面选择器对照：UI1 target=null；rc25 selected candidates=1，PASS。
- 从真实 rc25 GUI 启动与绑定相机页面：PASS。
- 原生透明流验收：PASS。真实 rc25 GUI 首次约 1802ms 完成验证，再进约 1389ms；1440×3206，GPU 读回原始 A8 精确，首轮连续运行超过 4 分钟、客户端超过 4100 帧。完整性能分布见 test-results/rc25/verification.json。
- 颜色范围：Display P3 转 sRGB 的 GPU 路径记录 maxRgbError=1，不能宣称 RGB 也逐字节完全一致。
- 实际扫码：PASS。桌面本地二维码经透视相机被企业微信识别为 RC25-PERSPECTIVE-20261101-ROUNDTRIP，结果页正常保留；证据图 test-results/rc25/isolation/QR_RESULT.png。
- 返回主界面、重新进入自动适配：PASS。
- 透明运行中从 GUI 关闭窗口：PASS，NATIVE_ALPHA_STOPPED restored=true，进程退出码 0，虚拟相机正常关闭。三次进入判定分别为 1802/1389/1479ms，会话累计 8007 个验证帧。
- 首轮实际采样 256.609 秒，帧率中位数 16.65fps，P10/P90 为 12.77/19.63fps；读回中位 29.45ms、编码 19.46ms、发送 0.37ms。这是当前设备、分辨率和局域网现场结果。
- 客户端/服务端协议与 GUI 版本均为 rc.25；构建 PASS。
- 对比 2345 个基线文件，UI1 原源码无改动；rc25 的 gui/web 全部保持基线内容。
- 本轮通过范围是这台 REDMI K80 / Android 16 / 企业微信 5.0.11 的扫一扫。没有把它扩大成任意应用、任意 ROM 或 60fps 的兼容承诺。

## RGBA 调研

详见 RGBA_RESEARCH.md（源码 docs/RC25_RGBA_RESEARCH.md）。本轮检索未找到“任意商业应用、免 root、不注入、原始 UI Alpha、高刷新率”同时成立的现成通道。独立 SurfaceView 图层与应用内 TextureView/共享纹理应分别处理。取到四字节 RGBA 不等于取到合成前透明度。
