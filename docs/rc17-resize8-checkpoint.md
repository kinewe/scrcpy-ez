# RC.17 resize8 保存点

日期：2026-10-24。保存基线为 layout6 提交 `6f1212f829251189494ad1151be10223566fb986`；本次保存其后的 preview7 和 resize8 改动。版本标签为 `ez-2.4.2-rc.17-resize8`。

## 用户反馈

用户体验 resize8 后反馈“似乎并没有本质改善”，并要求保留当前实现、同步到已授权的远程仓库。该反馈不能表述为已解决黑边或端到端延迟已明显降低。本轮没有新增设备日志，暂不作定量性能结论，也未继续修改运行逻辑。

## 保存内容

- 在 SDL 原生窗口拖动回调中消费新帧并重绘，包含主线程、窗口归属、重入及监听生命周期保护。
- 默认按比例适配尚未追上窗口尺寸的旧视频，并让预览与点击使用同一矩形；避免同一次新帧被重复提交。
- 普通 UI 保留 flex 路径首笔尺寸请求立即处理，后续按 100ms 合并最新请求，保留最终尺寸并串行执行；旧路径和透视模式保留 300ms。
- 继承 layout6 的主屏有效 DPI/逻辑画布及既有白名单、目标包解冻和心跳回收。

本地验证：32 项白名单断言、15 项共享心跳断言、56 个 JUnit 用例以及 SDL/CLI 回归通过；完整 server、93 个 native C 编译单元与 GUI 构建通过。新增调度边界 7 项完成复核。设备操作由用户进行。

## 交付物

GUI/ProductVersion 为 `v2.4.2-rc.17`，FileVersion 为 `2.4.2.1708`，客户端/server 协议为 `5.0.1-ez2.4.2-rc.17-resize8`。

| 文件 | SHA-256 |
| --- | --- |
| scrcpy.exe | `FA1D7013109398FF6ED9CD0244533C432B84AFAAFD816FE5513E386D6E48DF6F` |
| scrcpy-server | `FCD7789887847361DA2B514DE46187910F4BBBA79F4F30EDF9441C0A8270F563` |
| scrcpy-ez.exe | `3E75B4F1F55A1B435CB6682DF7B57FFF7E533FF9C84449D419454259307A0BD4` |
| scrcpy-ez-2.4.2-rc.17-resize8.zip | `55638CE6C89C62DCBDFAF74D76E499B53433D6CFF67555F2E3B0146A16D30C7A` |
| scrcpy-ez-2.4.2-rc.17-resize8-source.zip | `113661D8CD1DB8EC46D67418D90AE405E7C83865B66A68C95819BEA4CFE58553` |

本地目录为 `<runtime-root>/scrcpy-ez-2.4.2-rc.17-resize8/scrcpy-ez-2.4.2-rc.17-resize8`。原归档、运行二进制、preview7 和 layout6 均保留；本次只补充测试后的反馈和保存说明，不回写原归档。原归档中的“待用户测试”是交付时状态。

远程保存源码、测试、说明及产物哈希；本地配置、个人档案和设备日志不进入提交。细节见 [resize8 说明](rc17-resize8.md) 和 [验证记录](../packaging/TEST_REPORT.md)。
