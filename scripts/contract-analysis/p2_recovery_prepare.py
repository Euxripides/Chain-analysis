"""Build a pinned recovery candidate and configure only isolated QA services."""
import datetime,hashlib,json,os,pathlib,shutil,subprocess,sys,time,urllib.parse,urllib.request
from qa_support import dotenv
ROOT=pathlib.Path(__file__).resolve().parents[2]
OUT=ROOT/'acceptance/contract-analysis-partial-recovery'
TARGET=pathlib.Path('/data/backups/codex-contract-general-20261017/partial-recovery')
assert ROOT.name=='contract-permit-nonces-20261016'
if not OUT.exists():
 TARGET.mkdir(parents=True,exist_ok=True);OUT.symlink_to(TARGET,target_is_directory=True)
assert OUT.is_symlink()and OUT.resolve()==TARGET.resolve()
ENV={**os.environ,'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+os.environ['PATH'],'GOMAXPROCS':'2'}
BASE='sha256:2c294c4501eab28cfb7947154b79dbca74cc0350167c4e664d6761cacca9b7ec'
SHA=lambda b:hashlib.sha256(b).hexdigest()
DOCKER=['docker','run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m']
PRODUCTION=['blockchain-api','blockchain-worker','blockchain-mcp','blockchain-user-console','blockchain-contract-api','blockchain-contract-worker','blockchain-contract-mcp','blockchain-contract-console']
DOCKER.extend(['--ulimit','core=0'])
def pids():return {u:subprocess.check_output(['systemctl','--user','show',u,'-p','MainPID','--value'],text=True).strip()for u in PRODUCTION}

if sys.argv[1]=='engine':
 assert subprocess.check_output(['docker','image','inspect','blockchain-contract-analysis:general-input-2c294c4501ea','--format','{{.Id}}'],text=True).strip()==BASE
 build=ROOT/'.work/p2/image-build';build.mkdir(parents=True,exist_ok=True)
 names=['model.mjs','analyze.mjs','source.mjs','bytecode.mjs','capabilities.mjs','conclusion-dependencies.mjs','section-checkpoint.mjs','bytecode-sevm.mjs','bytecode-sevm-worker.mjs','bytecode-sevm-protocol.mjs','runner-resource.py']
 for name in names:shutil.copy2(ROOT/'analysis-engines'/name,build/name)
 shutil.copy2(ROOT/'analysis-engines/Dockerfile.partial-recovery',build/'Dockerfile')
 log=OUT/('build-'+datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')+'.log')
 with log.open('w')as f:subprocess.run(['docker','build','--network','none','--pull=false','--progress=plain','-t','blockchain-contract-analysis:partial-recovery-development-20261017',str(build)],check=True,stdout=f,stderr=subprocess.STDOUT)
 image=subprocess.check_output(['docker','image','inspect','blockchain-contract-analysis:partial-recovery-development-20261017','--format','{{.Id}}'],text=True).strip()
 subprocess.run(['docker','tag',image,'blockchain-contract-analysis:partial-recovery-'+image[7:23]],check=True)
 freeze=OUT/'frozen-engine'/image[7:23];freeze.mkdir(parents=True,exist_ok=True)
 files=sorted(p for p in(ROOT/'analysis-engines').rglob('*')if p.is_file()and p.suffix in['.mjs','.py']and not {'node_modules','test','bin'}.intersection(p.relative_to(ROOT/'analysis-engines').parts))
 paths=[p.relative_to(ROOT/'analysis-engines').as_posix()for p in files]
 script="const fs=require('node:fs'),c=require('node:crypto');process.stdout.write(JSON.stringify(Object.fromEntries(JSON.parse(fs.readFileSync(0,'utf8')).map(n=>[n,c.createHash('sha256').update(fs.readFileSync('/engine/'+n)).digest('hex')]))))"
 actual=json.loads(subprocess.check_output([*DOCKER,'--entrypoint','node',image,'-e',script],input=json.dumps(paths).encode()))
 records=[]
 for path,p in zip(paths,files):
  assert actual[path]==SHA(p.read_bytes()),path
  records.append({'path':'analysis-engines/'+path,'sha256':actual[path]})
 cap=json.loads(subprocess.check_output([*DOCKER,image],input=b'{"operation":"capabilities"}'));c=cap['sections']['capabilities'][0]
 assert c['semantic_rules_version']=='funds-semantics-15'and c['execution_isolation']['sevm_heap_mib']==512
 assert c['state_model_verification']['whole_authorization_proved']is False and c['state_model_verification']['whole_protocol_economics_proved']is False
 document={'image':image,'base_image':BASE,'rule_version':'funds-semantics-15','files':records,'dockerfile_sha256':SHA((build/'Dockerfile').read_bytes())}
 (freeze/'engine-source-freeze.json').write_text(json.dumps(document,indent=2));(freeze/'capabilities.json').write_text(json.dumps(cap,indent=2));shutil.copy2(log,freeze/'engine-build.log')
 (OUT/'engine-image.txt').write_text(image+'\n');(OUT/'current-candidate.json').write_text(json.dumps({'image':image,'freeze_directory':str(freeze)},indent=2))
 print('P2_ENGINE_FROZEN',image,len(records),flush=True)
elif sys.argv[1]=='qa':
 image=(OUT/'engine-image.txt').read_text().strip();target=OUT/'service-validation'/image[7:23];target.mkdir(parents=True,exist_ok=True)
 before=pids();(target/'production-pids-before.json').write_text(json.dumps(before,indent=2))
 original=pathlib.Path('/home/euripides/.config/blockchain-platform/contract-general-input-qa-20261017.env');values=dotenv(original)
 assert urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']).path=='/contract_analysis_qa_20261014'
 values['CONTRACT_ENGINE_PREVIOUS_IMAGES']=','.join(dict.fromkeys([values['CONTRACT_ENGINE_IMAGE'],*values.get('CONTRACT_ENGINE_PREVIOUS_IMAGES','').split(',')]))
 values['CONTRACT_ENGINE_IMAGE']=image
 values['DRPC_ENABLED']='false';values['DRPC_PROBES_ENABLED']='false'
 values['SOLANA_ENABLED']='false';values['SOLANA_PROVIDER']='ankr';values['ANKR_MANAGEMENT_ENABLED']='false';values['RPC_PROBE_ON_START']='false'
 config=original.with_name('contract-partial-recovery-qa-20261017.env');config.write_text('\n'.join(k+'='+v for k,v in values.items())+'\n');config.chmod(0o600)
 bindir=OUT/'bin'/image[7:23];bindir.mkdir(parents=True,exist_ok=True)
 for name,command,cwd in [('api-build',['go','build','-o',str(bindir/'api'),'./cmd/api'],ROOT/'backend'),('worker-build',['go','build','-o',str(bindir/'worker'),'./cmd/contract-worker'],ROOT/'backend'),('mcp-build',['npm','run','build'],ROOT/'mcp')]:
  with(target/(name+'.log')).open('w')as f:subprocess.run(command,cwd=cwd,env=ENV,check=True,stdout=f,stderr=subprocess.STDOUT)
 for name in['api','worker','mcp']:
  d=pathlib.Path.home()/'.config/systemd/user'/('blockchain-contract-coverage-qa-'+name+'.service.d');d.mkdir(exist_ok=True)
  contents='[Service]\nEnvironmentFile='+str(config)+'\n'
  if name!='mcp':contents+='ExecStart=\nExecStart='+str(bindir/name)+'\n'
  (d/'30-partial-recovery.conf').write_text(contents)
 subprocess.run(['systemctl','--user','daemon-reload'],check=True)
 subprocess.run(['systemctl','--user','restart',*['blockchain-contract-coverage-qa-'+n+'.service'for n in['api','worker','mcp']]],check=True)
 for url in['http://127.0.0.1:18091/health/ready','http://127.0.0.1:18092/health']:
  for attempt in range(80):
   try:
    with urllib.request.urlopen(url,timeout=1)as response:assert response.status==200
    break
   except Exception:time.sleep(.25)
  else:raise RuntimeError('Isolated service not ready')
 assert pids()==before,'Production process identity changed'
 (target/'binary-identity.json').write_text(json.dumps({'image':image,'api_sha256':SHA((bindir/'api').read_bytes()),'worker_sha256':SHA((bindir/'worker').read_bytes()),'mcp_dist_sha256':SHA((ROOT/'mcp/dist/contract-analysis.js').read_bytes())},indent=2))
 print('P2_ISOLATED_QA_STARTED',image,flush=True)
else:raise SystemExit('Choose engine or qa')
