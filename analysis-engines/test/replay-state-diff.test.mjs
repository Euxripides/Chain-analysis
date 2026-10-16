import test from'node:test';import assert from'node:assert/strict';
import{normalizeTransactionDiff,compareTransactionDiffs}from'../replay-state-diff.mjs';
const A='0x'+'1'.repeat(40),B='0x'+'2'.repeat(40),word=n=>'0x'+BigInt(n).toString(16).padStart(64,'0');
const observed=()=>({[A]:{balance:'0x9',nonce:2,code:'0x6000',storage:{'0x0':'0x3','0x1':'0x0','0x2':'0x0'}},[B]:{balance:'0x0',storage:{'0x0':'0x0'}}});
const fixture=()=>({pre:{[A]:{balance:'0x9',nonce:2,code:'0x6000',storage:{[word(0)]:word(3)}}},post:{[A]:{storage:{[word(1)]:word(7)}}}});
test('transaction diff compares zero clearing and all extra storage/account effects',()=>{
 const expected=fixture(),same=structuredClone(expected);same.post[A].storage={'0x1':'0x7'};
 assert.equal(compareTransactionDiffs(expected,observed(),same,observed()).matched,true);
 for(const mode of['extra_slot','extra_account','missing_clear','nonce']){
  const actual=fixture();if(mode==='extra_slot')actual.post[A].storage['0x2']='0x4';if(mode==='extra_account')actual.post[B]={balance:'0x1'};
  if(mode==='missing_clear')delete actual.pre[A].storage;if(mode==='nonce')actual.post[A].nonce=3;
  const check=compareTransactionDiffs(expected,observed(),actual,observed());assert.equal(check.complete,true,mode);assert.equal(check.matched,false,mode);assert.ok(check.differences.length,mode);
 }
});
test('missing observations, invalid shapes and conflicting hashes cannot prove a diff',()=>{
 for(const mode of['missing_map','missing_account','missing_balance','missing_slot','conflict','alias','quantity','hash','unexpected']){
  const raw=fixture(),pre=observed();if(mode==='missing_map')delete raw.pre;if(mode==='missing_account')delete pre[A];if(mode==='missing_balance')delete pre[A].balance;
  if(mode==='missing_slot')delete pre[A].storage['0x1'];if(mode==='conflict')raw.pre[A].balance='0x8';if(mode==='alias')raw.pre[A].storage['0x0']='0x3';
  if(mode==='quantity')raw.post[A].balance=-1;if(mode==='hash')raw.post[A].codeHash='0x'+'3'.repeat(64);if(mode==='unexpected')raw.post[A].unverified=true;
  assert.equal(normalizeTransactionDiff(raw,pre).complete,false,mode);
 }
});
test('new accounts, deletion and explicit empty code hash retain their full effects',()=>{
 const pre=observed(),created={pre:{},post:{[B]:{nonce:1,code:'0x6000',storage:{'0x0':'0x5'}}}};
 const birth=normalizeTransactionDiff(created,pre);assert.equal(birth.complete,true);assert.equal(birth.effects[B].nonce.after,'0x1');
 const death=normalizeTransactionDiff({pre:{[A]:{balance:'0x9',nonce:2,code:'0x6000',storage:{'0x0':'0x3'}}},post:{}},pre);
 assert.equal(death.complete,true);assert.equal(death.effects[A].account_deleted.after,false);assert.equal(death.effects[A].code.after,'0x');
 const clear=normalizeTransactionDiff({pre:{[A]:{balance:'0x9'}},post:{[A]:{codeHash:'0x'+'0'.repeat(64)}}},pre);assert.equal(clear.complete,true);assert.equal(clear.effects[A].code.after,'0x');
});
