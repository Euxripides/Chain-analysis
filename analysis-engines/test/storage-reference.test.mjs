import test from'node:test';import assert from'node:assert/strict';
import{keccak_256}from'@noble/hashes/sha3.js';
import{compileSource}from'../source.mjs';import{legacyLayout}from'../layout.mjs';
import{emptyResult}from'../model.mjs';
import{storageTypeID,storageTypeRegistry,storageArrayInfo,storageRoot,storageMember,storageIndex,evaluateStorageSlot,StorageReferences}from'../ir/storage-reference.mjs';

const source=`pragma solidity >=0.4.26 <0.9.0;
contract Base{uint128 left;}
contract Probe is Base{uint128 right;
struct Item{uint8 flag;uint128 amount;address who;mapping(address=>uint) debts;uint16[19] nested;}
mapping(uint=>Item) items;Item[] list;uint24[12][2] packed;mapping(bytes4=>uint) labels;bytes data;uint tail;
function pointerHelper(Item storage item,uint16[] storage numbers)internal returns(Item storage out){out=item;}
}`;
function fixture(version='0.8.30',text=source){
 const output=compileSource({language:'Solidity',sources:{'Probe.sol':{content:text}},settings:{}},version),nodes=new Map();
 const visit=n=>{if(!n||typeof n!=='object')return;if(n.nodeType)nodes.set(n.id,n);for(const [k,v]of Object.entries(n))if(k!=='typeDescriptions')if(Array.isArray(v))v.forEach(visit);else if(v&&typeof v==='object')visit(v);};visit(output.sources['Probe.sol'].ast);
 const main=[...nodes.values()].find(n=>n.nodeType==='ContractDefinition'&&n.name==='Probe'),ordered=main.linearizedBaseContracts.map(k=>nodes.get(k)),layout=output.contracts['Probe.sol'].Probe.storageLayout||legacyLayout(ordered,n=>n.typeDescriptions.typeString,nodes);
 return{output,nodes,main,ordered,layout,registry:storageTypeRegistry(layout,nodes)};
}
const key=(value,name='key')=>({key_literal:value===null?null:String(value),key_expression_id:name,key_text:name,key_ast_id:1,key_referenced_declaration:2});
const word=n=>Buffer.from(BigInt(n).toString(16).padStart(64,'0'),'hex'),hash=buffer=>BigInt('0x'+Buffer.from(keccak_256(buffer)).toString('hex'));
function root(f,name){return storageRoot(f.layout.storage.find(s=>s.label===name),f.layout.types);}
function field(f,parent,name){return storageMember(parent,f.layout.types[parent.type_id].members.find(m=>m.label===name),f.layout.types);}

test('old AST layouts agree with compiler layouts for inheritance, aggregates and packed arrays',()=>{
 const old=fixture('0.4.26'),fingerprint=(layout,type_id,seen=new Set())=>{
  const type=layout.types[type_id];if(seen.has(type_id))return type.label;seen=new Set([...seen,type_id]);
  return{encoding:type.encoding,label:type.label,bytes:type.numberOfBytes,...(type.base?{base:fingerprint(layout,type.base,seen)}:{}),
   ...(type.key?{key:fingerprint(layout,type.key,seen),value:fingerprint(layout,type.value,seen)}:{}),
   ...(type.members?{members:type.members.map(m=>({name:m.label,slot:m.slot,offset:m.offset,type:fingerprint(layout,m.type,seen)}))}:{})};
 };
 assert.deepEqual(old.layout.unresolved,[]);
 for(const version of['0.6.12','0.7.6','0.8.30']){
  const current=fixture(version);
  assert.deepEqual(old.layout.storage.map(s=>[s.label,s.slot,s.offset]),current.layout.storage.map(s=>[s.label,s.slot,s.offset]));
  for(const row of old.layout.storage){const other=current.layout.storage.find(s=>s.label===row.label);assert.deepEqual(fingerprint(old.layout,row.type),fingerprint(current.layout,other.type),version+':'+row.label);}
 }
});

test('compiler type identities bind storage references without accepting a same-named memory object',()=>{
 for(const version of['0.4.26','0.6.12','0.7.6','0.8.30']){
  const f=fixture(version),parameter=[...f.nodes.values()].find(n=>n.nodeType==='VariableDeclaration'&&n.name==='item');
  const type=f.registry.pointerType(parameter);assert.equal(type.sort,'StorageReference');
  assert.equal(type.type_id,f.layout.types[root(f,'items').type_id].value);
  assert.equal(storageTypeID(parameter.typeName),type.type_id);
  assert.equal(f.registry.pointerType({...parameter,storageLocation:'memory'}),null);
 }
});

