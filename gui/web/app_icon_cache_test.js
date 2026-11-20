'use strict';
const assert = require('node:assert/strict');
const Cache = require('./app_icon_cache.js');
function node() { return {textContent:'placeholder',style:{},classList:{add(){}}}; }
async function main() {
  let calls=[];
  const cache=Cache.create(async(serial,pkg)=>{calls.push([serial,pkg]);return 'data:'+serial;});
  const a=node(),b=node(),a2=node();
  await Promise.all([cache.request('device:A','USB_A','pkg',a),cache.request('device:B','USB_B','pkg',b),cache.request('device:A','WIFI_A','pkg',a2)]);
  assert.equal(calls.length,2); assert.notEqual(a.style.backgroundImage,b.style.backgroundImage);
  assert.deepEqual(calls, [['device:A','pkg'], ['device:B','pkg']]);
  assert.equal(a.style.backgroundImage,a2.style.backgroundImage);
  cache.invalidate('device:A','pkg');
  await cache.request('device:B','USB_B','pkg',node());assert.equal(calls.length,2);
  await cache.request('device:A','WIFI_A','pkg',node());assert.equal(calls.length,3);
  let resolve;
  const delayed=Cache.create(()=>new Promise(r=>{resolve=r;}));const stale=node();
  const old=delayed.request('device:A','USB_A','pkg',stale);
  await Promise.resolve(); delayed.invalidate('device:A','pkg');resolve('old'); await old;
  assert.equal(stale.textContent,'placeholder');
  let tries=0;
  const missing=Cache.create(()=>Promise.resolve(++tries===1?'':'new'));
  await missing.request('device:A','A','pkg',node());const retried=node();await missing.request('device:A','A','pkg',retried);
  assert.equal(retried.style.backgroundImage,'url("new")');
  const failed=Cache.create(()=>{throw Error('offline');});await failed.request('device:A','A','pkg',node());
  const batches=[];
  const batched=Cache.create(null,async(identity,pkgs)=>{
    batches.push([identity,pkgs.slice()]);
    return Object.fromEntries(pkgs.map(pkg=>[pkg,'data:'+identity+':'+pkg]));
  });
  const nodes=Array.from({length:65},node);
  await Promise.all(nodes.map((n,i)=>batched.request('device:A','WIFI_A','pkg'+i,n)).concat([
    batched.request('device:B','USB_B','pkg0',node())
  ]));
  assert.equal(batches.length,4);
  assert.ok(batches.every(b=>b[1].length<=24));
  assert.equal(nodes[64].style.backgroundImage,'url("data:device:A:pkg64")');
  const constructed=node();constructed.isConnected=false;
  assert.equal(batched.paintCached('device:A','pkg0',constructed),true);
  assert.equal(batched.paintCached('device:C','pkg0',node()),false);
  assert.equal(constructed.style.backgroundImage,nodes[0].style.backgroundImage,'cache hit must populate a new card before DOM insertion');
  const original=nodes[1].style.backgroundImage;
  await batched.refresh('device:A',['pkg0']);
  assert.equal(batches.length,5); assert.deepEqual(batches[4],['device:A',['pkg0']]);
  assert.equal(nodes[1].style.backgroundImage,original);
  let available=false;
  const arriving=Cache.create(null,async(identity,pkgs)=>available?{pkg:'ready'}:{});
  const waiting=node();
  await arriving.request('device:A','A','pkg',waiting);
  assert.equal(waiting.textContent,'placeholder');
  available=true;
  await arriving.refresh('device:A',['pkg']);
  assert.equal(waiting.style.backgroundImage,'url("ready")');
  waiting.isConnected=false;
  await arriving.refresh('device:A',['pkg']);
  // Detached nodes do not provoke another request or retain an active render target.
  const staleBatchNode=node(); let batchResolve;
  const staleBatch=Cache.create(null,()=>new Promise(resolve=>{batchResolve=resolve;}));
  const staleJob=staleBatch.request('device:A','A','pkg',staleBatchNode);
  await Promise.resolve(); await Promise.resolve();
  staleBatch.invalidate('device:A','pkg'); batchResolve({pkg:'old'}); await staleJob;
  assert.equal(staleBatchNode.textContent,'placeholder');
  let bridgeTries=0;
  const transient=Cache.create(null,async()=>{ if(++bridgeTries===1) throw Error('bridge temporarily busy');return {pkg:'recovered'}; });
  const recovered=node();await transient.request('device:A','A','pkg',recovered);
  assert.equal(bridgeTries,2);assert.equal(recovered.style.backgroundImage,'url("recovered")');
  let boundedCalls=0;
  const bounded=Cache.create(async()=>{boundedCalls++;return '12345678';},null,{maxEntries:2,maxBytes:32});
  for(const pkg of ['a','b','a','c','a']) await bounded.request('A','A',pkg,node());
  assert.equal(boundedCalls,3,'recent entries should be reused');
  await bounded.request('A','A','b',node());assert.equal(boundedCalls,4,'least recent entry must be evicted');
  const disconnected=node();disconnected.isConnected=false;let detachedCalls=0;
  const detached=Cache.create(null,async()=>{detachedCalls++;return {};});
  await detached.request('A','A','pkg',disconnected);assert.equal(detachedCalls,0,'discard detached queued requests');
  detached.prune();
  console.log('app_icon_cache: device isolation, transport reuse, invalidation, stale responses, retries passed');
}
main().catch(e=>{console.error(e);process.exitCode=1;});
