import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {readFileSync,writeFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const require=createRequire(new URL('../../analysis-engines/package.json',import.meta.url)),solc=require('solc-0.8.30'),{keccak_256}=require('@noble/hashes/sha3.js');
const out=new URL('../../acceptance/contract-analysis-deepening/deepening-runtime/callcode-context.json',import.meta.url),user='0x'+'1'.repeat(40),word=v=>BigInt(v).toString(16).padStart(64,'0');
const source='pragma solidity ^0.8.24; contract Target {fallback() external payable {assembly {mstore(0,caller()) return(0,32)}}} contract Caller {function go(address target,bool delegated) external payable returns(address who){assembly {let ok:=0 switch delegated case 0 {ok:=callcode(gas(),target,callvalue(),0,0,0,32)} default {ok:=delegatecall(gas(),target,0,0,0,32)} if iszero(ok){revert(0,0)} who:=mload(0)}}}';
const input={language:'Solidity',sources:{'Context.sol':{content:source}},settings:{evmVersion:'shanghai',outputSelection:{'*':{'*':['evm.bytecode','evm.methodIdentifiers']}}}},output=JSON.parse(solc.compile(JSON.stringify(input)));
if((output.errors||[]).some(e=>e.severity==='error'))throw new Error(JSON.stringify(output.errors));
let id=0;async function rpc(method,params=[]){const response=await fetch('http://127.0.0.1:18750',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++id,method,params})});const data=await response.json();if(data.error)throw new Error(data.error.message);return data.result;}
async function send(to,data,value='0x0'){const tx=await rpc('eth_sendTransaction',[{from:user,...(to?{to}:{}),data,value,gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');const receipt=await rpc('eth_getTransactionReceipt',[tx]);if(receipt.status!=='0x1')throw new Error('context transaction reverted');return receipt;}
const anvil=spawn(fileURLToPath(new URL('../../analysis-engines/bin/anvil',import.meta.url)),['--host','127.0.0.1','--port','18750','--accounts','0','--chain-id','1','--hardfork','shanghai','--no-mining','--auto-impersonate','--silent','--disable-default-create2-deployer'],{stdio:'ignore'});
try{
 let ready=false;for(let i=0;i<100;i++){try{await rpc('eth_chainId');ready=true;break;}catch{await new Promise(r=>setTimeout(r,50));}}if(!ready)throw new Error('local EVM not ready');
 await rpc('anvil_setBalance',[user,'0x'+(10n**30n).toString(16)]);
 const target=(await send(null,'0x'+output.contracts['Context.sol'].Target.evm.bytecode.object)).contractAddress,caller=(await send(null,'0x'+output.contracts['Context.sol'].Caller.evm.bytecode.object)).contractAddress;
 const selector='0x'+Buffer.from(keccak_256(Buffer.from('go(address,bool)'))).toString('hex').slice(0,8),records=[];
 for(const delegated of [false,true]){const receipt=await send(caller,selector+word(target)+word(delegated?1:0),'0x7'),trace=await rpc('debug_traceTransaction',[receipt.transactionHash,{tracer:'callTracer'}]),expected=delegated?user:caller,kind=delegated?'DELEGATECALL':'CALLCODE';
  if(trace.calls?.[0]?.type!==kind||!trace.calls[0].output.toLowerCase().endsWith(expected.slice(2)))throw new Error('actual EVM context mismatch');
  records.push({simulation:true,source,tx_hash:receipt.transactionHash,block_hash:receipt.blockHash,trace,expected_caller:expected,expected_storage:caller,call_type:kind});console.log(kind,'ACTUAL_CALLER_MATCHES',expected);
 }writeFileSync(out,JSON.stringify(records,null,2));
}finally{anvil.kill('SIGTERM');if(anvil.exitCode===null)await once(anvil,'exit');}
