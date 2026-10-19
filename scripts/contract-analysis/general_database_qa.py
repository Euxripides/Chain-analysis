"""Read durable results, provider budgets and audit for the candidate MCP jobs."""
import json,os,pathlib,subprocess,urllib.parse
from qa_support import dotenv
ROOT=pathlib.Path(__file__).resolve().parents[2];OUT=ROOT/'acceptance/contract-analysis-general';image=(OUT/'engine-image.txt').read_text().strip()
directory=OUT/('mcp-'+image[7:23]);jobs={}
for path in directory.glob('*-job.json'):
 job=json.loads(path.read_text());jobs[job['analysis_id']]=job
api_path=OUT/('api-'+image[7:23])/'job.json'
if api_path.exists():
 job=json.loads(api_path.read_text());jobs[job['analysis_id']]=job
assert len(jobs)>=11,'Run offline, large, compressed, live and API checks first'
values=dotenv('/home/euripides/.config/blockchain-platform/contract-general-input-qa-20261017.env');d=urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']);assert d.path=='/contract_analysis_qa_20261014'
def sql(query):
 raw=subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',d.username,'-d',d.path[1:]],input=query.encode(),env={**os.environ,'PGPASSWORD':urllib.parse.unquote(d.password)})
 return json.loads(raw)
assert all(len(i)==36 and set(i)<=set('0123456789abcdef-')for i in jobs)
joined=','.join("'"+i+"'::uuid"for i in jobs);text_ids=','.join("'"+i+"'"for i in jobs)
rows=sql("SELECT json_agg(t)FROM(SELECT j.id,j.status,j.fence,j.result_meta->'snapshot'->>'identity_kind' identity_kind,j.boundary->>'input_sha256' input_sha256,j.result_meta->'snapshot'->>'input_sha256' output_input_sha256,(SELECT count(*)FROM provider_usage u WHERE u.job_id=j.id)usage_rows,(SELECT count(*)FROM contract_analysis_snapshots s WHERE s.analysis_id=j.id)deployment_snapshots,b.reserved_requests,b.reserved_credits,b.state_reads,b.processing_ms,(SELECT json_object_agg(section,n)FROM(SELECT section,count(*)n FROM investigation_analysis_rows r WHERE r.analysis_id=j.id GROUP BY section)q)section_counts FROM investigation_analysis_jobs j JOIN contract_analysis_budgets b ON b.analysis_id=j.id WHERE j.id IN("+joined+"))t;")
(directory/'database-candidate-rows.json').write_text(json.dumps(rows,indent=2))
assert len(rows)==len(jobs)
for row in rows:
 assert row['status']=='completed'
 expected=jobs[row['id']]['metadata']['sections'];assert row['section_counts']=={k:v for k,v in expected.items()if v},row['id']
 if row['identity_kind']=='code_only':
  assert row['usage_rows']==0 and row['deployment_snapshots']==0 and row['reserved_requests']==0 and int(row['reserved_credits'])==0 and row['state_reads']==0
  assert row['input_sha256']==row['output_input_sha256']
 else:
  snapshot=jobs[row['id']]['metadata']['snapshot']
  assert row['deployment_snapshots']>=1 and snapshot.get('chain')and snapshot.get('chain_id')and snapshot.get('block_number',0)>0 and snapshot.get('block_hash')and snapshot.get('entry_address')
  if row['identity_kind']in[None,'']:
   # Legacy address requests retain their original JSON shape. Their observed
   # identity is established by the actual snapshot and deployment catalogue.
   assert row['input_sha256']in[None,''];row['validated_identity']='legacy_observed_deployment'
  else:assert row['identity_kind']=='observed_deployment'
audit=sql("SELECT json_agg(t)FROM(SELECT action,count(*)n,min(seq)first_seq,max(seq)last_seq,bool_and(request_id!='')request_ids,bool_and(event_hash!='')event_hashes,bool_and(sensitive)sensitive FROM audit_events_v2 WHERE resource_id IN("+text_ids+")AND result='success'GROUP BY action ORDER BY action)t;")
(directory/'audit-candidate-actions.json').write_text(json.dumps(audit,indent=2));print('OBSERVED_AUDIT_ACTIONS',[r['action']for r in audit],flush=True)
by_action={r['action']:r for r in audit}
required=['analyze_contract','get_contract_analysis','list_contract_methods','get_contract_method_analysis','get_contract_execution_flow','explain_contract_transaction','compare_contract_versions','get_contract_analysis_artifact','cancel','resume']
assert all('contract_analysis.'+name in by_action for name in required)
assert all(r['request_ids']and r['event_hashes']for r in audit)
assert by_action['contract_analysis.get_contract_analysis_artifact']['sensitive']
memory={}
for unit in ['api','worker','mcp']:
 raw=subprocess.check_output(['systemctl','--user','show','blockchain-contract-coverage-qa-'+unit+'.service','-p','MainPID','-p','NRestarts','-p','ActiveState','-p','MemoryPeak','-p','MemoryMax'],text=True)
 record=dict(line.split('=',1)for line in raw.splitlines()if'='in line);assert record['ActiveState']=='active'and record['NRestarts']=='0';memory[unit]=record
document={'image':image,'status':'PASS','jobs':rows,'audit':audit,'audit_scope':'durable action, request ID, event hash presence and sensitive export; not a full audit-chain cryptographic verification',
          'service_memory':memory,'resource_scope':'systemd service memory excludes separately managed Docker container memory'}
(directory/'database-audit-validation.json').write_text(json.dumps(document,indent=2));print('CANDIDATE_DATABASE_AUDIT_PASS',len(rows),'jobs',len(audit),'actions',flush=True)
