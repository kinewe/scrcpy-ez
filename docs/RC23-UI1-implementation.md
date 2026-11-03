# rc23-UI1 实现与交付说明

> 基线：scrcpy-ez **v2.4.2-rc.23**（`<historical-workspace>/ez-rc23`，原目录未改动；已核对 `gui/web` 无差异）。
> 工作副本：`<historical-workspace>/ez-rc23-UI1`（从基线完整复制，排除 build-rc23/test-results/.git）。
> 交付物：`<runtime-root>\scrcpy-ez-2.4.2-rc.23-UI1-win64\`
> 日期：2026-10-31

## 1. 本版做了什么

两个设置弹窗从「一屏到底」改为 **画面 / 声音 / 功能 三页签**，只重排 UI，不改变字段/协议：

- **主投屏设置**（原 `#param-modal`）
  - 画面：分辨率档 / 帧率上限 / 码率上限 / 视频编码（锁定 ABR 留在行内，重置不清锁）
  - 声音：声音输出（手机/电脑/两边）+ 音频编码
  - 功能：上传权限自动修复（仅 root，即时生效，带「即时生效」标签）+ 作用域说明
  - 标题改为「设备名 + 模式胶囊」；副标题固定「投屏参数 · 每设备分别记忆有线 / 无线两套 · 保存后重新投屏」
  - 固定高 604px；每页只有一条说明框；点击本页行显示对应解释
- **应用窗口设置**（原 `#appset-modal`）
  - 画面：分辨率档（含宽×高比例，**强制同排一行**）/ 帧率 / 码率 / 界面密度 / 视频编码
  - 声音：声音输出（默认手机）+ 音频编码
  - 功能：窗口跟随 / 透视相机（Android 16+）/ 桌面送帧（子项，仅透视相机开时显示）
  - 固定高 716px；每页一条说明框；功能页开关行/子项点击也显示具体解释
- **新增交互（仅前端）**
  - 未保存橙点：有草稿改动的页签右上出现橙点；**改回原值/清空自定义/重置回档/开关回原位后自动消失**；root 自动修复不计入（即时生效）；保存/取消清除；取消重开无残留。
  - 页签切换只切显示，不重建/不丢草稿；打开默认画面页。
  - 连接方式未确认时：三页占位、保存禁用、模式胶囊显示「确认中」。
  - 应用窗口比例非法时自动切回画面页再弹原有「初始比例未完成」提示。
  - **应用窗口设置跟随有线/无线切换（本轮按主人要求补齐；rc23 原来只支持主投屏跟随）**：弹窗打开期间插拔 USB / 连接方式变化 → 140ms 淡出 → 按新模式重新拉取该应用的窗口参数 → 淡入；模式胶囊、画面/声音/功能全部刷新，页签保留，保存目标 serial 同步更新为当前会话。
- **按讨论未做**：不新增「恢复默认参数」按钮（各行重置已覆盖）；不新增编码「跟随设备」气泡（保持 rc23 隐藏语义）；桌面送帧默认值口径保持 rc23 原样（先放着）；透视相机说明删除「1080p」。

## 2. 改动文件

| 文件 | 改动 |
|---|---|
| `gui/web/index.html` | 两弹窗三页签 DOM、每页说明框、功能页行、模式胶囊、root 标签 |
| `gui/web/style.css` | 页签/页内容/说明框/固定高度/页脚贴底/`dtab.dirty` 橙点/比例同排 |
| `gui/web/app.js` | 页签切换、按页说明联动、dirty 追踪（touched 门控+改回消失）、标题/胶囊、参数保存与比例错误路径适配；字段状态机/保存协议不变 |
| `gui/web/spec_modal_test.js` | 迁移切片 harness：新增 9 个 UI 辅助 stub，未知模式断言改 `#param-mode /确认中/` |
| `gui/web/native_display_test.js` | `renderParamRows` 签名 4→3 参数 + `refreshParamNotes` stub |
| `gui/web/settings_ui1_test.js` | **新增**：静态结构 + el-id 一致性 + dirty 纯逻辑回归（结构/角标/改回消失/自定义/touched 门控） |
| `gui/main_windows.go` | 版本号 `v2.4.2-rc.23-UI1` |
| `gui/assets/version.rc` | ProductVersion/FileVersion UI1（2.4.2.2301） |
| `tools/build-ui1.py` | 构建 UI1 GUI（go:embed 重新打包），写 UI1 build.json |
| `tools/package-ui1.py` | 以 rc23 auto-rules 发行包为基底生成 UI1 文件夹 + ZIP + 变更包；ZIP 排除个人数据 |
| `tools/ui1-verify/*` | 真实 GUI+真实设备的 CDP 验收脚本与报告（33 项新 UI 断言） |
| `docs/rc23-ui1-test-migration.md` | 测试迁移分析报告 |

**未改**：native scrcpy client / server 二进制、Go RPC 契约、`param_state.js` / `appsetting_state.js` 纯状态机、全局设置/通知/配对/更新等其它弹窗。