test('mapping and dynamic struct locations match independently constructed Keccak preimages and strides',()=>{
 const f=fixture(),mapped=storageIndex(root(f,'items'),key(7),f.layout.types),amount=field(f,mapped,'amount');
 const expected=hash(Buffer.concat([word(7),word(root(f,'items').base_slot)]));
 assert.equal(evaluateStorageSlot(amount.slot_expression),expected);assert.equal(amount.offset,1);
 const itemType=f.layout.types[mapped.type_id],stride=(BigInt(itemType.numberOfBytes)+31n)/32n;
 const listed=storageIndex(root(f,'list'),key(2),f.layout.types),who=field(f,listed,'who');
 assert.equal(evaluateStorageSlot(who.slot_expression),hash(word(root(f,'list').base_slot))+2n*stride+1n);
 assert.equal(who.bounds[0].kind,'dynamic_array');assert.equal(who.bounds[0].length,null);
 assert.equal(evaluateStorageSlot(who.bounds[0].length_slot_expression),BigInt(root(f,'list').base_slot));
});

test('nested fixed and dynamic packed arrays preserve each level of stride, offset and bounds',()=>{
 const f=fixture(),outer=storageIndex(root(f,'packed'),key(1),f.layout.types),inner=storageIndex(outer,key(11),f.layout.types);
 assert.equal(evaluateStorageSlot(inner.slot_expression),BigInt(root(f,'packed').base_slot)+3n);assert.equal(inner.offset,3);
 assert.deepEqual(inner.bounds.map(b=>b.length),['2','12']);
 const array=fixture('0.8.30',source.replace('uint tail;','uint tail;uint24[][] matrix;')),
  first=storageIndex(root(array,'matrix'),key(null,'outer'),array.layout.types),second=storageIndex(first,key(null,'inner'),array.layout.types),
  firstSlot=hash(word(root(array,'matrix').base_slot))+4n;
 assert.equal(evaluateStorageSlot(second.slot_expression,{outer:4,inner:13}),hash(word(firstSlot))+1n);
 assert.equal(evaluateStorageSlot(second.offset_expression,{outer:4,inner:13}),9n);
 assert.equal(second.bounds.length,2);assert.equal(storageArrayInfo(array.layout.types[first.type_id],array.layout.types).elements_per_slot,'10');
});

test('bytes mapping keys are left aligned and unavailable dynamic key bytes never become a guessed slot',()=>{
 const f=fixture(),projection=storageIndex(root(f,'labels'),key(0xaabbccddn),f.layout.types),
  expected=hash(Buffer.concat([Buffer.from('aabbccdd'.padEnd(64,'0'),'hex'),word(root(f,'labels').base_slot)]));
 assert.equal(evaluateStorageSlot(projection.slot_expression),expected);
 const dynamic=fixture('0.8.30',source.replace('uint tail;','uint tail;mapping(string=>Item) names;')),
  pending=storageIndex(root(dynamic,'names'),key(null),dynamic.layout.types);
 assert.ok(pending.unresolved.includes('STORAGE_DYNAMIC_MAPPING_KEY_BYTES_UNAVAILABLE'));
 assert.equal(dynamic.layout.types[pending.type_id].label,'struct Probe.Item');
 assert.throws(()=>evaluateStorageSlot(pending.slot_expression),/STORAGE_DYNAMIC_MAPPING_KEY_BYTES_UNAVAILABLE/);
});

test('captured keys, rebinding and branch locations remain separate immutable references',()=>{
 const f=fixture(),result=emptyResult({}),method={method_id:'method',code_hash:'code'},tracker=new StorageReferences(result,method),
  captured=tracker.capture(storageIndex(root(f,'items'),key(null,'key-at-construction'),f.layout.types),{expression_id:'origin',context:{path:'entry',order:2,conditions:[]}});
 tracker.bind(9,captured);const before=tracker.get(9),yes=tracker.fork(),no=tracker.fork();
 yes.bind(9,yes.capture(storageIndex(root(f,'items'),key(8),f.layout.types),{context:{path:'entry/if:true',conditions:['flag']}}));
 assert.deepEqual(tracker.get(9),before);tracker.merge(yes,no,{yesConditions:['flag'],noConditions:['!(flag)'],context:{ast_id:4}});
 assert.equal(tracker.get(9).cases.length,2);assert.equal(result.sections.storage_reference_phis.length,1);
 const old=tracker.resolve(before)[0].record;assert.equal(old.projection.path[0].key_expression_id,'key-at-construction');
 assert.equal(evaluateStorageSlot(old.projection.slot_expression,{'key-at-construction':3}),hash(Buffer.concat([word(3),word(root(f,'items').base_slot)])));
 const unknown=tracker.fork();unknown.bind(9,null);tracker.merge(unknown,no);
 assert.ok(tracker.get(9).cases.some(c=>c.storage_reference_id===null));assert.ok(tracker.get(9).unresolved.length);
});

test('unsupported old types stop subsequent allocation rather than shifting known roots',()=>{
 const f=fixture('0.4.26',source.replace('uint tail;','function() internal cb;uint tail;'));
 assert.ok(f.layout.unresolved.some(g=>/TYPE_UNSUPPORTED/.test(g.reason)));
 assert.equal(f.layout.storage.some(s=>s.label==='tail'),false);assert.ok(f.layout.storage.some(s=>s.label==='labels'));
});
