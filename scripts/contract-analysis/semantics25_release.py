"""Switch the four contract analysis services to the funds-semantics-25 engine image."""
import argparse,hashlib,json,os,pathlib,subprocess,time,urllib.parse,urllib.request
os.environ.setdefault('XDG_RUNTIME_DIR','/run/user/'+str(os.getuid()))
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-permit-nonces-20261016'
BASE=ROOT/'acceptance/contract-analysis-semantics25';OUT=BASE/'release';OUT.mkdir(parents=True,exist_ok=True)
sys.path.insert(0,str(ROOT/'scripts/contract-analysis'));from qa_support import dotenv
ORIGINAL=pathlib.Path('/home/euripides/projects/blockchain-platform');UNITDIR=pathlib.Path('/home/euripides/.config/systemd/user')
UNITS=['blockchain-contract-api.service','blockchain-contract-worker.service','blockchain-contract-mcp.service','blockchain-contract-console.service']
OLDENV=pathlib.Path('/home/euripides/.config/blockchain-platform/contract-heap-writes-release-20261019.env')
ENVFILE=pathlib.Path('/home/euripides/.config/blockchain-platform/contract-semantics25-release-20261019.env')
PREVIOUS_IMAGE='sha256:4440baadedb3f57d5994da131f65007c85b691288dff9e6b0715f21921c7e485'
def sha(path):return hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()
def run(args):subprocess.run(args,check=True)
def sql(query):
    d=urllib.parse.urlparse(dotenv(ORIGINAL/'deploy/.env')['MIGRATION_DATABASE_URL']);assert d.path=='/blockchain_platform'
    env={**os.environ,'PGPASSWORD':urllib.parse.unquote(d.password)}
    return subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',d.username,'-d',d.path[1:]],input=query.encode(),env=env).decode().strip()
def systemctl_show(unit):
    env={**os.environ,'XDG_RUNTIME_DIR':'/run/user/'+str(os.getuid())}
    return subprocess.check_output(['systemctl','--user','show',unit,'-p','MainPID','--value'],text=True,env=env).strip()
def original_pids():
    return {u:systemctl_show(u) for u in ['blockchain-api.service','blockchain-worker.service','blockchain-mcp.service','blockchain-user-console.service']}
def public_capabilities():
    values=dotenv(ORIGINAL/'deploy/.env');request=urllib.request.Request('https://aurore.online/api/v1/analysis/contracts/get_contract_analysis_capabilities',data=b'{}',headers={'Authorization':'Bearer '+values['GPT_ACTIONS_API_KEY'],'Content-Type':'application/json','Origin':'https://aurore.online','User-Agent':'contract-analysis-qa/1'},method='POST')
    with urllib.request.urlopen(request,timeout=45)as response:assert response.status==200;payload=json.load(response)
    assert payload['success'];return payload['data']
def gates():
    manifest=json.loads((BASE/'candidate-freeze.json').read_text())
    assert manifest['status']=='candidate_certified_not_deployed' and manifest['rule_version']=='funds-semantics-25' and manifest['whole_goal_complete'] is False
    for file in manifest['sources']+manifest['built_files']+manifest['evidence_files']:assert sha(ROOT/file['path'])==file['sha256'],file['path']
    assert manifest['source_commit']==subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT).decode().strip()
    run(['git','-C',str(ROOT),'diff','--quiet','HEAD','--','analysis-engines','backend','mcp','frontend','scripts','docs','openapi'])
    original_pids();return manifest
def prepare():
    candidate=gates();image=candidate['image']
    previous=dotenv(OLDENV);assert previous['CONTRACT_ENGINE_IMAGE']==PREVIOUS_IMAGE
    values={**previous,'CONTRACT_ENGINE_IMAGE':image,'CONTRACT_ENGINE_PREVIOUS_IMAGES':','.join(dict.fromkeys([previous['CONTRACT_ENGINE_IMAGE'],*previous.get('CONTRACT_ENGINE_PREVIOUS_IMAGES','').split(',')]))}
    ENVFILE.write_text('\n'.join(k+'='+v for k,v in values.items())+'\n');ENVFILE.chmod(0o600)
    for unit in UNITS:
        raw=(UNITDIR/unit).read_text();assert str(OLDENV)in raw and str(ROOT)in raw,unit
        (OUT/(unit+'.before')).write_text(raw);(OUT/unit).write_text(raw.replace(str(OLDENV),str(ENVFILE)))
    manifest={'status':'prepared','image':image,'previous_image':previous['CONTRACT_ENGINE_IMAGE'],'source_commit':candidate['source_commit'],'candidate_freeze_sha256':sha(BASE/'candidate-freeze.json'),
        'environment_sha256':sha(ENVFILE),'previous_environment_sha256':sha(OLDENV),
        'unit_before_hashes':{unit:sha(OUT/(unit+'.before'))for unit in UNITS},'unit_after_hashes':{unit:sha(OUT/unit)for unit in UNITS},'original_pids':original_pids()}
    (OUT/'manifest.json').write_text(json.dumps(manifest,indent=2));print('SEMANTICS25_RELEASE_PREPARED',image,flush=True)
