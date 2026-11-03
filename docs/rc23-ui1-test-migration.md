# rc23-UI1 GUI 测试迁移分析报告（「画面 / 声音 / 功能」三页签改版）

> **性质**：只读分析 + 复核报告。未修改任何代码/测试，唯一写入文件即本报告。
> **快照时间**：2026-10-31 22:12（Asia/Shanghai）。行号为该快照下的工作副本行号，重构后可能漂移；**测试依赖的是字符串锚点，不是行号**。
> **基线**：`<historical-workspace>/ez-rc23`（只读，rc.23；gui/web 无未提交改动）。
> **工作副本**：`<historical-workspace>/ez-rc23-UI1`。
> **结论前置**：27 个 gui/web JS 测试中，三页签改版真正会弄碎的只有 **2 个**（`spec_modal_test.js`、`native_display_test.js`），实现方已在工作副本完成最小迁移，当前 **27/27 全绿**；其余 25 个无需改动（其中 4 个有“锚点/切片区间”脆弱约束，见 §2）。Go 侧只有 `gui/internal/ui/html_test.go` 的资源组装冒烟，无需改动（已实跑通过）。**真正的缺口不是“旧测试碎”，而是“新 UI 行为（页签切换 / 每页说明联动 / 未保存 dirty 角标）几乎没有单测覆盖”——现有迁移用 stub 把新逻辑全部旁路掉了**（见 §3、§4）。

---

## 0. 验证基线（先看证据）

| 检查 | 命令 | 结果 |
|---|---|---|
| 基线 rc23 全量 web 测试 | `cd <historical-workspace>/ez-rc23\gui\web; node --test` | **27/27 pass**（改版前基线确认全绿） |
| UI1 最终快照全量 web 测试 | `cd <historical-workspace>/ez-rc23-UI1\gui\web; node --test` | **tests 27 / pass 27 / fail 0** |
| 改版过程中的真实碎点（实测） | `node spec_modal_test.js`（实现中途快照） | `ReferenceError: refreshParamNotes is not defined`，栈：`renderParamRows` ← `renderParamModal` ← `spec_modal_test.js:70` |
| Go 资源组装冒烟 | `<drive>/go\bin\go.exe test ./internal/ui/ -count=1`（CGO/msys64 环境） | **ok scrcpy-ez/gui/internal/ui** |
| 静态 DOM id 一致性（本报告新增检查） | app.js 中 `el('…')` 字面量 id ⊆ index.html `id="…"` | **171 个字面量 id 全部存在，0 缺失**（index.html 共 220 个 id） |

环境：Node **v22.23.1**（`node --test` 自动发现 `*_test.js` + `*.test.js`，新增测试文件无需注册）；Go **1.27.0** + mingw64 gcc（`go test ./...` 全量本次未跑，只跑了 `./internal/ui`；真实 WebView2 GUI 验收由 `gui-verifier` 的 CDP 脚本（task-2）覆盖，不在本报告范围）。

UI1 相对 rc23 的 gui 侧改动（测试相关 5 个 + 版本 2 个，**无新增 web JS 文件**）：

```
M gui/web/index.html             三页签 DOM
M gui/web/style.css              tabs/page/pill/dirty 样式
M gui/web/app.js                 页签/说明/脏标记 helper + 两个弹窗渲染重构
M gui/web/spec_modal_test.js     ← 已迁移（见 §2.1）
M gui/web/native_display_test.js ← 已迁移（见 §2.2）
M gui/main_windows.go            version = "v2.4.2-rc.23-UI1"（无测试断言该字符串）
M gui/assets/version.rc          文件版本 2.4.2.2301 / ProductVersion rc.23-UI1
```

---

## 1. 实现改动面（只列与测试/契约相关的部分）

### 1.1 `gui/web/index.html`（行号为快照）

- `#param-modal` 69–155：头部改为 `#param-title` + 模式胶囊 `#param-mode`（73–76），副标题 `#param-sub`（79），三页签 `#param-tabs`（80–84，按钮 `#param-tab-display/audio/feature`，`data-tab`/`role=tab`/`aria-selected`/`aria-controls`），内容区 `.param-body`（85）：
  - `#param-page-display`（86–98）：**`#param-rows`（87）**、`#param-vcodec-row`（89–95）、**`#param-note-display`（97）**；
  - `#param-page-audio`（100–130）：`#param-audio-row`（含 `#param-audio-slider`、`#param-audio-note`）、`#param-acodec-row`、**`#param-note-audio`（129）**；
  - `#param-page-feature`（132–146）：`#param-root-repair`（140，仍在 label 内）、**`#param-note-feature`（145）**；
  - `#param-pending`（148）：「连接方式确认后…」占位。
  - 页脚 `#param-cancel`/`#param-save` 位置未变（150–153）。**旧 id `#param-note`、`#param-mode` 的旧文案节点已删/改。**
- `#appset-modal` 380–472：`#appset-card`（382）、`#appset-title` + 模式胶囊 `#appset-mode`（385–386）、`#appset-tabs`（391–395）、
  - `#appset-page-display`（397–408）：`#appset-rows`、`#appset-vcodec-row`、`#appset-note-display`（407）；
  - `#appset-page-audio`（410–440）：`#appset-audio-row`（`#appset-audio` 滑条、`#appset-audio-note`）、`#appset-acodec-row`、`#appset-note-audio`（439）；
  - `#appset-page-feature`（442–465）：`#appset-flex-row`（444）、`#appset-perspective-row`（452）、`#appset-perspective-fps-row`（459）、`#appset-note-feature`（464）；
  - 页脚 `#appset-cancel/#appset-save` 位置未变。**旧 id `#appset-note` 已删。**

### 1.2 `gui/web/style.css`

- **旧断言规则原样保留**（spec_modal_test.js:130 依赖，**不要动**）：
  - `#param-card { transition: opacity 140ms ease; }`（887）
  - `#param-card.param-mode-out { opacity: 0; }`（888）
  - `#param-card.param-mode-busy { pointer-events: none; }`（889）
  - `@media (prefers-reduced-motion: reduce) { #param-card { transition: none; } }`（890）
