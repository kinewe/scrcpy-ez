const assert = require('node:assert/strict');
const {model} = require('./update_ui.js');
for (const phase of ['checking','downloading','validating','installing']) {
  const m = model({phase,info:{hasNew:true},canInstall:true});
  assert.equal(m.download,false); assert.equal(m.install,false); assert.equal(m.check,false);
}
assert.equal(model({phase:'available',info:{hasNew:false}}).download,false);
assert.equal(model({phase:'canceled',info:{hasNew:true}}).download,true);
assert.equal(model({phase:'ready',canInstall:true}).install,true);
assert.equal(model({phase:'installing'}).later,false);
assert.equal(model({phase:'downloading',downloaded:50,total:100}).percent,50);
assert.equal(model({phase:'downloading',total:0}).percent,null);
assert.equal(model({phase:'validating'}).cancel,true);
assert.equal(model({phase:'downloading',downloaded:50,total:100}).animateProgress,true);
for(const input of [{phase:'selecting',downloaded:50,total:100},{phase:'validating',downloaded:50,total:100},{phase:'downloading',downloaded:0,total:100},{phase:'downloading',downloaded:100,total:100},{phase:'downloading',downloaded:101,total:100},{phase:'downloading',total:0}]) assert.equal(model(input).animateProgress,false);
console.log('update UI state cases passed');
