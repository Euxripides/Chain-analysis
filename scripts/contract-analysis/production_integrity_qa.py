import hashlib,io,json,os,pathlib,subprocess,urllib.parse,uuid,zipfile
from qa_support import ROOT,Client,dotenv
from live_api_qa import artifact_bytes
OUT=ROOT/'acceptance/contract-analysis/release';rows=[]
def check(name,ok,evidence=None):
 rows.append({'scenario':name,'status':'PASS' if ok else 'FAIL','evidence':evidence});(OUT/'production-integrity-qa.json').write_text(json.dumps(rows,indent=2));print(name,rows[-1]['status'],flush=True)
 if not ok:raise AssertionError(name)
env={**os.environ,**dotenv('/home/euripides/projects/blockchain-platform/deploy/.env')};dsn=urllib.parse.urlparse(env['MIGRATION_DATABASE_URL']);assert dsn.path=='/blockchain_platform';env['PGPASSWORD']=urllib.parse.unquote(dsn.password)
def sql(query):return subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',dsn.username,'-d',dsn.path[1:]],input=query.encode(),env=env).decode().strip()
aid=(OUT/'production-analysis-id.txt').read_text().strip();uuid.UUID(aid);c=Client(env['GPT_ACTIONS_API_KEY']);status=c.must('/api/v1/analysis/contracts/get_contract_analysis',{'analysis_id':aid})
db=json.loads(sql("SELECT json_build_object('status',status,'version',version,'result_sha',result_sha,'lease_owner',lease_owner,'lease_until',lease_until,'fence',fence,'attempts',attempts,'rows',(SELECT count(*) FROM investigation_analysis_rows WHERE analysis_id=j.id),'artifacts',(SELECT count(*) FROM contract_analysis_artifacts WHERE analysis_id=j.id),'snapshots',(SELECT count(*) FROM contract_analysis_snapshots WHERE analysis_id=j.id)) FROM investigation_analysis_jobs j WHERE id='"+aid+"'"))
check('production-database-publication-is-complete',db['status']=='completed' and db['version']=='contract-analysis-v1' and db['result_sha']==status['result_sha256'] and db['lease_owner'] is None and db['snapshots']==1,db)
counts=json.loads(sql("SELECT json_object_agg(section,n) FROM (SELECT section,count(*) n FROM investigation_analysis_rows WHERE analysis_id='"+aid+"' GROUP BY section) x"))
check('production-section-counts-match-durable-rows',all(counts.get(section,0)==count for section,count in status['metadata']['sections'].items()),counts)
usage=json.loads(sql("SELECT json_build_object('http_requests',coalesce(sum(http_requests),0),'credits',coalesce(sum(credits) FILTER(WHERE NOT cache_hit AND NOT singleflight_hit),0),'estimated_usd',coalesce(sum(estimated_cost_usd) FILTER(WHERE NOT cache_hit AND NOT singleflight_hit),0)::numeric) FROM provider_usage WHERE job_id='"+aid+"' AND usage_type='contract_analysis'"))
m=status['metadata']['metrics'];check('production-cost-matches-provider-usage',usage['http_requests']==m['rpc_http_requests'] and usage['credits']==m['credits'] and abs(usage['estimated_usd']-m['estimated_usd'])<1e-12,usage)
audit=json.loads(sql("SELECT json_build_object('n',count(*),'from_seq',min(seq),'to_seq',max(seq),'failures',count(*) FILTER(WHERE result!='success')) FROM audit_events_v2 WHERE action LIKE 'contract_analysis.%' AND resource_id='"+aid+"'"))
check('production-submission-reads-and-export-audited',audit['n']>=8 and audit['failures']==0,audit)
arts=c.must('/api/v1/analysis/contracts/get_contract_analysis',{'analysis_id':aid,'section':'artifacts','page_size':200})['items'];bundle=next(a for a in arts if a['kind']=='evidence_bundle')
# Use the Action route for production; verify every byte, then every ZIP entry.
import base64
data=bytearray();offset=0
while True:
 part=c.must('/api/v1/export/contracts/get_contract_analysis_artifact',{'analysis_id':aid,'artifact_id':bundle['artifact_id'],'offset':offset,'chunk_size':1<<20});raw=base64.b64decode(part['content_base64']);assert hashlib.sha256(raw).hexdigest()==part['chunk_sha256'];data.extend(raw);offset+=len(raw)
 if part['next_offset'] is None:break
 assert part['next_offset']==offset
assert len(data)==bundle['byte_size'] and hashlib.sha256(data).hexdigest()==bundle['sha256']
with zipfile.ZipFile(io.BytesIO(data)) as z:
 manifest=json.loads(z.read('manifest.json'));assert set(z.namelist())=={'manifest.json'}|{r['path'] for r in manifest['entries']}
 for entry in manifest['entries']:
  blob=z.read(entry['path']);assert len(blob)==entry['byte_size'] and hashlib.sha256(blob).hexdigest()==entry['sha256']
check('production-zip-every-entry-checksum',True,{'byte_size':len(data),'sha256':bundle['sha256'],'files':len(manifest['entries'])});(OUT/'production-weth-evidence.zip').write_bytes(data)
expected={'blockchain-api.service':'3258429','blockchain-mcp.service':'3258435','blockchain-user-console.service':'2384809','blockchain-worker.service':'1971802'}
actual={unit:subprocess.check_output(['systemctl','--user','show',unit,'-p','MainPID','--value']).decode().strip() for unit in expected};check('original-services-kept-running',actual==expected,actual)
manifest=json.loads((OUT/'manifest.json').read_text());check('deployed-binaries-and-assets-match-frozen-manifest',all(hashlib.sha256((ROOT/f['path']).read_bytes()).hexdigest()==f['sha256'] for f in manifest['files']))
env['PATH']='/usr/local/go/bin:'+env['PATH'];subprocess.run(['go','build','-o','bin/contract-auditverify','./cmd/auditverify'],cwd=ROOT/'backend',env=env,check=True)
run=subprocess.run([str(ROOT/'backend/bin/contract-auditverify'),'-chain','audit','-from',str(audit['from_seq']),'-to',str(audit['to_seq']),'-timeout','30s'],env=env,capture_output=True,text=True)
if run.returncode==0:(OUT/'production-audit-chain.json').write_text(run.stdout)
check('production-audit-hash-chain-valid',run.returncode==0,run.stdout[:1400] if run.returncode==0 else 'read-only verifier failed')
print('PRODUCTION_INTEGRITY_QA_PASS',len(rows),aid,flush=True)