- 新增 UI1 样式块从 1168 起：`.dialog-tabs`（1189）、`.dtab`（1190）、`.dtab.active`（1196）、**`.dtab.dirty::after`（1199，橙点，`var(--orange)` 已存在）**、`.dpage`（1205，`display:none` / `.active{display:block}`）、`.param-title-wrap`/`.param-mode-pill`/`.fgroup-title`/`.param-inline-tag`/`.param-pending`、`#param-note-*`/`#appset-note-*` 间距（1232 起）；两个弹窗固定高 604/716 + `max-height: calc(100vh - 32px)` + `.param-body` 滚动兜底。

### 1.3 `gui/web/app.js`（测试相关函数/锚点）

**新增共用 helper（1715–1781）**：`var DIALOG_TABS`（1717）、`dialogTabEl`（1718）、`dialogPageEl`（1719）、`showDialogTab`（1720）、`showAllDialogPages`（1737）、`currentDialogTab`（1746）、`bindDialogTabs`（1753，点击 + ←/→ 键盘）、`setTabDirty`（1771）、`clearDialogDirty`（1775）、`setNote`（1778）、`PARAM_FEATURE_NOTE_DEFAULT`（1780）、`APPSET_FEATURE_NOTE_DEFAULT`（1781）。

**appset 侧**：`appsetEffectiveField`（1812）、`snapshotAppSetState`（1820）、`updateAppsetDirtyTabs`（1834，display/audio/feature 三页角标；以 `touched` 门控，root 不参与）、`resetAppsetPageNote`（1850）、`bindDialogTabs('appset', …)`（1855）；`openAppSetModal` 打开时写 `initial`/`touched`、`showDialogTab('appset','display')`、`clearDialogDirty('appset')`（1913–1916）；`renderAppSetModal` 写 `#appset-title` + `#appset-mode` 胶囊并 `updateAppsetDirtyTabs()`（1940–1951）；`#appset-note-display` 由 `renderAppSetRows` 尾部刷新（2154）；功能页三行点击 → `#appset-note-feature`（2168–2176）；`closeAppSetDialog` 关窗清角标（2178–2181）；保存 payload/调用顺序**未变**（2273–2281，仅新增 `clearDialogDirty('appset')` 2282）。

**param 侧**：
- `makeParamState`（4515–4546）**未变**，仍不初始化 `initial/touched`（由 `openSpecModal` 补）。
- 新增 param dirty/说明块（4558–4612）：`paramEffectiveFieldFor`（4559）、`snapshotParamMode`（4566）、`updateParamDirtyTabs`（4579，display/audio 页角标；**feature 恒 false——root 自动修复不算草稿**）、`resetParamPageNote`（4595）、`refreshParamNotes`（4605）、`bindDialogTabs('param', resetParamPageNote)`（4606）、`bindParamFeatureNote`（4607，顶层 `document.querySelector('#param-page-feature .param-assist')`）。
- `updateParamCodecRows`（4627）在回调内标 `touched.display/audio`（4636/4643）。
- **`renderParamRows` 签名变了**：`(state, box, noteEl, redraw)` → **`(state, box, redraw)`**（4652）；行点击/输入/锁定/重置回调内标 `touched.*`；**渲染尾部新增 `refreshParamNotes()`（4765）——这是本轮唯一在“渲染路径”中新增的跨区依赖，也是 spec_modal_test 实测碎掉的直接原因**。
- `clearParamTransition`（4807）未变；`closeSpecModal`（4814–4822）新增 `clearDialogDirty('param')`（4817）。
- `syncSpecModal`（4824–4871）**过渡/两段 140ms/`param-mode-out`/`param-mode-busy`/`inert`/保存禁用逻辑全部未变**；仅“模式未变”分支改为写 `#param-title = devName` + `#param-mode = 有线/无线模式`（4843–4844），不再拼「· 自定义参数」。
- `openSpecModal`（4874–4906）：在 `paramState.identity` 之后、**root repair 代码块之前**新增 `initial`/`touched`/`showDialogTab('param','display')`/`clearDialogDirty('param')`（4885–4888）；root 块锚点原样保留（`var rootState = paramState, rootBox` 4889、`renderParamModal();` 4900）。
- `renderParamModal`（4922–4938）：标题/胶囊拆分；`#param-tabs` 显隐（4928）；`#param-pending` 显隐（4929）；`#param-save.disabled = !ready || !!paramTransition`（4930）；未确认走 `showAllDialogPages('param', false)`（4932）；就绪后 `renderParamRows(paramState, el('param-rows'), renderParamModal)`（4935）→ `showDialogTab('param', currentDialogTab('param'))`（4936）→ `updateParamDirtyTabs()`（4937）。
- 顶层 param 绑定 4940–4988（滑条/声音/编码/重置），保存 handler 4989–5016：**保存协议与参数顺序未变**（`syncSpecModal → settle → closeSpecModal → toast → SaveProfileAndRestart(serial, mode, res, fps, bit, custom, audio, lockFps, lockBitrate, vcodec, acodec)`；spec test 断言 `saves[0][1]='wifi'`、`[2]=2048`、`[6]='both'`）。
- 区间锚点仍在：`// ---------- 应用内更新：`（4248）、`// ---------- 参数状态机（参数浮窗专用；纯函数 SCEZParamState 单测覆盖）`（4352）、`// ---------- rc23-UI1：主投屏三页签脏标记 / 每页说明框`（4558）、`// ---------- 指引弹窗…`（5018）、`var settingsState = …`（5029）、`var lastProfileSaveError = ''`（5456）。
- `param_state.js` / `appsetting_state.js` **未被修改**（纯状态机语义未动）。

---

## 2. 受影响测试逐项分析

### 2.0 受影响矩阵

