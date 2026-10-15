"""Verify the released service, old tasks, new proofs and public assets."""
import json,os,pathlib,subprocess,time
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-deepening-20261015'
OUT=ROOT/'acceptance/contract-analysis-deepening/release';OUT.mkdir(parents=True,exist_ok=True)
env=os.environ.copy();env.update({'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+env['PATH'],'GOMAXPROCS':'2','CONTRACT_QA_OUT':'acceptance/contract-analysis-deepening/release','CONTRACT_QA_API':'https://aurore.online','E2E_BROWSER_CHANNEL':'chromium'})
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
 rows.append({'scenario':name,'status':'PASS'if ok else'FAIL','evidence':evidence});(OUT/'production-deepening-qa.json').write_text(json.dumps(rows,indent=2));print(name,rows[-1]['status'],flush=True)
 if not ok:raise AssertionError(name)
image=(OUT.parent/'engine-image.txt').read_text().strip()
cap=client.must('/api/v1/analysis/contracts/get_contract_analysis_capabilities',{})
check('public-capabilities-are-current',cap['engine_digest']==image and cap['semantic_rules_version']=='funds-semantics-6'and cap['source_return_analysis']['symbolic_loop_exit_paths']and cap['property_analysis']['solver']=='z3_shared_library',cap)
(OUT/'public-capabilities.json').write_text(json.dumps(cap,indent=2))
prior=ROOT.parent/'contract-expressions-20261015/acceptance/contract-analysis-expressions/release'
oldid=(prior/'production-analysis-id.txt').read_text().strip();old=client.must('/api/v1/analysis/contracts/get_contract_analysis',{'analysis_id':oldid})
check('previous-pinned-job-is-still-readable',old['status']=='completed'and old['metadata']['metrics']['engine_digest']=='sha256:8d226da81212053a90c0d589d4cf845f31e0619605053f0ed9edee434a373d7f',{'analysis_id':oldid,'image':old['metadata']['metrics']['engine_digest']})
submitted=client.must('/api/v1/analysis/contracts/analyze_contract',{'chain':'ethereum','address':'0xdAC17F958D2ee523a2206206994597C13D831ec7','block':19000000,'idempotency_key':'deepening-production-proof-'+image[7:19],'budget':{'max_contracts':1,'max_state_reads':24,'max_rpc_requests':100}})
aid=submitted['analysis_id'];deadline=time.monotonic()+300
while time.monotonic()<deadline:
 status=client.must('/api/v1/analysis/contracts/get_contract_analysis',{'analysis_id':aid})
 if status['status']in ['completed','failed','cancelled']:break
 time.sleep(2)
check('public-source-proof-job-completes',status['status']=='completed'and status['metadata']['metrics']['engine_digest']==image,{'analysis_id':aid,'status':status['status'],'error':status.get('error_code')})
items=[];cursor=None
while True:
 q={'analysis_id':aid,'section':'property_obligations','page_size':7}
 if cursor:q['cursor']=cursor
 page=client.must('/api/v1/analysis/contracts/get_contract_analysis',q);items+=page['items']
 if not page['has_more']:break
 cursor=page['next_cursor']
proved=[p for p in items if p['status']=='proved_under_declared_model']
check('public-source-has-evidence-bound-proofs',len(proved)>0 and all(p['proof_artifact_id']and p['code_hash']and p['proof_scope']=='represented_source_model'and p['all_paths_proved']is False for p in proved),{'analysis_id':aid,'properties':len(items),'proved':len(proved)})
(OUT/'production-property-analysis.json').write_text(json.dumps({'analysis_id':aid,'status':status,'properties':items},indent=2))
import deepening_release
manifest=deepening_release.verify();check('release-manifest-and-original-services-still-match',manifest['image']==image)
print('DEEPENING_PRODUCTION_PASS',len(rows),flush=True)
