# RC.17 layout6 保存点

保存日期：2026-10-24。用户要求将当前成果同步到远程仓库，供后续实时预览改进前回溯。

## 版本与内容

- GUI/ProductVersion：`v2.4.2-rc.17`；GUI/native FileVersion：`2.4.2.1706`。
- 客户端/server 协议：`5.0.1-ez2.4.2-rc.17-layout6`。
- 基于 RC.16 提交 `b221d2757b9e958abbaba6a98e78a7609d2cccce`，包含 RC.17 会话白名单、HyperOS 目标包解冻及 layout6 的自由窗口 UI 实验。
- 自动密度的普通 flex 窗口使用主屏有效 DPI，逻辑短边尽量保留主屏空间，视频输出单独跟随电脑窗口和编码能力；同一比例缩放窗口时尽量不改变 Android 逻辑画布。
- 没有改动主屏尺寸/密度、强停应用或修改现有微信兼容保护。极端比例受单边 8192、总像素 20 Mi 限制；不能保证所有应用、任意比例保持相同 UI。

## 验证范围

电脑本地：32 项白名单断言、15 项心跳断言及 49 个 JUnit 用例通过，完整 server、93 个 native C 编译单元和 GUI 构建通过，版本与功能标记核对通过。

设备：此前 thaw5 的 Pad 8 Pro / Android 16 浏览器黑屏直接启动及正常退出恢复已有用户反馈与日志证据；layout6 已收到用户保留本版的请求，本轮尚未收到新的设备日志，不将本地检查扩展为所有应用或性能已验证。

详细实现与测试步骤见 [layout6 说明](rc17-flex-ui-layout6.md)，既有用户日志结论见 [测试报告](../packaging/TEST_REPORT.md)。

## 本地交付物与 SHA-256

运行目录：`<runtime-root>/scrcpy-ez-2.4.2-rc.17-layout6/scrcpy-ez-2.4.2-rc.17-layout6`。

| 交付物 | SHA-256 |
| --- | --- |
| scrcpy.exe | `6292F6ED0AF54EDB08AD588275AFE6771B9292730D011E3FE3A913D1C8710F90` |
| scrcpy-server | `FCED57607FF7C1C5BA54BC747B70BBDC34C5D10AE8EB3EBB63B079BD5F981EFA` |
| scrcpy-ez.exe | `F220E7358DEABBF94A7C8066E32997F7CF80AD29C35AE6DB149CF81D92F3E8D3` |
| scrcpy-ez-2.4.2-rc.17-layout6.zip | `855B933B352960E4EEF47F99644863BD50AE9C880F8D4ACFB3011DB60E18DEE9` |
| scrcpy-ez-2.4.2-rc.17-layout6-source.zip | `DBD4CC8F837F0CEBB641F78EF37FF45EF0A8CFC3DDFEF0C6EF329065B44DAD89` |

源码包的 `engineering/` 保存构建脚本、验证输出与交付元数据；原 thaw5 包保留。上述归档为本次保存点之前生成，当前保存说明属于后补文档，不回写原归档。远程 Git 保存源码和本说明，运行包继续保留在本地。
