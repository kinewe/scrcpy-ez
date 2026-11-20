'use strict';
const assert = require('assert');
const fs = require('fs');
const vm = require('vm');
const source = fs.readFileSync(__dirname + '/app.js', 'utf8');
const PairUI = require('./pair_ui.js');
const elements = new Map();
function el(id) {
  if (!elements.has(id)) elements.set(id, {style:{}, disabled:false, textContent:'', classList:{add(){},remove(){},toggle(){}}, listeners:{}, addEventListener(name,fn){this.listeners[name]=fn;}});
  return elements.get(id);
}
let backend = {pending:[], pairStatus:{phase:'failed',errCode:'internal',errText:'missing adb.exe'}};
let deviceRenders = 0;
const noOp = () => {};
const ctx = vm.createContext({
  el, PairUI, window:{}, PairConnect:null, Promise,
  pairOpen:true, pairCtx:{key:'192.0.2.10:40000',ip:'192.0.2.10',pending:[{}],manual:false},
  pairBusy:false, pairFailed:false, pairSuccessShown:false,
  pairSubmitPending:false, pairSubmitToken:0,
  pairSuccessTimer:null, pairMaskFadeTimer:null, pairQrRefreshTimer:null,
  pairQrLast:null, pairQrAutoRefreshKey:null,
  lastJson:'', lastState:null, lastProfileSaveError:'', lastWeChatScanNotice:'', nextStateRequest:0, appliedStateRequest:0,
  tickAppWins:noOp, syncSettings:noOp, syncAppWins:noOp, syncSpecModal:noOp, syncAppSetModal:noOp, syncAppLauncherGrid:noOp, appLaunchDialog:{sync:noOp}, cameraPermissionDialog:{sync:noOp},
  syncAppEntryMasks:noOp, renderDevices(){deviceRenders++;}, renderSessions:noOp, renderNewDevice:noOp,
  pairSyncFromState:noOp, pairRenderFound:noOp, pairRenderStepsInto:noOp, pairRenderQr:noOp,
  pairCodeInput:()=>null,
  pairCodeInputs:()=>[], pairSegInputs:()=>[], pairCodeValue:()=> '123456',
  GetState:()=>Promise.resolve(backend),
  toast(message){throw new Error('unexpected UI error: '+message);},
  setTimeout:()=>1, clearTimeout:noOp
});
function loadFunction(name, nextName) {
  const start = source.indexOf('  function '+name+'(');
  const end = source.indexOf('  function '+nextName+'(', start);
  assert(start >= 0 && end > start, name);
  vm.runInContext(source.slice(start,end),ctx);
}
loadFunction('pairFailText','pairShowFail');
loadFunction('pairShowFail','pairClearFail');
loadFunction('pairClearFail','pairSetBusy');
loadFunction('pairDirectAction','pairRenderAction');
loadFunction('pairRenderAction','pairSetBusy');
loadFunction('pairSetBusy','pairRenderFound');
loadFunction('pairRenderForm','renderPairSuccess');
loadFunction('renderPairStatus','closePairModal');
loadFunction('closePairModal','openPairModal');
const refreshStart = source.indexOf('  function refreshNow()');
const refreshEnd = source.indexOf("  el('btn-refresh').addEventListener",refreshStart);
vm.runInContext(source.slice(refreshStart,refreshEnd),ctx);
const clickStart = source.indexOf("  el('pair-go').addEventListener('click'");
const clickEnd = source.indexOf("  el('pair-swap').addEventListener",clickStart);
vm.runInContext(source.slice(clickStart,clickEnd),ctx);
const flush = () => new Promise(resolve=>setImmediate(resolve));
async function refresh() { ctx.refreshNow(); await flush(); }
function deferred() { let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b;});return {promise,resolve,reject}; }
function setRPC(pending) { ctx.window.PairConnect=()=>pending.promise;ctx.PairConnect=ctx.window.PairConnect; }

(async()=>{
  ctx.pairSetBusy(true);
  await refresh();
  assert.strictEqual(ctx.pairBusy,false,'first failure should clear busy');
  assert.strictEqual(el('pair-go').disabled,false);
  assert.strictEqual(el('pair-fail').textContent, PairUI.errText('internal'), 'inline failure uses the two-line copy');
  ctx.pairSetBusy(true);
  await refresh();
  assert.strictEqual(ctx.pairBusy,false,'identical second failure must clear local busy even when the snapshot JSON is unchanged');
  assert.strictEqual(el('pair-go').textContent,'重新配对');
  assert.strictEqual(deviceRenders,1,'device rendering should still use snapshot diff');
  ctx.pairClearFail();ctx.pairSetBusy(false);
  await refresh();
  assert.strictEqual(ctx.pairFailed,false,'editing after failure must not repaint the same old error every poll');

  const first = deferred();setRPC(first);
  el('pair-go').listeners.click();
  assert.strictEqual(ctx.pairBusy,true);
  await refresh();
  assert.strictEqual(ctx.pairBusy,true,'old failed snapshot must not unlock an RPC that has not been acknowledged');
  first.resolve();await flush();
  assert.strictEqual(ctx.pairBusy,false,'fast identical failure after RPC acknowledgement should unlock retry');

  const old = deferred();setRPC(old);
  el('pair-go').listeners.click();
  ctx.closePairModal();
  ctx.pairOpen=true;
  ctx.pairCtx={key:'192.0.2.20:40000',ip:'192.0.2.20',pending:[{}],manual:false};
  const current = deferred();setRPC(current);
  el('pair-go').listeners.click();
  old.resolve();await flush();
  assert.strictEqual(ctx.pairBusy,true,'an old modal RPC completion must not unlock a new submission');
  current.resolve();await flush();
  assert.strictEqual(ctx.pairBusy,false);

  // A GetState started before a new submission can finish after its RPC ack.
  const oldState = deferred();
  ctx.GetState=()=>oldState.promise;
  ctx.refreshNow();
  ctx.GetState=()=>Promise.resolve(backend);
  const next = deferred();setRPC(next);
  el('pair-go').listeners.click();
  const failedSnapshot = backend;
  backend={pending:[],pairStatus:{phase:'pairing'}};
  next.resolve();await flush();
  assert.strictEqual(ctx.pairBusy,true);
  oldState.resolve(failedSnapshot);await flush();
  assert.strictEqual(ctx.pairBusy,true,'a stale pre-submission poll must not unlock the current pairing');
  backend=failedSnapshot;await refresh();
  assert.strictEqual(ctx.pairBusy,false,'the current terminal result must still unlock an identical failure');
  assert(PairUI.errText('runtime-missing').includes('完整'),'missing runtime should explain how to recover');
  console.log('pair retry, identical failures, pending RPC and modal generation passed');
})().catch(error=>{console.error(error);process.exitCode=1;});
