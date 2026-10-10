// 纯展示模型：按钮与进度由后台阶段决定，避免轮询覆盖用户确认。
(function (root) {
  'use strict';
  function bytes(n) { return (Math.max(0, n || 0) / 1048576).toFixed(1) + ' MB'; }
  function model(s) {
    s = s || {}; var p = s.phase || 'idle', i = s.info || {};
    var busy = ['checking', 'selecting', 'downloading', 'validating', 'installing'].indexOf(p) >= 0;
    return {
      busy: busy,
      download: !!i.hasNew && !i.packageMissing && !s.canInstall && !busy,
      downloadText: ['error', 'canceled'].indexOf(p) >= 0 ? '继续下载' : '下载更新',
      install: !!s.canInstall && !busy,
      cancel: p === 'selecting' || p === 'downloading' || p === 'validating',
      progress: p === 'selecting' || p === 'downloading' || p === 'validating',
      percent: p !== 'selecting' && s.total > 0 ? Math.min(100, Math.max(0, s.downloaded / s.total * 100)) : null,
	  animateProgress: p === 'downloading' && s.total > 0 && s.downloaded > 0 && s.downloaded < s.total,
      detail: bytes(s.downloaded) + (s.total > 0 ? ' / ' + bytes(s.total) : '') + (s.speed > 0 && p === 'downloading' ? ' · ' + bytes(s.speed) + '/s' : ''),
      message: s.message || '点击检查更新', error: s.error || '',
      source: s.source || '自动选择（GitHub / Gitee）', notice: i.notice || '',
      check: !busy && !s.canInstall, later: p !== 'installing'
    };
  }
  var api = {model:model};
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  else root.SCEZUpdateUI = api;
})(typeof window !== 'undefined' ? window : globalThis);
