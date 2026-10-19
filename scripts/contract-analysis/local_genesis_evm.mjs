import {createServer} from 'node:http';
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {createRequire} from 'node:module';
const require=createRequire(new URL('../../analysis-engines/package.json',import.meta.url)),{keccak_256}=require('@noble/hashes/sha3.js');
const emptyRoot='0x'+Buffer.from(keccak_256(Uint8Array.of(0x80))).toString('hex');
const sleep=ms=>new Promise(r=>setTimeout(r,ms));

// The only upstream state is an explicitly empty, hash-checked local genesis.
// Fork mode keeps the pinned Anvil execution path equal to historical replay;
// the Shanghai gas canary below detects its non-fork calldata-floor discrepancy.
export async function localGenesisEVM({binary,port,sourcePort}){
 const reads=[],base=['--host','127.0.0.1','--port',String(port),'--accounts','0','--threads','2','--chain-id','1','--hardfork','shanghai','--timestamp','1700000000','--no-mining','--auto-impersonate','--steps-tracing','--silent','--disable-default-create2-deployer'];
 let sequence=0;const rpc=async(method,params=[])=>{
  const r=await fetch('http://127.0.0.1:'+port,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(10000)}),d=await r.json();
  if(d.error)throw Error(method+':'+d.error.message);return d.result;
 };
 async function start(extra=[]){
  const child=spawn(binary,[...base,...extra],{stdio:['ignore','ignore','pipe']});let diagnostics='';child.stderr.on('data',b=>{if(diagnostics.length<8192)diagnostics+=b.toString();});
  try{for(let i=0;i<100;i++){if(child.exitCode!==null)throw Error('OWNED_EVM_EXITED:'+diagnostics);try{await rpc('eth_chainId');return child;}catch{await sleep(50);}}throw Error('OWNED_EVM_STARTUP_TIMEOUT');}
  catch(error){if(child.exitCode===null){child.kill('SIGTERM');await once(child,'exit');}throw error;}
 }
 let bootstrap=await start(),genesis;
 try{genesis=await rpc('eth_getBlockByNumber',['0x0',false]);if(genesis.number!=='0x0'||genesis.stateRoot!==emptyRoot||genesis.gasUsed!=='0x0')throw Error('GENESIS_IS_NOT_EMPTY');}
 finally{bootstrap.kill('SIGTERM');await once(bootstrap,'exit');}
 const server=createServer(async(req,res)=>{
  const parts=[];let length=0;for await(const p of req){length+=p.length;if(length>1<<20){res.writeHead(413);res.end();return;}parts.push(p);}
  let d;try{d=JSON.parse(Buffer.concat(parts).toString('utf8'));}catch{res.writeHead(400);res.end();return;}
  const handle=q=>{
   const p=q.params||[],m=q.method,result={jsonrpc:'2.0',id:q.id};if(reads.length>=4096)return{...result,error:{code:-32000,message:'GENESIS_RPC_BUDGET'}};reads.push({method:m,params:p});
   let value;
   if(m==='eth_chainId')value='0x1';else if(m==='net_version')value='1';else if(m==='eth_blockNumber')value='0x0';
   else if(m==='eth_getBlockByNumber'&&['0x0','latest'].includes(p[0])||m==='eth_getBlockByHash'&&p[0]===genesis.hash)value=genesis;
   else if(m==='eth_getBalance'||m==='eth_getTransactionCount')value='0x0';
   else if(m==='eth_getCode')value='0x';else if(m==='eth_getStorageAt')value='0x'+'0'.repeat(64);
   else if(m==='eth_gasPrice')value='0x77359400';else return{...result,error:{code:-32601,message:'UNAVAILABLE_EMPTY_GENESIS_METHOD'}};
   return{...result,result:value};
  };
  res.setHeader('content-type','application/json');res.end(JSON.stringify(Array.isArray(d)?d.map(handle):handle(d)));
 });
 server.listen(sourcePort,'127.0.0.1');await once(server,'listening');let child;
 try{
  child=await start(['--fork-url','http://127.0.0.1:'+sourcePort,'--fork-block-number','0','--no-fork-node-info','--no-bal']);
  const sender='0x'+'9'.repeat(40),target='0x'+'8'.repeat(40);await rpc('anvil_setBalance',[sender,'0x'+(10n**30n).toString(16)]);await rpc('anvil_setCode',[target,'0x600060005500']);await rpc('anvil_setStorageAt',[target,'0x'+'0'.repeat(64),'0x'+'0'.repeat(63)+'1']);
  const tx=await rpc('eth_sendTransaction',[{from:sender,to:target,data:'0x'+'ff'.repeat(128),gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');const receipt=await rpc('eth_getTransactionReceipt',[tx]);
  const expected=21000+128*16+2*3+5000-4800;if(BigInt(receipt.gasUsed)!==BigInt(expected))throw Error('SHANGHAI_GAS_CANARY_FAILED:'+receipt.gasUsed);
  const info={mode:'empty_genesis_fork',genesis,empty_state_root:emptyRoot,gas_canary:{tx_hash:tx,observed:Number(BigInt(receipt.gasUsed)),expected,eip7623_floor:21000+128*4*10},node_info:await rpc('anvil_nodeInfo'),reads};
  return{child,info,close:async()=>{if(child.exitCode===null){child.kill('SIGTERM');await once(child,'exit');}server.close();await once(server,'close');}};
 }catch(error){if(child?.exitCode===null){child.kill('SIGTERM');await once(child,'exit');}server.close();await once(server,'close');throw error;}
}
