"""Create an offline scalar-value development candidate; existing QA stays frozen."""
import datetime,hashlib,json,os,pathlib,shutil,subprocess,sys

ROOT=pathlib.Path(__file__).resolve().parents[2]
OUT=ROOT/'acceptance/contract-analysis-heap-writes'
DATA=pathlib.Path('/data/backups/codex-contract-general-20261018/heap-field-writes');DATA.mkdir(parents=True,exist_ok=True)
if not OUT.exists():OUT.symlink_to(DATA,target_is_directory=True)
assert OUT.is_symlink()and OUT.resolve()==DATA.resolve()
BASE='sha256:8948dae2a22a36db68e9fb1c82447e9d87eb759b620f5eb7b1f2a0df06743d9d'
assert sys.argv[1]=='engine'
env={**os.environ,'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+os.environ['PATH']}
assert subprocess.check_output(['docker','image','inspect','blockchain-contract-analysis:aggregate-'+BASE[7:23],'--format','{{.Id}}'],text=True).strip()==BASE
files=sorted(p for p in(ROOT/'analysis-engines').rglob('*')if p.is_file()and p.suffix in['.mjs','.py']and not{'node_modules','test','bin'}.intersection(p.relative_to(ROOT/'analysis-engines').parts))
build=ROOT/'.work/p2-06/image-build';build.mkdir(parents=True,exist_ok=True)
lines=['FROM blockchain-contract-analysis:aggregate-'+BASE[7:23]+'@'+BASE]
paths=[]
for p in files:
    name=p.relative_to(ROOT/'analysis-engines').as_posix();paths.append(name);target=build/name;target.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(p,target)
    lines.append(('COPY --chmod=0755 'if name=='adapters/solc-shim.mjs'else'COPY ')+json.dumps([name,'/engine/'+name]))
(build/'Dockerfile').write_text('\n'.join(lines)+'\n')
log=OUT/('build-'+datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')+'.log')
with log.open('w')as f:subprocess.run(['docker','build','--network','none','--pull=false','--progress=plain','-t','blockchain-contract-analysis:heap-development-20261018',str(build)],env=env,stdout=f,stderr=subprocess.STDOUT,check=True)
image=subprocess.check_output(['docker','image','inspect','blockchain-contract-analysis:heap-development-20261018','--format','{{.Id}}'],text=True).strip()
subprocess.run(['docker','tag',image,'blockchain-contract-analysis:heap-'+image[7:23]],check=True)
directory=OUT/'frozen-engine'/image[7:23];directory.mkdir(parents=True,exist_ok=True)
docker=['docker','run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--ulimit','core=0','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m']
script="const fs=require('node:fs'),c=require('node:crypto');process.stdout.write(JSON.stringify(Object.fromEntries(JSON.parse(fs.readFileSync(0,'utf8')).map(n=>[n,{sha256:c.createHash('sha256').update(fs.readFileSync('/engine/'+n)).digest('hex'),unix_mode:fs.statSync('/engine/'+n).mode&511}]))))"
actual=json.loads(subprocess.check_output([*docker,'--entrypoint','node',image,'-e',script],input=json.dumps(paths).encode()))
records=[]
for name,p in zip(paths,files):
    assert actual[name]['sha256']==hashlib.sha256(p.read_bytes()).hexdigest();records.append({'path':'analysis-engines/'+name,**actual[name]})
assert actual['adapters/solc-shim.mjs']['unix_mode']&0o111
cap=json.loads(subprocess.check_output([*docker,image],input=b'{"operation":"capabilities"}'));c=cap['sections']['capabilities'][0]
assert c['semantic_rules_version']=='funds-semantics-20'and not c['storage_reference_analysis']['all_execution_paths_verified']
assert c['source_scalar_analysis']['version']=='source-scalar-v1'and not c['source_scalar_analysis']['all_execution_paths_verified']
assert c['source_aggregate_analysis']['version']=='source-aggregate-v2'and not c['source_aggregate_analysis']['all_execution_paths_verified']
assert len(records)==66
assert c['state_model_verification']['whole_authorization_proved']is False and c['state_model_verification']['whole_protocol_economics_proved']is False
(directory/'engine-source-freeze.json').write_text(json.dumps({'image':image,'base_image':BASE,'rule_version':'funds-semantics-20','files':records},indent=2))
(directory/'capabilities.json').write_text(json.dumps(cap,indent=2));shutil.copy2(log,directory/'engine-build.log');shutil.copy2(build/'Dockerfile',directory/'Dockerfile')
(OUT/'engine-image.txt').write_text(image+'\n');print('P2_HEAP_DEVELOPMENT_FROZEN',image,len(records),flush=True)