| 测试 | 依赖方式 | 影响 | 当前状态 | 是否需改 |
|---|---|---|---|---|
| `spec_modal_test.js` | app.js **函数名切片 + 顶层保存块切片 + DOM mock + style.css 正则** | **高，实测碎**（ReferenceError） | 已迁移（9 个 stub + 1 处断言） | 已改；建议再补断言（§4.3） |
| `native_display_test.js` | app.js **函数名切片**，直接调用 `renderParamRows` | **中**（签名 4→3 参数；尾部新依赖 `refreshParamNotes`） | 已迁移（stub + 改调用） | 已改 |
| `root_repair_ui.test.js` | app.js **源码字符串区间切片**（4 个锚点） | 中（锚点脆弱；当前因新代码插在锚点**之前**而安全） | **未改，通过** | 不需要改；**必须保持锚点/顺序** |
| `settings_compat_test.js` | app.js 区域切片（`var settingsState =` → `var lastProfileSaveError =`） | 低（UI1 未触及该区域；锚点约束） | 未改，通过 | 不改；勿在该区间插顶层 DOM 调用 |
| `notification_settings_test.js` | 同上两锚点 + `function closeAppWinModal(` | 低 | 未改，通过 | 不改 |
| `update_flow.test.js` | 区域 marker：`// ---------- 应用内更新：` → `// ---------- 参数状态机` | 低（UI1 新代码在该区间之外） | 未改，通过 | 不改；保持 marker 文本/相对位置 |
| 其余 21 个 JS 测试 | 各自模块/函数切片，不触两个弹窗 DOM | 无 | 通过 | 不改（§2.7） |
| Go `gui/internal/ui/html_test.go` | 只做 index.html/style.css/JS 组装冒烟 | 无（无新增 JS 资源文件） | 通过（已实跑） | 不改（§2.8） |

### 2.1 `spec_modal_test.js` — 主受害测试（P0）

**它如何工作**（132 行）：
1. 第 32–37 行按 `  function <name>(` 从 app.js **逐函数切源码**执行，白名单 17 个：
   `nativeTiers, frameRateTiers, ladderTiers, baseValOf, curParam, curField, applyField, baselineOf, initField, makeParamState, renderParamRows, specContext, clearParamTransition, closeSpecModal, syncSpecModal, openSpecModal, renderParamModal`。
2. 第 38–39 行再切一段**顶层代码**：从 `  el('param-save').addEventListener('click'` 到 `\n  // ---------- 指引弹窗`，用 `vm.runInContext` 执行（当前 app.js:4989 → 5018）。
3. 用极简 `Node` mock（`el(id)` 任意 id 即建节点）+ `vm` context 模拟 app.js 全局；`setTimeout` 被替换成可手动推进的定时器表。
4. 断言覆盖：启动模式推断、两套草稿、模式切换 140ms 淡出/淡入两段、`param-mode-out/param-mode-busy/inert`、保存禁用与保存参数、late event/取消、未确认模式、reduced-motion 0ms、`style.css` 中 `#param-card` 140ms 正则。

**它会怎样碎（逐条，含实测）**：
- **（已实测）切片函数内新增依赖未注入 vm context**：`renderParamRows` 尾部新增 `refreshParamNotes()`（app.js:4765）→ `ReferenceError`。同理新增的渲染/打开/关闭路径依赖：
  - `renderParamModal` → `showAllDialogPages`、`showDialogTab`、`currentDialogTab`、`updateParamDirtyTabs`；
  - `openSpecModal` → `snapshotParamMode`、`showDialogTab`、`clearDialogDirty`；
  - `closeSpecModal` → `clearDialogDirty`。
- **旧断言文本失效**：`#param-title` 现在只写设备名（未知模式为 `投屏参数`，app.js:4925），不再含「正在确认」；旧断言 `assert.match(h.el('param-title').textContent,/正在确认/)` 必碎。模式文案移到 `#param-mode`（`确认中`/`有线模式`/`无线模式`，4926）。
- **（潜在）DOM mock 方法缺口**：若把上述 stub 换成真实 helper，mock `Node` 只实现了 `style/children/events/parts/classes/classList{add,remove,contains}/innerHTML/appendChild/querySelector/addEventListener/contains`；而真实 helper 会用到 **`classList.toggle`、`setAttribute`、`removeAttribute`、`getAttribute`、`focus`、`tabIndex`、`hidden`** → 会 `TypeError`。当前用 stub 规避。
- **（潜在）定时器断言脆弱**：`h.timer()` 取 timers Map 的第一个并断言 `140`/`0`。若页签/角标逻辑在渲染路径引入 `setTimeout`（例如角标淡出动画用 JS 定时器），定时器顺序/数量会变，`assert.equal(h.timer(),140)`、`h.finish()`、`timers.size===0` 会碎。要求：**dirty 角标只用 CSS 动画，渲染/切页路径不新增定时器**（当前实现满足）。
- **（潜在）`#param-rows` 结构**：第 71 行 `h.el('param-rows').children[0].querySelector('.chips').children.find(n=>n.type==='number')`；第 97–98 行用 `children` 引用比较“未变化快照不重绘”。要求 `renderParamRows` 仍以 `#param-rows` 为容器、首行 = 分辨率栏、输入框仍是 `type=number`、未变化时不重建 DOM（当前满足）。
- **（潜在）style.css 断言**：第 130 行 `/#param-card\s*\{\s*transition:\s*opacity 140ms ease/`——若把规则重排/合并/换行会碎。**保留 887 行原样单行格式**（当前已保留、通过）。
- **（潜在）保存参数顺序**：第 109 行断言 `saves[0][1]='wifi'`、`[2]=2048`、`[6]='both'`——`SaveProfileAndRestart` 实参顺序不可变。
- **（潜在）函数重命名/移动**：`source.indexOf('  function '+name+'(')` 找不到时 `assert.ok(start>=0,name)` 直接失败；`renderParamModal` 还必须以 `  function renderParamModal()` 两空格缩进存在（`root_repair_ui.test.js` 的切片终点也依赖它）。

