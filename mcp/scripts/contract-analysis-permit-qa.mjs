import{readFileSync,writeFileSync,mkdirSync}from'node:fs';import{createHash}from'node:crypto';
import{Client}from'@modelcontextprotocol/sdk/client/index.js';import{StreamableHTTPClientTransport}from'@modelcontextprotocol/sdk/client/streamableHttp.js';
const root=new URL('../../',import.meta.url),out=new URL((process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis-permit-nonces/service-validation')+'/',root);mkdirSync(out,{recursive:true});
const vars=Object.fromEntries(readFileSync('/home/euripides/projects/blockchain-platform/deploy/.env','utf8').split('\n').filter(l=>l.includes('=')&&!l.startsWith('#')).map(l=>{const i=l.indexOf('=');return[l.slice(0,i),l.slice(i+1).trim().replace(/^["']|["']$/g,'')]}));
const image=readFileSync(new URL('engine-image.txt',out),'utf8').trim(),checks=[],client=new Client({name:'permit-candidate-acceptance',version:'1.0.0'});
function check(scenario,ok,evidence){checks.push({scenario,status:ok?'PASS':'FAIL',evidence});writeFileSync(new URL('permit-mcp-qa.json',out),JSON.stringify(checks,null,2));console.log(scenario,ok?'PASS':'FAIL');if(!ok)throw Error(scenario);}
function payload(r){const e=r.structuredContent||JSON.parse(r.content.find(c=>c.type==='text').text);if(r.isError||e.success===false)throw Error(JSON.stringify(e.error||e));return e.data??e;}
const call=async(name,args={})=>payload(await client.callTool({name,arguments:args}));
async function all(name,args){let items=[],cursor;do{const page=await call(name,{...args,page_size:200,...(cursor?{cursor}:{})});items.push(...page.items);cursor=page.has_more?page.next_cursor:null;if(!cursor)check(name+'-all-pages',items.length===page.record_count);}while(cursor);return items;}
async function submit(address,key,{tx_hashes,max_contracts=1,max_state_reads=8}={}){
 const submitted=await call('analyze_contract',{chain:'ethereum',address,block:19000000,...(tx_hashes?{tx_hashes}:{}),idempotency_key:'permit-qa-'+image.slice(7,19)+'-'+key,budget:{max_contracts,max_rpc_requests:20,max_state_reads,max_credits:20000}});check(key+'-submitted',Boolean(submitted.analysis_id));
 let job;for(let i=0;i<450;i++){job=await call('get_contract_analysis',{analysis_id:submitted.analysis_id});if(!['queued','running'].includes(job.status))break;await new Promise(r=>setTimeout(r,2000));}
 check(key+'-completed-with-candidate',job.status==='completed'&&job.metadata.rule_version==='funds-semantics-13'&&job.metadata.metrics.engine_digest===image,{analysis_id:job.analysis_id,status:job.status,error:job.error_code});
 writeFileSync(new URL(key+'-mcp-job.json',out),JSON.stringify(job,null,2));return job;
}
async function artifact(id,item){let offset=0,parts=[];for(;;){const r=await client.callTool({name:'get_contract_analysis_artifact',arguments:{analysis_id:id,artifact_id:item.artifact_id,offset,chunk_size:65536}}),meta=payload(r),resource=r.content.find(c=>c.type==='resource')?.resource,bytes=Buffer.from(resource?.blob||'','base64');check('nonce-proof-chunk-checksum',meta.offset===offset&&bytes.length===meta.byte_length&&createHash('sha256').update(bytes).digest('hex')===meta.chunk_sha256);parts.push(bytes);if(meta.complete)break;offset=meta.next_offset;}const bytes=Buffer.concat(parts);check('nonce-proof-whole-checksum',bytes.length===item.byte_size&&createHash('sha256').update(bytes).digest('hex')===item.sha256);return bytes;}
try{
 await client.connect(new StreamableHTTPClientTransport(new URL(process.env.CONTRACT_QA_MCP||'http://127.0.0.1:18092/mcp'),{requestInit:{headers:{Authorization:'Bearer '+vars.MCP_API_KEY}}}));
 const caps=await call('get_contract_analysis_capabilities');check('candidate-capability-identity',caps.semantic_rules_version==='funds-semantics-13'&&caps.engine_digest===image&&caps.state_model_verification.authorization_models.includes('permit_nonce_monotonicity'));
 check('nonce-and-whole-authorization-scopes-stay-separate',caps.state_model_verification.nonce_state_analysis.replay_protection_with_domain_history==='not_proved'&&caps.state_model_verification.whole_authorization_proved===false);
 const pair=await submit('0xB4e16d0168e52d35CaCD2c6185b44281Ec28C9Dc','pair');
 const models=await all('get_contract_analysis',{analysis_id:pair.analysis_id,section:'authorization_models'}),nonce=models.find(m=>m.kind==='permit_nonce_monotonicity');
 check('real-contract-nonce-model-is-saved',Boolean(nonce)&&Number.isInteger(nonce.nonce_state_ast_id)&&nonce.entry_coverage.length>0,{kind:nonce?.kind,status:nonce?.status,unresolved:nonce?.unresolved});
 check('unresolved-entry-dependencies-block-nonce-closure',nonce.unresolved.length===0||nonce.complete===false);
 const methods=await all('list_contract_methods',{analysis_id:pair.analysis_id}),method=methods.find(m=>m.signature==='permit(address,address,uint256,uint256,uint8,bytes32,bytes32)'&&m.verified_signature);
 check('permit-method-source-identity',Boolean(method));
 const detail=await call('get_contract_method_analysis',{analysis_id:pair.analysis_id,method_id:method.method_id});check('permit-method-readable',detail.items[0].method_id===method.method_id);
 const flows=await all('get_contract_execution_flow',{analysis_id:pair.analysis_id,method_id:method.method_id});check('permit-execution-flow-readable',flows.length>0&&flows.every(e=>e.evidence_ids?.length));
 const obligations=await all('get_contract_analysis',{analysis_id:pair.analysis_id,section:'verification_obligations'}),nonceObligations=obligations.filter(p=>p.model_id===nonce.model_id),obligation=nonceObligations.find(p=>p.status==='counterexample')||nonceObligations[0];
 check('nonce-property-remains-linked',Boolean(obligation)&&nonce.obligations.includes(obligation.property_id));
 check('nonce-counterexample-is-selected',nonce.status!=='counterexample'||obligation.status==='counterexample');
 const files=await all('get_contract_analysis',{analysis_id:pair.analysis_id,section:'artifacts'}),file=files.find(a=>a.artifact_id===obligation.proof_artifact_id);check('nonce-proof-artifact-remains-linked',Boolean(file));
 const proof=JSON.parse((await artifact(pair.analysis_id,file)).toString('utf8'));check('saved-proof-matches-MCP-property',proof.property.property_id===obligation.property_id&&proof.property.model_id===nonce.model_id&&proof.property.proof_artifact_id===file.artifact_id&&proof.property.status===obligation.status&&proof.query.property_id===obligation.property_id);
 check('nonce-counterexample-survives-artifact-read',obligation.status!=='counterexample'||Boolean(proof.query.violation&&proof.property.counterexample?.values_smt&&proof.property.counterexample.values_smt===obligation.counterexample?.values_smt&&proof.property.solver?.decision==='sat'));
 writeFileSync(new URL('permit-mcp-model.json',out),JSON.stringify({analysis_id:pair.analysis_id,model:nonce,obligation,artifact:file,proof},null,2));
 const tx='0xecdc668e16ada76f439b09c9b8c2faa7b76f80244933193c7691ac797e26eae7',execution=await submit('0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2','selected-transaction',{tx_hashes:[tx],max_contracts:3,max_state_reads:24});
 const explanation=await call('explain_contract_transaction',{analysis_id:execution.analysis_id,tx_hash:tx});check('MCP-selected-transaction-explained',explanation.items.length>0&&explanation.items.every(x=>x.tx_hash===tx),{analysis_id:execution.analysis_id,record_count:explanation.record_count});
 const replays=await all('get_contract_analysis',{analysis_id:execution.analysis_id,section:'replays'});check('selected-transaction-full-replay',replays.some(r=>r.tx_hash===tx&&r.complete&&r.differences.length===0));
 const before=readFileSync(new URL('mcp-analysis-id.txt',out),'utf8').trim(),compared=await call('compare_contract_versions',{analysis_id:execution.analysis_id,compare_analysis_id:before,page_size:200});check('version-comparison-remains-readable',Array.isArray(compared.items));
 check('permit-MCP-complete',true);
}finally{await client.close();}
