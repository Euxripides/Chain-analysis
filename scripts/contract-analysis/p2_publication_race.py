"""Exercise real workers with a completed old result and a newer live fence."""
import datetime,hashlib,json,os,pathlib,re,subprocess,sys,time,urllib.parse,urllib.request
from qa_support import dotenv
ROOT=pathlib.Path(__file__).resolve().parents[2];OUT=ROOT/'acceptance/contract-analysis-partial-recovery'
image=(OUT/'engine-image.txt').read_text().strip();directory=OUT/('mcp-'+image[7:23])
assert json.loads((directory/'lifecycle-job.json').read_text())['status']=='completed','Finish normal candidate QA first'
stamp=datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ');out=OUT/'publication-race'/image[7:23]/stamp;out.mkdir(parents=True)
original=pathlib.Path('/home/euripides/.config/blockchain-platform/contract-partial-recovery-qa-20261017.env')
values=dotenv(original);assert values['CONTRACT_ENGINE_IMAGE']==image and urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']).path=='/contract_analysis_qa_20261014'
units=['blockchain-contract-coverage-qa-'+name+'.service'for name in['api','worker','mcp']]
production=['blockchain-api','blockchain-worker','blockchain-mcp','blockchain-user-console','blockchain-contract-api','blockchain-contract-worker','blockchain-contract-mcp','blockchain-contract-console']
def pids():return{u:subprocess.check_output(['systemctl','--user','show',u,'-p','MainPID','--value'],text=True).strip()for u in production}
before=pids();(out/'production-pids-before.json').write_text(json.dumps(before,indent=2))
(out/'delay.py').write_text("import os,time\ntime.sleep(3)\nos.execv('/opt/contract-python/bin/python',['python','/engine/runner-resource.py'])\n")
(out/'Dockerfile').write_text('FROM blockchain-contract-analysis:partial-recovery-development-20261017@'+image+'\nCOPY delay.py /qa-publication-delay.py\nENTRYPOINT ["/opt/contract-python/bin/python","/qa-publication-delay.py"]\n')
with(out/'build.log').open('w')as f:subprocess.run(['docker','build','--network','none','--pull=false','-t','contract-publication-race:20261017',str(out)],check=True,stdout=f,stderr=subprocess.STDOUT)
fault=subprocess.check_output(['docker','image','inspect','contract-publication-race:20261017','--format','{{.Id}}'],text=True).strip()
freeze=json.loads((OUT/'frozen-engine'/image[7:23]/'engine-source-freeze.json').read_text());paths=[r['path'].removeprefix('analysis-engines/')for r in freeze['files']]
script="const fs=require('node:fs'),c=require('node:crypto');process.stdout.write(JSON.stringify(Object.fromEntries(JSON.parse(fs.readFileSync(0,'utf8')).map(n=>[n,c.createHash('sha256').update(fs.readFileSync('/engine/'+n)).digest('hex')]))))"
actual=json.loads(subprocess.check_output(['docker','run','--rm','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--cpus','2','--memory','2g','--pids-limit','128','--ulimit','core=0','--user','65532:65532','--entrypoint','node',fault,'-e',script],input=json.dumps(paths).encode()))
assert all(actual[r['path'].removeprefix('analysis-engines/')]==r['sha256']for r in freeze['files'])
record={'image':fault,'base_image':image,'unchanged_candidate_files':len(freeze['files']),'fault':'three second entry delay only; all actual candidate engine files unchanged','output':str(out)}
(OUT/'publication-race-image.json').write_text(json.dumps(record,indent=2));(out/'image.json').write_text(json.dumps(record,indent=2))
changed={**values,'CONTRACT_ENGINE_IMAGE':fault,'CONTRACT_ENGINE_PREVIOUS_IMAGES':','.join(dict.fromkeys([image,*values.get('CONTRACT_ENGINE_PREVIOUS_IMAGES','').split(',')]))}
config=original.with_name('contract-partial-recovery-race-qa-20261017.env');config.write_text('\n'.join(k+'='+v for k,v in changed.items())+'\n');config.chmod(0o600)
dropins=[];status='FAIL';replica_path=None
def restart():
 subprocess.run(['systemctl','--user','daemon-reload'],check=True);subprocess.run(['systemctl','--user','restart',*units],check=True)
 for url in['http://127.0.0.1:18091/health/ready','http://127.0.0.1:18092/health']:
  for attempt in range(100):
   try:
    with urllib.request.urlopen(url,timeout=1)as response:assert response.status==200
    break
   except Exception:time.sleep(.25)
  else:raise RuntimeError('QA service not ready')