**当前已应用的最小迁移（工作副本 diff，评审见 §3）**：

```diff
@@ scope（spec_modal_test.js:23-26）
     SCEZParamState:require('./param_state'),updateParamAudioRow(){},updateParamCodecRows(){},watchNativeDefaults(){},refreshNow(){},
+    // rc23-UI1：三页签/角标/说明框相关辅助（这些函数本身由页面 DOM 驱动，独立验收在真实 GUI 里做）
+    refreshParamNotes(){},showDialogTab(){},showAllDialogPages(){},currentDialogTab(){return 'display';},
+    clearDialogDirty(){},updateParamDirtyTabs(){},snapshotParamMode(){return {};},setTabDirty(){},setNote(){},
@@ 未知模式断言（spec_modal_test.js:124）
-    assert.equal(h.el('param-save').disabled,true);assert.match(h.el('param-title').textContent,/正在确认/);
+    assert.equal(h.el('param-save').disabled,true);assert.match(h.el('param-mode').textContent,/确认中/);
```

- 9 个 stub 覆盖了 `.slice` 区间内实际被调用的全部新增符号（其中 `setTabDirty`、`setNote` 是“防御性”stub：只被已 stub 的 helper 引用，当前即使不加也能过；建议保留以便未来把 helper 换真）。
- `snapshotParamMode(){return {};}` 是**语义 stub**：`openSpecModal` 会把它存成 `paramState.initial`；只要 `updateParamDirtyTabs` 被 stub 就无副作用，但**若将来把 dirty 逻辑接入该测试，必须换成真实函数**，否则 `initial` 为空会误判角标。

**最小改法建议（在已完成迁移之上）**：见 §4.3（零额外依赖的 spy 断言）与 §4.1/§4.2（新增测试）。**不建议**把 9 个 stub 全删掉改走真实 helper：需要重写 Node mock 且会把“模式切换/保存”测试与“页签 DOM”耦合；拆到新测试更稳。

### 2.2 `native_display_test.js` — 次受害测试（P0，已迁移）

- 第 64–66 行按函数名切片 `nativeTiers/frameRateTiers/ladderTiers/baseValOf/initField/makeParamState/curParam/curField/baselineOf/renderParamRows`；第 75 行直接调 `scope.renderParamRows(main, box, ()=>{})`。
- 碎点：`renderParamRows` 旧签名 `(state, box, noteEl, redraw)`，新签名 `(state, box, redraw)`；且**渲染尾部新增 `refreshParamNotes()`（app.js:4765）**，该测试的 scope 没有它 → 同样 `ReferenceError`（迁移前同因；该测试在实现中途未单独留日志，但调用路径完全相同）。
- 已迁移（diff）：

```diff
@@ native_display_test.js:62
-  updateParamAudioRow(){},updateParamCodecRows(){}});
+  updateParamAudioRow(){},updateParamCodecRows(){},refreshParamNotes(){}});
@@ native_display_test.js:75
-const box=node();scope.renderParamRows(main,box,{},()=>{});
+const box=node();scope.renderParamRows(main,box,()=>{});
```

- 该测试的 `document.createElement` mock 只有 `children/className/appendChild/addEventListener/querySelector`；**`renderParamRows` 里不要增加 `setAttribute/classList.toggle` 等调用**（它们只在 handlebar 回调里、渲染时不触发；一旦挪到渲染路径就会 TypeError）。
- 新增 `refreshParamNotes` stub 目前是必须的（尾部渲染调用），保留。

### 2.3 `root_repair_ui.test.js` — 未改、当前通过，但锚点最脆（P1）

它按 **源码字符串区间** 切两段执行：

| 锚点 | 当前 app.js 位置 | 用途 |
|---|---|---|
| `'var rootState = paramState, rootBox'` | 4889 | 第一段起点 |
| `'renderParamModal();'`（其后首个） | 4900 | 第一段终点 |
| `"el('param-root-repair').addEventListener('change'"` | 4908 | 第二段起点 |
| `'function renderParamModal()'` | 4922 | 第二段终点 |

- **当前为什么安全**：`openSpecModal` 新增的 `initial/touched/showDialogTab/clearDialogDirty`（4885–4888）全部插在 `var rootState…` 锚点**之前**，不在切片内；root 读写与 `renderParamModal` 定义均未动。
- **会怎样碎**：
  1. 新增代码被放到 `var rootState…` → `renderParamModal();` 之间，且调用 `showDialogTab/clearDialogDirty/setNote/snapshotParamMode` 等测试 context 里没有的符号 → `ReferenceError`；
  2. 该区间内对 `el('param-tabs')`/`el('param-pending')` 等做 `.classList`/`.style` 操作也会碎：root 测试的 `el:()=>box` 永远返回同一个 checkbox 假对象（只有 `checked/disabled/addEventListener`，没有 `classList`/`style`），任何其它 DOM 操作都会 `TypeError`；
  3. 根修复 change handler 若被移进函数（如 `bindRootRepair()`）或挪到 `renderParamModal` 之后，两段锚点会切出空/错区间（vm 语法错误或断言失败）。
- **结论/要求**：**不改测试**；实现方保持上表 4 个字符串与代码块顺序（root repair 即时保存、禁用/回滚语义原样），把页签初始化放在锚点之前即可。如果非移不可，最小修法是在 `context` 里补 `showDialogTab(){}, clearDialogDirty(){}, snapshotParamMode(){return {};}` 三个 stub（不要补 DOM 操作）。

### 2.4 `settings_compat_test.js` — 不改（P2 约束）

- 切片：`source.indexOf('  var settingsState = ')` → `source.indexOf('  var lastProfileSaveError = ', start)`（当前 5029 → 5456），**整段** `vm.runInContext`；context 只给 mock `el`（支持 style/classList add/remove/attrs/handlers）、`SetSettings` 等。测试全绿。
- 约束：**不要改这份测试，也不要在这两个锚点之间插入任何新的顶层 DOM/全局调用**（该区间含通知策略等大段代码，插错会立即以 missing symbol 碎掉）；`#settings-modal` 本次不改，符合设计。
- 同理 `notification_settings_test.js`：同一对锚点 + `function closeAppWinModal(`（用于关闭弹窗 helper 切片），保持即可。

