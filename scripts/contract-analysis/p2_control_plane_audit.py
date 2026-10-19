"""Disable inherited QA background management and account for historical syncs."""
import collections,datetime,hashlib,json,os,pathlib,subprocess,time,urllib.parse,urllib.request
from qa_support import dotenv
ROOT=pathlib.Path(__file__).resolve().parents[2];OUT=ROOT/'acceptance/contract-analysis-partial-recovery'
config=pathlib.Path('/home/euripides/.config/blockchain-platform/contract-partial-recovery-qa-20261017.env');values=dotenv(config)
db=urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']);assert db.path=='/contract_analysis_qa_20261014'
units=['blockchain-contract-coverage-qa-'+n+'.service'for n in['api','worker','mcp']]
production=['blockchain-api','blockchain-worker','blockchain-mcp','blockchain-user-console','blockchain-contract-api','blockchain-contract-worker','blockchain-contract-mcp','blockchain-contract-console']
def pids():return{u:subprocess.check_output(['systemctl','--user','show',u,'-p','MainPID','--value'],text=True).strip()for u in production}
def sql(query):return json.loads(subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',db.username,'-d',db.path[1:]],input=query.encode(),env={**os.environ,'PGPASSWORD':urllib.parse.unquote(db.password)}))
before=pids();assert sql("SELECT count(*) FROM investigation_analysis_jobs WHERE status='running';")==0
stamp=datetime.datetime.now(datetime.timezone.utc);cutoff=stamp.isoformat();initial=config.read_bytes()
already_disabled=all(values.get(k)=='false'for k in['DRPC_ENABLED','DRPC_PROBES_ENABLED','ANKR_MANAGEMENT_ENABLED','RPC_PROBE_ON_START','SOLANA_ENABLED'])and values.get('SOLANA_PROVIDER')=='ankr'
previous=json.loads((OUT/'background-management-audit.json').read_text())if(OUT/'background-management-audit.json').exists()else None
if not already_disabled:
 backup=config.with_name(config.stem+'-before-background-disable-'+stamp.strftime('%Y%m%dT%H%M%SZ')+'.env');backup.write_bytes(initial);backup.chmod(0o600)
for name in['DRPC_ENABLED','DRPC_PROBES_ENABLED','ANKR_MANAGEMENT_ENABLED','RPC_PROBE_ON_START']:values[name]='false'
values['SOLANA_ENABLED']='false';values['SOLANA_PROVIDER']='ankr'
if not already_disabled:
 config.write_text('\n'.join(k+'='+v for k,v in values.items())+'\n');config.chmod(0o600)
 subprocess.run(['systemctl','--user','restart',*units],check=True)
elif previous:cutoff=previous['disabled_at_utc']
for url in['http://127.0.0.1:18091/health/ready','http://127.0.0.1:18092/health']:
 for attempt in range(100):
  try:
   with urllib.request.urlopen(url,timeout=1)as response:assert response.status==200
   break
  except Exception:time.sleep(.25)
 else:raise RuntimeError('QA service not ready')
assert pids()==before
for unit in units[:2]:
 pid=subprocess.check_output(['systemctl','--user','show',unit,'-p','MainPID','--value'],text=True).strip()
 environment=dict(row.split(b'=',1)for row in pathlib.Path('/proc',pid,'environ').read_bytes().split(b'\0')if b'='in row)
 assert all(environment.get(name.encode())==b'false'for name in['DRPC_ENABLED','DRPC_PROBES_ENABLED','ANKR_MANAGEMENT_ENABLED','RPC_PROBE_ON_START'])
start='2026-10-14T05:33:05.139606+00:00';p2date='2026-10-17T02:42:03+00:00'
command=['journalctl','--user','--since',start,'-o','json','--no-pager']
for unit in[*units,'blockchain-contract-publication-race-*.service']:command.extend(['-u',unit])
events=[]
for line in subprocess.check_output(command,text=True).splitlines():
 row=json.loads(line);message=row.get('MESSAGE','')
 if 'drpc management sync'not in message:continue
 try:message=json.loads(message)
 except ValueError:continue
 if message.get('msg')not in['drpc management sync started','drpc management sync configured','drpc management sync failed','drpc management sync is disabled']:continue
 events.append({'timestamp_us':row['__REALTIME_TIMESTAMP'],'unit':row.get('_SYSTEMD_USER_UNIT'),'message':message})
rows=sql("SELECT coalesce(json_agg(t),'[]'::json)FROM(SELECT synced_at,source,sync_status,sync_error_class FROM provider_account_snapshots WHERE provider_key='drpc'AND synced_at>='"+start+"' ORDER BY synced_at)t;")
p2rows=[r for r in rows if datetime.datetime.fromisoformat(r['synced_at'])>=datetime.datetime.fromisoformat(p2date)]
def calls(records):return sum((2 if'statistics'in r['source']else 0)+(1 if'keys'in r['source']else 0)for r in records)
record={'status':'QA_BACKGROUND_DISABLED_HISTORICAL_HTTP_NOT_IN_PREVIOUS_LEDGER','disabled_at_utc':cutoff,'qa_database':'contract_analysis_qa_20261014',
 'qa_solana_enabled':False,'qa_solana_provider':'ankr','startup_configuration_recovery':'Inherited SOLANA_PROVIDER=drpc required matching QA-only disable and provider selection; initial failed startup retained in journal',
 'p2_start_utc':p2date,'original_start_utc':start,'effective_flags':{k:'false'for k in['DRPC_ENABLED','DRPC_PROBES_ENABLED','ANKR_MANAGEMENT_ENABLED','RPC_PROBE_ON_START']},
 'production_pids_unchanged':True,'private_environment_before_sha256':hashlib.sha256(initial).hexdigest(),'private_environment_after_sha256':hashlib.sha256(config.read_bytes()).hexdigest(),
 'management_snapshots':len(rows),'p2_management_snapshots':len(p2rows),'completed_sync_logical_api_calls':calls(rows),'p2_completed_sync_logical_api_calls':calls(p2rows),
 'management_http_response_lower_bound':sum(any(v in r['sync_error_class'].split(',')for v in['client_error','unauthorized','server_error'])for r in rows),
 'p2_management_http_response_lower_bound':sum(any(v in r['sync_error_class'].split(',')for v in['client_error','unauthorized','server_error'])for r in p2rows),
 'http_evidence':'durable syncs and configured sources; interrupted syncs and redirect hops not fully captured; logical calls are not exact wire HTTP totals',
 'management_credits_and_usd':'not recorded; no inferred zero cost','analysis_task_rpc':'separate provider_usage and job budget reconciliation',
 'budget_status':'PREVIOUS_1988_TOTAL_EXCLUDES_BACKGROUND_MANAGEMENT_DO_NOT_USE_AS_ALL_HTTP_TOTAL','events':events,'snapshots':rows}
if previous:
 record['private_environment_before_sha256']=previous['private_environment_before_sha256']
 (OUT/'background-management-audit-initial.json').write_text(json.dumps(previous,indent=2))if not(OUT/'background-management-audit-initial.json').exists()else None
(OUT/'background-management-audit.json').write_text(json.dumps(record,indent=2));print(json.dumps({k:v for k,v in record.items()if k not in['events','snapshots']},indent=2),flush=True)
