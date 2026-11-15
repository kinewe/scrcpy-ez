'use strict';
const assert = require('node:assert/strict'), fs = require('node:fs'), vm = require('node:vm');
const source = fs.readFileSync(require('node:path').join(__dirname,'app.js'),'utf8');
const scope = {APPSET_FIELDS:{size:{fixed:[2560,1920,1080]},fps:{fixed:[120,90,60]}},
  PARAM_FIELDS:{res:{tiers:[2560,1920,1080]},fps:{tiers:[120,90,60]}},
  PARAM_FALLBACK:{usb:{res:2560,fps:120,bitrate:60},wifi:{res:1920,fps:60,bitrate:15}}};
vm.createContext(scope);
for (const name of ['nativeTiers','frameRateTiers','rebaseNativeField','applyNativeDefaults','appSetAutoDpi']) {
  const start=source.indexOf('  function '+name+'(');
  assert.ok(start>=0,name);
  vm.runInContext(source.slice(start,source.indexOf('\n  }',start)+4),scope);
}
function list(fixed,n){return Array.from(scope.nativeTiers(fixed,n));}
assert.deepEqual(list([2560,1920,1080],3200),[3200,2560,1920,1080]);
assert.deepEqual(list([2560,1920,1080],2160),[2160,1920,1080]);
assert.deepEqual(list([120,90,60],60),[60]);
assert.deepEqual(list([120,90,60],144),[144,120,90,60]);
assert.deepEqual(list([120,90,60],120),[120,90,60]);
function field(val,base,edit=false,input=''){return {val,base,edit,input,custom:false};}
const state={profile:{usb:{},wifi:{}},fields:{usb:{res:field(2560,2560),fps:field(90,120)},wifi:{res:field(4000,1920,true,'4000'),fps:field(60,60)}}};
assert.equal(scope.applyNativeDefaults(state,{},false),false);
scope.applyNativeDefaults(state,{longEdge:3200,fps:144},false);
assert.equal(state.fields.usb.res.val,3200);
assert.equal(state.fields.usb.fps.val,90,'late metadata preserves chosen lower tier');
assert.equal(state.fields.usb.fps.base,144);
assert.equal(state.fields.wifi.res.input,'4000','late metadata preserves explicit above-native input');
assert.equal(state.fields.wifi.fps.val,60);
assert.equal(state.fields.wifi.fps.base,60);
assert.equal(state.nativeFPS,144,'wireless defaults must not replace the physical ceiling');
assert.equal(state.profile.wifi.baseline.res,3200);
assert.equal(scope.applyNativeDefaults(state,{longEdge:3200,fps:144},false),false,'identical metadata does not rebuild DOM');
const app={tiers:{},fields:{size:field(1920,1920),fps:field(120,120)}};
scope.applyNativeDefaults(app,{longEdge:2160,fps:60,dpi:480,densityLongEdge:2160},true);
assert.deepEqual(Array.from(app.tiers.size),[2160,1920,1080]);
assert.deepEqual(Array.from(app.tiers.fps),[60]);
assert.equal(app.fields.size.val,2160);assert.equal(app.fields.fps.val,60);
scope.appSetState=app;assert.equal(scope.appSetAutoDpi(),480);
app.fields.size.val=1080;assert.equal(scope.appSetAutoDpi(),240);
assert.match(source,/size: \{ label: '分辨率档', unit: '', fixed: \[2560, 1920, 1080\]/);
assert.match(source,/res: \{ label: '分辨率档', unit: '', tiers: \[2560, 1920, 1080\]/);
console.log('native display: filtered presets, independent ceilings, late reads, custom retention, defaults and DPI passed');

const wireless={mode:'wifi',tiers:{},fields:{size:field(3200,3200),fps:field(60,60)}};
scope.applyNativeDefaults(wireless,{longEdge:3200,fps:144},true);
assert.deepEqual(Array.from(wireless.tiers.fps),[144,120,90,60]);
assert.equal(wireless.fields.fps.val,60);assert.equal(wireless.fields.fps.base,60);
wireless.fields.fps.val=144;
scope.applyNativeDefaults(wireless,{longEdge:3200,fps:144,dpi:600},true);
assert.equal(wireless.fields.fps.val,144,'a chosen high-rate tier remains selectable');
assert.equal(wireless.fields.fps.base,60,'reset still returns 60 after choosing native');
const appSM=require('./appsetting_state.js');
assert.equal(appSM.resetField(wireless.fields.fps).val,60);
assert.equal(appSM.settle(appSM.resetField(wireless.fields.fps)).custom,false);
assert.deepEqual(Array.from(scope.frameRateTiers([120,90,60],60,'wifi')),[60]);
assert.deepEqual(Array.from(scope.frameRateTiers([120,90,60],30,'wifi')),[30,60]);
const wired={mode:'usb',tiers:{},fields:{size:field(3200,3200),fps:field(120,120)}};
scope.applyNativeDefaults(wired,{longEdge:3200,fps:144},true);
assert.equal(wired.fields.fps.val,144);assert.equal(wired.fields.fps.base,144);

// Exercise the main panel's actual initial state and rendered selected chip.
Object.assign(scope,{SCEZParamState:require('./param_state.js'),PARAM_NOTES:{},
  updateParamAudioRow(){},updateParamCodecRows(){},refreshParamNotes(){}});
scope.PARAM_FIELDS.bit={label:'码率',unit:'',tiers:[120,80,60,40,15]};
for (const name of ['ladderTiers','baseValOf','initField','makeParamState','curParam','curField','baselineOf','renderParamRows']) {
  const start=source.indexOf('  function '+name+'(');
  vm.runInContext(source.slice(start,source.indexOf('\n  }',start)+4),scope);
}
const main=scope.makeParamState('device:A','A','wifi',{
  usb:{res:3200,fps:144,bitrate:60,baseline:{res:3200,fps:144,bitrate:60}},
  wifi:{res:3200,fps:60,bitrate:15,baseline:{res:3200,fps:60,bitrate:15}}
});
function node(){return {children:[],className:'',appendChild(n){this.children.push(n);},addEventListener(){},
  querySelector(selector){this.parts=this.parts||{};return this.parts[selector]||(this.parts[selector]=node());}};}
scope.document={createElement:node};
const box=node();scope.renderParamRows(main,box,()=>{});
const fpsChips=box.children[1].querySelector('.chips').children;
assert.deepEqual(fpsChips.slice(0,4).map(n=>n.textContent),['144','120','90','60']);
assert.equal(fpsChips[3].className,'chip sel');
assert.equal(main.fields.wifi.fps.base,60);
const explicit=scope.makeParamState('A','A','wifi',{
  usb:{res:3200,fps:144,bitrate:60,baseline:{res:3200,fps:144,bitrate:60}},
  wifi:{res:3200,fps:144,bitrate:15,custom:true,baseline:{res:3200,fps:60,bitrate:15}}
});
assert.equal(explicit.fields.wifi.fps.edit,false,'existing native choice is a preset, not an extra input');
assert.equal(scope.SCEZParamState.resetField(explicit.fields.wifi.fps).val,60);
console.log('wireless main/app defaults, native chip retention, selected 60, reset and custom choices passed');
