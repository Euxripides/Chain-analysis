"""Prepare a separate EVM expression candidate using the pinned deployed tree."""
import json,pathlib,shutil,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-expressions-20261015'
PRIOR=ROOT.parent/'contract-mechanisms-20261015'
OUT=ROOT/'acceptance/contract-analysis-expressions';OUT.mkdir(parents=True,exist_ok=True)
for name in ['analysis-engines/node_modules','frontend/node_modules','mcp/node_modules','frontend/apps/admin-console/dist']:
 target=ROOT/name;target.parent.mkdir(parents=True,exist_ok=True)
 if not target.exists():target.symlink_to((PRIOR/name).resolve(),target_is_directory=True)
vendors=ROOT/'.work/coverage/vendors';vendors.parent.mkdir(parents=True,exist_ok=True)
if not vendors.exists():vendors.symlink_to((ROOT.parent/'contract-coverage-20261014/.work/coverage/vendors').resolve(),target_is_directory=True)
for name in ['baseline.json','live-usage-start.txt']:
 shutil.copy2(PRIOR/'acceptance/contract-analysis-mechanisms'/name,OUT/name)
for name in ['coverage_qa_setup.py','coverage_suite.py']:
 p=ROOT/'scripts/contract-analysis'/name;s=p.read_text();s=s.replace("'contract-mechanisms-20261015']","'contract-mechanisms-20261015','contract-expressions-20261015']");p.write_text(s)
p=ROOT/'scripts/contract-analysis/engine_python_bridge.py';p.chmod(0o755)
build=ROOT/'.work/expressions/build';build.mkdir(parents=True,exist_ok=True)
for name in ['source.mjs','source-match.mjs','model.mjs','capabilities.mjs']:shutil.copy2(ROOT/'analysis-engines'/name,build/name)
for name in ['ir','rules']:shutil.copytree(ROOT/'analysis-engines'/name,build/name,dirs_exist_ok=True)
shutil.copy2(ROOT/'analysis-engines/Dockerfile.expressions',build/'Dockerfile')
with (OUT/'engine-build.log').open('w')as log:subprocess.run(['docker','build','--progress=plain','-t','platform-contract-expressions:20261015',str(build)],stdout=log,stderr=subprocess.STDOUT,check=True)
image=subprocess.check_output(['docker','image','inspect','platform-contract-expressions:20261015','--format','{{.Id}}']).decode().strip()
(OUT/'engine-image.txt').write_text(image+'\n');print('EXPRESSIONS_IMAGE',image,flush=True)
