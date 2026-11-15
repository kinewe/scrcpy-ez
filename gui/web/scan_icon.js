// 「微信扫一扫」设备卡按钮的专属图标（v2.4.2-rc.29 UI 修正）：
//   四角取景框 + 中央品牌绿扫描线 = 扫一扫的通用意象；24 网格、2px 线宽、圆头，
//   与 EZ 现有 Lucide 线性图标同族。框线用 currentColor 继承按钮前景色，扫描线
//   由 CSS 固定为品牌绿。
//
// 动效：悬停扫动由 app.js 用 requestAnimationFrame 驱动（快照会整体重建设备卡，
//   CSS 动画会从头开始闪一下；JS 相位按设备键保持，重建后无缝续播）。本模块只提供
//   纯函数：扫描周期/回位时长与两道相位曲线，便于 node 单测。
// 纯字符串构造、无 DOM 依赖——node 单测覆盖（scan_icon_test.js）。
(function (root, factory) {
  if (typeof module === 'object' && module.exports) {
    module.exports = factory();
  } else {
    root.SCEZScanIcon = factory();
  }
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  // 四角取景框：每角一段 2.5 圆角折线，框内缩 4.5（24×24 网格）。
  var FRAME = 'M4.5 9.5V7A2.5 2.5 0 0 1 7 4.5h2.5' +
              'M14.5 4.5H17A2.5 2.5 0 0 1 19.5 7v2.5' +
              'M19.5 14.5V17A2.5 2.5 0 0 1 17 19.5h-2.5' +
              'M9.5 19.5H7A2.5 2.5 0 0 1 4.5 17v-2.5';
  // 中央扫描线：两端各留 3.5 视觉安全区（与四角框拉大间隔），圆头。
  var BEAM = 'M8 12h8';

  var SVG = '<svg class="scan-ico" viewBox="0 0 24 24" fill="none" ' +
    'stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">' +
    '<path class="scan-ico-frame" d="' + FRAME + '" stroke="currentColor" stroke-width="2"/>' +
    '<path class="scan-ico-beam" d="' + BEAM + '" stroke="currentColor" stroke-width="2.2"/>' +
    '</svg>';

  var SWEEP_MS = 1400;   // 一个完整扫动周期
  var RESTORE_MS = 260;  // 离开后从底部淡入回中位的时长
  var AMPLITUDE = 2.5;   // 扫描线相对中位的最大偏移（取景框内）

  function clamp01(v) { return v < 0 ? 0 : (v > 1 ? 1 : v); }

  // sweepPose(p)：悬停扫动相位（p=0 顶部淡入 → p=1 底部淡出）。
  // 返回 { y: 相对中位偏移(px), opacity: 0..1 }。
  function sweepPose(p) {
    var t = clamp01(p);
    var fade = 0.18;
    var opacity;
    if (t < fade) opacity = t / fade;
    else if (t > 1 - fade) opacity = (1 - t) / fade;
    else opacity = 1;
    return { y: -AMPLITUDE + AMPLITUDE * 2 * t, opacity: clamp01(opacity) };
  }

  // restorePose(p)：离开后的回位相位（p=0 底部隐没 → p=1 中位完全可见）。
  function restorePose(p) {
    var t = clamp01(p);
    var ease = 1 - Math.pow(1 - t, 3); // ease-out cubic：快速上层、柔和落定
    return { y: AMPLITUDE * (1 - ease), opacity: ease };
  }

  function svg() {
    return SVG;
  }

  return {
    svg: svg,
    sweepMs: SWEEP_MS,
    restoreMs: RESTORE_MS,
    amplitude: AMPLITUDE,
    sweepPose: sweepPose,
    restorePose: restorePose
  };
});
