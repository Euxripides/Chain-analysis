import {createRequire} from 'node:module';
import {id,hash} from './model.mjs';
const require=createRequire(import.meta.url),{keccak_256}=require('@noble/hashes/sha3.js');
const MOD=1n<<256n;
const OPS={CALL:0xf1,CALLCODE:0xf2,DELEGATECALL:0xf4,STATICCALL:0xfa,CREATE:0xf0,CREATE2:0xf5,SELFDESTRUCT:0xff,
 MLOAD:0x51,MSTORE:0x52,MSTORE8:0x53,SLOAD:0x54,SSTORE:0x55,TLOAD:0x5c,TSTORE:0x5d,MCOPY:0x5e,CALLDATACOPY:0x37,CODECOPY:0x39,EXTCODECOPY:0x3c,RETURNDATACOPY:0x3e,
 STOP:0x00,RETURN:0xf3,REVERT:0xfd,INVALID:0xfe};
const CALLS=new Set(['CALL','CALLCODE','DELEGATECALL','STATICCALL']);
const bytes=value=>{if(typeof value!=='string'||!/^0x(?:[a-fA-F0-9]{2})*$/.test(value))throw new Error('OPCODE_WITNESS_INVALID_BYTES');return Buffer.from(value.slice(2),'hex');};
const integer=value=>value===undefined||value===''?0n:BigInt(value);
const word=(log,index)=>{const v=log.stack?.at(-index);if(typeof v!=='string'||! /^(?:0x)?[0-9a-fA-F]{1,64}$/.test(v))throw new Error('OPCODE_WITNESS_STACK_UNAVAILABLE');return BigInt(v.startsWith('0x')?v:'0x'+v);};
const address=value=>'0x'+(value&((1n<<160n)-1n)).toString(16).padStart(40,'0');
const digest=code=>'0x'+Buffer.from(keccak_256(code)).toString('hex');
const slotWord=value=>'0x'+value.toString(16).padStart(64,'0');
export function deriveCreate2Address(creator,salt,input){
 if(!/^0x[0-9a-fA-F]{40}$/.test(creator)||BigInt(salt)<0n||BigInt(salt)>=MOD)throw new Error('CREATE2_ADDRESS_INPUT_INVALID');
 return '0x'+digest(Buffer.from('ff'+creator.slice(2)+BigInt(salt).toString(16).padStart(64,'0')+digest(input).slice(2),'hex')).slice(-40);
}

export function callTraceShape(frame) {
 return {type:frame.type?.toUpperCase(),from:frame.from?.toLowerCase(),to:frame.to?.toLowerCase(),input:(frame.input||'0x').toLowerCase(),
  output:(frame.output||'0x').toLowerCase(),value:integer(frame.value).toString(),failed:Boolean(frame.error),calls:(frame.calls||[]).map(callTraceShape)};
}

