// Lucide SVGs from https://github.com/lucide-icons/lucide, ISC license in web/lucide/LICENSE.
// Same icon family as the approved UI concept; bundled locally without a CDN.
(function(root) {
  var paths = {"bell": "<path d=\"M10.268 21a2 2 0 0 0 3.464 0\" />\n  <path d=\"M3.262 15.326A1 1 0 0 0 4 17h16a1 1 0 0 0 .74-1.673C19.41 13.956 18 12.499 18 8A6 6 0 0 0 6 8c0 4.499-1.411 5.956-2.738 7.326\" />\n", "bell-off": "<path d=\"M10.268 21a2 2 0 0 0 3.464 0\" />\n  <path d=\"M17 17H4a1 1 0 0 1-.74-1.673C4.59 13.956 6 12.499 6 8a6 6 0 0 1 .258-1.742\" />\n  <path d=\"m2 2 20 20\" />\n  <path d=\"M8.668 3.01A6 6 0 0 1 18 8c0 2.687.77 4.653 1.707 6.05\" />\n", "key-round": "<path d=\"M2.586 17.414A2 2 0 0 0 2 18.828V21a1 1 0 0 0 1 1h3a1 1 0 0 0 1-1v-1a1 1 0 0 1 1-1h1a1 1 0 0 0 1-1v-1a1 1 0 0 1 1-1h.172a2 2 0 0 0 1.414-.586l.814-.814a6.5 6.5 0 1 0-4-4z\" />\n  <circle cx=\"16.5\" cy=\"7.5\" r=\".5\" fill=\"currentColor\" />\n", "moon-star": "<path d=\"M18 5h4\" />\n  <path d=\"M20 3v4\" />\n  <path d=\"M20.985 12.486a9 9 0 1 1-9.473-9.472c.405-.022.617.46.402.803a6 6 0 0 0 8.268 8.268c.344-.215.825-.004.803.401\" />\n", "sliders-horizontal": "<path d=\"M10 5H3\" />\n  <path d=\"M12 19H3\" />\n  <path d=\"M14 3v4\" />\n  <path d=\"M16 17v4\" />\n  <path d=\"M21 12h-9\" />\n  <path d=\"M21 19h-5\" />\n  <path d=\"M21 5h-7\" />\n  <path d=\"M8 10v4\" />\n  <path d=\"M8 12H3\" />\n", "file-text": "<path d=\"M6 22a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h8a2.4 2.4 0 0 1 1.704.706l3.588 3.588A2.4 2.4 0 0 1 20 8v12a2 2 0 0 1-2 2z\" />\n  <path d=\"M14 2v5a1 1 0 0 0 1 1h5\" />\n  <path d=\"M10 9H8\" />\n  <path d=\"M16 13H8\" />\n  <path d=\"M16 17H8\" />\n", "message-square": "<path d=\"M22 17a2 2 0 0 1-2 2H6.828a2 2 0 0 0-1.414.586l-2.202 2.202A.71.71 0 0 1 2 21.286V5a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2z\" />\n", "lock-keyhole": "<circle cx=\"12\" cy=\"16\" r=\"1\" />\n  <rect x=\"3\" y=\"10\" width=\"18\" height=\"12\" rx=\"2\" />\n  <path d=\"M7 10V7a5 5 0 0 1 10 0v3\" />\n", "clock-3": "<circle cx=\"12\" cy=\"12\" r=\"10\" />\n  <path d=\"M12 6v6h4\" />\n", "info": "<circle cx=\"12\" cy=\"12\" r=\"10\" />\n  <path d=\"M12 16v-4\" />\n  <path d=\"M12 8h.01\" />\n", "chevron-down": "<path d=\"m6 9 6 6 6-6\" />\n", "x": "<path d=\"M18 6 6 18\" />\n  <path d=\"m6 6 12 12\" />\n", "check": "<path d=\"M20 6 9 17l-5-5\" />\n"};
  function svg(name, className) {
    if (!paths[name]) throw new Error('Unknown notification icon: '+name);
    return '<svg class="'+(className || 'notification-symbol')+'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">'+paths[name]+'</svg>';
  }
  function paint(document) {
    document.querySelectorAll('[data-notification-icon]').forEach(function(node) {
      node.innerHTML=paths[node.getAttribute('data-notification-icon')];
    });
  }
  var api={svg:svg,paint:paint};
  if(typeof module==='object' && module.exports) module.exports=api;
  root.SCEZNotificationIcons=api;
  if(typeof document!=='undefined') paint(document);
})(typeof window!=='undefined' ? window : globalThis);