### 2.5 `update_flow.test.js` — 不改（P2 约束）

- 切片：`// ---------- 应用内更新：`（4248）→ `// ---------- 参数状态机`（4352）；context 只有 `window/el/lastState/toast/setTimeout/setInterval`。
- 约束：两个 marker 文本必须保留；**该区间内不要插入新的顶层语句**。UI1 的 helper 在 1715（区间外），param 三页签新代码在 4352 之后（区间外）——当前安全。

### 2.6 其余 21 个 JS 测试 — 不改

`app_entry_mask_test.js`、`app_icon_cache_test.js`、`app_icon_refresh_test.js`、`app_launch_ui_test.js`、`app_read_pipeline_test.js`、`app_search_test.js`、`appsetting_state_test.js`、`appwin_bar_test.js`、`appwin_text_test.js`、`device_cards_test.js`、`drag_order_test.js`、`identity_bindings_test.js`、`new_device_popup_test.js`、`pair_discovery_compat.test.js`、`pair_qr_refresh.test.js`、`pair_retry_state.test.js`、`pair_tcpip.test.js`、`pair_ui_test.js`、`param_state_test.js`、`session_map_test.js`、`update_ui.test.js`。

- 前 20 个按函数/正则切 app.js 或独立模块，**不引用两个弹窗的 DOM/状态**；`app_launch_ui_test.js` 只断言 app.js 字符串 `appendAppRestartButton` 不存在 / `appLaunchDialog.sync(st.appWins || [])` 存在——UI1 未触及。
- **`param_state_test.js` / `appsetting_state_test.js` 是纯状态机测试，本轮一定不能改**（`param_state.js`/`appsetting_state.js` 未被修改，语义不变；改得越多越容易掩盖真问题）。
- 全部 21 个当前通过。

### 2.7 Go 侧测试 — 不改（已验证）

- gui 下**没有** Go 测试断言两个弹窗的 DOM id/结构（全仓 `*_test.go` 中 `param-`/`appset-` 无匹配；`web/` 引用只有 `gui/internal/ui/html_test.go` 与 `gui/main_windows.go`）。
- `html_test.go:40-75`（`TestBuildIndexRealAssets`）只做资源组装冒烟：读 `web/index.html`、`style.css`、`session_map.js`、`param_state.js`、`appsetting_state.js`、`drag_order.js`、`app.js` → `BuildIndex` 替换占位符 → 断言存在 `<!DOCTYPE html>`、`})();`、`SessionMap`、`SCEZParamState`、`SCEZAppSetState`、`DragOrder`、`logmods`。本次 index.html 的 `/*__CSS__*/`（7 行）、`/*__JS__*/`（581 行）与 JS 内标记全部保留；**没有新增 web JS 文件**，所以 `main_windows.go` 的 embed 列表与 `html_test.go` 无需改动。已用 `<drive>/go\bin\go.exe test ./internal/ui/ -count=1` 实跑 **ok**。
- 唯一“Go 相关”改动是版本号：`main_windows.go:166 version="v2.4.2-rc.23-UI1"`、`assets/version.rc` 版本资源；无 Go 测试断言该字符串（已 grep 确认），不影响测试。
- **如果后续为了纯函数化新增 `gui/web/*.js` 资源文件**，则必须同步：`gui/main_windows.go` 的 `//go:embed` + `BuildIndex` 拼接参数、`gui/internal/ui/html_test.go` 的 `TestBuildIndexRealAssets` 拼接与断言。本报告不建议为此改动（见 §4.1 的切片方案可直接用现有 app.js）。

---

## 3. 现有迁移评审（够不够？）

**结论：够让既有回归全绿，但不够证明新 UI 正确。**

- 正确的部分：
  - 只动了 2 个必然受影响的测试，其他 25 个零改动——符合“最小迁移”；
  - `spec_modal_test` 保留全部旧断言与 `#param-card` 140ms/style.css/保存参数/草稿保活检查，只补 stub + 唯一失效文本断言，风险最低；
  - `native_display_test` 只修调用签名与渲染尾依赖；
  - root/settings/notification/update 四类锚点区间未被新代码污染（见 §2.3–2.5）。
- 覆盖缺口（按重要性）：
  1. **页签切换没有任何断言**：spec test 把 `showDialogTab/showAllDialogPages/currentDialogTab` 全部 stub 掉，`#param-tabs`/`#param-pending` 的显隐、默认停在「画面」、切页保留草稿——零覆盖；
  2. **dirty 角标没有任何断言**：`updateParamDirtyTabs`/`updateAppsetDirtyTabs` 全被 stub；而这是本轮唯一“新状态逻辑”（false→true→改回原值 false→保存/取消清空），最值得单测；
  3. **每页说明框联动零覆盖**：`setNote/resetParamPageNote/resetAppsetPageNote` 被 stub；
  4. **appset-modal 本来就没有任何 DOM 测试**（rc23 就没有），重构后仍无；且 appset 有 `touched` 门控、比例校验切页、功能页点击说明等新分支；
  5. **mock `el()` 对任意 id 都返回新节点**——如果 DOM id 被改名/删除（如旧 `#param-note`），测试不会报错，只有真实 GUI 才炸。本次静态检查 171/171 通过，但没有测试固化这一点。

---

## 4. 建议新增测试（按优先级；可直接在 `node --test` 下自动发现）

> 原则：新逻辑抽得越“纯”越好测。当前 app.js 的 dirty 判定函数（`paramEffectiveFieldFor/snapshotParamMode/updateParamDirtyTabs`、`appsetEffectiveField/snapshotAppSetState/updateAppsetDirtyTabs`）只依赖全局状态对象 + `setTabDirty`，非常适合“函数名切片 + `setTabDirty` spy”的纯单测，不需要 DOM mock。

