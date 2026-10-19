"""Freeze one storage-reference functional unit without claiming general acceptance.

No input or existing candidate is moved or removed. The release is created on
the data volume and binds successful gates, explicit delta review and failures.
"""
import datetime,hashlib,json,os,pathlib,re,shutil,subprocess,tarfile,zipfile
from qa_support import dotenv

ROOT=pathlib.Path(__file__).resolve().parents[2]
BASE=ROOT/'acceptance/contract-analysis-storage'
image=(BASE/'engine-image.txt').read_text().strip()
assert re.fullmatch(r'sha256:[a-f0-9]{64}',image)
suffix=image[7:23]
candidate=BASE/'corpus'/('candidate-17-'+suffix)
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
required=['engine-unit','backend','mcp-check','qa-start','source-bytes-runtime','tac-model','byte-memory','tac-memory','abi','permit','signature','namespace','storage-actual-EVM','effects-actual-EVM','actual-mcp']
assert set(required)<=gates.keys(),set(required)-gates.keys()
assert all(digest(BASE/g['log'])==g['log_sha256']for g in gates.values())
unit=(BASE/gates['engine-unit']['log']).read_text()
assert 'tests 241' in unit and 'pass 241' in unit and 'skipped 0' in unit
freeze=load(BASE/'frozen-engine'/suffix/'engine-source-freeze.json')
assert freeze['image']==image and len(freeze['files'])==64
assert all(digest(ROOT/f['path'])==f['sha256']for f in freeze['files'])
mcp=pathlib.Path((BASE/'mcp-current.txt').read_text().strip())
assert mcp.is_relative_to(BASE/'mcp'/suffix)
checks=load(mcp/'checks.json');mcp_result=load(mcp/'result.json');database=load(mcp/'database-audit-validation.json')
assert len(checks)==22 and all(c['status']=='PASS'for c in checks)
assert mcp_result['status']=='PASS'and mcp_result['image']==image and mcp_result['external_provider_requests']==0
assert database['status']=='PASS'and database['image']==image and len(database['jobs'])==3 and len(database['artifacts'])>46
runs=load(candidate/'runs.json')
assert len(runs)==54
for run in runs:
    assert run['image']==image and run['transport_status']=='completed'and run['provider_http']==0 and run['credits']==0
    assert run['container_execution']['scope']=='after_engine_exit_and_complete_output_forwarding'and run['container_execution']['engine_exit_code']==0
    for name,sha in run['files'].items():assert digest(candidate/name)==sha
strict=load(candidate/'semantic-regression.json');review=load(candidate/'semantic-review.json')
assert strict['image']==review['image']==image and len(strict['runs'])==162
assert review['status']=='PASS'and not review['unreviewed_rows']
assert review['raw_comparison_sha256']==digest(candidate/'semantic-regression.json')
assert review['verifier_sha256']==digest(ROOT/'scripts/contract-analysis/p2_storage_semantic_review.py')
selfcheck=load(candidate/'semantic-review-selfcheck.json')
assert selfcheck['status']=='PASS'and selfcheck['cases']==5
assert selfcheck['reviewer_sha256']==review['verifier_sha256']
assert selfcheck['verifier_sha256']==digest(ROOT/'scripts/contract-analysis/p2_storage_review_selfcheck.py')
assert review['source_condition_reference_sha256']==digest(ROOT/'scripts/contract-analysis/p2_storage_condition_reference.mjs')
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
for group,count,gate,fixture,verifier in [
    ('source-execution',224,'storage-actual-EVM','storage-flow.mjs','p2_storage_runtime.mjs'),
    ('effects-execution',288,'effects-actual-EVM','storage-effects.mjs','p2_storage_effects_runtime.mjs')]:
    options=[]
    for path in sorted((BASE/group).glob('*/results.json')):
        value=load(path)
        if value.get('image')==image and value.get('status')=='PASS'and value.get('cases')==count:options.append((path,value))
    assert options,group
    path,value=options[-1]
    assert value['complete_scenario_set']and value['external_http']==0 and len(value['records'])==count
    assert value['fixture_sha256']==digest(ROOT/'analysis-engines/test/fixtures'/fixture)
    assert value['verifier_sha256']==digest(ROOT/'scripts/contract-analysis'/verifier)
    assert value['solver_verifier_sha256']==digest(ROOT/'scripts/contract-analysis/p2_storage_eval.py')
    assert all(r['status']=='PASS'for r in value['records'])
    assert str(path.parent)in(BASE/gates[gate]['log']).read_text()
    runtime_dirs.append(path.parent)
