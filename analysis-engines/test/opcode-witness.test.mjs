import test from 'node:test';
import assert from 'node:assert/strict';
import {buildOpcodeWitness,callTraceShape,deriveCreate2Address} from '../opcode-witness.mjs';
const A='0x'+'1'.repeat(40),B='0x'+'2'.repeat(40),P='0x'+'0'.repeat(39)+'1',txHash='0x'+'a'.repeat(64);
const hex=v=>BigInt(v).toString(16).padStart(64,'0');
const callStack=(to,{kind='CALL',size=0n,out=0n}={})=>[hex(out),hex(0),hex(size),hex(0),...(kind==='CALL'?[hex(0)]:[]),hex(to),hex(100000)];
const log=(pc,op,depth,stack=[])=>({pc,op,depth,stack});
test('a newly created runtime is available at the subsequent call boundary',()=>{
 const target='0x'+'3'.repeat(40),initcode='0x600060005360016000f3';
 const root={type:'CALL',from:B,to:A,input:'0x',output:'0x',calls:[
  {type:'CREATE',from:A,to:target,input:initcode,output:'0x00',value:'0x0'},
  {type:'CALL',from:A,to:target,input:'0x',output:'0x',value:'0x0'}]};
 const trace={failed:false,structLogs:[log(0,'MSTORE',1,[hex(initcode),hex(0)]),log(1,'CREATE',1,[hex(10),hex(22),hex(0)]),
  log(0,'PUSH1',2),log(4,'MSTORE8',2,[hex(0),hex(0)]),log(9,'RETURN',2,[hex(1),hex(0)]),
  log(2,'CALL',1,callStack(target)),log(0,'STOP',2),log(3,'STOP',1)]};
 const proof=buildOpcodeWitness(trace,root,{txHash,prestate:{[A]:{code:'0x52f0f100'},[target]:{code:'0x'}}});
 assert.equal(proof.complete,true,JSON.stringify(proof));assert.equal(proof.calls.length,2);assert.equal(proof.calls[1].verified,true);
 const bad=structuredClone(root);bad.calls[0].output='0x01';assert.equal(buildOpcodeWitness(trace,bad,{txHash,prestate:{[A]:{code:'0x52f0f100'}}}).complete,false);
});
test('an ancestor revert removes the runtime installed by a nested creation',()=>{
 const target='0x'+'3'.repeat(40),initcode='0x605b60005360016000f3';
 const root={type:'CALL',from:B,to:A,input:'0x',output:'0x',calls:[{type:'CALL',from:A,to:B,input:'0x',output:'0x',value:'0x0',error:'execution reverted',calls:[{type:'CREATE',from:B,to:target,input:initcode,output:'0x5b',value:'0x0'}]}]};
 const trace={failed:false,structLogs:[log(0,'CALL',1,callStack(B)),
  log(0,'MSTORE',2,[hex(initcode),hex(0)]),log(1,'CREATE',2,[hex(10),hex(22),hex(0)]),
  log(0,'PUSH1',3),log(4,'MSTORE8',3,[hex(0x5b),hex(0)]),log(9,'RETURN',3,[hex(1),hex(0)]),
  log(2,'REVERT',2,[hex(0),hex(0)]),log(1,'EXTCODECOPY',1,[hex(1),hex(0),hex(0),hex(target)]),
  log(2,'MLOAD',1,[hex(0)]),log(3,'STOP',1,[hex(0)])]};
 const proof=buildOpcodeWitness(trace,root,{txHash,prestate:{[A]:{code:'0xf13c5100'},[B]:{code:'0x52f0fd'},[target]:{code:'0x'}}});
 assert.equal(proof.complete,true,JSON.stringify(proof));assert.equal(proof.calls[1].settled,false);assert.equal(proof.memory_effects.at(-1).value_raw,'0');
});
test('CREATE2 binds the salt, byte memory and independently derived destination',()=>{
 const creator='0x'+'0'.repeat(40),target='0x4d1a2e2bb4f88f0250f26ffff098b0b30b26bf38';
 assert.equal(deriveCreate2Address(creator,0n,Buffer.from('00','hex')),target);
 const root={type:'CALL',from:B,to:creator,input:'0x',output:'0x',calls:[{type:'CREATE2',from:creator,to:target,input:'0x00',output:'0x',value:'0x0'}]};
 const trace={failed:false,structLogs:[log(0,'CREATE2',1,[hex(0),hex(1),hex(0),hex(0)]),log(0,'STOP',2),log(1,'STOP',1)]},options={txHash,prestate:{[creator]:{code:'0xf500'}}};
 const proof=buildOpcodeWitness(trace,root,options);assert.equal(proof.complete,true,JSON.stringify(proof));assert.equal(proof.calls[0].create2_salt_raw,'0');assert.equal(proof.calls[0].derived_create2_address,target);assert.equal(proof.calls[0].execution_opcode_identity_verified,true);
 const bad=structuredClone(trace);bad.structLogs[0].stack[0]=hex(1);assert.equal(buildOpcodeWitness(bad,root,options).complete,false);
 const wrong=structuredClone(root);wrong.calls[0].to=A;assert.equal(buildOpcodeWitness(trace,wrong,options).complete,false);
});