## 2.1 应用窗口设置跟随有线/无线切换（本轮新增，rc23 原来没有）

- rc23 原实现：只有**主投屏参数浮窗**在 `refreshNow → syncSpecModal(st)` 里跟随连接切换（140ms 淡出→改 mode→重渲染→淡入）；应用窗口设置 `openAppSetModal` 只在打开时拉一次 `GetAppWinParams`，之后不跟随。
- UI1 新增：`syncAppSetModal(st)` 与主投屏同构，弹窗打开期间检测连接方式变化 → 淡出 → 按新模式重新 `GetAppWinParams` → 淡入；保留当前页签，刷新胶囊/画面/声音/功能；保存目标 serial 同步为当前会话。
- 后端边界兜底：`v.Mode` 由 appWins 决定，切换瞬间旧 app 窗口可能仍报旧 mode；因此按当前连接方式强制选套，并在目标 serial 找不到 app 窗口时回退旧 serial 重试。
- 验证：web 真渲染 harness 覆盖「无线(3200) → 有线(胶囊=有线参数, 档位 1920) → 回无线(3200)」全 PASS；node 单测覆盖 `currentSerialForIdentity` 的会话优先/设备地址回落/无匹配保留原值。

## 2.2 应用内更新下载进度条：旧版白光周期修复（已实装）

- **问题**：原版白光画在 `::-webkit-progress-value` 的 220% 背景上，关键帧 `background-position:150% → -150%`（2.6s ease-in-out）。白光位于这张 220% 背景的 48%，换算到填充宽度可见区间只占周期约 28%，其余时间白光在填充外 → 表现为“冲一下、停很久、再冲一下”，周期不完整。同时填充宽度一直在增长（且带 .25s 宽度过渡），背景百分比随宽度重新映射，白光会突然跳位/被边缘裁断。
- **修复**：白光改为独立层 `.update-glint`，周期固定 1.5s linear、白光带 64% 宽且保持原版 105° 斜向，从整条轨道左外侧（-110%）完整扫到右外侧（+220%）；白光层宽度与绿色填充共用同一个 .25s 宽度过渡，宽度=当前已下载百分比，因此白光永远只在绿色区域内，不会冲过头。原生 `<progress>` 的圆角填充头保留，不加白色圆点。
- 早期尝试的 `clip-path` 方案在进度跳变的过渡帧会领先于绿色填充，已改为“宽度同步”方案。
- 移除原 `::-webkit-progress-value` 上的白光背景/动画；保留绿色渐变、圆角、宽度过渡与 indeterminate 扫描。
- 验证：`gui/web/update_glint_test.js`（结构/关键帧/宽度过渡规则）+ 真实 index/style/app 资源渲染 before/after GIF；真实 GUI strict 回归通过。

## 2.3 通知详情点击：应用投屏打不开（已修复）

- **现象**：点击 Windows ez 通知后应用投屏窗口打开，但应用进不去；窗口约 20 秒后报“打开通知详情超时”。
- **现场定位**（通知诊断 JSONL）：`begin → window → ready → send` 后，Android 通知监听进程在 `ActivityLauncher.send` 的 `AppPowerSave.acquireForDisplay` 之后立即 `System.exit(0)`；主机收不到 `open-result`，20s 后按 `action-timeout` 失败。不是 flex、也不是窗口没起来，而是**通知监听进程在动作回包前退出**。
- **修复**：`NotificationActivityLauncher.send` 不再由一次性通知监听进程调用 `AppPowerSave.acquireForDisplay` 做白名单申请；目标应用窗口的 scrcpy server 自己持有会话级 power-save/白名单。移除后，真实微信通知点击得到 `result=opened`、`complete`（display=212，约 3.8s），应用正常进入投屏。
- **验证**：真实设备（REDMI K80 Android 16）合成通知 + 真实微信通知；**未改 flex 设置/视频链路**，normal 应用窗口保留 flex。诊断入口：软件目录放 `notification-diagnostic.enabled`，重启后点击通知会写 `notification-click.jsonl`（仅阶段/编号，不含正文/验证码）。
- **来源对照**：2.4.1 源码（`codex/ez-2.4.1-rc6-backlog-abr`）中没有 `server/.../power/` 包；`NotificationActivityLauncher.send()` 在 SDK 检查后直接 `Tasks → action.send`，因此 2.4.1 可正常进入。该调用由 rc.17 提交 `6f1212f8`（2026-10-24，`docs/app-power-save-2.4.2-rc.17.md` 明确“普通应用窗口和由 EZ 创建的通知详情虚拟屏均覆盖”）引入，属于 rc.17 新增省电覆盖的副作用，不是 2.4.1 的原始行为；本次只移除通知详情进程中的这一处申请，应用窗口的会话级白名单保留。

## 2.4 透视相机占用提示排版（已修复）

