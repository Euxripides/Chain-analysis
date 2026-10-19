import test from 'node:test';
import assert from 'node:assert/strict';
import {ByteMemory,MemoryFault,joinMemory,memorySource} from '../ir/memory.mjs';

const fresh=(options={})=>new ByteMemory({frameId:'test-frame',...options});
const bytes=(m,at,n)=>{const r=m.read(at,n);assert.equal(r.status,'exact',r.unresolved.join(','));assert.equal(r.cases.length,1);assert.ok(r.cases[0].bytes.every(b=>b.kind==='constant'));return Buffer.from(r.cases[0].bytes.map(b=>b.value));};
const hasFault=(fn,code)=>assert.throws(fn,error=>error instanceof MemoryFault&&error.code===code);

test('frame initialization, assembly entry and later fixed writes keep distinct byte provenance',()=>{
 const zero=fresh(),entry=fresh({entry:'solidity_assembly_entry'});
 assert.equal(zero.loadWord(64).cases[0].value,'0');assert.equal(zero.extent,'0');assert.equal(zero.loadWord(64).memory.extent,'96');
 assert.deepEqual(entry.read(64,32).unresolved,['ASSEMBLY_ENTRY_MEMORY_UNBOUND']);assert.equal(entry.extent,null);
 const written=entry.mstore(65,1n,{statement:'a'});assert.equal(written.loadWord(65).cases[0].value,'1');
 const crossing=written.read(64,33);assert.equal(crossing.cases[0].bytes[0].kind,'unknown');assert.equal(crossing.cases[0].bytes[32].value,1);
 assert.notEqual(zero.version_id,entry.version_id);assert.equal(entry.read(65,32).status,'partial');
});

test('unaligned words and low-byte writes agree with an independent big-endian Buffer reference',()=>{
 let state=fresh(),reference=Buffer.alloc(128);const value=(1n<<255n)+0x123456789abcdefn;
 const raw=Buffer.from(value.toString(16).padStart(64,'0'),'hex');state=state.mstore(3,value);raw.copy(reference,3);
 state=state.mstore8(17,0x1234n);reference[17]=0x34;
 state=state.mstore8(2,-1n);reference[2]=255;
 assert.deepEqual(bytes(state,0,128),reference);assert.equal(state.loadWord(4).cases[0].value,BigInt('0x'+reference.subarray(4,36).toString('hex')).toString());
 hasFault(()=>state.mstore(0,Number.MAX_SAFE_INTEGER+1),'MEMORY_UNSAFE_NUMBER');
});

test('both overlapping MCOPY directions use the old version and expand the source range',()=>{
 const initial=fresh().writeBytes(0,Buffer.from(Array.from({length:64},(_,i)=>i)));
 for(const[dst,src,length]of[[1,0,48],[0,1,48],[7,3,41],[3,7,41],[0,0,64]]){
  const expected=Buffer.from(Array.from({length:64},(_,i)=>i)),snapshot=Buffer.from(expected.subarray(src,src+length));snapshot.copy(expected,dst);
  const copied=initial.mcopy(dst,src,length);assert.deepEqual(bytes(copied,0,64),expected);assert.equal(copied.last_operation.context.source_memory_version,initial.version_id);
 }
 assert.deepEqual(bytes(initial,0,64),Buffer.from(Array.from({length:64},(_,i)=>i)));
 const expanded=fresh().mcopy(0,96,2);assert.equal(expanded.extent,'128');assert.deepEqual(bytes(expanded,0,2),Buffer.alloc(2));
 assert.equal(initial.mcopy((1n<<256n)-1n,(1n<<256n)-1n,0),initial);
});

test('mixed stores, loads and copies match 512 deterministic independent byte operations',()=>{
 let seed=0x82fa17c9;const next=()=>{seed^=seed<<13;seed^=seed>>>17;seed^=seed<<5;return seed>>>0;};
 let state=fresh({historyBudget:2048}),expected=Buffer.alloc(256);const prior=[];
 for(let i=0;i<512;i++){
  const at=next()%160;
  if(i%3===0){let value=0n;for(let n=0;n<8;n++)value=(value<<32n)|BigInt(next());state=state.mstore(at,value);Buffer.from(value.toString(16).padStart(64,'0'),'hex').copy(expected,at);}
  else if(i%3===1){const value=next();state=state.mstore8(at,BigInt(value));expected[at]=value&255;}
  else{const from=next()%160,n=next()%56,snapshot=Buffer.from(expected.subarray(from,from+n));state=state.mcopy(at,from,n);snapshot.copy(expected,at);}
  if(i%32===0){assert.deepEqual(bytes(state,0,224),expected.subarray(0,224));prior.push([state,Buffer.from(expected)]);}
 }
 assert.deepEqual(bytes(state,0,224),expected.subarray(0,224));for(const[m,r]of prior)assert.deepEqual(bytes(m,0,224),r.subarray(0,224));
});

