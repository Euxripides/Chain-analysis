"""Additive release: original API/MCP/downloader/console remain available."""
import argparse, hashlib, json, os, pathlib, re, subprocess, sys, time, urllib.parse, urllib.request
ROOT=pathlib.Path(__file__).resolve().parents[2];OUT=ROOT/'acceptance/contract-analysis/release';OUT.mkdir(parents=True,exist_ok=True)
sys.path.insert(0,str(ROOT/'scripts/contract-analysis'));from qa_support import dotenv
ORIGINAL=pathlib.Path('/home/euripides/projects/blockchain-platform');NGINX=pathlib.Path('/etc/nginx/sites-enabled/blockchain-platform.conf')
ENVFILE=pathlib.Path('/home/euripides/.config/blockchain-platform/contract-analysis-release-20261014.env')
UNITS=['blockchain-contract-api.service','blockchain-contract-worker.service','blockchain-contract-mcp.service','blockchain-contract-console.service']
def sha(path):return hashlib.sha256(path.read_bytes()).hexdigest()
def run(command,**kw):return subprocess.run(command,check=True,**kw)
def sql(query):
 values=dotenv(ORIGINAL/'deploy/.env');dsn=urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']);assert dsn.path=='/blockchain_platform';env=os.environ.copy();env['PGPASSWORD']=urllib.parse.unquote(dsn.password)
 return subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',dsn.username,'-d',dsn.path[1:]],input=query.encode(),env=env).decode().strip()
def baseline():
 original=json.loads((ROOT/'acceptance/contract-analysis/deployment-baseline.json').read_text());drift=[]
 for row in original['files']:
  p=ORIGINAL/row['path']
  if not p.is_file() or sha(p)!=row['sha256']:drift.append(row['path'])
 if drift:raise RuntimeError('original source drift: '+str(drift[:10]))
 for path,expected in original['running_artifact_hashes'].items():
  p=ORIGINAL/('backend/bin/'+path if path in ['api','worker'] else path)
  if sha(p)!=expected:raise RuntimeError('running artifact drift: '+path)
 for report in ['live-api-qa.json','mcp-live-qa.json','execution-live-qa.json','lifecycle-qa.json','resource-fault-qa.json','release-oauth-qa.json','browser/report.json']:
  rows=json.loads((ROOT/'acceptance/contract-analysis'/report).read_text());assert rows and all(x['status']=='PASS' for x in rows),report
 bench=json.loads((ROOT/'acceptance/contract-analysis/isolated-engine-benchmark.json').read_text());assert len(bench)==24 and all(x['passed'] for x in bench)
 assert json.loads((ROOT/'acceptance/contract-analysis/legacy-browser-regression/report.json').read_text())['ok']
 assert json.loads((ROOT/'acceptance/contract-analysis/baseline-frontend-identity.json').read_text())['status']=='PASS'
 return original
