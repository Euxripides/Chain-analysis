"""Build the candidate over the exact deployed image, without new packages."""
import json,pathlib,shutil,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-deepening-20261015'
PRIOR=ROOT.parent/'contract-expressions-20261015'
OUT=ROOT/'acceptance/contract-analysis-deepening';OUT.mkdir(parents=True,exist_ok=True)
for name in ['analysis-engines/node_modules','frontend/node_modules','mcp/node_modules','frontend/apps/admin-console/dist']:
 p=ROOT/name;p.parent.mkdir(parents=True,exist_ok=True)
 if not p.exists():p.symlink_to((PRIOR/name).resolve(),target_is_directory=True)
vendors=ROOT/'.work/coverage/vendors';vendors.parent.mkdir(parents=True,exist_ok=True)
if not vendors.exists():vendors.symlink_to((ROOT.parent/'contract-coverage-20261014/.work/coverage/vendors').resolve(),target_is_directory=True)
for name in ['baseline.json','live-usage-start.txt']:
 shutil.copy2(PRIOR/'acceptance/contract-analysis-expressions'/name,OUT/name)
for name in ['anvil','cast','heimdall']:
 p=ROOT/'analysis-engines/bin'/name;p.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(PRIOR/'analysis-engines/bin'/name,p)
(ROOT/'scripts/contract-analysis/engine_python_bridge.py').chmod(0o755)
build=ROOT/'.work/deepening/build';build.mkdir(parents=True,exist_ok=True)
for name in ['analyze.mjs','source.mjs','model.mjs','capabilities.mjs']:shutil.copy2(ROOT/'analysis-engines'/name,build/name)
for name in ['ir','properties']:shutil.copytree(ROOT/'analysis-engines'/name,build/name,dirs_exist_ok=True)
(build/'adapters').mkdir(exist_ok=True);shutil.copy2(ROOT/'analysis-engines/adapters/python-engine.py',build/'adapters/python-engine.py')
shutil.copy2(ROOT/'analysis-engines/Dockerfile.deepening',build/'Dockerfile')
with (OUT/'engine-build.log').open('w') as log:subprocess.run(['docker','build','--progress=plain','-t','platform-contract-deepening:20261015',str(build)],stdout=log,stderr=subprocess.STDOUT,check=True)
image=subprocess.check_output(['docker','image','inspect','platform-contract-deepening:20261015','--format','{{.Id}}']).decode().strip()
(OUT/'engine-image.txt').write_text(image+'\n');print('DEEPENING_IMAGE',image,flush=True)