### 4.1 [P0] 新增 `gui/web/dialog_dirty_test.js` — 脏标记计算

用与 spec test 相同的函数切片法加载：
`paramEffectiveFieldFor`、`snapshotParamMode`、`updateParamDirtyTabs`、`appsetEffectiveField`、`snapshotAppSetState`、`updateAppsetDirtyTabs`；scope 只需 `{el:()=>({}), paramState:null, appSetState:null, setTabDirty(prefix,name,dirty){marks[prefix+'/'+name]=dirty;}}`。

建议用例（参数化）：
- param 初始（`touched` 全 false）→ display/audio/feature 全 false；
- `touched.display=true` 但值不变 → display 仍 false（“改回原值消失”的根）；
- res 改 `2048` → display true；改回 `1920` → false；
- 自定义输入态：`{edit:true,input:'1920',val:1920}` 应视为未改（effective=1920）→ false；`input:'2048'` → true；`input:''`/非法 → 回 val；
- fps/bit 改档/revert、`lockFps`/`lockBitrate` 切换/回退各 ±1 例；
- vcodec `h265`→true→`h264`→false；audio `both`→true→`pc`→false；acodec 同理；
- **feature 页恒 false（root 自动修复即时生效，不计草稿）**；
- appset 侧：size/fps/bit/dpi 档位与 ratioW/H、vcodec+ownVCodec、lock ×2 → display；audio/acodec+ownACodec → audio；flex/perspective/perspectiveInputFps → feature；`touched` 未置位时对应页不脏；改回原值后角标消失。
- 先 `initial = snapshotParamMode(state, mode)` / `snapshotAppSetState(state)` 再逐项改，覆盖“打开后第一次点击才允许出现角标”的门控语义。

> 注意 fixture 必须补齐状态形状：param `{mode, profile[两套], audio[两套], vcodec[两套], acodec[两套], fields[两套], initial, touched}`；appset `{fields{size,fps,bit,dpi}, ratioW/H, flex, perspective, perspectiveInputFps, audio, vcodec, acodec, ownVCodec, ownACodec, lockFps, lockBitrate, touched}`（`touched` 缺失会 TypeError，因为 helper 里直接 `touch.display`）。

### 4.2 [P1] 新增 `gui/web/dialog_tabs_test.js` — 页签 helper 行为

函数切片加载：`dialogTabEl/dialogPageEl/showDialogTab/showAllDialogPages/currentDialogTab/bindDialogTabs/setTabDirty/clearDialogDirty/setNote`，scope 补 `DIALOG_TABS:['display','audio','feature']` + 一个支持 `classList{add,remove,toggle,contains}`、`setAttribute/getAttribute/removeAttribute`、`tabIndex`、`focus()`、`click()`、`style`、`textContent` 的节点工厂。
用例：
- 初始 `showDialogTab('param','display')`：display 按钮 `active` + `aria-selected="true"` + `tabIndex=0`；audio/feature 为 false/-1；display 页无 `hidden`，另两页有 `hidden`；
- `showDialogTab('param','audio')` 后各状态翻转；非法 name 回退 display；
- `currentDialogTab` 反映 `active`；
- `bindDialogTabs('param', cb)` 后模拟点击 `#param-tab-audio` → `cb` 收到 `'audio'`；←/→ 键循环（audio→feature→display）且 `preventDefault`；
- `setTabDirty('param','display',true)` 加 `dirty` 类；`clearDialogDirty('param')` 清三页；
- `setNote('param-note-display','x')` 写 textContent；
- 同一套对 `'appset'` 前缀跑一遍（id 前缀 `appset-tab-*`/`appset-page-*`）。
> 页面 DOM 里的真实 id 见 §1.1；helper 本身与 CSS 无关。不要整段切 1715–1781（含 `var PARAM_FEATURE_NOTE_DEFAULT = el(...)` 顶层执行与 `bindDialogTabs` 调用点），逐函数切。

### 4.3 [P1] `spec_modal_test.js` 增量断言（零新增依赖，最小改动）

把现有 stub 改成 spy（行为不变），在原有流程里补 4 类断言：
1. **打开默认画面页**：`await open(h)` 后 `assert.ok(showCalls.includes('param:display'))`（`openSpecModal` 4887）、`assert.equal(h.el('param-tabs').style.display,'')`、`assert.equal(h.el('param-pending').style.display,'none')`；
2. **未确认模式**（现 123–124 行附近）：`assert.equal(h.el('param-tabs').style.display,'none')`、`assert.equal(h.el('param-pending').style.display,'')`、`assert.ok(allCalls.includes('param:false'))`（`showAllDialogPages('param',false)` 4932）；
3. **关闭清角标**：`s.closeSpecModal()` 后 `assert.ok(cleared.includes('param'))`（closeSpecModal 4817）；
4. **就绪渲染会刷新角标**：`assert.ok(dirtyCalls.length>0)`（renderParamModal 4937）。
spy 写法示例：
```js
const calls={show:[],all:[],cleared:[],dirty:0,notes:[]};
showDialogTab:(p,n)=>calls.show.push(p+':'+n),
showAllDialogPages:(p,on)=>calls.all.push(p+':'+on),
currentDialogTab(){return 'display';},
clearDialogDirty:p=>calls.cleared.push(p),
updateParamDirtyTabs:()=>{calls.dirty++;},
setNote:(id)=>calls.notes.push(id),
```
（spy 放在原 stub 位置，`harness()` 返回里带出 `calls` 即可；不需要升级 Node mock。）
> 若想测真实 DOM 类切换（`active`/`hidden`/`aria-selected`），请走 §4.2 单独测试，不要在这里重写 Node mock。

### 4.4 [P2] 新增 `gui/web/dom_ids_test.js` — 静态 DOM id 一致性（防“mock 放行缺失 id”）

