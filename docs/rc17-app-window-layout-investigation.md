# RC.17 应用窗口与主屏 UI 差异调查

日期：2026-10-24。版本：`5.0.1-ez2.4.2-rc.17-thaw5`。

## 已验证的触发条件

用户提供 Xiaomi Pad 8 Pro 浏览器三张对照图：EZ 应用窗口为底部导航样式，主投屏及 Escrcpy 应用窗口保留顶部多标签与地址栏。用户随后保持应用窗口长边 3200、DPI 自动，仅关闭“窗口跟随”（flex），确认恢复主屏样式。设备操作均由用户执行，本次只读取电脑上的源码、配置和日志。

`<runtime-root>/scrcpy-ez-2.4.2-rc.17-thaw5/scrcpy-ez-2.4.2-rc.17-thaw5/app-power-save-test.log`：

- 19:45:21：flex=true，自动计算的启动 DPI 为 440；19:45:26 创建 `3200x2136/440`。19:45:29 视频纹理从 `3200x2144` 变为 `1952x1296`。
- 19:45:33：用户保存 size=3200、dpi=0、flex=false；19:45:34 启动 DPI 仍为 440，19:45:40 创建 `3200x2136/440`，后续纹理为 `3200x2144`，没有前一会话的窗口适配缩小。
- `3200x2144` 是视频编码对齐尺寸，不能误写成 Android 逻辑屏幕的 `3200x2136`。

该对照确认 UI 差异由当前 flex 路径触发，不能仅归因于用户把 DPI 设为 480，也不能归因于横竖屏差异。尚未取得浏览器 Activity 的完整 Configuration 或内部布局选择分支，不声称已经证明其具体资源判断或缓存问题。

## 代码证据与限制

- `gui/internal/app/appwin_launch.go`：应用窗口默认 Flex=true；自动 DPI 按主屏物理 DPI × 虚拟屏长边 ÷ 主屏长边计算。
- `app/src/screen.c`：flex 开启时，启动窗口为适应电脑桌面而缩小时也立即向设备请求虚拟屏 resize；之后拖动窗口继续发送 resize。关闭 flex 则电脑端缩放画面，不随电脑窗口修改 Android 虚拟屏尺寸。
- `server/src/main/java/com/genymobile/scrcpy/video/NewDisplayCapture.java` 与 `FlexDisplayDensity.java`：自动模式 resize 同时按固定初始长边基准重算 DPI；手动指定 DPI 时保持固定。
- 若本次自动 resize 的目标确为 `1952x1296`，代码计算得到 268 DPI，逻辑尺寸约 `1165x774 dp`，与初始 `1164x777 dp` 接近。这是按代码计算的估值，未由设备日志回读确认，不能假称已采集实时 DPI。它也说明不能简单用“低于 600dp 所以变成手机布局”解释本次自动 DPI 场景。较早固定 480 DPI 时同一像素尺寸只有约 `651x432 dp`，属于另一种确实改变逻辑尺寸的配置。
- Android 官方说明：移动到其他显示屏或改变窗口会触发尺寸、密度和资源配置变化；应用须正确使用 Activity/显示屏上下文。保持近似 dp 尺寸不保证处理不完善的应用保持同一布局。

## Escrcpy 对照

只读检查用户正在运行的 Escrcpy 3.0.8 的本地 `app.asar`：启动设置的 flexDisplay 默认未设置，用户可按需启用；未指定新屏规格的普通路径允许保留默认显示规格。其屏幕解析器优先使用 `Override size/density`，再回退 Physical 值。

本次未取得 Escrcpy 当前会话的完整启动参数，因此不把默认配置当成该会话参数的证明。图中窗口转为竖向本身也不能证明启用了 flex；以原有逻辑屏幕旋转并缩放视频同样可以保持平板布局。

## 独立发现与后续方向

EZ 的 `devicePhys()` 仅匹配 Physical density，size 取输出中第一个尺寸，没有优先采用用户的 Override 值。若“自动 DPI”目标是跟随当前主屏布局，这里与实际有效主屏配置存在潜在偏差；本次没有取得该平板 Override 状态，不据此认定这是本轮根因。

当前可验证的使用方式是关闭该应用的“窗口跟随”，保留原屏尺寸和自动 DPI。后续若调整默认策略，应区分“保持主屏布局并缩放视频”和“改变虚拟屏尺寸让应用重新布局”；自由改变长宽比时，不能承诺所有应用仍保持完全相同的主屏 UI。若继续定位 flex 下浏览器的具体分支，应记录主屏、虚拟屏和目标 Activity 的有效 densityDpi、screenWidthDp、screenHeightDp、smallestScreenWidthDp、screenLayout 与配置变化时间，并保留原任务。

本轮仅记录结论，未改动运行代码、版本或已交付二进制。

参考：

- https://developer.android.com/develop/ui/views/layout/responsive-adaptive-design-with-views
- https://developer.android.com/develop/adaptive-apps/guides/support-connected-displays
