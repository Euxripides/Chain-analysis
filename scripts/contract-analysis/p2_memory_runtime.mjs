import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {createServer} from 'node:net';
import {createHash} from 'node:crypto';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {ByteMemory,memorySource} from '../../analysis-engines/ir/memory.mjs';

const root=new URL('../../',import.meta.url),out=new URL(process.env.P2_MEMORY_OUT||'acceptance/contract-analysis-memory/byte-semantics/'+new Date().toISOString().replace(/\D/g,'')+'Z/',root);
mkdirSync(new URL('../',out),{recursive:true});mkdirSync(out);const sha=b=>createHash('sha256').update(b).digest('hex');
const binary=process.env.ANVIL_BINARY||new URL('../contract-verification-20261015/analysis-engines/bin/anvil',root).pathname;
assert.equal(sha(readFileSync(binary)),'00f97668e1ae0fcca118fd7d2d1b6d25c0496447a523ca6bc2dfd10958c99604');
const reservation=createServer();reservation.listen(0,'127.0.0.1');await once(reservation,'listening');const port=reservation.address().port;reservation.close();await once(reservation,'close');
const target='0x'+'2'.repeat(40),callee='0x'+'3'.repeat(40),sender='0x'+'1'.repeat(40);
const child=spawn(binary,['--host','127.0.0.1','--port',String(port),'--accounts','0','--threads','2','--chain-id','31337','--hardfork','cancun','--silent','--disable-default-create2-deployer'],{stdio:['ignore','ignore','pipe']});
let diagnostics='',sequence=0;child.stderr.on('data',b=>{if(diagnostics.length<8192)diagnostics+=b;});
const rpc=async(method,params=[])=>{const response=await fetch('http://127.0.0.1:'+port,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(10000)}),d=await response.json();if(d.error)throw Error(method+':'+d.error.message);return d.result;};
const push=n=>{let value=BigInt(n).toString(16);if(value.length%2)value='0'+value;assert.ok(value.length<=64);return(0x5f+value.length/2).toString(16)+value;};
const store=(at,value)=>push(value)+push(at)+'52',store8=(at,value)=>push(value)+push(at)+'53';
const copy=(to,from,n)=>push(n)+push(from)+push(to)+'5e',ret=(at,n)=>push(n)+push(at)+'f3';
const hex=value=>typeof value==='string'?value.replace(/^0x/,''):Buffer.from(value).toString('hex');
const concrete=(memory,at,n)=>{const read=memory.read(at,n);assert.equal(read.status,'exact');assert.equal(read.cases.length,1);return Buffer.from(read.cases[0].bytes.map(b=>{assert.equal(b.kind,'constant');return b.value;}));};
const records=[];
async function execute(name,code,expected,model,{data='0x',fails=false}={}) {
 assert.ok(/^[a-f0-9]+$/i.test(code));await rpc('anvil_setCode',[target,'0x'+code]);
 const request={from:sender,to:target,data,gas:'0x1c9c380'},trace=await rpc('debug_traceCall',[request,'latest',{enableMemory:true,disableStorage:true,enableReturnData:true}]);
 const raw=Buffer.from(JSON.stringify(trace)),traceFile=name+'-trace.json';writeFileSync(new URL(traceFile,out),raw);
 let actual,error=null;try{actual=await rpc('eth_call',[request,'latest']);}catch(e){error=String(e.message);}
 if(fails){assert.ok(error,name+' must fail');assert.equal(trace.failed,true,name+' trace must fail');}
 else{assert.equal(error,null,name);assert.equal(hex(actual),hex(expected),name+' EVM/reference');assert.equal(hex(model),hex(expected),name+' model/reference');assert.equal(trace.failed,false);assert.equal(hex(trace.returnValue),hex(expected));}
 records.push({name,bytecode:'0x'+code,code_sha256:sha(Buffer.from(code,'hex')),calldata:data,expected:hex(expected),actual:actual??null,model:hex(model||''),expected_failure:fails,error,trace_sha256:sha(raw),trace_file:traceFile,status:'PASS'});
 writeFileSync(new URL('results.json',out),JSON.stringify({status:'RUNNING',records},null,2));
}
try{
 for(let i=0;i<100;i++){if(child.exitCode!==null)throw Error('OWNED_EVM_EXITED:'+diagnostics);try{await rpc('eth_chainId');break;}catch{await new Promise(r=>setTimeout(r,50));if(i===99)throw Error('OWNED_EVM_STARTUP_TIMEOUT');}}
 assert.equal(await rpc('eth_chainId'),'0x7a69');const node=await rpc('anvil_nodeInfo');
 let seed=0x4171d53;const next=()=>{seed^=seed<<13;seed^=seed>>>17;seed^=seed<<5;return seed>>>0;};
 for(let sample=0;sample<32;sample++){
  let code='',model=new ByteMemory({frameId:'frame'}),reference=Buffer.alloc(256);
  for(let step=0;step<12;step++){
   const at=next()%160;
   if(step%3===0){let v=0n;for(let n=0;n<8;n++)v=(v<<32n)|BigInt(next());code+=store(at,v);model=model.mstore(at,v);Buffer.from(v.toString(16).padStart(64,'0'),'hex').copy(reference,at);}
   else if(step%3===1){const v=BigInt(next());code+=store8(at,v);model=model.mstore8(at,v);reference[at]=Number(v&255n);}
   else{const from=next()%160,n=next()%56,snapshot=Buffer.from(reference.subarray(from,from+n));code+=copy(at,from,n);model=model.mcopy(at,from,n);snapshot.copy(reference,at);}
  }
  await execute('mixed-'+sample,code+ret(0,224),reference.subarray(0,224),concrete(model,0,224));
 }
 for(const[to,from,n]of[[1,0,31],[0,1,31],[3,7,19],[7,3,19]]){
  const raw=Buffer.from(Array.from({length:64},(_,i)=>i)),model=new ByteMemory({frameId:'frame'}).writeBytes(0,raw).mcopy(to,from,n),reference=Buffer.from(raw),snapshot=Buffer.from(raw.subarray(from,from+n));snapshot.copy(reference,to);
  const code=store(0,BigInt('0x'+raw.subarray(0,32).toString('hex')))+store(32,BigInt('0x'+raw.subarray(32).toString('hex')))+copy(to,from,n)+ret(0,64);
  await execute('overlap-'+to+'-'+from,code,reference,concrete(model,0,64));
 }
 const msize=new ByteMemory({frameId:'frame'}).mcopy(0,96,2);await execute('mcopy-source-expands-msize',copy(0,96,2)+'59'+push(0)+'52'+ret(0,32),Buffer.from('80'.padStart(64,'0'),'hex'),Buffer.from(BigInt(msize.extent).toString(16).padStart(64,'0'),'hex'));
 const data=Buffer.from('aabbcc','hex'),source=memorySource('calldata',{sourceId:'entry-calldata',frameId:'frame',bytes:data});
 await execute('calldata-copy-padding',push(6)+push(1)+push(7)+'37'+ret(0,32),Buffer.concat([Buffer.alloc(7),Buffer.from('bbcc','hex'),Buffer.alloc(23)]),concrete(new ByteMemory({frameId:'frame'}).copyFrom(7,1,6,source),0,32),{data:'0x'+data.toString('hex')});
 const code=push(9)+push(0)+push(13)+'39'+ret(0,32),codeBytes=Buffer.from(code,'hex'),codeReference=Buffer.alloc(32);codeBytes.subarray(0,9).copy(codeReference,13);
 await execute('codecopy-identity',code,codeReference,concrete(new ByteMemory({frameId:'frame'}).copyFrom(13,0,9,memorySource('code',{sourceId:sha(codeBytes),frameId:'frame',bytes:codeBytes})),0,32));
 for(const reverting of[false,true]){
  const code=store(0,0xaabbccn)+push(3)+push(29)+(reverting?'fd':'f3');await rpc('anvil_setCode',[callee,'0x'+code]);
  const call=push(0)+push(0)+push(0)+push(0)+push(BigInt(callee))+push(100000)+'fa50';
  const source=memorySource('returndata',{sourceId:'last-call-'+reverting,frameId:'frame',bytes:data});
  const m=new ByteMemory({frameId:'frame'}).copyFrom(7,1,2,source),ref=Buffer.alloc(32);Buffer.from('bbcc','hex').copy(ref,7);
  await execute('returndata-'+reverting,call+push(2)+push(1)+push(7)+'3e'+ret(0,32),ref,concrete(m,0,32));
  await execute('returndata-zero-at-end-'+reverting,call+push(0)+push(3)+push(0)+'3e'+ret(0,0),Buffer.alloc(0),Buffer.alloc(0));
  assert.throws(()=>new ByteMemory({frameId:'frame'}).copyFrom(0,4,0,source),/RETURNDATA_COPY_OUT_OF_BOUNDS/);
  await execute('returndata-zero-past-end-'+reverting,call+push(0)+push(4)+push(0)+'3e00','',null,{fails:true});
 }
 const calleeCode=Buffer.from(store(0,0xaabbccn)+ret(29,3),'hex');await rpc('anvil_setCode',[callee,'0x'+calleeCode.toString('hex')]);
 const external=new ByteMemory({frameId:'frame'}).copyFrom(3,1,9,memorySource('external_code',{sourceId:sha(calleeCode),frameId:'frame',bytes:calleeCode})),expected=Buffer.alloc(32);calleeCode.subarray(1,10).copy(expected,3);
 await execute('extcodecopy-observed-identity',push(9)+push(1)+push(3)+push(BigInt(callee))+'3c'+ret(0,32),expected,concrete(external,0,32));
 const result={status:'PASS',scope:'byte memory and EVM returned bytes; not full CFG/ABI/MCP or gas proof',memory_module_sha256:sha(readFileSync(new URL('analysis-engines/ir/memory.mjs',root))),runtime_script_sha256:sha(readFileSync(new URL(import.meta.url))),anvil_binary_sha256:sha(readFileSync(binary)),node_info:node,external_http:0,chain_writes:'owned local Anvil state only',cases:records.length,records};
 writeFileSync(new URL('results.json',out),JSON.stringify(result,null,2));console.log('P2_MEMORY_REAL_EVM_PASS',records.length);
}catch(error){writeFileSync(new URL('failure.json',out),JSON.stringify({message:String(error.message),stack:error.stack,completed_cases:records.length},null,2));throw error;}
finally{if(child.exitCode===null){child.kill('SIGTERM');await once(child,'exit');}}
