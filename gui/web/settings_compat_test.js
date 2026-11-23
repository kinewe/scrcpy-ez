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
      getAttribute: key => attrs[key],
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
const revealed = [];
const launchAtLoginSaves = [];
let launchAtLogin = false;
const context = {
  el: node,
  document: { activeElement: null },
  revealSettingsDisclosure: panel => { if (!panel.inert) revealed.push(panel); },
  appWinModalSerial: null, notificationBatchTargets: [], notificationSaving: false,
  lastState: { settings: { showParamOverlay: false, closeToTray: true } },
  SetSettings: (...args) => { saves.push(args); return Promise.resolve(); },
  GetLaunchAtLogin: () => Promise.resolve(launchAtLogin),
  SetLaunchAtLogin: enabled => {
    launchAtLoginSaves.push(enabled);
    launchAtLogin = enabled;
    return Promise.resolve(enabled);
  },
  SetOtherAppWinSystemDecorations: value => { compatSaves.push(value); return Promise.resolve(); },
  SetKeepDeviceAwake: value => { awakeSaves.push(value); return Promise.resolve(); },
  SetAutoBlackoutMainCast: value => { autoBlackoutSaves.push(value); return Promise.resolve(); },
  SetVideoSmoothing: value => { smoothingSaves.push(value); return Promise.resolve(); },
  SetVideoSmoothingFrames: value => { smoothingFrameSaves.push(value); return Promise.resolve(); },
  toast: message => { throw new Error(message); }
};
vm.createContext(context);
vm.runInContext(source.match(/  function setSettingsDisclosure\([\s\S]+?\n  }/)[0], context);
vm.runInContext(source.slice(start, end), context);
node('btn-settings').handlers.click();
assert.equal(node('set-smoothing').attrs['aria-checked'], 'false');
assert.equal(node('set-smoothing-details').inert, true);
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
  const launchSwitch = node('set-launch-at-login');
  const settle = () => new Promise(resolve => setImmediate(resolve));
  await settle();
  assert.equal(launchSwitch.attrs['aria-checked'], 'false');
  assert.deepEqual(launchAtLoginSaves, [], 'opening settings must never enroll EZ in startup');
  launchSwitch.handlers.click();
  launchSwitch.handlers.click();
  await settle();
  assert.deepEqual(launchAtLoginSaves, [true]);
  assert.equal(launchSwitch.attrs['aria-checked'], 'true');
  launchAtLogin = false;
  node('settings-close').handlers.click();
  node('btn-settings').handlers.click();
  await settle();
  assert.equal(launchSwitch.attrs['aria-checked'], 'false', 'reopening settings reads the Windows entry again');
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
  assert.equal(node('set-smoothing-details').inert, false);
  assert.deepEqual(revealed, [node('set-smoothing-details')]);
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
  assert.equal(node('set-smoothing-details').inert, false);
  assert.equal(revealed.length, 1, 'a failed close does not request a reveal on rollback');
  assert.equal(errors.length, 1);
  context.syncSettings({ settings: {} });
  assert.equal(context.settingsState.videoSmoothing, false);
  assert.equal(context.settingsState.videoSmoothingFrames, 2);
  assert.equal(node('set-smoothing-one').disabled, true);
  assert.equal(node('set-smoothing-details').inert, true);
  console.log('smoothing: legacy default, independent save, pending snapshot and failed-save rollback passed');
})().catch(error => { console.error(error); process.exitCode = 1; });
