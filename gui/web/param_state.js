// 参数浮窗纯状态机（浏览器全局 + node 可 require 单测）。
// 字段级状态 st = { val: 当前值, base: 该模式 bat 基准值, custom: 档案是否自定义,
//                  edit: 是否处于自定义输入态, input: 输入框文本 }
// 修复点：
//   1) 点击标准档位 chip = 清除该栏自定义（输入框消失，值=该档）；
//   2) 打开浮窗推断：仅当"已自定义且值不在档位列表"→ 输入态且输入框=自定义值（非空）；
//   3) 空/非法输入提交 → 回 baseline（取消自定义）；
//   4) 保存结算：输入态非空 → 自定义值=输入；非输入态值≠baseline（档位点击）→ 自定义值=档位；
//      否则 baseline（自动档，不注入）。
(function (root, factory) {
  'use strict';
  if (typeof module === 'object' && module.exports) {
    module.exports = factory();
  } else {
    root.SCEZParamState = factory();
  }
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  function withBase(st, base) { return { val: st.val, base: base, custom: st.custom, edit: st.edit, input: st.input }; }

  // 点击标准档位：值=该档，退出自定义输入态（输入框消失）
  function clickTier(st, v) {
    return { val: v, base: st.base, custom: st.custom, edit: false, input: '' };
  }

  // 点"＋自定义"：进入输入态，初始为空（placeholder 提示"输入后生效"）
  function startCustom(st) {
    return { val: st.val, base: st.base, custom: st.custom, edit: true, input: '' };
  }

  // 输入框实时输入：仅记录文本（change 时才提交）
  function typeCustom(st, text) {
    return { val: st.val, base: st.base, custom: st.custom, edit: true, input: text };
  }

  // 输入提交（change）：合法正整数 → 值=输入、保持输入态；
  // 空/非法 → 取消自定义回 baseline（输入态退出）
  function commitCustom(st, text) {
    var n = parseInt(text, 10);
    if (n > 0) {
      return { val: n, base: st.base, custom: st.custom, edit: true, input: String(n) };
    }
    return { val: st.base, base: st.base, custom: st.custom, edit: false, input: '' };
  }

  // 重置：回 baseline（输入态退出）
  function resetField(st) {
    return { val: st.base, base: st.base, custom: st.custom, edit: false, input: '' };
  }

  // 打开浮窗推断：仅当"已自定义且自定义值不在档位列表"→ 输入态且输入框内容=自定义值（非空）；
  // 否则纯档位选择态（无输入框）
  function infer(st, tiers) {
    if (st.custom && tiers.indexOf(st.val) < 0) {
      return { val: st.val, base: st.base, custom: st.custom, edit: true, input: String(st.val) };
    }
    return { val: st.val, base: st.base, custom: st.custom, edit: false, input: '' };
  }

  // 保存结算 → {value, custom}：
  //   - 输入态且输入非空 → 自定义值=输入（custom=true）；
  //   - 非输入态且值≠baseline（档位点击）→ 自定义值=档位（custom=true）；
  //   - 否则 → baseline（custom=false，自动档不注入）
  function settle(st) {
    if (st.edit && st.input !== '') {
      var n = parseInt(st.input, 10) || st.val;
      return { value: n, custom: true };
    }
    if (!st.edit && st.val !== st.base) {
      return { value: st.val, custom: true };
    }
    return { value: st.base, custom: false };
  }

  return {
    withBase: withBase,
    clickTier: clickTier,
    startCustom: startCustom,
    typeCustom: typeCustom,
    commitCustom: commitCustom,
    resetField: resetField,
    infer: infer,
    settle: settle
  };
});
