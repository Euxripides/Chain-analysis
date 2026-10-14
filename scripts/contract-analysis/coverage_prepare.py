"""Prepare pinned public EVM dependencies and notices for the isolated candidate."""
import hashlib,json,pathlib,shutil,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2]
assert ROOT.name=='contract-coverage-20261014'
VENDORS=ROOT/'.work/coverage/vendors';VENDORS.mkdir(parents=True,exist_ok=True)
NOTICES=ROOT/'analysis-engines/notices/coverage';NOTICES.mkdir(parents=True,exist_ok=True)
DEPS=[
 ('gigahorse','https://github.com/nevillegrech/gigahorse-toolchain','1b06cef82f0a29790e87f576eba6d6d793170d70','gigahorse-toolchain-1b06cef82f0a'),
 ('slither','https://github.com/crytic/slither','eef5df948b8992eff85786b6b0bb4a193933e2c9','slither-eef5df948b89'),
 ('crytic-compile','https://github.com/crytic/crytic-compile','3d27afa70458b95c8660ba53b621ec6e72af08d4','crytic-compile-3d27afa70458'),
 ('openzeppelin-corpus','https://github.com/OpenZeppelin/openzeppelin-contracts','c64a1edb67b6e3f4a15cca8909c9482ad33a02b0','openzeppelin-v5.4.0'),
]
records=[]
for name,url,commit,directory in DEPS:
 p=VENDORS/directory
 if not p.exists():
  p.mkdir();subprocess.run(['git','init',str(p)],check=True,stdout=subprocess.DEVNULL)
  subprocess.run(['git','-C',str(p),'remote','add','origin',url],check=True)
  subprocess.run(['git','-C',str(p),'fetch','--depth','1','origin',commit],check=True)
  subprocess.run(['git','-C',str(p),'checkout','--detach',commit],check=True,stdout=subprocess.DEVNULL)
 actual=subprocess.check_output(['git','-C',str(p),'rev-parse','HEAD']).decode().strip();assert actual==commit,(name,actual)
 subprocess.run(['git','-C',str(p),'diff','--quiet'],check=True)
 if name=='gigahorse':
  subprocess.run(['git','-C',str(p),'submodule','update','--init','--depth','1'],check=True)
  assert subprocess.check_output(['git','-C',str(p/'souffle-addon'),'rev-parse','HEAD']).decode().strip()=='1b88916abe1f4fbe613b6f67be7051ec6e7fb746'
 licenses=[]
 for source in [*p.glob('LICENSE*'),*p.glob('COPYING*'),*(p/'souffle-addon').glob('LICENSE*')]:
  if not source.is_file():continue
  target=NOTICES/(name+'-'+str(source.relative_to(p)).replace('/','-'));shutil.copy2(source,target)
  licenses.append({'path':str(target.relative_to(ROOT/'analysis-engines')),'sha256':hashlib.sha256(target.read_bytes()).hexdigest(),'upstream_path':str(source.relative_to(p))})
 assert licenses,name
 records.append({'name':name,'repository':url,'commit':commit,'licenses':licenses,'role':'benchmark_only' if name=='openzeppelin-corpus' else 'runtime'})
freeze=ROOT/'acceptance/contract-analysis-coverage/python-freeze.txt'
lock=ROOT/'analysis-engines/requirements.coverage.lock'
if not lock.exists():lock.write_text('\n'.join(x for x in freeze.read_text().splitlines() if ' @ file:' not in x)+'\n')
(ROOT/'analysis-engines/notices/coverage-dependencies.json').write_text(json.dumps(records,indent=2))
print('PINNED_EVM_DEPENDENCIES',len(records),'LICENSES',sum(len(x['licenses']) for x in records))
