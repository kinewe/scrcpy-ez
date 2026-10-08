'use strict';
const assert = require('assert');
const fs = require('fs');
const vm = require('vm');
const source = fs.readFileSync(__dirname + '/app.js', 'utf8');
const flush = async () => { await Promise.resolve(); await Promise.resolve(); };
function deferred() { let resolve, reject; const promise = new Promise((a,b)=>{resolve=a;reject=b;}); return {promise,resolve,reject}; }

(async function () {
  const box = {checked:false, disabled:false};
  const reads = [], writes = [], errors = [];
  let handler;
  box.addEventListener = (event, fn) => { handler = fn; };
  const context = vm.createContext({
    paramState:{serial:'A'}, serial:'A', el:()=>box, toast:e=>errors.push(e),
    window:{
      GetRootRepairEnabled:s=>{const d=deferred();reads.push({s,d});return d.promise;},
      SetRootRepairEnabled:(s,value)=>{const d=deferred();writes.push({s,value,d});return d.promise;}
    }
  });
  const readStart = source.indexOf('var rootState = paramState, rootBox');
  const readEnd = source.indexOf('renderParamModal();',readStart);
  vm.runInContext(source.slice(readStart,readEnd),context);
  assert.strictEqual(box.disabled,true);
  context.paramState={serial:'B'};
  box.checked=false;
  reads[0].d.resolve(true); await flush();
  assert.strictEqual(box.checked,false,'old device read must not change new device');
  const saveStart=source.indexOf("el('param-root-repair').addEventListener('change'");
  const saveEnd=source.indexOf('function renderParamModal()',saveStart);
  vm.runInContext(source.slice(saveStart,saveEnd),context);
  box.checked=true; handler({currentTarget:box});
  assert.deepStrictEqual([writes[0].s,writes[0].value],['B',true]);
  context.paramState={serial:'C'};
  box.checked=true; box.disabled=true;
  writes[0].d.reject('old save error'); await flush();
  assert.strictEqual(box.checked,true);
  assert.strictEqual(box.disabled,true);
  assert.deepStrictEqual(errors,[]);
  box.checked=true; handler({currentTarget:box});
  writes[1].d.reject('disk unavailable'); await flush();
  assert.strictEqual(box.checked,false,'current failed save restores choice');
  assert.strictEqual(box.disabled,false);
  assert.strictEqual(errors.length,1);
  console.log('root repair preference async isolation passed');
})().catch(e=>{console.error(e);process.exit(1);});
