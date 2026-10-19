"""Freeze the nonce candidate on the accepted namespace runtime without network access."""
import hashlib,json,pathlib,shutil,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2]
assert ROOT.name=='contract-permit-nonces-20261016'
OUT=ROOT/'acceptance/contract-analysis-permit-nonces/frozen-engine';OUT.mkdir(parents=True,exist_ok=True)
BASE='sha256:c0def36572929c176ad33937cf555aa89c92509e9e5479565fa33b873e48a1ed'
assert subprocess.check_output(['docker','image','inspect','platform-contract-namespaced-storage:20261016','--format','{{.Id}}'],text=True).strip()==BASE
previous=subprocess.run(['docker','image','inspect','platform-contract-permit-nonces:20261016','--format','{{.Id}}'],text=True,capture_output=True)
if previous.returncode==0:
 old=previous.stdout.strip();assert old.startswith('sha256:')and len(old)==71
 subprocess.run(['docker','tag',old,'platform-contract-permit-nonces:sha-'+old[7:23]],check=True)
build=ROOT/'.work/permit/image-build';(build/'verification').mkdir(parents=True,exist_ok=True)
for name in ['model.mjs','capabilities.mjs','verification/signatures.mjs','verification/state.mjs','verification/policies.mjs']:
 shutil.copy2(ROOT/'analysis-engines'/name,build/name)
shutil.copy2(ROOT/'analysis-engines/Dockerfile.permit-nonces',build/'Dockerfile')
with(OUT/'engine-build.log').open('w')as log:
 subprocess.run(['docker','build','--network','none','--pull=false','--progress=plain','-t','platform-contract-permit-nonces:20261016',str(build)],stdout=log,stderr=subprocess.STDOUT,check=True)
image=subprocess.check_output(['docker','image','inspect','platform-contract-permit-nonces:20261016','--format','{{.Id}}'],text=True).strip()
subprocess.run(['docker','tag',image,'platform-contract-permit-nonces:sha-'+image[7:23]],check=True)
docker=['docker','run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m']
source=ROOT/'analysis-engines';files=sorted(p for p in source.rglob('*')if p.is_file()and p.suffix in ['.mjs','.py']and not {'node_modules','test','bin'}.intersection(p.relative_to(source).parts))
paths=[str(p.relative_to(source))for p in files]
script="const fs=require('node:fs'),c=require('node:crypto');const names=JSON.parse(fs.readFileSync(0,'utf8'));process.stdout.write(JSON.stringify(Object.fromEntries(names.map(n=>[n,c.createHash('sha256').update(fs.readFileSync('/engine/'+n)).digest('hex')]))))"
actual=json.loads(subprocess.check_output([*docker,'--entrypoint','node',image,'-e',script],input=json.dumps(paths).encode()));records=[]
for path,file in zip(paths,files):
 expected=hashlib.sha256(file.read_bytes()).hexdigest();assert actual[path]==expected,path
 records.append({'path':'analysis-engines/'+path,'image_path':'/engine/'+path,'sha256':expected})
cap=json.loads(subprocess.check_output([*docker,image],input=b'{"operation":"capabilities"}'));item=cap['sections']['capabilities'][0]
assert item['semantic_rules_version']=='funds-semantics-13'
assert 'permit_nonce_monotonicity'in item['state_model_verification']['authorization_models']
assert item['state_model_verification']['nonce_state_analysis']['replay_protection_with_domain_history']=='not_proved'
assert item['state_model_verification']['whole_authorization_proved']is False and item['state_model_verification']['whole_protocol_economics_proved']is False
(OUT/'engine-image.txt').write_text(image+'\n');(OUT/'capabilities.json').write_text(json.dumps(cap,indent=2))
(OUT/'engine-source-freeze.json').write_text(json.dumps({'image':image,'base_image':BASE,'rule_version':'funds-semantics-13','files':records},indent=2))
print('PERMIT_IMAGE_BYTE_VERIFIED',image,len(records),flush=True)
