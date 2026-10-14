"""Compatible EVM coverage upgrade, retaining original services and old digests."""
import argparse,hashlib,json,os,pathlib,shutil,subprocess,sys,time,urllib.parse,urllib.request
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-semantics-20261014'
sys.path.insert(0,str(ROOT/'scripts/contract-analysis'));from qa_support import dotenv
OUT=ROOT/'acceptance/contract-analysis-semantics/release';OUT.mkdir(parents=True,exist_ok=True)
PRIOR=pathlib.Path('/home/euripides/projects/blockchain-platform-worktrees/contract-coverage-20261014')
ORIGINAL=pathlib.Path('/home/euripides/projects/blockchain-platform');UNITDIR=pathlib.Path('/home/euripides/.config/systemd/user')
UNITS=['blockchain-contract-api.service','blockchain-contract-worker.service','blockchain-contract-mcp.service','blockchain-contract-console.service']
NODE='/home/euripides/.nvm/versions/node/v24.20.0/bin/node'
ENVFILE=pathlib.Path('/home/euripides/.config/blockchain-platform/contract-semantics-release-20261014.env')
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def sql(text):
 d=urllib.parse.urlparse(dotenv(ORIGINAL/'deploy/.env')['MIGRATION_DATABASE_URL']);assert d.path=='/blockchain_platform';e=os.environ.copy();e['PGPASSWORD']=urllib.parse.unquote(d.password)
 return subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',d.username,'-d',d.path[1:]],input=text.encode(),env=e).decode().strip()
def run(args):subprocess.run(args,check=True)
def original_pids():
 base=json.loads((ROOT/'acceptance/contract-analysis-semantics/baseline.json').read_text())['services'];expected={k:v for k,v in base.items()if k not in UNITS}
 current={u:subprocess.check_output(['systemctl','--user','show',u,'-p','MainPID','--value']).decode().strip()for u in expected};assert current==expected,('original service drift',current);return current
def gates():
 base=ROOT/'acceptance/contract-analysis-semantics'
 for name in ['live-api-qa.json','mcp-live-qa.json','execution-live-qa.json','lifecycle-qa.json','resource-fault-qa.json','authority-live-qa.json','browser/report.json','authority-browser/report.json','runtime-fixtures/report.json']:
  rows=json.loads((base/name).read_text());assert rows and all(r['status']=='PASS'for r in rows),name
 gold=json.loads((base/'gold/report.json').read_text());assert gold['checks']and all(r['status']=='PASS'for r in gold['checks'])and len(gold['runs'])==29
 custom=json.loads((base/'return-runtime/report.json').read_text());assert len(custom['checks'])>=51 and all(r['status']=='PASS'for r in custom['checks']) and custom['image']==(base/'engine-image.txt').read_text().strip()
 usage=json.loads((base/'live-usage.json').read_text())['actual'];assert usage['http_requests']<2000 and usage['credits']<1000000 and usage['estimated_usd']<10
 assert gold['image']==(base/'engine-image.txt').read_text().strip()
 for name in ['engine-unit','go-test','qa-setup','frontend-contract','frontend-typecheck-all','frontend-render-all']:
  record=json.loads((base/'build-gates.json').read_text());assert record[name]['status']=='PASS',name
 original_pids()
