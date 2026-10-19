"""Prepare the existing isolated QA for a frozen scalar-value candidate only."""
import datetime,hashlib,json,os,pathlib,subprocess,time,urllib.parse,urllib.request
from qa_support import dotenv
ROOT=pathlib.Path(__file__).resolve().parents[2];BASE=ROOT/'acceptance/contract-analysis-scalars'
image=(BASE/'engine-image.txt').read_text().strip();freeze=json.loads((BASE/'frozen-engine'/image[7:23]/'engine-source-freeze.json').read_text())
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
assert all(sha(ROOT/r['path'])==r['sha256']for r in freeze['files'])
env={**os.environ,'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+os.environ['PATH'],'GOMAXPROCS':'2','GOPROXY':'off'}
production=['blockchain-api','blockchain-worker','blockchain-mcp','blockchain-user-console','blockchain-contract-api','blockchain-contract-worker','blockchain-contract-mcp','blockchain-contract-console']
def pids():return{u:subprocess.check_output(['systemctl','--user','show',u,'-p','MainPID','--value'],text=True).strip()for u in production}
stamp=datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
target=BASE/'service-validation'/image[7:23]/stamp;target.mkdir(parents=True);before=pids()
(target/'production-pids-before.json').write_text(json.dumps(before,indent=2))
values=dotenv('/home/euripides/.config/blockchain-platform/contract-storage-qa-20261017.env')
assert urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']).path=='/contract_analysis_qa_20261014'
values['CONTRACT_ENGINE_PREVIOUS_IMAGES']=','.join(dict.fromkeys([values['CONTRACT_ENGINE_IMAGE'],*values.get('CONTRACT_ENGINE_PREVIOUS_IMAGES','').split(',')]))
values.update(CONTRACT_ENGINE_IMAGE=image,DRPC_ENABLED='false',DRPC_PROBES_ENABLED='false',SOLANA_ENABLED='false',SOLANA_PROVIDER='ankr',ANKR_MANAGEMENT_ENABLED='false',RPC_PROBE_ON_START='false')
config=pathlib.Path('/home/euripides/.config/blockchain-platform/contract-scalars-qa-20261017.env');config.write_text('\n'.join(k+'='+v for k,v in values.items())+'\n');config.chmod(0o600)
bindir=BASE/'bin'/image[7:23]/stamp;bindir.mkdir(parents=True)
for name,command,cwd in[('api-build',['go','build','-o',str(bindir/'api'),'./cmd/api'],ROOT/'backend'),('worker-build',['go','build','-o',str(bindir/'worker'),'./cmd/contract-worker'],ROOT/'backend'),('mcp-build',['npm','run','build'],ROOT/'mcp')]:
    with(target/(name+'.log')).open('w')as f:subprocess.run(command,cwd=cwd,env=env,stdout=f,stderr=subprocess.STDOUT,check=True)
for name in['api','worker','mcp']:
    directory=pathlib.Path.home()/'.config/systemd/user'/('blockchain-contract-coverage-qa-'+name+'.service.d');directory.mkdir(exist_ok=True)
    content='[Service]\nEnvironmentFile='+str(config)+'\n'
    if name!='mcp':content+='ExecStart=\nExecStart='+str(bindir/name)+'\n'
    (directory/'60-source-scalars.conf').write_text(content)
subprocess.run(['systemctl','--user','daemon-reload'],check=True)
subprocess.run(['systemctl','--user','restart',*['blockchain-contract-coverage-qa-'+n+'.service'for n in['api','worker','mcp']]],check=True)
for url in['http://127.0.0.1:18091/health/ready','http://127.0.0.1:18092/health']:
    for attempt in range(80):
        try:
            with urllib.request.urlopen(url,timeout=1)as response:assert response.status==200
            break
        except Exception:time.sleep(.25)
    else:raise RuntimeError('Isolated service not ready')
actual_env={}
for name in['api','worker']:
    pid=subprocess.check_output(['systemctl','--user','show','blockchain-contract-coverage-qa-'+name+'.service','-p','MainPID','--value'],text=True).strip()
    actual=dict(v.split('=',1)for v in(pathlib.Path('/proc')/pid/'environ').read_text().split('\0')if'='in v)
    selected={k:actual.get(k)for k in['CONTRACT_ENGINE_IMAGE','DRPC_ENABLED','DRPC_PROBES_ENABLED','SOLANA_ENABLED','SOLANA_PROVIDER','ANKR_MANAGEMENT_ENABLED','RPC_PROBE_ON_START']}
    assert selected=={k:values[k]for k in selected};actual_env[name]=selected
assert pids()==before,'Production process identity changed'
(target/'binary-identity.json').write_text(json.dumps({'image':image,'api_sha256':sha(bindir/'api'),'worker_sha256':sha(bindir/'worker'),
    'mcp_dist_sha256':sha(ROOT/'mcp/dist/contract-analysis.js'),'background_control_environment':actual_env,'production_pids_unchanged':True,
    'binary_directory':str(bindir),'backend_sources':[{ 'path':p.relative_to(ROOT).as_posix(),'sha256':sha(p)}for p in sorted((ROOT/'backend').rglob('*.go'))]},indent=2))
(BASE/'service-current.txt').write_text(str(target)+'\n')
print('P2_SCALAR_ISOLATED_QA_READY',image,flush=True)
