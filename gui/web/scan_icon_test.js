'use strict';
// 「微信扫一扫」专属图标回归测试（node gui/web/scan_icon_test.js）：
//   1) 造型契约：四角取景框 + 中央品牌绿扫描线的 24 网格线性图标；
//   2) 动效契约：扫动/回位相位函数（纯函数，节点重建设备卡后可续播/回位）；
//   3) 样式契约：方形图标按钮 + 品牌绿扫描线 + 快照重建后的 .pt-hover 兜底；
//   4) 装配契约：Go 内联模块与 app.js 动画控制器的接线。
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const Icon = require('./scan_icon.js');
const svg = Icon.svg();
let checks = 0;
function ok(value, message) { assert.ok(value, message); checks++; }
function eq(actual, expected, message) { assert.equal(actual, expected, message); checks++; }
function near(actual, expected, message) { assert.ok(Math.abs(actual - expected) < 1e-9, message + ' (got ' + actual + ')'); checks++; }
function match(re, text, message) { const m = text.match(re); assert.ok(m, message); checks++; return m; }

// ---------- 01 造型：四角取景框 + 一根扫描线 ----------
ok(svg.startsWith('<svg'), 'svg root');
ok(svg.includes('viewBox="0 0 24 24"'), '24x24 grid like the bundled Lucide family');
ok(svg.includes('class="scan-ico"'), 'stable icon class for CSS hooks');
ok(svg.includes('aria-hidden="true"') && svg.includes('focusable="false"'), 'decorative svg is hidden from AT');
ok(svg.includes('stroke-linecap="round"') && svg.includes('stroke-linejoin="round"'), 'round caps/joins match EZ line icons');

const frame = match(/<path class="scan-ico-frame" d="([^"]+)" stroke="currentColor" stroke-width="2"\/>/, svg, 'frame path exists');
eq((frame[1].match(/M/g) || []).length, 4, 'frame is exactly four corner subpaths');
eq((frame[1].match(/A2\.5 2\.5/g) || []).length, 4, 'each corner has a 2.5 rounded elbow');
ok(frame[1].startsWith('M4.5 9.5'), 'frame inset 4.5 keeps the icon optically centered');

const beam = match(/<path class="scan-ico-beam" d="([^"]+)" stroke="currentColor" stroke-width="2\.2"\/>/, svg, 'scan beam path exists');
eq(beam[1], 'M8 12h8', 'beam is one centered horizontal line with a clear gap to the frame');
const beamStartX = parseFloat(beam[1].replace(/^M/, ''));
ok(beamStartX - 4.5 >= 3.4, 'beam keeps a >=3.5 unit gap from the corner frame (airier look)');
ok(!svg.includes('<text'), 'icon-only: never renders words inside the glyph');
ok(!/[\u4e00-\u9fff]/.test(svg), 'icon markup carries no CJK text');
eq(svg, Icon.svg(), 'svg() is a stable pure string (idempotent)');

// ---------- 02 动效相位（JS 驱动，跨快照重建续播） ----------
eq(Icon.sweepMs, 1400, 'one sweep cycle is 1.4s');
eq(Icon.restoreMs, 260, 'leave-restore fade is quick (260ms)');
eq(Icon.amplitude, 2.5, 'beam sweeps within the viewfinder (2.5 units)');

const top = Icon.sweepPose(0);
near(top.y, -2.5, 'cycle starts above center');
near(top.opacity, 0, 'cycle fades in from the top');
const mid = Icon.sweepPose(0.5);
near(mid.y, 0, 'half cycle crosses the center');
near(mid.opacity, 1, 'beam fully visible mid-cycle');
const end = Icon.sweepPose(1);
near(end.y, 2.5, 'cycle ends below center');
near(end.opacity, 0, 'cycle fades out at the bottom');
let prevY = -Infinity;
for (const p of [0, 0.25, 0.5, 0.75, 1]) {
  const pose = Icon.sweepPose(p);
  ok(pose.y >= prevY, 'sweep is monotonically downward');
  ok(pose.opacity >= 0 && pose.opacity <= 1, 'opacity stays in 0..1');
  prevY = pose.y;
}

const restoreStart = Icon.restorePose(0);
near(restoreStart.y, 2.5, 'restore starts at the bottom (where the cycle faded out)');
near(restoreStart.opacity, 0, 'restore starts invisible');
const restoreMid = Icon.restorePose(0.5);
ok(restoreMid.opacity > 0 && restoreMid.opacity < 1 && restoreMid.y > 0 && restoreMid.y < 2.5, 'restore fades in while rising');
const restoreEnd = Icon.restorePose(1);
near(restoreEnd.y, 0, 'restore settles at the resting center');
near(restoreEnd.opacity, 1, 'restore ends fully visible');
ok(Icon.restorePose(0.5).opacity > 0.5, 'restore eases out (fast first half)');

// ---------- 03 样式/装配契约 ----------
const css = fs.readFileSync(path.join(__dirname, 'style.css'), 'utf8');
ok(css.includes('.btn.scan-btn {'), 'square icon button style exists');
match(/\.btn\.scan-btn \{[^}]*width: 28px; height: 28px;/, css, 'scan button is a 28x28 square matched to card button height');
match(/\.scan-btn \.scan-ico \{[^}]*width: 18px; height: 18px;/, css, 'icon is 18px so the button keeps less padding');
ok(css.includes('.scan-btn .scan-ico-beam { stroke: var(--green);'), 'beam uses the EZ brand green');
ok(css.includes('will-change: transform, opacity'), 'beam is promoted for smooth JS-driven transforms');
ok(css.includes('.device-card .btn.scan-btn.pt-hover'), 'rebuilt card restores the scan hover style from JS');
ok(css.includes('.device-card .btn.primary.pt-hover') && css.includes('.device-card .btn.ghost.pt-hover'), 'rebuilt card restores ordinary button hover styles');
ok(!css.includes('@keyframes scan-beam-sweep'), 'CSS animation removed: JS drives the phase so card rebuilds do not restart it');

const appJs = fs.readFileSync(path.join(__dirname, 'app.js'), 'utf8');
ok(appJs.includes('function scanAnimationSync('), 'app.js owns the animation phase state machine');
ok(appJs.includes('SCEZScanIcon.sweepPose(') && appJs.includes('SCEZScanIcon.restorePose('), 'app.js uses the pure phase functions');
ok(appJs.includes('st.finishAt'), 'mouse-leave waits for the current cycle before restoring');
ok(appJs.includes('function markPointerHover('), 'card rebuild restores pointer hover state');
ok(appJs.includes("scanBtn.dataset.role = 'scan'"), 'scan button carries its pointer-tracked role');

const mainGo = fs.readFileSync(path.join(__dirname, '..', 'main_windows.go'), 'utf8');
ok(mainGo.includes('//go:embed web/scan_icon.js'), 'scan_icon.js is embedded into the GUI binary');
const concat = match(/ui\.BuildIndex\([^\n]*\)/, mainGo, 'BuildIndex call present')[0];
ok(concat.includes('scanIconJS'), 'embedded module is concatenated into the page');
ok(concat.indexOf('scanIconJS') < concat.indexOf('appJS'), 'module loads before app.js uses SCEZScanIcon');

console.log('SCAN_ICON_TESTS_OK ' + checks);
