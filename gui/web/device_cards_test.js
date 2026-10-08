'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(require('node:path').join(__dirname, 'app.js'), 'utf8');

// Execute the actual renderer against a small DOM, including event handlers and
// rename inputs. This catches failures that pure card helper tests cannot see.
class Element {
  constructor(tag) { this.tagName=tag; this.children=[]; this.style={}; this.dataset={}; this.events={}; this.className=''; this.value=''; }
  get classList() { return {
    contains: c => this.className.split(' ').includes(c),
    add: c => { if (!this.classList.contains(c)) this.className+=' '+c; },
    remove: c => { this.className=this.className.split(' ').filter(x=>x!==c).join(' '); },
    toggle: (c,on) => { (on ? this.classList.add : this.classList.remove)(c); }
  }; }
  get nextSibling() { if(!this.parentNode)return null; return this.parentNode.children[this.parentNode.children.indexOf(this)+1] || null; }
  contains(el) { return this===el || this.children.some(c=>c.contains(el)); }
  set innerHTML(v) { this.children.slice().forEach(c=>this.removeChild(c)); }
  appendChild(el) { return this.insertBefore(el,null); }
  insertBefore(el,before) {
    if(el===before)return el;
    if(el.parentNode)el.parentNode.removeChild(el);
    const at=before ? this.children.indexOf(before) : this.children.length;
    assert.ok(at>=0,'insertion anchor belongs to parent');
    this.children.splice(at,0,el);el.parentNode=this;return el;
  }
  removeChild(el) {
    const at=this.children.indexOf(el);assert.ok(at>=0,'removed child belongs to parent');
    if(el.contains(document.activeElement))document.activeElement=null;
    this.children.splice(at,1);el.parentNode=null;return el;
  }
  focus() { document.activeElement=this; }
  setSelectionRange(start,end,direction) { this.selectionStart=start;this.selectionEnd=end;this.selectionDirection=direction; }
  addEventListener(name,fn) { this.events[name]=fn; }
  closest(selector) { if(this.classList.contains(selector.slice(1))) return this; return this.parentNode ? this.parentNode.closest(selector) : null; }
  querySelectorAll(selector) { const out=[]; for(const c of this.children) {if(c.classList.contains(selector.slice(1)))out.push(c); out.push(...c.querySelectorAll(selector));} return out; }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
}
const nodes={};
const document={activeElement:null,getElementById: id => nodes[id] || (nodes[id]=new Element('div')), createElement: tag => new Element(tag),createTextNode: txt => Object.assign(new Element('text'),{textContent:txt}),querySelectorAll: s => nodes['device-list'].querySelectorAll(s.split(' ').pop())};
const scope={document,Date,setTimeout:()=>0,clearTimeout:()=>{},batchMode:false,renameMode:false,batchSel:{},renameDraft:{},batchDeviceKeys:{},lastState:null,appBarLeaving:{},renderedBars:{},deletingKeys:{},devOrder:[],appWins:{},openAppCards:{},fieldSticky:{},settingsState:{notificationDefault:false},notificationSaving:false,appWinNotificationView:false,notificationDraft:null,
  AppWinBar:require('./appwin_bar'),SessionMap:require('./session_map'),DragOrder:require('./drag_order'),
  sweepDeletingKeys:()=>{},saveOrderBackend:()=>{},scheduleAppBarRefresh:()=>{},profileKeysFor:()=>[],deviceIdentityOf:()=>'',openDeleteBubble:()=>{},startCast:()=>{},switchView:()=>{},openAppWin:()=>{},activateSession:()=>{},switchToDeviceAppWin:()=>{},stopAllAppWins:()=>{},refreshNow:()=>{},StopCast:()=>Promise.resolve(),toast:()=>{},toggleBatchSelect: (key,st)=>{scope.batchSel[key]=!scope.batchSel[key];scope.renderDevices(st);}};
