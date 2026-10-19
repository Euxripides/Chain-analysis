import { id, hash, unique } from '../model.mjs';

const WORD=1n<<256n, MAX=WORD-1n;
const freeze=value=>{if(value&&typeof value==='object'){for(const v of Object.values(value))freeze(v);Object.freeze(value);}return value;};
const constant=value=>typeof value==='bigint'?value:typeof value==='number'&&Number.isSafeInteger(value)?BigInt(value):
 typeof value==='string'&&/^(?:0x[\da-f]+|-?\d+)$/i.test(value)?BigInt(value):null;
const offset=value=>{const n=constant(value);if(n!==null&&(n<0n||n>MAX))throw new MemoryFault('MEMORY_WORD_OUT_OF_RANGE');
 if(typeof value==='number'&&!Number.isSafeInteger(value))throw new MemoryFault('MEMORY_UNSAFE_NUMBER');return n;};
const range=(start,length)=>{const n=offset(length);if(n===0n)return{start:null,length:0n};const at=offset(start);
 if(at!==null&&n!==null&&at+n>WORD)throw new MemoryFault('MEMORY_ADDRESS_OVERFLOW');return{start:at,length:n};};
const cell=(value,metadata={})=>freeze({kind:'constant',value,...metadata});
const unknown=(reason,metadata={})=>freeze({kind:'unknown',reason,...metadata});
const semanticByte=b=>b.kind==='constant'?['constant',b.value]:b.kind==='word_byte'?['word_byte',b.expression,b.byte_index]:
 b.kind==='source_byte'?['source_byte',b.source_id,b.source_offset]:['unknown',b.reason];
const sameBytes=(a,b)=>a.length===b.length&&a.every((v,i)=>hash(semanticByte(v))===hash(semanticByte(b[i])));
const records=rows=>[...new Map(rows.map(r=>[r.memory_version_id,r])).values()];
const wordValue=value=>{if(typeof value==='number'&&!Number.isSafeInteger(value))throw new MemoryFault('MEMORY_UNSAFE_NUMBER');
 const n=constant(value);if(n!==null)return{constant:(n%WORD+WORD)%WORD};
 const expression=typeof value==='string'?value:value?.expression;
 if(typeof expression!=='string'||!expression.trim())throw new MemoryFault('MEMORY_VALUE_EXPRESSION_REQUIRED');
 return{expression,evidence_ids:value?.evidence_ids||[]};};

export class MemoryFault extends Error { constructor(code){super(code);this.code=code;} }

export function memorySource(kind,{sourceId,frameId,bytes,length,evidence_ids=[]}={}) {
 if(!['calldata','returndata','code','external_code'].includes(kind)||!sourceId||!frameId)throw new MemoryFault('MEMORY_SOURCE_IDENTITY_REQUIRED');
 if(bytes!==undefined&&(!Number.isSafeInteger(bytes.length)||bytes.length>1<<20))throw new MemoryFault('MEMORY_SOURCE_BYTE_BUDGET');
 const data=bytes===undefined?null:Array.from(bytes);
 if(data?.some(v=>!Number.isInteger(v)||v<0||v>255))throw new MemoryFault('MEMORY_INVALID_SOURCE_BYTES');
 const size=data?BigInt(data.length):length===undefined?null:offset(length);
 if(data&&length!==undefined&&offset(length)!==size)throw new MemoryFault('MEMORY_SOURCE_LENGTH_MISMATCH');
 return freeze({kind,source_id:sourceId,frame_id:frameId,bytes:data,length:size===null?null:String(size),evidence_ids:[...evidence_ids]});
}