export function buildOpcodeWitness(trace,root,{txHash,prestate={},maxSteps=20000,maxMemoryBytes=65536}={}) {
 const gaps=[],memoryGaps=[],storageGaps=[],calls=[],storage=[],memory=[],accounts=new Map(Object.entries(prestate).map(([a,v])=>[a.toLowerCase(),v])),
  persistent=new Map(),transient=new Map(),deployedCode=new Map(),journal=[],frames=new Map();
 const proof={schema_version:'contract-opcode-witness-v1',witness_id:id(txHash,'opcodes'),tx_hash:txHash,
  calls,storage_effects:storage,memory_effects:memory,gaps,memory_gaps:memoryGaps,storage_gaps:storageGaps,
  calls_complete:false,storage_complete:false,memory_complete:false,complete:false,scope:'one_exact_transaction_execution'};
 const fail=reason=>{throw new Error(reason);};
 const gap=(list,reason)=>{if(!list.includes(reason))list.push(reason);};
 const stateKey=(ctx,slot)=>ctx.storageAddress+':'+slotWord(slot);
 function initialWord(ctx,slot) {
  const data=accounts.get(ctx.storageAddress)?.storage||{},key=stateKey(ctx,slot),entry=Object.entries(data).find(([s])=>integer(s)===slot);
  if(!entry){gap(storageGaps,'PRESTATE_STORAGE_WORD_UNAVAILABLE:'+key);return null;}
  return integer(entry[1]);
 }
 function currentWord(ctx,slot,temporary=false) {
  const table=temporary?transient:persistent,key=stateKey(ctx,slot);
  if(!table.has(key))table.set(key,temporary?0n:initialWord(ctx,slot));return table.get(key);
 }
 function writeWord(ctx,slot,value,temporary=false) {
  const table=temporary?transient:persistent,key=stateKey(ctx,slot),before=currentWord(ctx,slot,temporary);
  journal.push({table,key,before});table.set(key,value);return before;
 }
 function context(frame,path,parent=null) {
  const type=frame.type?.toUpperCase(),codeAddress=frame.to?.toLowerCase(),storageAddress=parent&&['DELEGATECALL','CALLCODE'].includes(type)?parent.storageAddress:codeAddress;
  const phase=['CREATE','CREATE2'].includes(type)?'creation':'runtime';
  const rawCode=phase==='creation'?frame.input:deployedCode.has(codeAddress)?deployedCode.get(codeAddress):accounts.get(codeAddress)?.code;
  const code=Buffer.isBuffer(rawCode)?rawCode:rawCode!==undefined?bytes(rawCode):null;
  return {frame,path,parent,type,codeAddress,storageAddress,phase,code,codeHash:code?digest(code):null,nextChild:0,
   effectiveCaller:parent?(type==='DELEGATECALL'?parent.effectiveCaller:parent.storageAddress):frame.from?.toLowerCase(),
   callValue:parent&&type==='DELEGATECALL'?parent.callValue:integer(frame.value),
   settled:(!parent||parent.settled)&&!frame.error,memory:new Map(),memoryUnknown:false,returnData:Buffer.alloc(0),checkpoint:journal.length};
 }
 function close(ctx) {
  if(ctx.nextChild!==(ctx.frame.calls||[]).length)fail('CALL_TREE_HAS_UNVISITED_CHILDREN:'+ctx.path);
  if(!ctx.terminal)fail('OPCODE_TRACE_MISSING_FRAME_TERMINATION:'+ctx.path);
  if(ctx.frame.error){for(let i=journal.length-1;i>=ctx.checkpoint;i--){const e=journal[i];if(e.absent)e.table.delete(e.key);else e.table.set(e.key,e.before);}journal.length=ctx.checkpoint;}
  else if(['CREATE','CREATE2'].includes(ctx.type)){
   // RETURN bytes were checked against this frame's reconstructed memory.
   // Install code at the actual creation boundary, and undo it with its parent.
   const key=ctx.codeAddress;journal.push({table:deployedCode,key,before:deployedCode.get(key),absent:!deployedCode.has(key)});
   deployedCode.set(key,bytes(ctx.frame.output||'0x'));
  }
 }
 function range(ctx,offset,length) {
  if(length===0n)return true;
  if(offset<0n||length<0n||offset+length>BigInt(maxMemoryBytes)){gap(memoryGaps,'MEMORY_RANGE_BUDGET:'+ctx.path);return false;}return true;
 }
 function readMemory(ctx,offset,length) {
  if(!range(ctx,offset,length))return null;
  const out=Buffer.alloc(Number(length));for(let i=0;i<out.length;i++){
   const b=ctx.memory.get(Number(offset)+i);if(b===null||b===undefined&&ctx.memoryUnknown)return null;out[i]=b??0;
  }return out;
 }
 function writeMemory(ctx,offset,data,length=BigInt(data.length)) {
  if(!range(ctx,offset,length)){ctx.memoryUnknown=true;return;}
  for(let i=0;i<Number(length);i++)ctx.memory.set(Number(offset)+i,data===null?null:data[i]??0);
 }
 function codeMatches(ctx,log) {
  const op=OPS[log.op];return Boolean(ctx.code)&&Number.isSafeInteger(log.pc)&&log.pc>=0&&log.pc<ctx.code.length&&ctx.code[log.pc]===op;
 }
 try {
  const logs=trace?.structLogs;
  if(!Array.isArray(logs)||typeof trace.failed!=='boolean')fail('OPCODE_TRACE_INCOMPLETE_SCHEMA');
  if(logs.length>maxSteps||trace.truncated===true)fail('OPCODE_STEP_BUDGET_EXCEEDED');
  if(!logs.length){
   if((root.calls||[]).length)fail('OPCODE_TRACE_EMPTY_WITH_CALLS');
   const rootCode=accounts.get(root.to?.toLowerCase())?.code;
   if(rootCode!==undefined&&rootCode!=='0x')fail('OPCODE_TRACE_EMPTY_WITH_CODE');
   if(rootCode===undefined)fail('OPCODE_TRACE_EMPTY_WITHOUT_CODE_IDENTITY');
   proof.calls_complete=true;proof.storage_complete=true;proof.memory_complete=true;proof.complete=true;return proof;
  }
  if(logs[0].pc!==0)fail('OPCODE_TRACE_MISSING_FRAME_PREFIX');
  const rootDepth=logs[0].depth,top=context(root,'0');frames.set(rootDepth,top);
  let previousDepth=rootDepth,pendingChild=null;
  for(let index=0;index<logs.length;index++) {
   const log=logs[index],next=logs[index+1];
   if(!Number.isInteger(log.depth)||log.depth<rootDepth)fail('OPCODE_TRACE_INVALID_DEPTH');
   if(log.depth>previousDepth){if(log.depth!==previousDepth+1||!pendingChild||log.pc!==0)fail('OPCODE_TRACE_UNMATCHED_FRAME_ENTRY');pendingChild.checkpoint=journal.length;frames.set(log.depth,pendingChild);}
   if(log.depth<previousDepth)for(let d=previousDepth;d>log.depth;d--){const ctx=frames.get(d);if(!ctx)fail('OPCODE_TRACE_FRAME_UNAVAILABLE');close(ctx);frames.delete(d);}
   previousDepth=log.depth;pendingChild=null;
   const ctx=frames.get(log.depth);if(!ctx)fail('OPCODE_TRACE_FRAME_UNAVAILABLE');
   if(ctx.terminal)fail('OPCODE_TRACE_CONTINUES_AFTER_FRAME_TERMINATION');
   if(log.error){ctx.terminal=true;continue;}
   if(['RETURN','REVERT','STOP','INVALID'].includes(log.op)){
    if(!codeMatches(ctx,log))fail('OPCODE_TERMINATION_CODE_IDENTITY_MISMATCH');
    ctx.terminal=true;
    if(['RETURN','REVERT'].includes(log.op)){
     const returned=readMemory(ctx,word(log,1),word(log,2));
     if(returned===null)gap(memoryGaps,'FRAME_RETURN_MEMORY_UNAVAILABLE:'+ctx.path);
     else if(!returned.equals(bytes(ctx.frame.output||'0x')))fail('OPCODE_FRAME_RETURN_DATA_MISMATCH:'+ctx.path);
    }else if(bytes(ctx.frame.output||'0x').length)fail('OPCODE_HALT_HAS_UNEXPECTED_RETURN_DATA:'+ctx.path);
   }
   if(CALLS.has(log.op)||['CREATE','CREATE2','SELFDESTRUCT'].includes(log.op)) {
    const child=(ctx.frame.calls||[])[ctx.nextChild];
    if(!child)fail('OPCODE_CALL_HAS_NO_CALL_TREE_FRAME:'+ctx.path+':'+log.pc);
    if(child.type?.toUpperCase()!==log.op)fail('OPCODE_CALL_TYPE_MISMATCH');
    if(child.from?.toLowerCase()!==ctx.storageAddress)fail('OPCODE_CALLER_CONTEXT_MISMATCH');
    const path=ctx.path+'.'+ctx.nextChild++,childCtx=context(child,path,ctx),input=bytes(child.input||'0x'),output=bytes(child.output||'0x');
    let inputMatches=true,targetMatches=true,valueMatches=true,outOffset=0n,outSize=0n;
    if(CALLS.has(log.op)) {
     const valueCall=['CALL','CALLCODE'].includes(log.op),inputOffset=word(log,valueCall?4:3),inputSize=word(log,valueCall?5:4);
     outOffset=word(log,valueCall?6:5);outSize=word(log,valueCall?7:6);
     const actual=readMemory(ctx,inputOffset,inputSize);inputMatches=actual!==null&&actual.equals(input);
     targetMatches=address(word(log,2))===child.to?.toLowerCase();
     if(valueCall)valueMatches=word(log,3)===integer(child.value);
    }else if(log.op==='CREATE'||log.op==='CREATE2'){
     const actual=readMemory(ctx,word(log,2),word(log,3));inputMatches=actual!==null&&actual.equals(input);valueMatches=word(log,1)===integer(child.value);
    }else targetMatches=address(word(log,1))===child.to?.toLowerCase();
    if(!targetMatches||!valueMatches)fail('OPCODE_CALL_STACK_IDENTITY_MISMATCH');
    const opcodeMatches=codeMatches(ctx,log);
    const row={opcode_call_id:id(txHash,path,'opcode-call'),trace_path:path,parent_trace_path:ctx.path,instruction_order:index,pc:log.pc,call_type:log.op,
     caller_code_address:ctx.codeAddress,caller_storage_address:ctx.storageAddress,caller_code_hash:ctx.codeHash,caller_code_phase:ctx.phase,
     target:child.to?.toLowerCase(),input:'0x'+input.toString('hex'),return_data:'0x'+output.toString('hex'),value_raw:childCtx.callValue.toString(),effective_caller:childCtx.effectiveCaller,
     input_memory_matches:inputMatches,bytecode_opcode_matches:opcodeMatches,settled:childCtx.settled,
     execution_opcode_identity_verified:inputMatches&&opcodeMatches,
     verified:inputMatches&&opcodeMatches&&ctx.phase==='runtime'};
    if(log.op==='CREATE2'){
     row.create2_salt_raw=word(log,4).toString();
     row.derived_create2_address=deriveCreate2Address(ctx.storageAddress,word(log,4),input);
     if(row.derived_create2_address!==row.target)fail('OPCODE_CREATE2_DERIVED_ADDRESS_MISMATCH');
    }
    calls.push(row);if(!row.verified)gap(gaps,'CALL_PC_OR_INPUT_NOT_VERIFIED:'+path);
    if(log.op==='SELFDESTRUCT')ctx.terminal=true;
    ctx.returnData=output;
    if(CALLS.has(log.op))writeMemory(ctx,outOffset,output,BigInt(output.length)<outSize?BigInt(output.length):outSize);
    if(next?.depth===log.depth+1)pendingChild=childCtx;
    else if((child.calls||[]).length)fail('UNENTERED_FRAME_HAS_NESTED_CALLS');
    continue;
   }
   if(['SLOAD','SSTORE','TLOAD','TSTORE'].includes(log.op)) {
    const slot=word(log,1),temporary=log.op[0]==='T',before=currentWord(ctx,slot,temporary),store=log.op.endsWith('STORE');
    let value;
    if(store)value=word(log,2);else{
     if(!next||next.depth!==log.depth)fail('OPCODE_STORAGE_READ_RESULT_UNAVAILABLE');value=word(next,1);
     if(before!==null&&before!==value)fail('OPCODE_STORAGE_READ_MISMATCH');
    }
    if(!codeMatches(ctx,log))gap(storageGaps,'STORAGE_OPCODE_CODE_IDENTITY_UNAVAILABLE:'+ctx.path);
    if(store)writeWord(ctx,slot,value,temporary);
    storage.push({effect_id:id(txHash,ctx.path,index,'storage'),trace_path:ctx.path,instruction_order:index,pc:log.pc,opcode:log.op,
     code_address:ctx.codeAddress,code_hash:ctx.codeHash,storage_address:ctx.storageAddress,slot:slotWord(slot),before_raw:before?.toString()??null,value_raw:value.toString(),
     settled:ctx.settled,scope:temporary?'transaction_transient_storage':'persistent_storage'});continue;
   }
   if(['MLOAD','MSTORE','MSTORE8','MCOPY','CALLDATACOPY','CODECOPY','EXTCODECOPY','RETURNDATACOPY'].includes(log.op)) {
    if(!codeMatches(ctx,log))gap(memoryGaps,'MEMORY_OPCODE_CODE_IDENTITY_UNAVAILABLE:'+ctx.path);
    const effect={effect_id:id(txHash,ctx.path,index,'memory'),trace_path:ctx.path,instruction_order:index,pc:log.pc,opcode:log.op,code_address:ctx.codeAddress,code_hash:ctx.codeHash,scope:'one_evm_call_frame'};
    if(log.op==='MLOAD'){
     const offset=word(log,1),actual=readMemory(ctx,offset,32n);if(!next||next.depth!==log.depth)fail('OPCODE_MEMORY_READ_RESULT_UNAVAILABLE');
     const value=word(next,1);if(actual!==null&&integer('0x'+actual.toString('hex'))!==value)fail('OPCODE_MEMORY_READ_MISMATCH');
     if(actual===null)gap(memoryGaps,'MEMORY_READ_NOT_RECONSTRUCTED:'+ctx.path);Object.assign(effect,{offset_raw:offset.toString(),value_raw:value.toString(),reconstructed:actual!==null});
    }else if(['MSTORE','MSTORE8'].includes(log.op)){
     const offset=word(log,1),value=word(log,2),data=log.op==='MSTORE8'?Buffer.from([Number(value&255n)]):bytes(slotWord(value));
     writeMemory(ctx,offset,data);Object.assign(effect,{offset_raw:offset.toString(),value_raw:value.toString(),byte_length:data.length});
    }else{
     const ext=log.op==='EXTCODECOPY',dest=word(log,ext?2:1),source=word(log,ext?3:2),length=word(log,ext?4:3);let data=null;
     if(range(ctx,dest,length)&&length<=BigInt(maxMemoryBytes)){
      if(log.op==='MCOPY')data=readMemory(ctx,source,length);
      else{
       const raw=log.op==='CALLDATACOPY'?bytes(ctx.frame.input||'0x'):log.op==='CODECOPY'?ctx.code:log.op==='RETURNDATACOPY'?ctx.returnData:
        deployedCode.has(address(word(log,1)))?deployedCode.get(address(word(log,1))):
        accounts.get(address(word(log,1)))?.code!==undefined?bytes(accounts.get(address(word(log,1))).code):null;
       if(raw){data=Buffer.alloc(Number(length));for(let i=0;i<data.length;i++)data[i]=source+BigInt(i)<BigInt(raw.length)?raw[Number(source)+i]:0;}
      }
     }
     if(data===null)gap(memoryGaps,'MEMORY_COPY_SOURCE_UNAVAILABLE:'+ctx.path);writeMemory(ctx,dest,data,length);
     Object.assign(effect,{destination_raw:dest.toString(),source_raw:source.toString(),byte_length_raw:length.toString(),reconstructed:data!==null});
    }memory.push(effect);
   }
  }
  for(let d=previousDepth;d>=rootDepth;d--)if(frames.has(d))close(frames.get(d));
  if(trace.failed!==Boolean(root.error))fail('OPCODE_TRACE_ROOT_STATUS_MISMATCH');
  proof.calls_complete=gaps.length===0;proof.storage_complete=storageGaps.length===0;proof.memory_complete=memoryGaps.length===0;
  proof.complete=proof.calls_complete&&proof.storage_complete&&proof.memory_complete;
  proof.steps=logs.length;proof.final_storage=Object.fromEntries([...persistent].map(([k,v])=>[k,v?.toString()??null]));
 }catch(error){gap(gaps,String(error.message));proof.complete=false;proof.calls_complete=false;proof.storage_complete=false;proof.memory_complete=false;}
 proof.raw_trace_sha256=hash(JSON.stringify(trace));return proof;
}