test('symbolic words survive exact copying and partial overwrites retain the other symbolic bytes',()=>{
 const input={expression:'amount',evidence_ids:['argument-origin']},written=fresh().mstore(13,input);input.expression='mutated';
 assert.equal(written.loadWord(13).cases[0].expression,'amount');
 const copied=written.mcopy(80,13,32);assert.equal(copied.loadWord(80).cases[0].expression,'amount');
 const changed=copied.mstore8(81,0x7f);assert.match(changed.loadWord(80).cases[0].expression,/byte\(0, amount\), 127, byte\(2, amount\)/);
 assert.deepEqual(changed.read(80,1).cases[0].bytes[0].evidence_ids,['argument-origin']);
 assert.throws(()=>written.read(13,1).cases[0].bytes[0].evidence_ids.push('bad'));
});

test('copy sources retain their identity, pad code/calldata and reject cross-frame return buffers',()=>{
 for(const kind of['calldata','code','external_code']){
  const source=memorySource(kind,{sourceId:kind+'-observed-code',frameId:'test-frame',bytes:Buffer.from('aabbcc','hex')});
  const state=fresh().copyFrom(7,1,5,source);assert.deepEqual(bytes(state,7,5),Buffer.from('bbcc000000','hex'));
  assert.equal(state.last_operation.context.source_id,source.source_id);
 }
 const symbolic=memorySource('calldata',{sourceId:'frame-calldata',frameId:'test-frame',length:2});
 const read=fresh().copyFrom(0,1,3,symbolic).read(0,3);assert.equal(read.status,'exact');assert.equal(read.cases[0].bytes[0].kind,'source_byte');assert.equal(read.cases[0].bytes[1].value,0);
 hasFault(()=>fresh().copyFrom(0,0,0,memorySource('returndata',{sourceId:'foreign-call',frameId:'other-frame',bytes:[]})),'MEMORY_SOURCE_FRAME_MISMATCH');
 hasFault(()=>memorySource('code',{sourceId:'code',frameId:'test-frame',bytes:[1],length:2}),'MEMORY_SOURCE_LENGTH_MISMATCH');
});

test('returndata zero length still checks its source boundary and unresolved bounds remain a frontier',()=>{
 const empty=memorySource('returndata',{sourceId:'last-call',frameId:'test-frame',bytes:[]}),memory=fresh();
 assert.equal(memory.copyFrom((1n<<256n)-1n,0,0,empty),memory);
 hasFault(()=>memory.copyFrom(0,1,0,empty),'RETURNDATA_COPY_OUT_OF_BOUNDS');
 hasFault(()=>memory.copyFrom(0,0,1,empty),'RETURNDATA_COPY_OUT_OF_BOUNDS');
 const unknown=memorySource('returndata',{sourceId:'unknown-call-return',frameId:'test-frame'});
 const pending=memory.copyFrom(0,1,0,unknown);assert.ok(pending.unresolved.includes('RETURNDATA_COPY_BOUNDS_UNPROVED'));assert.equal(pending.read(0,0).status,'partial');
 assert.equal(pending.loadWord(0).cases[0].value,'0');assert.equal(pending.loadWord(0).status,'partial');
});

test('unknown writes affect possible aliases while bounded invalidation and copies preserve unrelated bytes',()=>{
 const initial=fresh().mstore(0,1n).mstore(64,2n),bounded=initial.invalidate(16,1,'OPAQUE_BYTE_WRITE');
 assert.equal(bounded.loadWord(64).cases[0].value,'2');assert.equal(bounded.read(16,1).status,'partial');
 const aliased=initial.mstore({expression:'dynamic_offset'},5n);assert.equal(aliased.loadWord(64).status,'partial');
 const fixed=aliased.mstore(64,3n);assert.equal(fixed.loadWord(64).cases[0].value,'3');assert.equal(fixed.loadWord(0).status,'partial');
 const partly=fresh({entry:'solidity_assembly_entry'}).mstore8(0,33),copied=partly.mcopy(64,0,4).read(64,4);
 assert.equal(copied.cases[0].bytes[0].value,33);assert.equal(copied.cases[0].bytes[1].reason,'ASSEMBLY_ENTRY_MEMORY_UNBOUND');
});

