"""Close CALLCODE context against real EVM returns and rebuilt services."""
import datetime,json,os,pathlib,subprocess,time,urllib.parse,urllib.request
ROOT=pathlib.Path(__file__).resolve().parents[2];OUT=ROOT/'acceptance/contract-analysis-deepening';assert ROOT.name=='contract-deepening-20261015'
env=os.environ.copy();env.update({'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+env['PATH'],'GOMAXPROCS':'2','CONTRACT_QA_OUT':'acceptance/contract-analysis-deepening','CONTRACT_QA_ENV':'/home/euripides/.config/blockchain-platform/contract-deepening-qa-20261015.env','CONTRACT_QA_API':'http://127.0.0.1:18091','CONTRACT_QA_RUN_SUFFIX':'','CONTRACT_CALLCODE_RUNTIME':str(OUT/'deepening-runtime/callcode-context.json'),'CONTRACT_DEEPENING_RUNTIME':str(OUT/'deepening-runtime')})
deadline=time.monotonic()+900
while any(subprocess.run(['systemctl','--user','is-active','--quiet',u]).returncode==0 for u in ['contract-deepening-acceptance-20261015.service','contract-deepening-finish-20261015.service']):
 if time.monotonic()>deadline:raise RuntimeError('acceptance did not finish')
 time.sleep(3)
gates=json.loads((OUT/'build-gates.json').read_text())
repair=os.environ.get('CONTRACT_DEEPENING_REBUILD_FAILED_EVIDENCE')=='1'
assert all(g['status']=='PASS'or(repair and name=='deepening-evidence')for name,g in gates.items()),gates
if not repair:assert json.loads((OUT/'final-acceptance.json').read_text())['status']=='PASS'
def run(name,args,cwd=ROOT):
 with (OUT/(name+'.log')).open('w')as log:p=subprocess.run(args,cwd=cwd,env=env,stdout=log,stderr=subprocess.STDOUT)
 gates.update(json.loads((OUT/'build-gates.json').read_text()))
 gates[name]={'status':'PASS'if p.returncode==0 else'FAIL','exit_code':p.returncode};(OUT/'build-gates.json').write_text(json.dumps(gates,indent=2));print('CONTEXT',name,p.returncode,flush=True)
 if p.returncode:print((OUT/(name+'.log')).read_text()[-6000:],flush=True);raise RuntimeError(name)
run('call-context-runtime',['node','scripts/contract-analysis/deepening_context_qa.mjs'])
run('backend-context-unit',['go','test','./internal/contractanalysis','./internal/api','-count=1'],ROOT/'backend')
for name,target in [('api','api'),('worker','contract-worker')]:run(name+'-context-build',['go','build','-o','bin/contract-analysis-'+name,'./cmd/'+target],ROOT/'backend')
run('context-restart',['systemctl','--user','restart','blockchain-contract-coverage-qa-api.service','blockchain-contract-coverage-qa-worker.service'])
deadline=time.monotonic()+40
while True:
 try:
  with urllib.request.urlopen('http://127.0.0.1:18091/health/ready',timeout=2)as response:assert response.status==200
  break
 except Exception:
  if time.monotonic()>deadline:raise RuntimeError('context API not ready')
  time.sleep(.5)
def usage():
 from qa_support import dotenv
 record=json.loads((OUT/'live-usage.json').read_text());stamp=record['start_utc'];datetime.datetime.fromisoformat(stamp)
 dsn=urllib.parse.urlparse(dotenv(env['CONTRACT_QA_ENV'])['MIGRATION_DATABASE_URL']);assert dsn.path=='/contract_analysis_qa_20261014'
 sqlenv=env.copy();sqlenv['PGPASSWORD']=urllib.parse.unquote(dsn.password)
 query="SELECT json_build_object('http_requests',coalesce(sum(http_requests),0),'credits',coalesce(sum(credits) FILTER(WHERE NOT cache_hit AND NOT singleflight_hit),0),'estimated_usd',coalesce(sum(estimated_cost_usd) FILTER(WHERE NOT cache_hit AND NOT singleflight_hit),0)) FROM provider_usage WHERE usage_type='contract_analysis' AND request_time>='"+stamp+"'"
 record['actual']=json.loads(subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',dsn.username,'-d',dsn.path[1:]],input=query.encode(),env=sqlenv).decode())
 (OUT/'live-usage.json').write_text(json.dumps(record,indent=2));return record['actual']
current=usage();assert current['http_requests']+120<=2000 and current['credits']+100000<=1000000 and current['estimated_usd']<10
if repair:
 env['CONTRACT_QA_RUN_SUFFIX']='-scoped-proof'
 run('authority-proof-live',['python3','scripts/contract-analysis/semantics_authority_qa.py'])
 env['CONTRACT_QA_RUN_SUFFIX']=''
run('execution-context-live',['python3','scripts/contract-analysis/execution_live_qa.py']);current=usage();assert current['http_requests']<=2000 and current['credits']<=1000000 and current['estimated_usd']<=10
run('context-evidence',['python3','scripts/contract-analysis/deepening_evidence_qa.py'])
if repair:
 gates['deepening-evidence']=dict(gates['context-evidence']);(OUT/'build-gates.json').write_text(json.dumps(gates,indent=2))
 runtime=json.loads((OUT/'deepening-runtime/report.json').read_text());assert runtime['complete']and all(r['status']=='PASS'for r in runtime['checks'])
 (OUT/'final-acceptance.json').write_text(json.dumps({'status':'PASS','image':(OUT/'engine-image.txt').read_text().strip(),'runtime_checks':len(runtime['checks']),'all_paths_proved':False},indent=2))
run('engine-certify',['python3','scripts/contract-analysis/deepening_certify.py'])
print('DEEPENING_BACKEND_CONTEXT_PASS',flush=True)
