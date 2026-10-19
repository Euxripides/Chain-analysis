"""Freeze one memory/ABI functional unit without claiming general acceptance.

No input or existing candidate is moved or removed. The release is created on
the data volume and binds successful gates, explicit delta review and failures.
"""
import datetime,hashlib,json,os,pathlib,re,shutil,subprocess,tarfile,zipfile
from qa_support import dotenv

ROOT=pathlib.Path(__file__).resolve().parents[2]
BASE=ROOT/'acceptance/contract-analysis-memory'
image=(BASE/'engine-image.txt').read_text().strip()
assert re.fullmatch(r'sha256:[a-f0-9]{64}',image)
suffix=image[7:23]
candidate=BASE/'corpus'/('candidate-16-'+suffix)
load=lambda p:json.loads(p.read_text())

def digest(path):
    h=hashlib.sha256()
    with path.open('rb')as f:
        for block in iter(lambda:f.read(1<<20),b''):h.update(block)
    return h.hexdigest()

def record(path,base):
    return{'path':path.relative_to(base).as_posix(),'byte_size':path.stat().st_size,'sha256':digest(path)}

gates={}
for path in sorted((BASE/'validation'/suffix).glob('*/gates.json')):
    for gate in load(path):
        if gate['image']==image and gate['exit_code']==0:gates[gate['name']]={**gate,'gate_record':path.relative_to(BASE).as_posix()}
required=['engine-unit','backend','mcp-check','qa-start','source-bytes-runtime','tac-model','byte-memory','tac-memory','abi','permit','signature','namespace','actual-mcp']
assert set(required)<=gates.keys(),set(required)-gates.keys()
assert all(digest(BASE/g['log'])==g['log_sha256']for g in gates.values())
unit=(BASE/gates['engine-unit']['log']).read_text()
assert 'tests 219' in unit and 'pass 219' in unit and 'skipped 0' in unit
freeze=load(BASE/'frozen-engine'/suffix/'engine-source-freeze.json')
assert freeze['image']==image and len(freeze['files'])==63
assert all(digest(ROOT/f['path'])==f['sha256']for f in freeze['files'])
mcp=pathlib.Path((BASE/'mcp-current.txt').read_text().strip())
assert mcp.is_relative_to(BASE/'mcp'/suffix)
checks=load(mcp/'checks.json');mcp_result=load(mcp/'result.json');database=load(mcp/'database-audit-validation.json')
assert len(checks)==21 and all(c['status']=='PASS'for c in checks)
assert mcp_result['status']=='PASS'and mcp_result['image']==image and mcp_result['external_provider_requests']==0
assert database['status']=='PASS'and database['image']==image and len(database['jobs'])==3 and len(database['artifacts'])==46
runs=load(candidate/'runs.json')
assert len(runs)==54
for run in runs:
    assert run['image']==image and run['transport_status']=='completed'and run['provider_http']==0 and run['credits']==0
    assert run['container_execution']['scope']=='after_engine_exit_and_complete_output_forwarding'and run['container_execution']['engine_exit_code']==0
    for name,sha in run['files'].items():assert digest(candidate/name)==sha
strict=load(candidate/'semantic-regression.json');review=load(candidate/'semantic-review.json')
assert strict['image']==review['image']==image and len(strict['runs'])==108
assert review['status']=='PASS'and not review['unreviewed_rows']
assert review['raw_comparison_sha256']==digest(candidate/'semantic-regression.json')
assert review['verifier_sha256']==digest(ROOT/'scripts/contract-analysis/p2_memory_semantic_review.py')
assert review['source_condition_reference_sha256']==digest(ROOT/'scripts/contract-analysis/p2_memory_condition_reference.mjs')
quality=load(candidate/'business-quality.json')
assert len(quality['facts'])==192 and not any(f['missing_evidence_ids']for f in quality['facts'])
assert not quality['full_authorization_proved']and not quality['full_protocol_economics_proved']
assert sum(f['channel']=='hidden_source'and f['status']=='not_recovered'for f in quality['facts'])==64
for channel in['source','submitted_source']:
    tail=''if channel=='source'else'-'+channel
    for stem,count in[('formula-counterexample',72),('token-formula-counterexample',24)]:
        formula=load(candidate/(stem+tail+'.json'))
        assert formula['status']=='PASS'and len(formula['vectors'])==count
        assert formula['result_sha256']==digest(candidate/('safe-singleton-'+channel+'-output.json'))

