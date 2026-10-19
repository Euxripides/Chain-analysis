"""Exercise the nonce candidate through isolated HTTP/MCP without resetting campaign budgets."""
import hashlib,json,os,pathlib,shutil,subprocess,sys,time,urllib.parse,urllib.request
from decimal import Decimal
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-permit-nonces-20261016'
sys.path.insert(0,str(ROOT/'scripts/contract-analysis'));from qa_support import dotenv
OUT=ROOT/'acceptance/contract-analysis-permit-nonces/service-validation';OUT.mkdir(parents=True,exist_ok=True)
BASE=OUT.parent;prior=ROOT.parent/'contract-namespaced-storage-20261016'
image=(BASE/'frozen-engine/engine-image.txt').read_text().strip();(OUT/'engine-image.txt').write_text(image+'\n')
env={**os.environ,'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+os.environ['PATH'],'GOMAXPROCS':'2','npm_config_verify_deps_before_run':'false',
 'CONTRACT_QA_OUT':str(OUT.relative_to(ROOT)),'CONTRACT_QA_ENV':'/home/euripides/.config/blockchain-platform/contract-permit-nonces-qa-20261016.env',
 'CONTRACT_QA_API':'http://127.0.0.1:18091','CONTRACT_QA_MCP':'http://127.0.0.1:18092/mcp','CONTRACT_QA_CONSOLE':'http://127.0.0.1:18093/app-v2',
 'CONTRACT_EXPECTED_RULE_VERSION':'funds-semantics-13','E2E_BROWSER_CHANNEL':'chromium','UC_CONTRACT_BASELINE':'1cfe42a4e060a5607fadacde5d259f210449ffd6'}
summary=json.loads((prior/'acceptance/contract-analysis-namespaced-storage/publication-summary.json').read_text());production=summary['usage']['previous_production'];assert production['http_requests']==123
start=OUT/'live-usage-start.txt';prior_start=prior/'acceptance/contract-analysis-namespaced-storage/service-validation/live-usage-start.txt'
if not start.exists():shutil.copyfile(prior_start,start)
assert start.read_text()==prior_start.read_text()
units=['blockchain-api.service','blockchain-worker.service','blockchain-mcp.service','blockchain-user-console.service','blockchain-contract-api.service','blockchain-contract-worker.service','blockchain-contract-mcp.service','blockchain-contract-console.service']
def services():return{unit:subprocess.check_output(['systemctl','--user','show',unit,'-p','MainPID','--value'],text=True).strip()for unit in units}
before=OUT/'production-pids-before.json'
if not before.exists():before.write_text(json.dumps(services(),indent=2))
assert services()==json.loads(before.read_text())
gates=json.loads((OUT/'gates.json').read_text())if(OUT/'gates.json').exists()else{}
def run(name,args,cwd=ROOT):
 log=OUT/(name+'.log')
 if log.exists():log.rename(OUT/(str(time.time_ns())+'-'+log.name))
 started=time.monotonic()
 with log.open('w')as handle:p=subprocess.run(args,cwd=cwd,env=env,stdout=handle,stderr=subprocess.STDOUT,timeout=1200)
 gates[name]={'status':'PASS'if p.returncode==0 else'FAIL','exit_code':p.returncode,'image':image,'wall_seconds':time.monotonic()-started};(OUT/'gates.json').write_text(json.dumps(gates,indent=2));print('PERMIT_SERVICE_GATE',name,p.returncode,flush=True)
 if p.returncode:print(log.read_text()[-7000:],flush=True);raise SystemExit(p.returncode)
def budget(reserve=0):
 values=dotenv(env['CONTRACT_QA_ENV']if pathlib.Path(env['CONTRACT_QA_ENV']).exists()else'/home/euripides/.config/blockchain-platform/contract-namespaced-storage-qa-20261016.env')
 dsn=urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']);assert dsn.path=='/contract_analysis_qa_20261014'
 query="SELECT json_build_object('http_requests',coalesce(sum(http_requests),0),'credits',coalesce(sum(credits)FILTER(WHERE NOT cache_hit AND NOT singleflight_hit),0),'estimated_usd',coalesce(sum(estimated_cost_usd)FILTER(WHERE NOT cache_hit AND NOT singleflight_hit),0))FROM provider_usage WHERE usage_type='contract_analysis'AND request_time>='"+start.read_text().strip()+"'"
 args=['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',dsn.username,'-d',dsn.path[1:]]
 raw_usage=subprocess.check_output(args,input=query.encode(),env={**env,'PGPASSWORD':urllib.parse.unquote(dsn.password)})
 (OUT/'provider-usage-source.json').write_bytes(raw_usage)
 qa=json.loads(raw_usage);total={k:qa[k]+production[k]for k in qa}
 total['estimated_usd']=float(Decimal(str(qa['estimated_usd']))+Decimal(str(production['estimated_usd'])))
 assert total['http_requests']+reserve<=2000 and total['credits']+100000<=1000000 and total['estimated_usd']<10,total
 (OUT/'live-usage.json').write_text(json.dumps({'start_utc':start.read_text().strip(),'actual_isolated':qa,'carried_production':production,'actual':total,'limits':{'http_requests':2000,'credits':1000000,'estimated_usd':10}},indent=2));return total
phase=sys.argv[1]if len(sys.argv)>1 else'prepare';assert phase in ['prepare','accept','refresh','accept-incremental']
if phase=='prepare':
 budget();run('qa-setup',['python3','scripts/contract-analysis/coverage_qa_setup.py'])
 run('frontend-contract',['node','scripts/uc-contract-freeze-check.mjs'],ROOT/'frontend')
 run('frontend-typecheck-all',['pnpm','--config.verify-deps-before-run=false','run','typecheck'],ROOT/'frontend')
 run('frontend-render-all',['node','scripts/uc-v4-render-check.mjs','--out',str(OUT/'frontend-render')],ROOT/'frontend')
 print('PERMIT_SERVICES_PREPARED',budget(),flush=True)
