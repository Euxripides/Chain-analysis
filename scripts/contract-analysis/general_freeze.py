"""Freeze reviewable P1 source, binaries and evidence; never deploy or commit."""
import datetime,hashlib,json,os,pathlib,shutil,subprocess,tarfile,zipfile
from qa_support import dotenv
ROOT=pathlib.Path(__file__).resolve().parents[2];BASE=ROOT/'acceptance/contract-analysis-general'
sha=lambda data:hashlib.sha256(data).hexdigest()
image=(BASE/'engine-image.txt').read_text().strip();suffix=image[7:23]
release=BASE/'release'/suffix;release.mkdir(parents=True,exist_ok=True)
validation=sorted((BASE/'validation'/suffix).glob('*/gates.json'))[-1];gates=json.loads(validation.read_text())
required=['engine-unit','backend','mcp-check','qa-start','mcp-offline','mcp-large','mcp-compressed','api','openapi-discovery']
assert set(required+['mcp-audit','database-audit'])<=set(g['name']for g in gates)and all(g['image']==image and g['exit_code']==0 for g in gates)
mcp=BASE/('mcp-'+suffix);assert all(c['status']=='PASS'for c in json.loads((mcp/'checks.json').read_text()))
assert json.loads((mcp/'database-audit-validation.json').read_text())['status']=='PASS'
assert all(g['status']=='PASS'and g['image']==image for g in json.loads((BASE/('runtime-'+suffix)/'gates.json').read_text()).values())
candidate=BASE/('candidate-14-'+suffix);runs=json.loads((candidate/'runs.json').read_text());assert len(runs)==54
quality=json.loads((candidate/'business-quality.json').read_text());assert len(quality['facts'])==192
assert json.loads((candidate/'formula-counterexample.json').read_text())['status']=='PASS'
usage=json.loads((BASE/'live-usage.json').read_text());assert usage['next_provider_reservation']==0
assert usage['conservative_http_including_upper_bound']<=1999 and usage['p1_conservative_http']<=70
freeze=json.loads((BASE/'frozen-engine'/suffix/'engine-source-freeze.json').read_text())
assert all(sha((ROOT/f['path']).read_bytes())==f['sha256']for f in freeze['files'])
names=set()
for args in [['git','ls-files','-z'],['git','ls-files','--others','--exclude-standard','-z']]:
 names.update(n for n in subprocess.check_output(args,cwd=ROOT).decode().split('\0')if n)
prefixes={'analysis-engines','backend','mcp','scripts','docs','openapi','frontend'}
root_files={'AGENTS.md','README.md','LICENSE','LICENSE.md','NOTICE','package.json','package-lock.json','pnpm-lock.yaml','pnpm-workspace.yaml'}
paths=sorted(name for name in names if(name.split('/')[0]in prefixes or name in root_files)and not {'node_modules','dist','bin'}.intersection(pathlib.PurePosixPath(name).parts)and not name.endswith('.env')and(ROOT/name).is_file()and not(ROOT/name).is_symlink())
manifest=[{'path':name,'sha256':sha((ROOT/name).read_bytes()),'byte_size':(ROOT/name).stat().st_size}for name in paths]
source_zip=release/'source.zip'
if not source_zip.exists():
 with zipfile.ZipFile(source_zip,'x',compression=zipfile.ZIP_DEFLATED,compresslevel=9)as archive:
  for f in manifest:archive.write(ROOT/f['path'],f['path'])
with zipfile.ZipFile(source_zip)as archive:
 assert sorted(archive.namelist())==paths
 assert all(sha(archive.read(f['path']))==f['sha256']for f in manifest)
