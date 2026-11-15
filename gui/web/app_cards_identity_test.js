'use strict';
// 应用窗口卡片「按设备身份聚合」回归测试（node gui/web/app_cards_identity_test.js）。
//
// 背景：手机通知可直接打开应用投屏窗口——后端给该窗口的 serial=设备身份
// （如 "device:example-phone"）；而「微信扫一扫」按钮打开的窗口 serial=当前连接键
// （如 "192.0.2.159:5555"）。日志按设备身份合并（两种窗口的日志都能看到），
// 但总览此前只从 openAppCards[当前连接键] 出卡片 → 通知打开的窗口有日志却没有
// 卡片。修复：渲染时按设备身份聚合（appCardsForDevice），每张卡片保留自己的
// serial 数据键用于操作（StopAppWin / RestartAppWin / BringAppWinToFront /
// openAppSetModal / requestIcon）。
//
// 本测试不加载浏览器页面，而是在 node:vm 中执行从 gui/web/app.js（浏览器 IIFE）
// 抽取出的真实函数，覆盖：
//   1) appCardsForDevice：跨数据键聚合、pkg 去重、appCardOrders 排序、无 identity
//      卡经 pane 键归一；
//   2) renderOpenAppCards：数据键与容器键不同也能把卡片画进会话 pane、全部关闭
//      按钮点亮、图标懒加载用各卡自己的键；
//   3) 逐卡按钮/点击：BringAppWinToFront / openAppSetModal / StopAppWin 均收到
//      该卡自己的 serial（通知窗口=device 身份键；扫一扫窗口=连接键）；
//   4) stopAllAppWins：每个窗口只按自己的键停止一次（3 个窗口 = 3 次调用，
//      键分别为 1 个连接键 + 2 个身份键）；
//   5) syncAppWins：记录卡片 identity，把两种后端 serial 的窗口都渲染进已有会话
//      pane；无会话/pane 的设备（appWins 只有后端 serial）会经 ensureAppWinPane
//      补建应用窗口页并渲染卡片，重复快照不重复建页。
//
// 运行：在仓库根目录执行 `node gui/web/app_cards_identity_test.js`。
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname, 'app.js'), 'utf8');

const checks = [];
function ok(value, message) { checks.push(message); assert.ok(value, message); }
function eq(actual, expected, message) { checks.push(message); assert.equal(actual, expected, message); }
function eqList(actual, expected, message) {
  checks.push(message);
  assert.deepEqual(Array.from(actual), expected, message);
}

// ---------- 从 app.js 抽取命名函数源文本 ----------
// app.js 是浏览器 IIFE（函数两空格缩进）。函数体内嵌套回调很多，固定按缩进切片
// 容易被内部 `  })` 误截，这里用花括号配平扫描（跳过字符串/注释）取完整源码。
function extractFunction(name) {
  let start = source.indexOf('  function ' + name + '(');
  if (start < 0) start = source.indexOf('function ' + name + '(');
  assert.ok(start >= 0, 'app.js 缺少函数：' + name);
  let depth = 0;
  let state = 'code';
  for (let i = start; i < source.length; i++) {
    const ch = source[i];
    const next = source[i + 1];
    if (state === 'code') {
      if (ch === '/' && next === '/') { state = 'line'; i++; continue; }
      if (ch === '/' && next === '*') { state = 'block'; i++; continue; }
      if (ch === "'") { state = 'single'; continue; }
      if (ch === '"') { state = 'double'; continue; }
      if (ch === '`') { state = 'template'; continue; }
      if (ch === '{') depth++;
      else if (ch === '}') {
        depth--;
        if (depth === 0) return source.slice(start, i + 1);
      }
    } else if (state === 'line') {
      if (ch === '\n') state = 'code';
    } else if (state === 'block') {
      if (ch === '*' && next === '/') { state = 'code'; i++; }
    } else if (state === 'single' || state === 'double') {
      if (ch === '\\') i++;
      else if ((state === 'single' && ch === "'") || (state === 'double' && ch === '"')) state = 'code';
    } else if (state === 'template') {
      if (ch === '\\') i++;
      else if (ch === '`') state = 'code';
    }
  }
  assert.fail('app.js 函数源码未闭合：' + name);
}