def prepare():
 baseline();image=(ROOT/'acceptance/contract-analysis/engine-image.txt').read_text().strip();assert re.fullmatch(r'sha256:[0-9a-f]{64}',image)
 env='\n'.join(['CONTRACT_ANALYSIS_ENABLED=1','CONTRACT_ENGINE_IMAGE='+image,'API_HTTP_PORT=18084','API_HTTP_BIND_HOST=127.0.0.1','API_FEATURE_ONLY=1','API_STANDBY_STARTUP=1','INVESTIGATION_WORKER_ENABLE=0','MCP_PORT=18085','REST_API_BASE_URL=http://127.0.0.1:8080/api/v1','CONTRACT_REST_API_BASE_URL=http://127.0.0.1:18084/api/v1','MCP_LEGACY_OAUTH_VALIDATION_URL=http://127.0.0.1:8090/mcp'])+'\n';ENVFILE.write_text(env);ENVFILE.chmod(0o600)
 commands={UNITS[0]:(ROOT/'backend',str(ROOT/'backend/bin/contract-analysis-api'),'768M'),UNITS[1]:(ROOT/'backend',str(ROOT/'backend/bin/contract-analysis-worker'),'768M'),UNITS[2]:(ROOT/'mcp','/home/euripides/.nvm/versions/node/v24.20.0/bin/node dist/server.js','512M'),UNITS[3]:(ROOT/'frontend','/home/euripides/.nvm/versions/node/v24.20.0/bin/node '+str(ORIGINAL/'frontend/node_modules/vite/bin/vite.js')+' preview --config apps/user-console/vite.config.ts --host 127.0.0.1 --port 18086 --strictPort','512M')}
 for unit,(cwd,command,memory) in commands.items():
  text='[Unit]\nDescription=Contract analysis '+unit+'\n[Service]\nWorkingDirectory='+str(cwd)+'\nEnvironmentFile='+str(ORIGINAL/'deploy/.env')+'\nEnvironmentFile='+str(ENVFILE)+'\nEnvironment=PATH=/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:/usr/bin:/bin\nExecStart='+command+'\nMemoryMax='+memory+'\nRestart=on-failure\nRestartSec=5\n[Install]\nWantedBy=default.target\n'
  (OUT/unit).write_text(text)
 original=NGINX.read_text();(OUT/'nginx-before.conf').write_text(original)
 # Preserve the live OAuth authority. Only MCP protocol/health switch ports.
 def swap(match):return match.group(0).replace('127.0.0.1:8090','127.0.0.1:18085')
 updated=re.sub(r'location = /(?:mcp|mcp-health)\s*\{[^}]*\}',swap,original)
 updated=updated.replace('proxy_pass http://127.0.0.1:3001/app-v2/;','proxy_pass http://127.0.0.1:18086/app-v2/;')
 assert updated!=original and 'proxy_pass http://127.0.0.1:8090;' in updated
 shared='''
        proxy_pass http://127.0.0.1:18084;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $http_cf_connecting_ip;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_read_timeout 120s;
'''
 additions='\n    # Approved additive contract analysis routes.\n'
 for route in ['= /api/v2/contract-analyses','^~ /api/v2/contract-analyses/','^~ /api/v1/analysis/contracts/','^~ /api/v1/export/contracts/']:additions+='    location '+route+' {\n'+shared+'    }\n'
 additions+='    location = /contract-analysis-openapi.json {\n        alias /var/www/html/contract-analysis/openapi.json;\n        default_type application/json;\n    }\n'
 marker='    # API and large exports bypass';assert marker in updated;updated=updated.replace(marker,additions+'\n'+marker,1);(OUT/'nginx-candidate.conf').write_text(updated)
 manifest={'status':'prepared','candidate':str(ROOT),'engine_digest':image,'original_pids':{},'files':[]}
 for unit in ['blockchain-api.service','blockchain-mcp.service','blockchain-user-console.service','blockchain-downloader.service']:manifest['original_pids'][unit]=subprocess.check_output(['systemctl','--user','show',unit,'-p','MainPID','--value']).decode().strip()
 paths=[ROOT/'backend/bin/contract-analysis-api',ROOT/'backend/bin/contract-analysis-worker',ROOT/'backend/migrations/postgres/0083_contract_analysis.sql',ROOT/'openapi/contract-analysis-v1.json',*ROOT.joinpath('mcp/dist').rglob('*.js'),*ROOT.joinpath('frontend/apps/user-console/dist').rglob('*')]
 for p in paths:
  if p.is_file():manifest['files'].append({'path':str(p.relative_to(ROOT)),'byte_size':p.stat().st_size,'sha256':sha(p)})
 manifest['nginx_before_sha256']=sha(OUT/'nginx-before.conf');manifest['nginx_candidate_sha256']=sha(OUT/'nginx-candidate.conf');(OUT/'manifest.json').write_text(json.dumps(manifest,indent=2));print('RELEASE_PREPARED',len(manifest['files']),image,flush=True)
def verify_files():
 manifest=json.loads((OUT/'manifest.json').read_text())
 for f in manifest['files']:assert sha(ROOT/f['path'])==f['sha256'],f['path']
 return manifest
def ready(url):
 deadline=time.monotonic()+30
 while time.monotonic()<deadline:
  try:
   with urllib.request.urlopen(url,timeout=2) as response:
    if response.status==200:return
  except Exception:time.sleep(.5)
 raise RuntimeError('feature readiness timeout '+url)
