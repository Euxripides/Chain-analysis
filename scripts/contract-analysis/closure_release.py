"""Freeze and switch only the four authorized contract analysis services."""
import argparse,hashlib,json,os,pathlib,shutil,subprocess,sys,time,urllib.parse,urllib.request
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-model-closure-20261016'
sys.path.insert(0,str(ROOT/'scripts/contract-analysis'));from qa_support import dotenv
BASE=ROOT/'acceptance/contract-analysis-model-closure';OUT=BASE/'release';OUT.mkdir(parents=True,exist_ok=True)
PRIOR=ROOT.parent/'contract-verification-20261015';ORIGINAL=pathlib.Path('/home/euripides/projects/blockchain-platform');UNITDIR=pathlib.Path('/home/euripides/.config/systemd/user')
UNITS=['blockchain-contract-api.service','blockchain-contract-worker.service','blockchain-contract-mcp.service','blockchain-contract-console.service']
ENVFILE=pathlib.Path('/home/euripides/.config/blockchain-platform/contract-model-closure-release-20261016.env');OLDENV=pathlib.Path('/home/euripides/.config/blockchain-platform/contract-verification-release-20261015.env')
NGINX=pathlib.Path('/etc/nginx/sites-enabled/blockchain-platform.conf');PUBLIC_API=pathlib.Path('/var/www/html/contract-analysis/openapi.json')
def sha(path):return hashlib.sha256(path.read_bytes()).hexdigest()
def run(args):subprocess.run(args,check=True)
def sql(query):
 d=urllib.parse.urlparse(dotenv(ORIGINAL/'deploy/.env')['MIGRATION_DATABASE_URL']);assert d.path=='/blockchain_platform';env={**os.environ,'PGPASSWORD':urllib.parse.unquote(d.password)}
 return subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',d.username,'-d',d.path[1:]],input=query.encode(),env=env).decode().strip()
def original_pids():
 baseline=json.loads((BASE/'symbol-service-validation/production-pids-before.json').read_text());expected={unit:pid for unit,pid in baseline.items()if unit not in UNITS}
 current={unit:subprocess.check_output(['systemctl','--user','show',unit,'-p','MainPID','--value']).decode().strip()for unit in expected}
 assert current==expected and all(int(v)>0 for v in current.values()),'original business service PID drift';return current
def gates():
 manifest=json.loads((BASE/'candidate-freeze.json').read_text());assert manifest['status']=='PASS'and manifest['rule_version']=='funds-semantics-8'and manifest['whole_goal_complete']is False
 for file in manifest['sources']+manifest['built_files']+manifest['evidence_files']:assert sha(ROOT/file['path'])==file['sha256'],file['path']
 assert manifest['source_commit']==subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT).decode().strip()
 run(['git','-C',str(ROOT),'diff','--quiet','HEAD','--','analysis-engines','backend','mcp','frontend','scripts','docs','openapi']);original_pids();return manifest
def prepare():
 target=ROOT/'frontend/apps/user-console/dist/assets'
 for base in[PRIOR,ORIGINAL]:
  for file in(base/'frontend/apps/user-console/dist/assets').glob('*'):
   if file.is_file()and not(target/file.name).exists():shutil.copy2(file,target/file.name)
 run(['/usr/bin/python3',str(ROOT/'scripts/contract-analysis/closure_certify.py')]);candidate=gates()
 (BASE/'engine-image.txt').write_text(candidate['image']+'\n')
 previous=dotenv(OLDENV);assert previous['CONTRACT_ENGINE_IMAGE']=='sha256:6b2837d98f1623708b6917406b358aa8587439de118a5795abdd0ce5662b3e35'
 values={**previous,'CONTRACT_ENGINE_IMAGE':candidate['image'],'CONTRACT_ENGINE_PREVIOUS_IMAGES':','.join(dict.fromkeys([previous['CONTRACT_ENGINE_IMAGE'],*previous.get('CONTRACT_ENGINE_PREVIOUS_IMAGES','').split(',')])),'CONTRACT_PROGRAM_ANALYSIS_ENABLED':'0','CONTRACT_ANALYSIS_ENABLED':'1'}
 ENVFILE.write_text('\n'.join(k+'='+v for k,v in values.items())+'\n');ENVFILE.chmod(0o600)
 for unit in UNITS:
  raw=(UNITDIR/unit).read_text();assert str(PRIOR)in raw and str(OLDENV)in raw,unit
  (OUT/(unit+'.before')).write_text(raw);(OUT/unit).write_text(raw.replace(str(PRIOR),str(ROOT)).replace(str(OLDENV),str(ENVFILE)))
 (OUT/'openapi-before.json').write_bytes(PUBLIC_API.read_bytes())
 manifest={'status':'prepared','image':candidate['image'],'previous_image':previous['CONTRACT_ENGINE_IMAGE'],'source_commit':candidate['source_commit'],'candidate_freeze_sha256':sha(BASE/'candidate-freeze.json'),'files':candidate['built_files'],
  'environment_sha256':sha(ENVFILE),'previous_environment_sha256':sha(OLDENV),'nginx_sha256':sha(NGINX),'public_openapi_before_sha256':sha(OUT/'openapi-before.json'),'source_openapi_sha256':sha(ROOT/'openapi/contract-analysis-v1.json'),'unit_before_hashes':{unit:sha(OUT/(unit+'.before'))for unit in UNITS},'unit_after_hashes':{unit:sha(OUT/unit)for unit in UNITS},'original_pids':original_pids()}
 (OUT/'manifest.json').write_text(json.dumps(manifest,indent=2));print('MODEL_RELEASE_PREPARED',manifest['image'],flush=True)
