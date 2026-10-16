"""Fault injection restricted to the isolated QA database and worker unit."""
import json, os, pathlib, subprocess, time, urllib.parse, uuid
from qa_support import ROOT, Client, dotenv, fixture_clients
from live_api_qa import await_job

OUT=ROOT/os.environ.get('CONTRACT_QA_OUT','acceptance/contract-analysis');rows=[]
UNIT=os.environ.get('CONTRACT_QA_WORKER','blockchain-contract-qa-worker.service')
env={**os.environ,**dotenv('/home/euripides/projects/blockchain-platform/deploy/.env'),**dotenv(os.environ.get('CONTRACT_QA_ENV','/home/euripides/.config/blockchain-platform/contract-analysis-qa-20261014.env'))}
db=urllib.parse.urlparse(env['MIGRATION_DATABASE_URL'])
assert db.path=='/contract_analysis_qa_20261014'
env['PGPASSWORD']=urllib.parse.unquote(db.password)
def sql(text):
    return subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',db.username,'-d',db.path[1:]],input=text.encode(),env=env).decode().strip()
def unit(operation):subprocess.run(['systemctl','--user',operation,UNIT],check=True)
def check(name,ok,evidence=None):
    rows.append({'scenario':name,'status':'PASS' if ok else 'FAIL','evidence':evidence});(OUT/'lifecycle-qa.json').write_text(json.dumps(rows,indent=2));print(name,rows[-1]['status'],flush=True)
    if not ok:raise AssertionError(name)
def containers(aid):return subprocess.check_output(['docker','ps','-a','--filter','name=^/contract-analysis-'+aid+'-','--format','{{.Names}}']).decode().split()
def wait_container(aid):
    deadline=time.monotonic()+90
    while time.monotonic()<deadline:
        names=containers(aid)
        if names:return names[0]
        time.sleep(.04)
    raise RuntimeError('owned workload not observed')

client=fixture_clients()['investigator'];prefix='lifecycle-'+uuid.uuid4().hex
max_rpc=int(os.environ.get('CONTRACT_QA_LIFECYCLE_MAX_RPC','100'));assert 40<=max_rpc<=100
def submit(suffix,timeout=1800):return client.must('/api/v2/contract-analyses',{'chain':'ethereum','address':'0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2','block':19000000,'idempotency_key':prefix+suffix,'budget':{'max_contracts':2,'max_rpc_requests':max_rpc,'max_state_reads':24,'timeout_seconds':timeout}})
def budget(aid):return json.loads(sql("SELECT json_build_object('requests',reserved_requests,'credits',reserved_credits,'processing_ms',processing_ms) FROM contract_analysis_budgets WHERE analysis_id='"+aid+"'"))

try:
    unit('stop');queued=submit('-queued');aid=queued['analysis_id'];before=budget(aid)
    cancelled=client.must('/api/v2/contract-analyses/'+aid+'/cancel',{})
    check('queued-cancel-persists',cancelled['status']=='cancelled')
    resumed=client.must('/api/v2/contract-analyses/'+aid+'/resume',{})
    check('resume-preserves-frozen-snapshot',resumed['snapshot']==cancelled['snapshot'])
    check('resume-does-not-reset-rpc-budget',budget(aid)['requests']==before['requests'])
    unit('start');finished=await_job(client,aid,180)
    check('resumed-queued-job-completes',finished['status']=='completed',aid)
    check('completed-job-cannot-resume',client.call('/api/v2/contract-analyses/'+aid+'/resume',{})[0]==409)
    unit('stop');running=submit('-running');runid=running['analysis_id'];unit('start');name=wait_container(runid)
    cancelled=client.must('/api/v2/contract-analyses/'+runid+'/cancel',{});check('running-cancel-persists',cancelled['status']=='cancelled',name)
    deadline=time.monotonic()+20
    while containers(runid) and time.monotonic()<deadline:time.sleep(.2)
    check('cancel-removes-owned-workload',not containers(runid))
    check('cancelled-worker-cannot-publish',sql("SELECT count(*) FROM investigation_analysis_rows WHERE analysis_id='"+runid+"'")=='0')
    before=budget(runid);client.must('/api/v2/contract-analyses/'+runid+'/resume',{});finished=await_job(client,runid,180)
    check('running-cancel-can-resume',finished['status']=='completed')
    check('resumed-budget-is-monotonic',budget(runid)['requests']>=before['requests'])

    unit('stop');crashed=submit('-crash');crashid=crashed['analysis_id'];unit('start');name=wait_container(crashid)
    subprocess.run(['docker','pause',name],check=True,stdout=subprocess.DEVNULL)
    pid=int(subprocess.check_output(['systemctl','--user','show',UNIT,'-p','MainPID','--value']).decode())
    check('crash-target-is-isolated-worker',pathlib.Path('/proc/'+str(pid)+'/exe').resolve()==ROOT/'backend/bin/contract-analysis-worker')
    fence=int(sql("SELECT fence FROM investigation_analysis_jobs WHERE id='"+crashid+"'"))
    subprocess.run(['systemctl','--user','kill','--kill-whom=main','--signal=SIGKILL',UNIT],check=True)
    check('crash-retains-unpublished-job',sql("SELECT status FROM investigation_analysis_jobs WHERE id='"+crashid+"'")=='running')
    check('crashed-job-has-no-published-rows',sql("SELECT count(*) FROM investigation_analysis_rows WHERE analysis_id='"+crashid+"'")=='0')
    check('crash-leaves-detectable-owned-container',name in containers(crashid))
    deadline=time.monotonic()+40
    while sql("SELECT lease_until<now() FROM investigation_analysis_jobs WHERE id='"+crashid+"'")!='t' and time.monotonic()<deadline:time.sleep(1)
    unit('start');finished=await_job(client,crashid,180)
    check('lease-expiry-reclaims-and-completes',finished['status']=='completed',{'analysis_id':crashid,'attempts':finished['attempts']})
    check('reclaimed-worker-increments-fence',int(sql("SELECT fence FROM investigation_analysis_jobs WHERE id='"+crashid+"'"))>fence)
    check('reclaimed-worker-removes-stale-container',not containers(crashid))
    rowsCount=sql("SELECT count(*)=count(DISTINCT (section,ordinal)) FROM investigation_analysis_rows WHERE analysis_id='"+crashid+"'")
    check('recovery-publishes-each-row-once',rowsCount=='t')
    stale=sql("UPDATE investigation_analysis_jobs SET error_message='stale publication' WHERE id='"+crashid+"' AND fence="+str(fence)+" AND status='running' RETURNING id")
    check('old-fence-cannot-modify-published-job',stale=='UPDATE 0')

    unit('stop');timeout=submit('-timeout',30);tid=timeout['analysis_id'];client.must('/api/v2/contract-analyses/'+tid+'/cancel',{})
    sql("UPDATE contract_analysis_budgets SET processing_ms=30000 WHERE analysis_id='"+tid+"'")
    client.must('/api/v2/contract-analyses/'+tid+'/resume',{});unit('start');finished=await_job(client,tid,60)
    check('exhausted-processing-budget-fails-before-work',finished['status']=='failed' and finished['error_code']=='CONTRACT_ANALYSIS_TIMEOUT')
    check('timeout-does-not-leave-container',not containers(tid))
    print('LIFECYCLE_QA_PASS',len(rows),flush=True)
finally:
    unit('start')
