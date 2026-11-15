# EZ 2.5.0 Windows x64 发布与运行依赖

解压整个 ZIP 后运行 `scrcpy-ez.exe`。EZ 使用系统中已有的 WebView2 Runtime。如果系统缺少 WebView2，请从 [微软官网](https://developer.microsoft.com/microsoft-edge/webview2/) 或 [GitHub Release](https://github.com/kinewe/scrcpy-ez/releases/tag/v2.5.0) 下载微软签名的 x64 Evergreen 安装器，安装后重新打开 EZ。常规 ZIP 随附 `WebView2Loader.dll`，离线安装器单独提供

透视相机通过 Windows Graphics Capture 和 D3D11 采集桌面，再用 FFmpeg 调用 NVIDIA NVENC 或 Windows Media Foundation 编码 H.264。Windows 侧需要支持图形采集的 Windows 10 2004 及以上或 Windows 11、可用的显卡驱动，以及至少一种可用的 H.264 编码后端。如果是 Windows N 且系统没有 Media Foundation，在 Windows「可选功能」安装 Media Feature Pack 并重启。Windows 系统 DLL 和显卡驱动由系统及硬件厂商提供

手机侧需要 Android 16 或以上提供可用的虚拟相机服务，目标应用也需要接受虚拟相机。纯色抠图兼容模式共用桌面采集和 Android 虚拟相机；Zstd 仅用于原生 RGBA 的增量帧。Windows Zstd 静态链接，Android 的 arm64-v8a、armeabi-v7a、x86、x86_64 库随 `scrcpy-server` 下发，加载失败回退到 Deflate。使用者无需安装独立 Zstd 运行库

## 打包

1. 使用 `python tools/build-2.5.0.py native server gui` 同轮构建三端。`SCEZ_ZSTD_ANDROID_AAR` 指向经过 SHA-256 校验的 `zstd-jni 1.5.7-12` Android AAR。Windows 构建侧需要静态 zlib 和 Zstd
2. 用 `python tools/package-2.5.0.py BUILD RUNTIME REDIST DEST --ffmpeg-config FFMPEG_BUILD.sh --legacy-server scrcpy-server-legacy` 从空目录组包。`RUNTIME` 提供同版本 FFmpeg、SDL、ADB/USB 组件及许可。`REDIST` 提供 Microsoft Windows SDK Redist 的 `D3DCompiler_47.dll`（x64）及微软 `MicrosoftEdgeWebView2RuntimeInstallerX64.exe`。打包脚本要求两个文件均通过微软签名验证；仅采用官方下载或 SDK Redist 来源。`--legacy-server` 取自已发布的旧包，用于兼容旧版更新器的归档校验；所有设备投屏均使用本轮构建的 `scrcpy-server`。应用内更新识别的附件名为 `scrcpy-ez-2.5.0.zip`
3. 包内包含启动器、投屏客户端、Android 服务端、启动脚本、所有非系统 DLL、校准素材、许可和 FFmpeg 构建配置。公开发行 ZIP 排除离线安装器，离线安装器单独附在 GitHub，同一个常规 ZIP 和 SHA-256 文件用于 GitHub 与 Gitee。运行目录必须排除设备图标缓存、个人配置、日志和试用启动脚本。生成最终文件清单和 ZIP 校验值后再发布
4. 发布检查包括 PE 架构（独立 ADB 组件可成套使用 x86）、递归导入闭合、在净化 PATH 下运行 `adb version` 与 `scrcpy --version`、验证 Android 服务端 ZIP CRC 和四种 ABI 的 ELF 类型，以及重新读取 ZIP 校验全部哈希。发布前还要用解压包验证 GUI、主投屏、应用投屏、RGBA Zstd 和兼容色键。至少在一台无开发工具链、无 NVIDIA 或使用混合显卡的 Windows 机器开启透视相机，验证 Media Foundation 路径；用 Windows N 时记录 Media Feature Pack 状态

参考 [WebView2 分发](https://learn.microsoft.com/microsoft-edge/webview2/concepts/distribution)、[D3DCompiler 再分发](https://learn.microsoft.com/en-us/windows/desktop/directx-sdk--august-2009-) 和 [Windows N 媒体功能包](https://support.microsoft.com/en-us/windows/experience/platform-variants/media-feature-pack-for-windows-n)