- 读 `index.html`，正则收集 `id="([^"]+)"` 集合；读 `app.js`，正则收集 `el\('([^']+)'\)` 字面量；断言字面量集合 ⊆ html id 集合（另可收集 `getElementById('…')` 字面量）。
- 当前实测 **171 个字面量 id 全部在 index.html 中（220 个 id），0 缺失**；`el('param-note')`/`el('appset-note')` 旧 id 残留为 0。本测试可固化 §1.1 的新 id（`param-tabs/param-tab-*/param-page-*/param-note-*/param-pending/param-mode`、`appset-tabs/appset-tab-*/appset-page-*/appset-note-*/appset-mode/appset-card`），防止后续再 DOM 化时“测试绿、真机白屏”。
- 可选：顺手断言 `style.css` 含 `#param-card { transition: opacity 140ms ease; }`（已由 spec test 覆盖，可去重）。

### 4.5 appset 侧说明

- rc23 基线**没有** appset-modal DOM 测试，所以“迁移”对 appset 是 0 项；但三页签后 appset 有：`#appset-tabs` 切换、每页说明、dirty 角标、比例非法时自动切回画面页（2266–2268）、功能页三行点击说明（2168–2176）。§4.1/§4.2 已覆盖 dirty/helper 纯逻辑；真实 DOM 联动建议由 task-2 的 CDP 脚本验收（它已规划“切页签/改档位出橙点/改回消失/功能页说明变化”断言）。若一定要仓内集成测试，需要切 top-level bindings + 完整 DOM mock，成本远高于收益，**不建议本期做**。

---

## 5. 给实现者的清单：必须保留的 id / class / 行为

### 5.1 测试硬依赖 id（删/改会直接碎测试或失去覆盖）

| id | 依赖方 |
|---|---|
| `#param-modal` | spec_modal_test（display none/关闭）、closeSpecModal |
| `#param-card` | spec_modal_test（`param-mode-out`/`param-mode-busy`/`inert`）+ style.css 887 规则 |
| `#param-rows` | spec_modal_test:71,97–98（children[0].querySelector('.chips')、DOM 引用保持） |
| `#param-save` | spec_modal_test（disabled/click）|
| `#param-mode` | spec_modal_test:124（`/确认中/`）|
| `#param-title` | 渲染契约（app.js:4925；不再承载“正在确认”文案）|
| `#param-root-repair` | root_repair_ui.test.js（change handler 锚点 + 假 checkbox）|
| `#param-audio-slider` | closeSpecModal 取消拖动（`__cancelAudioDrag`）与 render 刷新 |
| `#param-tabs` / `#param-pending` | 未确认模式显隐（建议 §4.3 断言）|
| `#param-note-display/audio/feature` | 每页说明（新 UI 契约；静态 id 测试 §4.4）|
| `#param-tab-display/audio/feature`、`#param-page-display/audio/feature` | 页签 helper/新增测试 §4.2 |
| `#appset-*` 全组（card/title/mode/tabs/tab-*/page-*/note-*/rows/vcodec/audio/acodec/flex/perspective/fps/save/cancel）| app.js `el()` 字面量（静态 id 测试 §4.4）与真实 GUI 验收 |

### 5.2 必须保留的 class / ARIA / 状态

- `#param-card`：`inert` 开关、`param-mode-out`（淡出）、`param-mode-busy`（禁指针）；**140ms 两段定时**（淡出 140 → 换模式重绘 → 淡入 140 → 清 transition），reduced-motion 时 **0ms**；过渡期 `#param-save.disabled=true`、点击保存不生效；切换前先 `blur()` 焦点输入框、取消滑条拖动；**两套草稿保留、不重新拉档案**（一次打开只 1 次 `GetProfile`）。
- 页签：`active`（当前页/tab）、`hidden`（非当前 page）、`aria-selected="true|false"`、`tabIndex 0|-1`；`dirty` 角标类只做 CSS 动画（**禁止 JS 定时器进入渲染路径**，否则 spec test 定时器断言碎）。
- 页签切换只切显示、不重建内容；连接方式切换时**保持当前页签**（renderParamModal 用 `showDialogTab(currentDialogTab())`）。
- 未确认模式：`#param-tabs` 隐藏、`#param-pending` 显示、三页统一隐藏、`#param-save` 禁用、`#param-mode` 显示「确认中」。
- root 自动修复：**即时生效、不参与保存/取消、不计 dirty**；读取中/失败禁用，失败仅 toast 且回滚（root 测试覆盖）。
- dirty 语义：任一页草稿改动 → 对应页签橙点；改回原值（档位切回、自定义输入回原值/清空、比例清空、锁定/开关/滑条回原位）→ 消失；保存/取消/重新打开 → 清空；连接方式切换两套草稿各自保留；feature 页（root）恒不脏。
- 保存协议不变：param `syncSpecModal → settle → closeSpecModal → toast → SaveProfileAndRestart(serial, mode, res, fps, bit, custom, audio, lockFps, lockBitrate, vcodec, acodec)`；appset `settle×4 → parseRatio 拦截（并自动切回画面页）→ payload（字段/顺序不变）→ close → toast → SaveAppWinParams`。`saves[0][6]==='both'` 这类位置断言依赖实参顺序。
- 声音滑条：一次性绑定、拖动只局部刷新（`updateParamAudioRow`）、**禁止拖动中重建面板**；重置按钮 flash 反馈保留（`setTimeout 220ms` 只在点击路径）。
- `renderParamRows`：仍写 `#param-rows`；三行顺序 `res/fps/bit`；每行 `.param-row` + `.param-rowhead` + `.param-label` + `.param-actions` + `.chips`；输入框 `type=number`；档位 chip 点击清自定义、`＋自定义`就地变输入、重置回 baseline。

### 5.3 必须原样保留的源码锚点（测试靠字符串定位）

