import assert from'node:assert/strict';import{createRequire}from'node:module';import{createHash}from'node:crypto';
import{readFileSync,writeFileSync,mkdirSync}from'node:fs';import{spawnSync}from'node:child_process';import{gunzipSync}from'node:zlib';
import{createServer}from'node:net';import{once}from'node:events';
import{scalarFlowFixture}from'../../analysis-engines/test/fixtures/scalar-flow.mjs';import{localGenesisEVM}from'./local_genesis_evm.mjs';

const root=new URL('../../',import.meta.url),base=new URL('acceptance/contract-analysis-aggregates/',root),
 sha=b=>createHash('sha256').update(b).digest('hex'),image=process.env.CONTRACT_TEST_IMAGE||readFileSync(new URL('engine-image.txt',base),'utf8').trim();
assert.match(image,/^sha256:[a-f0-9]{64}$/);
const out=new URL('source-execution/'+new Date().toISOString().replace(/\D/g,'')+'Z/',base);mkdirSync(out,{recursive:true});
const require=createRequire(new URL('analysis-engines/package.json',root)),{keccak_256}=require('@noble/hashes/sha3.js'),
 selector=s=>Buffer.from(keccak_256(Buffer.from(s))).toString('hex').slice(0,8),word=n=>BigInt(typeof n==='boolean'?Number(n):n).toString(16).padStart(64,'0'),
 sender='0x'+'1'.repeat(40),target='0x'+'2'.repeat(40),receiver='0x'+'4'.repeat(40),MAX=(1n<<256n)-1n,HUGE=9007199254740993n,WIDE=(1n<<128n)-1n;
