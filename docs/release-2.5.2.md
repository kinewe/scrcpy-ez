# scrcpy-ez 2.5.2

**Windows x64 · 基于 scrcpy 5.0.1**

## 更新内容

- **滚轮兼容支持左右滑动**　开启「左右」后，纵向滚轮会在鼠标所在位置模拟横向手势，「反向」也适用。滑动幅度可在 5% 至 100% 间调整，按当前画面的宽度或高度计算，默认仍为 18%。主投屏按设备保存，应用投屏按设备和应用保存，两者各自的设置都适用于有线和无线连接
- **次级设置展开更顺畅**　应用窗口选项、通知选项及投屏功能的次级设置支持展开和收起动画。开启「自动平滑」时会展开缓冲上限和说明。设置窗口较矮时，会自动滚动到能看到新展开选项的位置
- **新增开机自启**　在「设置 → 窗口与显示」开启后，EZ 会在当前 Windows 用户登录时启动。默认关闭，每个 Windows 用户可独立设置

## 下载与升级

下载 `scrcpy-ez-2.5.2.zip` 和 `SHA256SUMS.txt`，校验后完整解压，运行 `scrcpy-ez.exe`。已有安装也可以使用应用内更新

常规 ZIP 随附 WebView2 加载器。缺少 WebView2 的电脑可安装 x64 Evergreen Runtime 后重新打开 EZ。[微软官方下载页](https://developer.microsoft.com/microsoft-edge/webview2/#download)

透视相机需要手机系统提供可用的虚拟相机服务。Windows 与手机端的完整运行依赖见 [Windows 运行依赖](https://github.com/kinewe/scrcpy-ez/blob/v2.5.2/packaging/WINDOWS-RUNTIME.md)
