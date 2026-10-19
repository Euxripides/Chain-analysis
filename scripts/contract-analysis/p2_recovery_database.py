"""Cross-check published recovery jobs against durable rows, cost and audit."""
import hashlib,json,os,pathlib,re,subprocess,urllib.parse
from qa_support import dotenv
ROOT=pathlib.Path(__file__).resolve().parents[2];OUT=ROOT/'acceptance/contract-analysis-partial-recovery'
image=(OUT/'engine-image.txt').read_text().strip();directory=OUT/('mcp-'+image[7:23]);jobs={}
for path in directory.glob('*-job.json'):
 job=json.loads(path.read_text());jobs[job['analysis_id']]=job
assert len(jobs)>=4,'Run core and lifecycle first'
race=json.loads((OUT/'publication-race-image.json').read_text())
assert race['base_image']==image
race_result=json.loads((pathlib.Path(race['output'])/'result.json').read_text());assert race_result['status']=='PASS'and race_result['production_pids_unchanged']
race_job=json.loads((directory/'race-job.json').read_text());assert race_job['analysis_id']in jobs
for aid,job in jobs.items():
 assert job['metadata']['metrics']['engine_digest']==(race['image']if aid==race_job['analysis_id']else image)
assert all(re.fullmatch(r'[0-9a-f-]{36}',i)for i in jobs)
values=dotenv('/home/euripides/.config/blockchain-platform/contract-partial-recovery-qa-20261017.env')
db=urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']);assert db.path=='/contract_analysis_qa_20261014'
def sql(query):
 raw=subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',db.username,'-d',db.path[1:]],input=query.encode(),env={**os.environ,'PGPASSWORD':urllib.parse.unquote(db.password)})
 return json.loads(raw)
joined=','.join("'"+i+"'::uuid"for i in jobs);texts=','.join("'"+i+"'"for i in jobs)
rows=sql("SELECT json_agg(t)FROM(SELECT j.id,j.status,j.fence,j.attempts,j.result_meta->>'rule_version' rule_version,j.result_meta->'snapshot'->>'identity_kind' identity_kind,j.boundary->>'input_sha256' input_sha256,j.result_meta->'snapshot'->>'input_sha256' output_input_sha256,(SELECT count(*)FROM provider_usage u WHERE u.job_id=j.id)usage_rows,(SELECT count(*)FROM contract_analysis_snapshots s WHERE s.analysis_id=j.id)deployment_snapshots,b.reserved_requests,b.reserved_credits,b.state_reads,b.processing_ms,(SELECT json_object_agg(section,n)FROM(SELECT section,count(*)n FROM investigation_analysis_rows r WHERE r.analysis_id=j.id GROUP BY section)q)section_counts FROM investigation_analysis_jobs j JOIN contract_analysis_budgets b ON b.analysis_id=j.id WHERE j.id IN("+joined+"))t;")
assert len(rows)==len(jobs)
for row in rows:
 assert row['status']=='completed'and row['rule_version']=='funds-semantics-15'and row['identity_kind']=='code_only'
 assert row['input_sha256']==row['output_input_sha256']
 row['engine_digest']=jobs[row['id']]['metadata']['metrics']['engine_digest']
 assert row['usage_rows']==row['deployment_snapshots']==row['reserved_requests']==row['state_reads']==0 and int(row['reserved_credits'])==0
 assert row['section_counts']=={k:v for k,v in jobs[row['id']]['metadata']['sections'].items()if v},row['id']
audit=sql("SELECT json_agg(t)FROM(SELECT action,count(*)n,bool_and(request_id!='')request_ids,bool_and(event_hash!='')event_hashes,bool_and(sensitive)sensitive FROM audit_events_v2 WHERE resource_id IN("+texts+")AND result='success'GROUP BY action ORDER BY action)t;")
required=['analyze_contract','get_contract_analysis','list_contract_methods','get_contract_method_analysis','get_contract_execution_flow','get_contract_analysis_artifact','cancel','resume']
by_action={r['action']:r for r in audit};assert all('contract_analysis.'+name in by_action for name in required)
assert all(a['request_ids']and a['event_hashes']for a in audit)and by_action['contract_analysis.get_contract_analysis_artifact']['sensitive']
lifecycle=json.loads((directory/'lifecycle-final-state.json').read_text());assert not lifecycle['owned_containers']
before=json.loads((directory/'lifecycle-before-worker-loss.json').read_text());assert lifecycle['job']['fence']>before['job']['fence']and lifecycle['job']['attempts']>before['job']['attempts']
memory={}
for unit in['api','worker','mcp']:
 raw=subprocess.check_output(['systemctl','--user','show','blockchain-contract-coverage-qa-'+unit+'.service','-p','MainPID','-p','ActiveState','-p','NRestarts','-p','MemoryPeak','-p','MemoryMax'],text=True)
 record=dict(line.split('=',1)for line in raw.splitlines()if'='in line);assert record['ActiveState']=='active';memory[unit]=record
record={'status':'PASS','image':image,'jobs':rows,'audit':audit,'worker_fault':'explicitly killed isolated QA worker during running child; recovered at a greater fence and attempt',
 'completed_old_result_publication_race':race_result,
 'audit_scope':'durable action, request ID, event hash presence and sensitive export; full audit-chain cryptographic verification not performed',
 'service_memory':memory,'resource_scope':'service memory and independently recorded container cgroup peaks are separate'}
(directory/'database-audit-validation.json').write_text(json.dumps(record,indent=2));print('P2_DATABASE_AUDIT_PASS',len(rows),len(audit),flush=True)
