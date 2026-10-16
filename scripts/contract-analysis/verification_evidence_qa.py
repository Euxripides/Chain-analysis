"""Check new state/PC/proof records through scoped APIs, DB and every file."""
import hashlib,io,json,os,subprocess,urllib.parse,uuid,zipfile
from qa_support import ROOT,fixture_clients,dotenv
from live_api_qa import all_pages,artifact_bytes,machine_section,await_job
OUT=ROOT/os.environ.get('CONTRACT_QA_OUT','acceptance/contract-analysis-verification');checks=[]
def check(name,ok,evidence=None):
 checks.append({'scenario':name,'status':'PASS'if ok else'FAIL','evidence':evidence});(OUT/'verification-evidence-qa.json').write_text(json.dumps(checks,indent=2));print(name,checks[-1]['status'],flush=True)
 if not ok:raise AssertionError(name)
clients=fixture_clients();c=clients['investigator'];image=(OUT/'engine-image.txt').read_text().strip();cap=c.must('/api/v2/contract-analyses/capabilities')
expected_rule=os.environ.get('CONTRACT_EXPECTED_RULE_VERSION','funds-semantics-7')
check('observed-runtime-reports-state-verification',cap['engine_digest']==image and cap['semantic_rules_version']==expected_rule and cap['source_return_analysis']['induction']['summary_requires_all_obligations']and cap['assembly_state_analysis']['byte_memory']and cap['state_model_verification']['closure_requires_all_obligations_and_no_gaps'],cap)
values=dotenv(os.environ['CONTRACT_QA_ENV']);dsn=urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']);assert dsn.path=='/contract_analysis_qa_20261014'
env=os.environ.copy();env['PGPASSWORD']=urllib.parse.unquote(dsn.password)
def sql(query):return subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',dsn.username,'-d',dsn.path[1:]],input=query.encode(),env=env).decode().strip()
stamp=(OUT/'live-usage-start.txt').read_text().strip()
def usage():return json.loads(sql("SELECT json_build_object('http_requests',coalesce(sum(http_requests),0),'credits',coalesce(sum(credits) FILTER(WHERE NOT cache_hit AND NOT singleflight_hit),0),'estimated_usd',coalesce(sum(estimated_cost_usd) FILTER(WHERE NOT cache_hit AND NOT singleflight_hit),0)) FROM provider_usage WHERE usage_type='contract_analysis' AND request_time>='"+stamp+"'"))
reserved_http=int(os.environ.get('CONTRACT_QA_RESERVED_HTTP','0'));reserved_credits=int(os.environ.get('CONTRACT_QA_RESERVED_CREDITS','0'))
before=usage();check('new-model-fixture-fits-cumulative-budget',before['http_requests']+100+reserved_http<=2000 and before['credits']+100000+reserved_credits<=1000000 and before['estimated_usd']<10,before)
query={'chain':'ethereum','address':'0x6982508145454Ce325dDbE47a25d4ec3d2311933','block':19000000,'idempotency_key':'verification-state-model-export-arrays-'+image[7:19],'budget':{'max_contracts':1,'max_rpc_requests':100,'max_state_reads':24}}
aid=c.must('/api/v2/contract-analyses',query)['analysis_id'];uuid.UUID(aid);(OUT/'verification-analysis-id.txt').write_text(aid)
job=await_job(c,aid,300);check('pinned-state-model-fixture-completes',job['status']=='completed',{'analysis_id':aid,'address':query['address'],'block':query['block']})
sections={s:all_pages(c,aid,s,7)for s in ['state_transitions','authorization_models','verification_obligations','verification_closure','source_pc_maps','artifacts']}
check('new-models-published-by-actual-worker',job['metadata']['metrics']['engine_digest']==image and bool(sections['state_transitions'])and bool(sections['authorization_models'])and bool(sections['verification_closure']),{'analysis_id':aid,'counts':{s:len(v)for s,v in sections.items()}})
check('whole-model-boundaries-remain-explicit',all(x['whole_authorization_proved']is False and x['whole_protocol_economics_proved']is False and x['remaining_requirements']for x in sections['verification_closure']))
proofs=sections['verification_obligations'];check('new-state-obligations-are-nonempty',bool(proofs),{'properties':len(proofs)})
byid={a['artifact_id']:a for a in sections['artifacts']};proved=[p for p in proofs if p['status']=='proved_under_declared_model']
check('actual-source-has-new-discharged-obligations',bool(proved),{'properties':len(proofs),'proved':len(proved)})
for p in proofs:
 saved=json.loads(artifact_bytes(c,aid,byid[p['proof_artifact_id']],65536))
 check('new-proof-identities-'+p['property_id'],saved['property']==p and saved['query']['property_id']==p['property_id']and p['all_paths_proved']is False)
