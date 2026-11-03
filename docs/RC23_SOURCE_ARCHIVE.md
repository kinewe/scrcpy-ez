# RC23 源码归档 — 2026-10-31

- 基线：RC22 `6f63b8ec4438ce554c9aa446cebf540499b827fd`。
- 运行候选：RC23 Engine 16，协议 `5.0.1-ez2.4.2-rc.23`。
- 内容：原生 RGBA/A8 读取与无损差分、Windows 叠加、能力匹配及档案、窗口方向跟随、相关测试/构建/打包脚本、实验源码及交接说明。
- 本轮既有验证：66 项 Go 相关测试通过；服务端/GUI 构建及公开包 52 文件校验通过；Steam 虚拟屏图层读取已命中，失败后的重复重建在 Engine 16 实测停止。
- 已在历史样本中验证微信、企业微信、小红书等原生 Alpha 叠加；不代表任意应用兼容。Steam 现有实时路径仍未成功，支付宝/向日葵/V次元等的兼容性仍有未解决问题。
- 最新研究发现：原始 Steam UI 重绘有有效 Alpha；隐藏 SurfaceView 可能破坏透明孔洞。新的通用、快速原始 UI 导出通道尚未打通。研究源码在 research/native-alpha-buffer-20261031，完整证据说明见 docs/handoffs/调研_合成前UI原生Alpha_20261031.md。
- app/src/perspective.c、app/src/desktop_capture.c、server/src/main/java/com/genymobile/scrcpy/video/PerspectiveCamera.java 与基线保持一致。

这是可追溯的实验源码快照，不是兼容性完成或正式发布声明。个人 profiles/settings、设备原始采集、APK、运行包 ZIP、构建缓存不进入本次提交。原始实验数据保留在本机研究目录。

构建入口：`python tools/build-rc23.py server gui`；相关测试：`python tools/test-rc23-profiles.py`。脚本使用本机工具链路径，迁移环境应按实际安装位置调整。
