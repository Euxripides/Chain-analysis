"""Run candidate-identity-bound recovery gates; preserve each attempt."""
import argparse,datetime,hashlib,json,os,pathlib,subprocess,time,resource
resource.setrlimit(resource.RLIMIT_CORE,(0,0))
ROOT=pathlib.Path(__file__).resolve().parents[2];OUT=ROOT/'acceptance/contract-analysis-partial-recovery'
p=argparse.ArgumentParser();p.add_argument('phase',choices=['unit','runtime','qa','mcp']);a=p.parse_args()
image=(OUT/'engine-image.txt').read_text().strip()
freeze=json.loads((OUT/'frozen-engine'/image[7:23]/'engine-source-freeze.json').read_text())
assert all(hashlib.sha256((ROOT/f['path']).read_bytes()).hexdigest()==f['sha256']for f in freeze['files'])
out=OUT/'validation'/image[7:23]/(datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')+'-'+a.phase);out.mkdir(parents=True)
env={**os.environ,'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+os.environ['PATH'],'GOMAXPROCS':'2',
 'CONTRACT_TEST_IMAGE':image,'CONTRACT_PYTHON':str(ROOT/'scripts/contract-analysis/engine_python_bridge.py'),
 'CONTRACT_EXPECTED_RULE_VERSION':'funds-semantics-15','CONTRACT_CLOSURE_ENGINE_MODE':'frozen','CONTRACT_QA_OUT':str(out.relative_to(ROOT)),
 'CONTRACT_QA_API':'http://127.0.0.1:18091','CONTRACT_QA_MCP':'http://127.0.0.1:18092/mcp'}
gates=[]
def run(name,command,cwd=ROOT):
 started=time.monotonic();log=out/(name+'.log')
 with log.open('w')as f:result=subprocess.run(command,cwd=cwd,env=env,stdout=f,stderr=subprocess.STDOUT,timeout=1800)
 gates.append({'name':name,'image':image,'command':command,'working_directory':str(cwd.relative_to(ROOT)),
  'exit_code':result.returncode,'wall_seconds':time.monotonic()-started,'log':str(log.relative_to(OUT)),'log_sha256':hashlib.sha256(log.read_bytes()).hexdigest()})
 (out/'gates.json').write_text(json.dumps(gates,indent=2));print('P2_VALIDATION',name,result.returncode,flush=True)
 if result.returncode:print(log.read_text()[-6000:],flush=True);raise SystemExit(result.returncode)
if a.phase=='unit':
 run('engine-unit',['node','--test','--test-concurrency=2',*[str(p.relative_to(ROOT/'analysis-engines'))for p in sorted((ROOT/'analysis-engines/test').glob('*.test.mjs'))]],ROOT/'analysis-engines')
 run('backend',['go','test','./internal/contractanalysis','./internal/api','-run','Contract|Submitted|CodeOnly|EngineTransport|Next|Investigation|ContainerExecutionResource','-count=1'],ROOT/'backend')
 run('mcp-check',['npm','run','check'],ROOT/'mcp')
elif a.phase=='runtime':
 anvil=ROOT.parent/'contract-verification-20261015/analysis-engines/bin/anvil'
 assert hashlib.sha256(anvil.read_bytes()).hexdigest()=='00f97668e1ae0fcca118fd7d2d1b6d25c0496447a523ca6bc2dfd10958c99604'
 env['ANVIL_BINARY']=str(anvil)
 for key,script in [('permit','permit_runtime_qa.mjs'),('signature','closure_signature_runtime_qa.mjs'),('namespace','namespace_runtime_qa.mjs')]:run(key,['node','scripts/contract-analysis/'+script])
elif a.phase=='qa':run('qa-start',['python3','scripts/contract-analysis/p2_recovery_prepare.py','qa'])
elif a.phase=='mcp':run('actual-mcp',['node','mcp/scripts/contract-analysis-recovery-qa.mjs'])
print('P2_GATES_COMPLETE',a.phase,image,str(out),flush=True)
