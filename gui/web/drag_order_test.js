// drag_order.js 纯函数单测（node drag_order_test.js）：
// moveOrder（插入语义 splice 移动）/ targetIndex（过中线计数=插入索引）。
(function () {
  'use strict';
  var DO = require('./drag_order.js');
  var ok = 0, fail = 0;
  function t(name, cond) {
    if (cond) { ok++; console.log('ok   ' + name); }
    else { fail++; console.log('FAIL ' + name); }
  }

  // moveOrder：从 from 移到 to（插入语义）
  var o = ['A', 'B', 'C', 'D'];
  t('moveOrder 右移一位', DO.moveOrder(o, 0, 1).join() === 'B,A,C,D');
  t('moveOrder 右移多位', DO.moveOrder(o, 0, 3).join() === 'B,C,D,A');
  t('moveOrder 左移一位', DO.moveOrder(o, 3, 2).join() === 'A,B,D,C');
  t('moveOrder 左移多位', DO.moveOrder(o, 3, 0).join() === 'D,A,B,C');
  t('moveOrder 原位不动', DO.moveOrder(o, 1, 1).join() === 'A,B,C,D');
  t('moveOrder 不修改入参', o.join() === 'A,B,C,D');
  t('moveOrder 越界安全', DO.moveOrder(o, -1, 2).join() === 'A,B,C,D');
  t('moveOrder 单元素', DO.moveOrder(['X'], 0, 0).join() === 'X');

  // targetIndex：cx 越过某项中心 → 计数（插入索引）
  // centers = 其余项中心（升序）
  t('targetIndex 未过半 → 原位', DO.targetIndex([30, 70], 10) === 0);
  t('targetIndex 越过第一项中心 → 互换', DO.targetIndex([30, 70], 50) === 1);
  t('targetIndex 越过两项 → 末尾', DO.targetIndex([30, 70], 90) === 2);
  t('targetIndex 边界：压线视为未越过（未完全拖过去）', DO.targetIndex([30, 70], 70) === 1);
  t('targetIndex 空表', DO.targetIndex([], 10) === 0);
  // 宽度不等场景：中心不匀（30/80），cx=55 只越过第一项
  t('targetIndex 不等宽中心', DO.targetIndex([30, 80], 55) === 1);
  t('targetIndex 三个中心', DO.targetIndex([20, 50, 90], 75) === 2);

  // devOrderMerge：设备卡顺序持久化合并（gui44）
  t('devOrderMerge 瞬态缺失保留（B 在档案 known）', DO.devOrderMerge(['A','B','C'], ['A','C'], ['A','B','C']).join() === 'A,B,C');
  t('devOrderMerge 新设备追加', DO.devOrderMerge(['A'], ['A','B'], []).join() === 'A,B');
  t('devOrderMerge 出档清理', DO.devOrderMerge(['A','B','C'], ['A'], ['A']).join() === 'A');
  t('devOrderMerge 档案键保留', DO.devOrderMerge(['A','B'], ['A'], ['A','B']).join() === 'A,B');
  t('devOrderMerge 空 seen', DO.devOrderMerge(['A','B'], [], []).join() === '');

  console.log('\ndrag_order 全部通过' + (fail ? '（有失败）' : ''));
  if (fail) process.exit(1);
})();
