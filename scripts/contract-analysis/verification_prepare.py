"""Build the state-verification candidate from the exact deployed image."""
import json,pathlib,shutil,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-verification-20261015'
PRIOR=ROOT.parent/'contract-deepening-20261015';OUT=ROOT/'acceptance/contract-analysis-verification';OUT.mkdir(parents=True,exist_ok=True)
for name in ['analysis-engines/node_modules','frontend/node_modules','mcp/node_modules','frontend/apps/admin-console/dist']:
 p=ROOT/name;p.parent.mkdir(parents=True,exist_ok=True)
 if not p.exists():p.symlink_to((PRIOR/name).resolve(),target_is_directory=True)
vendors=ROOT/'.work/coverage/vendors';vendors.parent.mkdir(parents=True,exist_ok=True)
if not vendors.exists():vendors.symlink_to((ROOT.parent/'contract-coverage-20261014/.work/coverage/vendors').resolve(),target_is_directory=True)
for name in ['baseline.json','live-usage-start.txt']:shutil.copy2(PRIOR/'acceptance/contract-analysis-deepening'/name,OUT/name)
for name in ['anvil','cast','heimdall']:
 p=ROOT/'analysis-engines/bin'/name;p.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(PRIOR/'analysis-engines/bin'/name,p);p.chmod(0o755)
(ROOT/'scripts/contract-analysis/engine_python_bridge.py').chmod(0o755)
build=ROOT/'.work/verification/build';build.mkdir(parents=True,exist_ok=True)
for name in ['analyze.mjs','source.mjs','model.mjs','capabilities.mjs','replay.mjs','source-pc.mjs','opcode-witness.mjs']:shutil.copy2(ROOT/'analysis-engines'/name,build/name)
for name in ['ir','properties','verification']:shutil.copytree(ROOT/'analysis-engines'/name,build/name,dirs_exist_ok=True)
shutil.copy2(ROOT/'analysis-engines/Dockerfile.verification',build/'Dockerfile')
with(OUT/'engine-build.log').open('w')as log:subprocess.run(['docker','build','--progress=plain','-t','platform-contract-verification:20261015',str(build)],stdout=log,stderr=subprocess.STDOUT,check=True)
image=subprocess.check_output(['docker','image','inspect','platform-contract-verification:20261015','--format','{{.Id}}']).decode().strip()
(OUT/'engine-image.txt').write_text(image+'\n');print('VERIFICATION_IMAGE',image,flush=True)
