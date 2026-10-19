"""Certify the frozen funds-semantics-20 candidate and its isolated evidence."""
import datetime,hashlib,json,os,pathlib,subprocess,time
os.environ.setdefault('XDG_RUNTIME_DIR','/run/user/'+str(os.getuid()))
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-permit-nonces-20261016'
BASE=ROOT/'acceptance/contract-analysis-heap-writes';OUT=BASE/'release';OUT.mkdir(parents=True,exist_ok=True)
NODE='/home/euripides/.nvm/versions/node/v24.20.0/bin/node'
sha=lambda p:hashlib.sha256(pathlib.Path(p).read_bytes()).hexdigest()
image=(BASE/'engine-image.txt').read_text().strip();freeze=json.loads((BASE/'frozen-engine'/image[7:23]/'engine-source-freeze.json').read_text())
assert freeze['image']==image and freeze['rule_version']=='funds-semantics-20' and len(freeze['files'])==66
def intact():
    for item in freeze['files']:assert sha(ROOT/item['path'])==item['sha256'],item['path']
intact()
ANVIL=str(ROOT.parent/'contract-verification-20261015/analysis-engines/bin/anvil')
env={**os.environ,'XDG_RUNTIME_DIR':'/run/user/'+str(os.getuid()),'PATH':NODE+':/usr/local/go/bin:'+os.environ['PATH'],'GOMAXPROCS':'2','GOPROXY':'off','CONTRACT_TEST_IMAGE':image,'P2_TAC_IMAGE':image,
 'CONTRACT_PYTHON':str(ROOT/'scripts/contract-analysis/engine_python_bridge.py'),'CONTRACT_CLOSURE_ENGINE_MODE':'frozen','CONTRACT_EXPECTED_RULE_VERSION':'funds-semantics-20','ANVIL_BINARY':ANVIL,'UC_CONTRACT_BASELINE':'1cfe42a4e060a5607fadacde5d259f210449ffd6'}
assert pathlib.Path(ANVIL).is_file()
gates=json.loads((OUT/'release-gates.json').read_text())if(OUT/'release-gates.json').exists()else{}
def run(name,command,cwd):
    intact()
    if gates.get(name,{}).get('status')=='PASS'and gates[name].get('image')==image:
        print('REUSE_RELEASE_GATE',name,flush=True);return
    log=OUT/(name+'.log')
    if log.exists():log.rename(OUT/(str(time.time_ns())+'-'+log.name))
    started=time.monotonic()
    with log.open('w')as handle:p=subprocess.run(command,cwd=cwd,env=env,stdout=handle,stderr=subprocess.STDOUT,timeout=2400)
    intact();gates[name]={'status':'PASS'if p.returncode==0 else'FAIL','exit_code':p.returncode,'image':image,'wall_seconds':round(time.monotonic()-started,3),'command':command,'working_directory':str(cwd.relative_to(ROOT)),'log':str(log.relative_to(ROOT)),'log_sha256':sha(log)}
    (OUT/'release-gates.json').write_text(json.dumps(gates,indent=2));print('RELEASE_GATE',name,gates[name]['status'],flush=True)
    if p.returncode:print(log.read_text()[-8000:],flush=True);raise SystemExit(p.returncode)
run('engine-unit',['node','--test','--test-concurrency=2',*[str(p.relative_to(ROOT/'analysis-engines'))for p in sorted((ROOT/'analysis-engines/test').glob('*.test.mjs'))]],ROOT/'analysis-engines')
run('backend-contract',['go','test','./internal/contractanalysis','./internal/api','-run','Contract|Submitted|CodeOnly|EngineTransport|Next|Investigation|ContainerExecutionResource','-count=1'],ROOT/'backend')
run('mcp-check',['npm','run','check'],ROOT/'mcp')
run('frontend-typecheck-user',[NODE,'node_modules/typescript/bin/tsc','--noEmit','-p','apps/user-console/tsconfig.json'],ROOT/'frontend')
run('frontend-contract-freeze',[NODE,'scripts/uc-contract-freeze-check.mjs'],ROOT/'frontend')
assert all(g.get('status')=='PASS'and g.get('image')==image for g in gates.values())
# Engine/runtime evidence for this exact image.
phases={}
for directory in sorted((BASE/'validation'/image[7:23]).glob('*')):
    rows=json.loads((directory/'gates.json').read_text())
    phases[directory.name.split('-',1)[1]]=rows
