// Application window ordering and outline geometry, shared by both views.
(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.SCEZAppLauncher = factory();
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  function options(catalog, profile) {
    profile = profile || {};
    var installed = new Set(catalog.map(function (app) { return app.pkg; }).filter(Boolean));
    var maximum = installed.size;
    var capacity = Number.isInteger(profile.capacity) ? profile.capacity : 4;
    function available(packages) {
      return Array.from(new Set((packages || []).filter(function (pkg) { return installed.has(pkg); })));
    }
    return {
      capacity: Math.max(0, Math.min(capacity, maximum)), maximum: maximum,
      custom: !!profile.custom || (Number.isInteger(profile.capacity) && profile.capacity !== 4 && profile.capacity !== 8),
      pins: available(profile.pins), recent: available(profile.recent)
    };
  }

  // Search first produces matches in catalog order using the common matcher.
  // Slot ownership is independent of search so the shelf doesn't pick up an
  // older open solely because a newer one has been filtered out.
  function arrange(catalog, matches, profile, draft) {
    var current = options(catalog, draft || profile);
    var pinKeys = current.pins.slice(0, current.capacity);
    var matchByKey = new Map(matches.map(function (app) { return [app.pkg, app]; }));
    var catalogByKey = draft ? new Map(catalog.map(function (app) { return [app.pkg, app]; })) : matchByKey;
    var recent = draft ? [] : current.recent.filter(function (pkg) { return !pinKeys.includes(pkg); })
      .slice(0, current.capacity - pinKeys.length).map(function (pkg) { return matchByKey.get(pkg); }).filter(Boolean);
    var pins = pinKeys.map(function (pkg) { return catalogByKey.get(pkg); }).filter(Boolean);
    var entries = recent.map(function (app) { return {app: app, key: app.pkg, zone: 'recent'}; });
    entries.push.apply(entries, pins.map(function (app) {
      return {app: app, key: draft ? 'pin:' + app.pkg : app.pkg, zone: 'pin'};
    }));
    if (draft) {
      for (var i = pins.length; i < current.capacity; ++i) entries.push({key: 'slot:' + i, zone: 'slot'});
    }
    var promoted = new Set(recent.concat(pins).map(function (app) { return app.pkg; }));
    var remaining = matches.filter(function (app) { return draft || !promoted.has(app.pkg); });
    entries.push.apply(entries, remaining.map(function (app) { return {app: app, key: app.pkg, zone: 'catalog'}; }));
    return {entries: entries, recents: recent.length, pins: draft ? current.capacity : pins.length, matches: remaining.length};
  }

  // A text input may temporarily contain invalid input. Excess integers are
  // immediately capped, including values larger than JavaScript can represent.
  function parseCapacity(text, maximum) {
    text = String(text).trim();
    return /^[0-9]+$/.test(text) ? Math.min(Number(text), maximum) : null;
  }

  function roundedPath(points, radius) {
    const near = (point, adjacent) => {
      const dx = adjacent[0] - point[0], dy = adjacent[1] - point[1];
      const distance = Math.hypot(dx, dy);
      const t = distance ? Math.min(radius / distance, .44) : 0;
      return [point[0] + dx * t, point[1] + dy * t].map(number => Math.round(number * 10) / 10).join(' ');
    };
    return points.map((p, i) => {
      const previous = points[(i + points.length - 1) % points.length];
      const next = points[(i + 1) % points.length];
      return (i ? 'L ' : 'M ') + near(p, previous) + ' Q ' + p.join(' ') + ' ' + near(p, next);
    }).join(' ') + ' Z';
  }
  function outlinePath(start, count, columns, columnWidth, rowHeight, columnGap, rowGap) {
    const end = start + count;
    const islands = [];
    let rows;
    for (let index = start; index < end;) {
      const row = Math.floor(index / columns);
      const from = index % columns;
      const to = Math.min(columns, from + end - index);
      const previous = rows?.[rows.length - 1];
      if (!previous || Math.max(previous.from, from) >= Math.min(previous.to, to)) {
        rows = [];
        islands.push(rows);
      }
      rows.push({row, from, to});
      index += to - from;
    }
    const pitch = columnWidth + columnGap;
    const stride = rowHeight + rowGap;
    const edge = 1;
    const left = row => row.from * pitch + edge;
    const right = row => row.to * pitch - columnGap - edge;
    const seam = row => row.row * stride - rowGap / 2;
    const inward = (a, b) => [Math.sign(a[1] - b[1]), Math.sign(b[0] - a[0])];
    const width = 1.4;
    return islands.map(rows => {
      const first = rows[0], last = rows[rows.length - 1];
      const top = first.row * stride + edge;
      const bottom = (last.row + 1) * stride - rowGap - edge;
      const points = [[left(first), top], [right(first), top]];
      for (let i = 1; i < rows.length; i++) {
        if (rows[i - 1].to !== rows[i].to) points.push([right(rows[i - 1]), seam(rows[i])], [right(rows[i]), seam(rows[i])]);
      }
      points.push([right(last), bottom], [left(last), bottom]);
      for (let i = rows.length - 1; i > 0; i--) {
        if (rows[i - 1].from !== rows[i].from) points.push([left(rows[i]), seam(rows[i])], [left(rows[i - 1]), seam(rows[i])]);
      }
      const inner = points.map((point, i) => {
        const entry = inward(points[(i + points.length - 1) % points.length], point);
        const exit = inward(point, points[(i + 1) % points.length]);
        return [point[0] + (entry[0] + exit[0]) * width, point[1] + (entry[1] + exit[1]) * width];
      });
      return roundedPath(points, 10) + ' ' + roundedPath(inner, 10 - width);
    }).join(' ');
  }

  return {options: options, arrange: arrange, parseCapacity: parseCapacity, outlinePath: outlinePath};
});