function runExtracts(scope, names) {
  names.forEach(name => vm.runInContext(extractFunction(name), scope, { filename: 'app.js#' + name }));
}

// ---------- 最小 DOM（只支持被测渲染函数用到的成员） ----------
class Element {
  constructor(tag) {
    this.tagName = tag;
    this.children = [];
    this.style = {};
    this.dataset = {};
    this.events = {};
    this.className = '';
    this.textContent = '';
    this.parentNode = null;
  }
  get classList() {
    const self = this;
    return {
      contains: cls => self.className.split(' ').filter(Boolean).indexOf(cls) >= 0,
      add: cls => { if (!self.classList.contains(cls)) self.className = (self.className ? self.className + ' ' : '') + cls; },
      remove: cls => { self.className = self.className.split(' ').filter(x => x && x !== cls).join(' '); }
    };
  }
  appendChild(child) {
    if (child.parentNode) child.parentNode.removeChild(child);
    this.children.push(child);
    child.parentNode = this;
    return child;
  }
  removeChild(child) {
    const at = this.children.indexOf(child);
    assert.ok(at >= 0, 'removeChild 目标必须属于父节点');
    this.children.splice(at, 1);
    child.parentNode = null;
    return child;
  }
  set innerHTML(value) {
    assert.equal(value, '', '测试 DOM 只支持清空 innerHTML');
    this.children.slice().forEach(child => this.removeChild(child));
  }
  get innerHTML() { return ''; }
  addEventListener(type, fn) { (this.events[type] = this.events[type] || []).push(fn); }
  querySelectorAll(selector) {
    assert.ok(selector.charAt(0) === '.', '测试 DOM 只支持类选择器：' + selector);
    const cls = selector.slice(1);
    const found = [];
    const walk = node => node.children.forEach(child => {
      if (child.classList.contains(cls)) found.push(child);
      walk(child);
    });
    walk(this);
    return found;
  }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
}

function fire(element, type) {
  const listeners = element.events[type] || [];
  ok(listeners.length > 0, '元素应有 ' + type + ' 监听器');
  const event = { stopped: false, stopPropagation() { event.stopped = true; } };
  listeners.forEach(fn => fn(event));
  return event;
}

function actionButton(card, label) {
  const actions = card.querySelector('.openapp-actions');
  ok(actions, '卡片 ' + card.dataset.pkg + ' 应有操作区');
  const button = actions.children.find(child => child.textContent === label);
  ok(button, '卡片 ' + card.dataset.pkg + ' 应有「' + label + '」按钮');
  return button;
}

// 应用窗口页/会话 pane 的最小骨架：box(.openapp-list) + 同行「全部关闭」按钮。
function makePane() {
  const box = new Element('div');
  box.className = 'openapp-list';
  const wrapper = new Element('div');
  const allBtn = new Element('button');
  allBtn.className = 'btn-allstop';
  allBtn.style.display = 'none'; // 隐藏态起步：渲染后有卡片才点亮
  wrapper.appendChild(allBtn);
  wrapper.appendChild(box);
  box.dataset.dragBound = '1'; // 预绑定拖拽：测试不依赖真实 DragOrder/pointerup
  return {
    pane: { querySelector: selector => (selector === '.openapp-list' ? box : null) },
    box,
    allBtn
  };
}

function openAppCardsFixture() {
  return {
    [CONN]: [{ pkg: PKG.WECHAT, identity: IDENTITY, name: '微信', bornAt: 1000 }],
    [IDENTITY]: [
      { pkg: PKG.MAP, identity: IDENTITY, name: '百度地图', bornAt: 1001 },
      { pkg: PKG.ZHIHU, identity: IDENTITY, name: '知乎', bornAt: 1002 }
    ]
  };
}

const CONN = '192.0.2.159:5555';
const IDENTITY = 'device:example-phone';
const LEGACY_SERIAL = '192.0.2.160:5555'; // 快照窗口缺 identity 时的后端 serial（老帧/边缘帧）
const LEGACY_IDENTITY = 'device:example-phoneX';
const PKG = {
  WECHAT: 'com.tencent.mm',
  MAP: 'com.baidu.map',
  ZHIHU: 'com.zhihu.android',
  TAOBAO: 'com.taobao.taobao'
};