def verify():
 manifest=json.loads((OUT/'manifest.json').read_text());assert sha(BASE/'candidate-freeze.json')==manifest['candidate_freeze_sha256']
 assert sha(ENVFILE)==manifest['environment_sha256']and sha(OLDENV)==manifest['previous_environment_sha256']and sha(NGINX)==manifest['nginx_sha256']
 assert sha(OUT/'openapi-before.json')==manifest['public_openapi_before_sha256']and sha(ROOT/'openapi/contract-analysis-v1.json')==manifest['source_openapi_sha256']
 assert gates()['image']==manifest['image']
 for unit in UNITS:assert sha(OUT/unit)==manifest['unit_after_hashes'][unit]and sha(OUT/(unit+'.before'))==manifest['unit_before_hashes'][unit]
 return manifest
def ready(url):
 end=time.monotonic()+40
 while time.monotonic()<end:
  try:
   with urllib.request.urlopen(url,timeout=2)as response:assert response.status==200
   return
  except Exception:time.sleep(.5)
 raise RuntimeError('candidate service readiness failed '+url)
def restore():
 for unit in UNITS:(UNITDIR/unit).write_bytes((OUT/(unit+'.before')).read_bytes())
 run(['systemctl','--user','daemon-reload']);run(['systemctl','--user','restart',*UNITS]);run(['sudo','-n','install','-m','0644',str(OUT/'openapi-before.json'),str(PUBLIC_API)])
 for url in['http://127.0.0.1:18084/health/ready','http://127.0.0.1:18085/health','http://127.0.0.1:18086/app-v2/']:ready(url)
 original_pids()
def deploy():
 manifest=verify()
 assert sha(PUBLIC_API)==manifest['public_openapi_before_sha256'],'public OpenAPI changed after release preparation'
 for unit in UNITS:assert sha(UNITDIR/unit)==manifest['unit_before_hashes'][unit],unit
 end=time.monotonic()+180
 while sql("SELECT count(*)FROM investigation_analysis_jobs WHERE version='contract-analysis-v1'AND status='running'")!='0':
  if time.monotonic()>end:raise RuntimeError('active contract tasks did not drain')
  time.sleep(1)
 run(['systemctl','--user','stop',*UNITS])
 try:
  for unit in UNITS:(UNITDIR/unit).write_bytes((OUT/unit).read_bytes())
  run(['systemctl','--user','daemon-reload']);run(['systemctl','--user','start',*UNITS])
  for url in['http://127.0.0.1:18084/health/ready','http://127.0.0.1:18085/health','http://127.0.0.1:18086/app-v2/']:ready(url)
  assert json.load(urllib.request.urlopen('http://127.0.0.1:18085/health'))['tools']==120
  run(['sudo','-n','install','-m','0644',str(ROOT/'openapi/contract-analysis-v1.json'),str(PUBLIC_API)]);original_pids()
  manifest.update({'status':'deployed','deployed_utc':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime())});(OUT/'manifest.json').write_text(json.dumps(manifest,indent=2));print('MODEL_RELEASE_DEPLOYED',manifest['image'],flush=True)
 except Exception:
  restore();manifest['status']='rolled_back_after_failed_switch';(OUT/'manifest.json').write_text(json.dumps(manifest,indent=2));raise
def rollback():
 manifest=verify()
 assert sha(PUBLIC_API)==manifest['source_openapi_sha256'],'public OpenAPI changed since this release'
 for unit in UNITS:assert sha(UNITDIR/unit)==manifest['unit_after_hashes'][unit],unit
 restore();manifest['status']='rolled_back';(OUT/'manifest.json').write_text(json.dumps(manifest,indent=2));print('MODEL_RELEASE_ROLLED_BACK_DATA_RETAINED',flush=True)
if __name__=='__main__':
 parser=argparse.ArgumentParser();parser.add_argument('operation',choices=['prepare','deploy','rollback']);globals()[parser.parse_args().operation]()
