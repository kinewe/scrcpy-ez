'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(require('node:path').join(__dirname, 'app.js'), 'utf8');
const start = source.indexOf('  var settingsState = ');
const end = source.indexOf('  var lastProfileSaveError = ', start);
const clone = value => JSON.parse(JSON.stringify(value));
function element() {
  return {style:{display:'none'}, attrs:{}, handlers:{}, value:'', textContent:'', disabled:false,
    classList:{add(){},remove(){}}, focus(){}, setAttribute(k,v){this.attrs[k]=v;}, addEventListener(k,v){this.handlers[k]=v;}, querySelectorAll(){return this.buttons || [];} };
}
const nodes = new Map();
const el = id => {if (!nodes.has(id)) nodes.set(id,element());return nodes.get(id);};
const modes = ['off','otp','all'].map(mode => Object.assign(element(),{dataset:{mode}}));
el('notification-batch-modal').buttons=modes;
el('notification-preview').buttons=['full','hidden'].map(value=>Object.assign(element(),{value,checked:false}));
el('notification-copy-minutes').buttons=['15','60','360','1440','4320'].map(value=>Object.assign(element(),{value,checked:false}));
const apps = [{pkg:'com.example.mail'},{pkg:'com.example.chat'},{pkg:'com.example.store'}];
const badge = {innerHTML:''};
const card = Object.assign(element(),{dataset:{notificationPkg:apps[0].pkg,notificationOther:'false'},lastElementChild:{textContent:'邮件'},querySelector(){return badge;}});
el('appwin-modal-grid').buttons=[card];
let backend = {settings:{notificationDefault:true,notificationPreview:true,notificationDevices:{},notificationPolicies:{},notificationCopyMinutes:1440},profiles:[{key:'tablet'},{key:'phone'}]};
let selected = [{identity:'tablet'}], reject=false, hold=null;
const saves=[],messages=[],launches=[];
const context = {el,Promise,SCEZNotificationIcons:require('./notification_icons'),lastState:backend,lastJson:'',appWinModalSerial:'wifi-tablet',appWinNotificationView:false,notificationOptionsOpen:false,notificationSaving:false,notificationBatchTargets:[],
  appWins:{'wifi-tablet':{identity:'tablet',loaded:true,name:'平板 <test>',apps}},
  batchSelectedDevices:()=>selected,syncBatchUI(){},renderDevices(){},renderAppWinGrid(serial){context.renderAppNotificationControls(serial);},deviceIdentityOf:()=>'',refreshNow(){},toast:text=>messages.push(text),
  SetSettings:()=>Promise.resolve(),SetOtherAppWinSystemDecorations:()=>Promise.resolve(),GetState:()=>Promise.resolve(clone(backend)),
  SetNotificationModes:(ids,mode)=>{saves.push({ids:ids.slice(),mode});if(reject)return Promise.reject(new Error('write failed'));ids.forEach(id=>backend.settings.notificationPolicies[id]={...backend.settings.notificationPolicies[id],mode});return Promise.resolve();},
  ApplyNotificationEdit:(id,edit)=>{
    saves.push({id,edit:clone(edit)});
    const commit=()=>{if(reject)throw new Error('write failed');const p={...backend.settings.notificationPolicies[id]};
      if(edit.selection){const {packages,other}=edit.selection;Object.assign(p,{mode:!packages.length&&!other?'off':other&&apps.every(app=>packages.includes(app.pkg))?'all':'whitelist',packages:packages.slice(),other});}
      if('preview' in edit)p.preview=edit.preview;
      if(edit.selection||'preview' in edit)backend.settings.notificationPolicies[id]=p;
      if('copyMinutes' in edit)backend.settings.notificationCopyMinutes=edit.copyMinutes;
    };
    return (hold || Promise.resolve()).then(commit);
  },StartAppWin:()=>launches.push(true)
};
vm.createContext(context);vm.runInContext(source.slice(start,end),context);
const closeStart=source.indexOf('  function closeAppWinModal(');
vm.runInContext(source.slice(closeStart,source.indexOf('\n  }',closeStart)+4),context);
const drain=()=>new Promise(resolve=>setImmediate(resolve));
const toggle=()=>el('appwin-notifications').handlers.click();
let prevented=0;
const rightClick=()=>el('appwin-modal-grid').handlers.contextmenu({preventDefault(){prevented++;}});
function fresh(mode='all') {
  context.notificationDraft=null;context.appWinModalSerial='wifi-tablet';context.appWinNotificationView=false;
  el('appwin-modal').style.display='';el('appwin-modal-search').value='';
  backend.settings.notificationPolicies.tablet={mode,packages:['com.example.mail'],other:true};
  context.syncSettings(clone(backend));toggle();
}
const draft=()=>context.notificationDraft.policy;
(async()=>{
  context.syncSettings(clone(backend));toggle();toggle();await drain();
  assert.equal(saves.length,0,'unedited view switch does not save or start listeners');
  assert.equal(el('appwin-modal-title').textContent,'平板 <test> · 应用窗口');
  context.openBatchNotifications();modes[1].handlers.click();await drain();
  assert.deepEqual(clone(saves[0]),{ids:['tablet'],mode:'otp'});
  assert.equal(backend.settings.notificationPolicies.phone,undefined,'unselected device untouched');
  selected=[{identity:'tablet'},{identity:'phone'}];context.openBatchNotifications();modes[2].handlers.click();await drain();
  assert.equal(backend.settings.notificationPolicies.phone.mode,'all');
  const before=saves.length;
  fresh();el('appwin-modal-search').value='mail';context.toggleNotificationApp('wifi-tablet','com.example.mail');
  assert.deepEqual(clone(draft().packages),['com.example.chat','com.example.store']);assert.equal(draft().other,true);
  assert.equal(card.attrs['aria-label'],'邮件，未允许通知','badge label retains application name');
  assert.equal(el('appwin-modal-grid').buttons[0],card,'draft edit preserves app DOM and keyboard focus');
  el('notification-preview').handlers.change({target:{value:'hidden'}});
  el('notification-copy-minutes').handlers.change({target:{value:'60'}});
  assert.equal(el('notification-preview').buttons[1].checked,true);assert.equal(el('notification-copy-minutes').buttons[1].checked,true);
  context.syncSettings(clone(backend));
  assert.equal(draft().preview,false,'background poll preserves draft');
  assert.equal(saves.length,before,'checkboxes and options must not write per edit');
  assert.equal(backend.settings.notificationPolicies.tablet.mode,'all','live policy unchanged before exit');
  assert.equal(backend.settings.notificationCopyMinutes,1440);
  toggle();await drain();
  assert.equal(saves.length,before+1,'switch to normal view saves once');
  assert.equal(context.appWinNotificationView,false);
  assert.deepEqual(saves.at(-1).edit,{selection:{packages:['com.example.chat','com.example.store'],other:true},preview:false,copyMinutes:60});
  assert.equal(backend.settings.notificationPolicies.phone.mode,'all');
  fresh();rightClick();assert.equal(draft().packages.length,0);assert.equal(draft().other,false);
  assert.equal(draft().mode,'off','empty selection maps to disabled mode');
  rightClick();assert.equal(draft().mode,'all','two rapid inversions restore all sources');
  fresh();rightClick();context.closeAppWinModal();await drain();
  assert.equal(backend.settings.notificationPolicies.tablet.mode,'off','exit commits empty selection as off');
  fresh('otp');rightClick();assert.equal(draft().packages.length,0,'OTP starts with all selected');
  fresh('off');context.toggleNotificationApp('wifi-tablet','com.example.chat');
  assert.deepEqual(clone(draft().packages),['com.example.chat']);assert.equal(draft().other,false,'off ignores remembered selections');
  fresh();el('appwin-modal-search').value='mail';rightClick();assert.deepEqual(clone(draft().packages),['com.example.chat','com.example.store']);
  el('appwin-modal-search').value='暗之通知';rightClick();assert.equal(draft().other,false);
  const editBefore=JSON.stringify(context.notificationDraft.edit);el('appwin-modal-search').value='no match';rightClick();
  assert.equal(JSON.stringify(context.notificationDraft.edit),editBefore,'empty search does not edit');
  const stateBefore=JSON.stringify(backend.settings), retryEdit=clone(context.notificationDraft.edit), count=saves.length;
  reject=true;context.closeAppWinModal();context.closeAppWinModal();await drain();
  assert.equal(saves.length,count+1,'double exit does not duplicate write');
  assert.equal(JSON.stringify(backend.settings),stateBefore,'failed transaction preserves saved settings');
  assert.deepEqual(clone(context.notificationDraft.edit),retryEdit,'failed exit retains draft');
  assert.equal(context.appWinNotificationView,true);assert.equal(el('appwin-modal').style.display,'');
  assert.equal(context.notificationSaving,false);assert.ok(messages.some(t=>t.includes('修改已保留')));
  reject=false;context.closeAppWinModal();await drain();assert.equal(el('appwin-modal').style.display,'none');assert.equal(context.appWinModalSerial,null);
  fresh('otp');let resolveHold;hold=new Promise(resolve=>{resolveHold=resolve;});
  el('notification-preview').handlers.change({target:{value:'full'}});toggle();await drain();
  assert.equal(context.notificationSaving,true);const pendingCount=saves.length;toggle();context.closeAppWinModal();
  assert.equal(saves.length,pendingCount);resolveHold();await drain();hold=null;
  assert.equal(backend.settings.notificationPolicies.tablet.mode,'otp','preview-only edit retains current mode');
  assert.equal(context.notificationSaving,false);assert.equal(context.appWinNotificationView,false);
  const ordinaryCount=saves.length;rightClick();assert.equal(saves.length,ordinaryCount,'normal view ignores right click');
  assert.equal(launches.length,0,'notification editing never casts');assert.ok(prevented>0);
  context.openBatchNotifications();reject=true;modes[0].handlers.click();await drain();reject=false;
  assert.equal(backend.settings.notificationPolicies.tablet.mode,'otp','failed batch write preserves policy');
  context.closeBatchNotifications();selected=[{identity:'unpaired'}];context.openBatchNotifications();
  assert.ok(messages.some(t=>t.includes('尚未完成建档')));
  context.appWinNotificationView=false;context.notificationDraft=null;
  backend.settings.notificationPolicies.tablet={mode:'whitelist',packages:[],other:false};
  context.syncSettings(clone(backend));
  assert.equal(context.notificationPolicyFor('tablet').mode,'off','legacy empty whitelist renders as disabled');
  assert.equal(context.notificationDetailFor('tablet',backend),'通知已关闭');
  selected=[{identity:'tablet'}];context.openBatchNotifications();assert.equal(modes[0].attrs['aria-pressed'],'true','bulk off button reflects empty whitelist');
  context.closeBatchNotifications();
  backend.settings.notificationPolicies.tablet.other=true;context.syncSettings(clone(backend));
  assert.equal(context.notificationPolicyFor('tablet').mode,'whitelist','complement-only rule remains enabled');
  console.log('notification UI: local drafts, atomic exit save, failure/retry, rapid inversion, search/complement, background poll, options and device isolation passed');
})().catch(error=>{console.error(error);process.exitCode=1;});
