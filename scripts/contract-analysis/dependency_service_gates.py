"""Prepare and exercise isolated candidate services without switching production."""
import fcntl,hashlib,json,os,pathlib,shutil,subprocess,sys,time,urllib.parse,urllib.request
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-dependency-models-20261016'
sys.path.insert(0,str(ROOT/'scripts/contract-analysis'));from qa_support import dotenv
OUT=ROOT/'acceptance/contract-analysis-dependency-models/service-validation';OUT.mkdir(parents=True,exist_ok=True)
phase_lock=(OUT/'.service-gates.lock').open('a');fcntl.flock(phase_lock,fcntl.LOCK_EX)
prior=ROOT.parent/'contract-model-closure-20261016';prepared=ROOT/'acceptance/contract-analysis-dependency-models/frozen-engine';runtime=ROOT/'acceptance/contract-analysis-dependency-models/frozen-validation'
phase=sys.argv[1]if len(sys.argv)>1 else'prepare';assert phase in['prepare','refresh-backend','accept']
image=(prepared/'engine-image.txt').read_text().strip();(OUT/'engine-image.txt').write_text(image+'\n')
env=os.environ.copy();env.update({'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+env['PATH'],'GOMAXPROCS':'2','npm_config_verify_deps_before_run':'false',
 'CONTRACT_QA_OUT':str(OUT.relative_to(ROOT)),'CONTRACT_QA_ENV':'/home/euripides/.config/blockchain-platform/contract-dependency-models-qa-20261016.env',
 'CONTRACT_QA_API':'http://127.0.0.1:18091','CONTRACT_QA_MCP':'http://127.0.0.1:18092/mcp','CONTRACT_QA_CONSOLE':'http://127.0.0.1:18093/app-v2',
 'CONTRACT_QA_WORKER':'blockchain-contract-coverage-qa-worker.service','CONTRACT_QA_LIFECYCLE_MAX_RPC':'40','CONTRACT_EXPECTED_RULE_VERSION':'funds-semantics-9',
 'E2E_BROWSER_CHANNEL':'chromium','UC_CONTRACT_BASELINE':'1cfe42a4e060a5607fadacde5d259f210449ffd6'})
campaign=json.loads((prior/'acceptance/contract-analysis-model-closure/release/campaign-usage.json').read_text())
production_usage={'actual':{key:campaign['previous_production'][key]+campaign['new_production'][key]for key in['http_requests','credits','estimated_usd']},'basis':'all_owned_public_QA_jobs_carried_from_previous_release'}
assert production_usage['actual']['http_requests']==123
(OUT/'production-usage-baseline.json').write_text(json.dumps(production_usage,indent=2))
env['CONTRACT_QA_RESERVED_HTTP']=str(production_usage['actual']['http_requests']+160)
env['CONTRACT_QA_RESERVED_CREDITS']=str(production_usage['actual']['credits']+100000)
start=OUT/'live-usage-start.txt';previous_start=prior/'acceptance/contract-analysis-model-closure/symbol-service-validation/live-usage-start.txt'
if not start.exists():shutil.copyfile(previous_start,start)
assert start.read_text()==previous_start.read_text(),'cumulative provider budget must not restart'
for name in['verification-runtime','deepening-runtime','expression-runtime','mechanism-runtime','return-runtime','runtime-fixtures','gold','nonlinear-runtime','assembly-runtime','creation-runtime','signature-runtime','factory-runtime']:
 path=OUT/name
 if not path.exists():path.symlink_to(runtime/name,target_is_directory=True)
units=['blockchain-api.service','blockchain-worker.service','blockchain-mcp.service','blockchain-user-console.service','blockchain-contract-api.service','blockchain-contract-worker.service','blockchain-contract-mcp.service','blockchain-contract-console.service']
def services():return {unit:subprocess.check_output(['systemctl','--user','show',unit,'-p','MainPID','--value']).decode().strip()for unit in units}
baseline=OUT/'production-pids-before.json'
if not baseline.exists():baseline.write_text(json.dumps(services(),indent=2))
assert services()==json.loads(baseline.read_text()),'production services changed during isolated acceptance'
gate_file=OUT/'build-gates.json';gates=json.loads(gate_file.read_text())if gate_file.exists()else{}
def run(name,args,cwd=ROOT):
 started=time.monotonic()
 with(OUT/(name+'.log')).open('w')as log:result=subprocess.run(args,cwd=cwd,env=env,stdout=log,stderr=subprocess.STDOUT)
 gates[name]={'status':'PASS'if result.returncode==0 else'FAIL','exit_code':result.returncode,'wall_seconds':round(time.monotonic()-started,3),'image':image}
 gate_file.write_text(json.dumps(gates,indent=2));print('SERVICE_GATE',name,result.returncode,flush=True)
 if result.returncode:print((OUT/(name+'.log')).read_text()[-6500:],flush=True);raise SystemExit(result.returncode)

def backend_sources():
 files=[*ROOT.joinpath('backend').rglob('*.go'),ROOT/'backend/go.mod',ROOT/'backend/go.sum']
 return[{'path':str(p.relative_to(ROOT)),'sha256':hashlib.sha256(p.read_bytes()).hexdigest()}for p in sorted(files)]
def freeze_backend(before):
 assert before==backend_sources(),'backend sources changed during the build'
 binaries=[{'path':'backend/bin/'+n,'sha256':hashlib.sha256((ROOT/'backend/bin'/n).read_bytes()).hexdigest()}for n in['contract-analysis-api','contract-analysis-worker']]
 (OUT/'backend-build-freeze.json').write_text(json.dumps({'sources':before,'binaries':binaries,'image':image},indent=2))
