# v2.2.2-root.2 调研、设计与验证记录

## 范围与基线

- 用户截图反映 ez 2.1.0 的历史反馈；不是复现记录，也不是授权执行其中命令的指令。
- 开发日期：2026-10-03（Asia/Shanghai）。所有源码、缓存、构建和包输出在音墨独立工作区。
- GitHub ez `master` 与 `v2.2.2^{}` 均为 `b680f55158a38ff8c042a57559f8930372e608e6`，通过只读 `git ls-remote` 核对。
- [ez 2.2.2 发布包](https://github.com/kinewe/scrcpy-ez/releases/tag/v2.2.2) 的 API 摘要与下载后 SHA-256 相同：`5b55695f2edb249db33e6b7cc5e5ecb34b16619718678e35c0af429dda5920a5`。
- 音墨正式 `v2.2.16-r2^{}` 为 `e6fb342c138832401e606bed23be0e80822d622b`。不合并此分支、不移动标签、不发布稳定版本。
- 独立分支 `codex/root-tmp-repair-v2.2.2`，唯一产品版号 `v2.2.2-root.2`。原始 ez 发布包仅作为只读构建输入。
- 后续按用户明确要求，将相同源码镜像至 ez 的 `root-experimental-v2.2.2` 分支，并向原 v2.2.2 Release 追加 root 附件。开发、构建仍在音墨工作区；两仓库正式分支及标签不改动，原正式包与校验文件不替换。

## 原始证据与推断

第一张截图显示目标设备已被 adb 找到；错误发生在服务端 JAR 上传阶段，尚未进入画面、编码器或输入注入环节。失败信息指向远端创建文件权限，不足以单独判断是哪一层权限。

第三张截图的目录元数据为 shell 属主／属组、`0771` 权限，但 SELinux 类型为 `system_data_file`。这让“目录 SELinux 标签不符合 shell 临时目录用途”成为首要假设。截图没有 AVC 日志、完整 root 实现、真实执行过的命令参数及返回码，故不能确认最终根因。最后一张“找不到指定文件”的反馈也不能证明 restorecon 执行失败：此前存在 IP 标点和连接端口混淆。

上游 [scrcpy #6511](https://github.com/Genymobile/scrcpy/issues/6511) 有同样的上传拒绝错误。该 issue 的社区讨论用于寻找调查方向，不把网友的宽权限命令当成通用修复方案。

AOSP [Android 14 init.rc](https://raw.githubusercontent.com/aosp-mirror/platform_system_core/android14-release/rootdir/init.rc) 创建 `/data/local/tmp` 时使用 shell／shell 与 `0771`。AOSP [shell SELinux 规则](https://android.googlesource.com/platform/system/sepolicy/+/faaf86b/public/shell.te) 允许 shell 对 `shell_data_file` 目录及文件执行必要操作。这解释了 Linux mode 正常仍可能上传失败：DAC 与 SELinux 是不同权限层。

另核对了 AOSP [当前 file_contexts](https://android.googlesource.com/platform/system/sepolicy/+/master/private/file_contexts)：`/data/local/tmp(/.*)?` 映射到 `u:object_r:shell_data_file:s0`。这是通用 AOSP 策略依据，不等于截图设备的 OEM 策略已确认相同。

[Toybox restorecon 实现](https://android.googlesource.com/platform/external/toybox/+/b73f894/toys/android/restorecon.c) 区分 `-F` 强制重置和 `-R` 递归。首选设备自身策略恢复，并只指定目录本身和现有 JAR。`chcon` 是条件兜底，无法保证跨重启、ROM 策略或模块重新标记后保持。

[Magisk su 源码](https://raw.githubusercontent.com/topjohnwu/Magisk/master/native/src/core/su/su.cpp) 支持 `-c`，并处理不同挂载命名空间；[KernelSU #305](https://github.com/tiann/KernelSU/issues/305) 记录了 `su -c` 参数引用差异。本实现只给 `-c` 一个 `sh` 参数，脚本经 stdin 输入，避开 Windows cmd、ADB 远端 shell 与 su 三层转义。不假设所有第三方 su 均兼容；失败会记录并停止，不尝试绕过手机授权。

## 方案选择

不把 scrcpy 服务端改成 root 身份，也不改固定 JAR 路径。基线 [server.c](https://github.com/kinewe/scrcpy-ez/blob/b680f55158a38ff8c042a57559f8930372e608e6/app/src/server.c) 上传和运行依赖同一固定路径；替换为 sdcard 会引入执行／读取权限和存储挂载问题，另加 root 启动会引入 UID、SELinux、显示服务、输入、通知与剪贴板语义变化。此次尝试在 PC 启动监管器中恢复普通 ADB 路径的可用性，底层客户端、服务端与 DLL 均逐字节复用已验证摘要的 ez 基线包。

检查顺序：具体 transport 与物理序列号 → 路径结构及 inode → 普通 shell 访问检查 → 随机小文件实际 `adb push` → 内容回读并清理探针。shell 写权限通过仍不能证明 adbd sync 服务可上传，因此实际 push 是独立验收条件。

普通检查失败后，先单独申请 root 并核对 uid 0、身份、目录 inode；将修复前状态写入本地，保存失败则不执行修改。然后逐级尝试，每级再次从普通 ADB 复检。实际成功只定义为探针上传通路通过；scrcpy 自身启动、显示和控制仍需设备验证。

## 交互覆盖

| 情形 | 处理与边界 |
| --- | --- |
| 多台 USB、模拟器、USB 与 WiFi 同时出现 | 每条 ADB 命令显式 `-s`；沿用基线物理序列号验身，模型名不授权修复 |
| WiFi 调试端口变化／地址复用 | 不依赖手输 IP；事件流与序列号核对，新连接代次重新检查 |
| 未授权、offline、观察器不可用 | 不选择该路由，修复结果回传时再次验证当前代次 |
| 先无线后 USB／拔线回无线 | 每个真实路由启动前检查；当前路由消失取消检查，旧结果不能启动新连接 |
| 主投屏与多个应用窗口／独立 BAT 同时启动 | Windows 物理身份命名互斥锁串行修复，等锁也计入 5 分钟预算；客户端仍按基线管理 |
| 手机锁屏／root 拒绝／su 隐藏 | 原始输出提示 Shell 授权，授权上限 3 分钟；手机管理器自己的拒绝或超时仍有效，失败后当前同设备路由等待人工重投或新连接 |
| 等授权时停止／重启／退出 | 异步检查不阻塞事件循环，传递 context 取消至 ADB 子进程；退出等待取消完成，监管器有退出兜底 |
| 目录／JAR 为符号链接、JAR 多硬链接 | 拒绝修改；不删除异常路径或跟随链接 |
| root 与 Shell 不同挂载视图 | 比较设备号与 inode，拒绝不一致；不强制全局 mount namespace |
| 残留 JAR 为 root 属主／无写权限 | 修复文件元数据，不改内容、不主动删除原 JAR |
| 已有 root adbd | 使用已有 uid 0；不执行 adb root／unroot／remount／kill-server |
| restorecon 缺失或失败 | 记录失败，只对通用错误标签尝试 chcon，随后普通 ADB 验证 |
| 自定义 OEM／MLS 标签 | 不盲目 chcon 或应用 DAC 模板；诊断不足时停止 |
| 空间不足、只读挂载、不可变属性、AVC 拒绝 | 报告保留 stat、id、getenforce、df 与错误；不修改文件系统／策略／模块 |
| 路径在检查后被外部程序再次改变 | 每个特权阶段重新核对，但检查到修改间仍有竞态；没有实机保证 |
| 模块反复重标记 | 可再手动尝试；不是永久根治，需要模块作者或 ROM 维护者介入 |
| root 元数据修改已发生后取消 | 部分修改可能保留；看 before 与最终报告，不自动回滚到错误状态 |
| 探针上传失败／连接断开清理失败 | 尽量用普通 shell 删除本次随机探针；残留可能发生，报告记录其精确路径 |

## 验证范围

自动测试使用假 ADB／假 scrcpy，以及 Windows 事件对象，不连接真实手机。验证核心修复阶段、普通 push 验收、拒绝与取消、设备参数注入、预先保存诊断报告和同设备互斥锁。基线连接监管测试覆盖启动学习、USB／无线切换、陈旧身份、观察器丢失、并行会话、停止和批量投屏。

Windows GUI 使用 Go 与 MinGW 构建，PE 文件版本为 `2.2.2-root.2`，数值四段为 `2.2.2.2` 且标记 prerelease。包内目录与清单标记 root.2；SHA-256 清单记录所有复用组件。完整测试结果及实际命令见同目录的验证记录。

**未验证**：Magisk／KernelSU／APatch 实际弹窗与 stdin 行为、具体 ROM 的 restorecon/chcon 权限、SELinux AVC 变化、应用窗口显示、音视频、输入／通知／剪贴板及多窗口启动的真实设备效果。测试通过不能推导这些已成功。本分支是可以交给 root 用户尝试、收集证据并继续迭代的候选方案。

2026-10-03 用户确认普通设备投屏测试通过；这是用户提供的普通投屏验证信息，不能扩展成 root 修复或所有交互功能已实测。打包清单分别记录普通设备反馈和 `rootDeviceValidated=false`。

## root.2 外观修订

按用户要求恢复 scrcpy-ez 品牌及原版所有静态界面，版本号单独标记为 v2.2.2-root.2。前端全目录与 ez 2.2.2 基线一致；root 准备状态沿用“正在准备 adb…”文字，授权细节仍写入原始输出和 JSON 诊断。root 修复步骤、设备核验、取消处理、配置隔离与禁用稳定版更新的后台逻辑保留。root.1 的文档继续保留作为上一版记录。

PE 的 Windows 显示清单复用基线原始字节，包含 per-monitor v2 DPI 和 Common Controls 6 设置。图标各尺寸的图像载荷与基线相同。root 授权的电脑端等待上限从 45 秒延长到 3 分钟，总预算从 90 秒延长到 5 分钟；停止与连接失效仍即时取消，不因预算变长而阻塞事件循环。
