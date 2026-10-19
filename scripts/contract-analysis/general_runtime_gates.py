"""Exercise the current frozen engine on local, real EVM regression scenarios."""
import hashlib,json,os,pathlib,subprocess,time
ROOT=pathlib.Path(__file__).resolve().parents[2];OUT=ROOT/'acceptance/contract-analysis-general';image=(OUT/'engine-image.txt').read_text().strip()
out=OUT/('runtime-'+image[7:23]);out.mkdir(exist_ok=True)
freeze=json.loads((OUT/'frozen-engine'/image[7:23]/'engine-source-freeze.json').read_text())
for f in freeze['files']:assert hashlib.sha256((ROOT/f['path']).read_bytes()).hexdigest()==f['sha256']
anvil=ROOT.parent/'contract-verification-20261015/analysis-engines/bin/anvil'
assert hashlib.sha256(anvil.read_bytes()).hexdigest()=='00f97668e1ae0fcca118fd7d2d1b6d25c0496447a523ca6bc2dfd10958c99604'
env={**os.environ,'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+os.environ['PATH'],
     'CONTRACT_TEST_IMAGE':image,'CONTRACT_EXPECTED_RULE_VERSION':'funds-semantics-14','CONTRACT_CLOSURE_ENGINE_MODE':'frozen',
     'CONTRACT_QA_OUT':str(out.relative_to(ROOT)),'CONTRACT_PYTHON':str(ROOT/'scripts/contract-analysis/engine_python_bridge.py'),'ANVIL_BINARY':str(anvil)}
gates=json.loads((out/'gates.json').read_text())if(out/'gates.json').exists()else{}
for key,script in [('permit','permit_runtime_qa.mjs'),('signature','closure_signature_runtime_qa.mjs'),('namespace','namespace_runtime_qa.mjs')]:
 old=gates.get(key,{})
 if old.get('status')=='PASS'and old.get('image')==image and old.get('script_sha256')==hashlib.sha256((ROOT/'scripts/contract-analysis'/script).read_bytes()).hexdigest()and(out/(key+'.log')).exists():
  print('GENERAL_REAL_EVM_REUSED',key,image,flush=True);continue
 started=time.monotonic()
 with(out/(key+'.log')).open('w')as f:p=subprocess.run(['node','scripts/contract-analysis/'+script],cwd=ROOT,env=env,stdout=f,stderr=subprocess.STDOUT,timeout=1200)
 gates[key]={'status':'PASS'if p.returncode==0 else'FAIL','image':image,'exit_code':p.returncode,'wall_seconds':time.monotonic()-started,
             'script_sha256':hashlib.sha256((ROOT/'scripts/contract-analysis'/script).read_bytes()).hexdigest(),'provider_http':0}
 (out/'gates.json').write_text(json.dumps(gates,indent=2));print('GENERAL_REAL_EVM',key,p.returncode,flush=True)
 if p.returncode:print((out/(key+'.log')).read_text()[-5000:],flush=True);raise SystemExit(p.returncode)
