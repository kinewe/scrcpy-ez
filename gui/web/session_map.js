// 会话映射纯函数（多会话隔离核心——浏览器/Node 双端可用）：
//   活动会话表构建（键=会话 serial，与后端 Snapshot.Sessions 一致）；
//   设备卡 → 会话唯一绑定（serial 直配 → 无线地址（卡片重键）→ 档案 identity 兜底）。
// 不变量（后端保证，映射据此唯一）：
//   - 同一设备（identity）同时最多一个活动会话（StartCast 同 identity 拒绝）；
//   - 会话键与设备卡 serial 不一致时（同身份双键：会话键 197:5555、卡 serial
//     601c9f08）经 d.wireless / d.identity 兜底仍命中唯一会话。
// 纯函数：无 DOM、无闭包副作用——node 单测覆盖（session_map_test.js）。
(function (root, factory) {
  if (typeof module === 'object' && module.exports) {
    module.exports = factory();
  } else {
    root.SessionMap = factory();
  }
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  // activeSessionMap：活动会话表（只收 active；重启闩锁期后端 Session.Active 仍为 true）
  function activeSessionMap(sessions) {
    if (!Array.isArray(sessions)) return {}; // 防御：非数组入参（接线错误）返回空表
    var out = {};
    (sessions || []).forEach(function (s) {
      if (s.active && s.serial) out[s.serial] = s;
    });
    return out;
  }

  // matchSession：设备卡 → 会话（三层兜底，命中唯一）：
  //   ① serial 直配；② wireless（卡片重键：卡主 serial 换成 USB、会话键还是旧无线地址）；
  //   ③ identity（档案市场名——identity 为空时跳过，绝不空串误配）。
  // 返回会话对象或 null。
  function matchSession(d, castMap) {
    if (!d || !castMap) return null;
    if (d.serial && castMap[d.serial]) return castMap[d.serial];
    if (d.wireless && castMap[d.wireless]) return castMap[d.wireless];
    if (d.identity) {
      var keys = Object.keys(castMap);
      for (var i = 0; i < keys.length; i++) {
        var s = castMap[keys[i]];
        if (s && s.identity && s.identity === d.identity) return s;
      }
    }
    return null;
  }

  return {
    activeSessionMap: activeSessionMap,
    matchSession: matchSession
  };
});
