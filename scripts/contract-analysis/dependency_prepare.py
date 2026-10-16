"""Build and byte-verify the dependency-model candidate over the accepted base."""
import hashlib,json,os,pathlib,shutil,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-dependency-models-20261016'
OUT=ROOT/'acceptance/contract-analysis-dependency-models/frozen-engine';OUT.mkdir(parents=True,exist_ok=True)
BASE='sha256:466f29f2075bc9c087e4fb7e00f2fc6cb11197abff3883823b4da33961b781b6'
assert subprocess.check_output(['docker','image','inspect','platform-contract-model-closure:20261016-symbols','--format','{{.Id}}'],text=True).strip()==BASE
build=ROOT/'.work/dependency/image-build';build.mkdir(parents=True,exist_ok=True)
for name in ['analyze.mjs','source.mjs','source-vyper.mjs','model.mjs','capabilities.mjs','replay.mjs','source-pc.mjs','opcode-witness.mjs']:shutil.copy2(ROOT/'analysis-engines'/name,build/name)
for name in ['ir','properties','verification','rules']:shutil.copytree(ROOT/'analysis-engines'/name,build/name,dirs_exist_ok=True)
shutil.copy2(ROOT/'analysis-engines/Dockerfile.dependency-models',build/'Dockerfile')
with(OUT/'engine-build.log').open('w')as log:subprocess.run(['docker','build','--network','none','--pull=false','--progress=plain','-t','platform-contract-dependency-models:20261016',str(build)],stdout=log,stderr=subprocess.STDOUT,check=True)
image=subprocess.check_output(['docker','image','inspect','platform-contract-dependency-models:20261016','--format','{{.Id}}'],text=True).strip();assert image.startswith('sha256:')and len(image)==71
docker=['docker','run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m']
source=ROOT/'analysis-engines';files=sorted(p for p in source.rglob('*')if p.is_file()and p.suffix in['.mjs','.py']and not{'node_modules','test','bin'}.intersection(p.relative_to(source).parts));paths=[str(p.relative_to(source))for p in files]
script="const fs=require('node:fs'),c=require('node:crypto');const names=JSON.parse(fs.readFileSync(0,'utf8'));process.stdout.write(JSON.stringify(Object.fromEntries(names.map(n=>[n,c.createHash('sha256').update(fs.readFileSync('/engine/'+n)).digest('hex')]))))"
actual=json.loads(subprocess.check_output([*docker,'--entrypoint','node',image,'-e',script],input=json.dumps(paths).encode()));records=[]
for path,file in zip(paths,files):
 expected=hashlib.sha256(file.read_bytes()).hexdigest();assert actual[path]==expected,path;records.append({'path':'analysis-engines/'+path,'image_path':'/engine/'+path,'sha256':expected})
cap=json.loads(subprocess.check_output([*docker,image],input=b'{"operation":"capabilities"}'));item=cap['sections']['capabilities'][0]
assert item['semantic_rules_version']=='funds-semantics-9'and item['state_model_verification']['factory_creation_identity']['create2_creator_salt_initcode_hash']
assert item['state_model_verification']['whole_authorization_proved']is False and item['state_model_verification']['whole_protocol_economics_proved']is False
(OUT/'engine-image.txt').write_text(image+'\n');(OUT/'capabilities.json').write_text(json.dumps(cap,indent=2));(OUT/'engine-source-freeze.json').write_text(json.dumps({'image':image,'base_image':BASE,'rule_version':'funds-semantics-9','files':records},indent=2))
print('DEPENDENCY_IMAGE_VERIFIED',image,'SOURCE_FILES',len(records),flush=True)
