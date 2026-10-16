"""Run factory and existing local-EVM regressions against the frozen candidate."""
import hashlib,json,os,pathlib,shutil,subprocess,time
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-dependency-models-20261016'
OUT=ROOT/'acceptance/contract-analysis-dependency-models/frozen-validation';OUT.mkdir(parents=True,exist_ok=True)
prepared=ROOT/'acceptance/contract-analysis-dependency-models/frozen-engine';freeze=json.loads((prepared/'engine-source-freeze.json').read_text());image=freeze['image'];assert image==(prepared/'engine-image.txt').read_text().strip()
(OUT/'engine-image.txt').write_text(image+'\n')
env=os.environ.copy();env.update({'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+env['PATH'],'GOMAXPROCS':'2',
 'CONTRACT_TEST_IMAGE':image,'CONTRACT_PYTHON':str(ROOT/'scripts/contract-analysis/engine_python_bridge.py'),
 'CONTRACT_DEPENDENCY_ENGINE_MODE':'frozen','CONTRACT_CLOSURE_ENGINE_MODE':'frozen','CONTRACT_QA_OUT':str(OUT.relative_to(ROOT)),
 'ANVIL_BINARY':str(ROOT.parent/'contract-verification-20261015/analysis-engines/bin/anvil'),
 'CONTRACT_CREATION_BINDING_CORPUS':str(OUT/'creation-runtime'),'CONTRACT_FACTORY_BINDING_CORPUS':str(OUT/'factory-runtime'),
 'CONTRACT_VERIFICATION_RUNTIME':str(OUT/'verification-runtime'),'CONTRACT_DEEPENING_RUNTIME':str(OUT/'deepening-runtime')})
assert hashlib.sha256(pathlib.Path(env['ANVIL_BINARY']).read_bytes()).hexdigest()=='00f97668e1ae0fcca118fd7d2d1b6d25c0496447a523ca6bc2dfd10958c99604'
vendors=ROOT/'.work/coverage/vendors';vendors.parent.mkdir(parents=True,exist_ok=True)
if not vendors.exists():vendors.symlink_to((ROOT.parent/'contract-coverage-20261014/.work/coverage/vendors').resolve(),target_is_directory=True)
gates={}
if os.environ.get('CONTRACT_DEPENDENCY_RESUME')=='1'and(OUT/'gates.json').exists():
 gates=json.loads((OUT/'gates.json').read_text());shutil.copyfile(OUT/'gates.json',OUT/('prior-attempt-'+str(time.time_ns())+'.json'))
def intact():
 for item in freeze['files']:assert hashlib.sha256((ROOT/item['path']).read_bytes()).hexdigest()==item['sha256'],item['path']
def run(name,args,cwd=ROOT):
 intact();started=time.monotonic()
 if gates.get(name,{}).get('status')=='PASS'and gates[name].get('image')==image:
  assert(OUT/(name+'.log')).is_file();print('REUSE_UNCHANGED_IMAGE_GATE',name,flush=True);return
 with(OUT/(name+'.log')).open('w')as log:p=subprocess.run(args,cwd=cwd,env=env,stdout=log,stderr=subprocess.STDOUT)
 gates[name]={'status':'PASS'if p.returncode==0 else'FAIL','exit_code':p.returncode,'wall_seconds':time.monotonic()-started,'image':image};(OUT/'gates.json').write_text(json.dumps(gates,indent=2));print('DEPENDENCY_FROZEN_GATE',name,p.returncode,flush=True)
 if p.returncode:print((OUT/(name+'.log')).read_text()[-7500:],flush=True);raise SystemExit(p.returncode)
 intact()
run('engine-unit',['node','--test','--test-concurrency=2',*map(str,sorted((ROOT/'analysis-engines/test').glob('*.test.mjs')))])
run('factory-runtime',['node','scripts/contract-analysis/dependency_factory_runtime_qa.mjs'])
for name,script in [('nonlinear-runtime','closure_runtime_qa.mjs'),('assembly-runtime','closure_assembly_runtime_qa.mjs'),('creation-runtime','closure_creation_runtime_qa.mjs'),('signature-runtime','closure_signature_runtime_qa.mjs'),('verification-runtime','verification_runtime_qa.mjs'),('deepening-runtime','deepening_runtime_qa.mjs'),('expression-runtime','expressions_runtime_qa.mjs'),('mechanism-runtime','mechanisms_runtime_qa.mjs'),('return-runtime','semantics_runtime_qa.mjs'),('runtime-fixtures','runtime_fixture_qa.mjs')]:run(name,['node','scripts/contract-analysis/'+script])
run('creation-binding-and-contract-backend',['go','test','./internal/contractanalysis','./internal/api','-run','Contract','-count=1','-v'],ROOT/'backend')
run('gold',['node','scripts/contract-analysis/coverage_gold_qa.mjs'])
intact();(OUT/'final-acceptance.json').write_text(json.dumps({'status':'PASS','scope':'frozen_engine_and_local_evm_regressions','image':image,'gates':gates,'whole_goal_complete':False},indent=2));print('DEPENDENCY_ENGINE_LOCAL_GATES_PASS_GOAL_REMAINS_ACTIVE',flush=True)
