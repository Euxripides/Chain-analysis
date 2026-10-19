"""Freeze and validate the P1 candidate without changing production services."""
import argparse,datetime,hashlib,json,os,pathlib,subprocess,time

ROOT=pathlib.Path(__file__).resolve().parents[2]
OUT=ROOT/'acceptance/contract-analysis-general'
env={**os.environ,'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+os.environ['PATH'],'GOMAXPROCS':'2','CONTRACT_QA_API':'http://127.0.0.1:18091','CONTRACT_QA_MCP':'http://127.0.0.1:18092/mcp'}
p=argparse.ArgumentParser();p.add_argument('--existing-engine',action='store_true');p.add_argument('--resume-from');args=p.parse_args()
if not args.existing_engine:subprocess.run(['python3','scripts/contract-analysis/general_prepare.py','engine'],cwd=ROOT,env=env,check=True)
image=(OUT/'engine-image.txt').read_text().strip()
freeze=json.loads((OUT/'frozen-engine'/image[7:23]/'engine-source-freeze.json').read_text())
assert all(hashlib.sha256((ROOT/f['path']).read_bytes()).hexdigest()==f['sha256']for f in freeze['files'])
assert hashlib.sha256((ROOT/'analysis-engines/Dockerfile.general-input').read_bytes()).hexdigest()==freeze['dockerfile_sha256']
assert subprocess.check_output(['docker','image','inspect',image,'--format','{{.Id}}'],text=True).strip()==image
out=OUT/'validation'/image[7:23]/datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ');out.mkdir(parents=True)
env.update(CONTRACT_TEST_IMAGE=image,CONTRACT_PYTHON=str(ROOT/'scripts/contract-analysis/engine_python_bridge.py'))
gates=[]
previous={}
if args.resume_from:
 old=pathlib.Path(args.resume_from)
 if not old.is_absolute():old=OUT/old
 assert old.resolve().is_relative_to(OUT.resolve())
 previous={g['name']:g for g in json.loads(old.read_text())if g['exit_code']==0 and g['image']==image}
def run(name,command,cwd=ROOT):
 prior=previous.get(name)
 if prior and prior['command']==command and prior['working_directory']==str(cwd.relative_to(ROOT)):
  old_log=OUT/prior['log'];assert old_log.is_file()
  gates.append({**prior,'reused_from_completed_gate':str(old.relative_to(OUT)),'reused_log_sha256':hashlib.sha256(old_log.read_bytes()).hexdigest()})
  (out/'gates.json').write_text(json.dumps(gates,indent=2));print('P1_VALIDATION_REUSED',name,0,flush=True);return
 started=time.monotonic();log=out/(name+'.log')
 with log.open('w')as f:result=subprocess.run(command,cwd=cwd,env=env,stdout=f,stderr=subprocess.STDOUT,timeout=1800)
 gates.append({'name':name,'image':image,'command':command,'working_directory':str(cwd.relative_to(ROOT)),'exit_code':result.returncode,'wall_seconds':time.monotonic()-started,'log':str(log.relative_to(OUT))})
 (out/'gates.json').write_text(json.dumps(gates,indent=2));print('P1_VALIDATION',name,result.returncode,flush=True)
 if result.returncode:
  print(log.read_text()[-6000:],flush=True);raise SystemExit(result.returncode)
run('engine-unit',['node','--test','--test-concurrency=2',*[str(p.relative_to(ROOT/'analysis-engines'))for p in sorted((ROOT/'analysis-engines/test').glob('*.test.mjs'))]],ROOT/'analysis-engines')
run('backend',['go','test','./internal/contractanalysis','./internal/api','-run','Contract|Submitted|CodeOnly|EngineTransport','-count=1'],ROOT/'backend')
run('mcp-check',['npm','run','check'],ROOT/'mcp')
run('qa-start',['python3','scripts/contract-analysis/general_prepare.py','qa'])
for phase in ['offline','large','compressed']:
 run('mcp-'+phase,['node','mcp/scripts/contract-analysis-general-qa.mjs',phase])
run('api',['python3','scripts/contract-analysis/general_api_qa.py'])
run('openapi-discovery',['node','mcp/scripts/contract-analysis-openapi.mjs'])
print('P1_OFFLINE_GATES_COMPLETE',image,str(out),flush=True)