required=['unit','qa','mcp','scalar-runtime','heap-runtime','storage-runtime','effects-runtime','legacy-runtime','business-runtime','scalar-profiles','decrement-runtime','copy-runtime']
assert set(required)<=set(phases),sorted(phases)
for phase in required:
    latest=[d for d in sorted((BASE/'validation'/image[7:23]).glob('*-'+phase))][-1]
    rows=json.loads((latest/'gates.json').read_text())
    assert rows and all(g['exit_code']==0 and g['image']==image for g in rows),phase
    phases[phase]={'directory':str(latest.relative_to(ROOT)),'gates':rows}
mcp_checks=[]
for directory in sorted((BASE/'mcp'/image[7:23]).glob('*')):
    rows=json.loads((directory/'checks.json').read_text());assert rows and all(r['status']=='PASS'for r in rows),directory
    mcp_checks.append({'directory':str(directory.relative_to(ROOT)),'checks':len(rows)})
feature_checks=[]
for category in ['field-mcp','storage-copy-mcp','tuple-field-mcp','creation-identity-mcp','byte-alias-mcp','byte-consumers-mcp','budget-frontier-mcp','large-output-mcp']:
    for directory in sorted((BASE/category/image[7:23]).glob('*')):
        rows=json.loads((directory/'checks.json').read_text());assert rows and all(r['status']=='PASS'for r in rows),directory
        feature_checks.append({'category':category,'directory':str(directory.relative_to(ROOT)),'checks':len(rows)})
assessment=json.loads(sorted((BASE/'business-assessment'/image[7:23]).glob('*/results.json'))[-1].read_text())
assert assessment['image']==image and assessment['assessment_completed']=='PASS' and assessment['general_acceptance']=='NO_GO'
# The production-facing API binary must be the byte-identical rebuild of the QA binary.
qa=json.loads(sorted((BASE/'service-validation'/image[7:23]).glob('*/binary-identity.json'))[-1].read_text())
api=sha(ROOT/'backend/bin/contract-analysis-api');worker=sha(ROOT/'backend/bin/contract-analysis-worker')
assert api==qa['api_sha256'] and worker==qa['worker_sha256'],'production binaries are not the verified QA builds'
built=[{'path':str(p.relative_to(ROOT)),'sha256':sha(p),'byte_size':p.stat().st_size}for p in sorted([ROOT/'backend/bin/contract-analysis-api',ROOT/'backend/bin/contract-analysis-worker',*ROOT.joinpath('mcp/dist').rglob('*.js'),*ROOT.joinpath('frontend/apps/user-console/dist').rglob('*')])if p.is_file()]
assert all((ROOT/n).is_file()for n in['backend/bin/contract-analysis-api','backend/bin/contract-analysis-worker','mcp/dist/server.js','frontend/apps/user-console/dist/index.html'])
units=['blockchain-api','blockchain-worker','blockchain-mcp','blockchain-user-console','blockchain-contract-api','blockchain-contract-worker','blockchain-contract-mcp','blockchain-contract-console']
pids={u:subprocess.check_output(['systemctl','--user','show',u,'-p','MainPID','--value'],text=True).strip()for u in units}
assert all(int(v)>0 for v in pids.values()),pids
sources=[]
paths=set(subprocess.check_output(['git','ls-files','-z'],cwd=ROOT).decode().split('\0'))
for name in sorted(paths):
    p=pathlib.PurePosixPath(name)
    if not name or p.parts[0]not in ['analysis-engines','backend','mcp','frontend','scripts','docs','openapi']or{'node_modules','dist','bin','acceptance','acceptance-evidence','__pycache__'}.intersection(p.parts)or p.name.startswith('.env')or not(ROOT/name).is_file():continue
    sources.append({'path':name,'sha256':sha(ROOT/name)})
