import{readFileSync,writeFileSync,mkdirSync,existsSync}from'node:fs';import{createHash}from'node:crypto';
import{createRequire}from'node:module';
import{Client}from'@modelcontextprotocol/sdk/client/index.js';import{StreamableHTTPClientTransport}from'@modelcontextprotocol/sdk/client/streamableHttp.js';
const root=new URL('../../',import.meta.url),out=new URL('acceptance/contract-analysis-general/corpus/materials/',root);mkdirSync(out,{recursive:true});
const vars=Object.fromEntries(readFileSync('/home/euripides/projects/blockchain-platform/deploy/.env','utf8').split('\n').filter(l=>l.includes('=')&&!l.startsWith('#')).map(l=>{const i=l.indexOf('=');return[l.slice(0,i),l.slice(i+1).trim().replace(/^["']|["']$/g,'')]}));
const client=new Client({name:'general-analysis-pinned-materials',version:'1.0.0'}),file=new URL('platform-material-rpc-ledger.json',out),ledger=existsSync(file)?JSON.parse(readFileSync(file,'utf8')):{started_at:'2026-10-16T22:30:47Z',calls:[],rpc_attempt_limit:18,prior_public_RPC_HTTP_failures:1};
const require=createRequire(new URL('analysis-engines/package.json',root)),{keccak_256}=require('@noble/hashes/sha3.js'),block=19000000,blockTag='0x121eac0',blockHash='0xcf384012b91b081230cdf17a3f7dd370d8e67056058af6b272b3d54aa2714fac';
const payload=r=>{const e=r.structuredContent||JSON.parse(r.content.find(c=>c.type==='text').text);if(r.isError||e.success===false)throw Error(JSON.stringify(e.error||e));return e.data??e;},sha=b=>createHash('sha256').update(b).digest('hex');
async function read(key,method,params){
 const path=new URL('platform-rpc-'+key+'.json',out),old=ledger.calls.find(x=>x.key===key&&x.status==='completed');
 if(old){const bytes=readFileSync(path);if(sha(bytes)!==old.sha256)throw Error('SAVED_RPC_INTEGRITY_FAILED');return JSON.parse(bytes);}
 if(ledger.calls.length+ledger.prior_public_RPC_HTTP_failures>=ledger.rpc_attempt_limit)throw Error('MATERIAL_RPC_BUDGET_EXHAUSTED');
 const record={key,method,params,status:'reserved',started_at:new Date().toISOString()};ledger.calls.push(record);writeFileSync(file,JSON.stringify(ledger,null,2));
 try{const result=payload(await client.callTool({name:'read_rpc',arguments:{chain:'ethereum',method,params}}));const bytes=Buffer.from(JSON.stringify(result));writeFileSync(path,bytes);record.status='completed';record.sha256=sha(bytes);record.response_bytes=bytes.length;console.log('PLATFORM_MATERIAL_RPC',key,method,typeof result.result==='string'?result.result.length:0);return result;}
 catch(error){record.status='failed';record.error=String(error.message).slice(0,300);throw error;}finally{record.finished_at=new Date().toISOString();writeFileSync(file,JSON.stringify(ledger,null,2));}
}
try{
 await client.connect(new StreamableHTTPClientTransport(new URL('http://127.0.0.1:18092/mcp'),{requestInit:{headers:{Authorization:'Bearer '+vars.MCP_API_KEY}}}));
 const mode=process.argv[2];
 if(mode==='inspect')await read('ampl-implementation','eth_getStorageAt',['0xd46ba6d942050d489dbd938a2c909a5d5039a161','0x7050c9e0f4ca769c69bd3a8ef740bc37934f8e2c036e5a723fd8ee048ed3f8c3','0x121eac0']);
 else if(mode==='acquire'){
  const ampl=await read('ampl-implementation','eth_getStorageAt',['0xd46ba6d942050d489dbd938a2c909a5d5039a161','0x7050c9e0f4ca769c69bd3a8ef740bc37934f8e2c036e5a723fd8ee048ed3f8c3',blockTag]);
  const steth=await read('steth-implementation','eth_call',[{to:'0xae7ab96520de3a18e5e111b5eaab095312d7fe84',data:'0x5c60da1b'},blockTag]);
  const proxies=[['ampl',ampl.result,'0xd46ba6d942050d489dbd938a2c909a5d5039a161','verified_proxy_source_slot'],['steth',steth.result,'0xae7ab96520de3a18e5e111b5eaab095312d7fe84','verified_proxy_source_getter']].map(([key,word,entry,basis])=>{
   if(!/^0x0{24}[0-9a-f]{40}$/i.test(word)||BigInt(word)===0n)throw Error('PROXY_IMPLEMENTATION_WORD_INVALID');return{key:key+'-implementation',code_address:'0x'+word.slice(-40).toLowerCase(),storage_address:entry,entry_address:entry,block_number:block,block_hash:blockHash,basis};
  });
  writeFileSync(new URL('proxy-code-bindings.json',out),JSON.stringify(proxies,null,2));
  const candidates=JSON.parse(readFileSync(new URL('new-root-candidates.json',out),'utf8')).filter(x=>!x.set.startsWith('supplemental'));
  for(const item of [...candidates,...proxies]){
   const address=item.address||item.code_address,result=await read('runtime-'+item.key,'eth_getCode',[address,blockTag]),code=result.result;
   if(!/^0x(?:[0-9a-f]{2})+$/i.test(code))throw Error('PINNED_RUNTIME_UNAVAILABLE:'+item.key);
   const bytes=Buffer.from(code.slice(2),'hex'),snapshot={chain:'ethereum',chain_id:'1',block_number:block,block_hash:blockHash,code_hash:'0x'+Buffer.from(keccak_256(bytes)).toString('hex'),code_sha256:sha(bytes),entry_address:item.entry_address||address,code_address:address,storage_address:item.storage_address||address,evm_rules:'shanghai',input_provenance:'platform_read_rpc_at_fixed_block',...Object.fromEntries(['group','set','basis'].filter(k=>item[k]).map(k=>[k,item[k]]))};
   const dir=new URL(item.key+'/',out);mkdirSync(dir,{recursive:true});writeFileSync(new URL('runtime.hex',dir),code);writeFileSync(new URL('snapshot.json',dir),JSON.stringify(snapshot,null,2));
  }
  console.log('PINNED_CORPUS_MATERIALS_COMPLETE',candidates.length,'ROOTS',proxies.length,'IMPLEMENTATIONS');
 }
 else throw Error('UNKNOWN_PHASE');
}finally{await client.close();}
