2026-10-24 更新：用户已确认 thaw5 在 Pad 8 Pro / Android 16 黑屏直接启动浏览器可正常操作，日志约 6 分 22 秒持续有效，正常退出后两套名单恢复。当前 layout6 保留该省电实现，新的 flex UI 测试见 APP_FLEX_UI.md（源码 docs/rc17-flex-ui-layout6.md）。以下为各阶段实现与历史测试说明。

# RC.17 应用投屏电池优化白名单

## 当前实验包 thaw5

millet4 实测：16:23:51.545 已添加并核验 MILLET 豁免，16:23:51.570 才复用任务。因此当前并非等亮屏后才设置。黑屏开窗时两个已记录进程最初为 frozen:1，数秒后虽恢复为 0，视频仍未更新；不能仅以主进程解冻判定应用恢复。第二段锁屏亮着开窗，随后完全息屏至 16:50，用户确认仍可操作，名单保持存在，退出恢复已核验。

thaw5 保持 GUI 版本 RC.17，FileVersion `2.4.2.1705`，协议 `5.0.1-ez2.4.2-rc.17-thaw5`。在有效的在线虚拟屏完成 MILLET 豁免回读后、启动/复用前，请求一次针对目标包的厂商解冻：向 owner 用户 0 的 `android` 包发送 `com.xiaomi.mipush.REFRESH_WHITE_LIST_ACTION`，携带目标 `pkgName` 和整数 `option=-1`。依据是另一 HyperOS 版本的 [开发者实测记录](https://github.com/dingwen07/hyperos-fcm-fix/blob/main/docs/xiaomi-hyperos4-nighttime-fcm-investigation.md)：该参数在其测试版本中会请求目标 UID 解冻而不改变 MiPush 名单。当前 Android 16 平板的兼容性和恢复效果尚未验证。

只在活动显示屏授权与成员回读成功后发送；白名单申请失败、显示屏未注册或已关闭时不发。普通启动、通知目标启动和超时恢复后的重新申请覆盖，后台名单巡检不反复发送。原有两层豁免、30 秒心跳回收和退出恢复保持。没有全局冻结开关、屏幕点亮/解锁、强停/重启应用或 GMS 重连动作。

日志 `action=thaw-requested ... dispatch_result=...` 仅说明广播分发调用返回，不证明系统接收器支持、解冻完成或画面恢复；`thaw-unavailable` 记录调用失败类型。仍以用户操作、持续出帧及进程状态共同确认。此前“已记录进程为 0 但画面不动”也可能涉及未覆盖的关联进程或渲染状态，本包不预先认定原因已全部解决。

用户测试：退出旧 EZ，从 thaw5 目录运行 `Start-LockScreen-Test.cmd`，这次保持平板完全息屏，直接打开同一个浏览器投屏窗口，点击/滚动约 30 秒。若无响应，最后再点亮锁屏界面作对照，然后关闭并提供同目录 `app-power-save-test.log`。不需要长时间重复已经有效的亮屏启动测试。仅完成电脑本地测试与构建，未连接或操作平板；旧版本与日志保留。以下为历史实验记录。

## millet4 包记录

awake3 日志首次确认了进程冻结：浏览器主进程和辅助进程在 15:41:45 起报告 `cgroup_freezer=frozen:1`。15:42:45 主屏亮起时恢复为 `0`，此时 `keyguard_locked=true`；15:42:55 主屏再次息屏，随后又变为 `1`。这与“锁屏界面亮着可操作，完全息屏后不可操作”的观察一致。虚拟屏组为 1、主屏组为 0，唤醒锁与标准白名单当时都存在。由此确认故障阶段存在内核进程组冻结；日志尚未直接标识发起冻结的厂商服务。[内核字段定义](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html)。

millet4 保留 GUI 版本 RC.17，FileVersion 为 `2.4.2.1704`，协议为 `5.0.1-ez2.4.2-rc.17-millet4`。撤掉未阻止本次冻结的虚拟屏唤醒锁实验，保留标准 Android 白名单，并为 Xiaomi/Redmi/POCO 尝试临时加入 owner 用户 0 的 `Settings.System.MILLET_NO_RESTRICT_APP`。它是 HyperOS 下游包级冻结豁免，与标准白名单不同，也不等于修改 PowerKeeper 私有数据库中的完整“无限制”档案。接口依据为开发者的 [HyperOS 实测记录](https://github.com/dingwen07/hyperos-fcm-fix/blob/main/docs/xiaomi-hyperos-gms-fcm-greezer-investigation.md)，仍需在本平板验证。

只处理已有且格式可识别的逗号分隔包名列表；设置缺失、格式未知或权限不足时记录失败，不覆盖未知值。添加和移除都读取当前列表，只改目标包，保留无关成员；原有豁免不接管。独立 `.scrcpy-ez-millet.state/.lock` 复用已有写前记录、跨进程锁、多所有者与独立清理进程；不同窗口共享同包时由最后一个所有者移除 EZ 新增项。标准名单和 MILLET 名单分别恢复，一方失败不阻止另一方尝试。

系统可能重建 MILLET 名单。在线会话通过设置变化观察器和每秒一次回读，按持久化所有权仅补回 EZ 添加的条目；已关闭、已超时或失效所有者不会重新申请。30 秒心跳超时释放两层引用，心跳恢复后重新注册/申请；退出停止监测并释放。失败记录保留供清理/后续会话重试。与系统写入没有跨服务原子比较交换接口，仍存在重写窗口；也无法区分投屏期间其他工具对同一目标包作出的添加。

**已经被冻结的进程可能不会因补名单立即解冻。** 本包不全局关闭冻结、不写 cgroup 控制文件，也不自动点亮或解锁。首次请让平板锁屏界面亮着再开启投屏，待初始化完成后让它完全息屏，继续操作；若开窗前已完全息屏，先点亮一次锁屏界面即可，不必解锁。PowerKeeper 重写造成再次冻结时也可能需要点亮一次，不能将“名单存在”当作已解冻。

当前日志：`SCRCPY_EZ_APP_MILLET action=added/preserved/shared/reapplied/retained/restored/recovered/state`，`verified=true` 仅表示该设置成员回读确认；`observer-unavailable` 时每秒回读仍在运行，失败原因仅记类型。继续保留输入、主屏亮灭、锁屏和进程/cgroup 状态诊断，不记录整份 MILLET 名单。

请退出旧 EZ，从 millet4 目录运行 `Start-LockScreen-Test.cmd`，按上述“锁屏亮着开窗→完全息屏”顺序测试。结束后提供同目录 `app-power-save-test.log`，重点对照 MILLET 添加、息屏后的 `cgroup_freezer` 和退出恢复。仅完成电脑本地代码验证与构建，实机效果仍待用户确认；旧版本及已有日志均保留。下方是历史实验记录，以本节为当前行为说明。

## 虚拟屏唤醒锁实验包 awake3

diag2 在 Pad 8 Pro / Android 16 上仍复现冻结。15:29:39 后帧日志停止，输入接收/完成数继续增长至 10766，拒绝数自启动初期起固定为 89；心跳持续到达、白名单存在，浏览器任务持续可见/有焦点/未休眠。UID 状态为 TOP（2），标准冻结查询为 UNFROZEN（2），这些状态不覆盖小米全部私有策略。用户补充以前点亮并解锁后会恢复。当前证据不支持“30 秒误释放白名单”或“任务没有焦点”，仍无法区分应用停滞和虚拟屏渲染停滞。

awake3 保留 RC.17 版本号（FileVersion `2.4.2.1703`，协议 `5.0.1-ez2.4.2-rc.17-awake3`），尝试给 EZ 创建的虚拟屏申请 display-scoped SCREEN_BRIGHT_WAKE_LOCK。先读取物理屏与目标虚拟屏的 displayGroupId，只有两者已知且不同才申请；接口不支持、同组或组信息不可读时明确记录 unavailable，不退回全局或主屏锁。现有封装不带 ACQUIRE_CAUSES_WAKEUP / ON_AFTER_RELEASE，不调用解锁、模拟触摸或 userActivity。Android 按锁所属显示屏的电源组处理：[PowerManagerService](https://raw.githubusercontent.com/aosp-mirror/platform_frameworks_base/android16-release/services/core/java/com/android/server/power/PowerManagerService.java)。这是待实测的修复尝试，不代表已经确认小米冻结原因或保证有效。

锁和白名单一起随原有 30 秒心跳超时释放、恢复心跳后重新申请，正常退出释放；释放失败可重试。server 进程死亡后由 Android Binder death 回收唤醒锁，无持久化主屏设置。`SCRCPY_EZ_VD_AWAKE action=acquired/released/unavailable` 记录调用结果；`state ... vd_wake_lock=true` 只表示本进程持有接口返回的锁令牌，不证明厂商一定执行。原有白名单独立清理机制保留。

保留 diag2 输入/任务诊断，新增主屏与虚拟屏的电源组，以及目标应用相关进程的标准冻结状态、Linux 进程状态和可读取的 cgroup 冻结状态；只读目标进程元数据，接口被拒绝时记录 unavailable。目标 UID 未冻结不等于相关进程或厂商机制都未冻结。

用户实测：退出旧 EZ，从 awake3 目录运行 `Start-LockScreen-Test.cmd`，保持平板已经锁屏再开浏览器。正常操作超过此前卡住所需时间；若再卡住，继续点击/滚动约 15 秒，然后可点亮并解锁，观察是否立即恢复，再关闭窗口并提供同目录 `app-power-save-test.log`。电脑端没有连接或操作平板。此前普通 RC.17、hb1 和 diag2 的目录/日志保留。

## 锁屏启动诊断包 diag2

用户在 hb1 中再次复现：平板已经锁屏时打开浏览器应用窗口，很快画面和点击失效。该次日志始终为 `exempt=true suspended=false vd_screen_on=true`，关闭前没有心跳超时或白名单移除记录；因此目前没有证据表明是 30 秒回收触发。此前成功会话为 `started`，失败会话为 `reused`，这是待验证的差异，不能据此确定原因。

diag2 保留 hb1 的白名单和共享心跳行为，只增加诊断，并将状态读取移出会话监视器，避免诊断 Binder 调用阻塞心跳和清理。GUI 版本仍为 RC.17，FileVersion 为 `2.4.2.1702`，协议为 `5.0.1-ez2.4.2-rc.17-diag2`。此前普通 RC.17 与 hb1 的目录和日志保留。

诊断版每 5 秒记录 `SCRCPY_EZ_CONTROL_RX`：收到的心跳、触摸/滚轮消息、输入调用完成/拒绝数及尚未返回数；`SCRCPY_EZ_CONTROL_TX` 是电脑已成功写入控制通道的对应消息数。Android 的异步输入返回成功只表示提交成功，不证明应用已经消费输入。计数不包含键盘文本、坐标、网页或应用内容。

`SCRCPY_EZ_APP_RUNTIME` 记录锁屏状态、目标 UID 进程状态/冻结状态，以及相关任务的显示屏、可见性、焦点和休眠字段。接口未提供或权限不足时明确记录 `unavailable`，不当作正常状态，也不等同于检测到 HyperOS 所有私有冻结机制。接口来源：[Android 16 IActivityManager](https://raw.githubusercontent.com/aosp-mirror/platform_frameworks_base/android16-release/core/java/android/app/IActivityManager.aidl)。读取运行状态只在独立诊断计时器执行；控制计数另用独立计时器，便于观察诊断调用本身是否卡住。这些详细诊断仅在 `-diag2` 构建启用。

实测请先退出旧 EZ，从 diag2 目录运行 `Start-LockScreen-Test.cmd`。先保持平板已经锁屏，再打开同一浏览器窗口；失效后继续点击、滚动十几秒，再关闭窗口。若方便，再以先开投屏、后按电源键锁屏的顺序对照一次。两次都写入同目录 `app-power-save-test.log`，交由代码端分析。此包用于定位，尚未证明修复本次冻结。

## 心跳补丁 hb1

此补丁继续显示 RC.17，Windows FileVersion 为 `2.4.2.1701`，配套协议为 `5.0.1-ez2.4.2-rc.17-hb1`。独立测试目录保留此前已由用户确认有效的 RC.17。请整包使用，避免旧客户端不发送应用会话心跳。

白名单现在复用主投屏防休眠使用的 `KEEP_AWAKE_HEARTBEAT` 消息、5 秒发送间隔、30 秒期限及同一个每秒检查线程。应用会话发送心跳不会开启主屏唤醒锁，不取决于物理屏幕是否点亮，也不依赖视频、音频或鼠标操作。正常情况下 30 秒未收到心跳后，在下一次检查时释放该会话的白名单引用。首个心跳从未到达也会超时，最小化/静止画面但客户端在线时不会误释放。

超时会原子记录该会话的应用和通知目标，关闭虚拟屏的白名单申请入口并尝试恢复；其他在线会话仍拥有同包引用时继续保留白名单。心跳恢复后重新注册并申请原目标；已正常退出的会话不会被迟到心跳重新开启。正常关闭、断连退出和独立设备端清理机制继续保留。恢复失败由检查线程重试，设备断电或所有清理进程同时失效仍只能在后续启动时回收记录，不承诺所有故障都能即时恢复。

日志新增 `action=heartbeat-timeout timeout_ms=30000`、`action=heartbeat-resumed` 和 `state ... suspended=true/false`。新补丁仅完成电脑本地测试与构建，设备断网/恢复实测待用户进行；此前 RC.17 的 Pad 8 Pro 浏览器约 10 分钟锁屏操作及退出恢复已有用户观察和日志确认。

本版本针对锁屏一段时间后应用画面停止、点击无效的情况，在应用启动或任务复用前，自动将目标包加入 Android deviceidle 完整电池优化白名单并回读核验。通过现有 shell 权限调用与 `adb shell dumpsys deviceidle whitelist +包名` 相同的系统接口，无需 root 或安装额外 APK。普通应用窗口和由 EZ 创建的通知详情虚拟屏均覆盖。主投屏、主屏防休眠及小米私有省电设置不改变。

白名单由 Android server 管理，设备端独立清理进程确认就绪后才允许申请。`/data/local/tmp/.scrcpy-ez-power-save.state` 只保存 EZ 新增项、虚拟屏和进程所有者，不保存应用内容。文件锁串行化多个 EZ 进程/窗口的操作，原子写入和写前记录使“已写入但回复丢失”也可恢复。所有者包含设备启动 ID、PID 和进程开始时间，避免重启或 PID 重用被当成原会话。

已有系统/用户白名单直接保留；多个窗口共享同一包时由最后一个所有者恢复。正常退出由主进程恢复并记录，断连或主进程被杀时由独立清理进程在设备本地恢复；恢复失败保留记录，清理进程及下一次开窗可重试。设备断电时无法即时清理，下一次 RC.17 开窗会尝试回收失效所有者。若没有启用 cleanup、独立清理进程未就绪、元数据不可读或系统拒绝白名单调用，则记录失败并继续原有投屏，不虚报成功。

本功能只处理 Android 标准白名单，不保证同步 HyperOS 的应用“无限制”选项，也不保证解决虚拟屏本身休眠。退出时恢复到 EZ 介入前的状态；在投屏期间手动改动同一项无法区分来源，建议先关投屏再改系统设置。

## 日志与手工验证

先退出旧 EZ，再从新包启动 `Start-LockScreen-Test.cmd`。该脚本仅开启日志并启动 GUI，不执行 ADB 操作。打开一个普通应用窗口，按平板物理电源键锁屏，等待超过此前卡住所需时间，测试滚动或输入，然后关闭应用窗口并退出 EZ。日志写入软件目录的 `app-power-save-test.log`；保留全部日志便于查看时间顺序。日志可能包含设备/应用名称，应按需要私下提供。

搜索 `SCRCPY_EZ_APP_POWER_SAVE`：

| action | 含义 |
| --- | --- |
| registered | 此虚拟屏已注册到独立清理进程对应的所有者。 |
| added | EZ 新增白名单，`verified=true` 表示回读存在。 |
| preserved | 启动前已有白名单，关闭窗口不会移除。 |
| shared / retained | 同包其他会话仍在使用，共享或继续保留。 |
| state | 普通应用启动后 15 秒开始、之后每 30 秒记录白名单、主屏与虚拟屏开屏状态；这是诊断快照，不发送输入、不保活。 |
| restored / recovered | 已恢复并回读确认不存在，或回收之前失效所有者。 |
| add-failed / restore-failed / cleanup-failed / register-failed / skipped | 调用失败、回读失败或条件不满足，`reason` 说明原因。 |

若已出现 `added ... verified=true` 但仍卡住，说明写入标准白名单不足以解决当前问题，需要结合 `state` 与锁屏/失效时间进一步定位；不能据此声称 HyperOS 私有策略已关闭。通知详情的申请来自独立通知进程，其申请细节同时写 Android scrcpy 日志；正常会话结束仍由窗口 server 记录恢复结果。

## 验证范围

仅运行电脑本地 JVM 回归、离线代码检查和构建。覆盖原有白名单保留、重复申请、多个进程共享、最后一个所有者退出、失效所有者回收、写入后回复丢失、权限失败、添加/移除回读失败、失败恢复重试、非法目标以及未知日志格式拒绝修改。没有连接平板，没有修改平板设置，没有进行物理锁屏测试。Xiaomi Pad 8 Pro / Android 16 的实际效果由用户验证后再判断。
