"""Bounded HTTP/UI/lifecycle acceptance for the candidate, never the production DB."""
import datetime,json,os,pathlib,subprocess,sys,urllib.parse
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-coverage-20261014'
sys.path.insert(0,str(ROOT/'scripts/contract-analysis'));from qa_support import dotenv
OUT=ROOT/'acceptance/contract-analysis-coverage';env=os.environ.copy();env['PATH']='/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+env['PATH']
env.update({'CONTRACT_QA_OUT':'acceptance/contract-analysis-coverage','CONTRACT_QA_API':'http://127.0.0.1:18091','CONTRACT_QA_MCP':'http://127.0.0.1:18092/mcp','CONTRACT_QA_CONSOLE':'http://127.0.0.1:18093/app-v2','CONTRACT_QA_ENV':'/home/euripides/.config/blockchain-platform/contract-coverage-qa-20261014.env','CONTRACT_QA_WORKER':'blockchain-contract-coverage-qa-worker.service'})
values=dotenv(env['CONTRACT_QA_ENV']);dsn=urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']);assert dsn.path=='/contract_analysis_qa_20261014'
sqlenv=env.copy();sqlenv['PGPASSWORD']=urllib.parse.unquote(dsn.password)
start=OUT/'live-usage-start.txt'
if not start.exists():start.write_text(datetime.datetime.now(datetime.timezone.utc).isoformat())
stamp=start.read_text().strip();datetime.datetime.fromisoformat(stamp)
def usage():
 query="SELECT json_build_object('http_requests',coalesce(sum(http_requests),0),'credits',coalesce(sum(credits) FILTER(WHERE NOT cache_hit AND NOT singleflight_hit),0),'estimated_usd',coalesce(sum(estimated_cost_usd) FILTER(WHERE NOT cache_hit AND NOT singleflight_hit),0)) FROM provider_usage WHERE usage_type='contract_analysis' AND request_time>='"+stamp+"'"
 raw=subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',dsn.username,'-d',dsn.path[1:]],input=query.encode(),env=sqlenv).decode().strip();data=json.loads(raw)
 (OUT/'live-usage.json').write_text(json.dumps({'start_utc':stamp,'database':dsn.path[1:],'limits':{'http_requests':2000,'credits':1000000,'estimated_usd':10},'actual':data},indent=2));return data
stages=[('live-api',['python3','scripts/contract-analysis/live_api_qa.py'],100,100000),('mcp-live',['node','mcp/scripts/contract-analysis-live-qa.mjs'],80,100000),('execution-live',['python3','scripts/contract-analysis/execution_live_qa.py'],120,100000),('browser',['node','frontend/scripts/contract-analysis-live-qa.mjs'],0,0),('lifecycle',['python3','scripts/contract-analysis/lifecycle_qa.py'],500,500000),('resource-fault',['python3','scripts/contract-analysis/resource_fault_qa.py'],0,0),('openapi',['node','mcp/scripts/contract-analysis-openapi.mjs'],0,0)]
selected=set(sys.argv[1:]);records=[]
for name,command,requests,credits in stages:
 if selected and name not in selected:continue
 u=usage();assert u['http_requests']+requests<=2000 and u['credits']+credits<=1000000 and u['estimated_usd']<10,'live acceptance budget exhausted'
 with (OUT/(name+'.log')).open('w') as log:result=subprocess.run(command,cwd=ROOT,env=env,stdout=log,stderr=subprocess.STDOUT)
 current=usage();records.append({'scenario':name,'status':'PASS' if result.returncode==0 else 'FAIL','exit_code':result.returncode,'usage_after':current});(OUT/('suite-'+('-'.join(sorted(selected)) if selected else 'all')+'.json')).write_text(json.dumps(records,indent=2));print('SUITE',name,result.returncode,current,flush=True)
 if result.returncode:print((OUT/(name+'.log')).read_text()[-7000:],flush=True);raise SystemExit(result.returncode)
 assert current['http_requests']<=2000 and current['credits']<=1000000 and current['estimated_usd']<=10