def prepare():
 # Source, binaries and evidence become concrete before the production switch.
 image=(ROOT/'acceptance/contract-analysis-semantics/engine-image.txt').read_text().strip();old=(PRIOR/'acceptance/contract-analysis-coverage/engine-image.txt').read_text().strip()
 env=dotenv('/home/euripides/.config/blockchain-platform/contract-coverage-release-20261014.env');env.update({'CONTRACT_ENGINE_IMAGE':image,'CONTRACT_ENGINE_PREVIOUS_IMAGES':old+',sha256:2b54082289b8ad8c487bacd86ed3227f0c3d4510243774439586f57f8805e37a','CONTRACT_PROGRAM_ANALYSIS_ENABLED':'0','CONTRACT_ANALYSIS_ENABLED':'1'})
 ENVFILE.write_text('\n'.join(k+'='+v for k,v in env.items())+'\n');ENVFILE.chmod(0o600)
 for unit in UNITS:
  prior=(UNITDIR/unit).read_text();(OUT/(unit+'.before')).write_text(prior)
  updated=prior.replace(str(PRIOR),str(ROOT)).replace('/home/euripides/.config/blockchain-platform/contract-coverage-release-20261014.env',str(ENVFILE));assert updated!=prior
  (OUT/unit).write_text(updated)
 # Keep every prior content-addressed asset for existing browser sessions.
 target=ROOT/'frontend/apps/user-console/dist/assets'
 for base in [PRIOR,ORIGINAL]:
  for source in (base/'frontend/apps/user-console/dist/assets').glob('*'):
   if source.is_file()and not(target/source.name).exists():shutil.copy2(source,target/source.name)
 paths=[ROOT/'backend/bin/contract-analysis-api',ROOT/'backend/bin/contract-analysis-worker',ROOT/'openapi/contract-analysis-v1.json',*ROOT.joinpath('mcp/dist').rglob('*.js'),*ROOT.joinpath('frontend/apps/user-console/dist').rglob('*')]
 manifest={'status':'prepared','candidate':str(ROOT),'source_commit':subprocess.check_output(['git','-C',str(ROOT),'rev-parse','HEAD']).decode().strip(),'image':image,'previous_image':old,'original_pids':original_pids(),'files':[{'path':str(p.relative_to(ROOT)),'sha256':sha(p),'byte_size':p.stat().st_size}for p in paths if p.is_file()], 'nginx_sha256':sha(pathlib.Path('/etc/nginx/sites-enabled/blockchain-platform.conf'))}
 manifest['unit_before_hashes']={u:sha(OUT/(u+'.before'))for u in UNITS};(OUT/'openapi-before.json').write_bytes(pathlib.Path('/var/www/html/contract-analysis/openapi.json').read_bytes())
 (OUT/'manifest.json').write_text(json.dumps(manifest,indent=2));print('COVERAGE_RELEASE_PREPARED',len(manifest['files']),image,flush=True)
def verify():
 m=json.loads((OUT/'manifest.json').read_text())
 for f in m['files']:assert sha(ROOT/f['path'])==f['sha256'],f['path']
 assert sha(pathlib.Path('/etc/nginx/sites-enabled/blockchain-platform.conf'))==m['nginx_sha256'],'nginx drift';original_pids();return m
def ready(url):
 end=time.monotonic()+40
 while time.monotonic()<end:
  try:
   with urllib.request.urlopen(url,timeout=2)as response:assert response.status==200
   return
  except Exception:time.sleep(.5)
 raise RuntimeError('feature readiness failed '+url)
def restore():
 for u in UNITS:(UNITDIR/u).write_bytes((OUT/(u+'.before')).read_bytes())
 run(['systemctl','--user','daemon-reload']);run(['systemctl','--user','restart',*UNITS]);run(['sudo','-n','install','-m','0644',str(OUT/'openapi-before.json'),'/var/www/html/contract-analysis/openapi.json'])
def deploy():
 gates();m=verify()
 for u in UNITS:assert sha(UNITDIR/u)==m['unit_before_hashes'][u],u
 # Drain active work. Queued jobs preserve the explicitly allowed prior digest.
 end=time.monotonic()+180
 while sql("SELECT count(*) FROM investigation_analysis_jobs WHERE version='contract-analysis-v1' AND status='running'")!='0':
  if time.monotonic()>end:raise RuntimeError('active feature tasks did not drain')
  time.sleep(1)
 run(['systemctl','--user','stop',*UNITS])
 try:
  for u in UNITS:(UNITDIR/u).write_bytes((OUT/u).read_bytes())
  run(['systemctl','--user','daemon-reload']);run(['systemctl','--user','start',*UNITS])
  for url in ['http://127.0.0.1:18084/health/ready','http://127.0.0.1:18085/health','http://127.0.0.1:18086/app-v2/']:ready(url)
  assert json.load(urllib.request.urlopen('http://127.0.0.1:18085/health'))['tools']==120
  run(['sudo','-n','install','-m','0644',str(ROOT/'openapi/contract-analysis-v1.json'),'/var/www/html/contract-analysis/openapi.json']);original_pids()
  m['status']='deployed';m['deployed_utc']=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime());(OUT/'manifest.json').write_text(json.dumps(m,indent=2));print('COVERAGE_DEPLOYED',m['image'],flush=True)
 except Exception:restore();raise
def rollback():
 verify();restore();m=json.loads((OUT/'manifest.json').read_text());m['status']='rolled_back';(OUT/'manifest.json').write_text(json.dumps(m,indent=2));print('COVERAGE_ROLLED_BACK_DATA_RETAINED')
if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('operation',choices=['prepare','deploy','rollback']);a=p.parse_args();globals()[a.operation]()
