"""Drop only the paused isolated worker's single heavy-admission DB session."""
import json,os,pathlib,re,subprocess,sys,urllib.parse
from qa_support import dotenv
aid=sys.argv[1];assert re.fullmatch(r'[0-9a-f-]{36}',aid)
values=dotenv('/home/euripides/.config/blockchain-platform/contract-partial-recovery-qa-20261017.env')
db=urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']);assert db.path=='/contract_analysis_qa_20261014'
unit='blockchain-contract-coverage-qa-worker.service'
pid=subprocess.check_output(['systemctl','--user','show',unit,'-p','MainPID','--value'],text=True).strip()
assert pid.isdigit()and int(pid)>1
state=next(l for l in pathlib.Path('/proc',pid,'status').read_text().splitlines()if l.startswith('State:'))
assert state.split()[1]=='T','Only a deliberately paused QA worker may lose its test DB session'
query="""WITH holder AS (SELECT a.pid FROM pg_locks l JOIN pg_stat_activity a USING(pid)
 WHERE a.datname=current_database() AND l.locktype='advisory' AND l.granted AND l.objsubid=1
 AND l.classid::bigint=((hashtextextended('contract-analysis-heavy-v1',0)>>32)&4294967295)
 AND l.objid::bigint=(hashtextextended('contract-analysis-heavy-v1',0)&4294967295)),
 owned AS(SELECT 1 FROM investigation_analysis_jobs WHERE id='"""+aid+"""'::uuid AND status='running' AND lease_owner IS NOT NULL)
 SELECT json_build_object('backend_pid',pid,'terminated',pg_terminate_backend(pid),'database',current_database())
 FROM holder WHERE (SELECT count(*)FROM holder)=1 AND EXISTS(SELECT 1 FROM owned);"""
raw=subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',db.username,'-d',db.path[1:]],input=query.encode(),env={**os.environ,'PGPASSWORD':urllib.parse.unquote(db.password)})
record=json.loads(raw);assert record['terminated']is True and record['database']=='contract_analysis_qa_20261014'
print(json.dumps({**record,'worker_pid':int(pid),'scope':'one paused QA worker heavy-admission session; production database excluded'}))
