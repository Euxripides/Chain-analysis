"""Validate task-linked auditing on the existing MCP control aliases."""
import datetime,hashlib,json,os,pathlib,subprocess,time
ROOT=pathlib.Path(__file__).resolve().parents[2];BASE=ROOT/'acceptance/contract-analysis-general'
image=(BASE/'engine-image.txt').read_text().strip();suffix=image[7:23]
freeze=json.loads((BASE/'frozen-engine'/suffix/'engine-source-freeze.json').read_text())
assert all(hashlib.sha256((ROOT/f['path']).read_bytes()).hexdigest()==f['sha256']for f in freeze['files'])
prior_path=sorted((BASE/'validation'/suffix).glob('*/gates.json'))[-1];prior=json.loads(prior_path.read_text());assert len(prior)==9 and all(g['exit_code']==0 for g in prior)
out=BASE/'validation'/suffix/(datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')+'-audit');out.mkdir(parents=True)
env={**os.environ,'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+os.environ['PATH'],'GOMAXPROCS':'2',
 'CONTRACT_QA_API':'http://127.0.0.1:18091','CONTRACT_QA_MCP':'http://127.0.0.1:18092/mcp','GENERAL_QA_LIFECYCLE_APPROVED':'isolated_only'}
stats={}
for unit in ['api','worker','mcp']:
 stats[unit]=subprocess.check_output(['systemctl','--user','show','blockchain-contract-coverage-qa-'+unit+'.service','-p','MainPID','-p','MemoryPeak','-p','MemoryMax','-p','NRestarts'],text=True)
(BASE/'audit-control-fix/before/service-memory.json').write_text(json.dumps(stats,indent=2))
gates=[]
def run(name,command,cwd=ROOT):
 started=time.monotonic();log=out/(name+'.log')
 with log.open('w')as f:result=subprocess.run(command,cwd=cwd,env=env,stdout=f,stderr=subprocess.STDOUT,timeout=900)
 gates.append({'name':name,'image':image,'command':command,'working_directory':str(cwd.relative_to(ROOT)),'exit_code':result.returncode,'wall_seconds':time.monotonic()-started,'log':str(log.relative_to(BASE))})
 (out/'gates.json').write_text(json.dumps(gates,indent=2));print('P1_AUDIT_GATE',name,result.returncode,flush=True)
 if result.returncode:print(log.read_text()[-5000:],flush=True);raise SystemExit(result.returncode)
for old in prior:
 name=old['name']
 if name=='backend':run(name,['go','test','./internal/api','-run','Contract|Next|Investigation','-count=1'],ROOT/'backend')
 elif name=='qa-start':run(name,['python3','scripts/contract-analysis/general_prepare.py','qa'])
 elif name=='api':run(name,['python3','scripts/contract-analysis/general_api_qa.py'])
 else:
  path=BASE/old['log'];assert path.is_file();gates.append({**old,'reused_from_completed_gate':str(prior_path.relative_to(BASE)),'reused_log_sha256':hashlib.sha256(path.read_bytes()).hexdigest()})
  (out/'gates.json').write_text(json.dumps(gates,indent=2))
run('mcp-audit',['node','mcp/scripts/contract-analysis-general-qa.mjs','audit'])
run('database-audit',['python3','scripts/contract-analysis/general_database_qa.py'])
print('P1_AUDIT_ALIAS_VALIDATED',image,str(out),flush=True)