for a in sections['artifacts']:
 if a['kind']!='source_pc_map':continue
 saved=json.loads(artifact_bytes(c,aid,a,65536));expected=[p for p in sections['source_pc_maps']if p['artifact_id']==a['artifact_id']]
 check('source-map-file-matches-scoped-api-'+a['artifact_id'],saved['call_instructions']==expected and all(p['artifact_id']==a['artifact_id']for p in saved['call_instructions']))
machine=json.loads(artifact_bytes(c,aid,next(a for a in byid.values()if a['kind']=='machine_result'),1<<20))
for section in ['state_transitions','authorization_models','verification_obligations','verification_closure','source_pc_maps']:
 check('machine-equals-paged-'+section,machine_section(c,aid,sections['artifacts'],machine,section)==sections[section])
 check('other-workspace-denied-'+section,clients['other'].call('/api/v2/contract-analyses/'+aid+'?section='+section)[0]==404)
report=artifact_bytes(c,aid,next(a for a in byid.values()if a['kind']=='analysis_report'),1<<20).decode()
check('report-includes-models-obligations-and-assumptions','权限模型证明'in report and'模型覆盖'in report and all(p['proof_artifact_id']in report for p in proved))
bundle=artifact_bytes(c,aid,next(a for a in byid.values()if a['kind']=='evidence_bundle'),1<<20)
with zipfile.ZipFile(io.BytesIO(bundle))as z:
 manifest=json.loads(z.read('manifest.json'));assert z.testzip()is None and set(z.namelist())=={'manifest.json'}|{e['path']for e in manifest['entries']}
 for entry in manifest['entries']:
  blob=z.read(entry['path']);assert len(blob)==entry['byte_size']and hashlib.sha256(blob).hexdigest()==entry['sha256']
 check('new-evidence-zip-every-file-hash',True,{'files':len(manifest['entries']),'sha256':hashlib.sha256(bundle).hexdigest()})
txaid=(OUT/'execution-analysis-id.txt').read_text().strip();uuid.UUID(txaid)
runtime={s:all_pages(c,txaid,s,7)for s in ['replays','opcode_witnesses','execution_callsite_witnesses','execution_memory_effects','execution_storage_effects','external_return_observations','artifacts']}
check('actual-historical-replay-has-opcode-evidence',bool(runtime['opcode_witnesses'])and any(w['calls_complete']for w in runtime['opcode_witnesses']),{'analysis_id':txaid,'witnesses':runtime['opcode_witnesses']})
bound=[o for o in runtime['external_return_observations']if o['source_binding_status']=='exact_program_counter_and_execution_context']
check('historical-return-has-exact-source-callsite',bool(bound),{'bound':len(bound),'observed':len(runtime['external_return_observations'])})
for o in bound:
 matches=[w for w in runtime['execution_callsite_witnesses']if w['opcode_call_id']==o['opcode_call_id']]
 check('historical-callsite-identity-'+o['observation_id'],len(matches)==1 and matches[0]['input']==o['input']and matches[0]['return_data']==o['return_data']and matches[0]['settled']==o['settled']and matches[0]['artifact_id']==o['opcode_artifact_id'])
txarts={a['artifact_id']:a for a in runtime['artifacts']}
for w in runtime['opcode_witnesses']:
 saved=json.loads(artifact_bytes(c,txaid,txarts[w['artifact_id']],1<<20))
 check('opcode-file-matches-raw-trace-'+w['witness_id'],saved['complete_replay']and saved['original_call_trace_matches']and saved['normalized']['raw_trace_sha256']==w['raw_trace_sha256']and saved['opcode_trace']['structLogs'])
query="SELECT json_object_agg(section,n) FROM(SELECT section,count(*) n FROM investigation_analysis_rows WHERE analysis_id='"+aid+"' GROUP BY section)x"
db=json.loads(sql(query))
check('new-models-counts-match-database',all(db.get(s,0)==len(v)for s,v in sections.items()),db)
local=json.loads((OUT/'verification-runtime/report.json').read_text());check('frozen-candidate-runtime-checks-passed',local['complete']and local['image']==image and all(x['status']=='PASS'for x in local['checks']),{'checks':len(local['checks'])})
gold=json.loads((OUT/'gold/report.json').read_text());vault=next(r for r in gold['runs']if r['sample']=='openzeppelin-ShareVault')
check('erc4626-runtime-stays-within-existing-budget',vault['wall_ms']<120000,{'wall_ms':vault['wall_ms'],'budget_ms':120000})
after=usage();check('new-model-fixture-keeps-cumulative-budget',after['http_requests']+reserved_http<=2000 and after['credits']+reserved_credits<=1000000 and after['estimated_usd']<=10,after)
cost=json.loads((OUT/'live-usage.json').read_text());cost['actual']=after;(OUT/'live-usage.json').write_text(json.dumps(cost,indent=2));(OUT/'verification-model-usage.json').write_text(json.dumps({'before':before,'after':after},indent=2))
print('VERIFICATION_EVIDENCE_PASS',len(checks),flush=True)
