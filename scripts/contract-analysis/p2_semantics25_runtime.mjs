import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {createRequire} from 'node:module';
import {createServer} from 'node:net';
import {once} from 'node:events';
import {analyze} from '../../analysis-engines/analyze.mjs';
import {localGenesisEVM} from './local_genesis_evm.mjs';

const root=process.cwd(),out=path.join(root,'acceptance/contract-analysis-semantics25',new Date().toISOString().replace(/[:.]/g,''));
const sha=b=>createHash('sha256').update(b).digest('hex'),write=(n,v)=>fs.writeFileSync(path.join(out,n),typeof v==='string'?v:JSON.stringify(v,(_,x)=>typeof x==='bigint'?x.toString():x,2));
fs.mkdirSync(out,{recursive:true});
const source=fs.readFileSync(path.join(root,'analysis-engines/test/fixtures/storage-memory-copy.sol'),'utf8'),require=createRequire(path.join(root,'analysis-engines/package.json')),solc=require('solc-0.8.30'),{keccak_256}=require('@noble/hashes/sha3.js');
const settings={evmVersion:'shanghai',optimizer:{enabled:false},outputSelection:{'*':{'*':['abi','evm.bytecode','evm.deployedBytecode','evm.methodIdentifiers','storageLayout'],'':['ast']}}};
const compiled=JSON.parse(solc.compile(JSON.stringify({language:'Solidity',sources:{'StorageCopyCases.sol':{content:source}},settings})));assert.deepEqual((compiled.errors||[]).filter(e=>e.severity==='error'),[]);
const contract=compiled.contracts['StorageCopyCases.sol'].StorageCopyCases,material={kind:'source',source:{language:'Solidity',files:{'StorageCopyCases.sol':source},compiler_version:solc.version().split('.Emscripten')[0],target:'StorageCopyCases.sol:StorageCopyCases',settings:{evmVersion:'shanghai',optimizer:{enabled:false}}}},raw=JSON.stringify(material);
const model=analyze({submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256:sha(raw),engine_digest:'local-source'},engines:['solc-ast']});
assert.equal(model.rule_version,'funds-semantics-25');write('model.json',model);
const method=name=>{const row=model.sections.methods.find(m=>m.name===name&&m.dispatch_role==='external_dispatch');assert.ok(row,name);return row;};
const transition=name=>model.sections.state_transitions.find(t=>t.method_id===method(name).method_id);
const field=name=>{const copy=model.sections.expressions.find(e=>e.method_id===method(name).method_id&&e.source_storage_copy);assert.ok(copy,name);return copy.fields.find(f=>f.type==='string');};
assert.equal(field('shortLiteralCopy').bytes_hex,Buffer.from('abc').toString('hex'));
assert.equal(field('shortLiteralCopy').bytes_length,3);assert.equal(field('shortLiteralCopy').bytes_source,'dominating_literal_storage_write');
const longText='0123456789012345678901234567890123456789';
assert.equal(field('longLiteralCopy').bytes_hex,Buffer.from(longText).toString('hex'));assert.equal(field('longLiteralCopy').bytes_length,40);
assert.match(field('unknownContentCopy').field_gap,/CONTENT_NOT_PROVEN/);
assert.equal(transition('shortLiteralCopy').complete,true,JSON.stringify(transition('shortLiteralCopy').unresolved));
assert.equal(transition('longLiteralCopy').complete,false);assert.ok(transition('longLiteralCopy').unresolved.includes('STATE_DYNAMIC_STORAGE_WRITE_LONG_UNSUPPORTED'));

const sender='0x'+'1'.repeat(40),receiver='0x'+'4'.repeat(40);let vm,id=0,address,slot;
async function freePort(){const s=createServer();s.listen(0,'127.0.0.1');await once(s,'listening');const p=s.address().port;s.close();await once(s,'close');return p;}
const port=await freePort(),sourcePort=await freePort();
async function rpc(method_,params=[]){const r=await fetch('http://127.0.0.1:'+port,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++id,method:method_,params}),signal:AbortSignal.timeout(20000)}),v=await r.json();if(v.error)throw Error(JSON.stringify(v.error));return v.result;}
const word=n=>BigInt(n).toString(16).padStart(64,'0');
const pad32=n=>'0x'+BigInt(n).toString(16).padStart(64,'0');
function decodeShort(slotWord){const raw=BigInt(slotWord),length=Number((raw&0xffn)>>1n);assert.equal((raw&1n),0n,'short flag');const bytes=Buffer.alloc(length);
 for(let i=0;i<length;i++)bytes[i]=Number((raw>>BigInt(8*(31-i)))&0xffn);return bytes;}
