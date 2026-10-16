"""Verify the released service, old tasks, new proofs and public assets."""
import base64,hashlib,json,os,pathlib,subprocess,time,uuid
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-verification-20261015'
OUT=ROOT/'acceptance/contract-analysis-verification/release';OUT.mkdir(parents=True,exist_ok=True)
env=os.environ.copy();env.update({'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+env['PATH'],'GOMAXPROCS':'2','CONTRACT_QA_OUT':'acceptance/contract-analysis-verification/release','CONTRACT_QA_API':'https://aurore.online','E2E_BROWSER_CHANNEL':'chromium'})
def run(name,args,cwd=ROOT):
 with (OUT/(name+'.log')).open('w')as log:p=subprocess.run(args,cwd=cwd,env=env,stdout=log,stderr=subprocess.STDOUT)
 print('PRODUCTION',name,p.returncode,flush=True)
 if p.returncode:print((OUT/(name+'.log')).read_text()[-6000:],flush=True);raise RuntimeError(name)
run('mcp',['node','mcp/scripts/contract-production-qa.mjs'])
run('integrity',['python3','scripts/contract-analysis/production_integrity_qa.py'])
run('render',['node','frontend/scripts/contract-production-render-qa.mjs'])
os.environ['CONTRACT_QA_API']='https://aurore.online'
from qa_support import Client,dotenv
values=dotenv('/home/euripides/projects/blockchain-platform/deploy/.env');client=Client(values['GPT_ACTIONS_API_KEY']);rows=[]
def check(name,ok,evidence=None):
 rows.append({'scenario':name,'status':'PASS'if ok else'FAIL','evidence':evidence});(OUT/'production-verification-qa.json').write_text(json.dumps(rows,indent=2));print(name,rows[-1]['status'],flush=True)
 if not ok:raise AssertionError(name)
image=(OUT.parent/'engine-image.txt').read_text().strip()
cap=client.must('/api/v1/analysis/contracts/get_contract_analysis_capabilities',{})
check('public-capabilities-are-current',cap['engine_digest']==image and cap['semantic_rules_version']=='funds-semantics-7'and cap['source_return_analysis']['induction']['summary_requires_all_obligations']and cap['assembly_state_analysis']['byte_memory']and cap['state_model_verification']['closure_requires_all_obligations_and_no_gaps']and cap['property_analysis']['solver']=='z3_shared_library',cap)
(OUT/'public-capabilities.json').write_text(json.dumps(cap,indent=2))
prior=ROOT.parent/'contract-deepening-20261015/acceptance/contract-analysis-deepening/release'
oldid=(prior/'production-analysis-id.txt').read_text().strip();old=client.must('/api/v1/analysis/contracts/get_contract_analysis',{'analysis_id':oldid})
check('previous-pinned-job-is-still-readable',old['status']=='completed'and old['metadata']['metrics']['engine_digest']=='sha256:b1b1ac0f9509f75d16cc58630b51a4f7ef3f66921bbb0d953ec1432f9af95025',{'analysis_id':oldid,'image':old['metadata']['metrics']['engine_digest']})
submitted=client.must('/api/v1/analysis/contracts/analyze_contract',{'chain':'ethereum','address':'0x6982508145454Ce325dDbE47a25d4ec3d2311933','block':19000000,'idempotency_key':'verification-production-proof-'+image[7:19],'budget':{'max_contracts':1,'max_state_reads':24,'max_rpc_requests':100}})
aid=submitted['analysis_id'];deadline=time.monotonic()+300
while time.monotonic()<deadline:
 status=client.must('/api/v1/analysis/contracts/get_contract_analysis',{'analysis_id':aid})
 if status['status']in ['completed','failed','cancelled']:break
 time.sleep(2)
check('public-source-proof-job-completes',status['status']=='completed'and status['metadata']['metrics']['engine_digest']==image,{'analysis_id':aid,'status':status['status'],'error':status.get('error_code')})
def section(name):
 items=[];cursor=None
 while True:
  q={'analysis_id':aid,'section':name,'page_size':7}
  if cursor:q['cursor']=cursor
  page=client.must('/api/v1/analysis/contracts/get_contract_analysis',q);items+=page['items']
  if not page['has_more']:return items
  cursor=page['next_cursor']
items=section('property_obligations')
proved=[p for p in items if p['status']=='proved_under_declared_model']
check('public-source-has-evidence-bound-proofs',len(proved)>0 and all(p['proof_artifact_id']and p['code_hash']and p['proof_scope']=='represented_source_model'and p['all_paths_proved']is False for p in proved),{'analysis_id':aid,'properties':len(items),'proved':len(proved)})
(OUT/'production-property-analysis.json').write_text(json.dumps({'analysis_id':aid,'status':status,'properties':items},indent=2))
models={name:section(name)for name in ['state_transitions','authorization_models','verification_obligations','verification_closure','artifacts']}
new_proved=[p for p in models['verification_obligations']if p['status']=='proved_under_declared_model']
check('public-state-models-and-proofs-published',bool(models['state_transitions'])and bool(models['authorization_models'])and bool(new_proved),{'analysis_id':aid,'counts':{s:len(v)for s,v in models.items()},'proved':len(new_proved)})
check('public-model-closure-keeps-remaining-requirements',bool(models['verification_closure'])and all(x['whole_authorization_proved']is False and x['whole_protocol_economics_proved']is False and x['remaining_requirements']for x in models['verification_closure']))
artifacts={a['artifact_id']:a for a in models['artifacts']}
for proof in new_proved:
 artifact=artifacts[proof['proof_artifact_id']];data=bytearray();offset=0
 while True:
  chunk=client.must('/api/v1/export/contracts/get_contract_analysis_artifact',{'analysis_id':aid,'artifact_id':artifact['artifact_id'],'offset':offset,'chunk_size':65536})
  raw=base64.b64decode(chunk['content_base64']);assert hashlib.sha256(raw).hexdigest()==chunk['chunk_sha256'];data.extend(raw);offset+=len(raw)
  if chunk['next_offset']is None:break
  assert chunk['next_offset']==offset
 saved=json.loads(data)
 check('public-state-proof-artifact-'+proof['property_id'],len(data)==artifact['byte_size']and hashlib.sha256(data).hexdigest()==artifact['sha256']and saved['property']==proof and saved['query']['property_id']==proof['property_id']and proof['all_paths_proved']is False)
(OUT/'production-state-models.json').write_text(json.dumps({'analysis_id':aid,'sections':models},indent=2))
import verification_release
uuid.UUID(aid)
durable=json.loads(verification_release.sql("SELECT json_object_agg(section,n) FROM(SELECT section,count(*) n FROM investigation_analysis_rows WHERE analysis_id='"+aid+"' GROUP BY section)x"))
check('public-state-models-match-durable-rows',all(durable.get(s,0)==len(v)for s,v in models.items()),durable)
usage=json.loads(verification_release.sql("SELECT json_build_object('http_requests',coalesce(sum(http_requests),0),'credits',coalesce(sum(credits) FILTER(WHERE NOT cache_hit AND NOT singleflight_hit),0),'estimated_usd',coalesce(sum(estimated_cost_usd) FILTER(WHERE NOT cache_hit AND NOT singleflight_hit),0)::numeric) FROM provider_usage WHERE job_id='"+aid+"' AND usage_type='contract_analysis'"))
metrics=status['metadata']['metrics']
check('public-state-proof-costs-match-provider-usage',usage['http_requests']==metrics['rpc_http_requests']and usage['credits']==metrics['credits']and abs(usage['estimated_usd']-metrics['estimated_usd'])<1e-12,usage)
audit=json.loads(verification_release.sql("SELECT json_build_object('n',count(*),'first',min(seq),'last',max(seq),'failures',count(*) FILTER(WHERE result!='success')) FROM audit_events_v2 WHERE action LIKE 'contract_analysis.%' AND resource_id='"+aid+"'"))
check('public-state-proof-reads-and-downloads-audited',audit['n']>=8 and audit['failures']==0,audit)
verified=subprocess.run([str(ROOT/'backend/bin/contract-auditverify'),'-chain','audit','-from',str(audit['first']),'-to',str(audit['last']),'-timeout','30s'],env={**env,**values},capture_output=True,text=True)
check('public-state-proof-audit-hash-chain-valid',verified.returncode==0,verified.stdout[:1400]if verified.returncode==0 else'verifier failed')
(OUT/'production-state-audit-chain.json').write_text(verified.stdout)
manifest=verification_release.verify();check('release-manifest-and-original-services-still-match',manifest['image']==image)
print('VERIFICATION_PRODUCTION_PASS',len(rows),flush=True)
