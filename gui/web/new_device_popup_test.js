'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname, 'app.js'), 'utf8');
const nodes = {};
const scope = { popupSerial: null, el: id => nodes[id] || (nodes[id] = { style: {}, textContent: '' }) };
vm.createContext(scope);
const match = source.match(/  function renderNewDevice\([\s\S]*?\n  }/);
assert.ok(match, 'popup renderer');
vm.runInContext(match[0], scope);

for (const connType of ['usb', 'wifi']) {
  const serial = connType === 'usb' ? 'PHONE' : '192.0.2.172:5555';
  const render = name => scope.renderNewDevice({ newDevice: { serial, name, connType } });
  render('');
  assert.equal(nodes['newdev-name'].textContent, serial);
  render('Xiaomi Pad 8 Pro');
  assert.equal(nodes['newdev-name'].textContent, 'Xiaomi Pad 8 Pro', 'late market name updates the same popup');
  render('我的平板');
  assert.equal(nodes['newdev-name'].textContent, '我的平板', 'custom name updates without reconnecting');
  assert.equal(scope.popupSerial, serial, 'display name must not change the action target');
  assert.equal(nodes['newdev-tag'].textContent, connType === 'usb' ? '（USB）' : '（无线）');
  scope.renderNewDevice({ newDevice: null });
  assert.equal(nodes['newdev-popup'].style.display, 'none');
  assert.equal(scope.popupSerial, null);
}
console.log('new_device_popup: wired and wireless names refresh after enrichment and rename');
