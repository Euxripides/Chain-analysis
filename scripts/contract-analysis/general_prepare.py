"""Build/freeze a P1 engine and switch only the three isolated QA services."""
import datetime,hashlib,json,os,pathlib,shutil,subprocess,sys,urllib.parse,urllib.request,time
from qa_support import dotenv
ROOT=pathlib.Path(__file__).resolve().parents[2]
OUT=ROOT/'acceptance/contract-analysis-general';assert ROOT.name=='contract-permit-nonces-20261016'
ENV={**os.environ,'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+os.environ['PATH'],'GOMAXPROCS':'2'}
BASE='sha256:c29c874732d26ccb8499c8256e42d1cb57578ce226e2890ebe0b6845dc2f6948'
sha=lambda b:hashlib.sha256(b).hexdigest()
DOCKER=['docker','run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m']
PRODUCTION=['blockchain-api','blockchain-worker','blockchain-mcp','blockchain-user-console','blockchain-contract-api','blockchain-contract-worker','blockchain-contract-mcp','blockchain-contract-console']

def pids():return {u:subprocess.check_output(['systemctl','--user','show',u,'-p','MainPID','--value'],text=True).strip()for u in PRODUCTION}

if sys.argv[1]=='engine':
 assert subprocess.check_output(['docker','image','inspect','blockchain-contract-analysis:permit-nonces-c29c874732d2','--format','{{.Id}}'],text=True).strip()==BASE
 build=ROOT/'.work/general/image-build';(build/'verification').mkdir(parents=True,exist_ok=True);(build/'ir').mkdir(exist_ok=True)
 for name in ['model.mjs','analyze.mjs','source.mjs','bytecode.mjs','input.mjs','conclusion-dependencies.mjs','transport.mjs','cli.mjs','capabilities.mjs','verification/state.mjs','verification/assembly.mjs','verification/signatures.mjs','ir/dataflow.mjs']:
  shutil.copy2(ROOT/'analysis-engines'/name,build/name)
 shutil.copy2(ROOT/'analysis-engines/Dockerfile.general-input',build/'Dockerfile')
 log=build/('build-'+str(datetime.datetime.now().timestamp())+'.log')
 with log.open('w')as f:subprocess.run(['docker','build','--network','none','--pull=false','--progress=plain','-t','blockchain-contract-analysis:general-input-development-20261017',str(build)],check=True,stdout=f,stderr=subprocess.STDOUT)
 image=subprocess.check_output(['docker','image','inspect','blockchain-contract-analysis:general-input-development-20261017','--format','{{.Id}}'],text=True).strip()
 freeze=OUT/'frozen-engine'/image[7:23];freeze.mkdir(parents=True,exist_ok=True)
 files=sorted(p for p in (ROOT/'analysis-engines').rglob('*')if p.is_file()and p.suffix in ['.mjs','.py']and not {'node_modules','test','bin'}.intersection(p.relative_to(ROOT/'analysis-engines').parts))
 paths=[str(p.relative_to(ROOT/'analysis-engines'))for p in files]
 script="const fs=require('node:fs'),c=require('node:crypto');process.stdout.write(JSON.stringify(Object.fromEntries(JSON.parse(fs.readFileSync(0,'utf8')).map(n=>[n,c.createHash('sha256').update(fs.readFileSync('/engine/'+n)).digest('hex')]))))"
 actual=json.loads(subprocess.check_output([*DOCKER,'--entrypoint','node',image,'-e',script],input=json.dumps(paths).encode()))
 records=[]
 for path,p in zip(paths,files):
  assert actual[path]==sha(p.read_bytes()),path
  records.append({'path':'analysis-engines/'+path,'sha256':actual[path]})
 cap=json.loads(subprocess.check_output([*DOCKER,image],input=b'{"operation":"capabilities"}'));c=cap['sections']['capabilities'][0]
 assert c['semantic_rules_version']=='funds-semantics-14' and c['submitted_inputs']['code_only_provider_requests']==0
 assert c['state_model_verification']['whole_authorization_proved']is False and c['state_model_verification']['whole_protocol_economics_proved']is False
 document={'image':image,'base_image':BASE,'rule_version':'funds-semantics-14','files':records,'dockerfile_sha256':sha((build/'Dockerfile').read_bytes())}
 target=freeze/'engine-source-freeze.json'
 if target.exists():assert json.loads(target.read_text())==document
 else:target.write_text(json.dumps(document,indent=2))
 shutil.copyfile(log,freeze/'engine-build.log');(freeze/'capabilities.json').write_text(json.dumps(cap,indent=2))
 (OUT/'engine-image.txt').write_text(image+'\n');(OUT/'current-candidate.json').write_text(json.dumps({'image':image,'freeze_directory':str(freeze)},indent=2))
 subprocess.run(['docker','tag',image,'blockchain-contract-analysis:general-input-'+image[7:19]],check=True)
 print('GENERAL_ENGINE_FROZEN',image,len(records),flush=True)
elif sys.argv[1]=='qa':
 image=(OUT/'engine-image.txt').read_text().strip();target=OUT/'service-validation'/image[7:23];target.mkdir(parents=True,exist_ok=True)
 before=pids();(target/'production-pids-before.json').write_text(json.dumps(before,indent=2))
 original=pathlib.Path('/home/euripides/.config/blockchain-platform/contract-permit-nonces-qa-20261016.env');values=dotenv(original)
 assert urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']).path=='/contract_analysis_qa_20261014'
 values['CONTRACT_ENGINE_PREVIOUS_IMAGES']=','.join(dict.fromkeys([values['CONTRACT_ENGINE_IMAGE'],*values.get('CONTRACT_ENGINE_PREVIOUS_IMAGES','').split(',')]))
 values['CONTRACT_ENGINE_IMAGE']=image
 config=original.with_name('contract-general-input-qa-20261017.env');config.write_text('\n'.join(k+'='+v for k,v in values.items())+'\n');config.chmod(0o600)
 bindir=ROOT/'.work/general/bin'/image[7:23];bindir.mkdir(parents=True,exist_ok=True)
 for name,command,cwd in [('api-build',['go','build','-o',str(bindir/'api'),'./cmd/api'],ROOT/'backend'),('worker-build',['go','build','-o',str(bindir/'worker'),'./cmd/contract-worker'],ROOT/'backend'),('mcp-build',['npm','run','build'],ROOT/'mcp')]:
  with(target/(name+'.log')).open('w')as f:subprocess.run(command,cwd=cwd,env=ENV,check=True,stdout=f,stderr=subprocess.STDOUT)
 for name in ['api','worker','mcp']:
  d=pathlib.Path.home()/'.config/systemd/user'/('blockchain-contract-coverage-qa-'+name+'.service.d');d.mkdir(exist_ok=True)
  contents='[Service]\nEnvironmentFile='+str(config)+'\n'
  if name!='mcp':contents+='ExecStart=\nExecStart='+str(bindir/name)+'\n'
  (d/'20-general-input.conf').write_text(contents)
 subprocess.run(['systemctl','--user','daemon-reload'],check=True)
 subprocess.run(['systemctl','--user','restart',*['blockchain-contract-coverage-qa-'+n+'.service'for n in ['api','worker','mcp']]],check=True)
 for url in ['http://127.0.0.1:18091/health/ready','http://127.0.0.1:18092/health']:
  for attempt in range(80):
   try:
    with urllib.request.urlopen(url,timeout=1)as response:assert response.status==200
    break
   except Exception:time.sleep(.25)
  else:raise RuntimeError('Isolated service did not become ready: '+url)
 assert pids()==before,'Production process identity changed'
 (target/'binary-identity.json').write_text(json.dumps({'image':image,'api_sha256':sha((bindir/'api').read_bytes()),'worker_sha256':sha((bindir/'worker').read_bytes()),'mcp_source_sha256':sha((ROOT/'mcp/src/contract-analysis.ts').read_bytes()),'mcp_dist_sha256':sha((ROOT/'mcp/dist/contract-analysis.js').read_bytes())},indent=2))
 (OUT/'current-service-validation.txt').write_text(str(target)+'\n')
 print('GENERAL_ISOLATED_QA_STARTED',image,flush=True)
else:raise SystemExit('unknown preparation phase')