// ---------- 1) appCardsForDevice：按设备身份聚合 ----------
(function testDeviceAggregation() {
  const scope = {
    sessions: {},
    appWins: {},
    openAppCards: openAppCardsFixture(),
    appCardOrders: Object.create(null),
    deviceIdentityOf: () => '',
    lastState: null
  };
  vm.createContext(scope);
  runExtracts(scope, ['logPaneDeviceKey', 'appCardsForDevice']);

  // 拖拽顺序按设备保存（appCardOrders 命中项排前，其余保持 openAppCards 键序）。
  scope.appCardOrders[IDENTITY] = [PKG.ZHIHU, PKG.WECHAT];

  const raw = scope.appCardsForDevice(IDENTITY);
  eqList(Object.keys(raw[0]).sort(), ['card', 'serial'], '聚合结果是 {serial, card} 条目');
  let entries = Array.from(raw, entry => ({ serial: entry.serial, pkg: entry.card.pkg, identity: entry.card.identity }));
  eq(entries.length, 3, '通知窗口卡片（数据在身份键下）要并入设备聚合');
  eqList(entries.map(entry => entry.pkg), [PKG.ZHIHU, PKG.WECHAT, PKG.MAP], 'appCardOrders 命中项排前，其余按数据键序');
  eqList(entries.map(entry => entry.serial), [IDENTITY, CONN, IDENTITY], '每条聚合条目保留卡片自己的数据键');
  ok(entries.every(entry => entry.identity === IDENTITY), '三张卡都属于同一设备身份');
  eq(raw[2].card, scope.openAppCards[IDENTITY][0], '条目.card 是真实卡片对象（状态改动要落回数据源）');

  // 同一 pkg 出现在多个键下 → 只出一张卡。
  scope.openAppCards['K80-SERIAL'] = [{ pkg: PKG.ZHIHU, identity: IDENTITY, name: '知乎', bornAt: 1003 }];
  entries = Array.from(scope.appCardsForDevice(IDENTITY), entry => ({ serial: entry.serial, pkg: entry.card.pkg }));
  eq(entries.length, 3, '同一 pkg 出现在第二个键下必须去重');
  eq(entries.find(entry => entry.pkg === PKG.ZHIHU).serial, IDENTITY, '去重保留先扫描到的非 leaving 卡');

  // 旧键卡片淡出中、新键仍存活 → 让存活的那张接管（不把离开中的卡当唯一代表）。
  scope.openAppCards[IDENTITY].find(card => card.pkg === PKG.ZHIHU).leaving = true;
  entries = Array.from(scope.appCardsForDevice(IDENTITY), entry => ({ serial: entry.serial, pkg: entry.card.pkg }));
  eq(entries.length, 3, 'leaving 重复卡不能造成重复条目');
  eq(entries.find(entry => entry.pkg === PKG.ZHIHU).serial, 'K80-SERIAL', '存活的重复卡接管 leaving 卡');
  scope.openAppCards[IDENTITY].find(card => card.pkg === PKG.ZHIHU).leaving = false;

  // 卡片自身没有 identity 时，经数据键所在 pane 的身份归一（logPaneDeviceKey 兜底）。
  scope.openAppCards['USB-K80'] = [{ pkg: PKG.TAOBAO, name: '淘宝', bornAt: 1004 }];
  scope.sessions['USB-K80'] = { identity: IDENTITY, pane: null };
  entries = Array.from(scope.appCardsForDevice(IDENTITY), entry => ({ serial: entry.serial, pkg: entry.card.pkg }));
  eq(entries.length, 4, '无 identity 的卡片经数据键所在 pane 身份归入本设备');
  eq(entries.find(entry => entry.pkg === PKG.TAOBAO).serial, 'USB-K80', '归入后仍保留自身操作键');
  eq(Array.from(scope.appCardsForDevice('device:OTHER')).length, 0, '其他设备不能串入本设备卡片');
  console.log('app_cards_identity: aggregation/dedupe/order passed');
})();

