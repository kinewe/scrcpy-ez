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
//   SaveProfileAndRestart(serial, mode, res, fps, bitrate, custom) -> error（浮窗保存并重投）
//   SaveProfile(serial, mode, res, fps, bitrate, custom) -> error（Go 保留接口；前端无独立入口）
//   PairConnect(devKey, ip, pairPort, connPort, code) -> error（无线调试配对向导：受理即返回，状态经 pairStatus 轮询）
//   PairReset() -> error（清配对向导状态）
//   RefreshNow() -> Snapshot JSON
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
      if (d.wirelessRes) parts.push(d.wirelessRes + ' · ' + (d.fps ? d.fps + 'fps' : '60fps'));
    } else {
      parts.push('其他');
      if (d.serial) parts.push(d.serial);
    }
    return parts.join(' · ');
  }

  // ---------- Tab 切换（设备/窗口总览；顶部共享栏常驻，tab 仅点击切换、禁止拖拽 gui28） ----------
  function switchView(name) {
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
    t.addEventListener('click', function () { switchView(t.getAttribute('data-view')); });
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

  function appendPairEntry(box) {
    var pe = document.createElement('button');
    pe.type = 'button';
    pe.id = 'pair-entry';
    pe.className = 'pair-entry';
    // gui53：入口按钮前置「+」（行动号召）；DOM 构造，无用户数据注入
    pe.innerHTML = '<span class="pe-plus">+</span>从「无线调试」配对新设备';
    pe.addEventListener('click', function () { openPairModal(null); });
    box.appendChild(pe);
  }

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
      obj[k] = (drafts[k] !== undefined) ? drafts[k].trim() : '';
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
    if (started) toast('已开始投屏 ' + started + ' 台');
  }

  function renderDevices(st) {
    // gui44：拖拽排序进行中跳过重建（下轮快照照常，轮询继续）
    if (el('device-list').classList.contains('drag-active')) return;
    sweepDeletingKeys(); // gui52-fix16e：超 15s 的删除中残留标记过期（防粘死）
    el('app-ver').textContent = st.version || '';
    el('dev-count').textContent = String(st.devices.length);
    syncBatchUI(st);

    var box = el('device-list');
    box.innerHTML = '';
    box.classList.toggle('batch', batchMode);
    if (!st.adbOK) {
      var bad = document.createElement('div');
      bad.className = 'banner error';
      bad.textContent = '⚠ 无法连接 adb（adb.exe 缺失或未响应），请检查程序目录';
      box.appendChild(bad);
      appendPairEntry(box);
      return;
    }
    // 真空期去抖：adb 仍可用但当前连续失败（点投屏后 bat 重置 adb 服务的 1-2s 真空）——
    // 保留上次设备列表并给出"刷新中"软提示，不亮红条；恢复后下一轮快照自动清除。
    if (st.adbFailing) {
      var trying = document.createElement('div');
      trying.className = 'banner info';
      trying.textContent = '⟳ 设备列表刷新中…（adb 服务忙，稍候自动恢复）';
      box.appendChild(trying);
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
      box.appendChild(tip);
      appendPairEntry(box);
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

      var card = document.createElement('div');
      card.dataset.key = key; // gui44：拖拽排序 key
      card.className = 'device-card';
      // gui52-fix15：删除中 —— 整卡灰化禁点，视口过渡按 key 命名（删除后下方卡片平滑上移）
      var deletingCard = !!deletingKeys[key];
      if (deletingCard) card.className += ' deleting';
      card.style.viewTransitionName = 'dev-' + vtSafeName(key);
      if (batchMode) {
        // gui51：批量模式——灰框未选 / 绿框选中；整卡点击切换选中
        card.className += selected ? ' selected' : ' dim';
        card.addEventListener('click', function (ev) {
          if (ev.target && ev.target.classList && ev.target.classList.contains('rename-input')) return;
          toggleBatchSelect(key, lastState);
        });
      } else {
        // gui53：普通模式所有卡片统一边框（不再给第一张卡单独 .selected 绿框——
        // 选中态只属于批量管理模式；第一卡绿描边只会问"为什么只有它有"）。
        card.className += (casting ? ' casting' : '');
        if (casting) {
          // 投屏中卡片整体可点：跳转到投屏中标签页（纯导航）
          card.title = '查看投屏中页面';
          card.addEventListener('click', function () { switchView('cast'); });
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
      dot.className = 'dot ' + (online ? (d.connType === 'usb' ? 'usb' : 'wifi') : 'off') + (casting ? ' casting' : '');

      var info = document.createElement('div');
      info.className = 'dev-info';
      if (inRename) {
        // gui51：改名模式——选中卡名称就地变输入框（绿边），只显示输入框
        var rn = document.createElement('input');
        rn.type = 'text';
        rn.className = 'rename-input';
        rn.value = (renameDraft[key] !== undefined) ? renameDraft[key] : devDisplayName(d);
        rn.addEventListener('input', function () { renameDraft[key] = rn.value; });
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

      box.appendChild(card);
    });

    // gui50-fix2：待配对卡已从设备列表移除（配对新设备唯一入口=列表底部常驻按钮）
    // gui51：配对新设备常驻入口（列表下方整宽虚线按钮；空列表也常驻）
    appendPairEntry(box);
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
  function ensureSession(serial, info) {
    if (sessions[serial]) return sessions[serial];

    var tab = document.createElement('div');
    tab.className = 'casttab';
    tab.setAttribute('data-key', serial); // 拖拽排序的 key（会话串号）
    tab.innerHTML = '<span class="casttab-dot"></span><span class="casttab-name"></span>';
    tab.querySelector('.casttab-name').textContent = (info && info.devName) || serial;
    tab.title = serial;
    tab.addEventListener('click', function () {
      activateSession(serial);
      // 标签点击 = 对应投屏窗口浮前（z-order 置顶，不夺 ez 焦点；
      // fire-and-forget：失败不阻塞标签切换）
      if (typeof window.BringCastToFront === 'function') {
        try {
          var p = window.BringCastToFront(serial);
          if (p && typeof p.catch === 'function') p.catch(function () {});
        } catch (e) { /* 忽略：浮前失败不影响切换 */ }
      }
    });
    el('cast-tabbar').appendChild(tab);
    if (castOrder.indexOf(serial) < 0) castOrder.push(serial); // 新会话标签追加到末尾

    var pane = document.createElement('div');
    pane.className = 'session-pane';
    pane.id = 'session-' + sanitizeId(serial);
    pane.innerHTML =
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
      '</div>' +
      '<a class="kbdlink">快捷键说明 →</a>' +
      '<div class="logbox">' +
        '<div class="logbox-head">原始输出（排查用） <span class="logbox-caret">▸</span></div>' +
        '<div class="cast-log" style="display:none"></div>' +
      '</div>';
    el('cast-sessions').appendChild(pane);

    // 会话级操作全部带 serial（按会话；重启=RestartCast(serial) 用于停滞提示/菜单按钮，
    // 停止=StopCast(serial)）
    // 规格徽标整体可点：打开参数浮窗（有线/无线两套独立）。
    // 所有按钮 stopPropagation：会话操作只作用于被点击的会话，绝不冒泡到容器
    // 级/全局处理器（多会话"点了 A 全动"的防御闸）。
    pane.querySelector('.spec-row').addEventListener('click', function (ev) {
      ev.stopPropagation();
      openSpecModal(serial);
    });
    pane.querySelector('.kbdlink').addEventListener('click', function (ev) {
      ev.stopPropagation();
      openShortcutModal();
    });
    pane.querySelector('.restart').addEventListener('click', function (ev) {
      ev.stopPropagation();
      RestartCast(serial).then(function () { refreshNow(); }).catch(function (e) {
        toast('重启失败：' + (e && e.message ? e.message : e));
      });
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

    var s = sessions[serial] = {
      serial: serial,
      tab: tab,
      pane: pane,
      fading: false,
      refs: {
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
        log: pane.querySelector('.cast-log'),
        logbox: pane.querySelector('.logbox'),
        loghead: pane.querySelector('.logbox-head'),
        caret: pane.querySelector('.logbox-caret'),
        dot: tab.querySelector('.casttab-dot')
      }
    };
    // 原始输出：默认收起；点击标题行开关。标题行在滚动区之外（固定），
    // 日志行永不会穿越/遮挡标题（修复 sticky 遮挡问题）。
    var scroller = s.refs.log;
    s.logOpen = false;
    s.logFollow = true;
    scroller.addEventListener('scroll', function () {
      s.logFollow = scroller.scrollTop + scroller.clientHeight >= scroller.scrollHeight - 24;
    });
    s.refs.loghead.addEventListener('click', function () {
      s.logOpen = !s.logOpen;
      s.refs.logbox.classList.toggle('open', s.logOpen);
      s.refs.caret.textContent = s.logOpen ? '▾' : '▸';
      scroller.style.display = s.logOpen ? '' : 'none';
    });

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
  }

  function activateFirstActive() {
    var first = null;
    Object.keys(sessions).forEach(function (k) {
      if (!first) first = k;
    });
    if (first) {
      activateSession(first);
    } else {
      activeSerial = null;
    }
  }

  function updateTabs() {
    var n = Object.keys(sessions).length;
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

  // gui51：DragOrder 回弹/拖出时 applyOrder 按 key 逐个 appendChild，会把无 data-key 的
  // pair-entry 挤到列表头部；pointerup 后（DragOrder finish 同步执行完）把它归位到末尾。
  window.addEventListener('pointerup', function () {
    setTimeout(function () {
      var list = el('device-list');
      var pe = el('pair-entry');
      if (list && pe && pe.parentNode === list && list.lastElementChild !== pe) {
        list.appendChild(pe);
      }
    }, 0);
  });

  // 标签淡出消失动画（宽度收敛 + 透明度），结束后移除 DOM 并通知后端 ForgetSession
  function startFade(s) {
    if (s.fading) return;
    s.fading = true;
    s.tab.classList.add('fading');
    s.pane.classList.add('fading');
    setTimeout(function () {
      if (!s.fading) return; // 重启复活：淡出已被取消
      if (sessions[s.serial] === s) {
        s.tab.remove();
        s.pane.remove();
        var oi = castOrder.indexOf(s.serial);
        if (oi >= 0) castOrder.splice(oi, 1); // 标签消失 → 移出显示顺序
        delete sessions[s.serial];
        if (activeSerial === s.serial) activeSerial = null;
        updateTabs();
        if (typeof window.ForgetSession === 'function') ForgetSession(s.serial);
      }
    }, 340);
  }

  function cancelFade(s) {
    s.fading = false;
    s.tab.classList.remove('fading');
    s.pane.classList.remove('fading');
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

    // 活动标签失效（已淡出/移除）→ 自动切到第一个活动会话
    if (activeSerial && sessions[activeSerial]) {
      var ak = known[activeSerial];
      if (!ak || !ak.active) activeSerial = null;
    }
    if (!activeSerial) activateFirstActive();
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
    if (c.tls) {
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
          ? 'bat 会按自己的超时自动重试；按钮=重启整个会话'
          : '按钮=会话级操作（重启/退出），GUI 不直接替 bat 按键';
        r.prompt.appendChild(hint);
      } else {
        r.prompt.style.display = 'none';
      }
    }

    // 原始日志（跟随标签）：新行到达时重建最近 40 行；
    // 跟随态自动滚底（最新行可见）；用户上滚后暂停跟随并保留其阅读位置。
    // 更新键 = 行数 + 末行内容：后端日志截断为最近 60 行后行数恒为 60，
    // 仅比较行数会导致日志冻结（新行不再渲染）——修复"日志只更新几条后停"。
    var logKey = String(c.log.length) + '\u0000' + (c.log.length ? c.log[c.log.length - 1] : '');
    if (r.log.getAttribute('data-n') !== logKey) {
      var prevScroll = r.log.scrollTop;
      r.log.setAttribute('data-n', logKey);
      r.log.innerHTML = '';
      c.log.slice(-40).forEach(function (l) {
        var d = document.createElement('div');
        d.className = 'log-line';
        d.textContent = l;
        r.log.appendChild(d);
      });
      if (s.logFollow) {
        r.log.scrollTop = r.log.scrollHeight;
      } else {
        r.log.scrollTop = prevScroll; // 重建会重置滚动，还原用户阅读位置
      }
    }
    // 展开状态（s.logOpen）由标题行点击切换；收起/无日志时隐藏滚动区
    r.log.style.display = (s.logOpen && c.log.length) ? '' : 'none';

    // 标签状态点：活动=绿点呼吸动画
    r.dot.classList.toggle('active', c.active);
  }

  function describeCast(c) {
    if (c.keyboardMode) return '键盘模式 ' + c.keyboardMode + (c.spec && c.spec.legacy ? ' · 老设备兼容档' : ' · 规格分配已生效');
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
    if (kbd) out.push('<div class="spec">键盘 ' + esc(kbd) + '</div>');
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
          { action: 'restart', label: '立即重投 (R)', primary: true },
          { action: 'stop', label: '退出循环 (Q)' },
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

  // ---------- 快捷键说明弹窗 ----------
  function openShortcutModal() { el('shortcut-modal').style.display = ''; }
  el('shortcut-close').addEventListener('click', function () { el('shortcut-modal').style.display = 'none'; });
  el('shortcut-modal').addEventListener('click', function (ev) {
    if (ev.target === el('shortcut-modal')) el('shortcut-modal').style.display = 'none';
  });

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
    bit: '码率说明：同理（55M → 55/40/15；150M → 150/80/60/40/15，级数不变）'
  };
  var PARAM_FALLBACK = {
    usb: { res: 2560, fps: 120, bitrate: 60 },
    wifi: { res: 1920, fps: 60, bitrate: 15 }
  };

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

      var row = document.createElement('div');
      row.className = 'param-row';
      row.innerHTML =
        '<div class="param-rowhead">' +
          '<span class="param-label">' + fd.label + ' (<b>' + val + fd.unit + '</b>)</span>' +
          '<button class="param-reset">重置</button>' + // 任何时候可点
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
    noteEl.textContent = PARAM_NOTES[state.activeField];
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
    SaveProfileAndRestart(paramState.serial, paramState.mode, r.value, s2.value, b.value, custom).then(function () {
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

  // ---------- 轮询主循环 ----------
  function refreshNow() {
    GetState().then(function (st) {
      lastState = st;
      var j = JSON.stringify(st);
      if (j !== lastJson) {
        lastJson = j;
        renderDevices(st);
        renderSessions(st);
        renderNewDevice(st);
        renderPairStatus(st);
      }
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

  // 轮询间隔 700ms（设备列表 Go 侧 2s 刷一次，这里只拉快照）
  pollTimer = setInterval(refreshNow, 700);
  refreshNow();

  // 页面就绪 → 通知 Go 层可获取 HWND（托盘"显示主窗口"依赖）
  window.addEventListener('load', function () {
    if (typeof window.UiReady === 'function') window.UiReady();
  });
})();
