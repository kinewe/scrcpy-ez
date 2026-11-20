'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const launcher = require('./app_launcher');
const source = fs.readFileSync(__dirname + '/app.js','utf8');

const apps = ['files','manager','browser','camera','notes','gallery','music','settings','market'].map(
  pkg => ({name:pkg,pkg:'com.example.'+pkg}));
const [files,manager,browser,camera,notes,gallery,music,settings,market] = apps.map(app => app.pkg);
const profile = {key:'device:A', appLauncher:{capacity:4, pins:[market,settings], recent:[notes,browser,files]}};
const ordered = (candidate,draft,search=apps) => launcher.arrange(apps,search,candidate,draft).entries.map(e=>e.key);
assert.deepEqual(ordered(profile.appLauncher).slice(0,4), [notes,browser,market,settings]);
const replaced = ordered({...profile.appLauncher,recent:[gallery,notes,browser,files]});
assert.deepEqual(replaced.slice(0,4), [gallery,notes,market,settings]);
assert.deepEqual(replaced.slice(4), [files,manager,browser,camera,music]);
assert.deepEqual(ordered({capacity:0,pins:[market],recent:[gallery]}), apps.map(app=>app.pkg));
const one = launcher.outlinePath(0,1,4,90,96,6,8);
assert.equal((one.match(/M /g)||[]).length,2,'one cell uses an outer and inner contour');
assert.match(one,/Q 89 1/);
const connected = launcher.outlinePath(2,6,4,90,96,6,8);
assert.match(connected,/Q 1 100/);
const separated = launcher.outlinePath(3,2,4,90,96,6,8);
assert.equal((separated.match(/M /g)||[]).length,4,'disconnected regions get two separate outlines');

let waveClock = 200;
const waveScope = vm.createContext({
  appLauncherDraft:{waveStart:0}, appLauncherColumns:()=>4, performance:{now:()=>waveClock}
});
const waveStart = source.indexOf('  function updateAppLauncherWave(');
vm.runInContext(source.slice(waveStart,source.indexOf('\n  }',waveStart)+4),waveScope);
const waveTiles = [4,5,8].map(index=>({
  dataset:{launcherIndex:String(index)}, style:{setProperty(key,value){this[key]=value;}}
}));
const waveGrid = {launcherArrangement:{matches:167,pins:4},querySelectorAll:()=>waveTiles};
waveScope.updateAppLauncherWave(waveGrid);
assert.deepEqual(waveTiles.map(tile=>tile.style['--launcher-wave-period']),['1500ms','1500ms','1500ms']);
assert.deepEqual(waveTiles.map(tile=>tile.style['--launcher-wave-delay']),['-200ms','-130ms','-130ms']);
waveClock = 500;
waveTiles[2].dataset.launcherIndex = '6';
waveScope.updateAppLauncherWave(waveGrid);
assert.deepEqual(waveTiles.map(tile=>tile.style['--launcher-wave-delay']),['-200ms','-130ms','-60ms']);