| 锚点字符串 | 当前行 | 依赖测试 |
|---|---|---|
| `var rootState = paramState, rootBox` | 4889 | root_repair_ui |
| `renderParamModal();`（root 块后首个） | 4900 | root_repair_ui |
| `el('param-root-repair').addEventListener('change'` | 4908 | root_repair_ui |
| `  function renderParamModal()`（两空格缩进） | 4922 | root_repair_ui（切片终点）、spec_modal_test（函数名切片）|
| `  function renderParamRows(` / `makeParamState(` 等 17 个函数名 | §2.1 | spec_modal_test |
| `  el('param-save').addEventListener('click'` | 4989 | spec_modal_test（保存块切片起点）|
| `\n  // ---------- 指引弹窗` | 5018 | spec_modal_test（切片终点）|
| `  var settingsState = ` | 5029 | settings_compat_test、notification_settings_test |
| `  var lastProfileSaveError = ` | 5456 | settings_compat_test、notification_settings_test |
| `  function closeAppWinModal(` | — | notification_settings_test |
| `// ---------- 应用内更新：` | 4248 | update_flow.test |
| `// ---------- 参数状态机（参数浮窗专用；纯函数 SCEZParamState 单测覆盖）` | 4352 | update_flow.test（切片终点）|
| `#param-card { transition: opacity 140ms ease; }` | style.css:887 | spec_modal_test:130 |

**新增三页签代码应放在 `// ---------- 参数状态机`（4352）之后、`// ---------- 指引弹窗`（5018）之前（当前实现即如此）；helper 段放在 1715 区（对现有切片安全）。root repair 块（4889–4921）保持原样顺序。**

### 5.4 其它

- 不要新增 `gui/web/*.js` 资源文件（否则改 Go embed + BuildIndex + html_test 三处）；纯逻辑测试用“函数名字符串切片”即可复用现有 app.js。
- 版本号 `v2.4.2-rc.23-UI1` 无测试依赖；若更新请同步 `main_windows.go` 与 `assets/version.rc`。
- 真实 GUI 验收：`#param-modal` 位于 `#view-cast` 内（view-enter 160ms transform），如后续把弹窗移到 body 级，浏览器真实行为会变，但上述 mock 测试不受影响。

---

## 6. 复跑命令（提交前回归）

```powershell
# 全量 web 测试（27 个；自动发现 *_test.js 与 *.test.js）
cd <historical-workspace>/ez-rc23-UI1\gui\web
node --test

# 本次受影响/关键测试
node spec_modal_test.js
node native_display_test.js
node root_repair_ui.test.js
node settings_compat_test.js
node notification_settings_test.js
node update_flow.test.js
node param_state_test.js
node appsetting_state_test.js

# Go 资源组装冒烟（需要 CGO + mingw64；环境变量与 packaging/rc22/regression.py 对齐）
cd <historical-workspace>/ez-rc23-UI1\gui
$env:PATH='<msys-root>\mingw64\bin;<drive>/go\bin;'+$env:PATH
$env:GOCACHE='<historical-workspace>/rc17-app-power-save-20261024\go-cache'
$env:GOMODCACHE='<historical-workspace>/gui\toolchain\win\gopath\pkg\mod'
$env:GOPROXY='off'; $env:GOSUMDB='off'; $env:GOTOOLCHAIN='local'; $env:CGO_ENABLED='1'
$env:CC='<msys-root>\mingw64\bin\gcc.exe'; $env:CXX='<msys-root>\mingw64\bin\g++.exe'
<drive>/go\bin\go.exe test ./internal/ui/ -count=1
```

发布脚本参考：`packaging/rc22/regression.py`（第 46 行 `web = sorted(set(...glob('*.test.js')) | set(...glob('*_test.js')))`，用 `node --test` 跑全部；新增测试文件自动纳入，无需注册。rc23/UI1 若需要独立回归脚本，可复制该模式，只需改 `WORK`/版本路径）。

**本报告未执行/未覆盖**：真实 WebView2 GUI（CDP 验收由 `gui-verifier` 负责）；Go 全量 `go test ./...`（只跑了 `./internal/ui`，其余包不涉及本次 DOM 改动）；性能/像素级视觉回归。

---

## 附录 A：证据摘要

- 迁移前实测失败（22:05:13 快照）：
  `ReferenceError: refreshParamNotes is not defined` → `renderParamRows`（app.js:4765 调用点）→ `renderParamModal` → `spec_modal_test.js:70`。
- 迁移后（22:12 快照）：`node --test` → `# tests 27 / # pass 27 / # fail 0`。
- 基线（rc23 只读副本）：27/27 pass。
- Go：`ok scrcpy-ez/gui/internal/ui 3.095s`（Go 1.27.0 / mingw64）。
- 静态 id：`el('…')` 字面量 171 个，index.html 定义 220 个 id，缺失 0；`el('param-note')`/`el('appset-note')` 残留 0。
- 设计依据：`<runtime-root>\rc23-UI1\design\README.md`（§4/§5 交互约定、§8 实现硬约束）、`_audit-settings-inventory.md`（§8 实现约束明确要求迁移 spec_modal_test 并保留 `#param-card` 140ms 与锚点）。

## 附录 B：27 个 gui/web 测试分类

- **A. 本轮已改（2）**：`spec_modal_test.js`、`native_display_test.js`
- **B. 锚点/切片约束、当前通过、不改（4）**：`root_repair_ui.test.js`、`settings_compat_test.js`、`notification_settings_test.js`、`update_flow.test.js`
- **C. 与两个弹窗无关、不改（21）**：`app_entry_mask_test.js`、`app_icon_cache_test.js`、`app_icon_refresh_test.js`、`app_launch_ui_test.js`、`app_read_pipeline_test.js`、`app_search_test.js`、`appsetting_state_test.js`、`appwin_bar_test.js`、`appwin_text_test.js`、`device_cards_test.js`、`drag_order_test.js`、`identity_bindings_test.js`、`new_device_popup_test.js`、`pair_discovery_compat.test.js`、`pair_qr_refresh.test.js`、`pair_retry_state.test.js`、`pair_tcpip.test.js`、`pair_ui_test.js`、`param_state_test.js`、`session_map_test.js`、`update_ui.test.js`