elif phase=='refresh':
 basis=json.loads((BASE/'interface-fix-basis.json').read_text());previous=pathlib.Path(basis['prior_directory'])/'service-validation';assert previous.resolve().is_relative_to(BASE.resolve())
 inherited=json.loads((previous/'gates.json').read_text())
 required=['qa-setup','frontend-contract','frontend-typecheck-all','frontend-render-all','execution-live','authority-live','live-api','mcp-live']
 assert all(inherited[k]['status']=='PASS'for k in required)
 # The getter fix changes only the frozen engine. Platform production sources
 # and the existing binaries are unchanged; retain their actual check provenance.
 subprocess.run(['git','diff','--quiet','HEAD','--','backend','frontend','mcp/src'],cwd=ROOT,check=True)
 files=subprocess.check_output(['git','ls-files','-z','--','backend','frontend','mcp/src'],cwd=ROOT).decode().split('\0')
 platform=[{'path':n,'sha256':hashlib.sha256((ROOT/n).read_bytes()).hexdigest()}for n in sorted(files)if n and(ROOT/n).is_file()and not {'acceptance-evidence','node_modules','dist','bin'}.intersection(pathlib.PurePosixPath(n).parts)]
 (OUT/'platform-regression-basis.json').write_text(json.dumps({'previous_image':basis['prior_image'],'previous_directory':str(previous),'checks':{k:inherited[k]for k in required},'unchanged_runtime_source_base':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),'sources':platform},indent=2))
 for name in ['frontend-contract','frontend-typecheck-all','frontend-render-all']:
  gates[name]={**inherited[name],'reuse_basis':'unchanged_platform_sources','evidence_directory':str(previous)}
 values=dotenv(env['CONTRACT_QA_ENV']);assert urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']).path=='/contract_analysis_qa_20261014'
 values['CONTRACT_ENGINE_IMAGE']=image;values['CONTRACT_ENGINE_PREVIOUS_IMAGES']=','.join(dict.fromkeys([basis['prior_image'],*values.get('CONTRACT_ENGINE_PREVIOUS_IMAGES','').split(',')]))
 pathlib.Path(env['CONTRACT_QA_ENV']).write_text('\n'.join(k+'='+v for k,v in values.items())+'\n');pathlib.Path(env['CONTRACT_QA_ENV']).chmod(0o600)
 subprocess.run(['systemctl','--user','restart','blockchain-contract-coverage-qa-api.service','blockchain-contract-coverage-qa-worker.service'],check=True)
 for _ in range(80):
  try:
   with urllib.request.urlopen('http://127.0.0.1:18091/health/ready',timeout=2)as r:assert r.status==200
   break
  except Exception:time.sleep(.25)
 else:raise RuntimeError('candidate API refresh timed out')
 shutil.copyfile(previous/'mcp-analysis-id.txt',OUT/'mcp-analysis-id.txt')
 for name in ['execution-live-qa.json','authority-live-qa.json','live-api-qa.json','mcp-live-qa.json']:
  (OUT/'inherited').mkdir(exist_ok=True);shutil.copyfile(previous/name,OUT/'inherited'/name)
 gates['qa-setup']={'status':'PASS','image':image,'scope':'runtime_image_refresh_with_unchanged_platform_binaries','previous_directory':str(previous)}
 (OUT/'gates.json').write_text(json.dumps(gates,indent=2));assert services()==json.loads(before.read_text());print('PERMIT_RUNTIME_REFRESHED',image,budget(),flush=True)
else:
 final=json.loads((BASE/'frozen-validation/final-acceptance.json').read_text());assert final['status']=='PASS'and final['image']==image
 assert all(gates.get(k,{}).get('status')=='PASS'for k in ['qa-setup','frontend-contract','frontend-typecheck-all','frontend-render-all'])
 env.update({'CONTRACT_NAMESPACE_RUNTIME':str(BASE/'frozen-validation/namespace-runtime'),'CONTRACT_PERMIT_RUNTIME':str(BASE/'frozen-validation/permit-runtime')})
 run('backend-contract-regressions',['go','test','./internal/contractanalysis','./internal/api','-run','Contract','-count=1','-v'],ROOT/'backend')
 # Larger declared job ceilings run first, preserving the existing aggregate cap.
 if phase=='accept':
  for name,script,reserve in [('execution-live','execution_live_qa.py',120),('authority-live','semantics_authority_qa.py',100),('live-api','live_api_qa.py',100)]:
   budget(reserve);run(name,['python3','scripts/contract-analysis/'+script]);budget()
  budget(80);run('mcp-live',['node','mcp/scripts/contract-analysis-live-qa.mjs']);budget()
 else:
  basis=json.loads((OUT/'platform-regression-basis.json').read_text())
  for item in basis['sources']:assert hashlib.sha256((ROOT/item['path']).read_bytes()).hexdigest()==item['sha256'],item['path']
 budget(40);run('permit-mcp',['node','mcp/scripts/contract-analysis-permit-qa.mjs']);budget()
 run('openapi',['node','mcp/scripts/contract-analysis-openapi.mjs'])
 assert services()==json.loads(before.read_text()),'production services changed'
 (OUT/'acceptance.json').write_text(json.dumps({'status':'PASS','scope':'isolated_services_and_nonce_result_readability','image':image,'gates':gates,'usage':budget(),'whole_goal_complete':False},indent=2))
 print('PERMIT_ISOLATED_SERVICES_PASS',flush=True)