def verify():
    manifest=json.loads((OUT/'manifest.json').read_text());assert sha(BASE/'candidate-freeze.json')==manifest['candidate_freeze_sha256']
    assert sha(ENVFILE)==manifest['environment_sha256']and sha(OLDENV)==manifest['previous_environment_sha256']
    assert gates()['image']==manifest['image']
    for unit in UNITS:assert sha(OUT/unit)==manifest['unit_after_hashes'][unit]and sha(OUT/(unit+'.before'))==manifest['unit_before_hashes'][unit]
    return manifest
def ready(url):
    end=time.monotonic()+60
    while time.monotonic()<end:
        try:
            with urllib.request.urlopen(url,timeout=2)as response:assert response.status==200
            return
        except Exception:time.sleep(.5)
    raise RuntimeError('candidate service readiness failed '+url)
def restore():
    for unit in UNITS:(UNITDIR/unit).write_bytes((OUT/(unit+'.before')).read_bytes())
    run(['systemctl','--user','daemon-reload']);run(['systemctl','--user','restart',*UNITS])
    for url in['http://127.0.0.1:18084/health/ready','http://127.0.0.1:18085/health','http://127.0.0.1:18086/app-v2/']:ready(url)
    original_pids()
def deploy():
    manifest=verify()
    for unit in UNITS:assert sha(UNITDIR/unit)==manifest['unit_before_hashes'][unit],unit
    end=time.monotonic()+180
    while sql("SELECT count(*)FROM investigation_analysis_jobs WHERE version='contract-analysis-v1'AND status='running'")!='0':
        if time.monotonic()>end:raise RuntimeError('active contract tasks did not drain')
        time.sleep(1)
    run(['systemctl','--user','stop',*UNITS])
    try:
        for unit in UNITS:(UNITDIR/unit).write_bytes((OUT/unit).read_bytes())
        run(['systemctl','--user','daemon-reload']);run(['systemctl','--user','start',*UNITS])
        for url in['http://127.0.0.1:18084/health/ready','http://127.0.0.1:18085/health','http://127.0.0.1:18086/app-v2/']:ready(url)
        assert json.load(urllib.request.urlopen('http://127.0.0.1:18085/health'))['tools']==120
        capability=public_capabilities();assert capability['semantic_rules_version']=='funds-semantics-25'and capability['engine_digest']==manifest['image'],capability.get('semantic_rules_version')
        assert capability['state_model_verification']['whole_authorization_proved']is False and capability['state_model_verification']['whole_protocol_economics_proved']is False
        original_pids()
        manifest.update({'status':'deployed','deployed_utc':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()),'public_capability':{'semantic_rules_version':capability['semantic_rules_version'],'engine_digest':capability['engine_digest']}})
        (OUT/'manifest.json').write_text(json.dumps(manifest,indent=2));print('SEMANTICS25_RELEASE_DEPLOYED',manifest['image'],flush=True)
    except Exception:
        restore();manifest['status']='rolled_back_after_failed_switch';(OUT/'manifest.json').write_text(json.dumps(manifest,indent=2));raise
def rollback():
    manifest=verify()
    for unit in UNITS:assert sha(UNITDIR/unit)==manifest['unit_after_hashes'][unit],unit
    restore();manifest['status']='rolled_back';(OUT/'manifest.json').write_text(json.dumps(manifest,indent=2));print('SEMANTICS25_RELEASE_ROLLED_BACK',flush=True)
if __name__=='__main__':
    parser=argparse.ArgumentParser();parser.add_argument('operation',choices=['prepare','verify','deploy','rollback']);globals()[parser.parse_args().operation]()
