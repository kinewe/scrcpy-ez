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
// gui55：配对已连接但读不到设备序列号（不得静默残缺入档）
ok('errText serial 含序列号', P.errText('serial').indexOf('序列号') >= 0);
ok('未授权明确提示允许调试与配对码', P.errText('adb-unauthorized').includes('允许') && P.errText('adb-unauthorized').includes('配对码'));
ok('errText 未知码回落 fallback', P.errText('unknown-x', '自定义文案') === '自定义文案');
ok('errText 未知码默认文案', P.errText('unknown-x').length > 0);

// Both the inline failure and pairing-only toast use this formatter. Sentence
// breaks must be intentional; dots in endpoint addresses and files stay intact.
for (const code of ['pair-code', 'pair-port', 'conn-port', 'connect-failed', 'adb-unauthorized', 'timeout', 'addr-invalid', 'serial', 'runtime-missing', 'internal']) {
  const text = P.formatError(P.errText(code));
  const lines = text.split('\n');
  ok(code + ' 两行且无句号结尾', lines.length === 2 && lines.every(line => line.length > 0 && !/[。．.]$/.test(line)));
  eq(code + ' 格式重复处理稳定', P.formatError(text), text);
}
eq('按句分行', P.formatError('配对失败。请检查后重试。'), '配对失败\n请检查后重试');
eq('单句按分句分行', P.formatError('地址不正确，请重新输入。'), '地址不正确\n请重新输入');
eq('保留 IP 端口与文件名', P.formatError('连接 192.0.2.159:5555 失败。请检查 adb.exe。'), '连接 192.0.2.159:5555 失败\n请检查 adb.exe');
eq('保留英文文件名与版本号', P.formatError('Missing adb.exe in v2.4.2. Retry.'), 'Missing adb.exe in v2.4.2\nRetry');
eq('换行与末尾句号归一', P.formatError(' 错误。\r\n 建议. '), '错误\n建议');
eq('多句合为两行', P.formatError('错误。先检查。再重试。'), '错误\n先检查，再重试');
eq('单个无标点错误仍有两行', P.formatError('读取失败'), '读取失败\n请检查后重试');
eq('空错误使用默认两行提示', P.formatError('   '), P.errText('internal'));

// --- stepLabel ---
eq('stepLabel pair', P.stepLabel('pair'), '配对设备（输入 6 位配对码）');
eq('stepLabel connect', P.stepLabel('connect'), '正在连接设备…');
eq('stepLabel done', P.stepLabel('done'), '完成');
eq('stepLabel 未知回退', P.stepLabel('x'), 'x');

if (failed) { console.error(failed + ' failed'); process.exit(1); }
console.log('all ok');
