'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(__dirname + '/app.js', 'utf8');
const flush = async () => { await Promise.resolve(); await Promise.resolve(); };
function deferred() { let resolve, reject; const promise = new Promise((a,b) => { resolve=a; reject=b; }); return {promise,resolve,reject}; }
class Node {
  constructor() { this.style={}; this.children=[]; this.events={}; this.parts={}; this.classes=new Set(); this.classList={add:(...x)=>x.forEach(v=>this.classes.add(v)),remove:(...x)=>x.forEach(v=>this.classes.delete(v)),contains:x=>this.classes.has(x)}; }
  set innerHTML(x) { this.children=[]; this.parts={}; }
  appendChild(n) { this.children.push(n); }
  querySelector(s) { return this.parts[s] || (this.parts[s]=new Node()); }
  addEventListener(k,f) { this.events[k]=f; }
  contains(n) { return !!n.inParam; }
}
function harness() {
  const nodes={}, timers=new Map(), reads=[], saves=[], errors=[];
  let clock=0;
  const el=id=>nodes[id] || (nodes[id]=new Node());
  const scope={el,document:{activeElement:null,createElement:()=>new Node()},lastState:null,sessions:{},paramState:null,paramOpenToken:0,paramTransition:null,
    PARAM_FIELDS:{res:{label:'分辨率',unit:'',tiers:[2560,1920,1080]},fps:{label:'帧率',unit:' FPS',tiers:[120,90,60]},bit:{label:'码率',unit:' Mbps',tiers:[120,80,60,40,15]}},
    PARAM_FALLBACK:{usb:{res:3200,fps:120,bitrate:60},wifi:{res:1920,fps:60,bitrate:15}},PARAM_NOTES:{res:'分辨率'},
    SCEZParamState:require('./param_state'),updateParamAudioRow(){},updateParamCodecRows(){},watchNativeDefaults(){},refreshNow(){},
    // rc23-UI1：三页签/角标/说明框相关辅助（这些函数本身由页面 DOM 驱动，独立验收在真实 GUI 里做）
    refreshParamNotes(){},showDialogTab(){},showAllDialogPages(){},currentDialogTab(){return 'display';},
    clearDialogDirty(){},updateParamDirtyTabs(){},snapshotParamMode(){return {};},setTabDirty(){},setNote(){},
    GetProfile(serial){const d=deferred();reads.push({serial,...d});return d.promise;},
    SaveProfileAndRestart(...args){saves.push(args);return Promise.resolve();},toast:x=>errors.push(x),
    setTimeout(fn,ms){const id=++clock;timers.set(id,{fn,ms});return id;},clearTimeout:id=>timers.delete(id),
    window:{GetRootRepairEnabled:()=>Promise.resolve(false),matchMedia:()=>({matches:false})}};
  vm.createContext(scope);
  for(const name of ['nativeTiers','frameRateTiers','ladderTiers','baseValOf','curParam','curField','applyField','baselineOf','initField','makeParamState','renderParamRows','specContext','clearParamTransition','closeSpecModal','syncSpecModal','openSpecModal','renderParamModal']) {
    const start=source.indexOf('  function '+name+'(');assert.ok(start>=0,name);
    const lineEnd=source.indexOf('\n',start);
    const end=source.slice(start,lineEnd).trimEnd().endsWith('}') ? lineEnd : source.indexOf('\n  }',lineEnd)+4;
    vm.runInContext(source.slice(start,end),scope);
  }
  const start=source.indexOf("  el('param-save').addEventListener('click'");
  vm.runInContext(source.slice(start,source.indexOf('\n  // ---------- 指引弹窗',start)),scope);
  function timer() {const next=timers.entries().next().value;assert.ok(next,'expected transition timer');timers.delete(next[0]);next[1].fn();return next[1].ms;}
  function finish() {for(let n=0;timers.size;n++){assert.ok(n<20,'timer loop');timer();}}
  el('param-modal').style.display='none';
  return {scope,el,timers,reads,saves,errors,timer,finish};
}
function snapshot(mode, serial='192.0.2.159:5555', identity='device:PHONE') {
  return {sessions:[{serial,identity,mode,devName:'手机',cast:{serial,mode,active:true}}],devices:[{serial:mode==='usb'?'PHONE':serial,wireless:serial,identity,name:'手机',state:'device',connType:mode}],profiles:[{key:identity,name:'手机',serials:['PHONE'],addrs:[serial]}]};
}
function profile() {return {usb:{res:3200,fps:120,bitrate:60,baseline:{res:3200,fps:120,bitrate:60}},wifi:{res:1920,fps:60,bitrate:15,baseline:{res:1920,fps:60,bitrate:15}}};}
async function open(h, mode='wifi', serial='192.0.2.159:5555') {h.scope.lastState=snapshot(mode,serial);h.scope.openSpecModal(serial);h.reads.at(-1).resolve(profile());await flush();}

