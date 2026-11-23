'use strict';
// rc23-UI1 设置界面回归：静态结构 + dirty 判定纯逻辑（不依赖真实 WebView）。
// 运行：node settings_ui1_test.js
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const dir = __dirname;
const html = fs.readFileSync(path.join(dir, 'index.html'), 'utf8');
const css = fs.readFileSync(path.join(dir, 'style.css'), 'utf8');
const app = fs.readFileSync(path.join(dir, 'app.js'), 'utf8');
let fails = 0;
function ok(name, cond, detail) {
  if (cond) { console.log('ok  ' + name); return; }
  fails++; console.error('FAIL ' + name + ' ' + (detail || ''));
}
function slice(name) {
  const start = app.indexOf('  function ' + name + '(');
  assert.ok(start >= 0, name + ' missing');
  const end = app.indexOf('\n  }', start) + 4;
  return app.slice(start, end);
}

// ---------- 静态结构：两个弹窗三页签 / 每页一条说明框 / 无恢复默认 / 无「跟随设备」 ----------
for (const prefix of ['param', 'appset']) {
  for (const tab of ['display', 'audio', 'feature']) {
    ok(`${prefix} 页签 ${tab} 存在`, html.includes(`id="${prefix}-tab-${tab}"`));
    ok(`${prefix} 页面 ${tab} 存在`, html.includes(`id="${prefix}-page-${tab}"`));
    ok(`${prefix} 说明框 ${tab} 存在`, html.includes(`id="${prefix}-note-${tab}"`));
  }
  const tabs = (html.match(new RegExp(`id="${prefix}-tab-`, 'g')) || []).length;
  ok(`${prefix} 恰好三个页签`, tabs === 3, tabs);
}
ok('index.html 不含恢复默认按钮文案', !html.includes('恢复本模式默认参数') && !html.includes('恢复本窗口默认参数'));
ok('index.html 不含「跟随设备」气泡文案', !html.includes('跟随设备</span>'));
ok('透视相机说明已删除 1080p', !html.includes('1080p'));
ok('样式包含固定高度 604/716', /#param-modal \.param-card[^}]*height:604px/.test(css) && /#appset-modal \.param-card[^}]*height:716px/.test(css));
ok('样式保留 #param-card 140ms 过渡', /#param-card\s*\{\s*transition:\s*opacity 140ms ease/.test(css));
ok('样式含未保存橙点规则', /\.dtab\.dirty::after/.test(css));
ok('样式含内容区 flex 填充（页脚置底）', /\.param-body[^}]*flex:1 1 auto/.test(css));
ok('应用窗口设置含跟随连接切换的过渡样式', /#appset-card\.param-mode-out/.test(css));

// ---------- 应用窗口设置跟随有线/无线切换（rc23-UI1 新增，与主投屏同构） ----------
ok('app.js 提供 syncAppSetModal', /function syncAppSetModal\(/.test(app));
ok('轮询主循环会同步应用窗口设置', /syncSpecModal\(st\);\s*\n\s*syncAppSetModal\(st\);/.test(app));
ok('刷新链路也会同步应用窗口设置', /syncSpecModal\(st\);\s*\n\s*syncAppSetModal\(st\);\s*\n\s*renderDevices/.test(app));
{
  const ctx = { console };
  vm.createContext(ctx);
  vm.runInContext(slice('currentSerialForIdentity'), ctx);
  const st = {
    sessions: [{ serial: 'usb-serial', identity: 'device:A' }],
    devices: [{ serial: 'usb-dev', identity: 'device:A' }, { serial: 'wifi-dev', identity: 'device:B', wireless: 'wifi-addr', connType: 'wifi' }]
  };
  ok('应用窗口跟随：优先取同 identity 的当前会话 serial', ctx.currentSerialForIdentity(st, 'device:A', 'old') === 'usb-serial');
  ok('应用窗口跟随：无会话时回落设备地址', ctx.currentSerialForIdentity(st, 'device:B', 'old') === 'wifi-addr');
  ok('应用窗口跟随：无匹配时保留原 serial', ctx.currentSerialForIdentity(st, 'device:C', 'old') === 'old');
}

// ---------- el('id') 字面量必须在 index.html 里存在 ----------
const ids = new Set([...html.matchAll(/\bid="([^"]+)"/g)].map(m => m[1]));
const referenced = new Set([...app.matchAll(/\bel\('([A-Za-z0-9_]+)'\)/g)].map(m => m[1]));
const missing = [...referenced].filter(id => !ids.has(id));
ok('app.js el() 引用的 id 都存在于 index.html', missing.length === 0, missing.slice(0, 10).join(','));

for (const prefix of ['param', 'appset']) {
  ok(prefix + ' 滑条范围 5%–100% 且连续移动', new RegExp('id="' + prefix + '-wheel-swipe-distance" type="range" min="5" max="100" step="any"').test(html));
  ok(prefix + ' 幅度徽标在滑条之后', new RegExp('id="' + prefix + '-wheel-swipe-distance"[^>]*>\\s*</div>\\s*<span class="wheel-swipe-value" id="' + prefix + '-wheel-swipe-value"').test(html));
  ok(prefix + ' 反向初始关闭', new RegExp('id="' + prefix + '-wheel-swipe-reverse" type="button" aria-pressed="false"').test(html));
}

// ---------- dirty 纯逻辑：appset ----------
{
  const saveButton = {};
  const ctx = { el: () => saveButton, setTabDirty: (prefix, tab, dirty) => { ctx.__dirty[tab] = !!dirty; }, __dirty: {}, console };
  vm.createContext(ctx);
  for (const name of ['readWheelSwipe', 'wheelSwipeChanged', 'appsetEffectiveField', 'snapshotAppSetState', 'updateAppsetDirtyTabs']) {
    vm.runInContext(slice(name), ctx);
  }
  const base = { size: 3200, fps: 60, bit: 15, dpi: 0, ratioW: '', ratioH: '', flex: true, perspective: true, perspectiveInputFps: 10, audio: 'phone', vcodec: 'h264', ownVCodec: false, acodec: 'opus', ownACodec: false, lockFps: false, lockBitrate: false };
  ctx.appSetState = {
    mode: 'wifi', initial: base, touched: { display: true, audio: false, feature: false },
    fields: {
      size: { val: 3200, edit: false, input: '' }, fps: { val: 60, edit: false, input: '' },
      bit: { val: 15, edit: false, input: '' }, dpi: { val: 0, edit: false, input: '' }
    },
    ratioW: '', ratioH: '', flex: true, perspective: true, perspectiveInputFps: 10,
    audio: 'phone', vcodec: 'h264', ownVCodec: false, acodec: 'opus', ownACodec: false, lockFps: false, lockBitrate: false
  };
  ctx.updateAppsetDirtyTabs();
  ok('appset 初始无橙点', ctx.__dirty.display === false && ctx.__dirty.audio === false && ctx.__dirty.feature === false);
  ctx.appSetState.fields.size = { val: 2560, edit: false, input: '' };
  ctx.updateAppsetDirtyTabs();
  ok('appset 改档位后画面橙点出现', ctx.__dirty.display === true);
  ctx.appSetState.fields.size = { val: 3200, edit: false, input: '' };
  ctx.updateAppsetDirtyTabs();
  ok('appset 改回原值橙点消失', ctx.__dirty.display === false);
  // 自定义输入：填非原值脏；填原值/清空不脏
  ctx.appSetState.fields.size = { val: 3200, edit: true, input: '2000' };
  ctx.updateAppsetDirtyTabs(); ok('appset 自定义非原值 → 橙点', ctx.__dirty.display === true);
  ctx.appSetState.fields.size = { val: 3200, edit: true, input: '3200' };
  ctx.updateAppsetDirtyTabs(); ok('appset 自定义=原值 → 不脏', ctx.__dirty.display === false);
  ctx.appSetState.fields.size = { val: 3200, edit: true, input: '' };
  ctx.updateAppsetDirtyTabs(); ok('appset 自定义空值 → 不脏', ctx.__dirty.display === false);
  // touched 门控：未交互的页签永远不脏
  ctx.appSetState.touched.display = false;
  ctx.appSetState.fields.size = { val: 2560, edit: false, input: '' };
  ctx.updateAppsetDirtyTabs(); ok('appset 未交互页签不亮角标', ctx.__dirty.display === false);
  // 功能页开关
  ctx.appSetState.touched.feature = true;
  ctx.appSetState.flex = false;
  ctx.updateAppsetDirtyTabs(); ok('appset 关窗口跟随 → 功能页橙点', ctx.__dirty.feature === true);
  ctx.appSetState.flex = true;
  ctx.updateAppsetDirtyTabs(); ok('appset 恢复窗口跟随 → 橙点消失', ctx.__dirty.feature === false);
  ctx.appSetState.initialWheelSwipe = ctx.readWheelSwipe({wheelSwipeEnabled: true, wheelSwipePercent: 28});
  ctx.appSetState.wheelSwipe = ctx.readWheelSwipe({wheelSwipeEnabled: true, wheelSwipePercent: 28, wheelSwipeReverse: true});
  ctx.updateAppsetDirtyTabs();
  ok('appset 滚轮反向单项立即保存', ctx.__dirty.feature && ctx.appSetState.wheelSwipeOnly && saveButton.textContent === '保存设置');
}

// ---------- dirty 纯逻辑：param ----------
{
  const ctx = { setTabDirty: (prefix, tab, dirty) => { ctx.__dirty[tab] = !!dirty; }, __dirty: {}, console };
  vm.createContext(ctx);
  for (const name of ['wheelSwipeChanged', 'paramEffectiveFieldFor', 'updateParamDirtyTabs']) vm.runInContext(slice(name), ctx);
  const modeBase = { res: 3200, fps: 60, bit: 15, audio: 'pc', vcodec: 'h264', acodec: 'opus', lockFps: false, lockBitrate: false };
  ctx.paramState = {
    mode: 'wifi', initial: { usb: modeBase, wifi: modeBase },
    touched: { display: true, audio: false, feature: false },
    profile: { wifi: { lockFps: false, lockBitrate: false } },
    fields: { wifi: { res: { val: 3200, edit: false, input: '' }, fps: { val: 60, edit: false, input: '' }, bit: { val: 15, edit: false, input: '' } } },
    audio: { wifi: 'pc' }, vcodec: { wifi: 'h264' }, acodec: { wifi: 'opus' }
  };
  ctx.updateParamDirtyTabs();
  ok('param 初始无橙点', !ctx.__dirty.display && !ctx.__dirty.audio && !ctx.__dirty.feature);
  ctx.paramState.fields.wifi.res = { val: 1920, edit: false, input: '' };
  ctx.updateParamDirtyTabs(); ok('param 改分辨率 → 画面橙点', ctx.__dirty.display === true);
  ctx.paramState.fields.wifi.res = { val: 3200, edit: false, input: '' };
  ctx.updateParamDirtyTabs(); ok('param 改回原值 → 橙点消失', ctx.__dirty.display === false);
  ctx.paramState.audio.wifi = 'both';
  ctx.paramState.touched.audio = true;
  ctx.updateParamDirtyTabs(); ok('param 改声音 → 声音页橙点', ctx.__dirty.audio === true);
  ctx.paramState.audio.wifi = 'pc';
  ctx.updateParamDirtyTabs(); ok('param 改回声音 → 橙点消失', ctx.__dirty.audio === false);
  // root 自动修复（即时生效）不进 dirty
  ok('param 功能页永不因 root 显示橙点', ctx.__dirty.feature === false);
}

if (fails) { console.error(`settings_ui1: ${fails} failed`); process.exit(1); }
console.log('settings_ui1: structure, ids, tab pages and dirty rules passed');