// ---------- 渲染场景工厂（2/3 共用） ----------
function createRenderScenario() {
  const dom = makePane();
  const calls = { stop: [], bring: [], restart: [], icons: [], setModal: [], toast: [] };
  const scope = {
    document: { createElement: tag => new Element(tag) },
    window: {
      StopAppWin: (...args) => { calls.stop.push(args); return Promise.resolve(); },
      BringAppWinToFront: (...args) => { calls.bring.push(args); return Promise.resolve(); },
      RestartAppWin: (...args) => { calls.restart.push(args); return Promise.resolve(); },
      RetryRootRepair: (...args) => { calls.restart.push(args); return Promise.resolve(); }
    },
    sessions: { [CONN]: { pane: dom.pane, identity: IDENTITY } },
    appWins: {},
    openAppCards: openAppCardsFixture(),
    appCardOrders: Object.create(null),
    renderedPkgs: {},
    pendingRender: {},
    lastState: { devices: [], profiles: [], sessions: [], recentCastLogs: [] },
    deviceIdentityOf: () => '',
    requestIcon: (serial, pkg) => calls.icons.push([serial, pkg]),
    AppWinText: { cardStateText: card => (card && card.closing ? '正在关闭' : '正在窗口 · 无线模式') },
    openAppSetModal: (...args) => calls.setModal.push(args),
    toast: (...args) => calls.toast.push(args),
    vtSafeName: key => String(key).replace(/[^a-zA-Z0-9_-]/g, '_'),
    appColor: () => '#123456',
    attachAppCardDrag: () => { throw new Error('本测试预置 dragBound，不应重绑拖拽'); },
    setTimeout: () => 0,
    clearTimeout: () => {}
  };
  vm.createContext(scope);
  runExtracts(scope, ['logPaneDeviceKey', 'appCardsForDevice', 'renderOpenAppCards']);
  return { scope, dom, calls };
}

// ---------- 2) renderOpenAppCards：容器键 ≠ 数据键也要画出全部卡片 ----------
(function testRenderAcrossDataKeys() {
  // 会话容器键=连接键；卡片数据分散在连接键(微信)与身份键(百度地图/知乎)两处。
  const { scope, dom, calls } = createRenderScenario();
  scope.appCardOrders[IDENTITY] = [PKG.ZHIHU, PKG.WECHAT];
  scope.renderOpenAppCards(CONN);

  const cards = dom.box.children.filter(child => child.classList.contains('openapp-card'));
  eq(cards.length, 3, '会话 pane 要渲染通知窗口 + 扫一扫窗口共 3 张卡');
  eq(dom.box.children.length, 3, '.openapp-list 内只有这 3 张卡');
  eqList(cards.map(card => card.dataset.pkg), [PKG.ZHIHU, PKG.WECHAT, PKG.MAP], '渲染顺序跟随设备卡片顺序');
  eq(dom.allBtn.style.display, '', '有卡片时「全部关闭」按钮变为可见');
  eq(new Set(cards.map(card => card.style.viewTransitionName)).size, 3, '每张卡的 view-transition 名唯一');
  eqList(calls.icons, [[IDENTITY, PKG.ZHIHU], [CONN, PKG.WECHAT], [IDENTITY, PKG.MAP]], '图标懒加载按各卡自己的数据键请求');
  for (const card of cards) {
    eq(card.children.length, 3, '卡片 ' + card.dataset.pkg + ' 由图标/标题/操作区组成');
    ok(card.querySelector('.openapp-icon'), '卡片 ' + card.dataset.pkg + ' 有图标');
    ok(card.querySelector('.openapp-name').textContent, '卡片 ' + card.dataset.pkg + ' 显示应用名');
    eq(card.querySelector('.openapp-state').textContent, '正在窗口 · 无线模式', '卡片 ' + card.dataset.pkg + ' 显示状态文字');
  }
  eq(cards.find(card => card.dataset.pkg === PKG.MAP).querySelector('.openapp-name').textContent, '百度地图',
    '通知打开的窗口保留后端下发的展示名');
  console.log('app_cards_identity: cross-key rendering passed');
})();