const nodes = new Map(), saved = [], errors = [];
function el(id) {
  if (!nodes.has(id)) {
    const flags=new Set();
    nodes.set(id,{events:{},attrs:{},hidden:true,value:'',scrollTop:0,
      setAttribute(k,v){this.attrs[k]=v;},addEventListener(k,f){this.events[k]=f;},
      style:{setProperty(k,v){this[k]=v;}},
      classList:{add:n=>flags.add(n),remove:n=>flags.delete(n),toggle(n,on){if(on)flags.add(n);else flags.delete(n);}},
      getBoundingClientRect(){return {top:32,height:360};},
      closest(s){return el(s);},focus(){this.focused=true;},select(){this.selected=true;}});
  }
  return nodes.get(id);
}
const scope = {
  el, performance, SCEZAppLauncher: launcher, nextStateRequest:3, appliedStateRequest:1,
  appWins:{A:{loaded:true,identity:'device:A',apps}},
  appWinModalSerial:'A', appWinNotificationView:false, appLauncherDraft:null, appLauncherDrag:null,
  document:{elementFromPoint:()=>pointerTarget},
  lastState:{profiles:[profile]}, deviceIdentityOf:()=>'', refreshNow(){}, toast:e=>errors.push(e),
  renderAppWinGrid(serial) { scope.renderAppLauncherControls(serial); },
  window:{SaveAppLauncherOptions(identity,capacity,custom,pins){
    saved.push({identity,capacity,custom,pins:Array.from(pins)});
    return Promise.resolve({capacity,custom,pins:Array.from(pins),recent:[gallery,notes,browser]});
  }}
};
let pointerTarget;
vm.createContext(scope);
const start=source.indexOf('  function appLauncherProfileItem('), end=source.indexOf('  function appLauncherColumns(',start);
vm.runInContext(source.slice(start,end),scope);
scope.bindAppLauncher();
const grid=el('appwin-modal-grid');
grid.setPointerCapture=id=>{grid.capture=id;};
grid.hasPointerCapture=id=>grid.capture===id;
grid.releasePointerCapture=()=>{grid.capture=null;};
grid.contains=tile=>tile===pointerTarget;
function pinnedTile(pkg) {
  const item=el('tile-'+pkg);
  item.dataset={launcherPkg:pkg,launcherKey:'pin:'+pkg,launcherZone:'pin'};
  item.closest=selector=>selector==='.appwin-unpin'?null:item;
  return item;
}
function pointer(tile,x,y=0) {return {target:tile,button:0,pointerId:10,clientX:x,clientY:y};}
const toggle=()=>el('appwin-launcher-options').events.click();
const input=el('appwin-launcher-number'), search=el('appwin-modal-search');
const type=value=>{input.value=value;input.events.input();};
const flush=()=>new Promise(resolve=>setImmediate(resolve));

(async()=>{
  toggle();assert.equal(el('appwin-launcher-panel').hidden,false);
  assert.equal(el('appwin-modal-hint').textContent,'已置顶 2 / 4 · 若还有空位，将留给「最近打开应用」');
  scope.toggleAppLauncherPin(camera,false);
  assert.deepEqual(profile.appLauncher.pins,[market,settings],'edits wait for panel close');
  assert.equal(saved.length,0);
  el('appwin-launcher-custom').events.click();assert.equal(input.hidden,false);
  type('5555');assert.equal(input.value,'9');
  type('1');type('6');
  assert.equal(scope.appLauncherSelection(scope.appWins.A).pins.length,3,'temporary truncation must not discard pins');
  assert.equal(grid.events.dragstart,undefined,'native drag previews are disabled');
  const third=pinnedTile(camera), first=pinnedTile(market);
  grid.events.pointerdown(pointer(third,180));
  grid.events.pointermove(pointer(third,177));
  assert.equal(third.style.transform,undefined,'stay put until drag threshold');
  pointerTarget=first;
  grid.events.pointermove(pointer(third,15));
  assert.equal(third.style.transform,'translate(-165px,0px)','the original tile follows the pointer');
  grid.events.pointerup(pointer(third,15));
  assert.equal(third.style.transform,'');
  assert.equal(grid.capture,null);
  grid.events.pointerdown(pointer(third,15));
  pointerTarget=null;
  grid.events.pointermove(pointer(third,15,120));
  grid.events.pointerup(pointer(third,15,120));
  assert.deepEqual(Array.from(scope.appLauncherDraft.pins),[camera,market,settings],'dragging outside preserves pins');
  type('7.5');toggle();assert.equal(saved.length,0);assert.equal(input.focused,true);
  type('6');
  search.value='xiangj';
  el('appwin-modal-grid').scrollTop=130;
  toggle();await flush();
  assert.deepEqual(saved,[{identity:'device:A',capacity:6,custom:true,pins:[camera,market,settings]}]);
  assert.equal(scope.appLauncherDraft,null);
  assert.equal(search.value,'');assert.equal(el('appwin-modal-grid').scrollTop,0);
  assert.deepEqual(profile.appLauncher.recent,[gallery,notes,browser]);
  toggle();assert.equal(input.value,'6');
  scope.rememberAppLauncherClick('A',files);
  assert.deepEqual(Array.from(profile.appLauncher.recent),[files,gallery,notes,browser]);
  assert.equal(errors.length,0);
  console.log('app launcher: canonical order, shared slots, outlines, inline validation, reorder and atomic exit save passed');
})().catch(error=>{console.error(error);process.exitCode=1;});