runtime_dirs=[]
for group,count,gate in[('source-execution',176,'source-bytes-runtime'),('tac-execution',33,'tac-memory'),('abi-execution',137,'abi'),('byte-semantics',46,'byte-memory')]:
    options=[]
    for p in sorted((BASE/group).glob('*/results.json')):
        value=load(p)
        if value.get('status')=='PASS'and value.get('cases')==count and value.get('engine_digest',image)==image:options.append((p,value))
    assert options,group
    path,value=options[-1]
    if group=='byte-semantics':
        assert value['memory_module_sha256']==digest(ROOT/'analysis-engines/ir/memory.mjs')
        assert value['runtime_script_sha256']==digest(ROOT/'scripts/contract-analysis/p2_memory_runtime.mjs')
    elif group=='abi-execution':assert value['abi_module_sha256']==digest(ROOT/'analysis-engines/ir/abi.mjs')
    elif group=='source-execution':
        assert value['verifier_sha256']==digest(ROOT/'scripts/contract-analysis/p2_source_bytes_runtime.mjs')
        assert value['source_result_sha256']==digest(path.parent/'result.json')
    log=(BASE/gates[gate]['log']).read_text()
    assert str(path.parent)in log or group=='byte-semantics'
    runtime_dirs.append(path.parent)
tac=sorted((BASE/'tac-runs').glob('*/result.json'))[-1]
assert load(tac)['snapshot']['engine_digest']==image
runtime_dirs.append(tac.parent)
reference=pathlib.Path((BASE/'abi-reference-current.txt').read_text().strip())
abi_diff=sorted(reference.glob('differential-runs/*/result.json'))[-1]
assert load(abi_diff)['status']=='PASS'
background=load(ROOT/'acceptance/contract-analysis-partial-recovery/background-management-audit.json')
assert background['production_pids_unchanged']and all(v=='false'for v in background['effective_flags'].values())

release_root=BASE/'release'
data=pathlib.Path('/data/backups/codex-contract-general-20261017/memory-abi-release')
data.mkdir(parents=True,exist_ok=True)
if not release_root.exists():release_root.symlink_to(data,target_is_directory=True)
assert release_root.is_symlink()and release_root.resolve()==data.resolve()
release=release_root/suffix
assert not release.exists(),'An existing release must not be overwritten'
release.mkdir()
print('P2_MEMORY_FREEZE_START',image,flush=True)
names=set()
for command in[['git','ls-files','-z'],['git','ls-files','--others','--exclude-standard','-z']]:
    names.update(n for n in subprocess.check_output(command,cwd=ROOT).decode().split('\0')if n)
prefixes={'analysis-engines','backend','mcp','scripts','docs','openapi','frontend'}
root_files={'AGENTS.md','README.md','LICENSE','LICENSE.md','NOTICE','package.json','package-lock.json','pnpm-lock.yaml','pnpm-workspace.yaml'}
paths=sorted(n for n in names if(n.split('/')[0]in prefixes or n in root_files)and not{'node_modules','dist','bin','__pycache__'}.intersection(pathlib.PurePosixPath(n).parts)and not n.endswith(('.env','.pyc'))and(ROOT/n).is_file()and not(ROOT/n).is_symlink())
manifest=[record(ROOT/name,ROOT)for name in paths]
source=release/'source.zip'
with zipfile.ZipFile(source,'x',compression=zipfile.ZIP_DEFLATED,compresslevel=9)as archive:
    for row in manifest:archive.write(ROOT/row['path'],row['path'])
with zipfile.ZipFile(source)as archive:
    assert sorted(archive.namelist())==paths
    for row in manifest:assert hashlib.sha256(archive.read(row['path'])).hexdigest()==row['sha256']
(release/'source-manifest.json').write_text(json.dumps({'image':image,'remote_head':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),'files':manifest},indent=2))
binary=release/'binaries';binary.mkdir()
for name in['api','worker']:shutil.copy2(BASE/'bin'/suffix/name,binary/name)
shutil.copytree(ROOT/'mcp/dist',binary/'mcp-dist')
binary_manifest=[record(p,release)for p in sorted(binary.rglob('*'))if p.is_file()]
identity=load(BASE/'service-validation'/suffix/'binary-identity.json')
assert identity['image']==image and digest(binary/'api')==identity['api_sha256']and digest(binary/'worker')==identity['worker_sha256']and digest(binary/'mcp-dist/contract-analysis.js')==identity['mcp_dist_sha256']
(release/'binary-manifest.json').write_text(json.dumps(binary_manifest,indent=2))
(release/'image-inspect.json').write_text(subprocess.check_output(['docker','image','inspect',image],text=True))
before=load(BASE/'service-validation'/suffix/'production-pids-before.json')
after={u:subprocess.check_output(['systemctl','--user','show',u,'-p','MainPID','--value'],text=True).strip()for u in before}
assert before==after
(release/'production-pids-unchanged.json').write_text(json.dumps({'before':before,'after':after,'equal':True},indent=2))
inventory=[];seen=set()
for current,dirs,files in os.walk(BASE,followlinks=True):
    folder=pathlib.Path(current)
    if folder==BASE:dirs[:]=[d for d in dirs if d!='release']
    resolved=folder.resolve();assert resolved not in seen,'Evidence directory cycle';seen.add(resolved)
    for name in sorted(files):
        path=folder/name
        assert not path.is_symlink(),'Unexpected evidence file link'
        inventory.append(record(path,BASE))
    if len(inventory)%100==0:print('P2_MEMORY_INVENTORY',len(inventory),flush=True)
