"""Read only the isolated QA job's fence and owned-container state."""
import json,os,pathlib,re,subprocess,sys,urllib.parse
from qa_support import dotenv
ROOT=pathlib.Path(__file__).resolve().parents[2]
aid=sys.argv[1];assert re.fullmatch(r'[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}',aid)
values=dotenv('/home/euripides/.config/blockchain-platform/contract-partial-recovery-qa-20261017.env')
db=urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']);assert db.path=='/contract_analysis_qa_20261014'
sql="SELECT row_to_json(t) FROM(SELECT j.id::text,j.status,j.phase,j.fence,j.attempts,j.lease_owner,j.lease_until,(SELECT count(*)FROM investigation_analysis_rows r WHERE r.analysis_id=j.id)published_rows,(SELECT coalesce(json_agg(json_build_object('fence',e.fence,'status',e.status,'has_output',coalesce(e.output_manifest <> '{}'::jsonb,false))),'[]'::json)FROM contract_analysis_engine_runs e WHERE e.analysis_id=j.id)engine_runs FROM investigation_analysis_jobs j WHERE j.id='"+aid+"')t;"
raw=subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',db.username,'-d',db.path[1:]],input=sql.encode(),env={**os.environ,'PGPASSWORD':urllib.parse.unquote(db.password)})
job=json.loads(raw)
containers=subprocess.check_output(['docker','ps','-a','--filter','name=^/contract-analysis-'+aid+'-','--format','{{.Names}}'],text=True).splitlines()
assert all(re.fullmatch('contract-analysis-'+re.escape(aid)+r'-[0-9]+',name)for name in containers)
worker=subprocess.check_output(['systemctl','--user','show','blockchain-contract-coverage-qa-worker.service','-p','MainPID','-p','Restart','-p','NRestarts','-p','MemoryCurrent','-p','MemoryPeak','-p','MemoryMax','-p','CPUUsageNSec'],text=True)
print(json.dumps({'job':job,'owned_containers':containers,'worker':worker,'engine_output_check':'nonempty_manifest_v2_default_empty_object_is_not_a_result'}))