// Immutable byte versions. Unwritten EVM frame memory is zero; memory at a
// Solidity-to-assembly boundary is unknown unless its earlier writes are given.
export class ByteMemory {
 #ops;#branches;
 constructor({frameId,entry='frame_start',byteBudget=65536,historyBudget=512,caseBudget=16},internal={}) {
  if(!frameId||!['frame_start','solidity_assembly_entry'].includes(entry))throw new MemoryFault('MEMORY_FRAME_IDENTITY_REQUIRED');
  for(const n of[byteBudget,historyBudget,caseBudget])if(!Number.isSafeInteger(n)||n<1)throw new MemoryFault('MEMORY_INVALID_BUDGET');
  if(byteBudget>65536||historyBudget>4096||caseBudget>64)throw new MemoryFault('MEMORY_BUDGET_LIMIT');
  this.frame_id=frameId;this.entry=entry;this.options=freeze({frameId,entry,byteBudget,historyBudget,caseBudget});
  this.#ops=internal.ops||[];this.#branches=internal.branches||null;
  this.extent=internal.extent===undefined?(entry==='frame_start'?'0':null):internal.extent;
  this.unresolved=freeze(internal.unresolved||[]);
  this.version_id=internal.version_id||id('byte-memory-entry',frameId,entry);
  this.last_operation=internal.last_operation||null;
  this.transition_records=freeze(internal.transition_records||[this.last_operation||{memory_version_id:this.version_id,frame_id:frameId,kind:'entry',entry}]);Object.freeze(this);
 }
 #append(operation,extent=this.extent) {
  let op=freeze(structuredClone(operation)),ops=[...this.#ops,op];
  if(ops.length>this.options.historyBudget){op=freeze({kind:'invalidate',offset:null,length:null,reason:'MEMORY_HISTORY_BUDGET',context:{...op.context,discarded_operation_kind:op.kind}});ops=[op];}
  const version_id=id('byte-memory',this.version_id,op);
  const unresolved=op.kind==='frontier'?unique([...this.unresolved,op.reason]):this.unresolved;
  const row=freeze({memory_version_id:version_id,parent_version_id:this.version_id,frame_id:this.frame_id,...op});
  return new ByteMemory(this.options,{ops,extent,version_id,unresolved,last_operation:row,transition_records:[...(this.last_operation?[]:this.transition_records),row]});
 }
 #withRecords(additional) { return new ByteMemory(this.options,{ops:this.#ops,branches:this.#branches,extent:this.extent,version_id:this.version_id,
  unresolved:this.unresolved,last_operation:this.last_operation,transition_records:records([...additional,...this.transition_records])}); }
 #map(fn) { return this.#branches?joinMemory(this.#branches.map(b=>({...b,memory:fn(b.memory)}))):fn(this); }
 #expanded(start,length) {
  if(length===0n)return this.extent;
  if(start===null||length===null||this.extent===null)return null;
  const end=((start+length+31n)/32n)*32n;return String(end>BigInt(this.extent)?end:BigInt(this.extent));
 }
 expand(start,length,context={}) {
  return this.#map(m=>{const r=range(start,length);if(r.length===0n)return m;
   const extent=m.#expanded(r.start,r.length);if(extent===m.extent)return m;
   return m.#append({kind:'expand',offset:r.start===null?null:String(r.start),length:r.length===null?null:String(r.length),context},extent);});
 }
 invalidate(start,length,reason,context={}) {
  return this.#map(m=>{const r=range(start,length);if(r.length===0n)return m;
   return m.#append({kind:'invalidate',offset:r.start===null?null:String(r.start),length:r.length===null?null:String(r.length),reason,context},m.#expanded(r.start,r.length));});
 }
 writeBytes(start,bytes,context={}) {
  if(!Number.isSafeInteger(bytes?.length)||bytes.length<0)throw new MemoryFault('MEMORY_INVALID_BYTE_SEQUENCE');
  if(bytes.length>this.options.byteBudget)return this.invalidate(start,bytes.length,'MEMORY_BYTE_BUDGET',context);
  const values=Array.from(bytes,b=>typeof b==='number'?cell(b):freeze(structuredClone(b)));
  if(values.some(b=>!['constant','word_byte','source_byte','unknown'].includes(b.kind)||b.kind==='constant'&&(!Number.isInteger(b.value)||b.value<0||b.value>255)))throw new MemoryFault('MEMORY_INVALID_BYTE');
  return this.#map(m=>{const r=range(start,values.length);if(!values.length)return m;
   if(r.start===null)return m.invalidate(null,null,'MEMORY_SYMBOLIC_WRITE_ADDRESS',context);
   if(values.length>m.options.byteBudget)return m.invalidate(start,values.length,'MEMORY_BYTE_BUDGET',context);
   return m.#append({kind:'write',offset:String(r.start),length:String(values.length),bytes:values,context},m.#expanded(r.start,r.length));});
 }
 mstore(start,value,context={}) {
  const v=wordValue(value);
  return this.writeBytes(start,Array.from({length:32},(_,i)=>v.constant===undefined?{kind:'word_byte',expression:v.expression,byte_index:i,evidence_ids:v.evidence_ids}:
   cell(Number((v.constant>>BigInt((31-i)*8))&255n))),{...context,opcode:'MSTORE'});
 }
 mstore8(start,value,context={}) {
  const v=wordValue(value);
  return this.writeBytes(start,[v.constant===undefined?{kind:'word_byte',expression:v.expression,byte_index:31,evidence_ids:v.evidence_ids}:cell(Number(v.constant&255n))],{...context,opcode:'MSTORE8'});
 }
 #byte(at) {
  for(let i=this.#ops.length-1;i>=0;i--){const op=this.#ops[i];if(['expand','frontier'].includes(op.kind))continue;
   if(op.offset===null)return unknown(op.reason,{operation_context:op.context});
   const start=BigInt(op.offset),end=op.length===null?null:start+BigInt(op.length);
   if(at>=start&&(end===null||at<end))return op.kind==='write'?op.bytes[Number(at-start)]:unknown(op.reason,{operation_context:op.context});
  }
  return this.entry==='frame_start'?cell(0,{origin:'frame_initial_zero'}):unknown('ASSEMBLY_ENTRY_MEMORY_UNBOUND');
 }
 read(start,length) {
  const r=range(start,length);
  if(r.length===0n)return{memory_version_id:this.version_id,status:this.unresolved.length?'partial':'exact',cases:[{conditions:[],bytes:[]}],unresolved:this.unresolved};
  if(r.length===null||r.start===null)return{memory_version_id:this.version_id,status:'partial',cases:[],unresolved:['MEMORY_SYMBOLIC_READ_RANGE']};
  if(r.length>BigInt(this.options.byteBudget))return{memory_version_id:this.version_id,status:'partial',cases:[],unresolved:['MEMORY_READ_BYTE_BUDGET']};
  const reads=this.#branches?.map(b=>({branch:b,read:b.memory.read(start,length)}));
  let cases=reads?reads.flatMap(({branch:b,read})=>read.cases.map(c=>({conditions:[...b.conditions,...c.conditions],bytes:c.bytes}))):
   [{conditions:[],bytes:Array.from({length:Number(r.length)},(_,i)=>this.#byte(r.start+BigInt(i)))}];
  if(reads?.some(r=>r.read.cases.length===0))return{memory_version_id:this.version_id,status:'partial',cases:[],unresolved:unique(['MEMORY_MERGE_READ_FRONTIER',...reads.flatMap(r=>r.read.unresolved)])};
  let equivalent_cases=[];
  if(cases.length>1&&cases.every(c=>sameBytes(c.bytes,cases[0].bytes))){equivalent_cases=cases;cases=[{conditions:[],bytes:cases[0].bytes}];}
  const unresolved=unique([...this.unresolved,...(reads||[]).flatMap(r=>r.branch.memory.unresolved),...cases.flatMap(c=>[...c.bytes.filter(b=>b.kind==='unknown').map(b=>b.reason),...c.conditions.filter(c=>!c.expression&&!c.expression_id).map(()=>'MEMORY_BRANCH_CONDITION_UNAVAILABLE')])]);
  return{memory_version_id:this.version_id,status:unresolved.length?'partial':cases.length>1?'conditional':'exact',cases,equivalent_cases,unresolved};
 }
 loadWord(start,context={}) {
  const memory=this.expand(start,32,context),read=memory.read(start,32);
  return{memory,...read,cases:read.cases.map(c=>{let value=null,expression=null;
   if(c.bytes.every(b=>b.kind==='constant')){value=c.bytes.reduce((n,b)=>(n<<8n)|BigInt(b.value),0n).toString();expression=value;}
   else if(c.bytes.every((b,i)=>b.kind==='word_byte'&&b.byte_index===i&&b.expression===c.bytes[0].expression))expression=c.bytes[0].expression;
   else if(c.bytes.every(b=>b.kind!=='unknown'))expression='concat_bytes('+c.bytes.map(b=>b.kind==='constant'?b.value:b.kind==='word_byte'?`byte(${b.byte_index}, ${b.expression})`:`${b.source_id}[${b.source_offset}]`).join(', ')+')';
   return{...c,value,expression};})};
 }
 mcopy(destination,source,length,context={}) {
  return this.#map(m=>{const dst=range(destination,length),src=range(source,length);if(dst.length===0n)return m;
   const read=m.read(source,length),expanded=m.expand(source,length,context);
   if(read.cases.length!==1)return expanded.invalidate(destination,length,'MCOPY_SOURCE_RANGE_UNRESOLVED',{...context,source_memory_version:m.version_id,source_unresolved:read.unresolved}).#withRecords(expanded.transition_records);
   return expanded.writeBytes(destination,read.cases[0].bytes,{...context,opcode:'MCOPY',source_offset:src.start===null?null:String(src.start),source_memory_version:m.version_id}).#withRecords(expanded.transition_records);});
 }
 copyFrom(destination,sourceOffset,length,source,context={}) {
  if(source.frame_id!==this.frame_id)throw new MemoryFault('MEMORY_SOURCE_FRAME_MISMATCH');
  const count=offset(length),from=offset(sourceOffset),size=source.length===null?null:BigInt(source.length);
  // EIP-211 requires this check even for zero bytes one past returndata end.
  if(source.kind==='returndata'&&from!==null&&count!==null&&size!==null&&(from+count>MAX||from+count>size))throw new MemoryFault('RETURNDATA_COPY_OUT_OF_BOUNDS');
  if(source.kind==='returndata'&&(from===null||count===null||size===null))return this.#map(m=>{const invalid=m.invalidate(destination,length,'RETURNDATA_COPY_BOUNDS_UNPROVED',context);
   return invalid.#append({kind:'frontier',reason:'RETURNDATA_COPY_BOUNDS_UNPROVED',context:{...context,source_id:source.source_id,possible_exception:true}}).#withRecords(invalid.transition_records);});
  if(count===0n)return this;
  if(count===null||from===null)return this.invalidate(destination,length,'MEMORY_SOURCE_RANGE_UNRESOLVED',{...context,source_id:source.source_id});
  if(count>BigInt(this.options.byteBudget))return this.invalidate(destination,length,'MEMORY_COPY_BYTE_BUDGET',{...context,source_id:source.source_id});
  const bytes=Array.from({length:Number(count)},(_,i)=>{const at=from+BigInt(i);
   if(size!==null&&at>=size)return cell(0,{origin:source.kind+'_padding',source_id:source.source_id,source_offset:String(at)});
   return source.bytes?cell(source.bytes[Number(at)],{source_id:source.source_id,source_offset:String(at)}):
    freeze({kind:'source_byte',source_id:source.source_id,source_offset:String(at),evidence_ids:source.evidence_ids});});
  return this.writeBytes(destination,bytes,{...context,opcode:{calldata:'CALLDATACOPY',returndata:'RETURNDATACOPY',code:'CODECOPY',external_code:'EXTCODECOPY'}[source.kind],source_id:source.source_id,source_offset:String(from)});
 }
 static join(branches) {
  if(!branches.length)throw new MemoryFault('MEMORY_JOIN_EMPTY');const first=branches[0].memory;
  if(!(first instanceof ByteMemory))throw new MemoryFault('MEMORY_JOIN_FRAME_MISMATCH');
  if(branches.some(b=>!(b.memory instanceof ByteMemory)||b.memory.frame_id!==first.frame_id||b.memory.entry!==first.entry))throw new MemoryFault('MEMORY_JOIN_FRAME_MISMATCH');
  if(branches.some(b=>hash(b.memory.options)!==hash(first.options)))throw new MemoryFault('MEMORY_JOIN_BUDGET_MISMATCH');
  const flat=branches.flatMap(b=>b.memory.#branches?b.memory.#branches.map(inner=>({memory:inner.memory,conditions:[...(b.conditions||[]),...inner.conditions]})):[{memory:b.memory,conditions:b.conditions||[]}]);
  if(flat.length>first.options.caseBudget)return new ByteMemory(first.options).invalidate(null,null,'MEMORY_CONTROL_FLOW_CASE_BUDGET',{incoming_versions:flat.map(b=>b.memory.version_id)});
  const versions=flat.map(b=>({memory_version_id:b.memory.version_id,conditions:b.conditions})),extent=flat.every(b=>b.memory.extent===first.extent)?first.extent:null;
  const version_id=id('byte-memory-join',first.frame_id,versions),row=freeze({memory_version_id:version_id,kind:'join',frame_id:first.frame_id,inputs:versions});
  return new ByteMemory(first.options,{branches:flat.map(b=>({memory:b.memory,conditions:freeze(structuredClone(b.conditions))})),extent,unresolved:unique(flat.flatMap(b=>b.memory.unresolved)),version_id,last_operation:row,
   transition_records:records([...flat.flatMap(b=>b.memory.transition_records),row])});
 }
}
export const joinMemory=branches=>ByteMemory.join(branches);