test('opcode witness binds repeated calls, return memory and a non-entered precompile',()=>{
 const input='0x'+hex(7),returned='0x'+hex(19),output='0x'+hex(29);
 const root={type:'CALL',from:B,to:A,input:'0x',output:'0x',calls:[
  {type:'CALL',from:A,to:B,input,output:returned,value:'0x0'},
  {type:'CALL',from:A,to:P,input:returned,output,value:'0x0'}]};
 const trace={failed:false,structLogs:[log(0,'MSTORE',1,[hex(7),hex(0)]),log(1,'CALL',1,callStack(B,{size:32n,out:32n})),
  log(0,'MSTORE',2,[hex(19),hex(0)]),log(1,'RETURN',2,[hex(32),hex(0)]),log(2,'CALL',1,callStack(P,{size:32n,out:32n})),log(3,'STOP',1)]};
 const options={txHash,prestate:{[A]:{code:'0x52f1f100'},[B]:{code:'0x52f3'}}};
 const witness=buildOpcodeWitness(trace,root,options);assert.equal(witness.complete,true,JSON.stringify(witness));
 assert.deepEqual(witness.calls.map(c=>[c.pc,c.trace_path,c.input,c.verified]),[[1,'0.0',input,true],[2,'0.1',returned,true]]);
 const wrong=structuredClone(root);wrong.calls[1].input=input;assert.equal(buildOpcodeWitness(trace,wrong,options).calls_complete,false);
 const badPC=structuredClone(trace);badPC.structLogs[1].pc=0;assert.equal(buildOpcodeWitness(badPC,root,options).calls_complete,false);
 assert.equal(buildOpcodeWitness(trace,root,{...options,maxSteps:2}).calls_complete,false);
 const truncated=structuredClone(trace);truncated.structLogs.pop();assert.equal(buildOpcodeWitness(truncated,root,options).complete,false);
 const wrongReturn=structuredClone(trace);wrongReturn.structLogs[2].stack[0]=hex(20);assert.equal(buildOpcodeWitness(wrongReturn,root,options).complete,false);
});

test('storage writes in a reverted child roll back before the next call',()=>{
 const root={type:'CALL',from:B,to:A,input:'0x',output:'0x',calls:[
  {type:'CALL',from:A,to:B,input:'0x',output:'0x',value:'0x0',error:'execution reverted'},
  {type:'STATICCALL',from:A,to:B,input:'0x',output:'0x'}]};
 const trace={failed:false,structLogs:[log(0,'CALL',1,callStack(B)),log(0,'JUMPDEST',2),log(1,'SSTORE',2,[hex(2),hex(0)]),log(4,'REVERT',2,[hex(0),hex(0)]),
  log(1,'STATICCALL',1,callStack(B,{kind:'STATICCALL'})),log(0,'JUMPDEST',2),log(2,'SLOAD',2,[hex(0)]),log(3,'STOP',2,[hex(1)]),log(2,'STOP',1)]};
 const w=buildOpcodeWitness(trace,root,{txHash,prestate:{[A]:{code:'0xf1fa00'},[B]:{code:'0x5b555400fd',storage:{'0x0':'0x1'}}}});
 assert.equal(w.complete,true,JSON.stringify(w));assert.equal(w.storage_effects[0].settled,false);assert.equal(w.storage_effects[1].value_raw,'1');
 assert.equal(w.final_storage[B+':0x'+hex(0)],'1');
});

test('delegate calls retain caller storage and missing call-tree frames never bind',()=>{
 const root={type:'CALL',from:B,to:A,input:'0x',output:'0x',value:'0x17',calls:[{type:'DELEGATECALL',from:A,to:B,input:'0x',output:'0x'}]};
 const trace={failed:false,structLogs:[log(0,'DELEGATECALL',1,callStack(B,{kind:'DELEGATECALL'})),log(0,'SSTORE',2,[hex(2),hex(0)]),log(1,'STOP',2),log(1,'STOP',1)]};
 const options={txHash,prestate:{[A]:{code:'0xf400',storage:{'0x0':'0x1'}},[B]:{code:'0x5500'}}};
 const w=buildOpcodeWitness(trace,root,options);assert.equal(w.complete,true,JSON.stringify(w));assert.equal(w.storage_effects[0].storage_address,A);
 assert.equal(w.calls[0].value_raw,'23');assert.equal(w.calls[0].effective_caller,B);
 assert.equal(buildOpcodeWitness(trace,{...root,calls:[]},options).complete,false);
 const shape=callTraceShape({...root,value:'0x01'});assert.equal(shape.value,'1');assert.equal(shape.calls[0].type,'DELEGATECALL');
});
