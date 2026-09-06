// 会话映射纯函数单测（node web/session_map_test.js）
// 覆盖多会话隔离关键路径：双设备两会话互不串、同身份双键命中唯一会话、
// identity 空安全、重启闩锁期会话仍活动（卡片仍绑会话）。
'use strict';
const M = require('./session_map.js');

let failed = 0;
function ok(name, cond) {
  if (!cond) { failed++; console.error('FAIL ' + name); }
  else { console.log('ok   ' + name); }
}
function eq(name, got, want) {
  const g = JSON.stringify(got), w = JSON.stringify(want);
  if (g !== w) { failed++; console.error('FAIL ' + name + ': got ' + g + ', want ' + w); }
  else { console.log('ok   ' + name); }
}

// --- activeSessionMap：只收活动会话（含重启闩锁期） ---
(function () {
  const m = M.activeSessionMap([
    { serial: 'A', active: true },
    { serial: 'B', active: false },
    { serial: 'C', active: true },
  ]);
  ok('activeSessionMap 只收活动会话', Object.keys(m).length === 2 && m.A && m.C && !m.B);
})();

// --- 双设备两会话：各卡绑各自会话（不串） ---
(function () {
  const castMap = M.activeSessionMap([
    { serial: '601c9f08', active: true, identity: 'REDMI K80' },
    { serial: 'a743e1df', active: true, identity: 'Xiaomi Pad 8 Pro' },
  ]);
  const k80 = M.matchSession({ serial: '601c9f08', identity: 'REDMI K80' }, castMap);
  const pad = M.matchSession({ serial: 'a743e1df', identity: 'Xiaomi Pad 8 Pro' }, castMap);
  ok('K80 卡绑 K80 会话', k80 && k80.serial === '601c9f08');
  ok('平板卡绑平板会话', pad && pad.serial === 'a743e1df');
  ok('两卡不串会话', k80 !== pad);
})();

// --- 同身份双键：会话键=无线地址、卡 serial=USB（卡片重键），仍命中唯一会话 ---
(function () {
  const castMap = M.activeSessionMap([
    { serial: '192.168.31.197:5555', active: true, identity: 'REDMI K80' },
  ]);
  const byWireless = M.matchSession({ serial: '601c9f08', wireless: '192.168.31.197:5555', identity: '' }, castMap);
  ok('同身份双键经 wireless 命中', byWireless && byWireless.serial === '192.168.31.197:5555');
  const byIdentity = M.matchSession({ serial: '601c9f08', wireless: '', identity: 'REDMI K80' }, castMap);
  ok('同身份双键经 identity 命中', byIdentity && byIdentity.serial === '192.168.31.197:5555');
})();

// --- identity 空安全：卡片 identity 读不到 → 不误配（显示为无会话） ---
(function () {
  const castMap = M.activeSessionMap([
    { serial: 'a743e1df', active: true, identity: 'Xiaomi Pad 8 Pro' },
  ]);
  const hit = M.matchSession({ serial: '601c9f08', wireless: '', identity: '' }, castMap);
  ok('identity 空不误配', hit === null);
  // 会话 identity 空 + 卡 identity 非空：也不配（后端不变量=会话必有档案身份）
  const castMap2 = M.activeSessionMap([{ serial: 'x', active: true, identity: '' }]);
  ok('会话 identity 空不配', M.matchSession({ serial: 'y', identity: 'REDMI K80' }, castMap2) === null);
})();

// --- 输入防御 ---
ok('matchSession null 输入安全', M.matchSession(null, {}) === null);
ok('matchSession 空表安全', M.matchSession({ serial: 'A' }, null) === null);

if (failed) { console.error('\n' + failed + ' 项失败'); process.exit(1); }
console.log('\nsession_map 全部通过');
