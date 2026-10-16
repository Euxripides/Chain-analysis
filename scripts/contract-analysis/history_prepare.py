"""Build and byte-check the transaction-history engine over the accepted base."""
import hashlib,json,pathlib,shutil,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-transaction-history-20261016'
OUT=ROOT/'acceptance/contract-analysis-transaction-history/frozen-engine';OUT.mkdir(parents=True,exist_ok=True)
BASE='sha256:d8eba4d2f1fa7fdc3266ef8ea005bea5990c2de2be44769a17ebbd3a5dea33a7'
assert subprocess.check_output(['docker','image','inspect','platform-contract-dependency-models:20261016','--format','{{.Id}}'],text=True).strip()==BASE
build=ROOT/'.work/history/image-build';build.mkdir(parents=True,exist_ok=True)
for name in['analyze.mjs','source.mjs','source-vyper.mjs','model.mjs','capabilities.mjs','replay.mjs','replay-system-state.mjs','replay-state-diff.mjs','source-pc.mjs','opcode-witness.mjs']:shutil.copy2(ROOT/'analysis-engines'/name,build/name)
for name in['ir','properties','verification','rules']:shutil.copytree(ROOT/'analysis-engines'/name,build/name,dirs_exist_ok=True)
shutil.copy2(ROOT/'analysis-engines/Dockerfile.transaction-history',build/'Dockerfile')
with(OUT/'engine-build.log').open('w')as log:subprocess.run(['docker','build','--network','none','--pull=false','--progress=plain','-t','platform-contract-transaction-history:20261016',str(build)],stdout=log,stderr=subprocess.STDOUT,check=True)
image=subprocess.check_output(['docker','image','inspect','platform-contract-transaction-history:20261016','--format','{{.Id}}'],text=True).strip();assert image.startswith('sha256:')and len(image)==71
docker=['docker','run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m']
source=ROOT/'analysis-engines';files=sorted(p for p in source.rglob('*')if p.is_file()and p.suffix in['.mjs','.py']and not{'node_modules','test','bin'}.intersection(p.relative_to(source).parts));paths=[str(p.relative_to(source))for p in files]
script="const fs=require('node:fs'),c=require('node:crypto');const names=JSON.parse(fs.readFileSync(0,'utf8'));process.stdout.write(JSON.stringify(Object.fromEntries(names.map(n=>[n,c.createHash('sha256').update(fs.readFileSync('/engine/'+n)).digest('hex')]))))"
actual=json.loads(subprocess.check_output([*docker,'--entrypoint','node',image,'-e',script],input=json.dumps(paths).encode()));records=[]
for path,file in zip(paths,files):
 expected=hashlib.sha256(file.read_bytes()).hexdigest();assert actual[path]==expected,path;records.append({'path':'analysis-engines/'+path,'image_path':'/engine/'+path,'sha256':expected})
cap=json.loads(subprocess.check_output([*docker,image],input=b'{"operation":"capabilities"}'));item=cap['sections']['capabilities'][0]
assert item['semantic_rules_version']=='funds-semantics-10'and item['replay_environment_analysis']['max_auxiliary_storage_words']==96 and item['replay_environment_analysis']['whole_block_execution_verified']is False
assert item['state_model_verification']['whole_authorization_proved']is False and item['state_model_verification']['whole_protocol_economics_proved']is False
(OUT/'engine-image.txt').write_text(image+'\n');(OUT/'capabilities.json').write_text(json.dumps(cap,indent=2));(OUT/'engine-source-freeze.json').write_text(json.dumps({'image':image,'base_image':BASE,'rule_version':'funds-semantics-10','files':records},indent=2));print('HISTORY_IMAGE_BYTE_VERIFIED',image,len(records),flush=True)