(release/'all-attempts-inventory.json').write_text(json.dumps(sorted(inventory,key=lambda r:r['path']),indent=2))
(release/'accepted-gates.json').write_text(json.dumps({'image':image,'gates':gates,'mcp_directory':mcp.relative_to(BASE).as_posix(),'mcp_checks':len(checks),
    'runtime_directories':[p.relative_to(BASE).as_posix()for p in runtime_dirs],
    'scenario_fingerprints':sorted({g['scenario_sha256']for g in gates.values()}),
    'scenario_scope':'Each gate retains its original aggregate scenario fingerprint. Later changes were in independent review helpers; used engine and execution fixtures were unchanged.'},indent=2))
shutil.copy2(ROOT/'scripts/contract-analysis/p2_memory_quality_notes.json',release/'quality-notes-overlay.json')
shutil.copy2(ROOT/'acceptance/contract-analysis-partial-recovery/background-management-audit.json',release/'historical-background-management-audit.json')
roots=[candidate,mcp,BASE/'frozen-engine'/suffix,BASE/'service-validation'/suffix,BASE/'validation'/suffix,BASE/'rejected-candidates',BASE/'business-review'/suffix,reference,*runtime_dirs]
selected=set(p for folder in roots for p in folder.rglob('*')if p.is_file()and not p.is_symlink())
selected.update(p for p in BASE.glob('*.json')if p.is_file())
selected.update(p for p in release.rglob('*')if p.is_file()and p!=source)
secrets=[]
for path in[pathlib.Path('/home/euripides/projects/blockchain-platform/deploy/.env'),pathlib.Path('/home/euripides/.config/blockchain-platform/contract-memory-qa-20261017.env')]:
    for key,value in dotenv(path).items():
        label=key.upper();credential=any(v in label for v in['PASSWORD','SECRET','API_KEY','PRIVATE_KEY','SIGNING_KEY','ENCRYPTION_KEY','ACCESS_KEY','CREDENTIAL'])or label.endswith('TOKEN')
        if not label.endswith('_REF')and credential and len(value)>=12:secrets.append(value.encode())
overlap=max(map(len,secrets),default=1)-1
for path in[*selected,*(ROOT/r['path']for r in manifest)]:
    with path.open('rb')as f:
        tail=b''
        for block in iter(lambda:f.read(1<<20),b''):
            data_block=tail+block
            assert not any(secret in data_block for secret in secrets),'Private credential detected in '+str(path.relative_to(ROOT))
            tail=data_block[-overlap:]if overlap else b''
member_manifest=release/'evidence-members-manifest.json'
member_manifest.write_text(json.dumps([record(p,BASE)for p in sorted(selected)],indent=2));selected.add(member_manifest)
evidence=release/'evidence.tar.gz'
print('P2_MEMORY_ARCHIVE_START',len(selected),flush=True)
with tarfile.open(evidence,'x:gz',compresslevel=6)as archive:
    for path in sorted(selected):archive.add(path,arcname=path.relative_to(BASE).as_posix(),recursive=False)
summary={'image':image,'rule_version':'funds-semantics-16','frozen_at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),
    'status':'P2_02_functional_unit_validated_general_acceptance_incomplete','general_business_depth':'NOT_ACCEPTED','production_deployed':False,'release_decision':'NO_GO',
    'all_external_http_budget':'NOT_VERIFIED','next_material_request_reservation':0,'new_provider_or_public_source_requests':0,'fresh_blind_samples':0,
    'engine_files':len(freeze['files']),'source_files':len(manifest),'archived_evidence_files':len(selected),'mcp_checks':len(checks),'real_material_context_runs':len(runs),
    'strict_comparison_status':strict['status'],'strict_delta_groups':len(strict['regression_candidates']),'reviewed_delta_rows':len(review['reviewed_rows']),'unreviewed_delta_rows':0,
    'source_execution_cases':176,'memory_execution_cases':46,'TAC_execution_cases':33,'ABI_execution_cases':137,
    'archives':{p.name:{'byte_size':p.stat().st_size,'sha256':digest(p)}for p in[source,evidence]},
    'retained_all_attempts_inventory_sha256':digest(release/'all-attempts-inventory.json'),'production_pids_unchanged':True,'credential_scan':'PASS',
    'business_quality_scope':'Previously exposed real materials; all partial and hidden-source gaps remain in the denominator. No full permissions, economics, all-path or other-VM acceptance.',
    'budget_scope':'Actual billed Provider1968 HTTP plus17 public and source upper bound3 do not cover inherited management HTTP;2691 historical management logical attempts are not wire-request counts. Original2000 limit and34-vs18 deviation retained.'}
(release/'publication-summary.json').write_text(json.dumps(summary,indent=2))
print('P2_MEMORY_FROZEN',json.dumps(summary),flush=True)
