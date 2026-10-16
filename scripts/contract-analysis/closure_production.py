"""Verify the deployed candidate, public proofs, old tasks and aggregate QA cost."""
import base64,hashlib,json,os,pathlib,subprocess,sys,time,uuid
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-model-closure-20261016'
os.environ['CONTRACT_QA_API']='https://aurore.online'
sys.path.insert(0,str(ROOT/'scripts/contract-analysis'));import closure_release
from qa_support import Client,dotenv
import qa_support
assert qa_support.BASE=='https://aurore.online','production client must never inherit an isolated API target'
if sys.argv[1:]==['--preflight']:
 values=dotenv('/home/euripides/projects/blockchain-platform/deploy/.env');cap=Client(values['GPT_ACTIONS_API_KEY']).must('/api/v1/analysis/contracts/get_contract_analysis_capabilities',{})
 print('PUBLIC_CLIENT_PREFLIGHT_PASS',qa_support.BASE,cap['semantic_rules_version'],cap['engine_digest'],flush=True);raise SystemExit(0)
BASE=ROOT/'acceptance/contract-analysis-model-closure';OUT=BASE/'release';OUT.mkdir(parents=True,exist_ok=True)
manifest=closure_release.verify();assert manifest['status']=='deployed';image=manifest['image']
env={**os.environ,'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+os.environ['PATH'],'GOMAXPROCS':'2','CONTRACT_QA_API':'https://aurore.online','CONTRACT_QA_OUT':str(OUT.relative_to(ROOT)),'CONTRACT_PRODUCTION_MAX_RPC':'80','E2E_BROWSER_CHANNEL':'chromium'}
private=json.loads((BASE/'symbol-service-validation/live-usage.json').read_text())['actual'];previous=json.loads((BASE/'symbol-service-validation/production-usage-baseline.json').read_text())
assert private['http_requests']+previous['actual']['http_requests']+160<=2000 and private['credits']+previous['actual']['credits']+100000<=1000000
checks=[];production_ids=set()
def check(name,ok,evidence=None):
 checks.append({'scenario':name,'status':'PASS'if ok else'FAIL','evidence':evidence});(OUT/'production-model-qa.json').write_text(json.dumps(checks,indent=2));print(name,checks[-1]['status'],flush=True)
 if not ok:raise AssertionError(name)
def run(name,args):
 with(OUT/(name+'.log')).open('w')as log:result=subprocess.run(args,cwd=ROOT,env=env,stdout=log,stderr=subprocess.STDOUT)
 if result.returncode:print((OUT/(name+'.log')).read_text()[-5500:],flush=True)
 check(name,result.returncode==0,{'exit_code':result.returncode})
def section(client,aid,name):
 rows=[];cursor=None;seen=set();count=None
 while True:
  q={'analysis_id':aid,'section':name,'page_size':7}
  if cursor:q['cursor']=cursor
  page=client.must('/api/v1/analysis/contracts/get_contract_analysis',q)
  if count is None:count=page['record_count']
  assert page['record_count']==count and page['returned_records']==len(page['items']);rows+=page['items']
  if not page['has_more']:assert len(rows)==count;return rows
  cursor=page['next_cursor'];assert cursor and cursor not in seen;seen.add(cursor)
def artifact(client,aid,item):
 data=bytearray();offset=0
 while True:
  part=client.must('/api/v1/export/contracts/get_contract_analysis_artifact',{'analysis_id':aid,'artifact_id':item['artifact_id'],'offset':offset,'chunk_size':65536});raw=base64.b64decode(part['content_base64'],validate=True)
  assert part['offset']==offset and len(raw)==part['byte_length']and hashlib.sha256(raw).hexdigest()==part['chunk_sha256'];data.extend(raw);offset+=len(raw)
  if part['next_offset']is None:break
  assert raw and part['next_offset']==offset
 assert len(data)==item['byte_size']and hashlib.sha256(data).hexdigest()==item['sha256'];return bytes(data)
try:
 run('public-mcp',['node','mcp/scripts/contract-production-qa.mjs'])
 weth_id=str(uuid.UUID((OUT/'production-analysis-id.txt').read_text().strip()));production_ids.add(weth_id)
 run('public-integrity',['python3','scripts/contract-analysis/production_integrity_qa.py'])
 run('public-browser',['node','frontend/scripts/contract-production-render-qa.mjs'])
 os.environ['CONTRACT_QA_API']='https://aurore.online';values=dotenv('/home/euripides/projects/blockchain-platform/deploy/.env');client=Client(values['GPT_ACTIONS_API_KEY'])
 cap=client.must('/api/v1/analysis/contracts/get_contract_analysis_capabilities',{})
 check('public-capabilities-bind-the-corrected-frozen-engine',cap['engine_digest']==image and cap['semantic_rules_version']=='funds-semantics-8'and cap['state_model_verification']['builtin_identity']=='compiler_AST_declaration_identity_with_shadowed_names_preserved'and cap['state_model_verification']['signature_state_analysis']['exact_concrete_ecdsa_recovery']and cap['source_return_analysis']['induction']['counter_polynomials_max_degree']==3,cap)
 (OUT/'public-capabilities.json').write_text(json.dumps(cap,indent=2))
 oldid=(ROOT.parent/'contract-verification-20261015/acceptance/contract-analysis-verification/release/production-analysis-id.txt').read_text().strip();old=client.must('/api/v1/analysis/contracts/get_contract_analysis',{'analysis_id':oldid})
 check('version-seven-task-and-evidence-still-readable',old['status']=='completed'and old['metadata']['metrics']['engine_digest']==manifest['previous_image'],{'analysis_id':oldid,'image':old['metadata']['metrics']['engine_digest']})
 submitted=client.must('/api/v1/analysis/contracts/analyze_contract',{'chain':'ethereum','address':'0x6982508145454Ce325dDbE47a25d4ec3d2311933','block':19000000,'idempotency_key':'model-closure-production-proof-'+image[7:19],'budget':{'max_contracts':1,'max_state_reads':24,'max_rpc_requests':80,'max_credits':50000}})
 aid=str(uuid.UUID(submitted['analysis_id']));production_ids.add(aid);(OUT/'production-model-analysis-id.txt').write_text(aid);deadline=time.monotonic()+300
 while True:
  job=client.must('/api/v1/analysis/contracts/get_contract_analysis',{'analysis_id':aid})
  if job['status']in['completed','failed','cancelled']:break
  if time.monotonic()>deadline:raise RuntimeError('production model job timeout')
  time.sleep(2)
 check('public-model-job-completes-on-the-new-image',job['status']=='completed'and job['metadata']['metrics']['engine_digest']==image,{'analysis_id':aid,'status':job['status'],'error':job.get('error_code')})
 sections={name:section(client,aid,name)for name in['initial_state_models','state_transitions','authorization_models','verification_obligations','verification_closure','artifacts']}
 check('public-model-sections-are-persisted',all(sections[name]for name in['initial_state_models','state_transitions','authorization_models','verification_obligations','verification_closure']),{name:len(rows)for name,rows in sections.items()})
 check('public-whole-model-requirements-remain-open',all(row['whole_authorization_proved']is False and row['whole_protocol_economics_proved']is False and row['remaining_requirements']for row in sections['verification_closure']))
 proved=[p for p in sections['verification_obligations']if p['status']=='proved_under_declared_model'];check('public-source-obligations-include-discharged-proofs',bool(proved),{'proved':len(proved)})
 byid={a['artifact_id']:a for a in sections['artifacts']}
 for proof in proved:
  raw=artifact(client,aid,byid[proof['proof_artifact_id']]);saved=json.loads(raw)
  check('public-proof-file-'+proof['property_id'],saved['property']==proof and saved['query']['property_id']==proof['property_id']and proof['all_paths_proved']is False)
 counts=json.loads(closure_release.sql("SELECT json_object_agg(section,n)FROM(SELECT section,count(*) n FROM investigation_analysis_rows WHERE analysis_id='"+aid+"'GROUP BY section)x"))
 check('public-model-pages-match-durable-database-counts',all(counts.get(name,0)==len(rows)for name,rows in sections.items()),counts)
 audit=json.loads(closure_release.sql("SELECT json_build_object('n',count(*),'first',min(seq),'last',max(seq),'failures',count(*)FILTER(WHERE result!='success'))FROM audit_events_v2 WHERE action LIKE'contract_analysis.%'AND resource_id='"+aid+"'"))
 check('public-model-and-file-accesses-are-audited',audit['n']>=8 and audit['failures']==0,audit)
 verifier=subprocess.run([str(ROOT/'backend/bin/contract-auditverify'),'-chain','audit','-from',str(audit['first']),'-to',str(audit['last']),'-timeout','30s'],env={**env,**values},capture_output=True,text=True)
 check('public-model-audit-hash-chain-verifies',verifier.returncode==0);(OUT/'production-model-audit-chain.json').write_text(verifier.stdout)
 query="SELECT json_build_object('http_requests',coalesce(sum(http_requests),0),'credits',coalesce(sum(credits)FILTER(WHERE NOT cache_hit AND NOT singleflight_hit),0),'estimated_usd',coalesce(sum(estimated_cost_usd)FILTER(WHERE NOT cache_hit AND NOT singleflight_hit),0))FROM provider_usage WHERE usage_type='contract_analysis'AND job_id IN("+','.join("'"+i+"'"for i in sorted(production_ids))+")"
 actual=json.loads(closure_release.sql(query));check('new-production-validation-respects-its-reserved-budget',actual['http_requests']<=160 and actual['credits']<=100000,actual)
 total={key:private[key]+previous['actual'][key]+actual[key]for key in['http_requests','credits','estimated_usd']};check('combined-private-and-production-acceptance-fits-cumulative-budget',total['http_requests']<=2000 and total['credits']<=1000000 and total['estimated_usd']<=10,total)
 (OUT/'production-models.json').write_text(json.dumps({'analysis_id':aid,'sections':sections},indent=2));(OUT/'campaign-usage.json').write_text(json.dumps({'isolated_qa':private,'previous_production':previous['actual'],'new_production':actual,'new_production_analysis_ids':sorted(production_ids),'actual':total,'limits':{'http_requests':2000,'credits':1000000,'estimated_usd':10}},indent=2))
 check('deployed-files-and-original-services-still-match',closure_release.verify()['image']==image)
 print('MODEL_PRODUCTION_ACCEPTANCE_PASS_GOAL_REMAINS_ACTIVE',len(checks),flush=True)
except Exception:
 subprocess.run(['/usr/bin/python3','scripts/contract-analysis/closure_release.py','rollback'],cwd=ROOT,check=True);raise