vm.createContext(scope);
for(const name of ['devKey','devDisplayName','el','vtSafeName','fmtSub','stickyKey','stickyFill','batchSelectedDevices','syncBatchUI','collectRenameObj','reconcileBatchDeviceKeys','collectRenameCards','commitDeviceCards','notificationPolicyFor','notificationDetailFor','renderDevices']) {
  const start=source.indexOf('  function '+name+'('); assert.ok(start>=0,name);
  const lineEnd=source.indexOf('\n',start);
  const line=source.slice(start,lineEnd);
  const end=line.trimEnd().endsWith('}') ? lineEnd : source.indexOf('\n  }',lineEnd)+4;
  vm.runInContext(source.slice(start,end),scope);
}
const addr='192.0.2.11:5555';
const pending={serial:addr,identity:'pending:'+addr,identityEpoch:1,state:'device',connType:'wifi',name:'Xiaomi Pad 8 Pro',wirelessIP:addr,wirelessRes:'1920x1280',fps:120,battery:83};
const phone={serial:'192.0.2.12:5555',identity:'device:PHONE',identityEpoch:2,state:'device',connType:'wifi',name:'Xiaomi Pad 8 Pro',stableSerial:'PHONE',wirelessIP:'192.0.2.12:5555',wirelessRes:'1920x864',fps:60};
function render(devices) {scope.lastState={adbOK:true,devices,sessions:[],profiles:[],devOrder:[]};scope.renderDevices(scope.lastState);return nodes['device-list'].children;}
assert.equal(render([pending,phone]).length,2,'ordinary cards');
assert.equal(nodes['device-list'].querySelectorAll('.dev-notification-detail').length,0,'normal cards must hide notification details');
assert.ok(!scope.fmtSub(phone).includes('序列号'));
assert.ok(scope.fmtSub({serial:'USB_A',state:'device',connType:'usb',res:'3200x2136',fps:120}).includes('USB_A'));
scope.batchMode=true;
let cards=render([pending,phone]);
assert.equal(cards.length,2,'batch mode must render every card without exceptions');
assert.equal(nodes['device-list'].querySelectorAll('.dev-notification-detail').length,2,'bulk cards show notification details');
cards[0].events.click({target:cards[0]});
assert.equal(scope.batchSelectedDevices(scope.lastState).length,1);
scope.renameMode=true; cards=render([pending,phone]);
const input=cards[0].querySelectorAll('.rename-input')[0];assert.ok(input,'selected card has name input');
input.value='我的平板';input.events.input();
assert.equal(scope.collectRenameObj(scope.lastState)['pending:'+addr+'|1'],'我的平板','pending rename includes connection token');
input.focus();input.setSelectionRange(1,3,'backward');
const renameCard=cards[0];
for(let frame=0;frame<10;frame++) {
  cards=render([Object.assign({},pending,{battery:83-frame}),phone]);
  assert.equal(document.activeElement,input,'casting snapshots must not remove rename focus');
  assert.equal(cards[0],renameCard,'rename card stays attached during refresh');
  assert.equal(cards[0].querySelector('.rename-input'),input,'native input is retained');
  assert.equal(input.selectionStart,1);assert.equal(input.selectionEnd,3);assert.equal(input.selectionDirection,'backward');
}
const confirmed=Object.assign({},pending,{identity:'device:PAD_A',stableSerial:'PAD_A'});
scope.appWins[addr]={identity:pending.identity,identityEpoch:1};
scope.appBarLeaving.test='playing';
render([confirmed,phone]);
assert.equal(renameCard.dataset.key,'device:PAD_A','deferred rendering still carries identity promotion to the existing input');
delete scope.appBarLeaving.test;
cards=render([confirmed,phone]);
assert.equal(cards.length,2);
assert.equal(scope.batchSel['device:PAD_A'],true,'selection follows identity confirmation');
assert.equal(document.activeElement,input,'identity confirmation preserves focus');
assert.equal(cards[0].querySelector('.rename-input'),input,'identity confirmation keeps the input node');
assert.equal(cards[0].querySelectorAll('.rename-input')[0].value,'我的平板','draft follows same device');
input.value='我的平板改名';input.events.input();
assert.equal(scope.renameDraft['device:PAD_A'],'我的平板改名','input handler follows the promoted identity');
assert.equal(scope.renameDraft[pending.identity],undefined,'old pending key is not recreated');
assert.equal(scope.collectRenameObj(scope.lastState)['device:PAD_A'],'我的平板改名');
assert.equal(scope.appWins[addr].identity,'device:PAD_A','open application panel follows confirmed archive');
const usbConfirmed=Object.assign({},confirmed,{serial:'PAD_A',connType:'usb',identityEpoch:0});
cards=render([usbConfirmed,phone]);
assert.equal(scope.batchSel['device:PAD_A'],true,'selection remains on USB switch');
assert.equal(document.activeElement,input,'transport switch preserves focus');
assert.ok(cards[0].querySelector('.dot').classList.contains('usb'),'connection state still refreshes during rename');
input.value='正在输入的中文'; // An IME draft need not have emitted its final input event yet.
cards=render([phone,usbConfirmed]);
assert.equal(document.activeElement,input,'device reordering never detaches the active input');
assert.equal(cards[1].querySelector('.rename-input'),input);
assert.equal(input.value,'正在输入的中文','refresh must not overwrite an in-progress composition with an older draft');
const newcomer=Object.assign({},phone,{serial:'NEW_PHONE',identity:'device:NEW_PHONE'});
cards=render([newcomer,usbConfirmed,phone]);
assert.equal(document.activeElement,input,'new device does not interrupt editing');
cards=render([usbConfirmed]);
assert.equal(document.activeElement,input,'removing another device does not interrupt editing');
assert.equal(scope.collectRenameObj(scope.lastState)['device:PAD_A'],'正在输入的中文');
scope.batchSel[phone.identity]=true;
cards=render([usbConfirmed,phone]);
const phoneInput=cards[1].querySelector('.rename-input');
phoneInput.value='另一台设备';phoneInput.events.input();phoneInput.focus();phoneInput.setSelectionRange(2,2,'none');
cards=render([Object.assign({},usbConfirmed,{state:'offline'}),phone]);
assert.equal(document.activeElement,phoneInput,'editing another selected device remains stable');
assert.equal(cards[0].querySelector('.rename-input'),input,'both selected inputs survive refresh');
assert.ok(cards[0].querySelector('.dot').classList.contains('off'),'offline state refreshes without rebuilding its input');
assert.equal(scope.collectRenameObj(scope.lastState)[phone.identity],'另一台设备');
scope.renameMode=false;scope.batchSel={};scope.renameDraft={};render([pending,phone]);
scope.batchSel[pending.identity]=true;scope.renameDraft[pending.identity]='旧名称';
scope.renameMode=true;cards=render([pending,phone]);
const oldOwnerInput=cards[0].querySelector('.rename-input');oldOwnerInput.focus();
render([Object.assign({},pending,{identityEpoch:3}),phone]);
assert.ok(!scope.batchSel[pending.identity],'a reused IP cannot keep pending selection');
assert.equal(scope.renameDraft[pending.identity],undefined);
assert.equal(document.activeElement,null,'reused IP drops the old owner input');
assert.equal(nodes['device-list'].querySelector('.rename-input'),null,'new owner cannot inherit the old input');
scope.renameMode=false;scope.batchMode=false;assert.equal(render([confirmed,phone]).length,2,'exit batch mode');
const initialCards=render([Object.assign({},confirmed,{appBusy:true}),phone]);
const firstAppButton=initialCards[0].children.find(c=>c.tagName==='button' && c.textContent==='读取中…');
assert.ok(firstAppButton && firstAppButton.disabled,'only the initial empty-cache app button is disabled');
const warmAppButton=initialCards[1].children.find(c=>c.tagName==='button' && c.textContent==='应用');
assert.ok(warmAppButton && !warmAppButton.disabled,'another device with a cache stays accessible');
console.log('device_cards: normal/batch rendering, rename focus/selection/draft retention, identity promotion, transport switch, device reorder and reused-IP isolation passed');
