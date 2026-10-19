"""Certify the funds-semantics-25 candidate: frozen image, gates and evidence."""
import datetime,hashlib,json,os,pathlib,subprocess,time
os.environ.setdefault('XDG_RUNTIME_DIR','/run/user/'+str(os.getuid()))
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-permit-nonces-20261016'
BASE=ROOT/'acceptance/contract-analysis-semantics25';OUT=BASE/'release';GATES=OUT/'gates';GATES.mkdir(parents=True,exist_ok=True)
NODE='/home/euripides/.nvm/versions/node/v24.20.0/bin/node';ANVIL=str(ROOT.parent/'contract-verification-20261015/analysis-engines/bin/anvil')
sha=lambda p:hashlib.sha256(pathlib.Path(p).read_bytes()).hexdigest()
freeze_meta=json.loads(next((BASE/'frozen-engine').glob('*/engine-source-freeze.json')).read_text())
image=freeze_meta['image'];assert freeze_meta['rule_version']=='funds-semantics-25'
def intact():
    for item in freeze_meta['files']:assert sha(ROOT/item['path'])==item['sha256'],item['path']
intact()
env={**os.environ,'PATH':NODE+':/usr/local/go/bin:'+os.environ['PATH'],'GOMAXPROCS':'2','GOPROXY':'off','CONTRACT_TEST_IMAGE':image,'CONTRACT_EXPECTED_RULE_VERSION':'funds-semantics-25',
 'CONTRACT_PYTHON':str(ROOT/'scripts/contract-analysis/engine_python_bridge.py'),'ANVIL_BINARY':ANVIL,'UC_CONTRACT_BASELINE':'1cfe42a4e060a5607fadacde5d259f210449ffd6'}
records=json.loads((GATES/'gates.json').read_text())if(GATES/'gates.json').exists()else{}
def run(name,command,cwd):
    intact()
    if records.get(name,{}).get('status')=='PASS'and records[name].get('image')==image:
        print('REUSE_GATE',name,flush=True);return
    log=GATES/(name+'.log')
    if log.exists():log.rename(GATES/(str(time.time_ns())+'-'+log.name))
    started=time.monotonic()
    with log.open('w')as handle:p=subprocess.run(command,cwd=cwd,env=env,stdout=handle,stderr=subprocess.STDOUT,timeout=2400)
    intact();records[name]={'status':'PASS'if p.returncode==0 else'FAIL','exit_code':p.returncode,'image':image,'wall_seconds':round(time.monotonic()-started,3),'command':command,'working_directory':str(cwd.relative_to(ROOT)),'log':str(log.relative_to(ROOT)),'log_sha256':sha(log)}
    (GATES/'gates.json').write_text(json.dumps(records,indent=2));print('GATE',name,records[name]['status'],flush=True)
    if p.returncode:print(log.read_text()[-6000:],flush=True);raise SystemExit(p.returncode)
run('engine-unit-solver',['node','--test','--test-concurrency=2',*[str(p.relative_to(ROOT/'analysis-engines'))for p in sorted((ROOT/'analysis-engines/test').glob('*.test.mjs'))]],ROOT/'analysis-engines')
run('backend-contract',['go','test','./internal/contractanalysis','./internal/api','-run','Contract|Submitted|CodeOnly|EngineTransport|Next|Investigation|ContainerExecutionResource','-count=1'],ROOT/'backend')
run('mcp-check',['npm','run','check'],ROOT/'mcp')
run('frontend-typecheck-user',[NODE,'node_modules/typescript/bin/tsc','--noEmit','-p','apps/user-console/tsconfig.json'],ROOT/'frontend')
run('frontend-contract-freeze',[NODE,'scripts/uc-contract-freeze-check.mjs'],ROOT/'frontend')
for rule in ['23','24','25']:
    run('runtime-'+rule,['node','scripts/contract-analysis/p2_semantics'+rule+'_runtime.mjs'],ROOT)
