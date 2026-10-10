'use strict';
const assert = require('assert'), fs = require('fs'), vm = require('vm');
const ctx = vm.createContext({Promise});
vm.runInContext(fs.readFileSync(__dirname + '/app_launch_ui.js', 'utf8'), ctx);
function windowItem(id, serial = 'A', extra = {}) {
  return Object.assign({serial, pkg: 'app.test', name: 'Test', launchFailureID: id, phase: 'app-layout-incompatible'}, extra);
}
function fixture(resolve) {
  const events = [], calls = [];
  const ui = ctx.SCEZAppLaunchUI.create({
    present: (w, busy, error) => events.push({id: w.launchFailureID, busy, error}),
    reveal: () => events.push('reveal'), hide: () => events.push('hide'),
    resolve: (...args) => { calls.push(args); return resolve ? resolve(...args) : Promise.resolve(); }
  });
  return {ui, events, calls};
}
(async function () {
  const f = fixture();
  f.ui.sync([windowItem(1)]);
  f.ui.sync([windowItem(1, 'A', {identity: 'learned-phone'})]);
  f.ui.sync([windowItem(1)]);
  assert.strictEqual(f.events.filter(e => e === 'reveal').length, 1, 'polling and identity learning must not repeat the dialog');
  await f.ui.choose(false);
  assert.deepStrictEqual(f.calls, [['A', 'app.test', 1, false]], 'cancel closes only this failed cast');
  f.ui.sync([windowItem(1)]);
  assert.strictEqual(f.events.filter(e => e === 'reveal').length, 1, 'old snapshots must not reopen dismissed notices');
  f.ui.sync([windowItem(2), windowItem(3, 'B')]);
  await f.ui.choose(true);
  assert.deepStrictEqual(f.calls[1], ['A', 'app.test', 2, true], 'restart requires explicit choice and matching attempt');
  await f.ui.choose(false);
  assert.deepStrictEqual(f.calls[2], ['B', 'app.test', 3, false], 'other devices remain queued');

  const removed = fixture();
  removed.ui.sync([windowItem(1)]); removed.ui.sync([]);
  await removed.ui.choose(true);
  assert.strictEqual(removed.calls.length, 0, 'stale dialog must not restart a removed window');
  removed.ui.sync([windowItem(2, 'A', {notificationWindow: true}), windowItem(3, 'B', {closing: true}), windowItem(4, 'C', {phase: 'restarting'})]);
  assert.strictEqual(removed.events.filter(e => e === 'reveal').length, 1, 'notifications and closing/restarting casts never enter the fallback');

  let release;
  const racing = fixture(() => new Promise(r => {release = r;}));
  racing.ui.sync([windowItem(1)]);
  const action = racing.ui.choose(true);
  await racing.ui.choose(true);
  racing.ui.sync([windowItem(2)]);
  assert.strictEqual(racing.calls.length, 1, 'double click cannot duplicate restart');
  release(); await action;
  assert.strictEqual(racing.events.filter(e => e === 'reveal').length, 2, 'new failure can appear after prior action completes');

  let fail = true;
  const retry = fixture(() => fail ? Promise.reject(new Error('test failure')) : Promise.resolve());
  retry.ui.sync([windowItem(1)]); await retry.ui.choose(true);
  assert.match(retry.events[retry.events.length - 1].error, /test failure/);
  fail = false; await retry.ui.choose(false);
  assert.strictEqual(retry.calls.length, 2, 'RPC failure must leave cancel/retry available');
  const ordinary = fixture();
  ordinary.ui.sync([windowItem(1, 'A', {pkg: 'app.other', phase: 'app-in-use'})]);
  assert.strictEqual(ordinary.events.filter(e => e === 'reveal').length, 1, 'foreground conflict for any app uses the same dialog');
  await ordinary.ui.choose(false);
  assert.deepStrictEqual(ordinary.calls, [['A', 'app.other', 1, false]]);
  const app = fs.readFileSync(__dirname + '/app.js', 'utf8');
  assert(!app.includes('appendAppRestartButton'), 'cards no longer contain a restart button');
  assert(app.includes('appLaunchDialog.sync(st.appWins || [])'), 'live snapshots feed the dialog');
  console.log('launch dialog deduplication, cancellation, explicit restart, queueing and stale state checks passed');
})().catch(error => {console.error(error); process.exit(1);});
