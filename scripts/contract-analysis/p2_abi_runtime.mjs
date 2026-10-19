import assert from'node:assert/strict';import{readFileSync,writeFileSync,mkdirSync}from'node:fs';import{createRequire}from'node:module';import{createHash}from'node:crypto';import{createServer}from'node:net';import{once}from'node:events';
import{localGenesisEVM}from'./local_genesis_evm.mjs';import{decodeAbi}from'../../analysis-engines/ir/abi.mjs';
const root=new URL('../../',import.meta.url),require=createRequire(new URL('analysis-engines/package.json',root)),solc=require('solc-0.8.30'),{keccak_256}=require('@noble/hashes/sha3.js');
const memoryBase=process.env.CONTRACT_MEMORY_BASE||'acceptance/contract-analysis-memory',folder=readFileSync(new URL(memoryBase+'/abi-reference-current.txt',root),'utf8').trim(),reference=JSON.parse(readFileSync(folder+'/fixtures.json'));
const out=new URL(memoryBase+'/abi-execution/'+new Date().toISOString().replace(/\D/g,'')+'Z/',root);mkdirSync(new URL('../',out),{recursive:true});mkdirSync(out);
const sha=b=>createHash('sha256').update(b).digest('hex'),hash=b=>Buffer.from(keccak_256(b)).toString('hex'),selector=s=>hash(Buffer.from(s)).slice(0,8),word=n=>BigInt(n).toString(16).padStart(64,'0'),pad=hex=>hex.padEnd(Math.ceil(hex.length/64)*64,'0');
const source=`pragma solidity ^0.8.24;contract ABIHarness{
struct Pair{uint128 a;int128 b;}struct Bundle{address who;Pair[] pairs;bytes32 tag;}
struct Payload{bytes data;uint256[] nums;}struct Thing{uint256 n;bytes data;string[] tags;}struct Mix{bool yes;string text;}
function g0(bytes calldata d)external pure returns(bytes memory){(uint8 a,int24 b,address c,bool e,bytes5 f,uint256 g)=abi.decode(d,(uint8,int24,address,bool,bytes5,uint256));return abi.encode(a,b,c,e,f,g);}
function g1(bytes calldata d)external pure returns(bytes memory){(bytes memory b,string memory s,uint256[] memory n)=abi.decode(d,(bytes,string,uint256[]));return abi.encode(b,s,n);}
function g2(bytes calldata d)external pure returns(bytes memory){(uint256[][] memory n,string[] memory s)=abi.decode(d,(uint256[][],string[]));return abi.encode(n,s);}
function g3(bytes calldata d)external pure returns(bytes memory){Thing memory v=abi.decode(d,(Thing));return abi.encode(v);}
function g4(bytes calldata d)external pure returns(bytes memory){Bundle[2] memory v=abi.decode(d,(Bundle[2]));return abi.encode(v);}
function g5(bytes calldata d)external pure returns(bytes memory){Payload[2][] memory v=abi.decode(d,(Payload[2][]));return abi.encode(v);}
function g6(bytes calldata d)external pure returns(bytes memory){bytes[][2] memory v=abi.decode(d,(bytes[][2]));return abi.encode(v);}
function g7(bytes calldata d)external pure returns(bytes memory){(uint256[2][3] memory n,Mix[] memory m)=abi.decode(d,(uint256[2][3],Mix[]));return abi.encode(n,m);}
function probeBytes(bytes calldata d)external pure returns(bytes memory){bytes memory b=abi.decode(d,(bytes));return abi.encode(b);}
function probeTwo(bytes calldata d)external pure returns(bytes memory){(bytes memory a,bytes memory b)=abi.decode(d,(bytes,bytes));return abi.encode(a,b);}}
`;
const compiled=JSON.parse(solc.compile(JSON.stringify({language:'Solidity',sources:{'ABIHarness.sol':{content:source}},settings:{optimizer:{enabled:true,runs:200},evmVersion:'shanghai',outputSelection:{'*':{'*':['evm.deployedBytecode.object','abi']}}}})));
assert.ok(!compiled.errors?.some(e=>e.severity==='error'),JSON.stringify(compiled.errors));const code='0x'+compiled.contracts['ABIHarness.sol'].ABIHarness.evm.deployedBytecode.object;writeFileSync(new URL('source.sol',out),source);writeFileSync(new URL('runtime.hex',out),code);
async function port(){const s=createServer();s.listen(0,'127.0.0.1');await once(s,'listening');const p=s.address().port;s.close();await once(s,'close');return p;}
const portNumber=await port(),sourcePort=await port(),target='0x'+'6'.repeat(40),sender='0x'+'9'.repeat(40),binary=process.env.ANVIL_BINARY||new URL('../contract-verification-20261015/analysis-engines/bin/anvil',root).pathname;
assert.equal(sha(readFileSync(binary)),'00f97668e1ae0fcca118fd7d2d1b6d25c0496447a523ca6bc2dfd10958c99604');let vm,sequence=0;const records=[];
const rpc=async(method,params=[])=>{const r=await fetch('http://127.0.0.1:'+portNumber,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(10000)}),d=await r.json();if(d.error)throw Error(method+':'+d.error.message);return d.result;};
const wrapped=(name,hex)=>'0x'+selector(name+'(bytes)')+word(32)+word(hex.length/2)+pad(hex);
function unwrap(output){const bytes=Buffer.from(output.slice(2),'hex');assert.ok(bytes.length>=64);assert.equal(BigInt('0x'+bytes.subarray(0,32).toString('hex')),32n);const length=BigInt('0x'+bytes.subarray(32,64).toString('hex'));assert.ok(length<=BigInt(bytes.length-64));return bytes.subarray(64,64+Number(length)).toString('hex');}
async function call(name,encoded){return rpc('eth_call',[{from:sender,to:target,data:wrapped(name,encoded.replace(/^0x/,'')),gas:'0x1c9c380'},'latest']);}
async function trace(name,encoded,label){const observed=await rpc('debug_traceCall',[{from:sender,to:target,data:wrapped(name,encoded.replace(/^0x/,'')),gas:'0x1c9c380'},'latest',{enableMemory:true,disableStorage:true}]),raw=Buffer.from(JSON.stringify(observed));writeFileSync(new URL(label+'-trace.json',out),raw);return{failed:observed.failed,sha256:sha(raw)};}
try{
 vm=await localGenesisEVM({binary,port:portNumber,sourcePort});await rpc('anvil_setCode',[target,code]);
 for(const f of reference.cases.filter(f=>f.group<8)){
  const result=unwrap(await call('g'+f.group,f.encoded));assert.equal(result,f.encoded.slice(2),f.name+' Solidity decode/re-encode/reference mismatch');
  let observation;if(f.name.endsWith('-5')){observation=await trace('g'+f.group,f.encoded,f.name);assert.equal(observation.failed,false);}
  records.push({name:f.name,group:f.group,reference_sha256:f.sha256,actual_reencoded_sha256:sha(Buffer.from(result,'hex')),trace:observation,status:'PASS'});
 }
 const variants=[
  {name:'gap',hex:word(64)+word(0)+word(1)+pad('aa'),expected:'0xaa'},
  {name:'unaligned-unpadded',hex:word(33)+'00'+word(1)+'aa',expected:'0xaa'},
  {name:'head-alias-empty',hex:word(0),expected:'0x'},
  {name:'nonzero-padding',hex:word(32)+word(1)+'aa'+'ff'.repeat(31),expected:'0xaa'},
  {name:'truncated-payload',hex:word(32)+word(10)+'aabbcc',reverts:true},
  {name:'offset-overflow',hex:word((1n<<256n)-1n),reverts:true},
  {name:'length-overflow',hex:word(32)+word((1n<<256n)-1n),reverts:true}
 ];
 for(const v of variants){const parsed=decodeAbi('0x'+v.hex,['bytes']);let output,error;try{output=unwrap(await call('probeBytes',v.hex));}catch(e){error=String(e.message);}
  const observation=await trace('probeBytes',v.hex,v.name);
  if(v.reverts){assert.ok(error,v.name);assert.equal(observation.failed,true);assert.equal(parsed.status,'invalid');}
  else{assert.equal(error,undefined,v.name);assert.equal(observation.failed,false);assert.equal(parsed.values[0].value,v.expected);assert.equal(decodeAbi('0x'+output,['bytes']).values[0].value,v.expected);assert.equal(parsed.canonical.status,'noncanonical');}
  records.push({...v,trace:observation,project_decode_status:parsed.status,canonical_status:parsed.canonical.status,actual_error:error,status:'PASS'});
 }
 const overlapping=word(64)+word(64)+word(3)+pad('aabbcc'),shared=decodeAbi('0x'+overlapping,['bytes','bytes']),actual=decodeAbi('0x'+unwrap(await call('probeTwo',overlapping)),['bytes','bytes']);
 assert.deepEqual(shared.values.map(v=>v.value),['0xaabbcc','0xaabbcc']);assert.deepEqual(actual.values.map(v=>v.value),['0xaabbcc','0xaabbcc']);assert.equal(shared.canonical.status,'noncanonical');records.push({name:'overlapping-tails',input:overlapping,project_values:shared.values.map(v=>v.value),actual_values:actual.values.map(v=>v.value),status:'PASS'});
 const first=reference.cases.find(f=>f.name==='0-0'),dirty='0x'+word(256)+first.encoded.slice(66);let failed=false;try{await call('g0',dirty);}catch{failed=true;}assert.ok(failed);assert.equal(decodeAbi(dirty,first.types).status,'invalid');records.push({name:'dirty-uint8',status:'PASS'});
 const result={status:'PASS',cases:records.length,canonical_cases:128,reference_only_fixed_point_cases:16,compiler_version:solc.version(),anvil_sha256:sha(readFileSync(binary)),code_sha256:sha(Buffer.from(code.slice(2),'hex')),abi_module_sha256:sha(readFileSync(new URL('analysis-engines/ir/abi.mjs',root))),reference_fixture_sha256:sha(readFileSync(folder+'/fixtures.json')),
  external_http:0,scope:'actual Solidity ABI decode/re-encode; noncanonical acceptance is tied to this compiler and harness, not every target',genesis:vm.info,records};
 writeFileSync(new URL('results.json',out),JSON.stringify(result,null,2));console.log('ABI_SOLIDITY_RUNTIME_PASS',records.length,out.pathname);
}catch(error){writeFileSync(new URL('failure.json',out),JSON.stringify({message:error.message,stack:error.stack,completed_cases:records.length},null,2));throw error;}
finally{if(vm)await vm.close();}
