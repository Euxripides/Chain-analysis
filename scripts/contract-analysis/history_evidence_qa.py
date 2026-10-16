"""Read the new transaction-diff artifact through actual scoped services and storage."""
import hashlib,io,json,os,subprocess,urllib.parse,uuid,zipfile
from qa_support import ROOT,fixture_clients,dotenv
from live_api_qa import all_pages,artifact_bytes,machine_section
OUT=ROOT/os.environ['CONTRACT_QA_OUT'];checks=[]
def check(name,ok,evidence=None):
 checks.append({'scenario':name,'status':'PASS'if ok else'FAIL','evidence':evidence});(OUT/'history-evidence-qa.json').write_text(json.dumps(checks,indent=2));print(name,checks[-1]['status'],flush=True)
 if not ok:raise AssertionError(name)
clients=fixture_clients();c=clients['investigator'];aid=(OUT/'execution-analysis-id.txt').read_text().strip();uuid.UUID(aid)
values=dotenv(os.environ['CONTRACT_QA_ENV']);dsn=urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']);assert dsn.path=='/contract_analysis_qa_20261014'
env={**os.environ,'PGPASSWORD':urllib.parse.unquote(dsn.password)}
def sql(query):return json.loads(subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',dsn.username,'-d',dsn.path[1:]],input=query.encode(),env=env))
job=c.must('/api/v2/contract-analyses/'+aid);check('new-state-diff-produced-by-exact-worker',job['status']=='completed'and job['metadata']['metrics']['engine_digest']==(OUT/'engine-image.txt').read_text().strip(),{'analysis_id':aid})
sections={s:all_pages(c,aid,s,7)for s in ['artifacts','replays']};arts=sections['artifacts'];rows=sections['replays']
check('live-replay-compares-complete-transaction-diff',bool(rows)and all(r['complete']and r['state_diff_complete']and r['state_diff_scope']=='complete_transaction_changes_including_additional_replay_effects'and r['differences']==[]for r in rows))
diffs=[a for a in arts if a['kind']=='transaction_state_diff'];check('one-state-diff-file-per-replayed-transaction',len(diffs)==len(rows))
for item in diffs:
 artifact_id=item['artifact_id'];assert artifact_id.replace('-','').isalnum()
 raw=artifact_bytes(c,aid,item,65536);saved=json.loads(raw);comparison=saved['comparison']
 check('state-diff-file-full-hash-'+artifact_id,hashlib.sha256(raw).hexdigest()==item['sha256']and len(raw)==item['byte_size'])
 check('state-diff-file-includes-both-observed-traces-'+artifact_id,saved['schema_version']=='contract-transaction-state-diff-v1'and saved['original']['pre']and saved['replayed']['pre']and saved['replayed_prestate']and comparison['matched']and comparison['complete']and comparison['expected']['effects']==comparison['actual']['effects'])
 check('state-diff-other-workspace-denied-'+artifact_id,clients['other'].call('/api/v2/contract-analyses/'+aid+'/artifacts/'+artifact_id+'?chunk_size=65536')[0]==404)
 check('state-diff-not-exposed-as-source-preview-'+artifact_id,c.call('/api/v2/contract-analyses/'+aid+'/artifacts/'+artifact_id+'/preview?chunk_size=65536')[0]==403)
 db=sql("SELECT json_build_object('n',count(*),'sha256',min(sha256),'bytes',min(byte_size))FROM contract_analysis_artifacts WHERE analysis_id='"+aid+"'AND artifact_id='"+artifact_id+"'AND kind='transaction_state_diff'")
 check('state-diff-database-matches-file-'+artifact_id,db['n']==1 and db['sha256']==item['sha256']and db['bytes']==len(raw),db)
 audit=sql("SELECT json_build_object('n',count(*),'sensitive',bool_and(sensitive),'request_ids',bool_and(request_id!=''),'event_hashes',bool_and(event_hash!=''))FROM audit_events_v2 WHERE resource_id='"+aid+"'AND action='contract_analysis.get_contract_analysis_artifact'AND result='success'AND detail->>'artifact_id'='"+artifact_id+"'")
 check('state-diff-read-has-scoped-audit-'+artifact_id,audit['n']>0 and audit['sensitive']and audit['request_ids']and audit['event_hashes'],audit)
machine=json.loads(artifact_bytes(c,aid,next(a for a in arts if a['kind']=='machine_result'),1<<20))
check('state-diff-replay-fields-survive-machine-export',machine_section(c,aid,arts,machine,'replays')==rows)
bundle=artifact_bytes(c,aid,next(a for a in arts if a['kind']=='evidence_bundle'),1<<20)
with zipfile.ZipFile(io.BytesIO(bundle))as z:
 manifest=json.loads(z.read('manifest.json'));assert z.testzip()is None
 for item in diffs:
  matches=[e for e in manifest['entries']if e['sha256']==item['sha256']and e['byte_size']==item['byte_size']]
  check('state-diff-in-full-evidence-bundle-'+item['artifact_id'],len(matches)==1 and hashlib.sha256(z.read(matches[0]['path'])).hexdigest()==item['sha256'])
print('HISTORY_EVIDENCE_PASS',len(checks),flush=True)
