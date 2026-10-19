"""Freeze one accepted recovery unit, preserving every prior attempt in place."""
import datetime,hashlib,json,pathlib,shutil,subprocess,tarfile,zipfile
from qa_support import dotenv
ROOT=pathlib.Path(__file__).resolve().parents[2];BASE=ROOT/'acceptance/contract-analysis-partial-recovery'
sha=lambda b:hashlib.sha256(b).hexdigest()
image=(BASE/'engine-image.txt').read_text().strip();suffix=image[7:23]
release=BASE/'release'/suffix;release.mkdir(parents=True,exist_ok=True)
gates={}
for path in sorted((BASE/'validation'/suffix).glob('*/gates.json')):
 for gate in json.loads(path.read_text()):gates[gate['name']]={**gate,'gate_record':str(path.relative_to(BASE))}
required=['engine-unit','backend','mcp-check','qa-start','actual-mcp','permit','signature','namespace']
assert set(required)<=set(gates)and all(gates[n]['image']==image and gates[n]['exit_code']==0 for n in required)
for gate in gates.values():assert sha((BASE/gate['log']).read_bytes())==gate['log_sha256']
mcp=BASE/('mcp-'+suffix);checks=json.loads((mcp/'checks.json').read_text())
assert checks and all(c['status']=='PASS'for c in checks)
database=json.loads((mcp/'database-audit-validation.json').read_text());assert database['status']=='PASS'and database['image']==image and len(database['jobs'])>=5
race=json.loads((BASE/'publication-race-image.json').read_text());race_result=json.loads((pathlib.Path(race['output'])/'result.json').read_text())
assert race['base_image']==image and race_result['status']=='PASS'and race_result['production_pids_unchanged']
runner_path=sorted((BASE/'runner-integration'/suffix).glob('*/result.json'))[-1]
runner=json.loads(runner_path.read_text());assert runner['status']=='PASS'and runner['candidate_image']==image
candidate=BASE/('candidate-15-'+suffix);runs=json.loads((candidate/'runs.json').read_text())
assert len(runs)==54 and all(r['image']==image and r['transport_status']=='completed'and r['provider_http']==0 and r['credits']==0 and r['container_execution']['scope']=='after_engine_exit_and_complete_output_forwarding'for r in runs)
regression=json.loads((candidate/'semantic-regression.json').read_text());assert regression['status']=='PASS'and len(regression['runs'])==54
quality=json.loads((candidate/'business-quality.json').read_text());assert len(quality['facts'])==192
for name in['formula-counterexample.json','formula-counterexample-submitted_source.json']:
 formula=json.loads((candidate/name).read_text());assert formula['status']=='PASS'and len(formula['vectors'])==72
usage=json.loads((BASE/'live-usage.json').read_text());assert usage['next_provider_reservation']==0 and usage['conservative_http_including_upper_bound']<=2000 and usage['p1_conservative_http']<=70
background=json.loads((BASE/'background-management-audit.json').read_text())
assert background['production_pids_unchanged']and all(v=='false'for v in background['effective_flags'].values())
freeze=json.loads((BASE/'frozen-engine'/suffix/'engine-source-freeze.json').read_text())
assert all(sha((ROOT/f['path']).read_bytes())==f['sha256']for f in freeze['files'])
names=set()
for args in[['git','ls-files','-z'],['git','ls-files','--others','--exclude-standard','-z']]:names.update(n for n in subprocess.check_output(args,cwd=ROOT).decode().split('\0')if n)
prefixes={'analysis-engines','backend','mcp','scripts','docs','openapi','frontend'}
root_files={'AGENTS.md','README.md','LICENSE','LICENSE.md','NOTICE','package.json','package-lock.json','pnpm-lock.yaml','pnpm-workspace.yaml'}
paths=sorted(n for n in names if(n.split('/')[0]in prefixes or n in root_files)and not {'node_modules','dist','bin','__pycache__'}.intersection(pathlib.PurePosixPath(n).parts)and not n.endswith(('.env','.pyc'))and(ROOT/n).is_file()and not(ROOT/n).is_symlink())
manifest=[{'path':n,'sha256':sha((ROOT/n).read_bytes()),'byte_size':(ROOT/n).stat().st_size}for n in paths]
source=release/'source.zip'
with zipfile.ZipFile(source,'x',compression=zipfile.ZIP_DEFLATED,compresslevel=9)as archive:
 for record in manifest:archive.write(ROOT/record['path'],record['path'])
with zipfile.ZipFile(source)as archive:
 assert sorted(archive.namelist())==paths and all(sha(archive.read(r['path']))==r['sha256']for r in manifest)