test('control-flow joins retain correlated word alternatives and identical bytes retain both origins',()=>{
 const entry=fresh(),a=entry.mstore(0,1n,{statement:'left'}),b=entry.mstore(0,2n,{statement:'right'});
 const joined=joinMemory([{memory:a,conditions:[{expression:'allowed',expected:true}]},{memory:b,conditions:[{expression:'allowed',expected:false}]}]);
 const word=joined.loadWord(0);assert.equal(word.status,'conditional');assert.deepEqual(word.cases.map(c=>c.value),['1','2']);assert.deepEqual(word.cases.map(c=>c.conditions[0].expected),[true,false]);
 assert.deepEqual(joined.mcopy(64,0,32).loadWord(64).cases.map(c=>c.value),['1','2']);
 const identical=joinMemory([{memory:a,conditions:[{}]},{memory:entry.mstore(0,1n,{statement:'other'}),conditions:[{}]}]).read(0,32);
 assert.equal(identical.status,'exact');assert.equal(identical.equivalent_cases.length,2);
 const missing=joinMemory([{memory:a,conditions:[{}]},{memory:b,conditions:[{}]}]).loadWord(0);assert.equal(missing.status,'partial');assert.ok(missing.unresolved.includes('MEMORY_BRANCH_CONDITION_UNAVAILABLE'));
 hasFault(()=>joinMemory([{memory:a},{memory:new ByteMemory({frameId:'other-frame'})}]),'MEMORY_JOIN_FRAME_MISMATCH');
});

test('byte, history, range and case exhaustion retain explicit frontiers without invented zero bytes',()=>{
 let limited=fresh({byteBudget:32,historyBudget:2,caseBudget:2});limited=limited.mstore(0,1n).mstore(32,2n).mstore(64,3n);
 assert.ok(limited.read(0,1).unresolved.includes('MEMORY_HISTORY_BUDGET'));
 assert.equal(limited.last_operation.reason,'MEMORY_HISTORY_BUDGET');assert.equal(limited.last_operation.kind,'invalidate');
 const oversize=fresh({byteBudget:32}).writeBytes(64,Buffer.alloc(33,1));assert.ok(oversize.read(64,1).unresolved.includes('MEMORY_BYTE_BUDGET'));assert.equal(oversize.loadWord(0).cases[0].value,'0');
 const entry=fresh({caseBudget:2}),many=joinMemory([0,1,2].map(n=>({memory:entry.mstore(0,BigInt(n)),conditions:[{expression:'x=='+n}]})));
 assert.ok(many.read(0,1).unresolved.includes('MEMORY_CONTROL_FLOW_CASE_BUDGET'));
 hasFault(()=>fresh().mstore((1n<<256n)-1n,1n),'MEMORY_ADDRESS_OVERFLOW');
 hasFault(()=>fresh().mcopy(0,(1n<<256n)-1n,2),'MEMORY_ADDRESS_OVERFLOW');
});

test('each compound transition exposes all intermediate versions and branch identities for evidence storage',()=>{
 const known=new Map(),save=memory=>{for(const row of memory.transition_records)known.set(row.memory_version_id,row);return memory;};
 const entry=save(fresh()),left=save(entry.mstore(0,1n)),right=save(entry.mstore(0,2n));
 let memory=save(joinMemory([{memory:left,conditions:[{expression:'flag'}]},{memory:right,conditions:[{expression:'!flag'}]}]));
 memory=save(memory.mcopy(128,64,32));memory=save(memory.mstore8(129,17));memory=save(memory.loadWord(256).memory);
 memory=save(memory.copyFrom(400,0,4,memorySource('returndata',{sourceId:'pending-call',frameId:'test-frame'})));
 for(const row of known.values()){
  if(row.parent_version_id)assert.ok(known.has(row.parent_version_id),JSON.stringify(row));
  for(const input of row.inputs||[])assert.ok(known.has(input.memory_version_id),JSON.stringify(row));
  if(row.context?.source_memory_version)assert.ok(known.has(row.context.source_memory_version),JSON.stringify(row));
 }
 assert.equal(known.get(memory.version_id).kind,'join');
});