(release/'source-manifest.json').write_text(json.dumps({'image':image,'remote_head':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),'files':manifest},indent=2))
binary=release/'binaries';binary.mkdir(exist_ok=True)
for name in ['api','worker']:shutil.copy2(ROOT/'.work/general/bin'/suffix/name,binary/name)
shutil.copytree(ROOT/'mcp/dist',binary/'mcp-dist',dirs_exist_ok=True)
(release/'binary-manifest.json').write_text(json.dumps([{'path':str(p.relative_to(release)),'sha256':sha(p.read_bytes()),'byte_size':p.stat().st_size}for p in sorted(binary.rglob('*'))if p.is_file()],indent=2))
inspect=json.loads(subprocess.check_output(['docker','image','inspect',image],text=True))
(release/'image-inspect.json').write_text(json.dumps(inspect,indent=2))
services=BASE/'service-validation'/suffix
before=json.loads((services/'production-pids-before.json').read_text())
after={u:subprocess.check_output(['systemctl','--user','show',u,'-p','MainPID','--value'],text=True).strip()for u in before};assert before==after
(release/'production-pids-unchanged.json').write_text(json.dumps({'before':before,'after':after,'equal':True},indent=2))
# Save the complete inventory of all attempts without deleting or overwriting
# any earlier run. The review archive includes baseline, first assessment and
# final evidence; other attempts remain at the inventoried original paths.
inventory=[]
for p in sorted(BASE.rglob('*')):
 if p.is_file()and not p.is_relative_to(BASE/'release'):
  inventory.append({'path':str(p.relative_to(BASE)),'byte_size':p.stat().st_size,'sha256':sha(p.read_bytes())})
(release/'all-attempts-inventory.json').write_text(json.dumps(inventory,indent=2))
secrets=[]
for path in [pathlib.Path('/home/euripides/projects/blockchain-platform/deploy/.env'),pathlib.Path('/home/euripides/.config/blockchain-platform/contract-general-input-qa-20261017.env')]:
 for key,value in dotenv(path).items():
  # References and TOKEN_LOGO-style metadata are public configuration, whereas
  # actual password/key values and fields ending in TOKEN hold credentials.
  label=key.upper();credential=any(marker in label for marker in ['PASSWORD','SECRET','API_KEY','PRIVATE_KEY','SIGNING_KEY','ENCRYPTION_KEY','ACCESS_KEY','CREDENTIAL'])or label.endswith('TOKEN')
  if not label.endswith('_REF')and credential and len(value)>=12:secrets.append(value.encode())
selected_roots=['corpus','baseline-13','candidate-14-d095e01d110125ab','candidate-14-'+suffix,'mcp-'+suffix,'api-'+suffix,'runtime-'+suffix,'frozen-engine/'+suffix,'service-validation/'+suffix,'validation/'+suffix,'storage-recovery','audit-control-fix']
selected=[p for root in selected_roots for p in (BASE/root).rglob('*')if p.is_file()]
selected.extend(p for p in BASE.glob('*.json')if p.is_file())
selected.extend(p for p in release.rglob('*')if p.is_file())
for p in [*selected,*(ROOT/f['path']for f in manifest)]:
 data=p.read_bytes();assert not any(secret in data for secret in secrets),'Private credential detected in '+str(p.relative_to(ROOT))
archive_path=release/'evidence.tar.gz'
if not archive_path.exists():
 with tarfile.open(archive_path,'x:gz',compresslevel=6)as archive:
  for p in sorted(set(selected)):archive.add(p,arcname=str(p.relative_to(BASE)),recursive=False)
summary={'image':image,'rule_version':'funds-semantics-14','frozen_at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),
 'status':'isolated_candidate_evidence_frozen_not_production','general_business_depth':'NOT_ACCEPTED','budget_material_allocation':'DEVIATION_RECORDED',
 'validation':str(validation.relative_to(BASE)),'source_files':len(manifest),'archived_evidence_files':len(set(selected)),
 'archives':{p.name:{'byte_size':p.stat().st_size,'sha256':sha(p.read_bytes())}for p in [source_zip,archive_path]},
 'retained_all_attempts_inventory_sha256':sha((release/'all-attempts-inventory.json').read_bytes()),'production_pids_unchanged':True,'credential_scan':'PASS'}
(release/'publication-summary.json').write_text(json.dumps(summary,indent=2));print(json.dumps(summary,indent=2),flush=True)
