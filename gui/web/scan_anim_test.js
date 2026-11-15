'use strict';
// 扫一扫动效控制器回归测试（node gui/web/scan_anim_test.js）：
//   从 app.js 抽取真实的扫动相位控制器，用可手动推进的假 requestAnimationFrame 驱动，覆盖：
//     1) 悬停开始 → 周期进行到中段；
//     2) 快照重建设备卡（节点被替换）→ 相位不重置、姿态写到新节点；
//     3) 鼠标移开 → 等当前周期自然播完，再快速淡入回中位（初始态）；
//     4) 重建发生在“等待收尾”期间 → finishAt 不丢；
//     5) prefers-reduced-motion → 不启动扫动。
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const Icon = require('./scan_icon.js');
const appJs = fs.readFileSync(path.join(__dirname, 'app.js'), 'utf8');
let checks = 0;
function ok(value, message) { assert.ok(value, message); checks++; }
function eq(actual, expected, message) { assert.equal(actual, expected, message); checks++; }
function near(actual, expected, message) { assert.ok(Math.abs(actual - expected) < 1e-6, message + ' (got ' + actual + ')'); checks++; }

function extractFunction(name) {
  const start = appJs.indexOf('  function ' + name + '(');
  assert.ok(start >= 0, 'app.js 缺少函数：' + name);
  const lineEnd = appJs.indexOf('\n', start);
  if (appJs.slice(start, lineEnd).trimEnd().endsWith('}')) return appJs.slice(start, lineEnd);
  const end = appJs.indexOf('\n  }', lineEnd);
  assert.ok(end > 0, 'function end not found: ' + name);
  return appJs.slice(start, end + 4);
}

function makeBeam() { return { style: {} }; }
function makeButton(beam) {
  return {
    dataset: {},
    style: {},
    classes: {},
    classList: {
      add: function (c) { this.owner.classes[c] = true; },
      remove: function (c) { delete this.owner.classes[c]; },
      contains: function (c) { return !!this.owner.classes[c]; }
    },
    querySelector: function (sel) { return sel === '.scan-ico-beam' ? beam : null; }
  };
}
// classList 需要回指按钮
function fakeButton(beam) { const b = makeButton(beam); b.classList.owner = b; return b; }

function makeScope() {
  const scope = {
    SCEZScanIcon: Icon,
    pointerTarget: null,
    pointerHoverEl: null,
    scanButtons: {},
    scanStates: {},
    scanRaf: null,
    scanReducedMotion: false,
    __now: 1000,
    __rafCb: null,
    __rafCount: 0,
    requestAnimationFrame: function (fn) { scope.__rafCb = fn; scope.__rafCount += 1; return scope.__rafCount; },
    document: { addEventListener: function () {} },
    window: {
      performance: { now: function () { return scope.__now; } },
      matchMedia: function () { return { matches: false }; }
    }
  };
  scope.window.window = scope.window;
  vm.createContext(scope);
  for (const name of ['scanNow', 'beginScanButtonRegistration', 'registerScanButton', 'clearPointerHover',
    'markPointerHover', 'scanTargetKey', 'scanApplyPose', 'scanRestPose', 'scanTick',
    'scanStartLoop', 'scanAnimationSync']) {
    vm.runInContext(extractFunction(name), scope);
  }
  return scope;
}

function tickAt(scope, t) {
  assert.ok(typeof scope.__rafCb === 'function', 'raf callback scheduled');
  const cb = scope.__rafCb;
  scope.__rafCb = null;
  scope.__now = t;
  cb(t);
  return scope;
}

// ---------- 1) 悬停开始：产生 sweep 状态并推进到中段 ----------
const scope = makeScope();
const beam1 = makeBeam();
const btn1 = fakeButton(beam1);
scope.pointerTarget = { key: 'dev:A', role: 'scan' };
scope.beginScanButtonRegistration();
scope.registerScanButton(btn1, 'dev:A');
scope.scanAnimationSync();
eq(scope.scanStates['dev:A'].phase, 'sweep', 'hover starts a sweep state');
const t0 = scope.scanStates['dev:A'].t0;
near(t0, 1000, 'sweep keeps its start timestamp');
tickAt(scope, t0 + 700);
near(parseFloat(beam1.style.transform.replace(/[^0-9.-]/g, '')), 0, 'mid-cycle beam crosses the center');
near(parseFloat(beam1.style.opacity), 1, 'mid-cycle beam is fully visible');

