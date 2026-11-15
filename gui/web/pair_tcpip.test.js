'use strict';
const assert = require('assert');
const fs = require('fs');
const vm = require('vm');
const PairUI = require('./pair_ui.js');
const source = fs.readFileSync(__dirname+'/app.js','utf8');
const elements = new Map();
function el(id) {
  if (!elements.has(id)) elements.set(id,{value:'',placeholder:'',style:{},classList:{toggle(){},add(){},remove(){}},listeners:{},addEventListener(e,fn){this.listeners[e]=fn;},select(){},focus(){}});
  return elements.get(id);
}
let calls=[], errors=[];
const ctx=vm.createContext({el,PairUI,window:{PairConnect(){}},
  pairCtx:{manual:true,pending:[],directAddr:'',key:'',ip:''},
  pairBusy:false,pairFailed:false,pairOpen:true,pairSubmitPending:false,pairSubmitToken:0,
  pairSetBusy(v){ctx.pairBusy=v;},pairClearFail(){ctx.pairFailed=false;},
  pairRenderFound(){},pairRenderStepsInto(){},
  PairConnect(...args){calls.push(args);},refreshNow(){},toast(msg){errors.push(msg);}
});
function load(name,next) {
  const start=source.indexOf('  function '+name+'('),end=source.indexOf('  function '+next+'(',start);
  assert(start>=0&&end>start,name);vm.runInContext(source.slice(start,end),ctx);
}
load('pairCodeInput','pairCodeInputs');
load('pairCodeValue','pairClearCode');
load('pairManualIp','pairApplyPending');
load('pairDirectAction','pairRenderAction');
load('pairRenderAction','pairSetBusy');
const start=source.indexOf('  // ---------- 配对向导事件 ----------');
const end=source.indexOf("  el('pair-swap').addEventListener",start);
vm.runInContext(source.slice(start,end),ctx);
for (let i=1;i<=4;i++) el('pair-ip'+i).value=['192','0','2','10'][i-1];
function code(value) {el('pair-code').value=value;el('pair-code').listeners.input();}
function port(value) {el('pair-port-seg').value=value;el('pair-port-seg').listeners.input();}
function click() {ctx.pairBusy=false;el('pair-go').listeners.click();}
port('5555');code('');
assert.strictEqual(el('pair-code').placeholder,'免配对码，首次连接可能需要授权');
assert.strictEqual(el('pair-go').textContent,'连接');
click();assert.deepStrictEqual(calls.pop(),['','192.0.2.10','5555','','']);
ctx.pairBusy=false;code('1');
assert.strictEqual(el('pair-go').textContent,'配对');
assert.strictEqual(el('pair-code').placeholder,'000 000');
click();assert.strictEqual(calls.length,0,'incomplete code must never fall back to 5555');
assert.strictEqual(errors.at(-1),'配对码格式不正确\n请输入手机显示的 6 位数字配对码');
code('123456');click();assert.deepStrictEqual(calls.pop(),['','192.0.2.10','5555','','123456']);
ctx.pairBusy=false;code('abcdef');assert.strictEqual(el('pair-go').textContent,'配对');
click();assert.strictEqual(calls.length,0,'invalid text must not turn into an empty-code connection');
ctx.pairBusy=false;code('');assert.strictEqual(el('pair-go').textContent,'连接');
for (const value of ['5554','05555','65535','0','65536','5555a','abc','']) {
  port(value);assert.strictEqual(el('pair-go').textContent,'配对',value);
  code('123456');click();
  if (PairUI.validPort(value)) assert.strictEqual(calls.pop()[2],value);
  else {
    assert.strictEqual(calls.length,0,'invalid port submitted: '+value);
    assert.strictEqual(errors.at(-1),'端口格式不正确\n请输入 1–65535 的端口号');
  }
  ctx.pairBusy=false;code('');
}
ctx.pairCtx={manual:false,pending:[{}],key:'192.0.2.10:41000',ip:'192.0.2.10',directAddr:'192.0.2.10:5555'};
code('');click();assert.deepStrictEqual(calls.pop(),['192.0.2.10:41000','192.0.2.10','','','']);
ctx.pairBusy=false;code('123456');assert.strictEqual(el('pair-go').textContent,'配对');
click();assert.strictEqual(calls.pop()[4],'123456');
ctx.pairBusy=false;code('');assert.strictEqual(el('pair-go').textContent,'连接');
ctx.pairCtx.directAddr='';ctx.pairRenderAction();assert.strictEqual(el('pair-go').textContent,'配对');
click();assert.strictEqual(calls.length,0,'no advertised capability cannot connect empty code');
console.log('TCP/IP action, placeholder, editing, manual/auto RPC routing and invalid ports passed');
