"""A pinned public contract checks observed address conditions through real APIs."""
import json,os,pathlib,subprocess,urllib.parse
from qa_support import ROOT,fixture_clients,dotenv
from live_api_qa import await_job,all_pages,artifact_bytes
OUT=ROOT/os.environ.get('CONTRACT_QA_OUT','acceptance/contract-analysis-semantics');rows=[]
def check(name,ok,evidence=None):
 rows.append({'scenario':name,'status':'PASS' if ok else 'FAIL','evidence':evidence});(OUT/'authority-live-qa.json').write_text(json.dumps(rows,indent=2));print(name,rows[-1]['status'],flush=True)
 if not ok:raise AssertionError(name)
c=fixture_clients()['investigator'];image=(OUT/'engine-image.txt').read_text().strip()
q={'chain':'ethereum','address':'0xdAC17F958D2ee523a2206206994597C13D831ec7','block':19000000,'from_block':19000000,'to_block':19000000,
 'idempotency_key':'ca-authority-'+image[7:19],'budget':{'max_contracts':1,'max_rpc_requests':100,'max_state_reads':24}}
aid=c.must('/api/v2/contract-analyses',q)['analysis_id'];job=await_job(c,aid);(OUT/'authority-analysis-id.txt').write_text(aid)
check('pinned-owner-contract-completes',job['status']=='completed',{'analysis_id':aid,'phase':job['phase'],'error':job.get('error_code')})
capabilities=c.must('/api/v2/contract-analyses/capabilities')
check('saved-new-rule-version',job['metadata']['rule_version']==capabilities['semantic_rules_version'])
permissions=all_pages(c,aid,'permissions',100);constraints=[x for g in permissions for x in g.get('caller_constraints',[])];observed=[x for x in constraints if x.get('snapshot_resolution_status')=='observed_direct_state_address']
check('observed-source-owner-address',bool(observed),{'total_conditions':len(constraints),'observed':len(observed)})
check('every-owner-observation-is-pinned',all(x['snapshot_observations'] and all(p['block_number']==19000000 and p['block_hash']==job['metadata']['snapshot']['block_hash'] and p['storage_address'].lower()==q['address'].lower() for p in x['snapshot_observations']) for x in observed))
effects=all_pages(c,aid,'control_effects',100)
check('real-owner-linked-to-funds-controls',any(e.get('current_controller_status')=='observed_address_conditions' and e.get('affected_method_ids') and e.get('impact_types') for e in effects))
check('address-conditions-retain-authorization-scope',all(x['authorization_sufficiency_verified'] is False for x in constraints))
arts=all_pages(c,aid,'artifacts',100);report=artifact_bytes(c,aid,next(a for a in arts if a['kind']=='analysis_report'),1<<20).decode()
check('report-preserves-full-observed-addresses',all(x['snapshot_target_address'] in report for x in observed))
check('report-shows-control-impact', '配置与资金关联' in report)
machine=json.loads(artifact_bytes(c,aid,next(a for a in arts if a['kind']=='machine_result'),1<<20))
check('exported-permissions-equal-paged-result',machine['sections']['permissions']==permissions)
values=dotenv(os.environ['CONTRACT_QA_ENV']);dsn=urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']);assert dsn.path=='/contract_analysis_qa_20261014'
env=os.environ.copy();env['PGPASSWORD']=urllib.parse.unquote(dsn.password)
query="SELECT json_build_object('permissions',count(*) FILTER(WHERE section='permissions'),'effects',count(*) FILTER(WHERE section='control_effects'),'observed',count(*) FILTER(WHERE section='permissions' AND data::text LIKE '%observed_direct_state_address%')) FROM investigation_analysis_rows WHERE analysis_id='"+aid+"'"
db=json.loads(subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',dsn.username,'-d',dsn.path[1:]],input=query.encode(),env=env).decode())
check('database-has-the-same-observed-fields',db['permissions']==len(permissions) and db['effects']==len(effects) and db['observed']>0,db)
print('AUTHORITY_LIVE_PASS',len(rows),flush=True)
