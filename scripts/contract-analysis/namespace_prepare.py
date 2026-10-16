"""Build and byte-check the namespace engine over the accepted coupled-loop base."""
import hashlib,json,pathlib,shutil,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-namespaced-storage-20261016'
OUT=ROOT/'acceptance/contract-analysis-namespaced-storage/frozen-engine';OUT.mkdir(parents=True,exist_ok=True)
BASE='sha256:bc78e04257bf8c15e4b2449f64e6125b613dea22032abc28bd021f15eef2b740'
assert subprocess.check_output(['docker','image','inspect','platform-contract-coupled-invariants:20261016','--format','{{.Id}}'],text=True).strip()==BASE
build=ROOT/'.work/ns/image-build';build.mkdir(parents=True,exist_ok=True)
for name in['analyze.mjs','source.mjs','source-vyper.mjs','model.mjs','capabilities.mjs','replay.mjs','replay-system-state.mjs','replay-state-diff.mjs','source-pc.mjs','opcode-witness.mjs','namespace-layout.mjs']:shutil.copy2(ROOT/'analysis-engines'/name,build/name)
for name in['ir','properties','verification','rules']:shutil.copytree(ROOT/'analysis-engines'/name,build/name,dirs_exist_ok=True)
shutil.copy2(ROOT/'analysis-engines/Dockerfile.namespaced-storage',build/'Dockerfile')
with(OUT/'engine-build.log').open('w')as log:subprocess.run(['docker','build','--network','none','--pull=false','--progress=plain','-t','platform-contract-namespaced-storage:20261016',str(build)],stdout=log,stderr=subprocess.STDOUT,check=True)
image=subprocess.check_output(['docker','image','inspect','platform-contract-namespaced-storage:20261016','--format','{{.Id}}'],text=True).strip();assert image.startswith('sha256:')and len(image)==71
docker=['docker','run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m']
source=ROOT/'analysis-engines';files=sorted(p for p in source.rglob('*')if p.is_file()and p.suffix in['.mjs','.py']and not{'node_modules','test','bin'}.intersection(p.relative_to(source).parts));paths=[str(p.relative_to(source))for p in files]
script="const fs=require('node:fs'),c=require('node:crypto');const names=JSON.parse(fs.readFileSync(0,'utf8'));process.stdout.write(JSON.stringify(Object.fromEntries(names.map(n=>[n,c.createHash('sha256').update(fs.readFileSync('/engine/'+n)).digest('hex')]))))"
actual=json.loads(subprocess.check_output([*docker,'--entrypoint','node',image,'-e',script],input=json.dumps(paths).encode()));records=[]
for path,file in zip(paths,files):
 expected=hashlib.sha256(file.read_bytes()).hexdigest();assert actual[path]==expected,path;records.append({'path':'analysis-engines/'+path,'image_path':'/engine/'+path,'sha256':expected})
cap=json.loads(subprocess.check_output([*docker,image],input=b'{"operation":"capabilities"}'));item=cap['sections']['capabilities'][0];induction=item['source_return_analysis']['induction']
assert item['semantic_rules_version']=='funds-semantics-12'and induction['coupled_checked_locals']['max_accumulators']==4 and induction['coupled_unchecked_locals']is False
assert item['state_model_verification']['namespaced_storage']['actual_pointer_assignment_required']is True
assert item['state_model_verification']['whole_authorization_proved']is False and item['state_model_verification']['whole_protocol_economics_proved']is False
(OUT/'engine-image.txt').write_text(image+'\n');(OUT/'capabilities.json').write_text(json.dumps(cap,indent=2));(OUT/'engine-source-freeze.json').write_text(json.dumps({'image':image,'base_image':BASE,'rule_version':'funds-semantics-12','files':records},indent=2));print('NAMESPACE_IMAGE_BYTE_VERIFIED',image,len(records),flush=True)
