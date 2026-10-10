scrcpy-ez 2.4.0 — Windows x64
基于 scrcpy 5.0.1；协议 5.0.1-ez2.4.0

解压到可写的独立目录，双击 scrcpy-ez.exe。
首次使用在手机开发者选项中开启 USB 调试并授权；支持 USB 或无线调试配对。
图形界面依赖 Microsoft WebView2 运行时，其他运行组件随包提供。

本版主要特性
- scrcpy 5.0.1、默认硬件解码、新视频缓冲；FFmpeg 9.0.2 / SDL 3.4.18
- 普通通知点击跳转到应用投屏详情，按设备设置；验证码仍只复制
- 应用后台页面复用、手机正在使用应用的确认保护、微信布局兼容
- 通知方向、高清图标、连续消息横幅及设置作用范围改善
- 运行时不再启动 PowerShell，直接调用 Windows COM/WMI 定位投屏进程

推荐打开“规格面板”主动选择更高的分辨率、帧率和码率。
可从 1920 / 60fps / 16～32Mbps 开始；性能充足可尝试 2560 / 90～120fps / 40～80Mbps。
有线、无线及各设备参数分别保存，升级会保留已有设置；实际帧率受设备、应用、网络和黑屏省电影响。

更新前退出投屏并备份，保留个人 profiles.json、settings.json、root 偏好与图标缓存。
请整包配套更新 GUI、投屏支持.bat、客户端、server 和 DLL；旧 server 不可混用，回退请恢复完整旧包。
发布包只含空设备档案；ZIP 用 SHA256SUMS.txt 校验，包内文件用 FILE_SHA256SUMS.txt 校验。
详见 RELEASE_NOTES.md 与 SOURCE_MANIFEST.json。

GitHub：https://github.com/kinewe/scrcpy-ez/releases/tag/v2.4.0
Gitee：https://gitee.com/kinewe/scrcpy-ez/releases/tag/v2.4.0
