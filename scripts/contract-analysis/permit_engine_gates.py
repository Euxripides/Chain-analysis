"""Run focused nonce/state regressions against an exact frozen candidate."""
import hashlib,json,os,pathlib,subprocess,sys,time
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-permit-nonces-20261016'
OUT=ROOT/'acceptance/contract-analysis-permit-nonces/frozen-validation';OUT.mkdir(parents=True,exist_ok=True)
freeze=json.loads((OUT.parent/'frozen-engine/engine-source-freeze.json').read_text());image=freeze['image']
env={**os.environ,'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+os.environ['PATH'],
 'GOMAXPROCS':'2','CONTRACT_TEST_IMAGE':image,'CONTRACT_PYTHON':str(ROOT/'scripts/contract-analysis/engine_python_bridge.py'),
 'CONTRACT_CLOSURE_ENGINE_MODE':'frozen','CONTRACT_QA_OUT':str(OUT.relative_to(ROOT)),
 'ANVIL_BINARY':str(ROOT.parent/'contract-verification-20261015/analysis-engines/bin/anvil')}
assert hashlib.sha256(pathlib.Path(env['ANVIL_BINARY']).read_bytes()).hexdigest()=='00f97668e1ae0fcca118fd7d2d1b6d25c0496447a523ca6bc2dfd10958c99604'
gates=json.loads((OUT/'gates.json').read_text())if(OUT/'gates.json').exists()else{}
scenario_files=sorted([*ROOT.joinpath('analysis-engines/test').glob('*.mjs'),*ROOT.joinpath('scripts/contract-analysis').glob('*.mjs')])
scenario_identity=hashlib.sha256(json.dumps([(str(p.relative_to(ROOT)),hashlib.sha256(p.read_bytes()).hexdigest())for p in scenario_files]).encode()).hexdigest()
def intact():
 for item in freeze['files']:assert hashlib.sha256((ROOT/item['path']).read_bytes()).hexdigest()==item['sha256'],item['path']
def run(name,args):
 intact()
 if gates.get(name,{}).get('status')=='PASS'and gates[name].get('image')==image and gates[name].get('scenario_sha256')==scenario_identity:
  assert(OUT/(name+'.log')).is_file();print('REUSE_VERIFIED_GATE',name,flush=True);return
 log=OUT/(name+'.log')
 if log.exists():log.rename(OUT/(str(time.time_ns())+'-'+log.name))
 started=time.monotonic()
 with log.open('w')as handle:p=subprocess.run(args,cwd=ROOT,env=env,stdout=handle,stderr=subprocess.STDOUT,timeout=1200)
 intact();gates[name]={'status':'PASS'if p.returncode==0 else'FAIL','exit_code':p.returncode,'image':image,'scenario_sha256':scenario_identity,'wall_seconds':time.monotonic()-started}
 (OUT/'gates.json').write_text(json.dumps(gates,indent=2));print('PERMIT_GATE',name,p.returncode,flush=True)
 if p.returncode:print(log.read_text()[-10000:],flush=True);raise SystemExit(p.returncode)
phase=sys.argv[1]if len(sys.argv)>1 else'unit';assert phase in ['unit','runtime']
if phase=='unit':run('engine-unit',['node','--test','--test-concurrency=2',*map(str,sorted((ROOT/'analysis-engines/test').glob('*.test.mjs')))])
else:
 assert gates.get('engine-unit',{}).get('status')=='PASS'and gates['engine-unit']['image']==image and gates['engine-unit'].get('scenario_sha256')==scenario_identity
 for name,script in [('permit-runtime','permit_runtime_qa.mjs'),('signature-runtime','closure_signature_runtime_qa.mjs'),('namespace-runtime','namespace_runtime_qa.mjs')]:run(name,['node','scripts/contract-analysis/'+script])
 (OUT/'final-acceptance.json').write_text(json.dumps({'status':'PASS','scope':'frozen_engine_nonce_signature_namespace_regressions','image':image,'gates':gates,'whole_goal_complete':False},indent=2))
