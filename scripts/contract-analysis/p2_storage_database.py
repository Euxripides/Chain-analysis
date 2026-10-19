"""Reconcile actual memory/ABI MCP jobs with saved rows, artifacts, cost and audit."""
import hashlib,json,os,pathlib,re,subprocess,urllib.parse
from qa_support import dotenv
ROOT=pathlib.Path(__file__).resolve().parents[2];OUT=ROOT/'acceptance/contract-analysis-storage';image=(OUT/'engine-image.txt').read_text().strip();directory=pathlib.Path((OUT/'mcp-current.txt').read_text().strip());result=json.loads((directory/'result.json').read_text());assert result['status']=='PASS'and result['image']==image
jobs={}
for path in directory.glob('*-job.json'):
 job=json.loads(path.read_text());jobs[job['analysis_id']]=job
assert len(jobs)==3 and all(re.fullmatch(r'[0-9a-f-]{36}',aid)for aid in jobs)
values=dotenv('/home/euripides/.config/blockchain-platform/contract-storage-qa-20261017.env');db=urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']);assert db.path=='/contract_analysis_qa_20261014'
def sql(query):
 raw=subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',db.username,'-d',db.path[1:]],input=query.encode(),env={**os.environ,'PGPASSWORD':urllib.parse.unquote(db.password)})
 return json.loads(raw)
joined=','.join("'"+aid+"'::uuid"for aid in jobs);texts=','.join("'"+aid+"'"for aid in jobs)
rows=sql("SELECT json_agg(t)FROM(SELECT j.id,j.status,j.result_meta->>'rule_version' rule_version,j.result_meta->'snapshot'->>'identity_kind' identity_kind,j.boundary->>'input_sha256' input_sha256,j.result_meta->'snapshot'->>'input_sha256' output_input_sha256,(SELECT count(*)FROM provider_usage u WHERE u.job_id=j.id)usage_rows,(SELECT count(*)FROM contract_analysis_snapshots s WHERE s.analysis_id=j.id)deployment_snapshots,b.reserved_requests,b.reserved_credits,b.state_reads,b.processing_ms,(SELECT json_object_agg(section,n)FROM(SELECT section,count(*)n FROM investigation_analysis_rows r WHERE r.analysis_id=j.id GROUP BY section)q)section_counts FROM investigation_analysis_jobs j JOIN contract_analysis_budgets b ON b.analysis_id=j.id WHERE j.id IN("+joined+"))t;")
assert len(rows)==len(jobs)
for r in rows:
 assert r['status']=='completed'and r['rule_version']=='funds-semantics-17'and r['identity_kind']=='code_only'and r['input_sha256']==r['output_input_sha256']
 assert r['usage_rows']==r['deployment_snapshots']==r['reserved_requests']==r['state_reads']==0 and int(r['reserved_credits'])==0
 assert r['section_counts']=={k:v for k,v in jobs[r['id']]['metadata']['sections'].items()if v}
 assert jobs[r['id']]['metadata']['metrics']['engine_digest']==image
artifacts=sql("SELECT json_agg(t)FROM(SELECT analysis_id::text,artifact_id,kind,byte_size,sha256 FROM contract_analysis_artifacts WHERE analysis_id IN("+joined+")ORDER BY analysis_id,artifact_id)t;")
assert all(re.fullmatch(r'[a-f0-9]{64}',a['sha256'])and a['byte_size']>0 for a in artifacts)
audit=sql("SELECT json_agg(t)FROM(SELECT action,count(*)n,bool_and(request_id!='')request_ids,bool_and(event_hash!='')event_hashes,bool_and(sensitive)sensitive FROM audit_events_v2 WHERE resource_id IN("+texts+")AND result='success'GROUP BY action ORDER BY action)t;")
by_action={a['action']:a for a in audit};assert all('contract_analysis.'+name in by_action for name in['analyze_contract','get_contract_analysis','list_contract_methods','get_contract_method_analysis','get_contract_execution_flow','get_contract_analysis_artifact','compare_contract_versions']);assert all(a['request_ids']and a['event_hashes']for a in audit)and by_action['contract_analysis.get_contract_analysis_artifact']['sensitive']
service=json.loads((pathlib.Path((OUT/'service-current.txt').read_text().strip())/'binary-identity.json').read_text());assert service['production_pids_unchanged']
for name in['api','worker']:
 assert service['background_control_environment'][name]['DRPC_ENABLED']=='false'and service['background_control_environment'][name]['ANKR_MANAGEMENT_ENABLED']=='false'
record={'status':'PASS','image':image,'jobs':rows,'artifacts':artifacts,'audit':audit,'all_nine_MCP_tools':'verified by the complete client scenario including capabilities and a previously saved transaction',
 'audit_scope':'durable new-job actions, request IDs, event hashes and sensitive export; complete cryptographic audit-chain verification is not asserted',
 'provider_usage_scope':'these three offline code jobs only; historical background management cost remains NOT_VERIFIED','service_identity':service,'verifier_sha256':hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest()}
(directory/'database-audit-validation.json').write_text(json.dumps(record,indent=2));print('P2_STORAGE_DATABASE_AUDIT_PASS',len(rows),len(audit),len(artifacts),flush=True)
