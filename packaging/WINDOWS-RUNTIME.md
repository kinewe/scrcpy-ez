# EZ 2.5.1 Windows x64 发布与运行依赖

解压整个 ZIP 后运行 `scrcpy-ez.exe`。EZ 使用系统中已有的 WebView2 Runtime，常规 ZIP 随附 `WebView2Loader.dll`。如果系统缺少 WebView2，请从微软官网下载 x64 Evergreen 安装器，安装后重新打开 EZ。[微软官方下载页](https://developer.microsoft.com/microsoft-edge/webview2/#download)

透视相机通过 Windows Graphics Capture 和 D3D11 采集桌面，再用 FFmpeg 调用 NVIDIA NVENC 或 Windows Media Foundation 编码 H.264。Windows 侧需要支持图形采集的 Windows 10 2004 及以上或 Windows 11、可用的显卡驱动，以及至少一种可用的 H.264 编码后端。如果是 Windows N 且系统没有 Media Foundation，在 Windows「可选功能」安装 Media Feature Pack 并重启。Windows 系统 DLL 和显卡驱动由系统及硬件厂商提供

手机侧需要 Android 16 或以上提供可用的虚拟相机服务，目标应用也需要接受虚拟相机。纯色抠图兼容模式共用桌面采集和 Android 虚拟相机；Zstd 仅用于原生 RGBA 的增量帧。Windows Zstd 静态链接，Android 的 arm64-v8a、armeabi-v7a、x86、x86_64 库随 `scrcpy-server` 下发，加载失败回退到 Deflate。使用者无需安装独立 Zstd 运行库

## 打包

1. 从同一源码版本构建 GUI、Windows 客户端和 Android 服务端。配套投屏协议为 `5.0.1-ez2.5.1`。校验 `zstd-jni 1.5.7-12` Android AAR 的 SHA-256，Windows 构建使用静态 zlib 和 Zstd
2. 在空目录中组包，使用匹配的 FFmpeg、SDL、ADB/USB 组件及许可，并验证 SDK Redist 中 x64 `D3DCompiler_47.dll` 的微软签名。保留已发布的 `scrcpy-server-legacy` 以兼容旧版更新器的归档校验。实际投屏使用本轮构建的 `scrcpy-server`。应用内更新识别 `scrcpy-ez-2.5.1.zip`
3. 包内包含启动器、投屏客户端、Android 服务端、启动脚本、所有非系统 DLL、校准素材、许可和 FFmpeg 构建配置。GitHub 与 Gitee 使用相同的常规 ZIP 和 SHA-256 文件，WebView2 离线安装器由微软官方下载页提供。公开包排除设备图标缓存、个人配置、日志和试用启动脚本。生成文件清单和 ZIP 校验值后再发布
4. 发布检查包括 PE 架构（独立 ADB 组件可成套使用 x86）、递归导入闭合、在净化 PATH 下运行 `adb version` 与 `scrcpy --version`、验证 Android 服务端 ZIP CRC 和四种 ABI 的 ELF 类型，以及重新读取 ZIP 校验全部哈希。发布前还要用解压包验证 GUI、主投屏、应用投屏、RGBA Zstd 和兼容色键。至少在一台无开发工具链、无 NVIDIA 或使用混合显卡的 Windows 机器开启透视相机，验证 Media Foundation 路径；用 Windows N 时记录 Media Feature Pack 状态

参考 [WebView2 分发](https://learn.microsoft.com/microsoft-edge/webview2/concepts/distribution)、[D3DCompiler 再分发](https://learn.microsoft.com/en-us/windows/desktop/directx-sdk--august-2009-) 和 [Windows N 媒体功能包](https://support.microsoft.com/en-us/windows/experience/platform-variants/media-feature-pack-for-windows-n)