async function decodeStored(){const head=await rpc('eth_getStorageAt',[address,pad32(slot),'latest']),raw=BigInt(head);
 if(!(raw&1n))return{encoding:'short_inline',bytes:decodeShort(head)};
 const length=Number((raw-1n)>>1n);assert.ok(length>=32&&length<=256,'bounded long length '+head);const dataSlot='0x'+Buffer.from(keccak_256(Buffer.from(BigInt(slot).toString(16).padStart(64,'0'),'hex'))).toString('hex'),bytes=Buffer.alloc(length);
 for(let offset=0;offset<length;offset+=32){const chunk=Buffer.from((await rpc('eth_getStorageAt',[address,pad32(BigInt(dataSlot)+(BigInt(offset)/32n)),'latest'])).slice(2),'hex');chunk.copy(bytes,offset);}
 return{encoding:'long_keccak',bytes};}
try{
 slot=compiled.contracts['StorageCopyCases.sol'].StorageCopyCases.storageLayout.storage.find(s=>s.label==='text')?.slot;
 assert.ok(slot!==undefined,'text storage slot');
 vm=await localGenesisEVM({binary:path.join(root,'../contract-verification-20261015/analysis-engines/bin/anvil'),port,sourcePort});await rpc('anvil_setBalance',[sender,'0x'+(10n**30n).toString(16)]);
 const deployment='0x'+contract.evm.bytecode.object+word(BigInt(receiver)),hash=await rpc('eth_sendTransaction',[{from:sender,data:deployment,gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');
 const receipt=await rpc('eth_getTransactionReceipt',[hash]);assert.equal(receipt.status,'0x1');address=receipt.contractAddress;
 const records=[];
 for(const name of['shortLiteralCopy','longLiteralCopy']){
  await rpc('anvil_setBalance',[address,'0x'+(10n**24n).toString(16)]);
  const tx=await rpc('eth_sendTransaction',[{from:sender,to:address,data:'0x'+contract.evm.methodIdentifiers[name+'(address)']+word(BigInt(receiver)),gas:'0x989680',gasPrice:'0x77359400'}]);
  await rpc('evm_mine');assert.equal((await rpc('eth_getTransactionReceipt',[tx])).status,'0x1',name);
  const stored=await decodeStored(),modelField=field(name),actualHex=stored.bytes.toString('hex');
  assert.equal(actualHex,modelField.bytes_hex,name+':content');assert.equal(stored.bytes.length,modelField.bytes_length,name+':length');
  assert.equal(actualHex,Buffer.from(stored.encoding==='short_inline'?'abc':longText).toString('hex'),name+':literal');
  if(name==='shortLiteralCopy'){
   const head=await rpc('eth_getStorageAt',[address,pad32(slot),'latest']),writes=transition(name).paths[0].writes,labelWrite=writes.find(w=>BigInt(w.new_value));
   assert.ok(labelWrite,'state write for short label');assert.equal(BigInt(labelWrite.new_value),BigInt(head),'state short encoding matches chain storage');
  }
  records.push({method:name,encoding:stored.encoding,slot:String(slot),bytes_hex:actualHex,model_bytes_hex:modelField.bytes_hex,length:stored.bytes.length,state_complete:transition(name).complete,unresolved:transition(name).unresolved,transaction:tx,storage_word:await rpc('eth_getStorageAt',[address,pad32(slot),'latest'])});
 }
 write('report.json',{status:'PASS_STRING_CONTENT_COPY_AGAINST_CHAIN_STORAGE',rule_version:'funds-semantics-25',storage_slot:slot,records,provider_requests:0,scope:'One owned fixture. Short inline and bounded long string literals written before the struct copy are decoded from actual chain storage and compared byte for byte with the memory-copy content captured by the engine. Unknown entry content stays an explicit gap; long dynamic writes stay an explicit state-model gap.'});
 console.log('SEMANTICS25_ACTUAL_PASS',JSON.stringify({directory:out,records:records.length}));
}catch(error){write('failure.json',{error:error.message,stack:error.stack});throw error;}finally{await vm?.close();}