evidence=[]
for base in[BASE/'frozen-engine'/image[7:23],BASE/'business-assessment'/image[7:23],BASE/'service-validation'/image[7:23],BASE/'release']:
    for p in sorted(base.rglob('*')):
        if p.is_file()and not p.is_symlink()and p.stat().st_size<4<<20:evidence.append({'path':str(p.relative_to(ROOT)),'sha256':sha(p),'byte_size':p.stat().st_size})
for phase in required:
    directory=ROOT/phases[phase]['directory']
    for p in sorted(directory.glob('*.json')):
        evidence.append({'path':str(p.relative_to(ROOT)),'sha256':sha(p),'byte_size':p.stat().st_size})
for base in [BASE/'mcp'/image[7:23],*[BASE/category/image[7:23]for category in ['field-mcp','storage-copy-mcp','tuple-field-mcp','creation-identity-mcp','byte-alias-mcp','byte-consumers-mcp','budget-frontier-mcp','large-output-mcp']]]:
    for p in sorted(base.glob('*/checks.json')):
        evidence.append({'path':str(p.relative_to(ROOT)),'sha256':sha(p),'byte_size':p.stat().st_size})
remaining=['struct storage-to-memory copies and interpreter memory structs still partial','general memory/storage/SSA/conditional ABI handling still partial','nonlinear loops bounded at 32 paths, 16 iterations and 64 steps','real host side effects of external calls not fully modelled','historical initialization and proxy upgrade continuity partial','signatures, price, fee and liquidation models partial','AMM shares, vault, staking rewards and bridge settlement conversions partial','authorization sufficiency unproved and role membership not enumerable','protocol economics not proven and must bind state plus execution evidence','Gigahorse is not all-path verification','independent semantic review 4027 of 7125 facts complete, 3098 remaining','192 business quality facts NO_GO: source 13 recovered/42 partial/9 unavailable, submitted source 13/50/1, hidden source 64 unrecovered, fresh blind samples 0']
summary={'status':'isolated_candidate_certified_not_deployed','rule_version':'funds-semantics-20','image':image,'production_rule_version':'funds-semantics-8','production_image':'sha256:466f29f2075bc9c087e4fb7e00f2fc6cb11197abff3883823b4da33961b781b6','source_commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),'time_beijing':datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8))).isoformat(),'gates':{name:g['status']for name,g in gates.items()},'runtime_phases':{k:(v['directory']if isinstance(v,dict)else v)for k,v in phases.items()},'mcp_e2e_checks':sum(x['checks']for x in mcp_checks),'mcp_feature_checks':sum(x['checks']for x in feature_checks),'business_quality':{'general_acceptance':assessment['general_acceptance'],'facts':assessment['facts'],'aggregate':assessment['aggregate'],'fresh_blind_samples':assessment['fresh_blind_samples'],'dangling_fact_evidence':assessment['dangling_fact_evidence']},'whole_authorization_proved':False,'whole_protocol_economics_proved':False,'whole_goal_complete':False,'remaining':remaining}
(BASE/'publication-summary.json').write_text(json.dumps(summary,indent=2))
(BASE/'candidate-freeze.json').write_text(json.dumps({**summary,'engine_files':freeze['files'],'sources':sources,'built_files':built,'evidence_files':evidence,'production_pids':pids},indent=2))
print('HEAP_CANDIDATE_CERTIFIED',image,'SOURCES',len(sources),'EVIDENCE',len(evidence),'MCP',summary['mcp_e2e_checks']+summary['mcp_feature_checks'],flush=True)
