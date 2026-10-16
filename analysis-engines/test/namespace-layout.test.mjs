import test from'node:test';import assert from'node:assert/strict';import{createRequire}from'node:module';
import{erc7201Root,collectNamespaces,bindNamespaceAccessors,activateNamespaceLayout,namespacePointerType,namespaceRootReference}from'../namespace-layout.mjs';
const require=createRequire(import.meta.url),solc=require('solc-0.8.30'),root='0x'+erc7201Root('example.main').toString(16).padStart(64,'0');
function compile(body,{whole=false}={}){
 const source=whole?body:'pragma solidity ^0.8.20;contract Verification{'+body+'}';
 const output=JSON.parse(solc.compile(JSON.stringify({language:'Solidity',sources:{'Verification.sol':{content:source}},settings:{outputSelection:{'*':{'*':['storageLayout'],'':['ast']}}}})));
 if(output.errors?.some(e=>e.severity==='error'))throw Error(JSON.stringify(output.errors));const nodes=new Map();
 function walk(n){if(!n||typeof n!=='object')return;if(n.nodeType&&Number.isInteger(n.id))nodes.set(n.id,n);for(const[k,v]of Object.entries(n))if(!['typeDescriptions','documentation'].includes(k)){if(Array.isArray(v))v.forEach(walk);else if(v&&typeof v==='object')walk(v);}}
 walk(output.sources['Verification.sol'].ast);const main=[...nodes.values()].find(n=>n.nodeType==='ContractDefinition'&&n.name==='Verification'),orderedContracts=main.linearizedBaseContracts.map(id=>nodes.get(id)),layout=output.contracts['Verification.sol'].Verification.storageLayout,functions=orderedContracts.flatMap(c=>c.nodes.filter(n=>n.nodeType==='FunctionDefinition'));
 return{source,output,nodes,main,orderedContracts,layout,functions};
}
const basic=(tag='example.main',actual=root)=>`/// @custom:storage-location erc7201:${tag}
struct Store{address owner;uint8 initialized;mapping(address=>uint)balances;uint total;}
bytes32 constant ROOT=${actual};function store()internal pure returns(Store storage $){assembly{$.slot:=ROOT}}`;
test('ERC-7201 root matches the published example and rejects ambiguous identifiers',()=>{
 assert.equal(root,'0x183a6125c38840424c4a85fa12bab2ab606c4b6d0e7cc73c0c06ba5300eab500');
 for(const id of['','with whitespace','x\ny','x'.repeat(257)])assert.throws(()=>erc7201Root(id),/NAMESPACE_/);
});
test('namespace layout and actual pointer binding retain original declaration identities',()=>{
 const fixture=compile(basic()),spaces=collectNamespaces(fixture);assert.equal(spaces.records.length,1);const ns=spaces.records[0];
 assert.equal(ns.layout_complete,true,JSON.stringify(ns));assert.equal(ns.complete,false);assert.equal(ns.root_hex,root);
 assert.deepEqual(ns.members.map(m=>[m.label,m.slot,m.offset]),[['owner','0',0],['initialized','0',20],['balances','1',0],['total','2',0]]);
 for(const member of ns.members)assert.equal(fixture.nodes.get(member.astId).name,member.label);
 const bound=bindNamespaceAccessors({...fixture,namespaces:spaces});assert.equal(bound.byFunction.size,1);assert.equal(ns.pointer_assignment_verified,true);assert.equal(ns.complete,true);
 assert.equal(bound.records[0].constant_ast_id,[...fixture.nodes.values()].find(n=>n.nodeType==='VariableDeclaration'&&n.name==='ROOT').id);
});
function shape(types,key,depth=0){
 if(depth>8)throw Error('probe type depth');const t=types[key],out={encoding:t.encoding,label:t.label,numberOfBytes:t.numberOfBytes};
 if(t.members)out.members=t.members.map(m=>({label:m.label,slot:m.slot,offset:m.offset,type:shape(types,m.type,depth+1)}));
 for(const k of['base','key','value'])if(t[k])out[k]=shape(types,t[k],depth+1);return out;
}
test('independent compiler probe confirms packing, nested structs, arrays and dynamic root allocation',()=>{
 const body=`struct Pair{uint128 left;uint128 right;bool ready;}
/// @custom:storage-location erc7201:example.main
struct Store{address owner;uint8 initialized;bytes4 tag;mapping(address=>uint)balances;uint total;Pair pair;uint8[10] small;bytes data;}
bytes32 constant ROOT=${root};function store()internal pure returns(Store storage $){assembly{$.slot:=ROOT}}`;
 const original=compile(body),namespaces=collectNamespaces(original),ns=namespaces.records[0];assert.equal(ns.layout_complete,true,JSON.stringify(ns));
 const probed=compile(body+' Store private __compiler_probe;'),item=probed.layout.storage.find(s=>s.label==='__compiler_probe');
 assert.deepEqual(shape(ns.types,ns.type_id),shape(probed.layout.types,item.type));assert.equal(ns.slot_count,'7');
 assert.equal(ns.types[ns.type_id].model_supported,false);assert.equal(original.layout.storage.length,0);assert.equal(original.layout.types,null);
});
test('wrong roots, extra assembly effects and shadowed nonconstant names cannot establish identity',()=>{
 for(const mode of['root','assembly','shadow','modifier']){
  let body=basic();if(mode==='root')body=basic('example.main','0x'+'2'.repeat(64));
  if(mode==='assembly')body=body.replace('$.slot:=ROOT','$.slot:=ROOT mstore(0,1)');
  if(mode==='shadow')body=body.replace('assembly{$.slot:=ROOT}','bytes32 local=ROOT;assembly{$.slot:=local}');
  if(mode==='modifier')body=body.replace('function store()','modifier extra(){_;}function store()').replace('internal pure returns','internal pure extra returns');
  const f=compile(body),spaces=collectNamespaces(f),bound=bindNamespaceAccessors({...f,namespaces:spaces});
  assert.equal(bound.byFunction.size,0,mode);assert.equal(spaces.records[0].complete,false,mode);assert.ok(bound.records.some(r=>r.unresolved.length),mode);
 }
});
test('conflicting namespaces and ordinary storage overlap are explicit',()=>{
 const f=compile(basic()+'\n/// @custom:storage-location erc7201:example.main\nstruct Another{uint value;}'),spaces=collectNamespaces(f);
 assert.equal(spaces.byStruct.size,0);assert.ok(spaces.records.every(r=>r.unresolved.includes('NAMESPACE_DECLARED_REGION_CONFLICT')));
 const real=compile('uint normal;'+basic());real.layout.storage[0].slot=String(erc7201Root('example.main'));
 const overlap=collectNamespaces(real);assert.equal(overlap.byStruct.size,0);assert.ok(overlap.records[0].unresolved.includes('NAMESPACE_STANDARD_STORAGE_OVERLAP'));
});
test('inherited namespaces bind by compiler IDs and annotations outside contracts stay excluded',()=>{
 const source='pragma solidity ^0.8.20;\n/// @custom:storage-location erc7201:outside\nstruct Outside{uint x;}\ncontract Base{'+basic()+'}contract Verification is Base{}';
 const f=compile(source,{whole:true}),spaces=collectNamespaces(f),bound=bindNamespaceAccessors({...f,namespaces:spaces});assert.equal(spaces.records.length,1);assert.equal(bound.byFunction.size,1);
});
test('only validated pointers activate field roots, packed cells and storage reference types',()=>{
 const f=compile(basic()),spaces=collectNamespaces(f),bound=bindNamespaceAccessors({...f,namespaces:spaces}),original=structuredClone(f.layout),registry=activateNamespaceLayout(f.layout,spaces,bound);
 assert.equal(registry.byField.size,4);assert.deepEqual(f.layout,original);assert.equal(registry.layout.namespace_static_slots.length,3);
 const owner=[...registry.byField.values()].find(v=>v.label==='owner'),initialized=[...registry.byField.values()].find(v=>v.label==='initialized');
 assert.equal(owner.slot,initialized.slot);assert.equal(owner.offset,0);assert.equal(initialized.offset,20);assert.notEqual(owner.astId,initialized.astId);
 const pointer=[...bound.byFunction.values()][0],r=f.nodes.get(pointer.return_declaration_ast_id),type=namespacePointerType(r,registry),value=namespaceRootReference(pointer);
 assert.equal(value.type,type.type);assert.equal(value.storage_location.slot,String(erc7201Root('example.main')));
 assert.equal(namespacePointerType({...r,storageLocation:'memory'},registry),null);
 const bad=compile(basic('example.main','0x'+'2'.repeat(64))),missing=collectNamespaces(bad),invalid=bindNamespaceAccessors({...bad,namespaces:missing});
 assert.equal(activateNamespaceLayout(bad.layout,missing,invalid).byField.size,0);
});
