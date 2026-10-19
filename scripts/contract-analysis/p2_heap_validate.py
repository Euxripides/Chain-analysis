"""Run scalar gates with exact source snapshots and independent output folders."""
import datetime,hashlib,json,os,pathlib,resource,subprocess,sys,time,zipfile

resource.setrlimit(resource.RLIMIT_CORE,(0,0))
ROOT=pathlib.Path(__file__).resolve().parents[2];BASE=ROOT/'acceptance/contract-analysis-heap-writes'
image=(BASE/'engine-image.txt').read_text().strip();freeze=json.loads((BASE/'frozen-engine'/image[7:23]/'engine-source-freeze.json').read_text())
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
assert len(freeze['files'])==66 and all(sha(ROOT/r['path'])==r['sha256']for r in freeze['files'])
phase=sys.argv[1];assert phase in['unit','scalar-runtime','heap-runtime','storage-runtime','effects-runtime','legacy-runtime','business-runtime','scalar-profiles','decrement-runtime','copy-runtime','qa','mcp']
out=BASE/'validation'/image[7:23]/(datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')+'-'+phase);out.mkdir(parents=True)
paths={*(ROOT/'analysis-engines/test').rglob('*.mjs'),*(ROOT/'scripts/contract-analysis').glob('*.mjs'),*(ROOT/'scripts/contract-analysis').glob('*.py')}
paths.update({*(ROOT/'analysis-engines/test').rglob('*.sol'),*(ROOT/'analysis-engines/test').rglob('*.json')})
if phase in['unit','qa','mcp']:paths.update({*(ROOT/'backend').rglob('*.go'),*(ROOT/'mcp/src').rglob('*.ts'),*(ROOT/'mcp/test').rglob('*.ts'),*(ROOT/'mcp/scripts').glob('contract-analysis-*.mjs'),ROOT/'backend/go.mod',ROOT/'backend/go.sum',ROOT/'mcp/package.json',ROOT/'mcp/package-lock.json'})
manifest=[{'path':p.relative_to(ROOT).as_posix(),'sha256':sha(p)}for p in sorted(paths)]
(out/'scenario-manifest.json').write_text(json.dumps(manifest,indent=2))
dependencies=[*(ROOT/'analysis-engines/node_modules').glob('solc-*/soljson.js'),ROOT/'analysis-engines/node_modules/@noble/hashes/sha3.js',ROOT/'analysis-engines/package-lock.json',ROOT.parent/'contract-verification-20261015/analysis-engines/bin/anvil']
dependency_manifest=[{'path':str(p),'sha256':sha(p)}for p in sorted(dependencies)]
(out/'dependency-identity.json').write_text(json.dumps(dependency_manifest,indent=2))
with zipfile.ZipFile(out/'scenario-sources.zip','x',compression=zipfile.ZIP_DEFLATED)as archive:
    for row in manifest:archive.write(ROOT/row['path'],row['path'])
env={**os.environ,'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+os.environ['PATH'],
 'GOPROXY':'off','GOMAXPROCS':'2','CONTRACT_TEST_IMAGE':image,'P2_TAC_IMAGE':image,'CONTRACT_PYTHON':str(ROOT/'scripts/contract-analysis/engine_python_bridge.py'),
 'CONTRACT_EXPECTED_RULE_VERSION':'funds-semantics-20','CONTRACT_CLOSURE_ENGINE_MODE':'frozen','CONTRACT_QA_OUT':str(out.relative_to(ROOT)),
 'ANVIL_BINARY':str(ROOT.parent/'contract-verification-20261015/analysis-engines/bin/anvil')}
memory=out/'memory-regression';memory.mkdir();(memory/'abi-reference-current.txt').write_bytes((ROOT/'acceptance/contract-analysis-memory/abi-reference-current.txt').read_bytes())
env.update(CONTRACT_MEMORY_BASE=str(memory.relative_to(ROOT))+'/',P2_MEMORY_OUT=str((memory/'byte-semantics').relative_to(ROOT))+'/',CONTRACT_STORAGE_BASE=str((out/'storage-regression').relative_to(ROOT))+'/')
gates=[]
def run(name,command,cwd=ROOT):
    started=time.monotonic();log=out/(name+'.log')
    with log.open('w')as f:completed=subprocess.run(command,cwd=cwd,env=env,stdout=f,stderr=subprocess.STDOUT,timeout=1800)
    assert all(sha(ROOT/r['path'])==r['sha256']for r in freeze['files'])
    assert all(sha(ROOT/r['path'])==r['sha256']for r in manifest)
    assert all(sha(pathlib.Path(r['path']))==r['sha256']for r in dependency_manifest)
    gates.append({'name':name,'image':image,'command':command,'working_directory':cwd.relative_to(ROOT).as_posix(),'exit_code':completed.returncode,
      'wall_seconds':time.monotonic()-started,'log':log.relative_to(BASE).as_posix(),'log_sha256':sha(log),'scenario_manifest':(out/'scenario-manifest.json').relative_to(BASE).as_posix(),
      'scenario_manifest_sha256':sha(out/'scenario-manifest.json'),'scenario_archive_sha256':sha(out/'scenario-sources.zip'),'dependency_identity_sha256':sha(out/'dependency-identity.json')})
    (out/'gates.json').write_text(json.dumps(gates,indent=2));print('P2_HEAP_GATE',name,completed.returncode,flush=True)
    if completed.returncode:print(log.read_text()[-5000:],flush=True);raise SystemExit(completed.returncode)
if phase=='unit':
    run('engine-unit',['node','--test','--test-concurrency=2',*[p.relative_to(ROOT/'analysis-engines').as_posix()for p in sorted((ROOT/'analysis-engines/test').glob('*.test.mjs'))]],ROOT/'analysis-engines')
    run('backend',['go','test','./internal/contractanalysis','./internal/api','-run','Contract|Submitted|CodeOnly|EngineTransport|Next|Investigation|ContainerExecutionResource','-count=1'],ROOT/'backend')
    run('mcp-check',['npm','run','check'],ROOT/'mcp')
elif phase=='heap-runtime':run('scalar-field-write-actual-EVM',['node','scripts/contract-analysis/p2_heap_runtime.mjs'])
elif phase=='scalar-runtime':run('scalar-actual-EVM',['node','scripts/contract-analysis/p2_heap_scalar_runtime.mjs'])
elif phase=='storage-runtime':run('storage-actual-EVM',['node','scripts/contract-analysis/p2_storage_runtime.mjs'])
elif phase=='effects-runtime':run('effects-actual-EVM',['node','scripts/contract-analysis/p2_storage_effects_runtime.mjs'])
elif phase=='business-runtime':run('real-business-actual-EVM',['node','scripts/contract-analysis/p2_heap_business_runtime.mjs'])
elif phase=='scalar-profiles':run('saved-fee-formula-vectors',['python3','scripts/contract-analysis/p2_heap_formula_profiles.py'])
elif phase=='decrement-runtime':run('prior-decrement-counterexample',['node','scripts/contract-analysis/p2_heap_decrement_counterexample.mjs'])
elif phase=='copy-runtime':run('storage-copy90-actual-EVM',['node','scripts/contract-analysis/p2_storage_copy_runtime.mjs'])
elif phase=='qa':run('qa-start',['python3','scripts/contract-analysis/p2_heap_qa.py'])
elif phase=='mcp':run('actual-mcp',['node','mcp/scripts/contract-analysis-heap-qa.mjs'])
elif phase=='legacy-runtime':
    run('source-bytes-runtime',['node','scripts/contract-analysis/p2_source_bytes_runtime.mjs'])
    run('source-byte-alias-runtime',['node','scripts/contract-analysis/p2_source_bytes_alias_runtime.mjs'])
    run('source-byte-consumers-runtime',['node','scripts/contract-analysis/p2_source_bytes_consumers_runtime.mjs'])
    run('creation-identity-runtime',['node','scripts/contract-analysis/p2_creation_identity_runtime.mjs'])
    run('tuple-field-runtime',['node','scripts/contract-analysis/p2_tuple_field_calls_runtime.mjs'])
    run('tac-model',['node','scripts/contract-analysis/p2_tac_memory_capture.mjs'])
    for name,script in[('byte-memory','p2_memory_runtime.mjs'),('tac-memory','p2_tac_memory_runtime.mjs'),('abi','p2_abi_runtime.mjs'),('permit','permit_runtime_qa.mjs'),('signature','closure_signature_runtime_qa.mjs'),('namespace','namespace_runtime_qa.mjs')]:run(name,['node','scripts/contract-analysis/'+script])
print('P2_HEAP_PHASE_PASS',phase,image,str(out),flush=True)
