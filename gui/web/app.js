// scrcpy-ez GUI 前端逻辑（轮 B：多设备并行投屏——标签架构 + 新设备弹窗）
// Go 桥接函数（webview RPC，Promise 风格）：
//   GetState() -> Snapshot JSON
//   StartCast(serial string) -> error（设备卡"投屏"：普通会话）
//   StartCastParallel(serial string) -> error（新设备弹窗"开始投屏"：并行会话 SCEZ_NO_WATCH=1）
//   StopCast(serial string) -> error（停止=杀树退出，按会话；Session.stopping 透传"正在终止…"）
//   RestartCast(serial string) -> error（重启=杀树后重跑新会话，按会话）
//   ResetCast() -> error（结束态返回设备列表前重置投屏状态）
//   DismissNewDevice(serial string) -> error（弹窗"暂不"：本在线周期不再弹）
//   ForgetSession(serial string) -> error（标签淡出动画完成后移除结束态会话）
//   GetProfile(serial string) -> DeviceProfile（参数浮窗读参数记忆）
//   SaveProfileAndRestart(serial, mode, res, fps, bitrate, custom, audio, lockFps, lockBitrate) -> error（浮窗保存并重投）
//   SaveProfile(serial, mode, res, fps, bitrate, custom, audio, lockFps, lockBitrate) -> error（Go 保留接口；前端无独立入口）
//   PairConnect(devKey, ip, pairPort, connPort, code) -> error（无线调试配对向导：受理即返回，状态经 pairStatus 轮询）
//   PairReset() -> error（清配对向导状态）
//   RefreshNow() -> Snapshot JSON
//   SetSettings(showParamOverlay, closeToTray) -> error（设置面板两个开关，落盘 settings.json）
//   ExitApp()
// 只读展示架构：GUI 不向 bat 写 stdin；choice 菜单按钮全部映射为会话级操作。
// 多会话：sessions = map[serial] -> {tab, pane, refs, fading}，
// Snapshot.Sessions 每会话内嵌 cast（状态卡/规格徽标/raw 输出各自独立）。
(function () {
  'use strict';

  var lastJson = '';
  var lastState = null;
  var pollTimer = null;
  var sessions = {};     // serial -> 会话标签/面板
  var castOrder = [];    // 设备标签显示顺序（serial 顺序；纯 GUI——拖拽只改此数组，不碰后端/会话映射）
  var activeSerial = null;
  var toastTimer = null;
  var popupSerial = null; // 当前展示的新设备弹窗 serial
  var pairOpen = false;   // 配对向导弹窗打开标记（pairStatus 渲染闸门）
  var pairCtx = null;     // 配对上下文：{key, ip, pairPort, connPort, name, addr, pending, idx, manual}

  // gui51：批量管理状态（batchMode=批量管理模式；renameMode=批量改名模式）
  var batchMode = false;
  var renameMode = false;
  var batchSel = {};      // devKey -> true（选中设备）
  var renameDraft = {};   // devKey -> 改名输入草稿（快照重渲染不丢输入）
  var delBubbleEl = null; // 右键单删气泡 DOM
  var delBubbleKey = null;// 右键单删气泡对应的 devKey

  // ---------- 工具 ----------
  function el(id) { return document.getElementById(id); }

  function esc(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }

  function sanitizeId(s) { return String(s).replace(/[^a-zA-Z0-9_-]/g, '_'); }

  function toast(msg) {
    var t = el('cast-toast');
    t.textContent = msg;
    t.style.display = '';
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { t.style.display = 'none'; }, 2200);
  }

  // 副行 = 当前连接方式的投屏参数：
  //   USB 在线 → "USB · 序列号 · <原生分辨率(宽≥高)> · <Hz>"
  //   仅无线   → "无线 · IP:端口 · <无线分辨率(长边1920换算)> · 60fps"
  function fmtSub(d) {
    var parts = [];
    if (d.connType === 'usb') {
      parts.push('USB');
      if (d.serial) parts.push(d.serial);
      if (d.res) parts.push(d.res + (d.fps ? ' · ' + d.fps + 'Hz' : ''));
    } else if (d.connType === 'wifi') {
      parts.push('无线');
      if (d.wirelessIP) parts.push(d.wirelessIP);
      else if (d.serial) parts.push(d.serial);
      if (d.wirelessRes) parts.push(d.wirelessRes + ' · ' + (d.fps ? d.fps + 'fps' : '60fps'));
    } else {
      parts.push('其他');
      if (d.serial) parts.push(d.serial);
    }
    if (d.identity && d.identity.indexOf('pending:') === 0) parts.push('身份待确认');
    return parts.join(' · ');
  }

  // ---------- Tab 切换（设备/窗口总览；顶部共享栏常驻，tab 仅点击切换、禁止拖拽 gui28） ----------
  function switchView(name) {
    if (name === 'devices') clearAppWins(); // 二期：回设备页=撤销未完成的应用选择（清蓝灯标签+面板）
    document.querySelectorAll('.tab').forEach(function (t) {
      t.classList.toggle('active', t.getAttribute('data-view') === name);
    });
    document.querySelectorAll('.view').forEach(function (v) {
      var on = v.id === 'view-' + name;
      if (on) {
        v.style.display = '';
        // gui53：视图切换轻过渡——重启动画（先移除再强制 reflow 后添加）
        v.classList.remove('view-enter');
        void v.offsetWidth;
        v.classList.add('view-enter');
      } else {
        v.style.display = 'none';
        v.classList.remove('view-enter');
      }
    });
  }
  document.querySelectorAll('.tab').forEach(function (t) {
    t.addEventListener('click', function () {
      var name = t.getAttribute('data-view');
      switchView(name);
      if (name === 'cast') {
        // v2.1.29：用户手动切回窗口总览页——清孤儿空标签（设备页期间卡片清空、
        // 失去清理时机的蓝灯页）+ 激活修正（当前激活已无内容时切到仍有内容的
        // 标签）。只挂用户点击：程序性 switchView（点设备卡/投屏/开窗路径）不受
        // 影响——那些路径有自己的激活逻辑（新会话自动激活 / openAppWin 激活目标）。
        pruneEmptyAppWins();
        ensureCastActive();
      }
    });
  });

  // gui28：顶部 tabs 禁止拖拽换位——固定顺序 devices→cast（index.html 静态 DOM，
  // 初始 active=devices 也来自静态标记），不再读写 localStorage 的 scez_tab_order；
  // 点击切换（switchView）不变。投屏中设备标签行（#cast-tabbar）的 DragOrder
  // 拖拽排序是功能，保留（见下方 updateTabs 区的 DragOrder.attach）。

  // ---------- 活动会话映射（serial -> Session；设备卡按 serial/无线地址/identity 绑定） ----------
  // 纯函数抽到 session_map.js（SessionMap.activeSessionMap/matchSession）——多会话隔离
  // 单测覆盖（node web/session_map_test.js）：双设备互不串、同身份双键命中唯一会话、
  // identity 空不误配。此处一律经 SessionMap 调用，禁止本地重写匹配逻辑。

  // ---------- 设备列表渲染 ----------
  // gui44：富化字段粘滞（真空轮沿用上次电量/分辨率/无线规格；60s 过期）
  var fieldSticky = {};
  function stickyKey(d) { return d.identity || d.serial; }
  function stickyFill(d) {
    // gui44：只对真实在线卡粘滞（离线/未授权不补）。
    // gui49-fix8：connecting（连接中/断开中）是「未就绪」状态，也不粘滞——
    // 否则上轮有线规格/电量会补进遮罩卡，渲染出「全规格有线卡」误导帧。
    if (d.state !== 'device' || d.connecting) return;
    var k = stickyKey(d), now = Date.now();
    var e = fieldSticky[k];
    if (!e) e = fieldSticky[k] = { battery: 0, res: '', wirelessRes: '', at: now };
    if (d.battery > 0) e.battery = d.battery;
    if (d.res) e.res = d.res;
    if (d.wirelessRes) e.wirelessRes = d.wirelessRes;
    if (now - e.at > 60000) return;
    if (d.battery <= 0 && e.battery > 0) d.battery = e.battery;
    if (!d.res && e.res) d.res = e.res;
    if (!d.wirelessRes && e.wirelessRes) d.wirelessRes = e.wirelessRes;
    e.at = now;
  }

  // gui45：设备卡顺序后端持久化（profiles.json deviceOrder）——WebView2
  // 环境 localStorage 不可用（about:blank opaque origin），顺序由快照下发、
  // 变更经 SetDeviceOrder 写回。devOrder 仅前端内存镜像（渲染/拖拽用）。
  function devKey(d) { return d.identity || d.serial; }
  function saveOrderBackend(order) {
    if (typeof window.SetDeviceOrder === 'function') {
      window.SetDeviceOrder(order).catch(function () { /* 下轮快照自愈重写 */ });
    }
  }
  var devOrder = [];

  // gui52-fix15：删除中状态——点击删除立即反馈「删除中…」（后端同步跑 adb disconnect
  // 逐个地址最多 3s，处理期间卡片不再干等）；成功后 View Transition 淡出+下方卡片上移。
  // gui52-fix16e：值=时间戳；渲染时 15s 未清的残留标记自动过期（防「删除中」粘死）。
  var deletingKeys = {};
  function sweepDeletingKeys() {
    var nowK = Date.now();
    for (var k in deletingKeys) {
      if (nowK - deletingKeys[k] > 15000) delete deletingKeys[k];
    }
  }
  function vtSafeName(k) {
    // view-transition-name 必须是合法 CSS ident：非字母数字转 _码点_（保证唯一）
    return String(k).replace(/[^a-zA-Z0-9_-]/g, function (c) { return '_' + c.charCodeAt(0) + '_'; });
  }
  function animateRefresh() {
    var f = function () { refreshNow(); };
    if (document.startViewTransition) {
      document.startViewTransition(f); // 卡片淡出 + 下方卡片平滑上移（WebView2 支持）
    } else {
      f(); // 降级：无动画直接刷新
    }
  }
  // vtRender：带视口过渡的局部渲染（v2.1.34，与设备页删除的 animateRefresh 同款）——
  // 应用卡片删除（关闭窗口/停止回滚）时，下方卡片平滑上移补位（不再瞬移）；
  // WebView2 支持 View Transitions API；不支持时直接渲染（瞬移降级）。
  function vtRender(f) {
    if (document.startViewTransition) {
      document.startViewTransition(f);
    } else {
      f();
    }
  }
  function doDelete(keys, st, onDone) {
    keys.forEach(function (k) { deletingKeys[k] = Date.now(); });
    renderDevices(st || lastState); // 立即渲染「删除中…」态（不等后端）
    DeleteDevices(keys).then(function () {
      keys.forEach(function (k) { delete deletingKeys[k]; });
      toast(keys.length > 1 ? ('已删除 ' + keys.length + ' 台设备') : '设备已删除');
      if (onDone) onDone();
      animateRefresh();
    }).catch(function (e) {
      keys.forEach(function (k) { delete deletingKeys[k]; });
      toast('删除失败：' + (e && e.message ? e.message : e));
      refreshNow(); // 恢复卡片原样
    });
  }

  // ---------- gui51：批量管理 / 常驻配对入口 ----------
  function devDisplayName(d) { return d.name || d.serial; }

  function batchSelectedDevices(st) {
    var out = [];
    ((st && st.devices) || []).forEach(function (d) {
      if (batchSel[devKey(d)]) out.push(d);
    });
    return out;
  }

  function closeDeleteBubble() {
    if (delBubbleEl) {
      delBubbleEl.remove();
      delBubbleEl = null;
      delBubbleKey = null;
    }
  }

  function openDeleteBubble(x, y, key) {
    closeDeleteBubble();
    var b = document.createElement('div');
    b.className = 'pop-confirm';
    var item = document.createElement('div');
    item.className = 'pc-item danger';
    item.textContent = '🗑 删除此设备';
    item.addEventListener('click', function (ev) {
      ev.stopPropagation();
      var k = delBubbleKey;
      closeDeleteBubble();
      if (!k) return;
      doDelete([k], lastState);
    });
    b.appendChild(item);
    document.body.appendChild(b);
    // 贴指针位置，并夹在视口内
    var maxX = Math.max(8, window.innerWidth - b.offsetWidth - 8);
    var maxY = Math.max(8, window.innerHeight - b.offsetHeight - 8);
    b.style.left = Math.max(8, Math.min(x, maxX)) + 'px';
    b.style.top = Math.max(8, Math.min(y, maxY)) + 'px';
    delBubbleEl = b;
    delBubbleKey = key;
  }

  function syncBatchUI(st) {
    var n = batchSelectedDevices(st || lastState).length;
    var tg = el('batch-toggle');
    if (tg) {
      tg.textContent = batchMode ? '批量管理 ✓' : '批量管理';
      tg.classList.toggle('on', batchMode);
    }
    var bar = el('batch-bar');
    if (!bar) return;
    bar.style.display = batchMode ? '' : 'none';
    bar.classList.toggle('rename', renameMode);
    el('batch-cnt').textContent = renameMode ? ('改名 ' + n + ' 台') : ('已选 ' + n + ' 台');
    el('batch-del').style.display = renameMode ? 'none' : '';
    el('batch-rename').style.display = renameMode ? 'none' : '';
    el('batch-cast').style.display = renameMode ? 'none' : '';
    el('batch-rename-ok').style.display = renameMode ? '' : 'none';
    el('batch-del').disabled = n === 0;
    el('batch-rename').disabled = n === 0;
    el('batch-cast').disabled = n === 0;
    el('batch-rename-ok').disabled = n === 0;
  }

  function setBatchMode(on) {
    batchMode = on;
    if (!on) {
      renameMode = false;
      batchSel = {};
      renameDraft = {};
    }
    closeDeleteBubble();
    syncBatchUI(lastState);
    if (lastState) renderDevices(lastState);
  }

  function exitBatchMode(st) {
    batchMode = false;
    renameMode = false;
    batchSel = {};
    renameDraft = {};
    closeDeleteBubble();
    syncBatchUI(st || lastState);
    if (st || lastState) renderDevices(st || lastState);
  }

  function toggleBatchSelect(key, st) {
    if (batchSel[key]) delete batchSel[key]; else batchSel[key] = true;
    if (st || lastState) renderDevices(st || lastState);
  }

  // 配对入口常驻设备页底部，独立于设备列表重建和拖拽排序。
  el('pair-entry').addEventListener('click', function () { openPairModal(null); });

  function startBatchRename(st) {
    if (!batchSelectedDevices(st).length) { toast('请先选择设备'); return; }
    renameMode = true;
    renameDraft = {};
    syncBatchUI(st);
    renderDevices(st);
  }

  function collectRenameObj(st) {
    var obj = {};
    var drafts = {};
    Array.prototype.slice.call(document.querySelectorAll('#device-list .device-card .rename-input')).forEach(function (inp) {
      var card = inp.closest('.device-card');
      if (card && card.dataset.key) drafts[card.dataset.key] = inp.value;
    });
    batchSelectedDevices(st).forEach(function (d) {
      var k = devKey(d);
      var renameKey = k.indexOf('pending:') === 0 ? k + '|' + (d.identityEpoch || 0) : k;
      obj[renameKey] = (drafts[k] !== undefined) ? drafts[k].trim() : '';
    });
    return obj;
  }

  function finishBatchRename(st) {
    var obj = collectRenameObj(st);
    RenameDevices(JSON.stringify(obj)).then(function () {
      exitBatchMode(st);
      refreshNow();
    }).catch(function (e) {
      toast('改名失败：' + (e && e.message ? e.message : e));
    });
  }

  function batchDeleteOpen(st) {
    var sel = batchSelectedDevices(st);
    if (!sel.length) { toast('请先选择设备'); return; }
    var names = sel.map(function (d) { return devDisplayName(d); }).join(' · ');
    el('batch-confirm-sub').textContent = names + '（共 ' + sel.length + ' 台）';
    el('batch-confirm').style.display = '';
  }

  function batchDeleteConfirm(st) {
    var sel = batchSelectedDevices(st || lastState);
    var keys = sel.map(function (d) { return devKey(d); });
    el('batch-confirm').style.display = 'none';
    if (!keys.length) return;
    doDelete(keys, st || lastState, function () { exitBatchMode(st || lastState); });
  }

  function batchStartCast(st) {
    var sel = batchSelectedDevices(st);
    if (!sel.length) { toast('请先选择设备'); return; }
    var castMap = SessionMap.activeSessionMap(st.sessions);
    var started = 0;
    sel.forEach(function (d) {
      if (SessionMap.matchSession(d, castMap)) return; // 已在投屏中，跳过
      StartCast(d.serial).then(function () { refreshNow(); }).catch(function (e) {
        toast('启动失败：' + (e && e.message ? e.message : e));
      });
      started++;
    });
    exitBatchMode(st);
    if (started) toast('正在启动 ' + started + ' 台设备的投屏');
  }

  var batchDeviceKeys = {};
  function reconcileBatchDeviceKeys(st) {
    var next = {};
    var promoted = {};
    (st.devices || []).forEach(function (d) {
      var key = devKey(d), old = batchDeviceKeys[d.serial];
      var epoch = d.identityEpoch || 0;
      next[d.serial] = { key: key, epoch: epoch };
      var pane = appWins[d.serial];
      if (pane && pane.identity && pane.identity.indexOf('pending:') === 0 && pane.identityEpoch === epoch) {
        pane.identity = key;
      }
      if (old && (old.key !== key || old.epoch !== epoch)) {
        if (old.epoch === epoch && old.key.indexOf('pending:') === 0 && key.indexOf('pending:') !== 0) {
          if (batchSel[old.key]) batchSel[key] = true;
          if (renameDraft[old.key] !== undefined) renameDraft[key] = renameDraft[old.key];
          promoted[old.key] = key;
        }
        if (old.key !== key || old.key.indexOf('pending:') === 0) {
          delete batchSel[old.key];
          delete renameDraft[old.key];
        }
      }
    });
    Object.keys(batchDeviceKeys).forEach(function (serial) {
      if (!next[serial] && batchDeviceKeys[serial].key.indexOf('pending:') === 0) {
        delete batchSel[batchDeviceKeys[serial].key];
        delete renameDraft[batchDeviceKeys[serial].key];
      }
    });
    batchDeviceKeys = next;
    return promoted;
  }

  function collectRenameCards(box, promoted) {
    var cards = {};
    if (!batchMode || !renameMode) return cards;
    Array.prototype.slice.call(box.querySelectorAll('.device-card')).forEach(function (card) {
      var key = promoted[card.dataset.key] || card.dataset.key;
      if (!batchSel[key] || !card.querySelector('.rename-input')) return;
      card.dataset.key = key;
      if (!cards[key] || card.contains(document.activeElement)) cards[key] = card;
    });
    return cards;
  }

  // Keep the native rename input attached, including its selection and IME state.
  // If card order changes, move its siblings rather than the focused card itself.
  function commitDeviceCards(box, cards) {
    var active = document.activeElement;
    var focused = active && active.classList && active.classList.contains('rename-input') ? active.closest('.device-card') : null;
    var anchor = cards.indexOf(focused);
    Array.prototype.slice.call(box.children).forEach(function (child) {
      if (cards.indexOf(child) < 0) box.removeChild(child);
    });
    if (anchor >= 0) {
      var before = focused;
      for (var i = anchor - 1; i >= 0; i--) {
        if (cards[i].nextSibling !== before) box.insertBefore(cards[i], before);
        before = cards[i];
      }
      var after = focused.nextSibling;
      for (var j = anchor + 1; j < cards.length; j++) {
        if (cards[j] !== after) box.insertBefore(cards[j], after);
        after = cards[j].nextSibling;
      }
    } else {
      cards.forEach(function (card, i) {
        if (box.children[i] !== card) box.insertBefore(card, box.children[i] || null);
      });
    }
  }

  function renderDevices(st) {
    var promoted = reconcileBatchDeviceKeys(st);
    var box = el('device-list');
    var renameCards = collectRenameCards(box, promoted);
    // gui44：拖拽排序进行中跳过重建（下轮快照照常，轮询继续）
    if (el('device-list').classList.contains('drag-active')) return;
    // v2.1.67：条子退场动画在播（'playing'）→ 整页重建推迟（60ms 后重试，动画结束由
    // 480ms 计时器全量补渲染）——动画期间重建会让 .leaving 条子从头重播（主人实测
    // "关完卡几下动画才消失"）。'start'（待首渲）不跳过——首次渲染负责创建
    // .leaving 条子让动画开播。
    var barPlaying = false;
    Object.keys(appBarLeaving).forEach(function (k) { if (appBarLeaving[k] === 'playing') barPlaying = true; });
    if (barPlaying) { scheduleAppBarRefresh(); return; }
    sweepDeletingKeys(); // gui52-fix16e：超 15s 的删除中残留标记过期（防粘死）
    el('app-ver').textContent = st.version || '';
    el('dev-count').textContent = String(st.devices.length);
    syncBatchUI(st);

    var cards = [];
    box.classList.toggle('batch', batchMode);
    if (!st.adbOK) {
      var bad = document.createElement('div');
      bad.className = 'banner error';
      bad.textContent = '⚠ 无法连接 adb（adb.exe 缺失或未响应），请检查程序目录';
      commitDeviceCards(box, [bad]);
      return;
    }
    // 真空期去抖：adb 仍可用但当前连续失败（点投屏后 bat 重置 adb 服务的 1-2s 真空）——
    // 保留上次设备列表并给出"刷新中"软提示，不亮红条；恢复后下一轮快照自动清除。
    if (st.adbFailing) {
      var trying = document.createElement('div');
      trying.className = 'banner info';
      trying.textContent = '⟳ 设备列表刷新中…（adb 服务忙，稍候自动恢复）';
      cards.push(trying);
    }
    if (st.devices.length === 0) {
      var tip = document.createElement('div');
      tip.className = 'empty-tip';
      tip.textContent = '未发现设备 · 用 USB 线连接手机（首次请打开 USB 调试）';
      // 无线探测状态（mDNS + 并行 connect，不阻塞 UI）
      if (st.discovery) {
        if (st.discovery.status === 'searching') {
          tip.textContent += ' · 正在搜索档案中的无线设备…';
        } else if (st.discovery.status === 'notfound') {
          tip.textContent += ' · 未找到档案中的无线设备';
        } else if (st.discovery.status === 'found' && st.discovery.found) {
          tip.textContent += ' · 已找到 ' + st.discovery.found;
        }
      }
      cards.push(tip);
      commitDeviceCards(box, cards);
      return;
    }

    // gui45：以后端顺序为基底合并本轮（新设备追加/瞬态缺失保留/出档清理）
    var seen = {}, keys = [];
    st.devices.forEach(function (d) { var k = devKey(d); if (!seen[k]) { seen[k] = true; keys.push(k); } });
    var knownKeys = (st.profiles || []).map(function (p) { return p.key; });
    var base = st.devOrder || [];
    var newOrder = DragOrder.devOrderMerge(base, keys, knownKeys);
    devOrder = newOrder;
    if (newOrder.join('|') !== base.join('|')) saveOrderBackend(newOrder);

    // 按持久化顺序输出存在设备；理论上 merge 已含全部 seen，这里防漏
    var orderedDevices = [];
    devOrder.forEach(function (k) {
      for (var i = 0; i < st.devices.length; i++) {
        if (devKey(st.devices[i]) === k) { orderedDevices.push(st.devices[i]); break; }
      }
    });
    st.devices.forEach(function (d) {
      if (orderedDevices.indexOf(d) < 0) orderedDevices.push(d);
    });

    var castMap = SessionMap.activeSessionMap(st.sessions);

    orderedDevices.forEach(function (d, i) {
      var online = d.state === 'device';
      stickyFill(d); // gui44：真空轮补电量/分辨率/无线规格
      // 多会话：按设备 serial/无线地址/identity 绑定正确会话（各自"投屏中"徽章+停止按钮）
      var sess = SessionMap.matchSession(d, castMap);
      var casting = !!sess; // gui44：会话存在即显示（真空窗口 state≠device 不丢徽章）
      var key = devKey(d);
      var selected = !!batchSel[key]; // gui51：批量选中态
      var inRename = renameMode && selected;
      var deletingCard = !!deletingKeys[key];
      var dotClass = 'dot ' + (online ? (d.connType === 'usb' ? 'usb' : 'wifi') : 'off') + (casting ? ' casting' : '');
      var retained = inRename && renameCards[key];
      if (retained && (key.indexOf('pending:') !== 0 || retained.dataset.identityEpoch === String(d.identityEpoch || 0))) {
        retained.dataset.key = key;
        retained.dataset.identityEpoch = String(d.identityEpoch || 0);
        retained.className = 'device-card selected' + (deletingCard ? ' deleting' : '');
        retained.style.viewTransitionName = 'dev-' + vtSafeName(key);
        retained.querySelector('.dot').className = dotClass;
        retained.querySelector('.rename-input').disabled = deletingCard;
        cards.push(retained);
        return;
      }

      var card = document.createElement('div');
      card.dataset.key = key; // gui44：拖拽排序 key
      card.dataset.identityEpoch = String(d.identityEpoch || 0);
      card.className = 'device-card';
      // gui52-fix15：删除中 —— 整卡灰化禁点，视口过渡按 key 命名（删除后下方卡片平滑上移）
      if (deletingCard) card.className += ' deleting';
      card.style.viewTransitionName = 'dev-' + vtSafeName(key);
      var appBarKeys = [], appBarList = [], appBarCount = 0;
      if (batchMode) {
        // gui51：批量模式——灰框未选 / 绿框选中；整卡点击切换选中
        card.className += selected ? ' selected' : ' dim';
        card.addEventListener('click', function (ev) {
          if (ev.target && ev.target.classList && ev.target.classList.contains('rename-input')) return;
          toggleBatchSelect(card.dataset.key, lastState);
        });
      } else {
        // gui53：普通模式所有卡片统一边框（不再给第一张卡单独 .selected 绿框——
        // 选中态只属于批量管理模式；第一卡绿描边只会问"为什么只有它有"）。
        card.className += (casting ? ' casting' : '');
        // v2.1.59：应用投屏键/数量（卡片跳转与条子共用；提前计算）。
        // v2.1.64：并入档案全键（serials ∪ addrs）——插拔形态切换后应用窗口的键可能
        // 还是旧形态键，档案=权威映射（主人拍板"按档案查"）。
        appBarKeys = [d.serial];
        if (d.wireless) appBarKeys.push(d.wireless);
        if (sess && sess.serial) appBarKeys.push(sess.serial);
        appBarKeys = AppWinBar.appendExtraKeys(appBarKeys, profileKeysFor(d));
        appBarList = AppWinBar.keysForDevice(openAppCards, appBarKeys, d.identity || '', function (key) {
          return (appWins[key] && appWins[key].identity) || deviceIdentityOf(key);
        });
        appBarCount = AppWinBar.countFor(openAppCards, appBarList);
        // 卡片点击跳转（v2.1.59 三态）：主投屏在 → 窗口总览页（既有语义）；只有应用
        // 投屏 → 该设备的应用窗口标签页（懒建蓝灯页）；无任何投屏 → 不跳转（原有语义）。
        var jump = AppWinBar.jumpAction(casting, appBarCount);
        if (jump === 'cast') {
          // 投屏中卡片整体可点：跳转到「该设备」的投屏中标签页（v2.1.61：切换视图后
          // 显式激活该设备会话标签——此前只切视图、停留于上次激活的其他设备页，
          // 主人实测"点平板卡跳到手机标签页"即此因）。
          card.title = '查看投屏中页面';
          card.addEventListener('click', function () {
            switchView('cast');
            if (sess && sess.serial) activateSession(sess.serial);
          });
        } else if (jump === 'appwin') {
          // 只有应用投屏：点击卡片跳转到该设备的应用窗口标签页（同条子区域/整卡一致）。
          card.title = '查看应用窗口页面';
          card.addEventListener('click', function () { switchToDeviceAppWin(appBarList[0], d.name || d.serial); });
        }
      }

      // gui51：非批量模式右键卡片 → 单删确认气泡
      if (!batchMode) {
        card.addEventListener('contextmenu', function (ev) {
          ev.preventDefault();
          ev.stopPropagation();
          openDeleteBubble(ev.clientX, ev.clientY, key);
        });
      }

      // 状态点：投屏中 = 呼吸绿光动画
      var dot = document.createElement('div');
      dot.className = dotClass;

      var info = document.createElement('div');
      info.className = 'dev-info';
      if (inRename) {
        // gui51：改名模式——选中卡名称就地变输入框（绿边），只显示输入框
        var rn = document.createElement('input');
        rn.type = 'text';
        rn.className = 'rename-input';
        rn.value = (renameDraft[key] !== undefined) ? renameDraft[key] : devDisplayName(d);
        rn.disabled = deletingCard;
        rn.addEventListener('input', function () { renameDraft[card.dataset.key] = rn.value; });
        rn.addEventListener('click', function (ev) { ev.stopPropagation(); });
        info.appendChild(rn);
      } else {
        var name = document.createElement('div');
        name.className = 'dev-name';
        name.textContent = devDisplayName(d);
        if (!batchMode) {
          // gui51：批量模式只显示 点/名称/副行，以下富化标记仅在普通模式追加
          if (casting) {
            var badge = document.createElement('span');
            badge.className = 'badge-cast';
            badge.textContent = '投屏中';
            name.appendChild(badge);
            // gui43 实时形态标注：会话实际连接走 TLS → 「投屏中」胶囊旁加小标签
            if (sess.cast && sess.cast.tls) {
              var tlsCast = document.createElement('span');
              tlsCast.className = 'tag-tls cast';
              tlsCast.textContent = 'TLS加密';
              name.appendChild(tlsCast);
            }
          } else if (d.tls) {
            // gui12：有 TLS 可用（档案 mode=tls / mDNS tls 服务在播）→ 灰绿 TLS 标识
            var tlsTag = document.createElement('span');
            tlsTag.className = 'tag-tls';
            tlsTag.textContent = 'TLS';
            name.appendChild(tlsTag);
          }
          if (d.battery > 0) {
            var b = document.createElement('span');
            b.className = 'bat';
            b.textContent = d.battery + '%';
            name.appendChild(document.createTextNode(' '));
            name.appendChild(b);
          }
        }
        info.appendChild(name);
      }

      if (!inRename) {
        var sub = document.createElement('div');
        sub.className = 'dev-sub';
        if (deletingCard) {
          sub.textContent = '删除中…';
        } else if (online) {
          var sd = d;
          // gui43：投屏中副行跟随会话实际 transport（降级/插线切换不失配）
          if (casting && sess.cast && sess.cast.transportSerial) {
            sd = Object.assign({}, d, { serial: sess.cast.transportSerial });
          }
          sub.textContent = fmtSub(sd);
        } else {
          // gui49-fix5：USB offline 瞬态（线插着）→ 「连接中…」，不显示「离线」。
          sub.textContent = d.connecting ? '连接中…' : (d.state === 'unauthorized' ? '未授权 · 请解锁手机点击"允许 USB 调试"' : '离线 · ' + d.serial);
        }
        info.appendChild(sub);
      }

      card.appendChild(dot);
      card.appendChild(info);

      if (!batchMode) {
        var btn = document.createElement('button');
        // 二期 Step 2：「应用」入口（位于「投屏」左侧；就绪才出现——连接中/断开中/删除中
        // 不出入口，让连接态遮罩保持"单个按钮盖住两个入口"的既有语义）。
        // 样式与「投屏」同规格（实心/同尺寸），颜色浅一档区分（主人 0919 规格）。
        if (online && !d.connecting && !deletingCard) {
          var appBtn = document.createElement('button');
          // 与「投屏」同色同规格（主人 0919：区分度要么做大要么一样，略小=像褪色）；
          // 仅以文字区分（「应用」/「投屏」）。
          appBtn.className = 'btn primary tiny';
          appBtn.textContent = d.appBusy ? '读取中…' : '应用';
          appBtn.disabled = !!d.appBusy;
          appBtn.style.opacity = d.appBusy ? '0.55' : '';
            appBtn.addEventListener('click', function (ev) {
              ev.stopPropagation();
              // 主人 0919：先跳到「窗口总览」（应用窗口的家），再打开应用面板选择应用。
              switchView('cast');
              openAppWin(d.serial, d.name || d.serial);
            });
          card.appendChild(appBtn);
        }
        if (deletingCard) {
          // gui52-fix15：删除中 —— 按钮「删除中…」禁用（整卡已 .deleting 灰化）
          btn.className = 'btn ghost tiny';
          btn.textContent = '删除中…';
          btn.disabled = true;
          btn.style.opacity = '0.55';
        } else if (casting) {
          // 投屏中：红色"停止投屏"（StopCast(会话serial) 杀树退出 → 标签淡出）
          // stopping（主动停止已受理）→ 禁用态"正在终止…"；
          // closing（投屏窗口被点 X、bat 清理中）→ 禁用态"正在关闭…"；
          // 会话退出后快照恢复"投屏"。stopping 优先于 closing 显示。
          var stopping = !!sess.stopping;
          var closing = !!sess.closing;
          btn.className = 'btn danger tiny';
          btn.textContent = stopping ? '正在终止…' : (closing ? '正在关闭…' : '停止投屏');
          btn.disabled = stopping || closing;
          if (!stopping && !closing) {
            btn.addEventListener('click', function (ev) {
              ev.stopPropagation();
              var stopSerial = sess.serial; // 会话自己的 serial（卡片重键后仍可停止）
              btn.textContent = '正在终止…'; // 立即反馈（不等快照）
              btn.disabled = true;
              StopCast(stopSerial).then(function () { refreshNow(); }).catch(function (e) {
                toast('停止失败：' + (e && e.message ? e.message : e));
                if (lastState) renderDevices(lastState); // 失败恢复按钮（快照无变化时强刷）
              });
            });
          }
        } else {
          // gui34b：「连接中…」禁用态——插线学习遮罩窗口内合成 USB 连接中卡
          // （card.connecting=true）→ 按钮灰态禁点（与既有防重复点击态同风格：
          // disabled + :disabled 置灰 cursor:not-allowed + 半透明）；真实卡
          // 覆盖（connecting=false/缺省）→ 原样「投屏」可点，回归零影响。
          var connecting = !!d.connecting;
          btn.className = 'btn ' + (online ? 'primary' : 'ghost') + ' tiny';
          // gui49：拔线遮罩 = 无线形态 + connecting → 「断开中…」；插线遮罩（usb）保持「连接中…」
          // gui52-fix14：配对遮罩（pairing=true）虽也是 wifi+connecting，但语义是「连接中…」→
          // 优先按 pairing 标记区分：pairing → 「连接中…」，否则 wifi+connecting → 「断开中…」
          btn.textContent = connecting ? ((d.connType === 'wifi' && !d.pairing) ? '断开中…' : '连接中…') : (online ? '投屏' : '离线');
          btn.disabled = connecting || !online;
          if (connecting) {
            btn.style.opacity = '0.55';
          }
          if (online && !connecting) {
            btn.addEventListener('click', function () { startCast(d.serial, d.name || d.serial); });
          }
        }
        card.appendChild(btn);
      }

      // v2.1.58：应用投屏条——该设备有应用投屏（应用窗口）时，卡内底部出现
      // 「N 个应用正在投屏 + 全部关闭」：右侧按钮=关闭该设备全部应用投屏
      // （功能同窗口总览页「全部关闭」，复用 stopAllAppWins）。
      // 批量模式/删除中不显示（卡片内容整体简化/灰化）；数量含 closing 中的
      // 窗口（与卡片数一致，逐个摘除时递减）。
      // v2.1.59：数字单独加粗；条子整体不吞点击——点击冒泡到卡片=同款跳转。
      // v2.1.60：退场动画（条目清空 → .leaving 收缩后再移除，卡片高度随之收回）；
      // 关闭受理后按钮位换「正在关闭…」灰标签（同应用卡片遮罩语义；快照驱动保持）。
      var barLeaving = false;
      for (var bl = 0; bl < appBarKeys.length; bl++) {
        if (appBarLeaving[appBarKeys[bl]]) { barLeaving = true; break; }
      }
      if (!batchMode && !deletingCard && (appBarCount > 0 || barLeaving)) {
        var appBar = document.createElement('div');
        appBar.className = 'dev-appbar' + (barLeaving ? ' leaving' : '');
        var appBarTxt = document.createElement('div');
        appBarTxt.className = 'dev-appbar-txt';
        if (barLeaving) {
          appBarTxt.textContent = '正在关闭…';
        } else {
          var appBarNum = document.createElement('b');
          appBarNum.textContent = String(appBarCount);
          appBarTxt.appendChild(appBarNum);
          appBarTxt.appendChild(document.createTextNode(AppWinBar.suffix));
        }
        appBar.appendChild(appBarTxt);
        if (!barLeaving) {
          if (AppWinBar.allClosing(openAppCards, appBarList)) {
            // 关闭受理中（快照驱动；含点击后重建的保持）——按钮位显示灰标签
            var appBarClosing = document.createElement('div');
            appBarClosing.className = 'dev-appbar-closing';
            appBarClosing.textContent = '正在关闭…';
            appBar.appendChild(appBarClosing);
          } else {
            var appBarBtn = document.createElement('button');
            appBarBtn.type = 'button';
            appBarBtn.className = 'dev-appbar-btn';
            appBarBtn.textContent = '全部关闭';
            appBarBtn.addEventListener('click', function (ev) {
              ev.stopPropagation(); // 按钮=独立动作（全部关闭）；条子其余区域冒泡=跳转
              appBarList.forEach(function (k) { stopAllAppWins(k); });
              // v2.1.60：受理即反馈——按钮位换「正在关闭…」灰标签（重建后按数据态保持）
              var tag = document.createElement('div');
              tag.className = 'dev-appbar-closing';
              tag.textContent = '正在关闭…';
              if (appBarBtn.parentNode) appBarBtn.parentNode.replaceChild(tag, appBarBtn);
            });
            appBar.appendChild(appBarBtn);
          }
        }
        if (!barLeaving && !renderedBars[key]) {
          // v2.1.60：入场动画只对"首次出现"播一次（照抄卡片 entering 模式）
          appBar.classList.add('entering');
          setTimeout(function () { appBar.classList.remove('entering'); }, 360);
        }
        renderedBars[key] = true;
        if (barLeaving) {
          // v2.1.67：首渲完成 → 转 'playing'（此后渲染让位，动画不重播；480ms 计时器清）
          for (var bk = 0; bk < appBarKeys.length; bk++) {
            if (appBarLeaving[appBarKeys[bk]]) appBarLeaving[appBarKeys[bk]] = 'playing';
          }
        }
        card.appendChild(appBar);
      } else if (!batchMode && !deletingCard && renderedBars[key]) {
        delete renderedBars[key]; // 条子已真退场——下次重新出现时再播入场
      }

      cards.push(card);
    });
    commitDeviceCards(box, cards);
  }

  // ---------- 投屏 ----------
  function startCast(serial, name) {
    switchView('cast');
    StartCast(serial).then(function () {
      refreshNow();
    }).catch(function (e) {
      toast('启动失败：' + (e && e.message ? e.message : e));
    });
  }

  // ---------- 投屏中页（多会话标签架构） ----------
  // ---------- 二期 Step 2：应用窗口面板（设备「应用」入口 → 窗口总览页）----------
  // 与 sessions 平行的一套前端状态：每设备一个「应用标签 + 面板」（纯前端，不依赖快照）。
  // 数据（应用列表）经 GetAppList RPC 拉取；枚举未完成时空列表 + 轮询重试（≤40 次≈28s）。
  var appWins = {}; // serial → {name, tab, pane, apps, loaded, pollLeft, identity}（蓝灯表；收编/升级态 tab/pane=null）
  var appWinModalSerial = null; // 当前浮窗展示的 serial（null=浮窗关闭）

  // ---------- 同设备多形态键归一（v2.1.52） ----------
  // 背景：同一设备在不同时刻的「键」形态不同（USB serial / IP:port）——应用窗口可能用
  // 同一设备可能以不同形态键开窗（如 USB serial 与 IP:port 同时存在，实测「卡片不合并」）。四张表
  // （sessions/appWins/openAppCards/castOrder）各按字面键使用时必须经身份归一对齐。
  // 语义对照后端 session_map.js 的 matchSession 三层兜底（serial → wireless → identity）。
  function deviceIdentityOf(serial) {
    if (!serial || !lastState) return '';
    var ds = lastState.devices || [];
    for (var i = 0; i < ds.length; i++) {
      var d = ds[i];
      if ((d.serial && d.serial === serial) || (d.wireless && d.wireless === serial)) {
        return d.identity || '';
      }
    }
    var ps = lastState.profiles || [];
    for (var j = 0; j < ps.length; j++) {
      var p = ps[j];
      if (p.key === serial || (p.serials || []).indexOf(serial) >= 0 || (p.addrs || []).indexOf(serial) >= 0) return p.key;
    }
    if (sessions[serial] && sessions[serial].identity) return sessions[serial].identity;
    if (appWins[serial] && appWins[serial].identity) return appWins[serial].identity;
    return '';
  }

  // profileKeysFor（v2.1.64）：设备卡 → 档案全键（serials ∪ addrs；主人拍板"按档案查"）——
  // 档案=权威的设备↔全部键映射（active IP + USB serial，且持久化），拔线/形态切换后
  // 应用窗口的旧形态键仍能经档案归位到正确设备（替代 v2.1.63 内存历史方案）。
  // 命中判定：档案 key==identity，或设备当前任一键在档案 serials/addrs 中。
  function profileKeysFor(d) {
    var ps = (lastState && lastState.profiles) || [];
    var cand = [];
    if (d.serial) cand.push(d.serial);
    if (d.wireless) cand.push(d.wireless);
    for (var i = 0; i < ps.length; i++) {
      var p = ps[i] || {};
      var hit = !!(d.identity && p.key === d.identity);
      if (!hit && !d.identity && cand.length) {
        hit = (p.serials || []).some(function (k) { return cand.indexOf(k) >= 0; }) ||
              (p.addrs || []).some(function (k) { return cand.indexOf(k) >= 0; });
      }
      if (hit) return (p.serials || []).concat(p.addrs || []);
    }
    return [];
  }

  // findAppKeyFor：设备键 → 已存在的 appWins 键（serial 直配 → identity 兜底；无=null）。
  function findAppKeyFor(serial, identity) {
    var id = identity || deviceIdentityOf(serial);
    if (appWins[serial] && (!id || !appWins[serial].identity || appWins[serial].identity === id)) return serial;
    if (!id) return null;
    var hit = null;
    Object.keys(appWins).forEach(function (k) {
      if (!hit && appWins[k] && appWins[k].identity === id) hit = k;
    });
    return hit;
  }

  // sessionKeyOf：应用窗口键（appWins/openAppCards）→ 会话键（sessions）；无=''。
  function sessionKeyOf(appKey) {
    var id = (appWins[appKey] && appWins[appKey].identity) || deviceIdentityOf(appKey);
    if (sessions[appKey] && (!id || !sessions[appKey].identity || sessions[appKey].identity === id)) return appKey;
    if (!id) return '';
    var hit = '';
    Object.keys(sessions).forEach(function (k) {
      if (!hit && sessions[k] && sessions[k].identity === id) hit = k;
    });
    return hit;
  }

  // appKeyOf：会话键 → 应用窗口数据键（openAppCards/appWins 键）；无命中回原键。
  function appKeyOf(sessionSerial) {
    var id = (sessions[sessionSerial] && sessions[sessionSerial].identity) || deviceIdentityOf(sessionSerial);
    if ((openAppCards[sessionSerial] || appWins[sessionSerial]) &&
        (!id || !appWins[sessionSerial] || !appWins[sessionSerial].identity || appWins[sessionSerial].identity === id)) return sessionSerial;
    if (!id) return sessionSerial;
    var hit = '';
    Object.keys(appWins).forEach(function (k) {
      if (!hit && appWins[k] && appWins[k].identity === id) hit = k;
    });
    if (!hit) {
      Object.keys(openAppCards).forEach(function (k) {
        if (!hit && deviceIdentityOf(k) === id) hit = k;
      });
    }
    return hit || sessionSerial;
  }

  // The cache follows the immutable profile identity across USB/WiFi changes.
  var deviceIcons = SCEZAppIconCache.create(function (serial, pkg) {
    return window.GetAppIcon(serial, pkg);
  }, function (identity, pkgs) {
    var icons = {};
    function read(batch) {
      return window.GetAppIcons(identity, batch).then(function (result) {
        Object.keys((result && result.icons) || {}).forEach(function (pkg) { icons[pkg] = result.icons[pkg]; });
        if (result && result.remaining && result.remaining.length && result.remaining.length < batch.length) {
          return new Promise(function (resolve) { setTimeout(resolve, 0); }).then(function () { return read(result.remaining); });
        }
        return icons;
      });
    }
    return read(pkgs);
  });
  function iconIdentity(serial) {
    return (appWins[serial] && appWins[serial].identity) ||
           (sessions[serial] && sessions[serial].identity) || deviceIdentityOf(serial) || serial;
  }
  function requestIcon(serial, pkg, node) {
    return deviceIcons.request(iconIdentity(serial), serial, pkg, node);
  }

  // Backend appBusy is restricted to the first completely empty icon archive.
  function appEntryBusy(serial) {
    var identity = iconIdentity(serial);
    return ((lastState && lastState.devices) || []).some(function (d) {
      return !!d.appBusy && (d.identity || d.serial) === identity;
    });
  }

  function setAppEntryMask(button, busy) {
    if (!button) return;
    busy = !!busy;
    if (button._appEntryBusy === busy) return;
    button._appEntryBusy = busy;
    if (button._appEntryHTML === undefined) button._appEntryHTML = button.innerHTML;
    button.disabled = !!busy;
    button.style.opacity = busy ? '0.55' : '';
    button.innerHTML = busy ? '读取中…' : button._appEntryHTML;
  }

  function syncAppEntryMasks() {
    [sessions, appWins].forEach(function (entries) {
      Object.keys(entries).forEach(function (serial) {
        var pane = entries[serial].pane;
        if (pane) setAppEntryMask(pane.querySelector('.btn-appwin'), appEntryBusy(serial));
      });
    });
    // Cover a panel already opened during identity discovery or a stale snapshot.
    var w = appWinModalSerial && appWins[appWinModalSerial];
    if (w) {
      var busy = appEntryBusy(appWinModalSerial);
      if (!!w.initialIconBusy !== busy) {
        w.initialIconBusy = busy;
        renderAppWinGrid(appWinModalSerial);
      }
    }
  }

  var APPW_COLORS = ['#22C55E', '#FB7299', '#06B6D4', '#3B82F6', '#F59E0B', '#8B5CF6', '#EC4899', '#10B981', '#EF4444', '#6366F1', '#0EA5E9', '#64748B'];
  function appColor(key) {
    var h = 0;
    for (var i = 0; i < key.length; i++) { h = (h * 31 + key.charCodeAt(i)) >>> 0; }
    return APPW_COLORS[h % APPW_COLORS.length];
  }

  // ---------- 已打开的应用窗口卡片（v2.1.19 壳子档 → v2.1.27 走 bat 会话）----------
  // 数据：serial → [{pkg, name, bornAt?, closing?, closingAt?, leaving?}]
  //   closing = 停止已受理（停止按钮位置显示"正在关闭…"遮罩；快照为准，本地乐观）
  //   leaving = 离开动画中（淡出播完即真删——含"停止完成"/"用户手关窗口"）
  // 交互：浮窗里点应用 → 浮窗退出 + 卡片落到「应用窗口」按钮下方（同应用去重）。
  var openAppCards = {};
  // renderedPkgs：serial → {pkg:true}——entering 淡入动画只对"首次出现"的卡片播一次
  // （全量重建时不重复闪；卡片删卡后重新打开会再次播）。
  var renderedPkgs = {};
  // pendingRender：serial → true——拖拽进行中被跳过的渲染（v2.1.35），
  // pointerup 后补渲染一次（否则拖拽期间被删的卡片 DOM 会残留到下次渲染）。
  var pendingRender = {};
  // v2.1.60：应用投屏条退场状态（数据键 → true）——条目清空后设备页条子先播收缩
  // 动画（.leaving），420ms 后清标记再刷新（真移除；DOM 已收到 0 高度无跳动）。
  var appBarLeaving = {};
  // v2.1.60：设备页轻量防抖刷新（条子增删驱动；60ms 合并一轮内的多次变化）。
  var appBarRefreshTimer = null;
  // v2.1.60：renderedBars：设备键 → true——条子入场动画只对"首次出现"播一次。
  var renderedBars = {};

  // addOpenAppCard：把应用加入该设备卡片列表（已存在=去重不重复添加），返回是否新增。
  // bornAt：启动保护窗口（快照与 RPC 竞态时，不因"服务端暂未收录"而误判离开）。
  function addOpenAppCard(serial, pkg, name) {
    var list = openAppCards[serial] || (openAppCards[serial] = []);
    for (var i = 0; i < list.length; i++) {
      if (list[i].pkg === pkg) return false;
    }
    list.push({ pkg: pkg, name: name || pkg, bornAt: Date.now() });
    renderOpenAppCards(serial);
    return true;
  }

  // removeOpenAppCard：从卡片列表移除一个应用（离开动画结束/失败回滚；渲染刷新）。
  // v2.1.34：渲染经 vtRender 包裹——卡片淡出后，下方卡片平滑上移补位（不再瞬移，
  // 与设备页删除设备的 animateRefresh 同款 View Transitions）。
  function removeOpenAppCard(serial, pkg) {
    var list = openAppCards[serial];
    if (!list) return;
    for (var i = 0; i < list.length; i++) {
      if (list[i].pkg === pkg) {
        list.splice(i, 1);
        if (renderedPkgs[serial]) delete renderedPkgs[serial][pkg];
        if (list.length === 0) {
          delete openAppCards[serial];
          // v2.1.60：条子退场流——条目清空发生在快照变化之后（JSON 不再变，设备页
          // 等不到下一次刷新），必须由这里驱动：先播收缩动画，再真移除。
          markAppBarLeaving(serial);
        }
        vtRender(function () { renderOpenAppCards(serial); });
        return;
      }
    }
  }

  // markAppBarLeaving（v2.1.60）：应用投屏条退场流——条目清空后：先立即刷新设备页
  //（条子带 .leaving 类播收缩动画，卡片高度随之连续收回），480ms 后清标记再刷新
  //（条子真移除——此时 DOM 已收到 0 高度，移除无跳动）。
  // v2.1.62：480ms 对齐动画实际时长（60ms 调度 + 400ms 动画 = 460ms）——此前 420ms
  // 会在动画剩 40ms 处截断（条子收至 90% 被强移除）。
  function markAppBarLeaving(serial) {
    if (appBarLeaving[serial]) return;
    appBarLeaving[serial] = 'start'; // v2.1.67：'start'=待首渲（首渲后才转 'playing'）
    scheduleAppBarRefresh();
    setTimeout(function () {
      if (!appBarLeaving[serial]) return;
      delete appBarLeaving[serial];
      // v2.1.68：设备页内全关收尾——蓝灯页即时清理（主人场景：设备页点条子「全部关闭」，
      // 用户从未离开设备页 → clearAppWins/pruneEmptyAppWins 都不触发 → 空标签残留）。
      pruneAppWinPageOnDeviceView(serial);
      if (lastState) renderDevices(lastState);
    }, 480);
  }

  // scheduleAppBarRefresh（v2.1.60）：设备页轻量防抖刷新（条子增删驱动；60ms 合并
  // 一轮内的多次变化——避免逐条删除触发多次全量重建）。
  function scheduleAppBarRefresh() {
    if (appBarRefreshTimer) return;
    appBarRefreshTimer = setTimeout(function () {
      appBarRefreshTimer = null;
      if (lastState) renderDevices(lastState);
    }, 60);
  }

  // syncAppWins：用快照 appWins 校准卡片（后端=真相源）：
  //   服务器有 → 补卡 + 同步 closing（服务端为准，保留本地乐观窗口）；
  //   服务器没有 → 标记 leaving（淡出动画后真删——含"停止完成"/"用户手关窗口"）。
  // 保护窗口：刚点开的卡片（bornAt 5s 内）不因快照未收录而误判离开（RPC 竞态）。
  function syncAppWins(serverList) {
    var bySerial = {};
    (serverList || []).forEach(function (w) {
      (bySerial[w.serial] = bySerial[w.serial] || []).push(w);
    });
    var dirty = {};

    // ① 服务器有：补卡 / 同步 closing 状态。
    Object.keys(bySerial).forEach(function (serial) {
      bySerial[serial].forEach(function (w) {
        if (appWins[serial] && w.identity) appWins[serial].identity = w.identity;
        var list = openAppCards[serial] || (openAppCards[serial] = []);
        var a = null;
        for (var i = 0; i < list.length; i++) {
          if (list[i].pkg === w.pkg) { a = list[i]; break; }
        }
        if (!a) {
          list.push({ pkg: w.pkg, name: w.name || w.pkg, closing: !!w.closing, phase: w.phase || '', phaseText: w.phaseText || '', mode: w.mode || '', bornAt: Date.now() });
          dirty[serial] = true;
          return;
        }
        if (a.leaving) { a.leaving = false; dirty[serial] = true; } // 防御：条目复活
        // closing：服务端受理=true 立即同步；服务端 false（停止失败复位）等
        // 乐观窗口（4s）过后才清除——避免点击瞬间的旧快照把遮罩冲掉。
        if (w.closing) {
          if (!a.closing) { a.closing = true; dirty[serial] = true; }
        } else if (a.closing && (!a.closingAt || Date.now() - a.closingAt > 4000)) {
          a.closing = false;
          a.closingAt = 0;
          dirty[serial] = true;
        }
        // v2.1.54：动态状态文字（插拔转换/断线重连过程）——服务端=真相源；
        // 变化即标脏重渲染（卡片副行"正在窗口" ↔ 阶段文字）。
        var pt = w.phaseText || '';
        if ((a.phaseText || '') !== pt) { a.phaseText = pt; dirty[serial] = true; }
        // v2.1.70：阶段标记（restarting=参数重启间隙）——卡片保持、按钮换"重新连接中…"标签。
        var ph = w.phase || '';
        if ((a.phase || '') !== ph) { a.phase = ph; dirty[serial] = true; }
        // v2.1.57：连接形态同步（usb/wifi）——稳态文字追加" · 有线模式/ · 无线模式"。
        var md = w.mode || '';
        if ((a.mode || '') !== md) { a.mode = md; dirty[serial] = true; }
      });
    });

    // ② 服务器没有：标记离开（一次性定时后真删）。
    Object.keys(openAppCards).forEach(function (serial) {
      var srv = bySerial[serial] || [];
      openAppCards[serial].forEach(function (a) {
        if (srv.some(function (w) { return w.pkg === a.pkg; })) return;
        if (a.leaving) return; // 已在离开流程（动画中）
        if (a.bornAt && Date.now() - a.bornAt < 5000) return; // 启动保护窗口
        a.leaving = true;
        dirty[serial] = true;
        scheduleLeave(serial, a);
      });
    });

    Object.keys(dirty).forEach(function (serial) { renderOpenAppCards(serial); });
  }

  // scheduleLeave：离开动画播完即真删（350ms 动画 + 30ms 余量；一次性）。
  function scheduleLeave(serial, a) {
    setTimeout(function () { removeOpenAppCard(serial, a.pkg); }, 380);
  }

  // stopAllAppWins：给本设备全部应用窗口发关闭指令（v2.1.41「全部关闭」按钮）——
  // 逐个 StopAppWin（后端幂等受理）；乐观置 closing 遮罩（同单卡停止），单卡失败
  // 回滚该卡遮罩（快照校准兜底）。
  function stopAllAppWins(serial) {
    // v2.1.52：数据键归一（按钮绑定键=会话键；卡片数据键可能不同）。
    var dk = appKeyOf(serial);
    var list = (openAppCards[dk] || []).slice();
    if (list.length === 0) return;
    list.forEach(function (a) {
      a.closing = true;
      a.closingAt = Date.now();
    });
    renderOpenAppCards(dk); // 乐观：全部先显示"正在关闭…"
    list.forEach(function (a) {
      if (typeof window.StopAppWin === 'function') {
        window.StopAppWin(dk, a.pkg).catch(function (e) {
          toast('关闭失败：' + (e && e.message ? e.message : e));
          a.closing = false;
          a.closingAt = 0;
          renderOpenAppCards(dk);
        });
      }
    });
  }

  // attachAppCardDrag：应用卡片纵向拖拽换位（v2.1.35，照抄设备列表 gui44 的用法）。
  //   - 容器级绑定（监听器绑在容器，DOM 重建不失效；幂等——见 renderOpenAppCards）；
  //   - 只认带 data-pkg 的卡片：列表尾部的 hint 等无 key 元素不可拖（drag_order 的
  //     keyOf 判定），也不参与换位；closing/leaving 中的卡片不参与；
  //   - 拖动中的卡片置顶（.dragging 类 → z-index，CSS 同设备卡同款）；
  //   - 顺序=openAppCards[serial] 数组顺序（会话级内存态；不持久化，重启恢复快照序）。
  function attachAppCardDrag(box, serial) {
    DragOrder.attach(box, {
      axis: 'y',
      getOrder: function () {
        return (openAppCards[serial] || []).map(function (a) { return a.pkg; });
      },
      setOrder: function (next) {
        var list = openAppCards[serial] || [];
        var byPkg = {};
        list.forEach(function (a) { byPkg[a.pkg] = a; });
        openAppCards[serial] = next.map(function (k) { return byPkg[k]; }).filter(Boolean);
      },
      keyOf: function (el) {
        if (!el || !el.classList) return null;
        // closing/leaving 的卡片不参与拖拽（正在关闭/淡出的卡不折腾）
        if (el.classList.contains('closing') || el.classList.contains('leaving')) return null;
        return (el.dataset && el.dataset.pkg) ? el.dataset.pkg : null;
      },
      onDrop: null
    });
    // v2.1.35：拖拽期间被跳过的渲染（pendingRender）在此补一次。
    window.addEventListener('pointerup', function () {
      setTimeout(function () {
        if (pendingRender[serial]) {
          delete pendingRender[serial];
          // 延迟到落位动画（160ms）之后——立即重渲染会销毁旧节点、打断落位过渡
          setTimeout(function () { renderOpenAppCards(serial); }, 220);
        }
      }, 0);
    });
  }

  // renderOpenAppCards：把卡片列表渲染进该设备的容器（会话 pane / 蓝灯应用标签页
  // 都可能承载；容器缺失=no-op）。图标走 requestIcon 懒加载（与浮窗同一缓存）。
  // v2.1.27：entering 淡入（仅首次出现）/ leaving 淡出（状态类）/ closing 遮罩。
  function renderOpenAppCards(serial) {
    var list = openAppCards[serial] || [];
    var boxes = [];
    var s = sessions[serial];
    if (!s) {
      // v2.1.52：同设备多形态键——数据键（serial）与容器键（会话键）可能不同，身份归一。
      var sk = sessionKeyOf(serial);
      if (sk) s = sessions[sk];
    }
    if (s && s.pane) boxes.push(s.pane.querySelector('.openapp-list'));
    var w = appWins[serial];
    if (w && w.pane) boxes.push(w.pane.querySelector('.openapp-list'));
    var seen = renderedPkgs[serial] || (renderedPkgs[serial] = {});
    boxes.forEach(function (box, bi) {
      if (!box) return;
      // v2.1.35：拖拽进行中跳过重建（同设备页 gui44 的 drag-active 防御）——
      // 否则轮询刷新会在拖拽中销毁 activeEl 的 DOM 节点，拖拽错乱。标记 pending，
      // pointerup 后补渲染（拖拽期间被删的卡片 DOM 不残留）。
      if (box.classList.contains('drag-active')) {
        pendingRender[serial] = true;
        return;
      }
      // v2.1.35：容器级幂等绑定纵向拖拽（绑定在容器上、DOM 重建不失效；
      // pane 重建 → 标记随新 DOM 消失 → 自动重绑）。
      if (!box.dataset.dragBound) {
        box.dataset.dragBound = '1';
        attachAppCardDrag(box, serial);
      }
      box.innerHTML = '';
      // v2.1.41：「全部关闭」按钮显隐——本设备有应用窗口卡片时出现（右对齐同行）
      if (box.parentNode) {
        var allBtn = box.parentNode.querySelector('.btn-allstop');
        if (allBtn) allBtn.style.display = list.length > 0 ? '' : 'none';
      }
      if (list.length === 0) return;
      list.forEach(function (a) {
        var isNew = !seen[a.pkg];
        seen[a.pkg] = true;
        var card = document.createElement('div');
        card.className = 'openapp-card' +
          (isNew ? ' entering' : '') +
          (a.closing ? ' closing' : '') +
          (a.leaving ? ' leaving' : '');
        card.dataset.pkg = a.pkg; // 拖拽排序 key（v2.1.35）
        // v2.1.46：点击卡片 = 把对应应用窗口提到 z-order 前面（"拉上来"；浮前序列
        // 含 ez 顶回，最终 ez 仍在最前不被动摇）。拖拽（越阈值后吞 click）与卡片内
        // 按钮（stopPropagation）都不会误触发。
        card.addEventListener('click', function (ev) {
          ev.stopPropagation();
          if (typeof window.BringAppWinToFront === 'function') {
            try {
              var p = window.BringAppWinToFront(serial, a.pkg);
              if (p && typeof p.catch === 'function') p.catch(function () {});
            } catch (e) { /* 忽略：浮前失败不影响交互 */ }
          }
        });
        // v2.1.34：视口过渡命名（与设备卡 viewTransactionName 同款）——删除时下方
        // 卡片平滑上移补位（View Transitions 按名匹配同一张卡的位置变化）；
        // 前缀带容器序号，防两个容器（会话页/蓝灯页）同时存在时同名冲突。
        card.style.viewTransitionName = 'appw' + bi + '-' + vtSafeName(serial + '#' + a.pkg);
        if (isNew) {
          // v2.1.33：entering 类播完即移除（照抄会话进入动画的既有模式）。
          // 类留在元素上时，切页（view 的 display 切换）会让 CSS 动画重播——
          // 实测"最下面的应用卡片重复一次进入动画"。（340ms > 280ms 动画时长；
          // 中途重渲染时旧节点已被替换，对已脱离文档的节点 remove 无害。）
          setTimeout(function () { card.classList.remove('entering'); }, 340);
        }
        var icon = document.createElement('div');
        icon.className = 'openapp-icon';
        icon.style.backgroundColor = appColor(a.pkg || a.name || '?');
        icon.textContent = (a.name || '?').charAt(0);
        if (a.pkg) requestIcon(serial, a.pkg, icon);
        var title = document.createElement('div');
        title.className = 'openapp-title';
        var nm = document.createElement('span');
        nm.className = 'openapp-name';
        nm.textContent = a.name || a.pkg;
        var st = document.createElement('span');
        st.className = 'openapp-state';
        // v2.1.54：转换/重连中显示阶段文字（后端 phaseText，与主投屏同款措辞）；
        // v2.1.57：稳态追加连接形态（"正在窗口 · 有线模式/ · 无线模式"，与主投屏
        // modeTitle 措辞一致；形态未知/为空 → 只显示"正在窗口"）。纯函数 AppWinText
        // 单测覆盖（appwin_text_test.js）。
        st.textContent = AppWinText.cardStateText(a);
        title.appendChild(nm);
        title.appendChild(st);
        var actions = document.createElement('div');
        actions.className = 'openapp-actions';
        // v2.1.28：closing（停止受理 / 用户手关窗口，快照透传）→ 其余按钮直接
        // 消失，只留"正在关闭…"标签（v2.1.27 遮罩叠加在按钮上观感混乱）；
        // 真关完（条目摘除）→ 卡片淡出。
        if (a.closing) {
          var closingTag = document.createElement('div');
          closingTag.className = 'openapp-closing';
          closingTag.textContent = '正在关闭…';
          actions.appendChild(closingTag);
        } else if (a.phase === 'restarting') {
          // v2.1.70（主人拍板）：参数重启间隙——卡片保持存在（不消失不闪断），按钮换
          // 标签（防重启间隙误点停止）；新会话原地起来后 phase 清空、按钮自动恢复。
          var restartTag = document.createElement('div');
          restartTag.className = 'openapp-closing';
          restartTag.textContent = '重新连接中…';
          actions.appendChild(restartTag);
        } else {
          if (a.phase === 'retry-wait') {
            var bRetry = document.createElement('button');
            bRetry.type = 'button';
            bRetry.className = 'openapp-btn';
            bRetry.textContent = '重新投屏';
            bRetry.addEventListener('click', function (ev) {
              ev.stopPropagation();
              if (typeof window.RestartAppWin === 'function') {
                window.RestartAppWin(serial, a.pkg).catch(function (e) {
                  toast('重新投屏失败：' + (e && e.message ? e.message : e));
                });
              }
            });
            actions.appendChild(bRetry);
          }
          var bSet = document.createElement('button');
          bSet.type = 'button';
          bSet.className = 'openapp-btn';
          bSet.textContent = '设置';
          bSet.addEventListener('click', function (ev) { ev.stopPropagation(); openAppSetModal(serial, a.pkg); });
          var bStop = document.createElement('button');
          bStop.type = 'button';
          bStop.className = 'openapp-btn stop';
          bStop.textContent = '停止';
          bStop.addEventListener('click', function (ev) {
            ev.stopPropagation();
            // v2.1.27：停止=受理 → 遮罩（乐观置位，不等快照）→ bat 退出后卡片淡出；
            // 失败（RPC 报错）回滚遮罩（服务端失败复位场景由快照 4s 乐观窗口兜底）。
            a.closing = true;
            a.closingAt = Date.now();
            renderOpenAppCards(serial);
            if (typeof window.StopAppWin === 'function') {
              window.StopAppWin(serial, a.pkg).catch(function (e) {
                toast('停止失败：' + (e && e.message ? e.message : e));
                a.closing = false;
                a.closingAt = 0;
                renderOpenAppCards(serial);
              });
            }
          });
          actions.appendChild(bSet);
          actions.appendChild(bStop);
        }
        card.appendChild(icon);
        card.appendChild(title);
        card.appendChild(actions);
        box.appendChild(card);
      });
    });
  }

  // ensureAppWinPane：确保该设备的「应用窗口」蓝灯中间态页存在（幂等：已有=no-op）。
  // 蓝灯页=无投屏时应用卡片之家；投屏会话出现时被收编移除、会话退出时按需重建。
  // buildAppWinPane：构建蓝灯页 pane DOM（「应用窗口」入口 + 卡片容器）——
  // ensureAppWinPane（新建标签）与 startFade（会话标签原地复用为蓝灯）共用（v2.1.39）。
  function buildAppWinPane(serial) {
    var pane = document.createElement('div');
    pane.className = 'appwin-tab-pane';
    pane.id = 'appwintab-' + sanitizeId(serial);
    pane.innerHTML = APPWIN_ROW_HTML + OPENAPP_LIST_HTML;
    pane.querySelector('.btn-appwin').addEventListener('click', function (ev) {
      ev.stopPropagation();
      openAppWinModal(serial);
    });
    pane.querySelector('.btn-allstop').addEventListener('click', function (ev) {
      ev.stopPropagation();
      stopAllAppWins(serial);
    });
    // v2.1.40：按 serial 唯一命名——会话→蓝灯复用变身时按钮与卡片一起平滑上移补位
    pane.querySelector('.btn-appwin').style.viewTransitionName = 'appwbtn-' + vtSafeName(serial);
    return pane;
  }

  // ensureAppWinPane：确保该设备的「应用窗口」蓝灯中间态页存在（幂等：已有=no-op）。
  // 蓝灯页=无投屏时应用卡片之家；投屏会话出现时被收编移除、会话退出时按需重建。
  function ensureAppWinPane(serial) {
    var w = appWins[serial];
    if (!w || w.tab || w.pane) return;
    var name = w.name || serial;
    var tab = document.createElement('div');
    tab.className = 'casttab';
    tab.setAttribute('data-key', serial);
    tab.innerHTML = '<span class="casttab-dot appw"></span><span class="casttab-name"></span>';
    tab.querySelector('.casttab-name').textContent = name;
    tab.title = serial;
    // v2.1.66：动态分发（照抄会话标签的同款逻辑）——蓝灯标签会被「原地升级」复用为
    // 会话标签（v2.1.52 升级路径漏绑），此时按当前身份进会话；否则激活蓝灯页
    //（条目可能挂同设备另一形态键上，findAppKeyFor 归一）。此前固定绑
    // activateAppWinTab(蓝灯键)——升级后该键为收编态（tab/pane=null）→ 点标签
    // 全灰+空白（主人实测抓出）。
    tab.addEventListener('click', function () {
      var sk = sessions[serial] ? serial : sessionKeyOf(serial);
      if (sk) {
        activateSession(sk);
        // v2.1.88：补上与会话标签一致的「窗口浮前」——蓝灯标签被「原地升级」复用为
        // 会话标签后本 listener 仍在用（升级路径不重绑）：此前只切视图、漏了置顶
        //（主人实测：先应用窗口后主投屏，点标签不置顶）。
        if (typeof window.BringCastToFront === 'function') {
          try {
            var p = window.BringCastToFront(sk);
            if (p && typeof p.catch === 'function') p.catch(function () {});
          } catch (e) { /* 忽略：浮前失败不影响切换 */ }
        }
        return;
      }
      var ak = appWins[serial] ? serial : findAppKeyFor(serial);
      if (ak) activateAppWinTab(ak);
    });
    el('cast-tabbar').appendChild(tab);
    if (castOrder.indexOf(serial) < 0) castOrder.push(serial);
    w.tab = tab;

    var pane = buildAppWinPane(serial);
    el('cast-sessions').appendChild(pane);
    w.pane = pane;
    if (openAppCards[serial]) renderOpenAppCards(serial);
    updateTabs();
  }

  // openAppWin：应用入口总调度——懒建数据、维护蓝灯标签及其标签页(仅无投屏时)、
  // 跳到对应设备标签页（主人 0919 问题3）、打开选择浮窗。
  function openAppWin(serial, name) {
    if (appEntryBusy(serial)) return;
    serial = deviceIdentityOf(serial) || serial;
    var w = appWins[serial];
    if (!w) {
      // v2.1.52：同设备多形态键——先按 identity 找已有条目（防卡片数据分裂/双条目）。
      var ak = findAppKeyFor(serial);
      if (ak) {
        serial = ak;
        w = appWins[ak];
      }
    }
    if (!w) {
      var device = ((lastState && lastState.devices) || []).filter(function (d) { return d.serial === serial; })[0];
      w = appWins[serial] = { name: name || serial, tab: null, pane: null, apps: null, loaded: false, pollLeft: 40, identity: deviceIdentityOf(serial), identityEpoch: device ? (device.identityEpoch || 0) : 0 };
      if (!sessions[serial] && !sessionKeyOf(serial)) ensureAppWinPane(serial);
    } else {
      if (!w.identity) w.identity = deviceIdentityOf(serial);
      if (name) {
        w.name = name;
        if (w.tab) w.tab.querySelector('.casttab-name').textContent = name;
      }
    }
    w.pollLeft = 40; // 重新进入=重拉（防陈旧）
    loadAppWinApps(serial);
    // 主人 0919 问题3：点哪个设备就跳到哪个设备的标签页
    if (w.tab) { activateAppWinTab(serial); } else { activateSession(sessionKeyOf(serial) || serial); }
    openAppWinModal(serial);
  }

  // openAppWinModal：打开应用选择浮窗（模态；样式/交互同参数浮窗）。
  function openAppWinModal(serial) {
    if (appEntryBusy(serial)) return;
    var w = appWins[serial];
    if (!w) return;
    appWinModalSerial = serial;
    checkAppListSilently(serial);
    el('appwin-modal-title').textContent = (w.name || serial) + ' · 应用窗口';
    el('appwin-modal-search').value = '';
    renderAppWinGrid(serial);
    var m = el('appwin-modal');
    m.style.display = '';
    // v2.1.25：淡入 150ms（重触发：移除→强制 reflow→加回，保证每次打开都重播）
    m.classList.remove('fade-in');
    void m.offsetWidth;
    m.classList.add('fade-in');
  }

  function closeAppWinModal() {
    appWinModalSerial = null;
    el('appwin-modal').style.display = 'none';
  }

  // 浮窗交互（顶层绑定；同参数浮窗模式）：✕ / 点遮罩关闭；搜索过滤。
  el('appwin-modal-close').addEventListener('click', closeAppWinModal);
  el('appwin-modal').addEventListener('click', function (ev) {
    if (ev.target === el('appwin-modal')) closeAppWinModal();
  });
  el('appwin-modal-search').addEventListener('input', function () {
    if (appWinModalSerial) renderAppWinGrid(appWinModalSerial);
  });

  // activateAppWinTab：跳到蓝灯标签页（问题3）——会话标签/面板全退，显示该标签页
  //（含「应用窗口」按钮）并高亮蓝标签。
  function activateAppWinTab(serial) {
    var w = appWins[serial];
    if (!w) return;
    activeSerial = serial;
    Object.keys(sessions).forEach(function (k) {
      sessions[k].tab.classList.toggle('active', false);
      sessions[k].pane.style.display = 'none';
    });
    Object.keys(appWins).forEach(function (k) {
      if (appWins[k].tab) appWins[k].tab.classList.toggle('active', k === serial);
      if (appWins[k].pane) appWins[k].pane.style.display = k === serial ? '' : 'none';
    });
  }

  // switchToDeviceAppWin（v2.1.59）：跳转到该设备的应用窗口标签页（设备卡点击用）——
  // 主投屏缺席（只有应用投屏）时点设备卡=进入该设备蓝灯页；蓝灯页未建则先建
  //（懒建语义照抄 openAppWin / activateFirstActive：无会话时建页再激活）。
  function switchToDeviceAppWin(serial, name) {
    var w = appWins[serial];
    if (!w) {
      var ak = findAppKeyFor(serial, deviceIdentityOf(serial));
      if (ak) { serial = ak; w = appWins[ak]; }
    }
    if (!w) {
      w = appWins[serial] = {
        name: name || serial, tab: null, pane: null,
        apps: null, loaded: false, pollLeft: 0, identity: deviceIdentityOf(serial),
      };
    }
    if (!w.tab) ensureAppWinPane(serial);
    if (w.tab) activateAppWinTab(serial);
    switchView('cast');
  }

  // hideAppWinPanes：会话激活时隐藏蓝灯标签页（activateSession 内调用；无则 no-op）。
  function hideAppWinPanes() {
    Object.keys(appWins).forEach(function (k) {
      if (appWins[k].pane) appWins[k].pane.style.display = 'none';
      // v2.1.65：蓝灯标签一并去焦点——照抄 activateAppWinTab 的会话标签处理
      //（sessions[k].tab.classList.toggle('active', false) 同款）。此前只隐藏 pane、
      // active 类残留 → 非焦点的蓝灯标签仍是白底（主人实测抓出）。
      if (appWins[k].tab) appWins[k].tab.classList.toggle('active', false);
    });
  }

  // （hideAllAppWins 已随浮窗化移除：面板为模态浮层，与标签显隐无关联动。）

  // ---------- 声音档位滑条组件（v2.1.78：三档 phone/pc/both；appset 与 param 两处共用） ----------
  // 视觉：轨道 + 三档节点（位置纯 CSS，见 style.css .as-* 组）；交互：点击/拖动吸附最近档。
  var SCEZ_AUDIO_ORDER = ['phone', 'pc', 'both'];
  // 档名/副说明两处共用（v2.1.78；主人 0927 复核：应用窗口与主投屏三档行为一致——
  // 此前"虚拟屏应用手机端不出声"的观察是主投屏 output 采集把设备输出整体重定向所致，
  // 属测试环境干扰，非虚拟屏特性）。
  var SCEZ_AUDIO_LABELS = { phone: '手机', pc: '电脑', both: '两边' };
  var SCEZ_AUDIO_NOTES = {
    phone: '声音仅在手机上播放',
    pc: '声音仅在电脑上播放 · 手机静音',
    both: '电脑和手机同时播放声音'
  };

  function audioNote(v) { return SCEZ_AUDIO_NOTES[v] || SCEZ_AUDIO_NOTES.phone; }

  // renderAudioSlider：渲染当前档（高亮节点/标签）。纯 CSS 定位，无需测量宽度、无进度条填充。
  function renderAudioSlider(node, value) {
    if (!node) return;
    var idx = SCEZ_AUDIO_ORDER.indexOf(value);
    if (idx < 0) idx = 0;
    node.__audioVal = SCEZ_AUDIO_ORDER[idx];
    node.querySelectorAll('.as-stop').forEach(function (n) { n.classList.toggle('act', +n.dataset.i === idx); });
    node.querySelectorAll('.as-label').forEach(function (n) { n.classList.toggle('act', +n.dataset.i === idx); });
  }

  // bindAudioSlider：一次性绑定交互（点击/拖动吸附三档；cb(value) 通知外部）。
  function bindAudioSlider(node, cb) {
    if (!node || node.__audioBound) return;
    node.__audioBound = true;
    var dragging = false;
    function pick(ev) {
      var r = node.getBoundingClientRect();
      if (!r.width) return;
      var p = (ev.clientX - r.left) / r.width;
      var i = p < 0.25 ? 0 : (p < 0.75 ? 1 : 2);
      var v = SCEZ_AUDIO_ORDER[i];
      if (node.__audioVal !== v) { node.__audioVal = v; cb(v); }
    }
    node.addEventListener('pointerdown', function (ev) {
      dragging = true;
      try { node.setPointerCapture(ev.pointerId); } catch (e) {}
      pick(ev);
    });
    node.addEventListener('pointermove', function (ev) { if (dragging) pick(ev); });
    node.addEventListener('pointerup', function () { dragging = false; });
    node.addEventListener('pointercancel', function () { dragging = false; });
  }

  // ---------- 应用窗口设置浮窗（二期 Step 4/5：虚拟屏参数——每应用有线/无线两套） ----------
  // 与参数浮窗（param-modal）同构：四栏档位（分辨率/刷新率/码率/界面密度）+ 每栏重置 +
  // ＋自定义；开关=窗口跟随/静音；「保存并重新投屏」=写档案 + 重启会话（生效）。
  // fixed = 固定档序列；实际档位 = buildTiers(设备默认值, fixed)（v2.1.48：
  // 默认档共享主投屏规格，随设备变化——插档保证默认值始终可选中）。
  // v2.1.69：界面密度（dpi）栏——「自动」=等比公式（0，恒排最左）+ 固定档 + 自定义；
  // 显示「自动 ≈NNN」换算值（主屏参数由 GetAppWinParams 带出，前端同公式现算）。
  // v2.1.74（主人拍板）：分辨率档与主投屏 res 栏同口径——**只显示长边数字**，
  // 短边按设备宽高比由后端换算（mdns10ProfileRes；虚拟屏注入仍是完整 WxH）。
  var APPSET_FIELDS = {
    size: { label: '分辨率档', unit: '', fixed: [2560, 1920, 1080, 720] },
    fps: { label: '刷新率上限', unit: ' fps', fixed: [120, 90, 60, 30] },
    bit: { label: '码率上限', unit: ' Mbps', fixed: [32, 16, 8, 4] },
    dpi: { label: '界面密度', unit: '', isDpi: true, fixed: [160, 240, 320, 480] }
  };
  var APPSET_NOTES = {
    size: '分辨率说明：长边由档位/自定义值决定；比例框留空时跟随设备，填写宽 × 高后按该比例计算短边。开启「窗口跟随」后，调整窗口大小会同步改变分辨率',
    fps: '刷新率说明：虚拟屏会话的实际传输帧数上限；画面静止时降频属系统省电行为',
    bit: '码率说明：画面码率上限，越大越清晰、占用带宽越高；无线下建议低档',
    dpi: '界面密度说明：数值越大，字和图标越大、一屏显示的内容越少；「自动」按设备屏幕等比适配',
    audio: '声音说明：手机＝只在手机出声（不传输到电脑）；电脑＝只在电脑出声（手机静音；手机音量键同步控制投屏音量）；两边＝电脑与手机同时出声（需 Android 13+；两边音量相互独立——投屏音量请用电脑调）',
    vcodec: '视频编码说明：H.264 兼容性最好（默认）；H.265 同画质更省带宽；AV1/VP8/VP9 多数设备仅有软编码器，可能不流畅——自选使用',
    acodec: '音频编码说明：Opus（默认）/ AAC / FLAC / RAW——实际支持取决于设备端编码器；兼容性 Opus 最佳'
  };
  var APPSET_ORDER = ['size', 'fps', 'bit', 'dpi'];
  var appSetState = null; // {serial,pkg,name,mode,flex,mute,activeField,physDpi,physLong,tiers,fields}

  // openAppSetModal：打开窗口设置浮窗（拉档案 → 按当前形态那套初始化）。
  function openAppSetModal(serial, pkg) {
    var name = pkg;
    (openAppCards[serial] || []).forEach(function (a) { if (a.pkg === pkg) name = a.name || pkg; });
    if (typeof window.GetAppWinParams !== 'function') return;
    window.GetAppWinParams(serial, pkg).then(function (v) {
      var mode = (v && v.mode === 'wifi') ? 'wifi' : 'usb';
      var cur = (v && (mode === 'wifi' ? v.wifi : v.usb)) || {};
      var defRaw = (v && (mode === 'wifi' ? v.wifiDef : v.usbDef)) || {};
      var isSet = mode === 'wifi' ? !!(v && v.wifiSet) : !!(v && v.usbSet);
      // 设备默认档（v2.1.48 共享主投屏规格；缺失回退 1280/60/8；dpi 恒「自动」）。
      // v2.1.74：size=长边数字（后端已归一化；parseInt 顺带兼容旧 WxH 文本）。
      var def = {
        size: parseInt(defRaw.size, 10) || 1280,
        fps: defRaw.fps || 60,
        bit: defRaw.bitrate || 8,
        dpi: 0
      };
      var tiers = {
        size: SCEZAppSetState.buildTiers(def.size, APPSET_FIELDS.size.fixed),
        fps: SCEZAppSetState.buildTiers(def.fps, APPSET_FIELDS.fps.fixed),
        bit: SCEZAppSetState.buildTiers(def.bit, APPSET_FIELDS.bit.fixed),
        // dpi：「自动」（0）恒排最左，其后固定档降序（与「默认档在最左」的既有习惯一致）。
        dpi: [0].concat(SCEZAppSetState.buildTiers(null, APPSET_FIELDS.dpi.fixed))
      };
      function field(f) {
        var val = f === 'size' ? (parseInt(cur.size, 10) || def.size)
          : (f === 'fps' ? (cur.fps || def.fps)
          : (f === 'bit' ? (cur.bitrate || def.bit) : (cur.dpi || 0)));
        return SCEZAppSetState.infer({ val: val, base: def[f], custom: isSet, edit: false, input: '' }, tiers[f]);
      }
      appSetState = {
        serial: serial, pkg: pkg, name: name, mode: mode,
        ratioW: cur.ratioW ? String(cur.ratioW) : '',
        ratioH: cur.ratioH ? String(cur.ratioH) : '',
        flex: cur.flex === undefined ? true : !!cur.flex,
        audio: cur.audio || 'phone',
        // v2.1.95：编码格式（当前形态；显示生效值——应用级 ?? 设备级 ?? 默认）。
        // own 语义：应用级已有值 或 本次点过 chip → 保存时固化；否则保存发空 =
        // 保持未设置（重启时跟随设备档案；避免动其它参数时隐性固化编码）。
        vcodec: (mode === 'wifi' ? v.wifiVCodec : v.usbVCodec) || 'h264',
        acodec: (mode === 'wifi' ? v.wifiACodec : v.usbACodec) || 'opus',
        ownVCodec: !!cur.vcodec,
        ownACodec: !!cur.acodec,
        // v2.1.80：ABR 锁定（当前形态那套的开关状态）
        lockFps: !!cur.lockFps,
        lockBitrate: !!cur.lockBitrate,
        activeField: 'size',
        tiers: tiers,
        physDpi: (v && v.physDpi) || 0,
        physLong: (v && v.physLong) || 0,
        fields: { size: field('size'), fps: field('fps'), bit: field('bit'), dpi: field('dpi') }
      };
      renderAppSetModal();
      var m = el('appset-modal');
      m.style.display = '';
      // 淡入（同应用面板浮窗：移除→强制 reflow→加回，保证每次打开都重播）
      m.classList.remove('fade-in');
      void m.offsetWidth;
      m.classList.add('fade-in');
    }).catch(function (e) {
      toast('读取窗口设置失败：' + (e && e.message ? e.message : e));
    });
  }

  // setSwitchNode：绿/灰滑块开关渲染（同主设置面板 setSwitch 模式）。
  function setSwitchNode(node, on) {
    if (!node) return;
    node.classList[on ? 'add' : 'remove']('on');
    node.setAttribute('aria-checked', on ? 'true' : 'false');
  }

  function renderAppSetModal() {
    if (!appSetState) return;
    var modeName = appSetState.mode === 'usb' ? '有线参数' : '无线参数';
    el('appset-title').textContent = appSetState.name + ' · 窗口设置 · ' + modeName;
    setSwitchNode(el('appset-flex'), appSetState.flex);
    renderAudioSlider(el('appset-audio'), appSetState.audio);
    el('appset-audio-note').textContent = audioNote(appSetState.audio);
    renderAppSetRows();
    updateAppsetCodecRows(); // v2.1.95：编码行刷新（静态行在 #appset-rows 外）
  }

  function closeAppSetRatioError() { el('appset-ratio-error-modal').style.display = 'none'; }
  el('appset-ratio-error-close').addEventListener('click', closeAppSetRatioError);
  el('appset-ratio-error-ok').addEventListener('click', closeAppSetRatioError);
  el('appset-ratio-error-modal').addEventListener('click', function (ev) {
    if (ev.target === el('appset-ratio-error-modal')) closeAppSetRatioError();
  });

  // v2.1.95：编码格式行（视频/音频）——照抄主弹窗 updateParamCodecRows 模式；
  // 静态行放 #appset-rows 容器外（renderAppSetRows 重建内部不影响），随弹窗渲染刷新。
  function updateAppsetCodecRows() {
    if (!appSetState) return;
    var v = appSetState.vcodec || 'h264';
    var a = appSetState.acodec || 'opus';
    el('appset-vcodec-label').innerHTML = '视频编码 (<b>' + fmtVCodec(v) + '</b>)';
    el('appset-acodec-label').innerHTML = '音频编码 (<b>' + fmtACodec(a) + '</b>)';
    renderCodecChips('appset-vcodec-chips', VCODEC_OPTS, v, function (nv) {
      appSetState.vcodec = nv;
      appSetState.ownVCodec = true;
      appSetState.activeField = 'vcodec'; // 联动底部说明区
      renderAppSetModal();
    }, fmtVCodec);
    renderCodecChips('appset-acodec-chips', ACODEC_OPTS, a, function (na) {
      appSetState.acodec = na;
      appSetState.ownACodec = true;
      appSetState.activeField = 'acodec'; // 联动底部说明区
      renderAppSetModal();
    }, fmtACodec);
  }

  // v2.1.96：重置按钮通用交互（flash 反馈 + 动作回调）——编码/声音行六处共用；
  // 模式与四栏「重置」一致（stopPropagation 防触发行点击说明；点击动画重播）。
  function bindResetFlash(id, onReset) {
    var btn = el(id);
    if (!btn) return;
    btn.addEventListener('click', function (ev) {
      ev.stopPropagation();
      var b = ev.currentTarget;
      b.classList.remove('flash');
      void b.offsetWidth; // 重启动画
      b.classList.add('flash');
      setTimeout(function () { b.classList.remove('flash'); }, 220);
      onReset();
    });
  }

  // appSetAutoDpi：界面密度「自动」档的等比换算值（主屏dpi × 虚拟屏长边 ÷ 主屏长边，
  // 与 Go 端 calcVdDpi 同公式）——面板显示「自动 ≈NNN」用，随所选尺寸实时变化。
  // 设备物理参数未缓存（physDpi/physLong=0）→ 0（只显示「自动」；缓存由后台预热补上）。
  function appSetAutoDpi() {
    if (!appSetState || !appSetState.physDpi || !appSetState.physLong) return 0;
    var sz = appSetState.fields.size || {};
    // v2.1.74：size=长边数字（parseInt 顺带兼容旧 "WxH" 文本，取到长边）。
    var le = parseInt(String((sz.edit && sz.input) || sz.val || ''), 10);
    if (!(le > 0)) return 0;
    return Math.round(appSetState.physDpi * le / appSetState.physLong);
  }

  // renderAppSetRows：四栏档位渲染（照 renderParamRows 模式；size 栏=字符串档 + 分段双输入；
  // dpi 栏=「自动」文字档 + 数字档 + 自定义）。
  function renderAppSetRows() {
    var box = el('appset-rows');
    box.innerHTML = '';
    APPSET_ORDER.forEach(function (f) {
      var fd = APPSET_FIELDS[f];
      var st = appSetState.fields[f];
      var valText;
      if (fd.isDpi) {
        // 界面密度：「自动 ≈NNN」（phys 未缓存时只显示「自动」）；自定义值带单位。
        var auto = appSetAutoDpi();
        valText = st.val > 0 ? (st.val + ' DPI') : ('自动' + (auto > 0 ? ' ≈' + auto : ''));
      } else {
        valText = st.val + fd.unit;
      }
      // v2.1.80：刷新率/码率栏加「锁定」按钮（应用窗口同样支持——ABR 同链）
      var lockable = (f === 'fps' || f === 'bit');
      var isLocked = lockable && (f === 'fps' ? !!appSetState.lockFps : !!appSetState.lockBitrate);
      var row = document.createElement('div');
      row.className = 'param-row';
      row.innerHTML =
        '<div class="param-rowhead">' +
        '<span class="param-label">' + fd.label + ' (<b>' + valText + '</b>)</span>' +
        '<span class="param-actions">' +
        (lockable ? '<button class="param-lock' + (isLocked ? ' on' : '') + '">' + (isLocked ? '已锁定' : '锁定') + '</button>' : '') +
        '<button class="param-reset">重置</button>' +
        '</span>' +
        '</div>' +
        '<div class="chips"></div>';
      var chips = row.querySelector('.chips');
      (appSetState.tiers[f] || []).forEach(function (t) {
        var c = document.createElement('button');
        c.className = 'chip' + (st.val === t ? ' sel' : '');
        c.textContent = (fd.isDpi && t === 0) ? '自动' : String(t);
        c.addEventListener('click', function () {
          appSetState.fields[f] = SCEZAppSetState.clickTier(appSetState.fields[f], t);
          renderAppSetModal();
        });
        chips.appendChild(c);
      });
      if (st.edit) {
        // 自定义输入（各栏统一数字口径；size=长边像素数）——实时记文本、change 提交。
        var inp = document.createElement('input');
        inp.type = 'number';
        inp.min = '1';
        inp.className = 'chip-input';
        inp.placeholder = '输入后生效';
        inp.value = st.input;
        inp.addEventListener('input', function () {
          appSetState.fields[f] = SCEZAppSetState.typeCustom(appSetState.fields[f], inp.value);
        });
        inp.addEventListener('change', function () {
          appSetState.fields[f] = SCEZAppSetState.commitCustom(appSetState.fields[f], inp.value);
          renderAppSetModal();
        });
        chips.appendChild(inp);
      } else {
        var cb = document.createElement('button');
        cb.className = 'chip dashed';
        cb.textContent = '＋自定义';
        cb.addEventListener('click', function () {
          appSetState.fields[f] = SCEZAppSetState.startCustom(appSetState.fields[f]);
          renderAppSetModal();
        });
        chips.appendChild(cb);
      }
      if (f === 'size') {
        var ratioPair = document.createElement('span');
        ratioPair.className = 'appset-ratio-pair';
        ratioPair.setAttribute('aria-label', '初始比例，留空时跟随设备');
        var ratioW = document.createElement('input');
        ratioW.type = 'text';
        ratioW.inputMode = 'numeric';
        ratioW.maxLength = 5;
        ratioW.className = 'chip-input';
        ratioW.placeholder = '宽';
        ratioW.setAttribute('aria-label', '比例宽');
        ratioW.value = appSetState.ratioW;
        ratioW.addEventListener('input', function () { appSetState.ratioW = ratioW.value; });
        var ratioX = document.createElement('span');
        ratioX.className = 'appset-ratio-times';
        ratioX.textContent = '×';
        var ratioH = document.createElement('input');
        ratioH.type = 'text';
        ratioH.inputMode = 'numeric';
        ratioH.maxLength = 5;
        ratioH.className = 'chip-input';
        ratioH.placeholder = '高';
        ratioH.setAttribute('aria-label', '比例高');
        ratioH.value = appSetState.ratioH;
        ratioH.addEventListener('input', function () { appSetState.ratioH = ratioH.value; });
        ratioPair.appendChild(ratioW);
        ratioPair.appendChild(ratioX);
        ratioPair.appendChild(ratioH);
        chips.appendChild(ratioPair);
      }
      row.addEventListener('click', function () {
        appSetState.activeField = f;
        el('appset-note').textContent = APPSET_NOTES[f];
      });
      var lockBtn = row.querySelector('.param-lock');
      if (lockBtn) {
        lockBtn.addEventListener('click', function (ev) {
          ev.stopPropagation();
          // 锁定切换（独立于值；保存时随 payload 落档）
          if (f === 'fps') {
            appSetState.lockFps = !appSetState.lockFps;
          } else {
            appSetState.lockBitrate = !appSetState.lockBitrate;
          }
          renderAppSetModal();
        });
      }
      row.querySelector('.param-reset').addEventListener('click', function (ev) {
        ev.stopPropagation();
        var btn = ev.currentTarget;
        btn.classList.remove('flash');
        void btn.offsetWidth; // 重启动画
        btn.classList.add('flash');
        setTimeout(function () { btn.classList.remove('flash'); }, 220);
        if (f === 'size') {
          var reset = SCEZAppSetState.resetSizeAndRatio(appSetState.fields.size);
          appSetState.fields.size = reset.size;
          appSetState.ratioW = reset.ratioW;
          appSetState.ratioH = reset.ratioH;
        } else {
          appSetState.fields[f] = SCEZAppSetState.resetField(appSetState.fields[f]);
        }
        renderAppSetModal();
      });
      box.appendChild(row);
    });
    el('appset-note').textContent = APPSET_NOTES[appSetState.activeField || 'size'];
  }

  // v2.1.95：编码行点击 → 底部说明栏（照抄主弹窗 param-vcodec-row 绑定模式）。
  el('appset-vcodec-row').addEventListener('click', function () {
    if (!appSetState) return;
    appSetState.activeField = 'vcodec';
    el('appset-note').textContent = APPSET_NOTES.vcodec;
  });
  el('appset-acodec-row').addEventListener('click', function () {
    if (!appSetState) return;
    appSetState.activeField = 'acodec';
    el('appset-note').textContent = APPSET_NOTES.acodec;
  });
  // 浮窗交互（顶层绑定；同参数浮窗模式）：✕ / 取消 / 点遮罩关闭。
  el('appset-close').addEventListener('click', function () { el('appset-modal').style.display = 'none'; });
  el('appset-cancel').addEventListener('click', function () { el('appset-modal').style.display = 'none'; });
  el('appset-modal').addEventListener('click', function (ev) {
    if (ev.target === el('appset-modal')) el('appset-modal').style.display = 'none';
  });
  // 开关：本地翻转 + 即渲染（保存时提交）。声音滑条同：变更即渲染（保存时提交）。
  el('appset-flex').addEventListener('click', function () {
    if (!appSetState) return;
    appSetState.flex = !appSetState.flex;
    renderAppSetModal();
  });
  bindAudioSlider(el('appset-audio'), function (v) {
    if (!appSetState) return;
    appSetState.audio = v;
    // 只刷声音行（滑条高亮/说明）——不重建面板（拖动流畅；与 param 侧同模式）
    renderAudioSlider(el('appset-audio'), appSetState.audio);
    el('appset-audio-note').textContent = audioNote(appSetState.audio);
  });
  // v2.1.78：点声音行 → 底部说明栏显示声音说明（含音量联动差异；同其它栏位交互）。
  el('appset-audio-row').addEventListener('click', function () {
    if (!appSetState) return;
    appSetState.activeField = 'audio';
    el('appset-note').textContent = APPSET_NOTES.audio;
  });
  // v2.1.96：编码/声音行「重置」——跳默认初始值（H.264 / Opus / 手机）；
  // 编码重置=明确设置默认值（own=true，保存后固化，重开弹窗显示一致）。
  bindResetFlash('appset-vcodec-reset', function () {
    if (!appSetState) return;
    appSetState.vcodec = 'h264';
    appSetState.ownVCodec = true;
    renderAppSetModal();
  });
  bindResetFlash('appset-acodec-reset', function () {
    if (!appSetState) return;
    appSetState.acodec = 'opus';
    appSetState.ownACodec = true;
    renderAppSetModal();
  });
  bindResetFlash('appset-audio-reset', function () {
    if (!appSetState) return;
    appSetState.audio = 'phone';
    renderAudioSlider(el('appset-audio'), appSetState.audio);
    el('appset-audio-note').textContent = audioNote(appSetState.audio);
  });
  // v2.1.74（主人拍板）：底部「恢复默认」按钮已删——每栏有独立「重置」，冗余。
  // 保存并重新投屏：结算四栏 + 开关 → 写档案 + 重启会话（立即反馈：先关面板+提示）。
  el('appset-save').addEventListener('click', function () {
    if (!appSetState) return;
    var s = appSetState;
    var rSize = SCEZAppSetState.settle(s.fields.size);
    var rFps = SCEZAppSetState.settle(s.fields.fps);
    var rBit = SCEZAppSetState.settle(s.fields.bit);
    var rDpi = SCEZAppSetState.settle(s.fields.dpi);
    var ratio = SCEZAppSetState.parseRatio(s.ratioW, s.ratioH);
    if (!ratio.ok) {
      el('appset-ratio-error-title').textContent = ratio.reason === 'incomplete' ? '初始比例未输入完成' : '初始比例不合法';
      el('appset-ratio-error-message').textContent = ratio.reason === 'incomplete'
        ? '宽和高需要同时填写，请补全两个输入框。'
        : '宽和高须为 1–10000 的正整数。请修改后再保存。';
      el('appset-ratio-error-modal').style.display = '';
      return;
    }
    // v2.1.74：size=长边数字 → 字符串（对齐后端 string 字段；后端按设备宽高比换算 WxH 注入）。
    var payload = JSON.stringify({
      mode: s.mode, size: String(rSize.value), fps: rFps.value, bitrate: rBit.value,
      ratioW: ratio.width, ratioH: ratio.height,
      dpi: rDpi.value, flex: s.flex, audio: s.audio,
      lockFps: !!s.lockFps, lockBitrate: !!s.lockBitrate,
      // v2.1.95：编码（未持有且未改动 → 空 = 保持继承设备档案；否则固化当前值）
      vcodec: s.ownVCodec ? s.vcodec : '',
      acodec: s.ownACodec ? s.acodec : ''
    });
    el('appset-modal').style.display = 'none';
    toast('已保存，正在重新投屏…');
    if (typeof window.SaveAppWinParams !== 'function') return;
    window.SaveAppWinParams(s.serial, s.pkg, payload).then(function () {
      refreshNow();
    }).catch(function (e) {
      toast('保存失败：' + (e && e.message ? e.message : e));
    });
  });

  // clearAppWins：丢弃全部「应用选择」中间态（蓝灯标签+面板）——
  // 主人 0919：蓝灯是中间态；未选任何应用就回设备页 = 撤销本次选择，全部清掉。
  // （将来"已开应用窗口"的标签另行保留策略，见后续步骤。）
  function clearAppWins() {
    closeAppWinModal(); // 中间态浮窗一并关闭（幂等）
    var had = Object.keys(appWins);
    if (had.length === 0) return;
    had.forEach(function (serial) {
      // v2.1.19：已添加应用卡片的设备=用户已完成选择 → 蓝灯保留（不再算"未选任何应用"）
      if ((openAppCards[serial] || []).length > 0) return;
      var w = appWins[serial];
      if (w.tab && w.tab.parentNode) w.tab.parentNode.removeChild(w.tab);
      if (w.pane && w.pane.parentNode) w.pane.parentNode.removeChild(w.pane);
      delete appWins[serial];
    });
    if (activeSerial && had.indexOf(activeSerial) >= 0 && !appWins[activeSerial]) activeSerial = null;
    updateTabs();
  }

  // pruneEmptyAppWins：清理"孤儿空蓝灯页"（v2.1.29）——真的有页面（tab/pane）、
  // 无会话、无应用卡片的蓝灯页。典型场景：用户在设备页期间卡片收尾清空（"正在
  // 关闭"→淡出），标签失去清理时机（clearAppWins 只跑在切去设备页那一刻），
  // 切回窗口总览页时残留一张空标签。收编态（tab/pane 均为 null 或有会话）不动。
  function pruneEmptyAppWins() {
    var removed = false;
    Object.keys(appWins).forEach(function (serial) {
      var w = appWins[serial];
      if (!w || (!w.tab && !w.pane)) return;              // 收编态/无页 → 不动
      if (sessions[serial]) return;                       // 有会话 → 不动
      if ((openAppCards[serial] || []).length > 0) return; // 有卡片 → 保留
      if (w.tab && w.tab.parentNode) w.tab.parentNode.removeChild(w.tab);
      if (w.pane && w.pane.parentNode) w.pane.parentNode.removeChild(w.pane);
      delete appWins[serial];
      removed = true;
    });
    if (removed) updateTabs();
  }

  // pruneAppWinPageOnDeviceView（v2.1.68）：设备页内全关后的蓝灯页即时清理——
  // 主人拍板场景：在设备页点条子「全部关闭」→ 应用全退、条子消失，但用户从未离开
  // 设备页（clearAppWins 不触发）、也没经设备卡进过窗口总览（pruneEmptyAppWins 不
  // 触发）→ 残留空蓝灯页，下次进窗口总览才被看见。清理判据对照 pruneEmptyAppWins：
  // ①当前停在设备页（窗口总览页时不动作——保持"回设备页才清"的原设计）；
  // ②该设备无会话（归一判定）且无卡片——直接摘除残留空蓝灯页。
  function pruneAppWinPageOnDeviceView(dataKey) {
    var devTab = document.querySelector('.tab[data-view="devices"]');
    if (!devTab || !devTab.classList.contains('active')) return; // 仅设备页生效
    var wk = appWins[dataKey] ? dataKey : findAppKeyFor(dataKey);
    var w = wk ? appWins[wk] : null;
    if (!w || (!w.tab && !w.pane)) return;                // 收编态/无页 → 不动
    if (sessionKeyOf(wk)) return;                         // 有会话 → 不动
    if ((openAppCards[dataKey] || []).length > 0) return; // 还有卡片 → 保留
    if (w.tab && w.tab.parentNode) w.tab.parentNode.removeChild(w.tab);
    if (w.pane && w.pane.parentNode) w.pane.parentNode.removeChild(w.pane);
    delete appWins[wk];
    updateTabs();
  }

  // castHasContent：该设备在窗口总览页是否有内容——有会话（投屏中/结束态未清）
  // 或有应用窗口卡片。空蓝灯页（无卡片）=无内容。
  function castHasContent(serial) {
    if (!serial) return false;
    // v2.1.52：同设备多形态键——会话/数据都经身份归一再判定。
    if (sessions[serial] || sessionKeyOf(serial)) return true;
    if ((openAppCards[appKeyOf(serial)] || []).length > 0) return true;
    return false;
  }

  // ensureCastActive：激活修正（v2.1.29）——切回窗口总览页时，当前激活标签已无
  // 内容（设备清空后停在空页）→ 自动切到仍有内容的标签：活动会话优先，其次有
  // 应用卡片的蓝灯页；都没有则保持空态。仅切页时机调用（不在轮询里跑），
  // 避免破坏"用户站在空标签上准备开应用"的场景。
  function ensureCastActive() {
    if (castHasContent(activeSerial)) return;
    var pick = null;
    Object.keys(sessions).forEach(function (k) {
      if (!pick && castHasContent(k)) pick = k;
    });
    if (!pick) {
      Object.keys(openAppCards).forEach(function (k) {
        if (!pick && castHasContent(k)) pick = k;
      });
    }
    if (!pick) {
      activeSerial = null;
      updateTabs();
      return;
    }
    if (sessions[pick]) {
      activateSession(pick);
    } else if (appWins[pick] && appWins[pick].pane) {
      activateAppWinTab(pick);
    } else {
      activeSerial = null;
      updateTabs();
    }
  }

  // loadAppWinApps：拉应用列表（RPC：内存缓存→档案回退）；非空即停轮询。
  function loadAppWinApps(serial) {
    var w = appWins[serial];
    if (!w) return;
    if (typeof window.GetAppList !== 'function') { w.loaded = true; renderAppWinGrid(serial); return; }
    window.GetAppList(serial).then(function (items) {
      items = items || [];
      var changed = !sameAppList(w.apps, items);
      if (changed) invalidateAppIconsForChanges(serial, w.apps, items);
      if (w.loaded) {
        if (!changed) return;
        w.apps = items;
      } else if (items.length > 0) {
        w.apps = items;
        w.loaded = true;
        w.pollLeft = 0;
      } else {
        renderAppWinGrid(serial);
        return;
      }
      renderAppWinGrid(serial);
    }).catch(function () { renderAppWinGrid(serial); });
  }

  function sameAppList(a, b) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (a[i].pkg !== b[i].pkg || a[i].name !== b[i].name || !!a[i].sys !== !!b[i].sys) return false;
    }
    return true;
  }

  function invalidateAppIconsForChanges(serial, oldItems, newItems) {
    var oldByPkg = {}, newByPkg = {};
    (oldItems || []).forEach(function (item) { oldByPkg[item.pkg] = item; });
    (newItems || []).forEach(function (item) { newByPkg[item.pkg] = item; });
    Object.keys(oldByPkg).concat(Object.keys(newByPkg)).forEach(function (pkg) {
      var old = oldByPkg[pkg], current = newByPkg[pkg];
      if (!old || !current || old.name !== current.name) deviceIcons.invalidate(iconIdentity(serial), pkg);
    });
  }

  // 点击入口的静默检测与图标同步均在后台运行；只有实际列表变化才替换数据/重绘。
  function checkAppListSilently(serial) {
    var w = appWins[serial];
    if (!w || w.listCheckPolling || typeof window.CheckAppList !== 'function' ||
        typeof window.IsAppListCheckBusy !== 'function') return;
    w.listCheckPolling = true;
    window.CheckAppList(serial).then(function (watching) {
      if (!watching) { w.listCheckPolling = false; return; }
      var token = (w.listCheckToken || 0) + 1;
      w.listCheckToken = token;
      function finish() {
        if (w.listCheckToken === token) w.listCheckPolling = false;
      }
      function poll() {
        if (appWins[serial] !== w || w.listCheckToken !== token) { finish(); return; }
        window.IsAppListCheckBusy(serial).then(function (status) {
          if (appWins[serial] !== w || w.listCheckToken !== token) { finish(); return; }
          if (!!w.initialIconBusy !== !!(status && status.initialIconBusy)) {
            w.initialIconBusy = !!(status && status.initialIconBusy);
            renderAppWinGrid(serial);
          }
          if (status && status.busy) { setTimeout(poll, 900); return; }
          if (status && status.icons && status.icons.length) {
            deviceIcons.refresh(w.identity || iconIdentity(serial), status.icons);
          }
          if (!status || !status.changed) { finish(); return; }
          return window.GetAppList(serial).then(function (items) {
            items = items || [];
            if (!sameAppList(w.apps, items)) {
              invalidateAppIconsForChanges(serial, w.apps, items);
              w.apps = items;
              w.loaded = true;
              w.pollLeft = 0;
              renderAppWinGrid(serial);
            }
            finish();
          });
        }).catch(finish);
      }
      poll();
    }).catch(function () { w.listCheckPolling = false; });
  }

  // tickAppWins：全局轮询（refreshNow 每 0.7s）驱动——未加载成功且额度未尽的继续拉。
  function tickAppWins() {
    Object.keys(appWins).forEach(function (serial) {
      var w = appWins[serial];
      if (!w.loaded && w.pollLeft > 0) {
        w.pollLeft--;
        loadAppWinApps(serial);
      }
    });
  }

  // renderAppWinGrid：渲染应用网格到浮窗（仅当该 serial 正在浮窗展示；搜索过滤；点击=占位）。
  function renderAppWinGrid(serial) {
    var w = appWins[serial];
    if (!w || appWinModalSerial !== serial) return;
    var box = el('appwin-modal-grid');
    var q = (el('appwin-modal-search').value || '').trim().toLowerCase();
    var list = w.apps || [];
    el('appwin-modal-search').disabled = !!w.initialIconBusy;
    if (w.initialIconBusy) {
      box.innerHTML = '<div class="appwin-loading">正在读取应用图标…</div>';
      return;
    }
    if (!w.loaded && list.length === 0) {
      box.innerHTML = '<div class="appwin-loading">正在读取应用列表…</div>';
      return;
    }
    // v2.1.18 搜索升级：名称/包名子串（原有）+ 拼音模糊（app_search.js：
    // 全拼/首字母/混拼/部分前缀——「文件」← wenjian / wenj / wj / jian）。
    // 模块缺失时降级=原名称/包名子串（同 build 内嵌，理论不可达，防御）。
    var filtered = (typeof SCEZAppSearch !== 'undefined')
      ? SCEZAppSearch.filterApps(list, q)
      : list.filter(function (a) {
          if (!q) return true;
          return (a.name || '').toLowerCase().indexOf(q) >= 0 || (a.pkg || '').toLowerCase().indexOf(q) >= 0;
        });
    if (filtered.length === 0) {
      box.innerHTML = '<div class="appwin-loading">没有匹配的应用</div>';
      return;
    }
    box.innerHTML = '';
    filtered.forEach(function (a) {
      var card = document.createElement('div');
      card.className = 'appwin-app';
      card.title = a.pkg;
      var icon = document.createElement('div');
      icon.className = 'appwin-icon';
      // 必须用 backgroundColor 独立属性：background 简写会把 background-size/
      // background-position 重置为初始值（内联优先级高于 CSS 类），令 .has-img 的
      // cover/center 失效、真图标按"原图左上角 1:1 裁切"显示（v2.1.13 修复）。
      icon.style.backgroundColor = appColor(a.pkg || a.name || '?');
      icon.textContent = (a.name || '?').charAt(0);
      if (a.pkg) requestIcon(serial, a.pkg, icon); // 有 PNG 则替换彩块（懒加载+缓存）
      var label = document.createElement('span');
      label.textContent = a.name || a.pkg;
      card.appendChild(icon);
      card.appendChild(label);
      card.addEventListener('click', function () {
        // v2.1.21：点击应用 = 开窗（虚拟屏会话）+ 浮窗退出 + 卡片出现。
        // 乐观加卡（立即可见）；启动失败回滚卡片并提示。
        closeAppWinModal();
        if (!addOpenAppCard(serial, a.pkg, a.name || a.pkg)) return; // 已在运行=去重
        if (typeof window.StartAppWin === 'function') {
          window.StartAppWin(serial, a.pkg, a.name || a.pkg).catch(function (e) {
            toast('打开失败：' + (e && e.message ? e.message : e));
            removeOpenAppCard(serial, a.pkg);
          });
        }
      });
      box.appendChild(card);
    });
  }

  // v2.1.52：会话页组件模板/绑定/引用抽常量与函数（新建会话页 与「蓝灯页→会话页
  // 原地升级」两路共用；与 startFade/fadeSessionOnlyParts 的移除集合严格对称）。
  var SESSION_TOP_HTML =
    '<div class="status-card">' +
      '<div class="pulse-row"><div class="pulse"></div><div class="status-title"></div></div>' +
      '<div class="status-desc"></div>' +
      '<div class="spec-row" title="点击调整参数（有线/无线两套独立）"></div>' +
    '</div>' +
    '<div class="banner error" style="display:none"></div>' +
    '<div class="banner warn stalled" style="display:none">⚠️ 已 <span class="stalled-secs">0</span> 秒无输出，可点击 <button class="btn tiny primary restart">重启投屏</button>（不自动干预 bat）</div>' +
    '<div class="prompt-bar" style="display:none"></div>' +
    '<div class="session-actions">' +
      '<button class="btn danger tiny act-stop">停止投屏</button>' +
    '</div>';
  var APPWIN_ROW_HTML =
    '<div class="appwin-row">' +
      '<button class="btn-appwin" type="button">' +
        '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2.5"/><line x1="3" y1="9.5" x2="21" y2="9.5"/></svg>' +
        '应用窗口<span class="chev">▼</span>' +
      '</button>' +
      '<button class="btn-allstop" type="button" style="display:none">全部关闭</button>' +
    '</div>';
  var OPENAPP_LIST_HTML = '<div class="openapp-list"></div>';
  // v2.1.81：日志区不再内嵌模板——多来源日志模块组（.logmods）由 renderLogMods
  // 每轮渲染时动态 ensure（「蓝灯→会话」原地升级路径曾因模板拼接漏绑交互，日志打不开）。
  // bindSessionPaneHandlers：会话页交互绑定（会话级操作全部带 serial；所有按钮
  // stopPropagation——多会话「点了 A 全动」防御闸）。
  function bindSessionPaneHandlers(pane, serial, info) {
    pane.querySelector('.spec-row').addEventListener('click', function (ev) {
      ev.stopPropagation();
      openSpecModal(serial);
    });
    pane.querySelector('.restart').addEventListener('click', function (ev) {
      ev.stopPropagation();
      RestartCast(serial).then(function () { refreshNow(); }).catch(function (e) {
        toast('重启失败：' + (e && e.message ? e.message : e));
      });
    });
    // 二期：「应用窗口」按钮（常驻会话卡）→ 打开应用选择浮窗（主人 0919 毛病2 修正）
    pane.querySelector('.btn-appwin').addEventListener('click', function (ev) {
      ev.stopPropagation();
      openAppWin(serial, (info && info.devName) || serial);
    });
    // v2.1.41：「全部关闭」——给本设备全部应用窗口发关闭指令
    pane.querySelector('.btn-allstop').addEventListener('click', function (ev) {
      ev.stopPropagation();
      stopAllAppWins(serial);
    });
    pane.querySelector('.act-stop').addEventListener('click', function (ev) {
      ev.stopPropagation();
      var b = ev.currentTarget;
      b.textContent = '正在终止…'; // 立即反馈（不等快照）
      b.disabled = true;
      StopCast(serial).then(function () { refreshNow(); }).catch(function (e) {
        toast('停止失败：' + (e && e.message ? e.message : e));
        if (lastState) renderSessions(lastState); // 失败恢复按钮（快照无变化时强刷）
      });
    });
  }

  // sessionRefs：会话页组件引用获取（新建/升级两路共用）。
  function sessionRefs(pane, tab) {
    return {
      pulse: pane.querySelector('.pulse'),
      phase: pane.querySelector('.status-title'),
      desc: pane.querySelector('.status-desc'),
      specs: pane.querySelector('.spec-row'),
      err: pane.querySelector('.banner.error'),
      stalled: pane.querySelector('.banner.warn.stalled'),
      secs: pane.querySelector('.stalled-secs'),
      prompt: pane.querySelector('.prompt-bar'),
      actions: pane.querySelector('.session-actions'),
      stop: pane.querySelector('.act-stop'),
      dot: tab.querySelector('.casttab-dot')
    };
  }

  function ensureSession(serial, info) {
    if (sessions[serial]) return sessions[serial];

    // ===== v2.1.52：同设备蓝灯页 → 会话页「原地升级」（startFade 的对称反向）=====
    // 卡片/按钮 DOM 零迁移；标签原位、灯色蓝→绿（主人「原地变身」美学）。覆盖
    // 「先应用窗口后主投屏」且两键形态不同（USB serial vs IP:port）的场景
    //（此前字面键收编失配 → 双标签不合并，主人实测）。
    var upKey = findAppKeyFor(serial, info && info.identity);
    var upW = upKey ? appWins[upKey] : null;
    if (upW && upW.tab && upW.pane) {
      var upTab = upW.tab;
      var upPane = upW.pane;
      var dot0 = upTab.querySelector('.casttab-dot');
      if (dot0) dot0.className = 'casttab-dot'; // 蓝 → 绿（CSS transition 过渡）
      var nm0 = upTab.querySelector('.casttab-name');
      if (nm0 && info && info.devName) nm0.textContent = info.devName;
      upTab.title = serial;
      upTab.setAttribute('data-key', serial); // 拖拽排序键随 castOrder 迁移（原位）
      // v2.1.53：**同步完成**，不用 vtRender —— document.startViewTransition 的回调是
      // **异步**的（浏览器实测 [before,after] 回调未同步执行）：f 外的 bind/refs 会先于
      // 插入执行 → querySelector 取 null 崩溃 → sessions 未注册 → 每轮重复排队插入 →
      // 双份会话组件（v2.1.52 实测「空套+完整套」）。升级=瞬时变身，正确性优先。
      // 插前清理：防历史残留（复用/竞态产生的旧会话组件）叠加——幂等；卡片容器不受影响。
      fadeSessionOnlyParts(upPane, false);
      upPane.insertAdjacentHTML('afterbegin', SESSION_TOP_HTML);
      upPane.className = 'session-pane';
      upPane.id = 'session-' + sanitizeId(serial);
      upPane.querySelector('.btn-appwin').style.viewTransitionName = 'appwbtn-' + vtSafeName(serial);
      bindSessionPaneHandlers(upPane, serial, info);
      var s2 = sessions[serial] = {
        serial: serial,
        identity: (info && info.identity) || '',
        tab: upTab,
        pane: upPane,
        fading: false,
        refs: sessionRefs(upPane, upTab)
      };
      // 收编：蓝灯表项保留（数据键）仅摘页引用；castOrder 原位迁移（原键→会话键）。
      upW.tab = null;
      upW.pane = null;
      var oi2 = castOrder.indexOf(upKey);
      if (oi2 >= 0) {
        castOrder[oi2] = serial;
      } else if (castOrder.indexOf(serial) < 0) {
        castOrder.push(serial);
      }
      // 卡片重渲染（数据键 upKey；容器=升级后的会话 pane）。
      renderOpenAppCards(upKey);
      activateSession(serial);
      updateTabs();
      return s2;
    }

    var tab = document.createElement('div');
    tab.className = 'casttab';
    tab.setAttribute('data-key', serial); // 拖拽排序的 key（会话串号）
    tab.innerHTML = '<span class="casttab-dot"></span><span class="casttab-name"></span>';
    tab.querySelector('.casttab-name').textContent = (info && info.devName) || serial;
    tab.title = serial;
    tab.addEventListener('click', function () {
      // v2.1.39：标签可能被复用（会话退出 → 原地变蓝灯，见 startFade）——
      // 按当前身份动态分发：会话在=会话逻辑；已转蓝灯=激活蓝灯页。
      // v2.1.52：同设备多形态键——蓝灯页升级为会话后原键标签点击经 sessionKeyOf 进会话。
      var sk = sessions[serial] ? serial : sessionKeyOf(serial);
      if (sk) {
        activateSession(sk);
        // 标签点击 = 对应投屏窗口浮前（z-order 置顶，不夺 ez 焦点；
        // fire-and-forget：失败不阻塞标签切换）
        if (typeof window.BringCastToFront === 'function') {
          try {
            var p = window.BringCastToFront(sk);
            if (p && typeof p.catch === 'function') p.catch(function () {});
          } catch (e) { /* 忽略：浮前失败不影响切换 */ }
        }
      } else {
        // v2.1.52：蓝灯态——条目可能挂在同设备另一形态键上（退出复用/归一场景）。
        var ak = appWins[serial] ? serial : findAppKeyFor(serial);
        if (ak) activateAppWinTab(ak);
      }
    });
    el('cast-tabbar').appendChild(tab);
    if (castOrder.indexOf(serial) < 0) castOrder.push(serial); // 新会话标签追加到末尾

    var pane = document.createElement('div');
    pane.className = 'session-pane';
    pane.id = 'session-' + sanitizeId(serial);
    pane.innerHTML = SESSION_TOP_HTML + APPWIN_ROW_HTML + OPENAPP_LIST_HTML;
    el('cast-sessions').appendChild(pane);

    // v2.1.40：会话页的「应用窗口」按钮按 serial 唯一命名——会话→蓝灯复用变身时
    // 与卡片一起被 View Transition 捕捉（平滑上移补位，不留瞬移感）。
    pane.querySelector('.btn-appwin').style.viewTransitionName = 'appwbtn-' + vtSafeName(serial);

    // 会话级操作全部带 serial（按会话）——v2.1.52 抽为共享函数（升级路径复用）。
    bindSessionPaneHandlers(pane, serial, info);

    var s = sessions[serial] = {
      serial: serial,
      identity: (info && info.identity) || '',
      tab: tab,
      pane: pane,
      fading: false,
      refs: sessionRefs(pane, tab)
    };
    // v2.1.52：旧「删蓝灯+新建会话页」收编已由函数开头的「蓝灯页原地升级」取代
    //（同键与跨形态键统一走升级路径；卡片零迁移）。
    // v2.1.19：会话 pane 创建时，补渲染该设备已有应用卡片（无投屏期间已添加的场景）。
    // v2.1.52：数据键经 appKeyOf 归一（同设备多形态：应用窗口键可能≠投屏会话键）。
    renderOpenAppCards(appKeyOf(serial));
    // v2.1.81：日志区（多来源模块组）由 renderLogMods 每轮渲染动态 ensure（幂等）——
    // 不再内嵌模板+手动绑交互（「蓝灯→会话」原地升级路径曾漏绑 → 日志打不开）；
    // 开合/跟随状态改存模块级（logModOpen / body.__follow）。

    // 新会话淡入（浏览器范式：opacity/宽度动画）
    tab.classList.add('entering');
    pane.classList.add('entering');
    setTimeout(function () {
      tab.classList.remove('entering');
      pane.classList.remove('entering');
    }, 340);

    activateSession(serial);
    updateTabs();
    return s;
  }

  function activateSession(serial) {
    activeSerial = serial;
    Object.keys(sessions).forEach(function (k) {
      sessions[k].tab.classList.toggle('active', k === serial);
      sessions[k].pane.style.display = k === serial ? '' : 'none';
    });
    hideAppWinPanes(); // 二期：会话激活时隐藏蓝灯标签页（互斥）
  }

  // activateFirstActive：活动标签失效后的自动跳转——活动会话优先（跳过正在淡出的
  // 结束态会话）；无会话时退回"有应用卡片的蓝灯页"（虚拟屏在跑；蓝灯页未建则先建）。
  // v2.1.38：扩展虚拟屏兜底（对称 2.0 时期"关闭自动切到另一个投屏"）——此前只找
  // 会话，单主投屏关闭（他机有虚拟屏）时无会话可跳即停在空态。
  function activateFirstActive() {
    var first = null;
    Object.keys(sessions).forEach(function (k) {
      if (!first && sessions[k] && !sessions[k].fading) first = k;
    });
    if (first) {
      activateSession(first);
      return;
    }
    var pick = null;
    Object.keys(openAppCards).forEach(function (k) {
      if (!pick && (openAppCards[k] || []).length > 0) pick = k;
    });
    if (pick) {
      if (!(appWins[pick] && appWins[pick].pane)) {
        ensureAppWinPane(pick); // 蓝灯页未建（如会话收编态刚退场）→ 先建页再激活
      }
      if (appWins[pick] && appWins[pick].tab) {
        activateAppWinTab(pick);
      } else {
        activeSerial = null;
      }
      return;
    }
    activeSerial = null;
  }

  function updateTabs() {
    var n = Object.keys(sessions).length + Object.keys(appWins).length; // 二期：应用标签也计入
    el('cast-tabbar').style.display = n ? '' : 'none';
    el('cast-empty').style.display = n ? 'none' : '';
  }

  // ---------- 设备标签行拖拽排序（浏览器式：过中线互换/未过半回弹） ----------
  // order=castOrder（serial 顺序）；拖拽期间组件吞掉 click → 不触发 activateSession/
  // BringCastToFront（Go 调用零接触）。顺序不持久化：会话生命周期=进程生命周期，
  // 重启后新会话按创建顺序追加（localStorage 记忆无意义）。
  DragOrder.attach(el('cast-tabbar'), {
    getOrder: function () { return castOrder; },
    setOrder: function (next) { castOrder = next; },
    keyOf: function (t) { return t.getAttribute('data-key'); },
    onDrop: null
  });

  // gui44：设备列表纵向拖拽排序（axis:'y'；顺序持久化到 localStorage）
  // 监听器绑在容器，DOM 重建不失效；拖拽中 renderDevices 会跳过重建。
  // gui53：批量管理模式禁止拖拽（固定卡片）；退出后恢复。
  DragOrder.attach(el('device-list'), {
    axis: 'y',
    getOrder: function () { return devOrder.slice(); },
    setOrder: function (next) { devOrder = next; },
    keyOf: function (card) { return card.dataset.key; },
    enabled: function () { return !batchMode; },
    onDrop: function (order) { devOrder = order; saveOrderBackend(order); }
  });

  // 会话 pane 里"主投屏特有组件"（会话退出复用为蓝灯时淡出/移除；「应用窗口」按钮
  // 与卡片容器两形态共有、原地保留——卡片随后上移补位，v2.1.40）。
  // v2.1.81：日志区（.logmods）不进本集合——主投屏结束后它继续显示应用窗口日志，
  // 自身可见性由 renderLogMods 按来源有无控制。
  var SESSION_ONLY_SEL = '.status-card, .banner, .prompt-bar, .session-actions';
  function fadeSessionOnlyParts(pane, on) {
    var parts = pane.querySelectorAll(SESSION_ONLY_SEL);
    Array.prototype.forEach.call(parts, function (el) {
      if (on) {
        el.classList.add('ez-fade-out');
      } else if (el.parentNode) {
        el.parentNode.removeChild(el);
      }
    });
  }

  // 标签淡出消失动画（宽度收敛 + 透明度），结束后移除 DOM 并通知后端 ForgetSession
  function startFade(s) {
    if (s.fading) return;
    // v2.1.39：设备仍有应用卡片（虚拟屏在跑）→ 会话标签"原地变身"为蓝灯标签：
    // 同一个 DOM（位置/拖拽顺序天然保留），只切灯色 + 换 pane——不再"先删标签、
    // ensureAppWinPane 重建"（新标签 append 到末尾=排到其他设备后面，且观感像
    // 新开页签；主人实测）。
    // v2.1.40：变身时**只淡出"主投屏特有组件"**（状态卡/规格/停止按钮/日志等），
    // 卡片容器与「应用窗口」按钮原地保留（不再整页淡出——此前连卡片一起淡出再
    // 凭空出现，观感是"整页淡出再淡入"；主人实测）。
    // v2.1.52：数据键归一（同设备多形态：卡片数据键可能≠会话键）。
    var dataKey = appKeyOf(s.serial);
    var keepAsAppWin = (openAppCards[dataKey] || []).length > 0;
    s.fading = true;
    if (keepAsAppWin) {
      fadeSessionOnlyParts(s.pane, true);
    } else {
      s.pane.classList.add('fading'); // 无卡片：整页淡出（原行为）
      s.tab.classList.add('fading');
    }
    setTimeout(function () {
      if (!s.fading) return; // 重启复活：淡出已被取消
      if (sessions[s.serial] !== s) return;
      var wasActive = (activeSerial === s.serial);
      delete sessions[s.serial];
      if (keepAsAppWin) {
        // —— 会话标签 → 蓝灯标签（同一 DOM：原位、灯色动态切换）——
        var dot = s.tab.querySelector('.casttab-dot');
        if (dot) dot.className = 'casttab-dot appw'; // 绿/灰 → 蓝（CSS transition 过渡）
        // pane 就地改造：移除已淡出的会话组件 + 换类名/ID（样式对齐蓝灯页）。
        // 卡片容器原地不动——View Transition 让卡片平滑上移补位（v2.1.34 机制）。
        vtRender(function () {
          fadeSessionOnlyParts(s.pane, false);
          s.pane.className = 'appwin-tab-pane';
          s.pane.id = 'appwintab-' + sanitizeId(dataKey);
        });
        var w = appWins[dataKey];
        if (!w) {
          w = appWins[dataKey] = {
            name: s.tab.querySelector('.casttab-name').textContent || dataKey,
            tab: null, pane: null, apps: null, loaded: false, pollLeft: 40,
            identity: s.identity || deviceIdentityOf(dataKey)
          };
        }
        if (!w.identity) w.identity = s.identity || deviceIdentityOf(dataKey);
        w.tab = s.tab;
        w.pane = s.pane; // 复用同一 pane（卡片 DOM 原地保留，不重建）
        s.tab.setAttribute('data-key', dataKey); // 拖拽排序键随 castOrder 迁移（对称升级）
        // castOrder：会话键 → 数据键（原位；对称升级时的迁移）。
        var oi3 = castOrder.indexOf(s.serial);
        if (oi3 >= 0) castOrder[oi3] = dataKey;
        if (wasActive) activateAppWinTab(dataKey);
      } else {
        s.pane.remove();
        s.tab.remove();
        var oi = castOrder.indexOf(s.serial);
        if (oi >= 0) castOrder.splice(oi, 1); // 标签消失 → 移出显示顺序
        if (wasActive) {
          // v2.1.38：统一跳转——本设备蓝灯页（虚拟屏在跑）→ 其他仍有内容的标签
          //（会话 / 他机虚拟屏）→ 都没有则空态（不会停在空态）。
          if (appWins[s.serial] && appWins[s.serial].pane) {
            activateAppWinTab(s.serial);
          } else {
            activateFirstActive();
          }
        }
      }
      updateTabs();
      if (typeof window.ForgetSession === 'function') ForgetSession(s.serial);
    }, 340);
  }

  function cancelFade(s) {
    s.fading = false;
    s.tab.classList.remove('fading');
    s.pane.classList.remove('fading');
    // v2.1.40：复用路径的会话组件淡出撤销（重启复活）
    var parts = s.pane.querySelectorAll('.ez-fade-out');
    Array.prototype.forEach.call(parts, function (el) { el.classList.remove('ez-fade-out'); });
  }

  // 投屏中页渲染：会话标签/面板随 Snapshot 同步（每会话内容独立）
  function renderSessions(st) {
    var known = {};
    (st.sessions || []).forEach(function (s) { known[s.serial] = s; });

    // 已结束且前端没有 DOM 的会话：直接通知后端移除（正常路径由 startFade 完成）
    Object.keys(known).forEach(function (serial) {
      var k = known[serial];
      if (!k.active && !sessions[serial]) {
        if (typeof window.ForgetSession === 'function') ForgetSession(serial);
        return;
      }
      ensureSession(serial, k);
    });

    Object.keys(sessions).forEach(function (serial) {
      var s = sessions[serial];
      var k = known[serial];
      if (k) {
        // 设备名随档案富化更新时同步标签标题
        var nm = s.tab.querySelector('.casttab-name');
        var want = k.devName || serial;
        if (nm.textContent !== want) nm.textContent = want;
      }
      if (k && k.active) {
        if (s.fading) cancelFade(s); // 重启复活：取消淡出
        renderSessionPane(s, k, st);
      } else {
        // bat 已退出（关窗/停止）：标签淡出消失
        startFade(s);
      }
    });

    // v2.1.38：活动标签失效时**不再立即置空**——交由 startFade 的移除回调统一跳转
    //（本设备蓝灯页 / 其他仍有内容的标签 / 空态；会话重启复活时也无缝保持）。
    // 原逻辑立即置空 + activateFirstActive 只找会话：多设备混合场景（主投屏 +
    // 他机虚拟屏）关闭主投屏后无会话可跳 → 停在空态（主人实测：不会自动跳到
    // 正在进行的虚拟屏）。ak 缺失（会话从快照消失）才置空。
    if (activeSerial && sessions[activeSerial]) {
      var ak = known[activeSerial];
      if (!ak) activeSerial = null;
    }
    if (!activeSerial) activateFirstActive();
    // v2.1.81：蓝灯态 pane（无对应会话）的日志模块组渲染——会话态已由
    // renderSessionPane 处理（互斥；变形/升级路径下一轮自动走到对应分支）。
    Object.keys(appWins).forEach(function (k) {
      var w = appWins[k];
      if (!w || !w.pane) return;
      if (sessions[k] || sessionKeyOf(k)) return;
      renderLogMods(w.pane, k, st);
    });
    updateTabs();
  }

  function renderSessionPane(s, k, st) {
    var c = k.cast;
    var r = s.refs;
    r.pulse.style.display = c.active ? '' : 'none';
    // 模式显示（修复误判）：casting 且规格未到时用 c.mode（最近一次真实模式行，
    // classify 精确短语判定；"保存无线地址"等学习行不改变模式）；规格到达后用 spec.wired 定案
    var modeTitle;
    if (c.phase === 'casting' && c.spec) {
      modeTitle = c.spec.wired ? '投屏中 · 有线模式' : '投屏中 · 无线模式';
    } else if (c.phase === 'casting' && c.mode === 'usb') {
      modeTitle = '投屏中 · 有线模式';
    } else if (c.phase === 'casting' && c.mode === 'wifi') {
      modeTitle = '投屏中 · 无线模式';
    }
    // gui43 实时形态标注：会话实际连接走 TLS → 标题附加 TLS加密（5555 连接不显示）
    if (c.tls && c.phase === 'casting') {
      modeTitle = (modeTitle || '投屏中') + ' · TLS加密';
    }
    r.phase.textContent = modeTitle || (c.phaseText || (c.active ? '投屏中…' : ''));
    r.desc.textContent = c.active ? describeCast(c) : (c.exitText || '');
    if (c.lastError && c.active) {
      r.err.style.display = '';
      r.err.textContent = '⚠ ' + c.lastError;
    } else {
      r.err.style.display = 'none';
    }

    // 会话级按钮（停止=杀树）：仅运行中显示；唯一主按钮占满整行。
    // stopping（主动停止已受理）→ "正在终止…"禁用态；
    // closing（投屏窗口 X 已关闭、bat 清理中）→ "正在关闭…"禁用态；
    // onExit 后随快照恢复。stopping 优先于 closing 显示。
    r.actions.style.display = c.active ? '' : 'none';
    var stopping = !!k.stopping;
    var closing = !!k.closing;
    var stopText = stopping ? '正在终止…' : (closing ? '正在关闭…' : '停止投屏');
    var stopDisabled = stopping || closing;
    if (r.stop.textContent !== stopText) r.stop.textContent = stopText;
    if (r.stop.disabled !== stopDisabled) r.stop.disabled = stopDisabled;

    // 设备原生分辨率（宽≥高）：自定义长边规格的短边按此比例计算。
    // 优先 c.nativeRes（StartCast 时后端捕获并保持，恢复期设备离线仍可用）；
    // 回退实时设备列表匹配（Serial 或 Wireless——卡片重键兜底）
    var nativeRes = c.nativeRes || '';
    if (!nativeRes) {
      (st.devices || []).forEach(function (d) {
        if ((d.serial === c.serial || d.wireless === c.serial) && d.res) nativeRes = d.res;
      });
    }

    // 规格徽标：投屏中无规格（旧规格已在重连/切换时被清空）→ "等待规格…"
    var want = c.active && !c.spec
      ? '<div class="spec">等待规格…</div>'
      : specHtml(c.spec, c.keyboardMode, nativeRes);
    if (r.specs.innerHTML !== want) r.specs.innerHTML = want;

    // 无输出提示（只读展示）
    if (c.active && c.stalled) {
      r.stalled.style.display = '';
      r.secs.textContent = String(c.stallSecs || 40);
    } else {
      r.stalled.style.display = 'none';
    }

    // 输入提示条（choice/pause → 会话级操作按钮，不写 bat stdin）
    var wantBar = promptHtml(c);
    if (wantBar !== r.prompt.getAttribute('data-k')) {
      r.prompt.setAttribute('data-k', wantBar);
      r.prompt.innerHTML = '';
      if (c.waitingInput) {
        r.prompt.style.display = '';
        promptButtons(c.prompt).forEach(function (p) {
          var b = document.createElement('button');
          b.className = 'btn ' + (p.primary ? 'primary' : 'ghost');
          b.textContent = p.label;
          b.addEventListener('click', function (ev) {
            ev.stopPropagation(); // 只作用于本会话（防冒泡串会话）
            sessionAction(p.action, s.serial);
          });
          r.prompt.appendChild(b);
        });
        var hint = document.createElement('span');
        hint.className = 'hint';
        hint.textContent = c.prompt === 2
          ? '可重新投屏，或停止当前会话'
          : '按钮=会话级操作（重启/退出），GUI 不直接替 bat 按键';
        r.prompt.appendChild(hint);
      } else {
        r.prompt.style.display = 'none';
      }
    }

    // v2.1.81：多来源日志模块组（主投屏输出 + 各应用窗口「应用名」输出（虚拟屏）），
    // 排序/开合/跟随见 renderLogMods（主投屏恒第一；无主投屏时应用屏按开启顺序）。
    renderLogMods(s.pane, s.serial, st);

    // 标签状态点：活动=绿点呼吸动画
    r.dot.classList.toggle('active', c.active);
  }

  // ---------- v2.1.81：多来源日志模块组（排查用日志区重做） ----------
  // 来源：主投屏（"主投屏输出"）+ 本设备各应用窗口（「应用名」输出（虚拟屏））。
  // 排序：主投屏恒第一；无主投屏时应用屏按开启顺序（快照 appWins 数组=startedAt 序）
  // 顺延顶上；主投屏后加入自动插队第一（下一轮渲染即对齐）。
  // 容器 .logmods 动态 ensure（幂等）：会话 pane / 蓝灯 pane / 「蓝灯↔会话」原地
  // 变形三路共用——修复 v2.1.52 升级路径漏绑交互导致的「日志打不开」。
  var logModOpen = {}; // 开合状态：来源键 → bool（cast:<serial> / app:<serial>#<pkg>）

  function ensureLogMods(pane) {
    // v2.1.83：单例化——历史残留的多容器先清理（防叠印）
    var boxes = pane.querySelectorAll('.logmods');
    for (var i = 1; i < boxes.length; i++) boxes[i].remove();
    var box = boxes[0];
    if (!box) {
      box = document.createElement('div');
      box.className = 'logmods';
      pane.appendChild(box);
    }
    return box;
  }

  // sameDeviceKey：日志归属归一（快照的 serial / appWins 键 / 会话键可能是同设备不同形态）。
  function sameDeviceKey(a, b) {
    if (a === b) return true;
    if (!a || !b) return false;
    try { return appKeyOf(a) === appKeyOf(b); } catch (e) { return false; }
  }

  function renderLogMods(pane, serial, st) {
    if (!pane || !st) return;
    var box = ensureLogMods(pane);
    var srcs = [];
    // ① 主投屏输出（仅活跃会话；无主投屏时此源缺席 → 应用屏顺延顶上）
    (st.sessions || []).forEach(function (k) {
      if (!k || !k.active || !k.cast) return;
      if (!sameDeviceKey(k.serial, serial)) return;
      srcs.push({ id: 'cast:' + k.serial, title: '主投屏输出', log: k.cast.log || [] });
    });
    // ② 应用窗口输出（虚拟屏）——快照数组顺序=开启顺序（Go 侧按 startedAt 排好）
    (st.appWins || []).forEach(function (w) {
      if (!w) return;
      if (!sameDeviceKey(w.serial, serial)) return;
      srcs.push({ id: 'app:' + w.serial + '#' + w.pkg, title: '「' + (w.name || w.pkg) + '」输出（虚拟屏）', log: w.log || [] });
    });
    // ---- 结构同步：按 srcs 顺序复用/新建模块 DOM（appendChild 移动即排序）----
    var alive = {};
    srcs.forEach(function (s) { alive[s.id] = true; });
    Array.prototype.slice.call(box.children).forEach(function (m) {
      if (!alive[m.__id]) box.removeChild(m);
    });
    srcs.forEach(function (s, i) {
      var m = null;
      Array.prototype.some.call(box.children, function (c) {
        if (c.__id === s.id) { m = c; return true; }
        return false;
      });
      if (!m) {
        m = document.createElement('div');
        m.className = 'logmod';
        m.__id = s.id;
        m.innerHTML =
          '<div class="logmod-head"><span class="logmod-title"></span> <span class="logmod-caret">▸</span></div>' +
          '<div class="logmod-body"></div>';
        m.querySelector('.logmod-head').addEventListener('click', function () {
          logModOpen[s.id] = !logModOpen[s.id];
          renderLogMods(pane, serial, lastState); // 立即重绘（最近快照）
        });
        var body0 = m.querySelector('.logmod-body');
        body0.addEventListener('scroll', function () {
          body0.__follow = body0.scrollTop + body0.clientHeight >= body0.scrollHeight - 24;
        });
      }
      if (box.children[i] !== m) box.insertBefore(m, box.children[i] || null);
      // 标题（应用名可能变）
      var t = m.querySelector('.logmod-title');
      if (t.textContent !== s.title) t.textContent = s.title;
      // 开合（默认收起；caret/展开样式由 .open 类驱动）
      var open = !!logModOpen[s.id];
      m.classList.toggle('open', open);
      m.querySelector('.logmod-caret').textContent = open ? '▾' : '▸';
      // 内容键控更新（行数+末行：后端 60 行截断后行数恒 60，仅比行数会冻结）
      var body = m.querySelector('.logmod-body');
      var logKey = String(s.log.length) + '\u0000' + (s.log.length ? s.log[s.log.length - 1] : '');
      if (body.getAttribute('data-n') !== logKey) {
        var prev = body.scrollTop;
        var follow = body.__follow !== false; // 默认跟随（滚底）
        body.setAttribute('data-n', logKey);
        body.innerHTML = '';
        s.log.slice(-40).forEach(function (l) {
          var d = document.createElement('div');
          d.className = 'log-line';
          d.textContent = l;
          body.appendChild(d);
        });
        if (follow) body.scrollTop = body.scrollHeight;
        else body.scrollTop = prev; // 重建重置滚动：还原用户阅读位置
      }
    });
    // v2.1.82：容器态切换——存在展开模块时吃剩余空间（从下方扩展、上方不动）；
    // 全折叠=紧凑内容高度（条数多时列表可滚轮滚动浏览）。
    var anyOpen = false;
    for (var oi = 0; oi < srcs.length; oi++) {
      if (logModOpen[srcs[oi].id]) { anyOpen = true; break; }
    }
    box.classList.toggle('has-open', anyOpen);
    // 无来源 → 整组隐藏（如主投屏结束、应用屏也已退出的空窗）
    box.style.display = srcs.length ? '' : 'none';
  }

  function describeCast(c) {
    if (c.keyboardMode) return '键盘模式 ' + fmtKeyboardMode(c.keyboardMode) + (c.spec && c.spec.legacy ? ' · 老设备兼容档' : ' · 规格分配已生效');
    return '正在连接设备…';
  }

  // 分辨率统一大数字在前（宽≥高），与列表页一致
  function sortWideFirst(res) {
    var m = /^(\d+)x(\d+)$/.exec(res || '');
    if (!m) return res;
    var w = parseInt(m[1], 10), h = parseInt(m[2], 10);
    return w >= h ? w + 'x' + h : h + 'x' + w;
  }

  function specHtml(spec, kbd, nativeRes) {
    var out = [];
    if (spec && spec.res) {
      // 一律 WxH：Texture 真实值（classify 已提取）优先；[高清] 行原生值
      out.push('<div class="spec">' + esc(sortWideFirst(spec.res)) + '</div>');
    } else if (spec && spec.maxSize) {
      // 自定义长边（参数浮窗覆盖），Texture 未出时的换算兜底：
      // 短边按设备原生比例计算（宽≥高输出；整数截断与 bat/scrcpy 一致）
      var m = /^(\d+)x(\d+)$/.exec(sortWideFirst(nativeRes || ''));
      if (m) {
        var w = parseInt(m[1], 10), h = parseInt(m[2], 10);
        var short = Math.floor(Math.min(w, h) * spec.maxSize / Math.max(w, h));
        out.push('<div class="spec">' + spec.maxSize + 'x' + short + '</div>');
      } else {
        out.push('<div class="spec">长边 ' + spec.maxSize + '</div>');
      }
    }
    if (spec && spec.fps) out.push('<div class="spec">' + spec.fps + ' fps</div>');
    if (spec && spec.mbps) out.push('<div class="spec">' + spec.mbps + ' Mbps</div>');
    if (spec && spec.legacy) out.push('<div class="spec">老设备兼容档</div>');
    if (spec && spec.vcodec) out.push('<div class="spec">' + esc(fmtVCodec(spec.vcodec)) + '</div>');
    if (spec && spec.acodec) out.push('<div class="spec">' + esc(fmtACodec(spec.acodec)) + '</div>');
    if (kbd) out.push('<div class="spec">键盘 ' + esc(fmtKeyboardMode(kbd)) + '</div>');
    return out.join('');
  }

  function promptHtml(c) {
    if (!c.waitingInput) return 'none';
    switch (c.prompt) {
      case 1: return 'menu';   // [1]重新检测 [2]配对向导 [3]退出
      case 2: return 'qr';     // Q=退出循环 R=立即重投
      case 3: return 'any';    // pause 任意键
      default: return 'none';
    }
  }

  function promptButtons(prompt) {
    switch (prompt) {
      case 1:
        return [
          { action: 'restart', label: '① 重新检测', primary: true },
          { action: 'restart', label: '② 配对向导' },
          { action: 'stop', label: '③ 退出' },
        ];
      case 2:
        return [
          { action: 'restart', label: '重新投屏', primary: true },
          { action: 'stop', label: '停止投屏' },
        ];
      default:
        return [{ action: 'restart', label: '重启投屏（跳过等待）', primary: true }];
    }
  }

  // 会话级操作：重启=杀树后重跑新会话；停止=杀树退出（均不写 bat stdin；按会话 serial）
  function sessionAction(action, serial) {
    var fn = action === 'stop' ? StopCast : RestartCast;
    fn(serial).then(function () { refreshNow(); }).catch(function (e) {
      toast('操作失败：' + (e && e.message ? e.message : e));
    });
  }

  // ---------- 新设备弹窗（轮 B：检测到未创建会话的在线设备） ----------
  function renderNewDevice(st) {
    var p = el('newdev-popup');
    if (!st.newDevice) {
      if (popupSerial !== null) {
        popupSerial = null;
        p.style.display = 'none';
      }
      return;
    }
    var nd = st.newDevice;
    if (popupSerial !== nd.serial) {
      popupSerial = nd.serial;
      el('newdev-name').textContent = nd.name || nd.serial;
      el('newdev-tag').textContent = nd.connType === 'usb' ? '（USB）' : (nd.connType === 'wifi' ? '（无线）' : '');
      p.style.display = '';
    }
  }

  el('newdev-start').addEventListener('click', function () {
    if (popupSerial == null) return;
    var serial = popupSerial;
    // 并行 bat 实例：StartCastParallel → SCEZ_NO_WATCH=1（防 watcher 串会话）
    StartCastParallel(serial).then(function () {
      popupSerial = null;
      el('newdev-popup').style.display = 'none';
      switchView('cast'); // 新会话标签淡入并自动激活
      refreshNow();
    }).catch(function (e) {
      toast('启动失败：' + (e && e.message ? e.message : e));
      refreshNow();
    });
  });
  el('newdev-dismiss').addEventListener('click', function () {
    if (popupSerial == null) return;
    var serial = popupSerial;
    popupSerial = null;
    el('newdev-popup').style.display = 'none';
    // 暂不：会话周期（本在线周期）内不再弹；可后续手动点投屏按钮
    DismissNewDevice(serial).then(function () { refreshNow(); });
  });

  // ---------- 无线调试配对向导（gui50：自动发现 / 手动 / 二维码三路线） ----------
  var pairSuccessTimer = null;
  var pairMaskFadeTimer = null; // gui53：配对弹窗 mask 淡出 timer（防快速开关竞态）
  var pairSuccessShown = false;
  var pairBusy = false;
  var pairFailed = false;
  var pairQrLast = null;
  var pairQrAutoRefreshKey = null; // fix7：每次到期自动刷新只触发一次，直到后端换新码
  var pairQrRefreshTimer = null;   // fix7：按 expireAt 到点触发自动刷新

  function pairCodeInput() {
    return el('pair-code');
  }

  function pairCodeInputs() {
    var inp = pairCodeInput();
    return inp ? [inp] : [];
  }

  function pairSegInputs() {
    return [el('pair-ip1'), el('pair-ip2'), el('pair-ip3'), el('pair-ip4'), el('pair-port-seg')];
  }

  function pairCodeValue() {
    var inp = pairCodeInput();
    return inp ? inp.value.replace(/\D/g, '').slice(0, 6) : '';
  }

  function pairClearCode() {
    var inp = pairCodeInput();
    if (inp) inp.value = '';
  }

  function pairAddrPort(addr) {
    var m = /:(\d+)$/.exec(addr || '');
    return m ? m[1] : '';
  }

  function pairManualIp() {
    var parts = [];
    [el('pair-ip1'), el('pair-ip2'), el('pair-ip3'), el('pair-ip4')].forEach(function (inp) {
      if (!inp.value) return;
      parts.push(inp.value);
    });
    return parts.length === 4 ? parts.join('.') : '';
  }

  function pairApplyPending(p) {
    if (!pairCtx || !p) return;
    pairCtx.key = p.key || '';
    pairCtx.ip = p.ip || '';
    pairCtx.pairPort = p.pairPort || '';
    pairCtx.connPort = pairAddrPort(p.addr || '');
    pairCtx.name = p.name || '无线调试设备';
    pairCtx.addr = p.addr || '';
  }

  // 每次快照渲染时同步 mDNS 待配对列表到配对上下文；无目标时清空自动发现态。
  // 配对/连接/校验/失败阶段保留当前上下文，避免 mDNS 待配对卡在配对流程中短暂消失时打乱展示。
  function pairSyncFromState(st) {
    if (!pairCtx || !st) return;
    if (pairCtx.manual) return;
    var ps = st.pairStatus || null;
    // 配对/连接/校验/失败阶段都保留当前上下文，只有空闲态才跟随 mDNS 快照更新。
    var pairing = pairBusy || (ps && ps.phase !== 'idle');
    if (pairing) return;
    var list = (st.pending || []).slice();
    pairCtx.pending = list;
    if (!list.length) {
      pairCtx.key = '';
      pairCtx.ip = '';
      pairCtx.pairPort = '';
      pairCtx.connPort = '';
      pairCtx.name = '';
      pairCtx.addr = '';
      pairCtx.idx = 0;
      return;
    }
    var idx = -1;
    for (var i = 0; i < list.length; i++) {
      if (pairCtx.key && list[i].key && list[i].key === pairCtx.key) {
        idx = i;
        break;
      }
    }
    if (idx < 0) {
      pairCtx.idx = 0;
      pairApplyPending(list[0]);
    } else {
      pairCtx.idx = idx;
    }
  }

  function pairPrefillAddress() {
    if (!pairCtx) return;
    var ip = pairCtx.ip || '';
    if (!ip && pairCtx.addr) {
      var m = /^(\d+\.\d+\.\d+\.\d+):/.exec(pairCtx.addr);
      if (m) ip = m[1];
    }
    var octets = String(ip).split('.');
    var ips = [el('pair-ip1'), el('pair-ip2'), el('pair-ip3'), el('pair-ip4')];
    for (var i = 0; i < 4; i++) {
      var v = String(octets[i] || '').replace(/\D/g, '');
      ips[i].value = v;
      // 疑似地址灰显预填（核对即可），无地址时也保持灰色示例占位。
      ips[i].classList.add('pre');
    }
    var portEl = el('pair-port-seg');
    var port = String(pairCtx.pairPort || '').replace(/\D/g, '');
    portEl.value = port;
    portEl.classList.add('pre');
  }

  function pairFindStep(ps, name) {
    var steps = (ps && ps.steps) || [];
    for (var i = 0; i < steps.length; i++) {
      if (steps[i].name === name) return steps[i];
    }
    return null;
  }

  function pairFirstLabel(ps) {
    if (pairCtx && pairCtx.manual) return '输入';
    return (ps && ps.mode === 'qr') ? '扫码' : '输入';
  }

  function pairStepsData(ps, allDone) {
    var first = pairFirstLabel(ps);
    if (allDone) {
      return [{ label: first, state: 'ok' }, { label: '配对', state: 'ok' }, { label: '完成', state: 'ok' }];
    }
    var phase = ps ? ps.phase : 'idle';
    if (!ps || phase === 'idle') {
      return [{ label: first, state: 'cur' }, { label: '配对', state: 'none' }, { label: '完成', state: 'none' }];
    }
    if (phase === 'failed') {
      return [{ label: first, state: 'ok' }, { label: '配对', state: 'none' }, { label: '完成', state: 'none' }];
    }
    var pairStep = pairFindStep(ps, 'pair');
    var connectStep = pairFindStep(ps, 'connect');
    var doneStep = pairFindStep(ps, 'done');
    var s2 = 'none';
    if (pairStep) {
      s2 = pairStep.ok ? 'ok' : 'cur';
    } else if (phase === 'pairing') {
      s2 = 'cur';
    } else if (phase === 'connecting' || phase === 'verifying') {
      s2 = 'ok';
    }
    var s3 = 'none';
    if (doneStep && doneStep.ok) {
      s3 = 'ok';
    } else if (phase === 'connecting' || phase === 'verifying') {
      s3 = 'cur';
    } else if (connectStep) {
      s3 = connectStep.ok ? 'ok' : 'cur';
    }
    return [{ label: first, state: 'ok' }, { label: '配对', state: s2 }, { label: '完成', state: s3 }];
  }

  function pairRenderStepsInto(id, ps, allDone) {
    var box = el(id);
    if (!box) return;
    var steps = pairStepsData(ps, allDone);
    var html = '';
    for (var i = 0; i < steps.length; i++) {
      var st = steps[i];
      var cls = st.state === 'cur' ? ' cur' : (st.state === 'ok' ? ' done' : '');
      html += '<div class="pair-step' + cls + '"><span class="n">' + (i + 1) + '</span>' + esc(st.label) + '</div>';
      if (i < 2) html += '<div class="pair-step-sep' + (st.state === 'ok' ? ' green' : '') + '"></div>';
    }
    box.innerHTML = html;
  }

  function pairDrawQr(text) {
    var canvas = el('pair-qr-canvas');
    if (!canvas || typeof qrcode === 'undefined') return;
    var ctx = canvas.getContext('2d');
    var size = canvas.width;
    ctx.fillStyle = '#ffffff';
    ctx.fillRect(0, 0, size, size);
    if (!text) return;
    try {
      var qr = qrcode(0, 'M');
      qr.addData(text);
      qr.make();
      var n = qr.getModuleCount();
      var cell = Math.floor(size / (n + 2));
      var margin = Math.floor((size - cell * n) / 2);
      ctx.fillStyle = '#222222';
      for (var r = 0; r < n; r++) {
        for (var c = 0; c < n; c++) {
          if (qr.isDark(r, c)) {
            ctx.fillRect(margin + c * cell, margin + r * cell, cell, cell);
          }
        }
      }
    } catch (e) {
      // 二维码生成失败时保留白底（qrText 下轮快照通常自愈）
    }
  }

  function pairAutoRefreshQr() {
    if (pairBusy || pairSuccessShown || typeof window.PairConnect !== 'function') return;
    try {
      var pc = PairConnect('__qr_refresh__', '', '', '', '');
      if (pc && typeof pc.catch === 'function') pc.catch(function () {});
    } catch (e) { /* 忽略 */ }
    refreshNow();
  }

  function pairRenderQr(ps) {
    var qrText = ps ? ps.qrText : '';
    var expireAt = ps ? ps.qrExpireAt : 0;
    var expired = !!expireAt && Date.now() >= expireAt * 1000;
    if (qrText && qrText !== pairQrLast) {
      pairQrLast = qrText;
      pairDrawQr(qrText);
      // 新码出现时用新 expireAt 重排自动刷新定时器
      if (pairQrRefreshTimer) {
        clearTimeout(pairQrRefreshTimer);
        pairQrRefreshTimer = null;
      }
    }
    if (!qrText) {
      pairQrLast = null;
      pairDrawQr('');
    }
    el('pair-qr-overlay').style.display = expired ? 'none' : '';
    el('pair-qr-expired').style.display = expired ? '' : 'none';

    // fix7：未过期时按 expireAt 到点自动刷新（不依赖轮询快照变化）。
    if (qrText && !expired && pairQrRefreshTimer === null) {
      var delay = Math.max(0, expireAt * 1000 - Date.now());
      pairQrRefreshTimer = setTimeout(function () {
        pairQrRefreshTimer = null;
        pairAutoRefreshQr();
      }, delay + 100);
    }
    if (!qrText || expired) {
      if (pairQrRefreshTimer) {
        clearTimeout(pairQrRefreshTimer);
        pairQrRefreshTimer = null;
      }
    }

    // fix7：到期自动刷新一次；配对中/成功后不刷新。刷新后新码会更新 expireAt/文本，
    // pairQrAutoRefreshKey 用旧码去重，避免轮询期间重复触发。
    if (expired && !pairBusy && !pairSuccessShown && pairQrAutoRefreshKey !== qrText) {
      pairQrAutoRefreshKey = qrText;
      pairAutoRefreshQr();
    }
  }

  function pairFailText(ps) {
    if (!ps) return '连接失败，请确认设备和电脑处于局域网关系';
    var code = ps.errCode || '';
    var text = ps.errText || '';
    if (code === 'connect-failed') return '连接失败，请确认设备和电脑处于局域网关系';
    if (code) return PairUI.errText(code, text);
    if (text) return text;
    return '连接失败，请确认设备和电脑处于局域网关系';
  }

  function pairShowFail(msg) {
    pairFailed = true;
    el('pair-fail').style.display = '';
    el('pair-fail').textContent = msg || '连接失败，请确认设备和电脑处于局域网关系';
    el('pair-code-box').classList.add('err');
    el('pair-seg-box').classList.add('err');
    el('pair-go').classList.remove('primary');
    el('pair-go').classList.add('danger');
    el('pair-go').textContent = '重新配对';
  }

  function pairClearFail() {
    if (!pairFailed) return;
    pairFailed = false;
    el('pair-fail').style.display = 'none';
    el('pair-code-box').classList.remove('err');
    el('pair-seg-box').classList.remove('err');
    el('pair-go').classList.add('primary');
    el('pair-go').classList.remove('danger');
  }

  function pairSetBusy(busy) {
    pairBusy = busy;
    pairCodeInputs().concat(pairSegInputs()).forEach(function (inp) { inp.disabled = busy; });
    var noTarget = !busy && pairCtx && !pairCtx.manual && (!pairCtx.key || !(pairCtx.pending && pairCtx.pending.length));
    el('pair-go').disabled = busy || noTarget;
    el('pair-swap').disabled = busy;
    el('pair-swap').classList.toggle('dis', busy);
    el('pair-manual-toggle').classList.toggle('dim', busy);
    el('pair-back-auto').classList.toggle('dim', busy);
    el('pair-qr-area').classList.toggle('busy', busy);
    if (busy) {
      el('pair-go').textContent = '配对中…';
      el('pair-go').classList.add('primary');
      el('pair-go').classList.remove('danger');
    } else if (pairFailed) {
      el('pair-go').textContent = '重新配对';
    } else {
      el('pair-go').textContent = '配对';
    }
  }

  function pairRenderFound(ps) {
    if (!pairCtx) return;
    var found = el('pair-found');
    var nameEl = el('pair-found-name');
    var addrEl = el('pair-found-addr');
    var tagEl = el('pair-code-tag');
    if (pairCtx.manual) {
      found.classList.add('manual');
      el('pair-swap').style.display = 'none';
      nameEl.textContent = '手动填写';
      addrEl.textContent = '在「无线调试」中寻找IP地址和端口及配对码';
      if (tagEl) tagEl.textContent = '手机：使用配对码配对设备';
      el('pair-go').disabled = pairBusy;
      return;
    }
    found.classList.remove('manual');
    if (tagEl) tagEl.textContent = '在「无线调试」中寻找IP地址和端口及配对码';
    var list = (pairCtx.pending && pairCtx.pending.length) ? pairCtx.pending : [];
    var n = list.length;
    if (n > 0 && !pairCtx.key) {
      pairCtx.idx = 0;
      pairApplyPending(list[0]);
    }
    if (!n || !pairCtx.key) {
      nameEl.textContent = '未发现待配对设备';
      addrEl.textContent = '请确认手机已打开无线调试，且与电脑同一网络';
      el('pair-swap').style.display = 'none';
      el('pair-go').disabled = true;
      return;
    }
    nameEl.textContent = '已发现 ' + n + ' 台新设备 · 当前：' + pairCtx.name;
    var phase = ps ? ps.phase : 'idle';
    var ipText = pairCtx.ip || String(pairCtx.addr || '').replace(/:\d+$/, '');
    var sub = ipText + ' · ';
    if (phase === 'pairing' || phase === 'connecting' || phase === 'verifying') {
      sub += (ps.mode === 'qr') ? '已扫码，正在配对…' : '正在配对…';
    } else {
      sub += '配对端口已自动获取';
    }
    addrEl.textContent = sub;
    el('pair-swap').style.display = n > 1 ? '' : 'none';
    el('pair-go').disabled = pairBusy;
  }

  function pairSetManual(show) {
    if (!pairCtx) return;
    pairCtx.manual = show;
    el('pair-manual').style.display = show ? '' : 'none';
    el('pair-manual-toggle').style.display = show ? 'none' : '';
    el('pair-back-auto').style.display = show ? '' : 'none';
    el('pair-found').classList.toggle('manual', show);
    if (show) {
      pairPrefillAddress();
    } else if (lastState) {
      pairSyncFromState(lastState);
    }
    pairRenderFound(lastState ? lastState.pairStatus : null);
  }

  function pairIdleLike(ps) {
    if (!ps) return null;
    return { phase: 'idle', mode: ps.mode, qrText: ps.qrText, qrExpireAt: ps.qrExpireAt, steps: [] };
  }

  function pairRenderForm(ps, st) {
    var phase = ps ? ps.phase : 'idle';
    var busy = phase === 'pairing' || phase === 'connecting' || phase === 'verifying';
    if (phase === 'failed') {
      pairShowFail(pairFailText(ps));
    } else {
      pairClearFail();
    }
    pairSetBusy(busy);
    pairRenderFound(ps);
    pairRenderStepsInto('pair-steps', ps, false);
    pairRenderQr(ps);
  }

  function renderPairSuccess(ps) {
    if (pairSuccessShown) return;
    pairSuccessShown = true;
    if (pairSuccessTimer) {
      clearTimeout(pairSuccessTimer);
      pairSuccessTimer = null;
    }
    el('pair-form').style.display = 'none';
    el('pair-head').style.display = 'none';
    el('pair-success').style.display = '';
    var nm = (ps.device && (ps.device.name || ps.device.serial)) || ps.name || (pairCtx && pairCtx.name) || '设备';
    el('pair-success-title').textContent = '配对成功，' + nm + ' 已添加';
    var hero = el('pair-success-hero');
    var steps = el('pair-success-steps');
    hero.classList.remove('anim-hero');
    void hero.offsetWidth;
    hero.classList.add('anim-hero');
    steps.classList.remove('anim-steps');
    void steps.offsetWidth;
    steps.classList.add('anim-steps');
    pairRenderStepsInto('pair-success-steps', ps, true);
    el('pair-card').classList.add('anim-out');
    pairSuccessTimer = setTimeout(function () {
      closePairModal();
      refreshNow();
    }, 2400);
  }

  // 快照轮询渲染配对状态（只在弹窗打开时展示；idle 也渲染，以刷新二维码/步骤）
  function renderPairStatus(st) {
    if (!pairOpen || !pairCtx) return;
    var ps = st.pairStatus || null;
    if (ps && ps.phase === 'success') {
      renderPairSuccess(ps);
      return;
    }
    if (pairSuccessShown) return; // 成功动画播放中，等待自动关闭
    el('pair-form').style.display = '';
    el('pair-head').style.display = '';
    el('pair-success').style.display = 'none';
    el('pair-card').classList.remove('anim-out');
    pairSyncFromState(st);
    pairRenderForm(ps, st);
  }

  function closePairModal() {
    pairOpen = false;
    pairCtx = null;
    pairSuccessShown = false;
    pairBusy = false;
    pairFailed = false;
    pairQrLast = null;
    pairQrAutoRefreshKey = null;
    if (pairQrRefreshTimer) {
      clearTimeout(pairQrRefreshTimer);
      pairQrRefreshTimer = null;
    }
    if (pairSuccessTimer) {
      clearTimeout(pairSuccessTimer);
      pairSuccessTimer = null;
    }
    // gui53：mask 淡出（卡片作为子元素跟随一起渐隐）——300ms 后彻底隐藏；
    // anim-out 的移除必须挪到淡出结束后：提前 remove 会让卡片 opacity 弹回 1
    // 实体化（渐变过程又出现配对窗口——2026-09-02 17:03 实测）。
    el('pair-modal').classList.add('mask-fade');
    clearTimeout(pairMaskFadeTimer);
    pairMaskFadeTimer = setTimeout(function () {
      el('pair-modal').style.display = 'none';
      el('pair-modal').classList.remove('mask-fade');
      el('pair-card').classList.remove('anim-out');
      pairMaskFadeTimer = null;
    }, 300);
    el('pair-head').style.display = '';
    el('pair-form').style.display = '';
    el('pair-success').style.display = 'none';
    if (typeof window.PairReset === 'function') {
      try {
        var pr = PairReset();
        if (pr && typeof pr.catch === 'function') pr.catch(function () {});
      } catch (e) { /* 忽略：关闭路径清态失败不影响 UI */ }
    }
  }

  // 配对新设备 → 弹窗（默认自动发现主状态；打开后立即生成二维码）
  // gui51 常驻入口：有 mDNS 待配对设备则默认第一台；无设备也保持自动发现态，
  // 显示「未发现待配对设备」提示，不再默认跳手动填写。
  function openPairModal(p) {
    var pending = (lastState && lastState.pending) || [];
    var idx = 0;
    var target = null;
    if (p) {
      for (var i = 0; i < pending.length; i++) {
        if (pending[i].key === p.key) { idx = i; target = pending[i]; break; }
      }
      if (!target) target = p;
    } else if (pending.length) {
      idx = 0;
      target = pending[0];
    }
    pairCtx = {
      key: target ? target.key : '',
      ip: target ? (target.ip || '') : '',
      pairPort: target ? (target.pairPort || '') : '',
      connPort: target ? pairAddrPort(target.addr || '') : '',
      name: target ? (target.name || '无线调试设备') : '',
      addr: target ? (target.addr || '') : '',
      pending: pending,
      idx: idx,
      manual: false // 默认自动发现态（与有无待配对设备无关）
    };
    pairSuccessShown = false;
    pairBusy = false;
    pairFailed = false;
    pairQrLast = null;
    pairQrAutoRefreshKey = null;
    if (pairQrRefreshTimer) {
      clearTimeout(pairQrRefreshTimer);
      pairQrRefreshTimer = null;
    }
    if (pairSuccessTimer) {
      clearTimeout(pairSuccessTimer);
      pairSuccessTimer = null;
    }
    el('pair-card').classList.remove('anim-out');
    el('pair-head').style.display = '';
    el('pair-form').style.display = '';
    el('pair-success').style.display = 'none';
    el('pair-fail').style.display = 'none';
    el('pair-code-box').classList.remove('err');
    el('pair-seg-box').classList.remove('err');
    pairSetManual(false);
    pairClearCode();
    pairSetBusy(false);
    pairRenderFound(null);
    pairRenderStepsInto('pair-steps', null, false);
    pairRenderQr(null);
    // gui53：打开时取消可能的 mask 淡出（快速开关竞态），立即恢复显示
    clearTimeout(pairMaskFadeTimer);
    pairMaskFadeTimer = null;
    el('pair-modal').classList.remove('mask-fade');
    el('pair-modal').style.display = '';
    pairOpen = true;

    // 打开弹窗即生成/刷新二维码：PairReset 清旧状态 → PairConnect(__qr_open__) 生成
    function qrOpen() {
      if (typeof window.PairConnect === 'function') {
        try {
          var pc = PairConnect('__qr_open__', '', '', '', '');
          if (pc && typeof pc.catch === 'function') pc.catch(function () {});
        } catch (e) { /* 忽略 */ }
      }
      refreshNow();
    }
    if (typeof window.PairReset === 'function') {
      try {
        var pr = PairReset();
        if (pr && typeof pr.then === 'function') {
          pr.then(qrOpen).catch(qrOpen);
        } else {
          qrOpen();
        }
      } catch (e) {
        qrOpen();
      }
    } else {
      qrOpen();
    }
  }

  // ---------- 配对向导事件 ----------
  (function () {
    var inp = pairCodeInput();
    if (!inp) return;
    inp.addEventListener('input', function () {
      var v = inp.value.replace(/\D/g, '').slice(0, 6);
      if (v !== inp.value) inp.value = v;
      if (pairFailed) {
        pairClearFail();
        pairSetBusy(false);
      }
    });
    inp.addEventListener('keydown', function (ev) {
      if (ev.key === 'Backspace' && !inp.value && pairFailed) {
        pairClearFail();
        pairSetBusy(false);
      }
    });
    inp.addEventListener('focus', function () { inp.select(); });
    inp.addEventListener('paste', function (ev) {
      ev.preventDefault();
      var cd = ev.clipboardData || window.clipboardData;
      var text = cd ? (cd.getData('text') || '') : '';
      inp.value = text.replace(/\D/g, '').slice(0, 6);
      if (pairFailed) {
        pairClearFail();
        pairSetBusy(false);
      }
    });
  })();

  (function () {
    var ips = [el('pair-ip1'), el('pair-ip2'), el('pair-ip3'), el('pair-ip4')];
    var port = el('pair-port-seg');
    ips.forEach(function (inp, idx) {
      inp.addEventListener('input', function () {
        var v = inp.value.replace(/\D/g, '');
        if (v !== inp.value) inp.value = v;
        inp.classList.toggle('pre', !inp.value);
        if (v.length >= 3) {
          if (idx < 3) ips[idx + 1].focus();
          else port.focus();
        }
        if (pairFailed) {
          pairClearFail();
          pairSetBusy(false);
        }
      });
      inp.addEventListener('keydown', function (ev) {
        if (ev.key === '.') {
          ev.preventDefault();
          if (idx < 3) ips[idx + 1].focus();
          else port.focus();
        } else if (ev.key === ':' && idx === 3) {
          ev.preventDefault();
          port.focus();
        } else if (ev.key === 'Backspace' && !inp.value && idx > 0) {
          ev.preventDefault();
          ips[idx - 1].focus();
          ips[idx - 1].value = '';
          ips[idx - 1].classList.toggle('pre', !ips[idx - 1].value);
        }
      });
      inp.addEventListener('focus', function () { inp.select(); });
    });
    port.addEventListener('input', function () {
      var v = port.value.replace(/\D/g, '');
      if (v !== port.value) port.value = v;
      port.classList.toggle('pre', !port.value);
      if (pairFailed) {
        pairClearFail();
        pairSetBusy(false);
      }
    });
    port.addEventListener('keydown', function (ev) {
      if (ev.key === 'Backspace' && !port.value) {
        ev.preventDefault();
        ips[3].focus();
        ips[3].value = '';
        ips[3].classList.toggle('pre', !ips[3].value);
      }
    });
    port.addEventListener('focus', function () { port.select(); });
  })();

  el('pair-go').addEventListener('click', function () {
    if (!pairCtx || pairBusy) return;
    if (!pairCtx.manual && (!pairCtx.key || !(pairCtx.pending && pairCtx.pending.length))) return;
    var code = pairCodeValue();
    if (!PairUI.validCode(code)) {
      toast('请输入 6 位数字配对码');
      return;
    }
    var manual = pairCtx.manual;
    var devKey = manual ? '' : pairCtx.key;
    var ip, pairPort, connPort;
    if (manual) {
      ip = pairManualIp();
      pairPort = el('pair-port-seg').value;
      connPort = pairCtx.connPort || '';
      if (!PairUI.validIp(ip)) {
        toast('请输入正确的 IP 地址（四段，0-255）');
        return;
      }
      if (!PairUI.validPort(pairPort)) {
        toast('请输入正确的端口（1-65535）');
        return;
      }
    } else {
      ip = pairCtx.ip;
      pairPort = '';
      connPort = '';
    }
    pairSetBusy(true);
    if (typeof window.PairConnect !== 'function') {
      pairSetBusy(false);
      return;
    }
    try {
      var pc = PairConnect(devKey, ip, pairPort, connPort, code);
      if (pc && typeof pc.then === 'function') {
        pc.then(function () {
          refreshNow();
        }).catch(function (e) {
          toast('配对失败：' + (e && e.message ? e.message : e));
          pairSetBusy(false);
          refreshNow();
        });
      } else {
        refreshNow();
      }
    } catch (e) {
      toast('配对失败：' + (e && e.message ? e.message : e));
      pairSetBusy(false);
    }
  });

  el('pair-swap').addEventListener('click', function () {
    if (!pairCtx || pairBusy) return;
    var list = pairCtx.pending || [];
    if (list.length < 2) return;
    var next = (pairCtx.idx + 1) % list.length;
    var p = list[next];
    pairCtx.idx = next;
    pairCtx.key = p.key;
    pairCtx.ip = p.ip || '';
    pairCtx.pairPort = p.pairPort || '';
    pairCtx.connPort = pairAddrPort(p.addr);
    pairCtx.name = p.name || '无线调试设备';
    pairCtx.addr = p.addr || '';
    pairClearCode();
    pairClearFail();
    pairPrefillAddress();
    if (lastState) pairRenderForm(pairIdleLike(lastState.pairStatus || null), lastState);
  });

  el('pair-manual-toggle').addEventListener('click', function () {
    if (pairBusy) return;
    pairSetManual(true);
    pairClearFail();
    pairSetBusy(false);
    if (lastState) pairRenderForm(pairIdleLike(lastState.pairStatus || null), lastState);
  });

  el('pair-back-auto').addEventListener('click', function () {
    if (pairBusy) return;
    pairSetManual(false);
    pairClearFail();
    pairSetBusy(false);
    if (lastState) pairRenderForm(pairIdleLike(lastState.pairStatus || null), lastState);
    refreshNow(); // 回到自动发现：触发后端重新扫描，下轮快照同步待配对列表
  });

  el('pair-qr-area').addEventListener('click', function () {
    if (pairBusy || typeof window.PairConnect !== 'function') return;
    try {
      var pc = PairConnect('__qr_refresh__', '', '', '', '');
      if (pc && typeof pc.catch === 'function') pc.catch(function () {});
    } catch (e) { /* 忽略 */ }
    refreshNow();
  });

  el('pair-close').addEventListener('click', function () { closePairModal(); refreshNow(); });
  el('pair-modal').addEventListener('click', function (ev) {
    if (ev.target === el('pair-modal')) {
      closePairModal();
      refreshNow();
    }
  });

  // ---------- gui51：批量管理事件 ----------
  el('batch-toggle').addEventListener('click', function () { setBatchMode(!batchMode); });
  el('batch-x').addEventListener('click', function () { setBatchMode(false); });
  el('batch-del').addEventListener('click', function () { if (lastState) batchDeleteOpen(lastState); });
  el('batch-rename').addEventListener('click', function () { if (lastState) startBatchRename(lastState); });
  el('batch-rename-ok').addEventListener('click', function () { if (lastState) finishBatchRename(lastState); });
  el('batch-cast').addEventListener('click', function () { if (lastState) batchStartCast(lastState); });
  el('batch-confirm-cancel').addEventListener('click', function () { el('batch-confirm').style.display = 'none'; });
  el('batch-confirm-ok').addEventListener('click', function () { batchDeleteConfirm(lastState); });
  el('batch-confirm').addEventListener('click', function (ev) {
    if (ev.target === el('batch-confirm')) el('batch-confirm').style.display = 'none';
  });
  // 右键单删气泡：点气泡外任意处关闭（捕获阶段，兼容卡片内 stopPropagation）
  document.addEventListener('click', function (ev) {
    if (delBubbleEl && !delBubbleEl.contains(ev.target)) closeDeleteBubble();
  }, true);

  // ---------- 应用内更新：关闭弹窗不取消；安装由用户明确触发 ----------
  var updateState = null, updateSeenVersion = '', updateResultSeen = false, updateReading = false, updateRequest = false;
  var updateConfirm = false;
  var updateLocalError = '';
  function updateVisible(id, yes) { el(id).style.display = yes ? '' : 'none'; }
  function renderUpdate(s) {
    updateState = s; var info = s.info || {}, m = window.SCEZUpdateUI.model(s);
    var latestKey = (info.latest || '').replace(/^v/, '');
    if (el('update-modal').style.display !== 'none') {
      if (info.hasNew) updateSeenVersion = latestKey;
      if (s.result) updateResultSeen = true;
    }
    if ((info.hasNew && latestKey !== updateSeenVersion) || (s.result && !updateResultSeen)) el('app-ver').classList.add('has-new');
    else el('app-ver').classList.remove('has-new');
    el('update-current').textContent = info.current || (lastState && lastState.version) || '-';
    el('update-latest').textContent = info.latest || (s.phase === 'checking' ? '检查中…' : '未知');
    el('update-source').textContent = 'root 尝试版独立分支（自动更新停用）';
    el('update-notice').textContent = m.notice; updateVisible('update-notice', !!m.notice);
    el('update-state').textContent = '此版本尚未进行 root 设备实测';
    el('update-state').className = 'update-state' + (s.error ? ' warn' : info.hasNew ? ' new' : '');
    el('update-error').textContent = updateLocalError || m.error; updateVisible('update-error', !!(updateLocalError || m.error));
    updateVisible('update-progress', m.progress);
    if (m.percent === null) el('update-progress-bar').removeAttribute('value');
    else el('update-progress-bar').value = m.percent;
    el('update-progress-text').textContent = m.detail;
    updateVisible('update-download', m.download && !updateConfirm);
    el('update-download').textContent = m.downloadText;
    updateVisible('update-install', m.install && !updateConfirm);
    updateVisible('update-cancel', m.cancel);
    updateVisible('update-check', m.check && !updateConfirm);
    updateVisible('update-later', m.later && !updateConfirm);
    el('update-later').textContent = s.canInstall ? '稍后' : '关闭';
    el('update-close').style.visibility = s.phase === 'installing' ? 'hidden' : '';
    updateVisible('update-result', !!s.result);
    updateVisible('update-result-dismiss', !!s.result);
    if (s.result) el('update-result').textContent = s.result.ok ? '已完成更新：' + s.result.version : '上次更新未完成：' + s.result.error;
    ['update-download', 'update-install', 'update-check', 'update-confirm-ok'].forEach(function (id) { el(id).disabled = updateRequest; });
  }
  function readUpdate() {
    if (updateReading || typeof window.GetUpdateState !== 'function') return Promise.resolve(updateState);
    updateReading = true;
    return window.GetUpdateState().then(function (s) { renderUpdate(s); return s; })
      .catch(function () { return updateState; }).finally(function () { updateReading = false; });
  }
  function updateCall(name, args) {
    if (updateRequest) return Promise.resolve(null);
    updateRequest = true; updateLocalError = ''; if (updateState) renderUpdate(updateState);
    return window[name].apply(window, args || []).catch(function (e) {
      updateLocalError = (e && e.message) || String(e); el('update-error').textContent = updateLocalError; updateVisible('update-error', true);
      return null;
    }).finally(function () { updateRequest = false; readUpdate(); });
  }
  function clearUpdateConfirm() { updateConfirm = false; updateVisible('update-confirm', false); if (updateState) renderUpdate(updateState); }
  function closeUpdateModal() {
    if (updateState && updateState.phase === 'installing') return;
    clearUpdateConfirm(); el('update-modal').style.display = 'none';
    if (updateState && updateState.result && updateState.result.ok) updateCall('DismissUpdateResult');
  }
  function openUpdateModal() {
    el('app-ver').classList.remove('has-new');
    el('update-modal').style.display = ''; clearUpdateConfirm();
    readUpdate().then(function (s) { if (!s || s.phase === 'idle') updateCall('BeginUpdateCheck'); });
  }
  function installUpdate(confirmed) {
    updateCall('InstallUpdate', [confirmed]).then(function (reply) {
      if (reply && reply.needsConfirm) {
        updateConfirm = true;
        el('update-confirm-text').textContent = '目前有 ' + reply.activeWindows + ' 个投屏窗口。更新将结束这些窗口，安装后需要重新投屏。';
        updateVisible('update-confirm', true); renderUpdate(updateState);
      } else if (reply) clearUpdateConfirm();
    });
  }
  setTimeout(function () { readUpdate().then(function (s) { if (!s || !s.canInstall) updateCall('BeginUpdateCheck'); }); }, 2500);
  setInterval(function () {
    if (!updateState || el('update-modal').style.display !== 'none' || ['checking','selecting','downloading','validating','installing'].indexOf(updateState.phase) >= 0) readUpdate();
  }, 700);
  function openUpdateLink(kind) {
    var url = 'https://github.com/kinewe/PC-kinewe-yinmo';
    if (typeof window.OpenURL === 'function') {
      window.OpenURL(url).catch(function (e) {
        toast('打开失败：' + (e && e.message ? e.message : e));
      });
    }
  }
  el('update-close').addEventListener('click', closeUpdateModal);
  el('update-later').addEventListener('click', closeUpdateModal);
  el('update-modal').addEventListener('click', function (ev) {
    if (ev.target === el('update-modal')) closeUpdateModal();
  });
  el('update-repo').addEventListener('click', function () { openUpdateLink('repo'); });
  el('update-gitee').addEventListener('click', function () { openUpdateLink('gitee'); });
  el('update-download').addEventListener('click', function () { updateCall('DownloadUpdate'); });
  el('update-check').addEventListener('click', function () { updateCall('BeginUpdateCheck'); });
  el('update-cancel').addEventListener('click', function () { updateCall('CancelUpdate'); });
  el('update-install').addEventListener('click', function () { installUpdate(false); });
  el('update-confirm-ok').addEventListener('click', function () { installUpdate(true); });
  el('update-confirm-cancel').addEventListener('click', closeUpdateModal);
  el('update-result-dismiss').addEventListener('click', function () { updateCall('DismissUpdateResult'); });
  el('app-ver').title = 'root 尝试版说明';
  el('app-ver').style.cursor = 'pointer';
  el('app-ver').addEventListener('click', openUpdateModal);


  // ---------- 参数状态机（参数浮窗专用；纯函数 SCEZParamState 单测覆盖） ----------
  // 结构：{serial, devName, mode, profile:{usb,wifi},
  //        fields:{usb:{res,fps,bit},wifi:{...}}（每栏 SCEZParamState 纯状态机）, activeField}
  var paramState = null;

  var PARAM_FIELDS = {
    res: { label: '分辨率档', unit: '', tiers: [2560, 1920, 1080, 720] },
    fps: { label: '刷新率上限', unit: ' fps', tiers: [120, 90, 60, 30] },
    bit: { label: '码率上限', unit: ' Mbps', tiers: [120, 80, 60, 40, 15] }
  };
  var PARAM_NOTES = {
    res: '分辨率说明：长边档位/自定义长边，短边按设备比例自动计算（如 2136x3200 → 2400x1602），不会裁切',
    fps: '刷新率说明：低于现有档位时上级档自动忽略（80fps → 80/60/30）；高于现有上限替换上限（200 → 200/90/60/30，级数不变）',
    bit: '码率说明：同理（55M → 55/40/15；150M → 150/80/60/40/15，级数不变）',
    audio: '声音说明：手机＝只在手机出声（不传输到电脑）；电脑＝只在电脑出声（手机静音；手机音量键同步控制投屏音量）；两边＝电脑与手机同时出声（需 Android 13+；两边音量相互独立——投屏音量请用电脑调）',
    vcodec: '视频编码说明：H.264 兼容性最好（默认）；H.265 同画质更省带宽；AV1、VP8、VP9 多数设备仅有软编码器，可能不流畅——可用性取决于设备端编码器',
    acodec: '音频编码说明：Opus 兼容性最佳（默认）；FLAC 无损、体积较大；AAC 通用；RAW 未压缩'
  };
  var PARAM_FALLBACK = {
    usb: { res: 2560, fps: 120, bitrate: 60 },
    wifi: { res: 1920, fps: 60, bitrate: 15 }
  };

  // v2.1.91：编码格式选项（协议层支持集；实际可用性取决于设备端编码器）
  var VCODEC_OPTS = ['h264', 'h265', 'av1', 'vp8', 'vp9'];
  var ACODEC_OPTS = ['opus', 'aac', 'flac', 'raw'];

  // v2.1.92：界面显示名（H.264 规范写法；内部值保持小写不动）
  var VCODEC_LABELS = { h264: 'H.264', h265: 'H.265', av1: 'AV1', vp8: 'VP8', vp9: 'VP9' };
  var ACODEC_LABELS = { opus: 'Opus', aac: 'AAC', flac: 'FLAC', raw: 'RAW' };
  function fmtVCodec(v) { return VCODEC_LABELS[v] || v; }
  function fmtACodec(v) { return ACODEC_LABELS[v] || v; }
  function fmtKeyboardMode(v) { return String(v || '').toUpperCase(); }

  // 档位列表 = 阶梯构造：最左=baseline 值（选中），右侧=标准档中小于 baseline 的
  function ladderTiers(std, x) {
    if (!x || x <= 0) return std.slice();
    if (x >= std[0]) {
      var out = [x];
      for (var i = 1; i < std.length; i++) out.push(std[i]);
      return out;
    }
    var out2 = [x];
    std.forEach(function (t) { if (t < x) out2.push(t); });
    return out2;
  }

  function baselineOf(state, p) {
    var fb = PARAM_FALLBACK[state.mode];
    if (p.baseline && p.baseline.res > 0) return p.baseline;
    return { res: fb.res, fps: fb.fps, bitrate: fb.bitrate };
  }

  function curParam(state) { return state.profile[state.mode]; }
  function curField(state, f) { return state.fields[state.mode][f]; }
  function baseValOf(base, f) { return f === 'res' ? base.res : (f === 'fps' ? base.fps : base.bitrate); }

  // 字段状态机转换入口：写入新状态并同步 profile 值（重绘由调用方 redraw 完成）
  function applyField(state, f, st) {
    var p = state.profile[state.mode];
    if (f === 'res') p.res = st.val; else if (f === 'fps') p.fps = st.val; else p.bitrate = st.val;
    state.fields[state.mode][f] = st;
    state.profile[state.mode] = p;
  }

  // 打开时的字段状态推断（纯函数 SCEZParamState.infer）
  function initField(p, f, tiers, fb) {
    var val = f === 'res' ? p.res : (f === 'fps' ? p.fps : p.bitrate);
    var base = baseValOf(fb, f);
    return SCEZParamState.infer(
      { val: val, base: base, custom: p.custom, edit: false, input: '' },
      ladderTiers(tiers, base));
  }

  // 构造一套参数状态（浮窗/管理页共用）：两套模式字段全部初始化（每栏独立状态机）
  function makeParamState(serial, devName, mode, p) {
    var usb = p.usb || { res: 2560, fps: 120, bitrate: 60, custom: false, baseline: null };
    var wifi = p.wifi || { res: 1920, fps: 60, bitrate: 15, custom: false, baseline: null };
    var usbFB = (usb.baseline && usb.baseline.res > 0) ? usb.baseline : PARAM_FALLBACK.usb;
    var wifiFB = (wifi.baseline && wifi.baseline.res > 0) ? wifi.baseline : PARAM_FALLBACK.wifi;
    return {
      serial: serial,
      devName: devName,
      mode: mode,
      activeField: 'res',
      profile: { usb: usb, wifi: wifi },
      // v2.1.78：声音档位（两套独立记忆；后端 GetProfile 已归一为最终值，空兜底 pc）
      audio: { usb: (usb.audio || 'pc'), wifi: (wifi.audio || 'pc') },
      // v2.1.91：编码格式（两套独立记忆；空兜底默认）
      vcodec: { usb: (usb.vcodec || 'h264'), wifi: (wifi.vcodec || 'h264') },
      acodec: { usb: (usb.acodec || 'opus'), wifi: (wifi.acodec || 'opus') },
      // 打开推断——仅"已自定义且值不在档位列表"才进输入态，且输入框=自定义值（非空）
      fields: {
        usb: {
          res: initField(usb, 'res', PARAM_FIELDS.res.tiers, usbFB),
          fps: initField(usb, 'fps', PARAM_FIELDS.fps.tiers, usbFB),
          bit: initField(usb, 'bit', PARAM_FIELDS.bit.tiers, usbFB)
        },
        wifi: {
          res: initField(wifi, 'res', PARAM_FIELDS.res.tiers, wifiFB),
          fps: initField(wifi, 'fps', PARAM_FIELDS.fps.tiers, wifiFB),
          bit: initField(wifi, 'bit', PARAM_FIELDS.bit.tiers, wifiFB)
        }
      }
    };
  }

  // updateParamAudioRow：刷新主屏声音行（标签/滑条高亮/说明）——静态 DOM 局部更新。
  // v2.1.78 修复：拖动滑条只走这里，绝不重建面板（重建会让指针捕获失效、拖动断链——
  // 与应用屏滑条手感不一致的根因）。
  function updateParamAudioRow(state) {
    var cur = (state && state.audio && state.audio[state.mode]) || 'pc';
    el('param-audio-label').innerHTML = '声音 (<b>' + SCEZ_AUDIO_LABELS[cur] + '</b>)';
    renderAudioSlider(el('param-audio-slider'), cur);
    el('param-audio-note').textContent = audioNote(cur);
  }

  // v2.1.91：编码格式行（视频/音频）——chip 单选 + 局部刷新。
  function renderCodecChips(boxId, opts, cur, onPick, fmt) {
    var box = el(boxId);
    if (!box) return;
    box.innerHTML = '';
    opts.forEach(function (v) {
      var c = document.createElement('button');
      c.className = 'chip' + (v === cur ? ' sel' : '');
      c.textContent = fmt ? fmt(v) : v;
      c.addEventListener('click', function () { onPick(v); });
      box.appendChild(c);
    });
  }
  function updateParamCodecRows(state) {
    if (!state) return;
    var v = state.vcodec[state.mode] || 'h264';
    var a = state.acodec[state.mode] || 'opus';
    el('param-vcodec-label').innerHTML = '视频编码 (<b>' + fmtVCodec(v) + '</b>)';
    el('param-acodec-label').innerHTML = '音频编码 (<b>' + fmtACodec(a) + '</b>)';
    renderCodecChips('param-vcodec-chips', VCODEC_OPTS, v, function (nv) {
      state.vcodec[state.mode] = nv;
      state.activeField = 'vcodec'; // v2.1.92：联动底部说明区
      renderParamModal();
    }, fmtVCodec);
    renderCodecChips('param-acodec-chips', ACODEC_OPTS, a, function (na) {
      state.acodec[state.mode] = na;
      state.activeField = 'acodec'; // v2.1.92：联动底部说明区
      renderParamModal();
    }, fmtACodec);
  }

  // 三栏档位渲染（浮窗用；redraw=字段变更后的重绘回调）：
  // 档位大→左排、＋自定义固定最右、点击就地变输入框（空值=不传）、重置=回 bat 基线
  function renderParamRows(state, box, noteEl, redraw) {
    box.innerHTML = '';
    Object.keys(PARAM_FIELDS).forEach(function (f) {
      var fd = PARAM_FIELDS[f];
      var p = curParam(state);
      var base = baselineOf(state, p);
      var baseVal = f === 'res' ? base.res : (f === 'fps' ? base.fps : base.bitrate);
      var st = curField(state, f);
      var val = st.val;
      var explicit = st.edit; // 本栏是否处于自定义输入态
      var tiers = ladderTiers(fd.tiers, baseVal); // 最左=bat 基准默认值

      // v2.1.79：刷新率/码率栏加「锁定」按钮——锁定后该维度不被 ABR 自适应调整
      var lockable = (f === 'fps' || f === 'bit');
      var isLocked = lockable && (f === 'fps' ? !!p.lockFps : !!p.lockBitrate);
      var row = document.createElement('div');
      row.className = 'param-row';
      row.innerHTML =
        '<div class="param-rowhead">' +
          '<span class="param-label">' + fd.label + ' (<b>' + val + fd.unit + '</b>)</span>' +
          '<span class="param-actions">' +
            (lockable ? '<button class="param-lock' + (isLocked ? ' on' : '') + '">' + (isLocked ? '已锁定' : '锁定') + '</button>' : '') +
            '<button class="param-reset">重置</button>' + // 任何时候可点
          '</span>' +
        '</div>' +
        '<div class="chips"></div>';
      var chips = row.querySelector('.chips');
      tiers.forEach(function (t) {
        var c = document.createElement('button');
        c.className = 'chip' + (val === t ? ' sel' : '');
        c.textContent = String(t);
        // 修复：点击标准档位 = 清除该栏自定义（输入框消失；值=该档）
        c.addEventListener('click', function () {
          applyField(state, f, SCEZParamState.clickTier(st, t));
          if (redraw) redraw();
        });
        chips.appendChild(c);
      });
      if (explicit) {
        // ＋自定义 就地变成输入框（仅本栏）；打开推断时内容=自定义值（非空）
        var inp = document.createElement('input');
        inp.type = 'number';
        inp.min = '1';
        inp.className = 'chip-input';
        inp.placeholder = '输入后生效';
        inp.value = st.input;
        inp.addEventListener('input', function () {
          var st2 = curField(state, f);
          state.fields[state.mode][f] = SCEZParamState.typeCustom(st2, inp.value);
        });
        inp.addEventListener('change', function () {
          applyField(state, f, SCEZParamState.commitCustom(curField(state, f), curField(state, f).input));
          if (redraw) redraw();
        });
        chips.appendChild(inp);
      } else {
        var cb = document.createElement('button');
        cb.className = 'chip dashed';
        cb.textContent = '＋自定义';
        cb.addEventListener('click', function () {
          applyField(state, f, SCEZParamState.startCustom(curField(state, f)));
          if (redraw) redraw();
        });
        chips.appendChild(cb);
      }

      row.addEventListener('click', function () {
        state.activeField = f;
        noteEl.textContent = PARAM_NOTES[f];
      });
      var lockBtn = row.querySelector('.param-lock');
      if (lockBtn) {
        lockBtn.addEventListener('click', function (ev) {
          ev.stopPropagation();
          // 锁定切换：独立于值（重置只管值，锁管"不被自动调整"的开关）
          if (f === 'fps') {
            p.lockFps = !p.lockFps;
          } else {
            p.lockBitrate = !p.lockBitrate;
          }
          if (redraw) redraw();
        });
      }
      row.querySelector('.param-reset').addEventListener('click', function (ev) {
        ev.stopPropagation();
        // 重置=回 bat 基准默认（本栏）；常可用 + 点击动画反馈
        var btn = ev.currentTarget;
        btn.classList.remove('flash');
        void btn.offsetWidth; // 重启动画
        btn.classList.add('flash');
        setTimeout(function () { btn.classList.remove('flash'); }, 220);
        applyField(state, f, SCEZParamState.resetField(curField(state, f)));
        if (redraw) redraw();
      });
      box.appendChild(row);
    });
    // v2.1.78：声音档位行（静态 DOM——此处只刷新；拖动时绝不重建，避免滑条指针链断掉）。
    updateParamAudioRow(state);
    updateParamCodecRows(state); // v2.1.91：编码格式行刷新
    noteEl.textContent = PARAM_NOTES[state.activeField] || PARAM_NOTES.res; // v2.1.92：未知栏位兜底
  }

  // ---------- 参数浮窗（投屏中快捷调参；按当前投屏模式自动定位，保持现状） ----------
  function openSpecModal(serial) {
    var name = serial;
    var mode = 'usb';
    if (lastState) {
      var ss = (lastState.sessions || []).filter(function (s) { return s.serial === serial; })[0];
      if (ss && ss.devName) name = ss.devName;
      if (ss && ss.mode) mode = ss.mode;
      if (!mode && ss && ss.cast && ss.cast.mode) mode = ss.cast.mode;
      if (!mode && lastState.cast && lastState.cast.serial === serial && lastState.cast.mode) {
        mode = lastState.cast.mode;
      }
    }
    if (sessions[serial] && name === serial) {
      name = sessions[serial].tab.querySelector('.casttab-name').textContent;
    }
    GetProfile(serial).then(function (p) {
      paramState = makeParamState(serial, name, mode, p);
      renderParamModal();
      el('param-modal').style.display = '';
    }).catch(function (e) {
      toast('读取参数失败：' + (e && e.message ? e.message : e));
    });
  }

  function renderParamModal() {
    if (!paramState) return;
    var modeName = paramState.mode === 'usb' ? '有线模式' : '无线模式';
    el('param-title').textContent = paramState.devName + ' · ' + modeName + ' · 自定义参数';
    // 只读展示当前模式（浮窗按当前投屏模式自动定位，不可手动切换）
    el('param-mode').textContent = '当前投屏模式：' + modeName;
    renderParamRows(paramState, el('param-rows'), el('param-note'), renderParamModal);
  }

  // v2.1.78：声音滑条（静态元素一次性绑定）——拖动只刷新本行，不重建面板（手感与
  // 应用屏滑条一致；此前的动态重建版本拖动会断链）。
  bindAudioSlider(el('param-audio-slider'), function (v) {
    if (!paramState) return;
    paramState.audio[paramState.mode] = v;
    updateParamAudioRow(paramState);
  });
  el('param-audio-row').addEventListener('click', function () {
    if (!paramState) return;
    paramState.activeField = 'audio';
    el('param-note').textContent = PARAM_NOTES.audio;
  });
  el('param-vcodec-row').addEventListener('click', function () {
    if (!paramState) return;
    paramState.activeField = 'vcodec';
    el('param-note').textContent = PARAM_NOTES.vcodec;
  });
  el('param-acodec-row').addEventListener('click', function () {
    if (!paramState) return;
    paramState.activeField = 'acodec';
    el('param-note').textContent = PARAM_NOTES.acodec;
  });
  // v2.1.96：编码/声音行「重置」——跳默认初始值（H.264 / Opus / 电脑）。
  bindResetFlash('param-vcodec-reset', function () {
    if (!paramState) return;
    paramState.vcodec[paramState.mode] = 'h264';
    renderParamModal();
  });
  bindResetFlash('param-acodec-reset', function () {
    if (!paramState) return;
    paramState.acodec[paramState.mode] = 'opus';
    renderParamModal();
  });
  bindResetFlash('param-audio-reset', function () {
    if (!paramState) return;
    paramState.audio[paramState.mode] = 'pc';
    updateParamAudioRow(paramState);
  });
  el('param-cancel').addEventListener('click', function () { el('param-modal').style.display = 'none'; });
  el('param-close').addEventListener('click', function () { el('param-modal').style.display = 'none'; });
  el('param-modal').addEventListener('click', function (ev) {
    if (ev.target === el('param-modal')) el('param-modal').style.display = 'none';
  });
  el('param-save').addEventListener('click', function () {
    if (!paramState) return;
    var p = curParam(paramState);
    var base = baselineOf(paramState, p);
    // 每栏独立结算（纯函数 SCEZParamState.settle）：
    //   输入态且输入非空 → 自定义值=输入；非输入态值≠baseline（档位点击）→ 自定义值=档位；
    //   否则 baseline（自动档，不注入）
    function fieldSettle(f) {
      var st = curField(paramState, f);
      return SCEZParamState.settle({ val: st.val, base: baseValOf(base, f), custom: false, edit: st.edit, input: st.input });
    }
    var r = fieldSettle('res');
    var s2 = fieldSettle('fps');
    var b = fieldSettle('bit');
    var custom = r.custom || s2.custom || b.custom;
    // 立即反馈：先关面板 + 提示（不等 SaveProfileAndRestart 全流程完成——
    // 后端 RestartCast 要等杀树/重启/设备上线，挂在其 Promise 后关面板会"晚关"）；
    // 保存失败时面板已关：仅错误 toast（参数未生效，用户可重新打开浮窗重试）。
    el('param-modal').style.display = 'none';
    toast('已保存，正在以新参数重新投屏…');
    SaveProfileAndRestart(paramState.serial, paramState.mode, r.value, s2.value, b.value, custom, paramState.audio[paramState.mode], !!p.lockFps, !!p.lockBitrate, paramState.vcodec[paramState.mode], paramState.acodec[paramState.mode]).then(function () {
      refreshNow();
    }).catch(function (e) {
      toast('保存失败：' + (e && e.message ? e.message : e));
    });
  });

  // ---------- 指引弹窗（右上角灯泡按钮；原连接向导教程的精简版） ----------
  el('btn-guide').addEventListener('click', function () { el('guide-modal').style.display = ''; });
  el('guide-close').addEventListener('click', function () { el('guide-modal').style.display = 'none'; });
  el('guide-done').addEventListener('click', function () { el('guide-modal').style.display = 'none'; });
  el('guide-modal').addEventListener('click', function (ev) {
    if (ev.target === el('guide-modal')) el('guide-modal').style.display = 'none';
  });

  // ---------- 设置面板（右上角齿轮；两个开关即时保存，重启后保持） ----------
  // Go 侧 settings.json 是权威值：每次快照回家；面板打开时按它渲染开关状态。
  // 点击开关先本地翻转（即时反馈）再调 SetSettings 落盘，失败提示（快照下一轮会纠正）。
  var settingsState = { showParamOverlay: true, closeToTray: false, otherAppWinSystemDecorations: true };

  function setSwitch(node, on) {
    if (!node) return;
    node.classList[on ? 'add' : 'remove']('on');
    node.setAttribute('aria-checked', on ? 'true' : 'false');
  }

  function renderSettings() {
    setSwitch(el('set-overlay'), settingsState.showParamOverlay);
    setSwitch(el('set-tray'), settingsState.closeToTray);
    setSwitch(el('set-appwin-decor'), settingsState.otherAppWinSystemDecorations);
  }

  function syncSettings(st) {
    if (!st || !st.settings) return;
    settingsState.showParamOverlay = !!st.settings.showParamOverlay;
    settingsState.closeToTray = !!st.settings.closeToTray;
    settingsState.otherAppWinSystemDecorations = st.settings.otherAppWinSystemDecorations !== false;
    // 面板没开时不碰 DOM（避免与用户点击抢状态）
    if (el('settings-modal').style.display !== 'none') renderSettings();
  }

  function saveSettings() {
    SetSettings(settingsState.showParamOverlay, settingsState.closeToTray).catch(function (e) {
      toast('设置保存失败：' + (e && e.message ? e.message : e));
    });
  }

  el('btn-settings').addEventListener('click', function () {
    syncSettings(lastState);
    renderSettings();
    el('settings-modal').style.display = '';
  });
  el('settings-close').addEventListener('click', function () { el('settings-modal').style.display = 'none'; });
  el('settings-modal').addEventListener('click', function (ev) {
    if (ev.target === el('settings-modal')) el('settings-modal').style.display = 'none';
  });
  el('set-overlay').addEventListener('click', function () {
    settingsState.showParamOverlay = !settingsState.showParamOverlay;
    renderSettings();
    saveSettings();
  });
  el('set-tray').addEventListener('click', function () {
    settingsState.closeToTray = !settingsState.closeToTray;
    renderSettings();
    saveSettings();
  });
  el('set-appwin-decor').addEventListener('click', function () {
    settingsState.otherAppWinSystemDecorations = !settingsState.otherAppWinSystemDecorations;
    renderSettings();
    SetOtherAppWinSystemDecorations(settingsState.otherAppWinSystemDecorations).catch(function (e) {
      toast('设置保存失败：' + (e && e.message ? e.message : e));
    });
  });

  var lastProfileSaveError = '';
  // ---------- 轮询主循环 ----------
  function refreshNow() {
    tickAppWins(); // 二期：应用面板数据轮询（枚举未完成时重拉；独立于快照 diff）
    GetState().then(function (st) {
      lastState = st;
      if (st.profileSaveError && st.profileSaveError !== lastProfileSaveError) toast('设备档案保存失败，当前连接可继续使用：' + st.profileSaveError);
      lastProfileSaveError = st.profileSaveError || '';
      syncSettings(st);
      syncAppWins(st.appWins || []);
      var j = JSON.stringify(st);
      if (j !== lastJson) {
        lastJson = j;
        renderDevices(st);
        renderSessions(st);
        renderNewDevice(st);
        renderPairStatus(st);
      }
      syncAppEntryMasks();
    }).catch(function (e) {
      toast('桥接异常：' + (e && e.message ? e.message : e));
    });
  }

  // 刷新 = 立即搜索设备：ForceDiscover 清节流后走轮询链路（mDNS 扫描 +
  // 档案地址分层 connect 立刻执行，不等 15s 节流）；点击即时反馈"正在扫描"。
  el('btn-refresh').addEventListener('click', function () {
    toast('正在扫描设备…');
    ForceDiscover().then(function (st) {
      lastState = st;
      lastJson = JSON.stringify(st);
      renderDevices(st);
      renderSessions(st);
      renderNewDevice(st);
      renderPairStatus(st);
    }).catch(function () {});
  });

  // 窗口总览是常驻 tab：切回设备页靠顶部 tabs（无返回键）；
  // 空态"去设备"按钮同样只是切 tab（投屏继续运行）
  el('cast-empty-go').addEventListener('click', function () { switchView('devices'); });

  // 每 700ms 读取后台状态快照，日志与投屏状态可更新；设备连接监测独立运行。
  pollTimer = setInterval(refreshNow, 700);
  refreshNow();

  // 页面就绪 → 通知 Go 层可获取 HWND（托盘"显示主窗口"依赖）
  window.addEventListener('load', function () {
    if (typeof window.UiReady === 'function') window.UiReady();
  });
})();
