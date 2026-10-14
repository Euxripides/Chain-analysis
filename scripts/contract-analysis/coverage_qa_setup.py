"""Run candidate services against the existing isolated contract QA database."""
import json,os,pathlib,shutil,subprocess,sys,urllib.parse,urllib.request,time
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-coverage-20261014'
sys.path.insert(0,str(ROOT/'scripts/contract-analysis'));from qa_support import dotenv
OUT=ROOT/'acceptance/contract-analysis-coverage';OUT.mkdir(parents=True,exist_ok=True)
NODE='/home/euripides/.nvm/versions/node/v24.20.0/bin/node'
env=os.environ.copy();env['PATH']=str(pathlib.Path(NODE).parent)+':/usr/local/go/bin:'+env['PATH']
old=pathlib.Path('/home/euripides/projects/blockchain-platform-worktrees/contract-analysis-20261014')
image=(OUT/'engine-image.txt').read_text().strip()
values=dotenv('/home/euripides/.config/blockchain-platform/contract-analysis-qa-20261014.env')
assert urllib.parse.urlparse(values['DATABASE_URL']).path=='/contract_analysis_qa_20261014'
values.update({'CONTRACT_ENGINE_IMAGE':image,'CONTRACT_ENGINE_PREVIOUS_IMAGES':'sha256:2b54082289b8ad8c487bacd86ed3227f0c3d4510243774439586f57f8805e37a','CONTRACT_PROGRAM_ANALYSIS_ENABLED':'0','API_HTTP_PORT':'18091','MCP_PORT':'18092','REST_API_BASE_URL':'http://127.0.0.1:18091/api/v1','CONTRACT_REST_API_BASE_URL':'http://127.0.0.1:18091/api/v1','CONTRACT_QA_API_PORT':'18091','CONTRACT_QA_CONSOLE_PORT':'18093'})
envfile=pathlib.Path('/home/euripides/.config/blockchain-platform/contract-coverage-qa-20261014.env');envfile.write_text('\n'.join(k+'='+v for k,v in values.items())+'\n');envfile.chmod(0o600)
for name,command,cwd in [('api-build',['go','build','-o','bin/contract-analysis-api','./cmd/api'],ROOT/'backend'),('worker-build',['go','build','-o','bin/contract-analysis-worker','./cmd/contract-worker'],ROOT/'backend'),('mcp-build',['npm','run','build'],ROOT/'mcp'),('mcp-unit',['npm','test'],ROOT/'mcp'),('frontend-typecheck',[NODE,'node_modules/typescript/bin/tsc','--noEmit','-p','apps/user-console/tsconfig.json'],ROOT/'frontend'),('frontend-build',['npm','run','build:user'],ROOT/'frontend')]:
 with (OUT/(name+'.log')).open('w') as log:p=subprocess.run(command,cwd=cwd,env=env,stdout=log,stderr=subprocess.STDOUT)
 print(name,p.returncode,flush=True)
 if p.returncode:print((OUT/(name+'.log')).read_text()[-7000:],flush=True);raise SystemExit(p.returncode)
# Vite aliases resolve packages against this candidate; reused node_modules only
# contains external dependencies and does not redirect @chain source packages.
for binary in ['anvil','cast','heimdall']:
 target=ROOT/'analysis-engines/bin'/binary;target.parent.mkdir(exist_ok=True);shutil.copy2(old/'analysis-engines/bin'/binary,target)
commands={'api':(ROOT/'backend',str(ROOT/'backend/bin/contract-analysis-api'),'768M'),'worker':(ROOT/'backend',str(ROOT/'backend/bin/contract-analysis-worker'),'768M'),'mcp':(ROOT/'mcp',NODE+' dist/server.js','512M'),'console':(ROOT/'frontend',NODE+' scripts/contract-analysis-qa-server.mjs','512M')}
units=[]
for name,(cwd,command,memory) in commands.items():
 unit='blockchain-contract-coverage-qa-'+name+'.service';units.append(unit)
 body='[Unit]\nDescription=EVM contract coverage isolated QA '+name+'\n[Service]\nWorkingDirectory='+str(cwd)+'\nEnvironmentFile=/home/euripides/projects/blockchain-platform/deploy/.env\nEnvironmentFile='+str(envfile)+'\nEnvironment=PATH='+env['PATH']+'\nExecStart='+command+'\nMemoryMax='+memory+'\nTasksMax=256\nRestart=no\n'
 (pathlib.Path('/home/euripides/.config/systemd/user')/unit).write_text(body)
subprocess.run(['systemctl','--user','daemon-reload'],check=True);subprocess.run(['systemctl','--user','restart',*units],check=True)
for url in ['http://127.0.0.1:18091/health/ready','http://127.0.0.1:18092/health','http://127.0.0.1:18093/app-v2/']:
 deadline=time.monotonic()+40
 while time.monotonic()<deadline:
  try:
   with urllib.request.urlopen(url,timeout=2) as r:assert r.status==200
   break
  except Exception:time.sleep(.5)
 else:raise RuntimeError('QA readiness timeout '+url)
with urllib.request.urlopen('http://127.0.0.1:18092/health') as response:health=json.load(response)
assert health['tools']==120,health
(OUT/'qa-services.json').write_text(json.dumps({'image':image,'database':'contract_analysis_qa_20261014','units':units,'ports':[18091,18092,18093],'tools':health['tools']},indent=2))
print('COVERAGE_ISOLATED_SERVICES_READY',health['tools'],flush=True)
