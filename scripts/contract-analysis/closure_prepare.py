"""Build and byte-verify the model candidate using the frozen deployed engine."""
import hashlib,json,os,pathlib,shutil,subprocess

ROOT=pathlib.Path(__file__).resolve().parents[2]
assert ROOT.name=='contract-model-closure-20261016'
stage=os.environ.get('CONTRACT_CLOSURE_ENGINE_STAGE','frozen-engine');assert stage in['frozen-engine','signature-engine','symbol-engine']
OUT=ROOT/'acceptance/contract-analysis-model-closure'/stage;OUT.mkdir(parents=True,exist_ok=True)
BASE='sha256:6b2837d98f1623708b6917406b358aa8587439de118a5795abdd0ce5662b3e35'
assert subprocess.check_output(['docker','image','inspect','platform-contract-verification:20261015','--format','{{.Id}}']).decode().strip()==BASE
build=ROOT/'.work/closure/image-build';build.mkdir(parents=True,exist_ok=True)
for name in ['analyze.mjs','source.mjs','source-vyper.mjs','model.mjs','capabilities.mjs','replay.mjs','source-pc.mjs','opcode-witness.mjs']:
 shutil.copy2(ROOT/'analysis-engines'/name,build/name)
for name in ['ir','properties','verification','rules']:shutil.copytree(ROOT/'analysis-engines'/name,build/name,dirs_exist_ok=True)
shutil.copy2(ROOT/'analysis-engines/Dockerfile.model-closure',build/'Dockerfile')
tag='platform-contract-model-closure:20261016'+{'signature-engine':'-signatures','symbol-engine':'-symbols'}.get(stage,'')
with(OUT/'engine-build.log').open('w')as log:
 subprocess.run(['docker','build','--network','none','--pull=false','--progress=plain','-t',tag,str(build)],stdout=log,stderr=subprocess.STDOUT,check=True)
image=subprocess.check_output(['docker','image','inspect',tag,'--format','{{.Id}}']).decode().strip()
assert image.startswith('sha256:')and len(image)==71
docker=['docker','run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m']
source=ROOT/'analysis-engines'
files=sorted(p for p in source.rglob('*')if p.is_file()and p.suffix in ['.mjs','.py']and not {'node_modules','test','bin'}.intersection(p.relative_to(source).parts))
paths=[str(p.relative_to(source)).replace('\\','/')for p in files]
script="const fs=require('node:fs'),c=require('node:crypto');const names=JSON.parse(fs.readFileSync(0,'utf8'));process.stdout.write(JSON.stringify(Object.fromEntries(names.map(n=>[n,c.createHash('sha256').update(fs.readFileSync('/engine/'+n)).digest('hex')]))))"
actual=json.loads(subprocess.check_output([*docker,'--entrypoint','node',image,'-e',script],input=json.dumps(paths).encode()))
records=[]
for path,file in zip(paths,files):
 expected=hashlib.sha256(file.read_bytes()).hexdigest();assert actual[path]==expected,path
 records.append({'path':'analysis-engines/'+path,'image_path':'/engine/'+path,'sha256':expected})
cap=json.loads(subprocess.check_output([*docker,image],input=b'{"operation":"capabilities"}'))
item=cap['sections']['capabilities'][0];assert item['semantic_rules_version']=='funds-semantics-8'
assert item['source_return_analysis']['induction']['counter_polynomials_max_degree']==3
assert item['state_model_verification']['whole_authorization_proved']is False and item['state_model_verification']['whole_protocol_economics_proved']is False
if stage=='symbol-engine':assert item['state_model_verification']['builtin_identity']=='compiler_AST_declaration_identity_with_shadowed_names_preserved'
(OUT/'engine-image.txt').write_text(image+'\n')
(OUT/'capabilities.json').write_text(json.dumps(cap,indent=2))
(OUT/'engine-source-freeze.json').write_text(json.dumps({'image':image,'base_image':BASE,'rule_version':'funds-semantics-8','files':records},indent=2))
print('MODEL_CLOSURE_IMAGE_VERIFIED',image,'SOURCE_FILES',len(records),flush=True)