memory=pathlib.Path(BASE/gates['source-bytes-runtime']['log']).parent/'memory-regression'
for group,count,gate in [('source-execution',176,'source-bytes-runtime'),('tac-execution',33,'tac-memory'),('abi-execution',137,'abi'),('byte-semantics',46,'byte-memory')]:
    options=[]
    # The original gate supplied a URL without a trailing slash to the byte
    # script. Its unchanged result and traces are in memory-regression itself.
    candidates=[memory/'results.json']if group=='byte-semantics'else sorted((memory/group).glob('*/results.json'))
    for path in candidates:
        value=load(path)
        if value.get('status')=='PASS'and value.get('cases')==count and value.get('engine_digest',image)==image:options.append((path,value))
    assert options,group
    path,value=options[-1]
    if group=='byte-semantics':
        assert value['memory_module_sha256']==digest(ROOT/'analysis-engines/ir/memory.mjs')
        assert value['runtime_script_sha256']==digest(ROOT/'scripts/contract-analysis/p2_memory_runtime.mjs')
        assert len(value['records'])==46 and all(r['status']=='PASS'and digest(path.parent/r['trace_file'])==r['trace_sha256']for r in value['records'])
    elif group=='abi-execution':assert value['abi_module_sha256']==digest(ROOT/'analysis-engines/ir/abi.mjs')
    elif group=='source-execution':
        assert value['verifier_sha256']==digest(ROOT/'scripts/contract-analysis/p2_source_bytes_runtime.mjs')
        assert value['source_result_sha256']==digest(path.parent/'result.json')
    assert str(path.parent)in(BASE/gates[gate]['log']).read_text()or group=='byte-semantics'
    runtime_dirs.append(path.parent)
tac=sorted((memory/'tac-runs').glob('*/result.json'))[-1]
assert load(tac)['snapshot']['engine_digest']==image
runtime_dirs.append(tac.parent)
for gate in gates.values():
    manifest=BASE/gate['scenario_manifest']
    assert digest(manifest)==gate['scenario_manifest_sha256']
    archive=manifest.parent/'scenario-sources.zip'
    assert digest(archive)==gate['scenario_archive_sha256']
    rows=load(manifest)
    with zipfile.ZipFile(archive)as z:
        assert sorted(z.namelist())==sorted(r['path']for r in rows)
        assert all(hashlib.sha256(z.read(r['path'])).hexdigest()==r['sha256']for r in rows)
service=pathlib.Path((BASE/'service-current.txt').read_text().strip())
identity=load(service/'binary-identity.json')
bindir=pathlib.Path(identity['binary_directory'])
assert service.is_relative_to(BASE/'service-validation'/suffix)and bindir.is_relative_to(BASE/'bin'/suffix)
assert all(digest(ROOT/r['path'])==r['sha256']for r in identity['backend_sources'])
assert database['service_identity']==identity
prior_memory=ROOT/'acceptance/contract-analysis-memory'
prior_summary=prior_memory/'release/1a7a80312d9f72e4/publication-summary.json'
baseline_abi={'scope':'Previously accepted independent ABI reference reused; this is not an additional test execution',
    'baseline_image':load(prior_summary)['image'],'publication_summary_sha256':digest(prior_summary),
    'current_abi_module_sha256':digest(ROOT/'analysis-engines/ir/abi.mjs'),'current_real_ABI_regression_cases':137}
old_abi=next(r for r in load(prior_memory/'frozen-engine/1a7a80312d9f72e4/engine-source-freeze.json')['files']if r['path']=='analysis-engines/ir/abi.mjs')
assert old_abi['sha256']==baseline_abi['current_abi_module_sha256']
background=load(ROOT/'acceptance/contract-analysis-partial-recovery/background-management-audit.json')
assert background['production_pids_unchanged']and all(v=='false'for v in background['effective_flags'].values())

release_root=BASE/'release'
data=pathlib.Path('/data/backups/codex-contract-general-20261017/storage-reference-release')
data.mkdir(parents=True,exist_ok=True)
if not release_root.exists():release_root.symlink_to(data,target_is_directory=True)
assert release_root.is_symlink()and release_root.resolve()==data.resolve()
release_id=suffix+'-'+identity['api_sha256'][:12]
release=release_root/release_id
assert not release.exists(),'An existing release must not be overwritten'
release.mkdir()
print('P2_STORAGE_FREEZE_START',image,flush=True)
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
for name in['api','worker']:shutil.copy2(bindir/name,binary/name)
shutil.copytree(ROOT/'mcp/dist',binary/'mcp-dist')
binary_manifest=[record(p,release)for p in sorted(binary.rglob('*'))if p.is_file()]
assert identity['production_pids_unchanged']
assert identity['image']==image and digest(binary/'api')==identity['api_sha256']and digest(binary/'worker')==identity['worker_sha256']and digest(binary/'mcp-dist/contract-analysis.js')==identity['mcp_dist_sha256']
(release/'binary-manifest.json').write_text(json.dumps(binary_manifest,indent=2))
(release/'image-inspect.json').write_text(subprocess.check_output(['docker','image','inspect',image],text=True))
before=load(service/'production-pids-before.json')
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
    if len(inventory)%100==0:print('P2_STORAGE_INVENTORY',len(inventory),flush=True)
