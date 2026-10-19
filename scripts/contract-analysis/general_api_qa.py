"""Real tenant, lifecycle, immutable input and durable zero-provider checks."""
import base64,hashlib,json,os,pathlib,subprocess,time,urllib.parse
from qa_support import Client,fixture_clients,dotenv
ROOT=pathlib.Path(__file__).resolve().parents[2];OUT=ROOT/'acceptance/contract-analysis-general'
image=(OUT/'engine-image.txt').read_text().strip();out=OUT/('api-'+image[7:23]);out.mkdir(exist_ok=True)
results=[]
def check(name,condition,evidence=None):
 results.append({'scenario':name,'status':'PASS'if condition else'FAIL','evidence':evidence});(out/'checks.json').write_text(json.dumps(results,indent=2));print(name,results[-1]['status'],flush=True);assert condition,name
clients=fixture_clients();investigator=clients['investigator'];other=clients['other'];viewer=clients['viewer']
request={'input':{'kind':'source','source':{'language':'Solidity','files':{'API.sol':'pragma solidity ^0.8.24; contract API {uint public total;function update(uint n)external{require(n<100);total=n;}}'},'compiler_version':'0.8.30+commit.73712a01','target':'API.sol:API','settings':{'evmVersion':'shanghai'}}},'engines':['whatsabi','sevm','solc-ast'],'budget':{'max_contracts':1,'max_state_reads':1,'max_rpc_requests':10,'max_credits':2000},'idempotency_key':'general-api-'+image[7:19]}
check('viewer-cannot-submit-code',viewer.call('/api/v2/contract-analyses',request)[0]==403)
submitted=investigator.must('/api/v2/contract-analyses',request);job_id=submitted['analysis_id'];artifact=submitted['snapshot']['input_artifact']
(out/'analysis-id.txt').write_text(job_id)
check('authorized-code-job-readable',investigator.call('/api/v2/contract-analyses/'+job_id)[0]==200)
check('other-workspace-cannot-read-code',other.call('/api/v2/contract-analyses/'+job_id)[0]==404)
check('anonymous-cannot-read-code',Client().call('/api/v2/contract-analyses/'+job_id)[0]==401)
check('other-workspace-cannot-read-input',other.call('/api/v2/contract-analyses/'+job_id+'/artifacts/'+artifact['artifact_id'])[0]==404)
check('viewer-cannot-export-input',viewer.call('/api/v2/contract-analyses/'+job_id+'/artifacts/'+artifact['artifact_id'])[0]==403)
check('csrf-still-required-for-control',investigator.call('/api/v2/contract-analyses/'+job_id+'/cancel',{},csrf=False)[0]==403)
for _ in range(150):
 job=investigator.must('/api/v2/contract-analyses/'+job_id)
 if job['status']not in ['queued','running']:break
 time.sleep(1)
check('actual-code-worker-completed',job['status']=='completed',{'analysis_id':job_id,'error':job.get('error_code')})
(out/'job.json').write_text(json.dumps(job,indent=2))
chunk=investigator.must('/api/v2/contract-analyses/'+job_id+'/artifacts/'+artifact['artifact_id']+'?chunk_size=65536');data=base64.b64decode(chunk['content_base64']);check('authorized-original-input-sha',hashlib.sha256(data).hexdigest()==artifact['sha256'] and json.loads(data)['source']['files']==request['input']['source']['files'])
values=dotenv('/home/euripides/.config/blockchain-platform/contract-general-input-qa-20261017.env');d=urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']);assert d.path=='/contract_analysis_qa_20261014'
ids=[job_id];mcp=OUT/('mcp-'+image[7:23])/'offline-task-ids.json'
if mcp.exists():ids+=json.loads(mcp.read_text())
assert all(len(i)==36 and set(i)<=set('0123456789abcdef-')for i in ids)
joined=','.join("'"+i+"'::uuid"for i in ids)
query="SELECT json_agg(t)FROM(SELECT j.id,j.status,j.fence,j.request->'input'->>'kind' input_kind,(SELECT count(*)FROM provider_usage u WHERE u.job_id=j.id)usage_rows,(SELECT count(*)FROM contract_analysis_snapshots c WHERE c.analysis_id=j.id)deployment_snapshots,b.reserved_requests,b.reserved_credits,b.state_reads,b.processing_ms,j.boundary->>'input_sha256'input_sha256,j.result_meta->'snapshot'->>'input_sha256'output_input_sha256 FROM investigation_analysis_jobs j JOIN contract_analysis_budgets b ON b.analysis_id=j.id WHERE j.id IN("+joined+"))t;"
raw=subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',d.username,'-d',d.path[1:]],input=query.encode(),env={**os.environ,'PGPASSWORD':urllib.parse.unquote(d.password)})
rows=json.loads(raw);(out/'database-zero-provider.json').write_bytes(raw)
check('all-code-only-jobs-durable-zero-usage-and-no-deployment-snapshot',len(rows)==len(ids)and all(r['usage_rows']==0 and r['deployment_snapshots']==0 and r['reserved_requests']==0 and int(r['reserved_credits'])==0 and r['state_reads']==0 and r['input_sha256']==r['output_input_sha256'] for r in rows),{'jobs':len(rows)})
lifecycle=json.loads((OUT/('mcp-'+image[7:23])/'lifecycle-job.json').read_text())if mcp.exists()else None
check('resume-keeps-fencing-and-cumulative-processing',all(r['fence']>=1 and r['processing_ms']>0 for r in rows)and(lifecycle is None or any(r['id']==lifecycle['analysis_id']and r['fence']>=6 for r in rows)))
