"""Bind memory/ABI QA evidence to an immutable engine and scenario sources."""
import datetime,hashlib,json,os,pathlib,resource,subprocess,sys,time
resource.setrlimit(resource.RLIMIT_CORE,(0,0));ROOT=pathlib.Path(__file__).resolve().parents[2];OUT=ROOT/'acceptance/contract-analysis-memory';image=(OUT/'engine-image.txt').read_text().strip();freeze=json.loads((OUT/'frozen-engine'/image[7:23]/'engine-source-freeze.json').read_text())
assert all(hashlib.sha256((ROOT/f['path']).read_bytes()).hexdigest()==f['sha256']for f in freeze['files'])
phase=sys.argv[1];assert phase in['unit','runtime','qa','source-runtime','mcp'];out=OUT/'validation'/image[7:23]/(datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')+'-'+phase);out.mkdir(parents=True)
env={**os.environ,'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+os.environ['PATH'],'GOMAXPROCS':'2','CONTRACT_TEST_IMAGE':image,'P2_TAC_IMAGE':image,'CONTRACT_PYTHON':str(ROOT/'scripts/contract-analysis/engine_python_bridge.py'),
 'CONTRACT_EXPECTED_RULE_VERSION':'funds-semantics-16','CONTRACT_CLOSURE_ENGINE_MODE':'frozen','CONTRACT_QA_OUT':str(out.relative_to(ROOT)),
 'CONTRACT_QA_API':'http://127.0.0.1:18091','CONTRACT_QA_MCP':'http://127.0.0.1:18092/mcp','ANVIL_BINARY':str(ROOT.parent/'contract-verification-20261015/analysis-engines/bin/anvil')};gates=[]
scenarios=sorted([*ROOT.joinpath('analysis-engines/test').rglob('*.mjs'),*ROOT.joinpath('scripts/contract-analysis').glob('*.mjs'),ROOT/'mcp/scripts/contract-analysis-memory-qa.mjs']);scenario_sha=hashlib.sha256(json.dumps([(str(p.relative_to(ROOT)),hashlib.sha256(p.read_bytes()).hexdigest())for p in scenarios]).encode()).hexdigest()
def run(name,command,cwd=ROOT):
 started=time.monotonic();log=out/(name+'.log')
 with log.open('w')as f:p=subprocess.run(command,cwd=cwd,env=env,stdout=f,stderr=subprocess.STDOUT,timeout=1800)
 assert all(hashlib.sha256((ROOT/f['path']).read_bytes()).hexdigest()==f['sha256']for f in freeze['files'])
 gates.append({'name':name,'image':image,'scenario_sha256':scenario_sha,'command':command,'working_directory':str(cwd.relative_to(ROOT)),'exit_code':p.returncode,'wall_seconds':time.monotonic()-started,'log':str(log.relative_to(OUT)),'log_sha256':hashlib.sha256(log.read_bytes()).hexdigest()});(out/'gates.json').write_text(json.dumps(gates,indent=2));print('P2_MEMORY_VALIDATION',name,p.returncode,flush=True)
 if p.returncode:print(log.read_text()[-6000:],flush=True);raise SystemExit(p.returncode)
if phase=='unit':
 run('engine-unit',['node','--test','--test-concurrency=2',*[str(p.relative_to(ROOT/'analysis-engines'))for p in sorted((ROOT/'analysis-engines/test').glob('*.test.mjs'))]],ROOT/'analysis-engines')
 run('backend',['go','test','./internal/contractanalysis','./internal/api','-run','Contract|Submitted|CodeOnly|EngineTransport|Next|Investigation|ContainerExecutionResource','-count=1'],ROOT/'backend');run('mcp-check',['npm','run','check'],ROOT/'mcp')
elif phase=='qa':run('qa-start',['python3','scripts/contract-analysis/p2_memory_prepare.py','qa'])
elif phase=='source-runtime':run('source-bytes-runtime',['node','scripts/contract-analysis/p2_source_bytes_runtime.mjs'])
elif phase=='runtime':
 run('tac-model',['node','scripts/contract-analysis/p2_tac_memory_capture.mjs'])
 for name,script in[('byte-memory','p2_memory_runtime.mjs'),('tac-memory','p2_tac_memory_runtime.mjs'),('abi','p2_abi_runtime.mjs'),('permit','permit_runtime_qa.mjs'),('signature','closure_signature_runtime_qa.mjs'),('namespace','namespace_runtime_qa.mjs')]:run(name,['node','scripts/contract-analysis/'+script])
elif phase=='mcp':run('actual-mcp',['node','mcp/scripts/contract-analysis-memory-qa.mjs'])
print('P2_MEMORY_GATES_COMPLETE',phase,image,str(out),flush=True)