// ---------- 3) 逐卡操作使用卡片自己的数据键 ----------
(function testPerCardActionsUseOwnKeys() {
  const { scope, dom, calls } = createRenderScenario();
  scope.renderOpenAppCards(CONN);
  const cards = dom.box.children.filter(child => child.classList.contains('openapp-card'));
  const mapCard = cards.find(card => card.dataset.pkg === PKG.MAP);       // 通知打开 → 身份键
  const wechatCard = cards.find(card => card.dataset.pkg === PKG.WECHAT); // 扫一扫 → 连接键
  ok(mapCard && wechatCard, '两种窗口的卡片都已渲染');

  const clickEvent = fire(mapCard, 'click');
  ok(clickEvent.stopped, '卡片点击阻止冒泡');
  eq(calls.bring.length, 1, '点击卡片把对应窗口提到前面');
  eqList(calls.bring[0], [IDENTITY, PKG.MAP], '通知窗口浮前用身份键');
  eq(calls.stop.length, 0, '点击卡片不能误停窗口');

  fire(actionButton(mapCard, '设置'), 'click');
  eq(calls.setModal.length, 1, '「设置」打开对应窗口设置浮窗');
  eqList(calls.setModal[0], [IDENTITY, PKG.MAP], '设置浮窗用该卡自己的身份键');

  fire(actionButton(mapCard, '停止'), 'click');
  eq(calls.stop.length, 1, '「停止」发出一次关闭请求');
  eqList(calls.stop[0], [IDENTITY, PKG.MAP], '通知打开的百度地图按身份键停止');
  eq(calls.restart.length, 0, '停止不会触发重新投屏');
  ok(scope.openAppCards[IDENTITY].find(card => card.pkg === PKG.MAP).closing, '停止后卡片乐观置 closing');

  const wechatStop = actionButton(wechatCard, '停止');
  ok(wechatStop.classList.contains('stop'), '停止按钮带 stop 类');
  fire(wechatStop, 'click');
  eq(calls.stop.length, 2, '扫一扫窗口的停止也发出');
  eqList(calls.stop[1], [CONN, PKG.WECHAT], '扫一扫窗口按连接键停止（不能用身份键）');
  ok(scope.openAppCards[CONN].find(card => card.pkg === PKG.WECHAT).closing, '关闭状态落在同一张卡对象上');
  console.log('app_cards_identity: per-card action keys passed');
})();

// ---------- 4) stopAllAppWins：每个窗口按自己的键各停一次 ----------
(function testStopAllUsesPerCardKey() {
  const calls = { stop: [], renders: [], toast: [] };
  const scope = {
    sessions: { [CONN]: { identity: IDENTITY } },
    appWins: {},
    openAppCards: openAppCardsFixture(),
    appCardOrders: Object.create(null),
    renderedPkgs: {},
    pendingRender: {},
    deviceIdentityOf: () => '',
    lastState: null,
    window: { StopAppWin: (...args) => { calls.stop.push(args); return Promise.resolve(); } },
    toast: message => calls.toast.push(message),
    renderOpenAppCards: serial => calls.renders.push(serial),
    setTimeout: () => 0
  };
  vm.createContext(scope);
  runExtracts(scope, ['logPaneDeviceKey', 'appCardsForDevice', 'stopAllAppWins']);
  // 按钮绑定的是会话键（连接键）；卡片数据键有两种。
  scope.stopAllAppWins(CONN);

  eq(calls.stop.length, 3, '「全部关闭」对每个存活窗口各发一次停止');
  const serialByPkg = new Map(calls.stop.map(args => [args[1], args[0]]));
  eq(serialByPkg.get(PKG.WECHAT), CONN, '扫一扫窗口用连接键停止');
  eq(serialByPkg.get(PKG.MAP), IDENTITY, '通知打开的百度地图用身份键停止');
  eq(serialByPkg.get(PKG.ZHIHU), IDENTITY, '通知打开的知乎用身份键停止');
  const perSerial = new Map();
  calls.stop.forEach(args => perSerial.set(args[0], (perSerial.get(args[0]) || 0) + 1));
  eq(perSerial.get(CONN), 1, '连接键只停经它打开的窗口');
  eq(perSerial.get(IDENTITY), 2, '身份键只停经它打开的窗口');
  eqList(calls.renders, [CONN], '先乐观渲染一次「正在关闭…」再逐窗停止');
  const cards = [].concat(scope.openAppCards[CONN], scope.openAppCards[IDENTITY]);
  eq(cards.length, 3, '数据源仍有 3 张卡（等待快照真删）');
  ok(cards.every(card => card.closing === true && card.closingAt > 0), '全部窗口进入 closing 遮罩态');
  eq(calls.toast.length, 0, '受理成功不弹失败提示');
  console.log('app_cards_identity: stop-all per-card keys passed');
})();

