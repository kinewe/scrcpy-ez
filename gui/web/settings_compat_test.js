'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

// Exercise the real settings handlers against backend snapshots, including an
// old snapshot without the new key. Keep save calls independent so changing
// either original checkbox cannot reset the compatibility choice.
const source = fs.readFileSync(path.join(__dirname, 'app.js'), 'utf8');
const start = source.indexOf('  var settingsState = ');
const end = source.indexOf('  var lastProfileSaveError = ', start);
assert(start >= 0 && end > start);
const nodes = new Map();
function node(id) {
  if (!nodes.has(id)) {
    const attrs = {};
    const handlers = {};
    const classes = new Set();
    nodes.set(id, {
      style: { display: 'none' }, attrs, handlers,
      children: [], childNodes: [],
      querySelectorAll: () => [],
      classList: { add: x => classes.add(x), remove: x => classes.delete(x) },
      setAttribute: (key, value) => { attrs[key] = value; },
      addEventListener: (name, handler) => { handlers[name] = handler; }
    });
  }
  return nodes.get(id);
}
const saves = [];
const compatSaves = [];
const awakeSaves = [];
const autoBlackoutSaves = [];
const smoothingSaves = [];
const smoothingFrameSaves = [];
const context = {
  el: node,
  appWinModalSerial: null, notificationBatchTargets: [], notificationSaving: false,
  lastState: { settings: { showParamOverlay: false, closeToTray: true } },
  SetSettings: (...args) => { saves.push(args); return Promise.resolve(); },
  SetOtherAppWinSystemDecorations: value => { compatSaves.push(value); return Promise.resolve(); },
  SetKeepDeviceAwake: value => { awakeSaves.push(value); return Promise.resolve(); },
  SetAutoBlackoutMainCast: value => { autoBlackoutSaves.push(value); return Promise.resolve(); },
  SetVideoSmoothing: value => { smoothingSaves.push(value); return Promise.resolve(); },
  SetVideoSmoothingFrames: value => { smoothingFrameSaves.push(value); return Promise.resolve(); },
  toast: message => { throw new Error(message); }
};
vm.createContext(context);
vm.runInContext(source.slice(start, end), context);
node('btn-settings').handlers.click();
assert.equal(node('set-smoothing').attrs['aria-checked'], 'false');
assert.equal(node('set-keep-awake').attrs['aria-checked'], 'true');
node('set-keep-awake').handlers.click();
assert.deepEqual(awakeSaves, [false]);
assert.equal(node('set-keep-awake').attrs['aria-checked'], 'false');
assert.equal(saves.length, 0);
assert.equal(node('set-appwin-decor').attrs['aria-checked'], 'true');
node('set-appwin-decor').handlers.click();
assert.deepEqual(compatSaves, [false]);
assert.equal(saves.length, 0);
assert.equal(node('set-appwin-decor').attrs['aria-checked'], 'false');
node('set-overlay').handlers.click();
assert.deepEqual(saves, [[true, true]]);
assert.equal(node('set-appwin-decor').attrs['aria-checked'], 'false');
context.lastState = { settings: { showParamOverlay: true, closeToTray: true, keepDeviceAwake: true, otherAppWinSystemDecorations: false } };
node('btn-settings').handlers.click();
assert.equal(node('set-keep-awake').attrs['aria-checked'], 'true');
node('set-keep-awake').handlers.click();
assert.deepEqual(awakeSaves, [false, false]);
assert.equal(node('set-appwin-decor').attrs['aria-checked'], 'false');
node('set-appwin-decor').handlers.click();
assert.deepEqual(compatSaves, [false, true]);
assert.equal(saves.length, 1);
console.log('settings compatibility: legacy snapshots, save bindings and independent original settings passed');

(async () => {
  assert.equal(node('set-auto-blackout').attrs['aria-checked'], 'true');
  node('set-auto-blackout').handlers.click();
  node('set-auto-blackout').handlers.click();
  assert.deepEqual(autoBlackoutSaves, [false]);
  assert.equal(node('set-auto-blackout').attrs['aria-checked'], 'false');
  context.syncSettings({ settings: { keepDeviceAwake: true, autoBlackoutMainCast: true } });
  assert.equal(node('set-keep-awake').attrs['aria-checked'], 'true');
  assert.equal(node('set-auto-blackout').attrs['aria-checked'], 'false');
  await Promise.resolve(); await Promise.resolve();
  context.syncSettings({ settings: { keepDeviceAwake: false, autoBlackoutMainCast: false } });
  assert.equal(node('set-keep-awake').attrs['aria-checked'], 'false');
  assert.equal(node('set-auto-blackout').attrs['aria-checked'], 'false');
  node('set-smoothing').handlers.click();
  node('set-smoothing').handlers.click(); // Do not enqueue a second save while pending.
  assert.deepEqual(smoothingSaves, [true]);
  context.syncSettings({ settings: { videoSmoothing: false } });
  assert.equal(context.settingsState.videoSmoothing, true);
  await Promise.resolve();
  assert.equal(node('set-smoothing-two').attrs['aria-pressed'], 'true');
  assert.equal(node('set-smoothing-one').disabled, false);
  node('set-smoothing-one').handlers.click();
  node('set-smoothing-two').handlers.click();
  context.syncSettings({ settings: { videoSmoothing: false, videoSmoothingFrames: 2 } });
  assert.equal(context.settingsState.videoSmoothingFrames, 1);
  assert.deepEqual(smoothingFrameSaves, [1]);
  await Promise.resolve();
  context.SetVideoSmoothingFrames = () => Promise.reject(new Error('budget failure'));
  const budgetErrors = [];
  context.toast = value => budgetErrors.push(value);
  node('set-smoothing-two').handlers.click();
  await Promise.resolve(); await Promise.resolve();
  assert.equal(context.settingsState.videoSmoothingFrames, 1);
  assert.equal(budgetErrors.length, 1);
  context.SetVideoSmoothing = () => Promise.reject(new Error('test failure'));
  const errors = [];
  context.toast = value => errors.push(value);
  node('set-smoothing').handlers.keydown({key: 'Enter', preventDefault() {}});
  await Promise.resolve(); await Promise.resolve();
  assert.equal(node('set-smoothing').attrs['aria-checked'], 'true');
  assert.equal(errors.length, 1);
  context.syncSettings({ settings: {} });
  assert.equal(context.settingsState.videoSmoothing, false);
  assert.equal(context.settingsState.videoSmoothingFrames, 2);
  assert.equal(node('set-smoothing-one').disabled, true);
  console.log('smoothing: legacy default, independent save, pending snapshot and failed-save rollback passed');
})().catch(error => { console.error(error); process.exitCode = 1; });