def deploy():
 baseline();manifest=verify_files();assert sha(NGINX)==manifest['nginx_before_sha256'],'nginx changed after prepare'
 before=sql("SELECT max(name) FROM schema_migrations");assert before=='postgres/0082_export_file_manifest.sql',before
 body=(ROOT/'backend/migrations/postgres/0083_contract_analysis.sql').read_text()
 migration="BEGIN; SET LOCAL lock_timeout='2s'; SET LOCAL statement_timeout='60s'; SELECT pg_advisory_xact_lock(hashtext('blockchain-platform:postgres-migrations'));"+body+"\nINSERT INTO schema_migrations(name) VALUES('postgres/0083_contract_analysis.sql');COMMIT;"
 sql(migration);manifest['migration_applied']=True;(OUT/'manifest.json').write_text(json.dumps(manifest,indent=2));print('MIGRATION_0083_APPLIED',flush=True)
 target=pathlib.Path('/home/euripides/.config/systemd/user')
 for unit in UNITS:assert not (target/unit).exists(),'unit already exists '+unit;(target/unit).write_bytes((OUT/unit).read_bytes())
 run(['systemctl','--user','daemon-reload']);run(['systemctl','--user','stop','blockchain-contract-qa-worker.service'])
 try:
  run(['systemctl','--user','start',*UNITS]);ready('http://127.0.0.1:18084/health/ready');ready('http://127.0.0.1:18085/health');ready('http://127.0.0.1:18086/app-v2/')
  run(['sudo','-n','install','-D','-m','0644',str(ROOT/'openapi/contract-analysis-v1.json'),'/var/www/html/contract-analysis/openapi.json'])
  assert json.load(urllib.request.urlopen('http://127.0.0.1:18085/health'))['tools']==119
  run(['sudo','-n','cp',str(OUT/'nginx-candidate.conf'),str(NGINX)]);run(['sudo','-n','nginx','-t']);run(['sudo','-n','systemctl','reload','nginx'])
  run(['systemctl','--user','enable',*UNITS]);manifest['status']='deployed';manifest['deployed_at']=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime());(OUT/'manifest.json').write_text(json.dumps(manifest,indent=2));print('ADDITIVE_RELEASE_DEPLOYED',flush=True)
 except Exception:
  run(['sudo','-n','cp',str(OUT/'nginx-before.conf'),str(NGINX)]);run(['sudo','-n','nginx','-t']);run(['sudo','-n','systemctl','reload','nginx']);run(['systemctl','--user','stop',*UNITS]);raise
def rollback():
 manifest=verify_files();assert sha(NGINX)==manifest['nginx_candidate_sha256'],'nginx drift requires review'
 run(['sudo','-n','cp',str(OUT/'nginx-before.conf'),str(NGINX)]);run(['sudo','-n','nginx','-t']);run(['sudo','-n','systemctl','reload','nginx']);run(['systemctl','--user','disable','--now',*UNITS]);manifest['status']='rolled_back';(OUT/'manifest.json').write_text(json.dumps(manifest,indent=2));print('ROLLED_BACK_WITH_DATA_RETAINED')
def disable_submissions():
 manifest=verify_files();text=ENVFILE.read_text();assert 'CONTRACT_ANALYSIS_ENABLED=1' in text
 ENVFILE.write_text(text.replace('CONTRACT_ANALYSIS_ENABLED=1','CONTRACT_ANALYSIS_ENABLED=0'));ENVFILE.chmod(0o600)
 run(['systemctl','--user','stop','blockchain-contract-worker.service']);run(['systemctl','--user','restart','blockchain-contract-api.service']);ready('http://127.0.0.1:18084/health/ready')
 manifest['status']='submission_disabled';(OUT/'manifest.json').write_text(json.dumps(manifest,indent=2));print('NEW_SUBMISSIONS_DISABLED_SAVED_RESULTS_REMAIN_READABLE')
if __name__=='__main__':
 parser=argparse.ArgumentParser();parser.add_argument('operation',choices=['prepare','deploy','rollback','disable_submissions']);args=parser.parse_args();globals()[args.operation]()