(release/'all-attempts-inventory.json').write_text(json.dumps(sorted(inventory,key=lambda r:r['path']),indent=2))
(release/'accepted-gates.json').write_text(json.dumps({'image':image,'gates':gates,'mcp_directory':mcp.relative_to(BASE).as_posix(),'mcp_checks':len(checks),
    'runtime_directories':[p.relative_to(BASE).as_posix()for p in runtime_dirs],
    'scenario_manifests':sorted({g['scenario_manifest_sha256']for g in gates.values()}),
    'scenario_scope':'Every gate retains and verifies its complete scenario sources ZIP and per-file SHA manifest. Failed first MCP and original backend attempt remain in the evidence.'},indent=2))
shutil.copy2(ROOT/'scripts/contract-analysis/p2_storage_quality_notes.json',release/'quality-notes-overlay.json')
shutil.copy2(ROOT/'acceptance/contract-analysis-partial-recovery/background-management-audit.json',release/'historical-background-management-audit.json')
(release/'reused-baseline-abi.json').write_text(json.dumps(baseline_abi,indent=2))
selected={BASE/row['path']for row in inventory}
selected.update(p for p in release.rglob('*')if p.is_file()and p!=source)
secrets=[]
for path in[pathlib.Path('/home/euripides/projects/blockchain-platform/deploy/.env'),pathlib.Path('/home/euripides/.config/blockchain-platform/contract-storage-qa-20261017.env')]:
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
print('P2_STORAGE_ARCHIVE_START',len(selected),flush=True)
with tarfile.open(evidence,'x:gz',compresslevel=6)as archive:
    for path in sorted(selected):archive.add(path,arcname=path.relative_to(BASE).as_posix(),recursive=False)
summary={'image':image,'release_id':release_id,'rule_version':'funds-semantics-17','frozen_at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),
    'status':'P2_03_functional_unit_validated_general_acceptance_incomplete','general_business_depth':'NOT_ACCEPTED','production_deployed':False,'release_decision':'NO_GO',
    'all_external_http_budget':'NOT_VERIFIED','next_material_request_reservation':0,'new_provider_or_public_source_requests':0,'fresh_blind_samples':0,
    'engine_files':len(freeze['files']),'source_files':len(manifest),'archived_evidence_files':len(selected),'mcp_checks':len(checks),'real_material_context_runs':len(runs),'strict_baselines':3,'strict_comparisons':162,'review_negative_checks':5,'business_fact_grades':{channel:{status:sum(f['channel']==channel and f['status']==status for f in quality['facts'])for status in sorted({f['status']for f in quality['facts']if f['channel']==channel})}for channel in ['source','submitted_source','hidden_source']},
    'strict_comparison_status':strict['status'],'strict_delta_groups':len(strict['regression_candidates']),'reviewed_delta_rows':len(review['reviewed_rows']),'unreviewed_delta_rows':0,
    'storage_execution_cases':224,'storage_effects_execution_cases':288,'source_execution_cases':176,'memory_execution_cases':46,'TAC_execution_cases':33,'ABI_execution_cases':137,
    'archives':{p.name:{'byte_size':p.stat().st_size,'sha256':digest(p)}for p in[source,evidence]},
    'retained_all_attempts_inventory_sha256':digest(release/'all-attempts-inventory.json'),'production_pids_unchanged':True,'credential_scan':'PASS',
    'business_quality_scope':'Previously exposed real materials; all partial and hidden-source gaps remain in the denominator. No full permissions, economics, all-path or other-VM acceptance.',
    'budget_scope':'Actual billed Provider1968 HTTP plus17 public and source upper bound3 do not cover inherited management HTTP;2691 historical management logical attempts are not wire-request counts. Original2000 limit and34-vs18 deviation retained.'}
(release/'publication-summary.json').write_text(json.dumps(summary,indent=2))
(BASE/'release-current.txt').write_text(str(release)+'\n')
print('P2_STORAGE_FROZEN',json.dumps(summary),flush=True)