assert all(g.get('status')=='PASS'and g.get('image')==image for g in records.values())
built=[{'path':str(p.relative_to(ROOT)),'sha256':sha(p),'byte_size':p.stat().st_size}for p in sorted([ROOT/'backend/bin/semantics25/contract-analysis-api',ROOT/'backend/bin/semantics25/contract-analysis-worker',*ROOT.joinpath('mcp/dist').rglob('*.js'),*ROOT.joinpath('frontend/apps/user-console/dist').rglob('*')])if p.is_file()]
assert all((ROOT/n).is_file()for n in['backend/bin/semantics25/contract-analysis-api','backend/bin/semantics25/contract-analysis-worker','mcp/dist/server.js','frontend/apps/user-console/dist/index.html'])
units={u:subprocess.check_output(['systemctl','--user','show',u,'-p','MainPID','--value'],text=True,env=os.environ).strip()for u in['blockchain-api.service','blockchain-worker.service','blockchain-mcp.service','blockchain-user-console.service']}
assert all(int(v)>0 for v in units.values()),units
sources=[]
for name in sorted(subprocess.check_output(['git','ls-files','-z'],cwd=ROOT).decode().split('\0')):
    p=pathlib.PurePosixPath(name)
    if not name or p.parts[0]not in['analysis-engines','backend','mcp','frontend','scripts','docs','openapi']or{'node_modules','dist','bin','acceptance','acceptance-evidence','__pycache__'}.intersection(p.parts)or p.name.startswith('.env')or not(ROOT/name).is_file():continue
    sources.append({'path':name,'sha256':sha(ROOT/name)})
evidence=[]
for base in[BASE/'frozen-engine'/image[7:23],GATES]:
    for p in sorted(base.rglob('*')):
        if p.is_file()and not p.is_symlink()and p.stat().st_size<4<<20 and p.name not in{'gates.json'}and p.suffix!='.log':evidence.append({'path':str(p.relative_to(ROOT)),'sha256':sha(p),'byte_size':p.stat().st_size})
    for p in sorted(base.glob('*.log')):
        evidence.append({'path':str(p.relative_to(ROOT)),'sha256':sha(p),'byte_size':p.stat().st_size})
for rule in ['23','24','25']:
    report=sorted((ROOT/'acceptance'/('contract-analysis-semantics'+rule)).glob('*/report.json'))[-1]
    evidence.append({'path':str(report.relative_to(ROOT)),'sha256':sha(report),'byte_size':report.stat().st_size})
    model=report.parent/'model.json'
    if model.exists():evidence.append({'path':str(model.relative_to(ROOT)),'sha256':sha(model),'byte_size':model.stat().st_size})
remaining=['arbitrary external calls and reentrancy remain explicit gaps','arrays and dynamic content beyond the 256 byte copy bound remain gaps','long dynamic storage writes remain an explicit state-model gap','historical initialization and proxy upgrade continuity not modeled','signatures, price, fee and liquidation models partial','protocol conversions for AMM shares, vaults, staking rewards and bridge settlement not implemented','whole authorization and protocol economics proofs remain false']
summary={'status':'candidate_certified_not_deployed','rule_version':'funds-semantics-25','image':image,'previous_production_image':'sha256:4440baadedb3f57d5994da131f65007c85b691288dff9e6b0715f21921c7e485','production_rule_version':'funds-semantics-20','source_commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),'time_beijing':datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8))).isoformat(),'gates':{k:g['status']for k,g in records.items()},'engine_files':len(freeze_meta['files']),'whole_authorization_proved':False,'whole_protocol_economics_proved':False,'whole_goal_complete':False,'remaining':remaining}
(BASE/'publication-summary.json').write_text(json.dumps(summary,indent=2))
(BASE/'candidate-freeze.json').write_text(json.dumps({**summary,'engine_files':freeze_meta['files'],'sources':sources,'built_files':built,'evidence_files':evidence,'production_pids':units},indent=2))
print('SEMANTICS25_CANDIDATE_CERTIFIED',image,'SOURCES',len(sources),'BUILT',len(built),'EVIDENCE',len(evidence),flush=True)
