"""Build an incremental, digest-pinned candidate without changing a running tree."""
import json,pathlib,shutil,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-mechanisms-20261015'
OUT=ROOT/'acceptance/contract-analysis-mechanisms';OUT.mkdir(parents=True,exist_ok=True)
BUILD=ROOT/'.work/mechanisms/build';BUILD.mkdir(parents=True,exist_ok=True)
source=ROOT/'analysis-engines'
for name in ['source.mjs','source-match.mjs','model.mjs','capabilities.mjs']:shutil.copy2(source/name,BUILD/name)
for name in ['ir','rules']:shutil.copytree(source/name,BUILD/name,dirs_exist_ok=True)
shutil.copy2(source/'Dockerfile.mechanisms',BUILD/'Dockerfile')
with (OUT/'engine-build.log').open('w')as log:subprocess.run(['docker','build','--progress=plain','-t','platform-contract-mechanisms:20261015',str(BUILD)],stdout=log,stderr=subprocess.STDOUT,check=True)
image=subprocess.check_output(['docker','image','inspect','platform-contract-mechanisms:20261015','--format','{{.Id}}']).decode().strip()
(OUT/'engine-image.txt').write_text(image+'\n');print('MECHANISMS_IMAGE',image,flush=True)
