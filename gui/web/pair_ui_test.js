// 无线调试配对向导纯函数单测（node web/pair_ui_test.js）
// 覆盖：配对码校验、错误分类码文案映射、步骤文案。
'use strict';
const P = require('./pair_ui.js');

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

// --- validCode：6 位数字 ---
ok('validCode 6位数字', P.validCode('123456'));
ok('validCode 带空白容忍', P.validCode(' 654321 '));
ok('validCode 拒绝5位', !P.validCode('12345'));
ok('validCode 拒绝字母', !P.validCode('12ab56'));
ok('validCode 拒绝空', !P.validCode(''));
ok('validCode 拒绝null', !P.validCode(null));

// --- validIp：IPv4 四段 ---
ok('validIp 正常', P.validIp('192.168.1.100'));
ok('validIp 允许0', P.validIp('0.0.0.0'));
ok('validIp 拒绝段数不足', !P.validIp('192.168.1'));
ok('validIp 拒绝越界', !P.validIp('256.168.1.1'));
ok('validIp 拒绝前导零', !P.validIp('192.168.001.001'));
ok('validIp 拒绝字母', !P.validIp('192.168.1.a'));
ok('validIp 拒绝空', !P.validIp(''));

// --- validPort：1-65535 ---
ok('validPort 正常', P.validPort('39673'));
ok('validPort 拒绝0', !P.validPort('0'));
ok('validPort 拒绝越界', !P.validPort('65536'));
ok('validPort 拒绝空', !P.validPort(''));

// --- errText：分类码 → 文案 ---
ok('errText pair-code 含30秒提示', P.errText('pair-code').indexOf('30 秒') >= 0);
ok('errText pair-port 含配对界面', P.errText('pair-port').indexOf('配对界面') >= 0);
ok('errText conn-port 含手动填写', P.errText('conn-port').indexOf('手动填写') >= 0);
ok('errText timeout 含网络', P.errText('timeout').indexOf('同一网络') >= 0);
ok('errText 未知码回落 fallback', P.errText('unknown-x', '自定义文案') === '自定义文案');
ok('errText 未知码默认文案', P.errText('unknown-x').length > 0);

// --- stepLabel ---
eq('stepLabel pair', P.stepLabel('pair'), '配对设备（输入 6 位配对码）');
eq('stepLabel connect', P.stepLabel('connect'), '正在连接设备…');
eq('stepLabel done', P.stepLabel('done'), '完成');
eq('stepLabel 未知回退', P.stepLabel('x'), 'x');

if (failed) { console.error(failed + ' failed'); process.exit(1); }
console.log('all ok');
