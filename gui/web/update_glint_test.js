'use strict';
// rc23-UI1：下载白光“完整周期”修复回归（结构 + 关键 CSS/JS 约束）。
const fs = require('node:fs');
const path = require('node:path');
const dir = __dirname;
const html = fs.readFileSync(path.join(dir, 'index.html'), 'utf8');
const css = fs.readFileSync(path.join(dir, 'style.css'), 'utf8');
const app = fs.readFileSync(path.join(dir, 'app.js'), 'utf8');
let fails = 0;
function ok(name, cond, detail) { if (cond) { console.log('ok  ' + name); return; } fails++; console.error('FAIL ' + name + ' ' + (detail || '')); }

ok('进度条外包 update-track', /class="update-track"[^>]*id="update-progress-track"/.test(html));
ok('白光独立层 update-glint 存在', /class="update-glint"[^>]*id="update-progress-glint"/.test(html));
ok('原生 progress 仍在（保留圆角端点）', /id="update-progress-bar"/.test(html));
ok('旧版白光（挂在 progress value 上）已移除', !/#update-progress-bar\.is-downloading::-webkit-progress-value/.test(css));
ok('旧版 update-progress-glint 关键帧已移除', !/@keyframes update-progress-glint/.test(css));
ok('白光有独立层样式', /\.update-glint\s*\{/.test(css) && /\.update-glint::before/.test(css));
ok('白光关键帧是完整进出周期（-110% → 220%）', /@keyframes update-progress-chase\s*\{[\s\S]*?-110%[\s\S]*?220%/.test(css));
ok('白光周期为 1.5s linear 无限循环', /animation:\s*update-progress-chase 1\.5s linear infinite/.test(css));
ok('白光更宽（64%）且保持原版 105° 斜向', /width:64%/.test(css) && /linear-gradient\(105deg/.test(css));
ok('下载中才显示白光层', /#update-progress-track\.is-downloading \.update-glint\s*\{\s*opacity:\s*1/.test(css));
ok('白光层宽度与绿色填充共用 .25s 过渡（不会超出绿色区域）', /\.update-glint\s*\{[\s\S]*?transition:\s*width \.25s ease/.test(css));
ok('减少动态效果下关闭白光动画', /prefers-reduced-motion[\s\S]{0,300}\.update-glint::before\s*\{\s*animation:\s*none/.test(css));
ok('JS 提供 renderUpdateGlint', /function renderUpdateGlint\(m\)/.test(app));
ok('JS 在 renderUpdate 中调用', /renderUpdateGlint\(m\);/.test(app));
ok('JS 按百分比设置白光层宽度', /glint\.style\.width = pct\.toFixed\(2\) \+ '%'/.test(app));
ok('JS 切换 track 的 downloading 状态', /track\.classList\.toggle\('is-downloading'/.test(app));
ok('不确定进度时不显示白光', /track\.classList\.toggle\('is-downloading', pct !== null && !!m\.animateProgress\)/.test(app));

if (fails) { console.error(`update_glint: ${fails} failed`); process.exit(1); }
console.log('update_glint: structure, animation cycle and clipping rules passed');
