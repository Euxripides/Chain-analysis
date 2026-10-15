"""Build and validate the candidate against the isolated existing QA database."""
import json,os,pathlib,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-expressions-20261015'
OUT=ROOT/'acceptance/contract-analysis-expressions';OUT.mkdir(parents=True,exist_ok=True)
env=os.environ.copy();env.update({'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+env['PATH'],'GOMAXPROCS':'2','CONTRACT_QA_OUT':'acceptance/contract-analysis-expressions','CONTRACT_QA_ENV':'/home/euripides/.config/blockchain-platform/contract-expressions-qa-20261015.env','npm_config_verify_deps_before_run':'false','CONTRACT_PYTHON':str(ROOT/'scripts/contract-analysis/engine_python_bridge.py'),'CONTRACT_TEST_IMAGE':(OUT/'engine-image.txt').read_text().strip()})
gates={}
def run(name,args,cwd=ROOT):
 with (OUT/(name+'.log')).open('w')as log:p=subprocess.run(args,cwd=cwd,env=env,stdout=log,stderr=subprocess.STDOUT)
 gates[name]={'status':'PASS'if p.returncode==0 else'FAIL','exit_code':p.returncode};(OUT/'build-gates.json').write_text(json.dumps(gates,indent=2));print('GATE',name,p.returncode,flush=True)
 if p.returncode:print((OUT/(name+'.log')).read_text()[-5000:],flush=True);raise SystemExit(p.returncode)
run('engine-unit',['node','--test','--test-concurrency=2',*map(str,sorted((ROOT/'analysis-engines/test').glob('*.test.mjs')))])
run('go-test',['go','test','./internal/contractanalysis','./internal/api','-run','TestContract','-count=1'],ROOT/'backend')
run('qa-setup',['python3','scripts/contract-analysis/coverage_qa_setup.py'])
env['UC_CONTRACT_BASELINE']='1cfe42a4e060a5607fadacde5d259f210449ffd6'
run('frontend-contract',['node','scripts/uc-contract-freeze-check.mjs'],ROOT/'frontend')
run('frontend-typecheck-all',['pnpm','--config.verify-deps-before-run=false','run','typecheck'],ROOT/'frontend')
print('EXPRESSIONS_BUILD_GATES_PASS',flush=True)
