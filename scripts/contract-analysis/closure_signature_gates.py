"""Run signature proofs and actual local-chain execution for one source freeze."""
import hashlib,json,os,pathlib,subprocess,time
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-model-closure-20261016'
stage=os.environ.get('CONTRACT_SIGNATURE_STAGE','r8');assert stage in['r8','r9','r10','signature-frozen']
OUT=ROOT/'acceptance/contract-analysis-model-closure'/stage;OUT.mkdir(parents=True,exist_ok=True)
frozen=os.environ.get('CONTRACT_CLOSURE_ENGINE_MODE')=='frozen'
image=(ROOT/'acceptance/contract-analysis-model-closure'/('signature-engine'if frozen else'frozen-engine')/'engine-image.txt').read_text().strip()
freeze=json.loads((ROOT/'.work/closure/source-freeze.json').read_text());(OUT/'changed-source-freeze.json').write_text(json.dumps(freeze,indent=2))
env=os.environ.copy();env.update({'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+env['PATH'],'GOMAXPROCS':'2','CONTRACT_TEST_IMAGE':image,
 'CONTRACT_PYTHON':str(ROOT/'scripts/contract-analysis/engine_python_bridge.py'),'CONTRACT_CLOSURE_ENGINE_MODE':'frozen'if frozen else'host',
 'ANVIL_BINARY':str(ROOT.parent/'contract-verification-20261015/analysis-engines/bin/anvil'),'CONTRACT_QA_OUT':str(OUT.relative_to(ROOT))})
env['CONTRACT_SHADOW_BASELINE']=str(ROOT/'acceptance/contract-analysis-model-closure/shadow-baseline')
gates={}
def intact():
 for item in freeze:assert hashlib.sha256((ROOT/item['path']).read_bytes()).hexdigest()==item['sha256'],item['path']
for name,args in [('engine-unit',['node','--test','--test-concurrency=2',*map(str,sorted((ROOT/'analysis-engines/test').glob('*.test.mjs')))]),('signature-runtime',['node','scripts/contract-analysis/closure_signature_runtime_qa.mjs'])]:
 intact();started=time.monotonic()
 with(OUT/(name+'.log')).open('w')as log:result=subprocess.run(args,cwd=ROOT,env=env,stdout=log,stderr=subprocess.STDOUT)
 gates[name]={'status':'PASS'if result.returncode==0 else'FAIL','exit_code':result.returncode,'wall_seconds':round(time.monotonic()-started,3),'image':image,'engine_mode':env['CONTRACT_CLOSURE_ENGINE_MODE']}
 (OUT/'gates.json').write_text(json.dumps(gates,indent=2));print('SIGNATURE_GATE',name,result.returncode,flush=True)
 if result.returncode:print((OUT/(name+'.log')).read_text()[-7500:],flush=True);raise SystemExit(result.returncode)
 intact()
print('SIGNATURE_PROOFS_AND_LOCAL_EVM_PASS_GOAL_REMAINS_ACTIVE',flush=True)