// ---------- 5) syncAppWins：记录身份 + 无 pane 设备补建应用窗口页 ----------
function createSyncScenario() {
  const sessionDom = makePane();
  const calls = { ensure: [], icons: [], renders: [], setModal: [], toast: [], panes: {} };
  const scope = {
    document: { createElement: tag => new Element(tag) },
    window: {
      StopAppWin: () => Promise.resolve(),
      BringAppWinToFront: () => Promise.resolve(),
      RestartAppWin: () => Promise.resolve(),
      RetryRootRepair: () => Promise.resolve()
    },
    sessions: { [CONN]: { pane: sessionDom.pane, identity: IDENTITY } },
    appWins: {},
    openAppCards: {},
    appCardOrders: Object.create(null),
    renderedPkgs: {},
    pendingRender: {},
    lastState: { devices: [], profiles: [], recentCastLogs: [] },
    // syncAppWins 末段的 key 兜底：win.identity || deviceIdentityOf(win.serial) || win.serial
    deviceIdentityOf: serial => (serial === LEGACY_SERIAL ? LEGACY_IDENTITY : ''),
    requestIcon: (serial, pkg) => calls.icons.push([serial, pkg]),
    AppWinText: { cardStateText: () => '正在窗口 · 无线模式' },
    openAppSetModal: (...args) => calls.setModal.push(args),
    toast: (...args) => calls.toast.push(args),
    vtSafeName: key => String(key).replace(/[^a-zA-Z0-9_-]/g, '_'),
    appColor: () => '#123456',
    attachAppCardDrag: () => { throw new Error('本测试预置 dragBound，不应重绑拖拽'); },
    // ensureAppWinPane 真实现会建 DOM/标签并驱动 updateTabs；这里只仿造 pane，
    // 断言"无 pane 设备确实被补建应用窗口页"以及卡片最终渲染。
    ensureAppWinPane: serial => {
      calls.ensure.push(serial);
      const built = makePane();
      calls.panes[serial] = built;
      scope.appWins[serial] = scope.appWins[serial] || { name: serial, identity: '', tab: null, pane: null };
      scope.appWins[serial].pane = built.pane;
    },
    setTimeout: () => 0,
    clearTimeout: () => {}
  };
  vm.createContext(scope);
  runExtracts(scope, ['logDeviceKey', 'logPaneDeviceKey', 'appCardsForDevice', 'renderOpenAppCards', 'syncAppWins']);
  // 记录 syncAppWins 因哪些数据键标脏而重绘（内部调用真实 renderOpenAppCards）。
  const realRender = scope.renderOpenAppCards;
  scope.renderOpenAppCards = serial => { calls.renders.push(serial); return realRender(serial); };
  return { scope, sessionDom, calls };
}

