(function (root) {
  'use strict';
  function eligible(w) {
    return w && w.launchFailureID > 0 && !w.notificationWindow && !w.closing &&
      /^app-(layout-incompatible|reuse-failed|launch-failed|in-use)$/.test(w.phase);
  }
  function key(w) { return w.serial + '\n' + w.pkg; }
  function create(options) {
    var seen = {}, latest = {}, pending = [], current = null, busy = false;
    function advance() {
      if (current || busy) return;
      while (pending.length) {
        var w = pending.shift();
        var fresh = latest[key(w)];
        if (!fresh || fresh.launchFailureID !== w.launchFailureID) continue;
        current = fresh;
        options.present(current, false, '');
        options.reveal();
        break;
      }
    }
    function finish() { current = null; busy = false; options.hide(); advance(); }
    return {
      sync: function (list) {
        latest = {};
        (list || []).forEach(function (w) {
          if (!eligible(w)) return;
          var k = key(w);
          latest[k] = w;
          if (w.launchFailureID > (seen[k] || 0)) {
            seen[k] = w.launchFailureID;
            pending.push(w);
          }
        });
        if (current && !busy) {
          var fresh = latest[key(current)];
          if (!fresh || fresh.launchFailureID !== current.launchFailureID) finish();
        }
        advance();
      },
      choose: function (restart) {
        if (!current || busy) return Promise.resolve();
        busy = true;
        var selected = current;
        options.present(selected, true, '');
        return Promise.resolve().then(function () {
          return options.resolve(selected.serial, selected.pkg, selected.launchFailureID, !!restart);
        }).then(finish, function (error) {
          busy = false;
          var fresh = latest[key(selected)];
          if (!fresh || fresh.launchFailureID !== selected.launchFailureID) { finish(); return; }
          options.present(selected, false, '操作失败：' + (error && error.message ? error.message : error));
        });
      }
    };
  }
  root.SCEZAppLaunchUI = {create: create};
})(typeof window !== 'undefined' ? window : globalThis);
