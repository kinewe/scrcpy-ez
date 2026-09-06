// 参数浮窗纯状态机单测（node web/param_state_test.js）
'use strict';
const P = require('./param_state.js');

let failed = 0;
function eq(name, got, want) {
  const g = JSON.stringify(got), w = JSON.stringify(want);
  if (g !== w) { failed++; console.error(`FAIL ${name}: got ${g}, want ${w}`); }
  else { console.log(`ok   ${name}`); }
}
function mk(v, b, custom, edit, input) {
  return { val: v, base: b, custom: custom || false, edit: edit || false, input: input || '' };
}

// 修复 1：点击标准档位 = 清除该栏自定义（输入框消失，值=该档）
eq('clickTier 退出输入态', P.clickTier(mk(75, 120, true, true, '75'), 90),
  { val: 90, base: 120, custom: true, edit: false, input: '' });

// 修复 2：打开浮窗推断——仅"custom 且值不在档位列表"才显示输入框，内容=自定义值（非空）
eq('infer 自定义值非档位 → 输入态且内容=75',
  P.infer(mk(75, 120, true, false, ''), [120, 90, 60, 30]),
  { val: 75, base: 120, custom: true, edit: true, input: '75' });
eq('infer 自定义值在档位内 → 纯档位选择态',
  P.infer(mk(90, 120, true, false, ''), [120, 90, 60, 30]),
  { val: 90, base: 120, custom: true, edit: false, input: '' });
eq('infer 未自定义 → 无输入框',
  P.infer(mk(120, 120, false, false, ''), [120, 90, 60, 30]),
  { val: 120, base: 120, custom: false, edit: false, input: '' });

// ＋自定义 / 输入 / 提交
eq('startCustom 初始为空输入态', P.startCustom(mk(90, 120, false, false, '')),
  { val: 90, base: 120, custom: false, edit: true, input: '' });
eq('typeCustom 仅记录文本', P.typeCustom(mk(90, 120, false, true, ''), '80'),
  { val: 90, base: 120, custom: false, edit: true, input: '80' });
eq('commitCustom 合法值', P.commitCustom(mk(90, 120, false, true, '80'), '80'),
  { val: 80, base: 120, custom: false, edit: true, input: '80' });
eq('commitCustom 空输入回 baseline', P.commitCustom(mk(75, 120, true, true, ''), ''),
  { val: 120, base: 120, custom: true, edit: false, input: '' });
eq('commitCustom 非法输入回 baseline', P.commitCustom(mk(75, 120, true, true, 'abc'), 'abc'),
  { val: 120, base: 120, custom: true, edit: false, input: '' });
eq('resetField 回 baseline', P.resetField(mk(75, 120, true, true, '75')),
  { val: 120, base: 120, custom: true, edit: false, input: '' });

// 保存结算
eq('settle 输入态非空 → 自定义值=输入', P.settle(mk(80, 120, false, true, '80')),
  { value: 80, custom: true });
eq('settle 输入态为空 → baseline 自动档', P.settle(mk(80, 120, false, true, '')),
  { value: 120, custom: false });
eq('settle 档位点击（值≠baseline）→ 自定义值=档位', P.settle(mk(90, 120, false, false, '')),
  { value: 90, custom: true });
eq('settle 未改动 → baseline 自动档', P.settle(mk(120, 120, false, false, '')),
  { value: 120, custom: false });

if (failed) { console.error(`\n${failed} 项失败`); process.exit(1); }
console.log('\nparam_state 全部通过');