// ---------- 2) 快照重建：节点替换后相位继续（不从头开始） ----------
const beam2 = makeBeam();
const btn2 = fakeButton(beam2);
scope.beginScanButtonRegistration();
scope.registerScanButton(btn2, 'dev:A');
scope.scanAnimationSync();
eq(scope.scanStates['dev:A'].t0, t0, 'card rebuild keeps the sweep phase (start timestamp unchanged)');
tickAt(scope, t0 + 840);
const yAfterRebuild = parseFloat(beam2.style.transform.replace(/[^0-9.-]/g, ''));
ok(yAfterRebuild > 0, 'after rebuild the beam continues downward on the new node');
ok(!!beam2.style.opacity, 'after rebuild the pose is written to the new beam node');
eq(beam1.style.transform === undefined, false, 'old node keeps its last pose but is no longer updated');

// ---------- 3) 鼠标移开：等当前周期播完，再快速淡入回中位 ----------
scope.pointerTarget = null;
scope.scanAnimationSync();
eq(scope.scanStates['dev:A'].phase, 'finish', 'leave switches to finish (does not snap to rest)');
eq(scope.scanStates['dev:A'].finishAt, t0 + Icon.sweepMs, 'finish waits for the end of the current cycle');
tickAt(scope, t0 + Icon.sweepMs - 100);
eq(scope.scanStates['dev:A'].phase, 'finish', 'still sweeping while the current cycle finishes');
tickAt(scope, t0 + Icon.sweepMs);
eq(scope.scanStates['dev:A'].phase, 'restore', 'cycle end starts the quick fade-in restore');
near(parseFloat(beam2.style.opacity), 0, 'restore starts invisible at the bottom');
tickAt(scope, t0 + Icon.sweepMs + Math.round(Icon.restoreMs / 2));
const restoreOpacity = parseFloat(beam2.style.opacity);
ok(restoreOpacity > 0 && restoreOpacity < 1, 'restore fades the beam back in');
ok(parseFloat(beam2.style.transform.replace(/[^0-9.-]/g, '')) > 0, 'restore rises from the bottom toward center');
tickAt(scope, t0 + Icon.sweepMs + Icon.restoreMs);
eq(scope.scanStates['dev:A'].phase, 'rest', 'restore settles into the rest phase');
eq(beam2.style.transform, 'translateY(0px)', 'rest pose is the centered initial state');
eq(beam2.style.opacity, '1', 'rest pose is fully visible');

// ---------- 4) 重建发生在等待收尾期间：finishAt 不丢 ----------
const scope2 = makeScope();
const beamA = makeBeam();
scope2.pointerTarget = { key: 'dev:B', role: 'scan' };
scope2.beginScanButtonRegistration();
scope2.registerScanButton(fakeButton(beamA), 'dev:B');
scope2.scanAnimationSync();
const t0b = scope2.scanStates['dev:B'].t0;
tickAt(scope2, t0b + 300);
scope2.pointerTarget = null;
scope2.scanAnimationSync();
const finishAt = scope2.scanStates['dev:B'].finishAt;
eq(finishAt, t0b + Icon.sweepMs, 'finish boundary is the next cycle end');
const beamB = makeBeam();
scope2.beginScanButtonRegistration();
scope2.registerScanButton(fakeButton(beamB), 'dev:B');
scope2.scanAnimationSync();
eq(scope2.scanStates['dev:B'].finishAt, finishAt, 'card rebuild preserves the pending finish boundary');
tickAt(scope2, finishAt - 1);
eq(scope2.scanStates['dev:B'].phase, 'finish', 'still finishing right before the boundary');
tickAt(scope2, finishAt);
eq(scope2.scanStates['dev:B'].phase, 'restore', 'rebuild does not lose the leave sequence');

// ---------- 5) prefers-reduced-motion：不启动扫动 ----------
const scope3 = makeScope();
scope3.scanReducedMotion = true;
scope3.pointerTarget = { key: 'dev:C', role: 'scan' };
scope3.beginScanButtonRegistration();
scope3.registerScanButton(fakeButton(makeBeam()), 'dev:C');
scope3.scanAnimationSync();
eq(scope3.scanStates['dev:C'], undefined, 'reduced motion keeps the beam static (no sweep state)');

// ---------- 6) 悬停中重建时指针悬停类也补回 ----------
const scope4 = makeScope();
scope4.pointerTarget = { key: 'dev:D', role: 'app' };
const newBtn = fakeButton(makeBeam());
scope4.markPointerHover(newBtn, 'dev:D', 'app');
ok(newBtn.classList.contains('pt-hover'), 'rebuilt card re-applies the pointer hover class');
eq(scope4.pointerHoverEl, newBtn, 'pointer hover element is tracked for cleanup');
newBtn.classList.owner = newBtn;
scope4.clearPointerHover();
ok(!newBtn.classList.contains('pt-hover'), 'pointer hover class is cleared when the pointer moves away');

console.log('SCAN_ANIM_TESTS_OK ' + checks);