const docker=['docker','run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--ulimit','core=0','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m'];
const write=(name,value)=>writeFileSync(new URL(name,out),typeof value==='string'||Buffer.isBuffer(value)?value:JSON.stringify(value,(_,v)=>typeof v==='bigint'?String(v):v));
async function port(){const server=createServer();server.listen(0,'127.0.0.1');await once(server,'listening');const value=server.address().port;server.close();await once(server,'close');return value;}
const portNumber=await port(),sourcePort=await port(),binary=process.env.ANVIL_BINARY||new URL('../contract-verification-20261015/analysis-engines/bin/anvil',root).pathname;
assert.equal(sha(readFileSync(binary)),'00f97668e1ae0fcca118fd7d2d1b6d25c0496447a523ca6bc2dfd10958c99604');
let sequence=0,vm,currentCase=null;const records=[];
const rpc=async(method,params=[])=>{const r=await fetch('http://127.0.0.1:'+portNumber,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(15000)}),body=await r.json();if(body.error)throw Error(method+':'+body.error.message);return body.result;};
function analyze(fixture,label){
 const input={...fixture.input,snapshot:{...fixture.input.snapshot,engine_digest:image}},run=spawnSync(docker[0],[...docker.slice(1),image],{input:JSON.stringify(input),timeout:180000,maxBuffer:64<<20});
 write(label+'-input.json',input);write(label+'-stdout.json',run.stdout);write(label+'-stderr.log',run.stderr);assert.equal(run.status,0,run.stderr.toString().slice(-1500));
 let result=JSON.parse(run.stdout);if(result.sections.engine_transport)result=JSON.parse(gunzipSync(Buffer.from(result.sections.engine_transport[0].content_base64,'base64')));
 assert.equal(result.snapshot.engine_digest,image);assert.equal(result.rule_version,'funds-semantics-19');assert.equal(result.sections.engines.find(e=>e.engine==='solc-ast').status,'completed');
 write(label+'-result.json',result);return result;
}
function scenarios(){
 const rows=[],add=(name,args,expected,reverted=false)=>rows.push({name,args:[...args,receiver],expected,reverted});
 for(const[due,requested]of[[0n,0n],[7n,3n],[7n,99n],[MAX,0n]])add('cap',[due,requested],[requested>0n&&requested<due?requested:due]);
 for(const[q,replacement]of[[0n,7n],[7n,0n],[HUGE,3n],[MAX,0n]])add('capture',[q,replacement],[q]);
 for(const[left,right]of[[0n,0n],[3n,7n],[HUGE,3n],[WIDE,1n<<120n]])add('tupleSwap',[left,right],[right*100n+left]);
 for(const[pick,left,right]of[[false,0n,7n],[true,HUGE,3n],[false,WIDE,1n<<64n],[true,MAX,1n]])add('choose',[pick,left,right],[pick?left:right]);
 for(const name of['shortAnd','shortOr'])for(const[pick,q]of[[false,0n],[true,HUGE],[false,HUGE],[true,MAX]])add(name,[pick,q],[(name==='shortAnd'?pick:!pick)?3n:q]);
 for(const[first,second]of[[0n,1n],[3n,7n],[HUGE,17n],[1n<<120n,WIDE]])add('modifierFrames',[first,second],[second,first]);
 for(const q of[0n,7n,HUGE,WIDE])add('callee',[q],[q,7n]);
 for(const[stop,due,requested]of[[true,7n,3n],[false,7n,3n],[false,HUGE,MAX],[false,0n,0n]])add('afterReturn',[stop,due,requested],stop?[]:[requested<due?requested:due]);
 for(const[fail,q]of[[false,0n],[true,7n],[false,HUGE],[true,MAX]])add('postRevert',[fail,q],[q],fail);
 assert.equal(rows.length,40);return rows;
}
function evaluate(expression,inputs){
 assert.equal(typeof expression,'string');assert.match(expression,/^[A-Za-z0-9_\s()+*/?:<>=!%&|.\-]+$/);
 // These existing fixtures use uint256 arithmetic within their specified
 // ranges. Narrow types and arithmetic failure are checked by compiled quotes.
 const casts={uint256:v=>BigInt.asUintN(256,v),uint:v=>BigInt.asUintN(256,v)},names=[...Object.keys(inputs),...Object.keys(casts)],allowed=new Set([...names,'true','false']);for(const name of expression.match(/[A-Za-z_]\w*/g)||[])assert.ok(allowed.has(name),'unexpected formula identifier '+name);
 const translated=expression.replace(/\b[0-9]+\b/g,'$&n');return new Function(...names,'return '+translated)(...Object.values(inputs),...Object.values(casts));
}
function nativeCalls(trace){const parse=value=>BigInt(value.startsWith('0x')?value:'0x'+value);
 return trace.structLogs.filter(l=>l.depth===1&&l.op==='CALL').map(l=>({pc:l.pc,target:'0x'+parse(l.stack.at(-2)).toString(16).padStart(40,'0').slice(-40),value:parse(l.stack.at(-3))}));
}
try{
 vm=await localGenesisEVM({binary,port:portNumber,sourcePort});await rpc('anvil_setBalance',[sender,'0x'+(10n**30n).toString(16)]);
 for(const version of(process.env.P2_AGGREGATE_VERSIONS||'0.4.26,0.6.12,0.7.6,0.8.30').split(',')){
  const fixture=scalarFlowFixture(version),result=analyze(fixture,'compiler-'+version),s=result.sections,methods=new Map(s.methods.map(m=>[m.name,m]));
  await rpc('anvil_setCode',[target,fixture.bytecode]);assert.equal(await rpc('eth_getCode',[target,'latest']),fixture.bytecode);
  for(const c of scenarios()){
   if(process.env.P2_AGGREGATE_METHODS&&!process.env.P2_AGGREGATE_METHODS.split(',').includes(c.name))continue;
   const label=version+'-'+records.length,m=methods.get(c.name);assert.ok(m);currentCase={version,method:c.name,arguments:c.args};
   const inputs=Object.fromEntries(m.parameters.map((p,i)=>[p.name,c.args[i]])),funds=s.funds.filter(f=>f.method_id===m.method_id&&f.kind==='native_payment'&&(f.conditions||[]).every(condition=>evaluate(condition,inputs)));
   const predicted=funds.map(f=>{assert.equal(f.amount_derivation_status,'represented_source_dependencies_resolved');assert.equal(evaluate(f.expanded_to_expression||f.to_expression,inputs).toLowerCase(),receiver);return evaluate(f.expanded_amount_expression,inputs);});
   assert.deepEqual(predicted,c.expected,'source quantity vs independently specified reference');
   await rpc('anvil_setBalance',[target,'0x'+MAX.toString(16)]);await rpc('anvil_setBalance',[receiver,'0x0']);
   const data='0x'+selector(m.signature)+c.args.map(word).join(''),tx=await rpc('eth_sendTransaction',[{from:sender,to:target,data,gas:'0x1c9c380',gasPrice:'0x77359400'}]);await rpc('evm_mine');
   const receipt=await rpc('eth_getTransactionReceipt',[tx]),trace=await rpc('debug_traceTransaction',[tx,{enableMemory:true,disableStorage:true}]),reverted=receipt.status==='0x0';write(label+'-receipt.json',receipt);write(label+'-trace.json',trace);
   assert.equal(reverted,c.reverted);assert.equal(trace.failed,reverted);const actual=nativeCalls(trace);assert.deepEqual(actual.map(c=>c.target),c.expected.map(()=>receiver));assert.deepEqual(actual.map(c=>c.value),c.expected);
   const paid=reverted?0n:c.expected.reduce((a,b)=>a+b,0n),balances={payer:BigInt(await rpc('eth_getBalance',[target,'latest'])),receiver:BigInt(await rpc('eth_getBalance',[receiver,'latest']))};
   assert.equal(balances.payer,MAX-paid);assert.equal(balances.receiver,paid);
   const record={...currentCase,transaction_hash:tx,calldata:data,reverted,expected_attempted_values:c.expected,actual_calls:actual,source_fund_ids:funds.map(f=>f.fund_id),
    amount_derivation_ids:funds.map(f=>f.amount_derivation_id),final_balances:balances,result_sha256:sha(JSON.stringify(result)),trace_sha256:sha(JSON.stringify(trace)),status:'PASS'};
   records.push(record);write(label+'-comparison.json',record);write('progress.json',{status:'RUNNING',image,cases:records.length,last:record});
  }
  console.log('P2_AGGREGATE_COMPILER_RUNTIME_PASS',version,records.length);
 }
 const complete=!process.env.P2_AGGREGATE_VERSIONS&&!process.env.P2_AGGREGATE_METHODS;if(complete)assert.equal(records.length,160);
 write('results.json',{status:'PASS',image,cases:records.length,complete_scenario_set:complete,records,genesis:vm.info,external_http:0,
  verifier_sha256:sha(readFileSync(new URL(import.meta.url))),fixture_sha256:sha(readFileSync(new URL('analysis-engines/test/fixtures/scalar-flow.mjs',root))),
  scope:'owned concrete native CALL quantities, recipient, order and final two-account balances including rollback; no full paths, fees, gas-cost or arbitrary-token settlement proof'});
 console.log('P2_AGGREGATE_REAL_EVM_PASS',records.length,out.pathname);
}catch(error){write('failure.json',{image,error:error.message,stack:error.stack,current_case:currentCase,completed_cases:records.length});throw error;}
finally{if(vm)await vm.close();}
