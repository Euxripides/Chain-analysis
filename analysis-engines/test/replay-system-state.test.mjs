import test from'node:test';import assert from'node:assert/strict';
import{prepareReplayAccounts,BEACON_ADDRESS,HISTORY_ADDRESS,SYSTEM_ADDRESS,WITHDRAWAL_ADDRESS,CONSOLIDATION_ADDRESS}from'../replay-system-state.mjs';
const hash='0x'+'1'.repeat(64),parent={hash,number:'0x9'},header={parentHash:hash,number:'0xa',timestamp:'0x64'},word=n=>'0x'+BigInt(n).toString(16).padStart(64,'0');
function fixture(){const accounts={};for(const[a,slots]of[[BEACON_ADDRESS,[100,8291]],[HISTORY_ADDRESS,[9]],[SYSTEM_ADDRESS,[]],[WITHDRAWAL_ADDRESS,[0,1,2,3]],[CONSOLIDATION_ADDRESS,[0,1,2,3]]])accounts[a]={balance:'0x0',nonce:'0x1',code:'0x00',storage:Object.fromEntries(slots.map(s=>[word(s),word(0)]))};return{schema_version:'contract-block-system-prestate-v1',execution_rules:'osaka',required:true,complete:true,parent_block_hash:hash,parent_block_number:'0x9',accounts};}
test('protocol parent observations fill only missing words and preserve transaction values',()=>{
 const support=fixture(),prestate={[BEACON_ADDRESS]:{balance:'0x7b',code:'0x6000',storage:{[word(100)]:word(123)}}},original=structuredClone(prestate);
 const p=prepareReplayAccounts(prestate,support,parent,header,'osaka');assert.deepEqual(p.gaps,[]);assert.equal(p.accounts[BEACON_ADDRESS].balance,'0x7b');assert.equal(p.accounts[BEACON_ADDRESS].code,'0x6000');assert.equal(p.accounts[BEACON_ADDRESS].storage[word(100)],word(123));assert.equal(p.accounts[BEACON_ADDRESS].storage[word(8291)],word(0));assert.deepEqual(prestate,original);
});
test('missing, incomplete, wrong-parent, wrong-rule and injected support cannot close replay',()=>{
 for(const mode of['missing','incomplete','parent','rule','account','injected','slot','queue']){
  const s=fixture();if(mode==='incomplete')s.complete=false;if(mode==='parent')s.parent_block_hash='0x'+'2'.repeat(64);if(mode==='rule')s.execution_rules='prague';if(mode==='account')delete s.accounts[HISTORY_ADDRESS];if(mode==='injected')s.accounts['0x'+'2'.repeat(40)]=s.accounts[HISTORY_ADDRESS];if(mode==='slot')delete s.accounts[BEACON_ADDRESS].storage[word(100)];if(mode==='queue')s.accounts[WITHDRAWAL_ADDRESS].storage[word(3)]=word(1000);
  assert.notDeepEqual(prepareReplayAccounts({},mode==='missing'?undefined:s,parent,header,'osaka').gaps,[],mode);
 }
 const malformed={['0x'+'2'.repeat(40)]:{code:'0x00'}};
 assert.notDeepEqual(prepareReplayAccounts(malformed,fixture(),parent,header,'osaka').gaps,[]);
 assert.deepEqual(prepareReplayAccounts({},undefined,parent,header,'shanghai').gaps,[]);
});