(release/'source-manifest.json').write_text(json.dumps({'image':image,'remote_head':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),'files':manifest},indent=2))
binary=release/'binaries';binary.mkdir()
for name in['api','worker']:shutil.copy2(BASE/'bin'/suffix/name,binary/name)
shutil.copytree(ROOT/'mcp/dist',binary/'mcp-dist')
binary_manifest=[{'path':str(p.relative_to(release)),'sha256':sha(p.read_bytes()),'byte_size':p.stat().st_size}for p in sorted(binary.rglob('*'))if p.is_file()]
identity=json.loads((BASE/'service-validation'/suffix/'binary-identity.json').read_text())
assert identity['image']==image and sha((binary/'api').read_bytes())==identity['api_sha256']and sha((binary/'worker').read_bytes())==identity['worker_sha256']and sha((binary/'mcp-dist/contract-analysis.js').read_bytes())==identity['mcp_dist_sha256']
(release/'binary-manifest.json').write_text(json.dumps(binary_manifest,indent=2))
(release/'image-inspect.json').write_text(subprocess.check_output(['docker','image','inspect',image],text=True))
before=json.loads((BASE/'service-validation'/suffix/'production-pids-before.json').read_text())
after={u:subprocess.check_output(['systemctl','--user','show',u,'-p','MainPID','--value'],text=True).strip()for u in before};assert before==after
(release/'production-pids-unchanged.json').write_text(json.dumps({'before':before,'after':after,'equal':True},indent=2))
inventory=[]
for path in sorted(BASE.rglob('*')):
 if path.is_file()and not path.is_relative_to(BASE/'release'):
  inventory.append({'path':str(path.relative_to(BASE)),'byte_size':path.stat().st_size,'sha256':sha(path.read_bytes())})
(release/'all-attempts-inventory.json').write_text(json.dumps(inventory,indent=2))
(release/'accepted-gates.json').write_text(json.dumps({'image':image,'gates':gates,'runner':str(runner_path.relative_to(BASE)),'publication_race':str(pathlib.Path(race['output']).relative_to(BASE)),'mcp_checks':len(checks)},indent=2))
roots=['candidate-15-'+suffix,'mcp-'+suffix,'frozen-engine/'+suffix,'service-validation/'+suffix,'validation/'+suffix,'runner-integration/'+suffix,'publication-race/'+suffix]
selected=[p for name in roots for p in(BASE/name).rglob('*')if p.is_file()]
selected.extend(p for p in BASE.glob('*.json')if p.is_file());selected.extend(p for p in release.rglob('*')if p.is_file())
secrets=[]
for path in[pathlib.Path('/home/euripides/projects/blockchain-platform/deploy/.env'),pathlib.Path('/home/euripides/.config/blockchain-platform/contract-partial-recovery-qa-20261017.env')]:
 for key,value in dotenv(path).items():
  label=key.upper();credential=any(v in label for v in['PASSWORD','SECRET','API_KEY','PRIVATE_KEY','SIGNING_KEY','ENCRYPTION_KEY','ACCESS_KEY','CREDENTIAL'])or label.endswith('TOKEN')
  if not label.endswith('_REF')and credential and len(value)>=12:secrets.append(value.encode())
for path in[*selected,*(ROOT/r['path']for r in manifest)]:
 data=path.read_bytes();assert not any(secret in data for secret in secrets),'Private credential detected in '+str(path.relative_to(ROOT))
member_manifest=release/'evidence-members-manifest.json'
member_manifest.write_text(json.dumps([{'path':str(p.relative_to(BASE)),'byte_size':p.stat().st_size,'sha256':sha(p.read_bytes())}for p in sorted(set(selected))],indent=2))
selected.append(member_manifest)
evidence=release/'evidence.tar.gz'
with tarfile.open(evidence,'x:gz',compresslevel=6)as archive:
 for path in sorted(set(selected)):archive.add(path,arcname=str(path.relative_to(BASE)),recursive=False)
summary={'image':image,'rule_version':'funds-semantics-15','frozen_at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),
 'status':'P2_01_functional_validation_pass_with_budget_deviation_not_release_accepted','general_business_depth':'NOT_ACCEPTED','production_deployed':False,
 'release_decision':'NO_GO','all_external_http_budget':'NOT_VERIFIED','next_material_request_reservation':0,
 'engine_files':len(freeze['files']),'source_files':len(manifest),'archived_evidence_files':len(set(selected)),
 'mcp_checks':len(checks),'real_material_context_runs':len(runs),'semantic_regressions':regression['regression_candidates'],
 'archives':{p.name:{'byte_size':p.stat().st_size,'sha256':sha(p.read_bytes())}for p in[source,evidence]},
 'retained_all_attempts_inventory_sha256':sha((release/'all-attempts-inventory.json').read_bytes()),'production_pids_unchanged':True,'credential_scan':'PASS',
 'business_quality_scope':'regression only; original blind materials exposed; full permissions/economics and TVM/SBF remain outside accepted scope',
 'budget_material_allocation':'historical 34 vs 18 deviation retained; analysis tasks have zero RPC but inherited dRPC background HTTP occurred',
 'background_management_budget':{k:background[k]for k in['budget_status','completed_sync_logical_api_calls','p2_completed_sync_logical_api_calls','disabled_at_utc','management_credits_and_usd']}}
(release/'publication-summary.json').write_text(json.dumps(summary,indent=2));print(json.dumps(summary,indent=2),flush=True)