try:
 for unit in units:
  path=pathlib.Path.home()/'.config/systemd/user'/(unit+'.d')/'40-publication-race.conf';assert not path.exists();path.write_text('[Service]\nEnvironmentFile='+str(config)+'\n');dropins.append(path)
 environment_files=[pathlib.Path('/home/euripides/projects/blockchain-platform/deploy/.env'),
  original.with_name('contract-permit-nonces-qa-20261016.env'),original.with_name('contract-general-input-qa-20261017.env'),original,config]
 merged={}
 for path in environment_files:
  assert path.is_file();merged.update(dotenv(path))
 for name in['DATABASE_URL','MIGRATION_DATABASE_URL']:
  assert urllib.parse.urlparse(merged.get(name,'')).path=='/contract_analysis_qa_20261014','Replica requires isolated QA database'
 replica='blockchain-contract-publication-race-'+str(time.time_ns())+'.service'
 replica_path=pathlib.Path.home()/'.config/systemd/user'/replica;assert not replica_path.exists()
 binary=OUT/'bin'/image[7:23]/'worker';assert binary.is_file()
 replica_path.write_text('[Unit]\nDescription=Contract analysis isolated publication race replica\n[Service]\nType=simple\n'+''.join('EnvironmentFile='+str(p)+'\n'for p in environment_files)+
  'Environment=GOMAXPROCS=2\nWorkingDirectory='+str(ROOT)+'\nExecStart='+str(binary)+'\nMemoryMax=805306368\nCPUQuota=200%\nRestart=no\n')
 replica_path.chmod(0o600)
 restart()
 env={**os.environ,'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:'+os.environ['PATH'],'RECOVERY_RACE_IMAGE':fault,'RECOVERY_RACE_UNIT':replica}
 with(out/'actual-mcp.log').open('w')as f:r=subprocess.run(['node','mcp/scripts/contract-analysis-recovery-qa.mjs','race'],cwd=ROOT,env=env,stdout=f,stderr=subprocess.STDOUT,timeout=300)
 if r.returncode:print((out/'actual-mcp.log').read_text()[-5000:],flush=True);raise RuntimeError('Actual publication race failed')
 status='PASS'
finally:
 marker=directory/'publication-race-unit.json'
 if marker.exists():
  owner=json.loads(marker.read_text())
  if owner['image']==fault:
   assert re.fullmatch(r'blockchain-contract-publication-race-[0-9]+\.service',owner['unit'])
   for unit in[owner['main'],owner['unit']]:subprocess.run(['systemctl','--user','kill','--kill-whom=main','--signal=SIGCONT',unit],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
   subprocess.run(['systemctl','--user','stop',owner['unit']],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 if replica_path is not None:
  assert replica_path.parent==pathlib.Path.home()/'.config/systemd/user'and re.fullmatch(r'blockchain-contract-publication-race-[0-9]+\.service',replica_path.name)
  if replica_path.exists():replica_path.unlink()
 for path in dropins:
  assert path.name=='40-publication-race.conf'and path.parent.parent==pathlib.Path.home()/'.config/systemd/user'
  path.unlink()
 restart();assert pids()==before,'Production process identity changed'
 (out/'result.json').write_text(json.dumps({**record,'status':status,'restored_candidate':image,'production_pids_unchanged':True,'provider_http':0,'log_sha256':hashlib.sha256((out/'actual-mcp.log').read_bytes()).hexdigest()if(out/'actual-mcp.log').exists()else None},indent=2))
print('P2_COMPLETED_RESULT_PUBLICATION_RACE',status,fault,'RESTORED',image,flush=True)
