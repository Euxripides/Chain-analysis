import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {createRequire} from 'node:module';
import {createServer} from 'node:net';
import {once} from 'node:events';
import {gzipSync} from 'node:zlib';
import {spawnSync} from 'node:child_process';
import {localGenesisEVM} from './local_genesis_evm.mjs';

const root=process.cwd(),image=process.env.CONTRACT_TEST_IMAGE||fs.readFileSync(path.join(root,'acceptance/contract-analysis-heap-writes/engine-image.txt'),'utf8').trim();
const out=path.join(root,'acceptance/contract-analysis-heap-writes/creation-identity-runtime',image.slice(7,23),new Date().toISOString().replace(/[:.]/g,''));fs.mkdirSync(out,{recursive:true});
const sha=value=>createHash('sha256').update(value).digest('hex'),write=(name,value)=>fs.writeFileSync(path.join(out,name),Buffer.isBuffer(value)||typeof value==='string'?value:JSON.stringify(value,null,2));
const require=createRequire(path.join(root,'analysis-engines/package.json')),solc=require('solc-0.8.30'),materials=[];
for(const value of[7,9]){
 const source=`pragma solidity ^0.8.30;contract InitIdentity{uint256 public value=${value};function set(uint256 n)external{value=n;}}`,settings={evmVersion:'shanghai',metadata:{bytecodeHash:'none'}};
 const material={kind:'source',source:{language:'Solidity',files:{'InitIdentity.sol':source},compiler_version:'0.8.30+commit.73712a01',target:'InitIdentity.sol:InitIdentity',settings}};
 const raw=JSON.stringify(material),input={submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256:sha(raw),engine_digest:image},engines:['solc-ast']};
 const engine=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--ulimit','core=0','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify(input),encoding:'utf8',timeout:180000,maxBuffer:64<<20});assert.equal(engine.status,0,engine.stderr);
 const model=JSON.parse(engine.stdout);assert.equal(model.sections.engines.find(e=>e.engine==='solc-ast')?.status,'completed');
 const compilerInput={language:'Solidity',sources:{'InitIdentity.sol':{content:source}},settings:{...settings,outputSelection:{'*':{'*':['abi','evm.bytecode','evm.deployedBytecode','evm.methodIdentifiers']}}}};
 const compiled=JSON.parse(solc.compile(JSON.stringify(compilerInput)));assert.deepEqual((compiled.errors||[]).filter(e=>e.severity==='error'),[]);
 const contract=compiled.contracts['InitIdentity.sol'].InitIdentity,method=model.sections.methods.find(m=>m.entry_kind==='constructor'&&m.dispatch_role==='implicit_source_definition'),initial=model.sections.initial_state_models.find(m=>m.method_id===method.method_id);assert.ok(method&&initial);
 assert.equal(initial.creation_identity.initcode,'0x'+contract.evm.bytecode.object);assert.equal(initial.deployment_identity_verified,false);assert.ok(method.completeness.unresolved.includes('DEPLOYMENT_CREATION_IDENTITY_NOT_VERIFIED'));
 write(value+'-input.json',input);write(value+'-model.json.gz',gzipSync(Buffer.from(JSON.stringify(model))));write(value+'-compiler-input.json',compilerInput);write(value+'-compiler-output.json',compiled);
 materials.push({value,input,model,contract,method,initial});
}
assert.equal(materials[0].contract.evm.deployedBytecode.object,materials[1].contract.evm.deployedBytecode.object);assert.notEqual(materials[0].contract.evm.bytecode.object,materials[1].contract.evm.bytecode.object);assert.notEqual(materials[0].input.snapshot.input_sha256,materials[1].input.snapshot.input_sha256);
assert.notEqual(materials[0].model.sections.methods.find(m=>m.name==='set').method_id,materials[1].model.sections.methods.find(m=>m.name==='set').method_id);
async function freePort(){const server=createServer();server.listen(0,'127.0.0.1');await once(server,'listening');const port=server.address().port;server.close();await once(server,'close');return port;}
const port=await freePort(),sourcePort=await freePort(),sender='0x'+'1'.repeat(40);let vm,id=0;const deployments=[];
async function rpc(method,params=[]){const response=await fetch('http://127.0.0.1:'+port,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++id,method,params}),signal:AbortSignal.timeout(20000)}),value=await response.json();if(value.error)throw Error(JSON.stringify(value.error));return value.result;}
try{
 vm=await localGenesisEVM({binary:path.join(root,'../contract-verification-20261015/analysis-engines/bin/anvil'),port,sourcePort});await rpc('anvil_setBalance',[sender,'0x'+(10n**30n).toString(16)]);
 for(const material of materials){
  const tx=await rpc('eth_sendTransaction',[{from:sender,data:'0x'+material.contract.evm.bytecode.object,gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');const receipt=await rpc('eth_getTransactionReceipt',[tx]);assert.equal(receipt.status,'0x1');
  const code=await rpc('eth_getCode',[receipt.contractAddress,'latest']);assert.equal(code,'0x'+material.contract.evm.deployedBytecode.object);assert.equal(code,material.model.sections.artifacts.find(a=>a.kind==='bytecode').text);
  const slot=await rpc('eth_getStorageAt',[receipt.contractAddress,'0x0','latest']),returned=await rpc('eth_call',[{to:receipt.contractAddress,data:'0x'+material.contract.evm.methodIdentifiers['value()']},'latest']);assert.equal(BigInt(slot),BigInt(material.value));assert.equal(BigInt(returned),BigInt(material.value));
  deployments.push({input_sha256:material.input.snapshot.input_sha256,creation_method_id:material.method.method_id,initial_model_id:material.initial.model_id,initial_value:material.value,address:receipt.contractAddress,deployment_transaction_hash:tx,initcode_sha256:sha(Buffer.from(material.contract.evm.bytecode.object,'hex')),runtime_sha256:sha(Buffer.from(code.slice(2),'hex')),actual_storage:slot,actual_getter_return:returned,deployment_identity_verified:material.initial.deployment_identity_verified});write(material.value+'-deployment.json',receipt);
 }
}finally{await vm?.close();}
const collision=deployments[0].creation_method_id===deployments[1].creation_method_id;
const record={status:collision?'FAIL_CONFIRMED_IMPLICIT_CREATION_IDENTITY_COLLISION':'PASS_DISTINCT_IMPLICIT_CREATION_IDENTITIES',image,actual_deployments:deployments.length,getter_eth_calls:deployments.length,deployments,scope:'Distinct submitted input and initcode with equal compiled runtime and two actual initial states. Existing deployment-not-verified flags remain; no wrong state within a job or cross-job permission leak is asserted.'};write('results.json',record);console.log('IMPLICIT_CREATION_IDENTITY_OBSERVATION',JSON.stringify(record),out);assert.ok(!collision,'Implicit constructor method identity must distinguish different submitted input/initcode like ordinary method identities');