def verify_backend():
 freeze=json.loads((OUT/'backend-build-freeze.json').read_text());assert freeze['sources']==backend_sources()and freeze['image']==image
 for item in freeze['binaries']:assert hashlib.sha256((ROOT/item['path']).read_bytes()).hexdigest()==item['sha256'],item['path']
def budget(reserved):
 values=dotenv(env['CONTRACT_QA_ENV']);dsn=urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']);assert dsn.path=='/contract_analysis_qa_20261014'
 sqlenv={**env,'PGPASSWORD':urllib.parse.unquote(dsn.password)}
 query="SELECT json_build_object('http_requests',coalesce(sum(http_requests),0),'credits',coalesce(sum(credits)FILTER(WHERE NOT cache_hit AND NOT singleflight_hit),0),'estimated_usd',coalesce(sum(estimated_cost_usd)FILTER(WHERE NOT cache_hit AND NOT singleflight_hit),0))FROM provider_usage WHERE usage_type='contract_analysis'AND request_time>='"+start.read_text().strip()+"'"
 current=json.loads(subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',dsn.username,'-d',dsn.path[1:]],input=query.encode(),env=sqlenv))
 assert current['http_requests']+reserved+int(env['CONTRACT_QA_RESERVED_HTTP'])<=2000 and current['credits']+100000+int(env['CONTRACT_QA_RESERVED_CREDITS'])<=1000000 and current['estimated_usd']+production_usage['actual']['estimated_usd']<10,current
 (OUT/'live-usage.json').write_text(json.dumps({'start_utc':start.read_text().strip(),'database':dsn.path[1:],'limits':{'http_requests':2000,'credits':1000000,'estimated_usd':10},'actual':current,'previous_production_actual':production_usage['actual'],'reserved_production_http_requests':160,'reserved_production_credits':100000},indent=2));return current
if phase=='prepare':
 before=backend_sources()
 run('qa-setup',['python3','scripts/contract-analysis/coverage_qa_setup.py'])
 freeze_backend(before)
 run('frontend-contract',['node','scripts/uc-contract-freeze-check.mjs'],ROOT/'frontend')
 run('frontend-typecheck-all',['pnpm','--config.verify-deps-before-run=false','run','typecheck'],ROOT/'frontend')
 run('frontend-build-admin',['npm','run','build:admin'],ROOT/'frontend')
 run('frontend-render-all',['node','scripts/uc-v4-render-check.mjs','--out',str(OUT/'frontend-render')],ROOT/'frontend')
 print('ISOLATED_SERVICES_AND_FRONTEND_GATES_READY',budget(0),flush=True)
elif phase=='refresh-backend':
 before=backend_sources()
 env['CONTRACT_FACTORY_BINDING_CORPUS']=str(runtime/'factory-runtime');env['CONTRACT_CREATION_BINDING_CORPUS']=str(runtime/'creation-runtime')
 run('backend-current-source-tests',['go','test','./internal/contractanalysis','./internal/api','-run','Contract','-count=1','-v'],ROOT/'backend')
 for name,target in[('api','api'),('worker','contract-worker')]:run('backend-refresh-'+name,['go','build','-o','bin/contract-analysis-'+name,'./cmd/'+target],ROOT/'backend')
 freeze_backend(before)
 subprocess.run(['systemctl','--user','restart','blockchain-contract-coverage-qa-api.service','blockchain-contract-coverage-qa-worker.service'],check=True)
 deadline=time.monotonic()+40
 while time.monotonic()<deadline:
  try:
   with urllib.request.urlopen('http://127.0.0.1:18091/health/ready',timeout=2)as response:assert response.status==200
   break
  except Exception:time.sleep(.5)
 else:raise RuntimeError('isolated backend refresh readiness timeout')
 verify_backend();assert services()==json.loads(baseline.read_text())
 (OUT/'backend-refresh-gates.json').write_text(json.dumps(gates,indent=2))
 print('ISOLATED_BACKEND_SOURCE_AND_BINARIES_REFRESHED',flush=True)
else:
 verify_backend()
 assert gates.get('backend-current-source-tests',{}).get('status')=='PASS'
 final=json.loads((runtime/'final-acceptance.json').read_text());assert final['status']=='PASS'and final['image']==image
 assert all(gates.get(name,{}).get('status')=='PASS'for name in['qa-setup','frontend-contract','frontend-typecheck-all','frontend-build-admin','frontend-render-all'])
 run('live-core',['python3','scripts/contract-analysis/coverage_suite.py','authority-live','live-api','mcp-live','execution-live','browser','openapi'])
 budget(100);run('verification-evidence',['python3','scripts/contract-analysis/verification_evidence_qa.py'])
 budget(100);run('dependencies',['python3','scripts/contract-analysis/mechanisms_dependency_qa.py'])
 for name,script in [('verification-browser','contract-analysis-verification-qa.mjs'),('authority-browser','contract-analysis-authority-qa.mjs'),('mechanism-browser','contract-analysis-mechanisms-qa.mjs')]:run(name,['node','frontend/scripts/'+script])
 run('live-resilience',['python3','scripts/contract-analysis/coverage_suite.py','lifecycle','resource-fault'])
 assert services()==json.loads(baseline.read_text()),'production service PID drift'
 print('ISOLATED_SERVICE_ACCEPTANCE_PASS_GOAL_REMAINS_ACTIVE',budget(0),flush=True)
