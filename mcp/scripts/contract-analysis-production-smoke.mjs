import{readFileSync,writeFileSync}from'node:fs';import assert from'node:assert/strict';
import{Client}from'@modelcontextprotocol/sdk/client/index.js';import{StreamableHTTPClientTransport}from'@modelcontextprotocol/sdk/client/streamableHttp.js';
const root=new URL('../../',import.meta.url),out=new URL('acceptance/contract-analysis-heap-writes/release/production-smoke.json',root),image=readFileSync(new URL('acceptance/contract-analysis-heap-writes/engine-image.txt',root),'utf8').trim();
const vars=Object.fromEntries(readFileSync('/home/euripides/projects/blockchain-platform/deploy/.env','utf8').split('\n').filter(l=>l.includes('=')&&!l.startsWith('#')).map(l=>{const i=l.indexOf('=');return[l.slice(0,i),l.slice(i+1).trim().replace(/^["']|["']$/g,'')]}));
const client=new Client({name:'contract-analysis-production-smoke',version:'1.0.0'}),checks=[];
const check=(name,ok,evidence)=>{checks.push({name,status:ok?'PASS':'FAIL',evidence});writeFileSync(out,JSON.stringify({checks},null,2));if(!ok)throw Error(name);console.log(name,'PASS');};
const payload=r=>r.structuredContent||JSON.parse(r.content.find(c=>c.type==='text').text);
const call=async(name,args={})=>{const envelope=payload(await client.callTool({name,arguments:args}));return envelope.data??envelope;};
await client.connect(new StreamableHTTPClientTransport(new URL('http://127.0.0.1:18085/mcp'),{requestInit:{headers:{Authorization:'Bearer '+vars.MCP_API_KEY}}}));
const tools=(await client.listTools()).tools;
check('production-mcp-exposes-120-tools',tools.length===120,tools.length);
const capability=await call('get_contract_analysis_capabilities');
check('production-runs-the-certified-rule-and-image',capability.semantic_rules_version==='funds-semantics-20'&&capability.engine_digest===image,{rule:capability.semantic_rules_version,engine:capability.engine_digest});
check('production-still-declares-unproved-authorization-and-economics',capability.state_model_verification.whole_authorization_proved===false&&capability.state_model_verification.whole_protocol_economics_proved===false);
const source=`pragma solidity 0.8.24;contract Accounts{struct Account{uint256 balance;uint256 debt;}mapping(address=>Account)public accounts;uint256 public total;function deposit(uint256 amount)external{Account storage account=accounts[msg.sender];account.balance+=amount;total+=amount;}function quote(uint256 left,uint256 right)public pure returns(uint256 sum,uint256 product){return(left+right,left*right);}function pair(uint256 left)external view returns(uint256,uint256){return quote(total,left);}}`;
const request={input:{kind:'source',source:{language:'Solidity',files:{'Accounts.sol':source},compiler_version:'0.8.24+commit.e11b9ed9',target:'Accounts.sol:Accounts',settings:{optimizer:{enabled:false},evmVersion:'shanghai'}}},engines:['whatsabi','sevm','solc-ast'],budget:{max_contracts:1,max_rpc_requests:10,max_state_reads:1,max_records:20000,max_credits:5000,timeout_seconds:300},idempotency_key:'production-smoke-'+new Date().toISOString().slice(0,10)};
const submitted=await call('analyze_contract',request);let job;
for(let attempt=0;attempt<600;attempt++){
  job=await call('get_contract_analysis',{analysis_id:submitted.analysis_id});
  if(!['queued','running'].includes(job.status))break;
  await new Promise(resolve=>setTimeout(resolve,500));
}
const metrics=job.metadata?.metrics||{};
check('production-code-only-analysis-completed',job.status==='completed'&&job.metadata?.rule_version==='funds-semantics-20'&&metrics.engine_digest===image,{analysis_id:submitted.analysis_id,status:job.status,error:job.error_message});
check('production-code-only-analysis-used-no-provider-requests',metrics.rpc_http_requests===0&&Number(metrics.credits)===0&&job.metadata.snapshot.identity_kind==='code_only',{rpc:metrics.rpc_http_requests,credits:String(metrics.credits)});
const methods=await call('list_contract_methods',{analysis_id:submitted.analysis_id,page_size:50});
check('production-analysis-returns-its-declared-methods',methods.record_count===job.metadata.sections.methods&&methods.items.length>0,{methods:methods.record_count});
const previous=process.env.CONTRACT_PRODUCTION_EXISTING_ANALYSIS;
if(previous){
  const saved=await call('get_contract_analysis',{analysis_id:previous});
  check('production-keeps-previous-tasks-readable',saved.analysis_id===previous&&saved.metadata?.rule_version==='funds-semantics-8',{analysis_id:previous,status:saved.status});
}
writeFileSync(out,JSON.stringify({image,generated_utc:new Date().toISOString(),checks},null,2));
console.log('PRODUCTION_SMOKE_PASS',submitted.analysis_id);