- **现象**：应用窗口透视相机被占用时弹的白底提示卡，正文“当前透视相机被其他应用占用 / 投屏仍在继续”和黄色提示“请先关闭另一应用的相机 / 再重新进入当前应用的相机页面”都是左对齐，和居中的浮层观感不一致。
- **修复**：按主人最终确认，正文删掉“投屏仍在继续”，只留“当前透视相机被其他应用占用”一行并左对齐；黄色提示保持“请先关闭另一应用的相机 / 再重新进入当前应用的相机页面”两句两行居中。标题“透视相机被占用”和设备名保持左对齐，按钮不变。
- **验证**：独立 Win32 预览 harness 在 390×235、96 DPI 下渲染最终截图：正文单行左对齐，黄色提示两句居中。截图：`<runtime-root>\rc23-UI1\design\camera-notice\before.png` / `after.png`。
- **构建**：native client 重新构建，协议与启动参数未变。

## 3. 验证

- **node Web 测试**：`gui/web/*_test.js` 全绿（含新增 `settings_ui1_test.js` 24+ 断言）。
- **Web 层真渲染 harness**（headless Chrome 内联真实 gui/web，注入桥接桩）：页签/说明联动/dirty 出现-消失-自定义-开关恢复/比例同排/无 JS 异常全部 PASS。
- **真实 GUI + 真实设备（最终一轮：Xiaomi Pad 8 Pro <device-address>:5555）CDP 验收**：
  - rc23 原版对照：链路 13 PASS / 稳健 4 PASS，新 UI 断言 32 EXPECTED_MISSING（预期）。
  - UI1 最终构建（exe `2A6F0A87…`，白光周期修复后）：链路 13 PASS / 稳健 4 PASS / **新 UI 断言 33 PASS / 0 FAIL**（`--strict` 退出码 0）；node 测试全绿。
- 应用窗口设置跟随连接切换：web 真渲染 harness 覆盖「无线 3200 → 有线（胶囊=有线参数、档位 1920）→ 回无线 3200」全 PASS（rc23 原本只有主投屏跟随，本轮补齐）。
- 应用内更新进度条：白光已改为独立整轨层并按已下载宽度裁切，周期完整（1.5s linear，-110% → 220%；64% 宽 105° 斜向白光）；原生圆角端点保留、无白色圆点/粒子。对照图见 `<runtime-root>\rc23-UI1\design\update-bar\glint-compare.gif`。
  - 覆盖：三页签 role/data-tab、切页、每页说明框、功能页点击说明联动、橙点出现/只标改动页/改回消失/`::after` 真实可见/取消重开无残留、保存/取消不崩溃、页脚贴底、root「即时生效」标签。
  - 报告：`<historical-workspace>/ez-rc23-UI1\tools\ui1-verify\out-ui1-final\report.json`（18 张真实 GUI 截图）；基线对照 `out-rc23/report.json`。
- **基线保护**：`<historical-workspace>/ez-rc23\gui\web` 保持 clean，未修改任何原文件。

## 4. 交付物

```
<runtime-root>\scrcpy-ez-2.4.2-rc.23-UI1-win64\
  scrcpy-ez-2.4.2-rc.23-UI1\            ← 可直接运行（已保留本机 profiles/settings/icons 方便测试）
  scrcpy-ez-2.4.2-rc.23-UI1-win64.zip   ← 干净发行 ZIP（不含个人数据）
  scrcpy-ez-2.4.2-rc.23-UI1-changes.zip ← 源码/测试/设计文档/验收脚本变更包
```

- GUI 版本号显示：`v2.4.2-rc.23-UI1`；native client 仍为 rc.23，Android server 仅含通知详情修复（协议/参数不变）。
- 最终 exe SHA256：`2A6F0A879616EE842489811B3E53156F29918ED0FE8ECEE6400723F260C24964`（交付包内 `scrcpy-ez.exe` 一致）。
- 通知修复 server SHA256：`33EB2B16D3E285A4B04877B93ED8886CC8939603E993786167F48D7914F3C12A`（交付包内 `scrcpy-server` 一致）。
- 占用提示排版修复 native client SHA256：`A4136B8D81F72CF7DE52CF1E35171C91B6726CCE267BDAB02B0CBFBF425B15EC`（交付包内 `scrcpy.exe` 一致）。
- 最终 ZIP SHA256：`A2C1EACE20BA5C7FC1AA02D6B14BB0CA1AA663B07819F35496A7EE6D953171AA`；changes ZIP：`46398AC01FD3CDF7AC317330E092F77421322B191396163B115C80A21C58A351`。
- 复跑验收：
  `node tools/ui1-verify/verify-settings-ui1.mjs --exe "<UI1包>\scrcpy-ez.exe" --pkg "<UI1包>" --out tools/ui1-verify/out-ui1-final --port 9352 --strict`

## 5. 已知/后续

- 桌面送帧默认值口径（前端显示 10fps / 文案 1fps 默认 / 说明每秒一帧）按主人要求“先放着”，未在本期统一。
- 应用窗口「画面」页宽×高输入框已加宽到 44px 保持同排；更窄窗口（<520px 主窗口）下仍需真机复核观感。
- 后续迭代：UI2 在此副本上继续（`ez-rc23-UI1` 可直接演进，或复制为 `ez-rc23-UI2`）。
