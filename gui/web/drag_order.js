// drag_order.js —— 浏览器式标签拖拽排序组件（顶部 tabs 与投屏中设备标签行共用）。
// 数据流（映射关系核心）：
//   order 数组（标签 key 顺序）是唯一顺序数据源；渲染按 order 输出；
//   拖拽中越过相邻项中线 → 实时 splice 移动 order + DOM 换位（FLIP 动画）；
//   未过半松手/拖出标签行 → 回弹原位（order 恢复起点快照）。
// 事件约定：
//   拖拽期间绝不触发任何激活/点击回调（Go 调用零接触——浮前/切换均不触发）；
//   位移未超阈值松手 = 点击（原有 click 监听器照常生效，组件仅拦截拖拽后的 click）。
//   pointer capture 只在进入拖拽（越过阈值）时设置：pointerdown 即 capture 会把
//   合成 click 的 target 变成容器，子标签的 click 监听器收不到 → 点击失效。
// 纯函数（moveOrder/targetIndex）node 可单测（drag_order_test.js）。
(function (root, factory) {
  'use strict';
  if (typeof module === 'object' && module.exports) {
    module.exports = factory();
  } else {
    root.DragOrder = factory();
  }
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  var DRAG_THRESHOLD = 6; // 位移超过该像素才视为拖拽（否则=点击，浏览器同款）
  var ANIM_MS = 160;      // 交换/回弹动画时长（150-200ms ease，浏览器标签换位同款）

  // --- 纯函数 ---

  // moveOrder：把 order[from] 移到 to（插入语义），返回新数组（不修改入参）。
  function moveOrder(order, from, to) {
    var out = order.slice();
    if (from < 0 || from >= out.length || to < 0 || to >= out.length || from === to) {
      return out;
    }
    var key = out.splice(from, 1)[0];
    out.splice(to, 0, key);
    return out;
  }

  // targetIndex：拖动项当前应插入的索引（插入语义，兼容宽度不等的标签）。
  // centers = 其余各项中心 x（拖动项自身不算）；cx = 拖动项中心当前 x。
  // 规则：cx 越过某项中心 → 该项计入其左侧 → 目标索引 = 计数（过中线即互换）。
  function targetIndex(centers, cx) {
    var idx = 0;
    for (var i = 0; i < centers.length; i++) {
      if (centers[i] < cx) idx++;
    }
    return idx;
  }

  // devOrderMerge(existing, seenKeys, knownKeys) -> 新 order：
  //   existing 中仍在 seenKeys 的保留原相对顺序；
  //   seenKeys 中不在 existing 的追加尾部；
  //   从 existing 移除不在 seenKeys 且不在 knownKeys 的键（清理）。
  function devOrderMerge(existing, seenKeys, knownKeys) {
    var seen = {};
    var out = existing.slice();
    seenKeys.forEach(function (k) { seen[k] = true; });
    seenKeys.forEach(function (k) {
      if (out.indexOf(k) < 0) out.push(k);
    });
    var known = {};
    (knownKeys || []).forEach(function (k) { known[k] = true; });
    var result = out.filter(function (k) {
      return seen[k] || known[k];
    });
    return result;
  }

  // --- DOM 绑定 ---

  // attach(container, opts)：把拖拽排序绑到 flex 行容器。
  //   container：.tabs / #cast-tabbar（子元素即标签，顺序=order 数组顺序）
  //   opts.getOrder() -> [key,...]（key 与 DOM 子元素一一对应）
  //   opts.setOrder(next)（组件实时回写；落地后调用 opts.onDrop(order) 可空）
  //   opts.keyOf(el) -> key（top tabs 用 data-view；设备标签用 data-key）
  function attach(container, opts) {
    var axis = opts.axis || 'x';
    var activeEl = null;
    var activePointer = -1;   // 本次按压的 pointerId（拖拽开始才 capture；松手释放）
    var dragging = false;     // 已越过位移阈值
    var suppressClick = false;
    var startX = 0, lastX = 0, startY = 0, lastY = 0;
    var startCenter = 0;      // 拖动项起点中心 x（layout，不含 transform）
    var fromIdx = -1;         // 拖动项起点索引
    var startOrder = null;    // 起点顺序快照（回弹用）

    function dx() { return axis === 'y' ? lastY - startY : lastX - startX; }

    function translate(el, px) {
      el.style.transform = axis === 'y' ? 'translateY(' + px + 'px)' : 'translateX(' + px + 'px)';
    }

    function children() { return Array.prototype.slice.call(container.children); }

    // 布局测量（offsetLeft/offsetWidth 不含 transform——FLIP 动画进行中测量不抖）
    function measure() {
      return children().map(function (el) {
        var off = axis === 'y' ? el.offsetTop : el.offsetLeft;
        var size = axis === 'y' ? el.offsetHeight : el.offsetWidth;
        return {
          el: el,
          left: off,
          width: size,
          center: off + size / 2
        };
      });
    }

    function indexOfEl(el) {
      var m = measure();
      for (var i = 0; i < m.length; i++) if (m[i].el === el) return i;
      return -1;
    }

    // 动画结束后清掉内联 transition/transform（还原类样式——casttab 淡入淡出靠类 transition）
    function clearAnim(el) {
      setTimeout(function () {
        if (el && el.style) {
          el.style.transition = '';
          el.style.transform = '';
        }
      }, ANIM_MS + 60);
    }

    // 把拖动项换到 to（插入语义）+ 其余项 FLIP 动画让位；拖动项保持跟手
    function moveDom(from, to) {
      var m = measure();
      var moved = m[from].el;
      // 1) order 同步（实时；松手不再改）
      opts.setOrder(moveOrder(opts.getOrder(), from, to));
      // 2) DOM 换位（节点移动，事件监听器不丢）
      var kids = children();
      if (to > from) {
        container.insertBefore(moved, kids[to + 1] || null);
      } else if (to < from) {
        container.insertBefore(moved, kids[to]);
      }
      // 3) FLIP：其余项用反位移平滑过渡到新位置
      var after = measure();
      for (var j = 0; j < after.length; j++) {
        var el = after[j].el;
        if (el === moved) continue;
        var oldLeft = null;
        for (var i = 0; i < m.length; i++) {
          if (m[i].el === el) { oldLeft = m[i].left; break; }
        }
        if (oldLeft === null) continue;
        var delta = oldLeft - after[j].left;
        if (Math.abs(delta) < 0.5) continue;
        el.style.transition = 'none';
        translate(el, delta);
        void el.offsetWidth; // 强制回流（反位移先生效）
        el.style.transition = 'transform ' + ANIM_MS + 'ms ease';
        el.style.transform = '';
        clearAnim(el);
      }
      // （拖动项的跟手 transform 由 pointermove 每帧按新 slot 重算——此处不设，
      //   避免与帧内重算重复且"旧参照"残留造成视觉飞跳）
    }

    // 按 orderArr 重排 DOM + FLIP 动画（回弹/外部重排共用）。
    // 拖动项特殊处理：视觉起点=跟手位置，过渡回新 slot（回弹动画）。
    function applyOrder(orderArr) {
      var m = measure();
      var prev = {};
      var byKey = {};
      m.forEach(function (it) {
        prev[opts.keyOf(it.el)] = it.left;
        byKey[opts.keyOf(it.el)] = it;
      });
      orderArr.forEach(function (key) {
        var it = byKey[key];
        if (it) container.appendChild(it.el);
      });
      var after = measure();
      after.forEach(function (it) {
        var delta;
        if (activeEl && it.el === activeEl) {
          var newCenter = it.left + it.width / 2;
          delta = startCenter + dx() - newCenter;
        } else {
          var old = prev[opts.keyOf(it.el)];
          if (old === undefined) return;
          delta = old - it.left;
        }
        if (Math.abs(delta) < 0.5) return;
        it.el.style.transition = 'none';
        translate(it.el, delta);
        void it.el.offsetWidth;
        it.el.style.transition = 'transform ' + ANIM_MS + 'ms ease';
        it.el.style.transform = '';
        clearAnim(it.el);
      });
    }

    // 落位：清除拖动态 + 过渡到最终位置（从当前 transform 平滑滑入新 slot）
    function settle() {
      if (!activeEl) return;
      activeEl.style.transition = 'transform ' + ANIM_MS + 'ms ease';
      activeEl.style.transform = '';
      clearAnim(activeEl);
      activeEl.classList.remove('dragging');
      if (activePointer >= 0) {
        try { container.releasePointerCapture(activePointer); } catch (e) { /* 忽略 */ }
      }
      activePointer = -1;
      activeEl = null;
      dragging = false;
      container.classList.remove('drag-active');
      document.body.classList.remove('drag-body');
    }

    container.addEventListener('pointerdown', function (ev) {
      if (ev.button !== undefined && ev.button !== 0) return; // 仅主键
      var el = ev.target;
      while (el && el.parentNode !== container) el = el.parentNode;
      if (!el) return;
      // 无 key 的元素（如设备列表的 pair-entry 按钮）不参与拖拽排序——
      // 否则入口按钮会被当卡片一起拖（2026-09-02 实测：配对新设备入口可被拖动）。
      if (!opts.keyOf(el)) return;
      // 批量模式等场景可整体禁用拖拽（opts.enabled 回调，默认开启）。
      if (opts.enabled && !opts.enabled()) return;
      fromIdx = indexOfEl(el);
      if (fromIdx < 0) return;
      activeEl = el;
      activePointer = ev.pointerId;
      dragging = false;
      suppressClick = false;
      lastX = ev.clientX;
      startX = ev.clientX;
      startY = ev.clientY;
      lastY = ev.clientY;
      var m = measure();
      startCenter = m[fromIdx].center;
      startOrder = opts.getOrder().slice();
      // 注意：这里不 setPointerCapture——一旦 pointerdown 就 capture，浏览器合成的
      // click target 会变成 container（而非被点的 .tab/.casttab），子元素的 click
      // 监听器（switchView/activateSession/BringCastToFront）永远收不到 → 点击失效。
      // capture 推迟到真正进入拖拽（越过阈值）时才设置（见 pointermove）。
    });

    function finish(ev, outRow) {
      if (!activeEl) return;
      var wasDragging = dragging;
      try {
        if (!wasDragging) {
          // 普通点击：不碰任何内联样式，click 交给子标签监听器处理
          activeEl = null;
          activePointer = -1;
          return;
        }
        var cur = indexOfEl(activeEl);
        if (outRow || cur === fromIdx) {
          // 回弹：拖出标签行 / 未过半 → 恢复起点顺序
          opts.setOrder(startOrder);
          applyOrder(startOrder);
        } else {
          // 已换位：抑制本次 click（不激活）；通知落位
          if (opts.onDrop) opts.onDrop(opts.getOrder());
        }
      } finally {
        // gui44-fix2：settle 必须执行（onDrop/applyOrder 异常也不能残留跟手
        // transform / drag-active——否则卡片"吸在鼠标"、列表重建被跳过）。
        if (wasDragging) {
          suppressClick = true;
          settle();
        }
      }
    }

    // gui44-fix2：move/up/cancel 挂在 window——纵向拖拽指针容易移出容器，
    // WebView2 下 setPointerCapture 不保证收到跨出容器的 up（横向标签行窄
    // 移动小未暴露）。window 级监听覆盖指针任意位置；无拖拽时 move 直接 return。
    window.addEventListener('pointermove', function (ev) {
      if (!activeEl) return;
      lastX = ev.clientX;
      lastY = ev.clientY;
      var ddx = ev.clientX - startX;
      var ddy = ev.clientY - startY;
      if (!dragging) {
        if (Math.abs(ddx) < DRAG_THRESHOLD && Math.abs(ddy) < DRAG_THRESHOLD) return;
        // 大幅度垂直/水平位移：不进入拖拽（保持点击语义），但吞掉本次 click（防误激活）
        if (axis === 'y' ? (Math.abs(ddx) > Math.abs(ddy) && Math.abs(ddx) > DRAG_THRESHOLD * 2)
                         : (Math.abs(ddy) > Math.abs(ddx) && Math.abs(ddy) > DRAG_THRESHOLD * 2)) {
          activeEl = null;
          activePointer = -1;
          suppressClick = true;
          return;
        }
        dragging = true;
        container.classList.add('drag-active');
        document.body.classList.add('drag-body');
        activeEl.classList.add('dragging');
        // 进入拖拽才 capture：此后 click 由下方 capture 监听器按 suppressClick 吞掉
        try { container.setPointerCapture(ev.pointerId); } catch (e) { /* 忽略 */ }
      }
      // 过中线判定：其余项中心 vs 拖动项视觉中心（视觉中心=起点中心+位移，不随换位变）
      // gui44-fix：y 轴用垂直位移（ddx 是水平——向上拖 ddx≈0 会钉死/误判方向）
      var visualX = startCenter + (axis === 'y' ? ddy : ddx);
      var m = measure();
      var centers = [];
      for (var i = 0; i < m.length; i++) {
        if (m[i].el !== activeEl) centers.push(m[i].center);
      }
      var to = Math.min(targetIndex(centers, visualX), m.length - 1);
      var cur = indexOfEl(activeEl);
      if (to !== cur) moveDom(cur, to);
      // 每帧重算跟手 transform：换位后 activeEl 的 slot 布局中心已变——
      // transform = 视觉中心 - 当前 slot 布局中心（修复换位后"飞到更远"）
      var m2 = measure();
      var slot = null;
      for (var j = 0; j < m2.length; j++) {
        if (m2[j].el === activeEl) { slot = m2[j]; break; }
      }
      if (slot) {
        activeEl.style.transition = 'none';
        translate(activeEl, (visualX - slot.center));
      }
    });

    window.addEventListener('pointerup', function (ev) {
      var r = container.getBoundingClientRect();
      var outRow = axis === 'y'
        ? (ev.clientX < r.left - 8 || ev.clientX > r.right + 8)
        : (ev.clientY < r.top - 8 || ev.clientY > r.bottom + 8);
      finish(ev, outRow);
    });
    window.addEventListener('pointercancel', function (ev) {
      finish(ev, true);
    });

    // 拖拽后的 click 一律吞掉（激活回调不触发）；正常点击放行。
    container.addEventListener('click', function (ev) {
      if (suppressClick) {
        suppressClick = false;
        ev.stopPropagation();
        ev.preventDefault();
      }
    }, true);

    return { moveOrder: moveOrder, targetIndex: targetIndex };
  }

  return {
    attach: attach,
    moveOrder: moveOrder,
    targetIndex: targetIndex,
    devOrderMerge: devOrderMerge,
    DRAG_THRESHOLD: DRAG_THRESHOLD,
    ANIM_MS: ANIM_MS
  };
});
