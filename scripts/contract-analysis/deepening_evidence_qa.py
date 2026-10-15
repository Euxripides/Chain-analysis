"""Check new proof and return records through the real scoped API and files."""
import hashlib,io,json,os,pathlib,subprocess,urllib.parse,zipfile
from qa_support import ROOT,fixture_clients,dotenv
from live_api_qa import all_pages,artifact_bytes,machine_section
OUT=ROOT/os.environ.get('CONTRACT_QA_OUT','acceptance/contract-analysis-deepening');checks=[]
def check(name,ok,evidence=None):
 checks.append({'scenario':name,'status':'PASS'if ok else'FAIL','evidence':evidence});(OUT/'deepening-evidence-qa.json').write_text(json.dumps(checks,indent=2));print(name,checks[-1]['status'],flush=True)
 if not ok:raise AssertionError(name)
c=fixture_clients()['investigator'];cap=c.must('/api/v2/contract-analyses/capabilities')
check('runtime-capabilities-report-new-support',cap['semantic_rules_version']=='funds-semantics-6'and cap['source_return_analysis']['symbolic_loop_exit_paths']and cap['source_return_analysis']['structured_yul']['local_functions']and cap['property_analysis']['solver']=='z3_shared_library',cap)
aid=(OUT/'authority-analysis-id.txt').read_text().strip();proofs=all_pages(c,aid,'property_obligations',7);proved=[p for p in proofs if p['status']=='proved_under_declared_model']
check('real-public-source-has-permission-proofs',bool(proved),{'analysis_id':aid,'properties':len(proofs),'proved':len(proved)})
arts=all_pages(c,aid,'artifacts',31);by_id={a['artifact_id']:a for a in arts}
for p in proved:
 check('proof-artifact-'+p['property_id'],p.get('all_paths_proved')is False and p.get('proof_scope')=='represented_source_model'and p['proof_artifact_id']in by_id)
 proof=json.loads(artifact_bytes(c,aid,by_id[p['proof_artifact_id']],65536))
 check('saved-smt-matches-'+p['property_id'],proof['property']==p and proof['query']['property_id']==p['property_id']and '(set-option :timeout 300)'in proof['query']['declarations'])
machine=json.loads(artifact_bytes(c,aid,next(a for a in arts if a['kind']=='machine_result'),1<<20))
check('exported-proof-records-match-api',machine_section(c,aid,arts,machine,'property_obligations')==proofs)
report=artifact_bytes(c,aid,next(a for a in arts if a['kind']=='analysis_report'),1<<20).decode()
check('report-includes-proof-state-and-assumptions','性质验证'in report and all(p['proof_artifact_id']in report for p in proved))
zip_art=next(a for a in arts if a['kind']=='evidence_bundle');blob=artifact_bytes(c,aid,zip_art,1<<20)
with zipfile.ZipFile(io.BytesIO(blob))as z:
 check('zip-crc-and-proof-files',z.testzip()is None and all(any(p['property_id']in n for n in z.namelist())for p in proved),{'files':len(z.namelist()),'sha256':hashlib.sha256(blob).hexdigest()})
txaid=(OUT/'execution-analysis-id.txt').read_text().strip();observed=all_pages(c,txaid,'external_return_observations',3);frames=all_pages(c,txaid,'execution_calls',3)
check('real-trace-returns-persist-by-frame',len(observed)>0 and all(any(o['execution_call_id']==f['call_id']and o['return_data']==f['output']and o['tx_hash']==f['tx_hash']and o['storage_address']==f['storage_address']for f in frames)for o in observed),{'analysis_id':txaid,'observations':len(observed)})
check('observed-returns-keep-source-binding-boundary',all(o['scope']=='execution_frame_only'and o['source_binding_status']=='requires_program_counter_witness'for o in observed))
txarts=all_pages(c,txaid,'artifacts',100);txmachine=json.loads(artifact_bytes(c,txaid,next(a for a in txarts if a['kind']=='machine_result'),1<<20))
check('exported-return-observations-match-api',machine_section(c,txaid,txarts,txmachine,'external_return_observations')==observed)
values=dotenv(os.environ['CONTRACT_QA_ENV']);dsn=urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']);assert dsn.path=='/contract_analysis_qa_20261014'
env=os.environ.copy();env['PGPASSWORD']=urllib.parse.unquote(dsn.password)
query="SELECT json_build_object('properties',count(*) FILTER(WHERE section='property_obligations'),'returns',count(*) FILTER(WHERE section='external_return_observations')) FROM investigation_analysis_rows WHERE analysis_id IN ('"+aid+"','"+txaid+"')"
db=json.loads(subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',dsn.username,'-d',dsn.path[1:]],input=query.encode(),env=env).decode())
check('new-rows-are-durable-in-database',db['properties']>=len(proofs)and db['returns']==len(observed),db)
runtime=json.loads((OUT/'deepening-runtime/report.json').read_text());check('local-execution-and-counterexamples-passed',runtime['complete']and all(r['status']=='PASS'for r in runtime['checks']),{'checks':len(runtime['checks'])})
gold=json.loads((OUT/'gold/report.json').read_text());vault=next(r for r in gold['runs']if r['sample']=='openzeppelin-ShareVault')
check('erc4626-regression-stays-within-wall-budget',vault['wall_ms']<120000,{'sample':vault['sample'],'wall_ms':vault['wall_ms'],'baseline_wall_ms':31134,'regression_wall_ms':294632})
print('DEEPENING_EVIDENCE_PASS',len(checks),flush=True)