(function testSyncAppWinsRecordsIdentityAndPanes() {
  const { scope, sessionDom, calls } = createSyncScenario();
  // 快照同时下发两种键形态：身份键（通知打开）与连接键（扫一扫打开）。
  const serverList = [
    { serial: IDENTITY, identity: IDENTITY, pkg: PKG.MAP, name: '百度地图', deviceName: 'K80' },
    { serial: CONN, identity: IDENTITY, pkg: PKG.WECHAT, name: '微信', deviceName: 'K80' }
  ];
  scope.syncAppWins(serverList);

  eqList(Object.keys(scope.openAppCards).sort(), [CONN, IDENTITY].sort(), '两种后端 serial 的窗口各自入表');
  eq(scope.openAppCards[IDENTITY][0].pkg, PKG.MAP, '通知打开的窗口记在身份键下');
  eq(scope.openAppCards[IDENTITY][0].identity, IDENTITY, '身份键卡片记录设备身份');
  eq(scope.openAppCards[IDENTITY][0].name, '百度地图', '身份键卡片记录后端展示名');
  eq(scope.openAppCards[CONN][0].pkg, PKG.WECHAT, '扫一扫打开的窗口记在连接键下');
  eq(scope.openAppCards[CONN][0].identity, IDENTITY, '连接键卡片同样记录设备身份（跨键归一的依据）');
  const sessionCards = sessionDom.box.children.filter(child => child.classList.contains('openapp-card'));
  eq(sessionCards.length, 2, '已有会话 pane 渲染两条后端窗口（原 bug 下通知窗口无卡片）');
  eqList(sessionCards.map(card => card.dataset.pkg).sort(), [PKG.MAP, PKG.WECHAT].sort(), '两种窗口都进入会话 pane');
  ok(calls.renders.indexOf(IDENTITY) >= 0 && calls.renders.indexOf(CONN) >= 0, '两个脏数据键都触发本设备重绘');
  eqList(calls.ensure, [], '已有会话 pane 时不重复建应用窗口页');
  eqList(calls.toast, [], '正常同步不弹失败提示');

  // 无会话/pane 的设备：快照里的 serial=身份键 → 必须补建应用窗口页并渲染卡片。
  const nopaneWindow = { serial: 'device:NOPANE', identity: 'device:NOPANE', pkg: PKG.TAOBAO, name: '淘宝', deviceName: 'NOPANE' };
  scope.syncAppWins(serverList.concat([nopaneWindow]));
  ok(scope.appWins['device:NOPANE'], '无会话/pane 的窗口要创建 appWins 条目');
  eq(scope.appWins['device:NOPANE'].identity, 'device:NOPANE', 'appWins 条目记录设备身份');
  eqList(calls.ensure, ['device:NOPANE'], '经 ensureAppWinPane 补建应用窗口页（仅一次）');
  const nopaneDom = calls.panes['device:NOPANE'];
  ok(nopaneDom, '补建的应用窗口 pane 存在');
  const nopaneCards = nopaneDom.box.children.filter(child => child.classList.contains('openapp-card'));
  eq(nopaneCards.length, 1, '补建的 pane 渲染通知打开的窗口卡片');
  eq(nopaneCards[0].dataset.pkg, PKG.TAOBAO, '补建 pane 的卡片就是通知窗口应用');
  eq(scope.openAppCards['device:NOPANE'][0].identity, 'device:NOPANE', '新设备卡片记录设备身份');
  ok(calls.icons.some(args => args[0] === 'device:NOPANE' && args[1] === PKG.TAOBAO), '补建窗格图标按身份键请求');

  // 重复快照（pane 已存在）：不重复建页，另一设备卡片不串入本会话 pane。
  scope.syncAppWins(serverList.concat([nopaneWindow]));
  eq(calls.ensure.length, 1, '重复快照不重复创建应用窗口页');
  eq(sessionDom.box.children.filter(child => child.classList.contains('openapp-card')).length, 2, '会话 pane 不被其他设备窗口串入');

  // 快照窗口没有 identity（老帧/边缘帧）：建页 key 经 deviceIdentityOf(serial) 兜底。
  const legacyWindow = { serial: LEGACY_SERIAL, pkg: PKG.TAOBAO, name: '无身份窗口', deviceName: 'K80X' };
  scope.syncAppWins(serverList.concat([nopaneWindow, legacyWindow]));
  eqList(calls.ensure, ['device:NOPANE', LEGACY_IDENTITY], '无 identity 的窗口经 deviceIdentityOf 归一设备身份后建页');
  ok(scope.appWins[LEGACY_IDENTITY], '无 identity 窗口按设备身份键创建 appWins 条目');
  eq(scope.openAppCards[LEGACY_SERIAL].length, 1, '无 identity 窗口卡片仍记在自己的后端 serial 下');
  const legacyDom = calls.panes[LEGACY_IDENTITY];
  const legacyCards = legacyDom && legacyDom.box.children.filter(child => child.classList.contains('openapp-card'));
  ok(legacyCards && legacyCards.length === 1, '补建的设备窗格渲染无 identity 窗口卡片');
  eq(legacyCards[0].dataset.pkg, PKG.TAOBAO, '无 identity 窗口卡片应用正确');
  ok(calls.icons.some(args => args[0] === LEGACY_SERIAL && args[1] === PKG.TAOBAO), '无 identity 窗口图标请求用其自身数据键');
  console.log('app_cards_identity: sync/in-pane creation passed');
})();

console.log('APP_CARDS_IDENTITY_TESTS_OK ' + checks.length);
