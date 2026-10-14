"""Real Docker timeout/OOM checks, with explicit fixture budgets."""
import json, pathlib, subprocess, urllib.request, os
ROOT=pathlib.Path(__file__).resolve().parents[2];OUT=ROOT/'acceptance/contract-analysis';rows=[]
image=(OUT/'engine-image.txt').read_text().strip();assert image.startswith('sha256:')
def check(name,condition,evidence=None):
 rows.append({'scenario':name,'status':'PASS' if condition else 'FAIL','evidence':evidence});(OUT/'resource-fault-qa.json').write_text(json.dumps(rows,indent=2));print(name,rows[-1]['status'],flush=True)
 if not condition:raise AssertionError(name)
subprocess.run(['systemctl','--user','stop','blockchain-contract-qa-worker.service'],check=True)
try:
 stage='contract-analysis-qa-stalled-source'
 subprocess.run(['docker','create','--name',stage,image],stdout=subprocess.DEVNULL,check=True)
 try:subprocess.run(['docker','commit','--change','ENTRYPOINT ["node","-e","setInterval(()=>{},1000)"]',stage,'contract-analysis-qa-stalled'],stdout=subprocess.DEVNULL,check=True)
 finally:subprocess.run(['docker','rm',stage],stdout=subprocess.DEVNULL,check=True)
 fixture=subprocess.check_output(['docker','image','inspect','contract-analysis-qa-stalled','--format','{{.Id}}']).decode().strip()
 env=os.environ.copy();env['PATH']='/usr/local/go/bin:'+env['PATH'];env['CONTRACT_RUNNER_FAULT_IMAGE']=fixture
 run=subprocess.run(['go','test','./internal/contractanalysis','-run','TestRunnerTimeoutAndOwnedOrphanCleanup','-v','-count=1'],cwd=ROOT/'backend',env=env,capture_output=True,text=True)
 check('runner-timeout-and-fenced-orphan-cleanup',run.returncode==0,run.stdout[-1800:]+run.stderr[-500:])
 name='contract-analysis-qa-oom-20261014'
 try:
  run=subprocess.run(['docker','run','--name',name,'--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','64m','--memory-swap','64m','--pids-limit','32','--entrypoint','node',image,'-e','const allocations=[];for(;;)allocations.push(Buffer.alloc(8<<20,1));'],capture_output=True,text=True,timeout=30)
  state=json.loads(subprocess.check_output(['docker','inspect',name,'--format','{{json .State}}']).decode())
  check('isolated-memory-limit-kills-workload',state['OOMKilled'] and state['ExitCode']==137,{'memory_budget_bytes':64<<20,'state':state})
 finally:subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 with urllib.request.urlopen('http://127.0.0.1:18081/health/ready') as response:data=json.load(response)
 check('api-remains-ready-after-resource-faults',response.status==200,data)
 oversized=b' '*((24<<20)+1)
 run=subprocess.run(['docker','run','--rm','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--memory','256m',image],input=oversized,capture_output=True,timeout=30)
 check('oversized-input-rejected-before-parsing',run.returncode!=0 and b'ENGINE_INPUT_TOO_LARGE' in run.stderr)
 print('RESOURCE_FAULT_QA_PASS',len(rows),flush=True)
finally:subprocess.run(['systemctl','--user','start','blockchain-contract-qa-worker.service'],check=True)
