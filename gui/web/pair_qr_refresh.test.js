'use strict';
const assert = require('assert');
const fs = require('fs');
const vm = require('vm');
const PairUI = require('./pair_ui');
const source = fs.readFileSync(__dirname + '/app.js', 'utf8');
const elements = new Map();
function el(id) {
  if (!elements.has(id)) elements.set(id, {style:{}, listeners:{}, addEventListener(name, fn){ this.listeners[name] = fn; }});
  return elements.get(id);
}
let resolveRPC, rejectRPC, rpcCount = 0, refreshCount = 0, messages = [], drawn = [];
const ctx = vm.createContext({el, PairUI, window:{PairConnect(){}},
  pairBusy:false, pairSuccessShown:false, pairOpen:true, pairSubmitToken:1,
  pairQrLast:null, pairQrRefreshTimer:null, pairQrAutoRefreshKey:null,
  PairConnect(){ rpcCount++; return new Promise((resolve, reject) => {resolveRPC=resolve; rejectRPC=reject;}); },
  refreshNow(){refreshCount++;}, toast(message){messages.push(message);},
  pairDrawQr(text){drawn.push(text);}, setTimeout(){return 1;}, clearTimeout(){}, Date
});
function load(name, next) {
  const start=source.indexOf('  function '+name+'('), end=source.indexOf('  function '+next+'(',start);
  assert(start>=0 && end>start); vm.runInContext(source.slice(start,end),ctx);
}
load('pairAutoRefreshQr','pairRenderQr');
load('pairRenderQr','pairFailText');
const start=source.indexOf("  el('pair-qr-area').addEventListener('click'");
const end=source.indexOf("  el('pair-close').addEventListener",start);
vm.runInContext(source.slice(start,end),ctx);
const click=()=>el('pair-qr-area').listeners.click();
const flush=()=>new Promise(resolve=>setImmediate(resolve));
(async()=>{
  ctx.pairRenderQr({phase:'failed',mode:'auto',qrText:'expired-test-qr',qrExpireAt:1});
  assert.strictEqual(rpcCount,1,'failed connection must still allow automatic QR refresh');
  assert.strictEqual(refreshCount,0,'wait for the refresh RPC before reading its state');
  resolveRPC(); await flush(); assert.strictEqual(refreshCount,1);
  ctx.pairRenderQr({phase:'idle',mode:'qr',qrText:'fresh-test-qr',qrExpireAt:Math.floor(Date.now()/1000)+120});
  assert.strictEqual(el('pair-qr-expired').style.display,'none');
  assert.strictEqual(drawn.at(-1),'fresh-test-qr');
  click(); rejectRPC(new Error('refresh rejected')); await flush();
  assert.strictEqual(messages.length,1,'manual refresh rejection must be visible');
  assert.strictEqual(messages[0], '二维码刷新失败\n请点击二维码重试');
  const before=rpcCount; ctx.pairBusy=true; click(); assert.strictEqual(rpcCount,before);
  ctx.pairBusy=false; click(); ctx.pairSubmitToken++; ctx.pairOpen=false;
  resolveRPC(); await flush(); assert.strictEqual(refreshCount,1,'closed modal must ignore the old completion');
  console.log('expired QR recovery, RPC completion, visible errors and active/closed modal guards passed');
})().catch(error=>{console.error(error);process.exitCode=1;});