(async function() {
  const h=harness(), s=h.scope, serial='192.0.2.159:5555';
  let state=snapshot('wifi');state.sessions[0].mode='';
  assert.equal(s.specContext(state,serial).mode,'wifi','cast mode must work on the first open');
  state.sessions[0].cast.mode='';
  assert.equal(s.specContext(state,serial).mode,'wifi','wireless topology is a valid startup fallback');
  state=snapshot('usb');
  assert.equal(s.specContext(state,serial).mode,'usb','original wireless session key must not override current USB mode');
  state.sessions[0].mode='';state.sessions[0].cast.mode='wifi';
  assert.equal(s.specContext(state,serial).mode,'wifi','session evidence outranks a newly inserted USB card before switching');
  state.sessions[0].active=false;state.sessions[0].cast.active=false;
  assert.equal(s.specContext(state,serial).mode,'usb','ended session mode must yield to the current online connection');
  assert.equal(s.specContext({},'unresolved').mode,'','unknown must never silently select wired');
  assert.equal(s.specContext({cast:{serial,mode:'wifi'}},serial).mode,'wifi','legacy snapshots');
  assert.equal(s.specContext(snapshot('wifi',serial,'device:OTHER'),serial,'device:PHONE').conflict,true,'address reuse must not select another device');

  s.lastState=snapshot('usb');s.openSpecModal(serial);
  s.lastState=snapshot('wifi');h.reads[0].resolve(profile());await flush();
  assert.equal(s.paramState.mode,'wifi','profile response uses the latest mode, not the mode when requested');
  const draft=s.paramState;
  draft.fields.wifi.res.edit=true;draft.fields.wifi.res.input='2048';draft.fields.wifi.res.val=2048;
  draft.audio.wifi='both';draft.vcodec.wifi='h265';draft.profile.wifi.lockFps=true;
  s.renderParamModal();
  const oldInput=h.el('param-rows').children[0].querySelector('.chips').children.find(n=>n.type==='number');
  assert.ok(oldInput);
  oldInput.inParam=true;let blurred=false;
  oldInput.blur=()=>{blurred=true;oldInput.events.change();s.document.activeElement=null;};
  s.document.activeElement=oldInput;
  s.lastState=snapshot('usb');s.syncSpecModal(s.lastState);
  assert.equal(blurred,true,'old field is committed before switching');
  assert.equal(s.paramState,draft,'switching must not recreate the two-mode state');
  assert.equal(draft.mode,'wifi','old content remains throughout fade-out');
  assert.equal(h.el('param-card').classes.has('param-mode-out'),true);
  assert.equal(h.el('param-card').inert,true,'keyboard focus is blocked during the transition');
  assert.equal(h.el('param-save').disabled,true);
  h.el('param-save').events.click();assert.equal(h.saves.length,0,'save is blocked during switching');
  assert.equal(h.timer(),140);assert.equal(draft.mode,'usb');
  assert.equal(h.el('param-card').classes.has('param-mode-out'),false);
  assert.equal(h.el('param-card').classes.has('param-mode-busy'),true);
  oldInput.value='9999';oldInput.events.input();oldInput.events.change();
  assert.equal(draft.fields.usb.res.val,3200,'detached old-mode handlers cannot change the new mode');
  h.timer();assert.equal(h.el('param-save').disabled,false);
  assert.equal(h.el('param-card').inert,false);
  s.lastState={};s.syncSpecModal(s.lastState);
  assert.equal(draft.mode,'usb','temporary gaps must not reuse the original wireless key as a new route');
  assert.equal(h.timers.size,0);
  s.lastState=snapshot('wifi');s.syncSpecModal(s.lastState);h.finish();
  assert.equal(draft.fields.wifi.res.input,'2048');assert.equal(draft.audio.wifi,'both');assert.equal(draft.vcodec.wifi,'h265');assert.equal(draft.profile.wifi.lockFps,true);
  assert.equal(h.reads.length,1,'mode switches never reload and overwrite unsaved drafts');
  const currentRows=h.el('param-rows').children;
  s.syncSpecModal(s.lastState);assert.equal(h.el('param-rows').children,currentRows,'unchanged snapshots preserve the input DOM');

  // Rapid reversals, both before and after the midpoint, converge on latest mode.
  s.lastState=snapshot('usb');s.syncSpecModal(s.lastState);
  s.lastState=snapshot('wifi');s.syncSpecModal(s.lastState);h.finish();assert.equal(draft.mode,'wifi');
  s.lastState=snapshot('usb');s.syncSpecModal(s.lastState);h.timer();
  s.lastState=snapshot('wifi');s.syncSpecModal(s.lastState);h.finish();assert.equal(draft.mode,'wifi');
  h.el('param-save').events.click();await flush();
  assert.equal(h.saves.length,1);assert.equal(h.saves[0][1],'wifi');assert.equal(h.saves[0][2],2048);assert.equal(h.saves[0][6],'both');
  assert.equal(s.paramState,null);

  // A closed dialog is never revived by a late animation or profile response.
  await open(h);s.lastState=snapshot('usb');s.syncSpecModal(s.lastState);
  const queued=Array.from(h.timers.values())[0].fn;s.closeSpecModal();queued();
  assert.equal(h.el('param-modal').style.display,'none');assert.equal(h.timers.size,0);
  s.openSpecModal(serial);const stale=h.reads.at(-1);s.closeSpecModal();stale.resolve(profile());await flush();
  assert.equal(s.paramState,null);
  s.openSpecModal(serial);const first=h.reads.at(-1);
  const other='192.0.2.160:5555';s.lastState=snapshot('wifi',other,'device:OTHER');s.openSpecModal(other);const second=h.reads.at(-1);
  second.resolve(profile());await flush();first.resolve(profile());await flush();assert.equal(s.paramState.serial,other);

  // Unknown startup is explicit and non-saveable; later evidence switches it.
  s.lastState={};s.openSpecModal('unknown');h.reads.at(-1).resolve(profile());await flush();
  assert.equal(h.el('param-save').disabled,true);assert.match(h.el('param-mode').textContent,/确认中/);
  s.lastState=snapshot('wifi','unknown');s.syncSpecModal(s.lastState);h.finish();assert.equal(s.paramState.mode,'wifi');
  s.lastState=snapshot('wifi','unknown','device:REPLACED');s.syncSpecModal(s.lastState);assert.equal(s.paramState,null);
  assert.ok(h.errors.some(e=>e.includes('身份已变化')));
  await open(h);s.window.matchMedia=()=>({matches:true});s.lastState=snapshot('usb');s.syncSpecModal(s.lastState);
  assert.equal(h.timer(),0);assert.equal(h.timer(),0,'reduced motion does not wait through fades');assert.equal(s.paramState.mode,'usb');
  assert.match(fs.readFileSync(__dirname+'/style.css','utf8'),/#param-card\s*\{\s*transition:\s*opacity 140ms ease/);
  console.log('spec modal: startup, live switch, two-mode drafts, fade phases, late events, save routing and async cancellation passed');
})().catch(e=>{console.error(e);process.exit(1);});
