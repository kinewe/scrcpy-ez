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
const context = {
  el: node,
  appWinModalSerial: null, notificationBatchTargets: [], notificationSaving: false,
  lastState: { settings: { showParamOverlay: false, closeToTray: true } },
  SetSettings: (...args) => { saves.push(args); return Promise.resolve(); },
  SetOtherAppWinSystemDecorations: value => { compatSaves.push(value); return Promise.resolve(); },
  SetKeepDeviceAwake: value => { awakeSaves.push(value); return Promise.resolve(); },
  toast: message => { throw new Error(message); }
};
vm.createContext(context);
vm.runInContext(source.slice(start, end), context);
node('btn-settings').handlers.click();
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
