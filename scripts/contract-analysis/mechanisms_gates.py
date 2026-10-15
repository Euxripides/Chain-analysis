"""Run required candidate gates with independent logs and no production writes."""
import json,os,pathlib,subprocess,sys
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-mechanisms-20261015'
OUT=ROOT/'acceptance/contract-analysis-mechanisms';OUT.mkdir(parents=True,exist_ok=True)
env=os.environ.copy();env.update({'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+env['PATH'],'GOMAXPROCS':'2','CONTRACT_QA_OUT':'acceptance/contract-analysis-mechanisms','CONTRACT_QA_ENV':'/home/euripides/.config/blockchain-platform/contract-mechanisms-qa-20261015.env','npm_config_verify_deps_before_run':'false','CONTRACT_PYTHON':str(ROOT/'scripts/contract-analysis/engine_python_bridge.py')})
gates={}
def run(name,args,cwd=ROOT):
 with (OUT/(name+'.log')).open('w')as log:result=subprocess.run(args,cwd=cwd,env=env,stdout=log,stderr=subprocess.STDOUT)
 gates[name]={'status':'PASS'if result.returncode==0 else'FAIL','exit_code':result.returncode};(OUT/'build-gates.json').write_text(json.dumps(gates,indent=2));print('GATE',name,result.returncode,flush=True)
 if result.returncode:print((OUT/(name+'.log')).read_text()[-6000:],flush=True);raise SystemExit(result.returncode)
run('engine-unit',['node','--test','--test-concurrency=2',*map(str,sorted((ROOT/'analysis-engines/test').glob('*.test.mjs')))])
run('go-test',['go','test','./internal/contractanalysis','./internal/api','-run','TestContract','-count=1'],ROOT/'backend')
run('qa-setup',['python3','scripts/contract-analysis/coverage_qa_setup.py'])
env['UC_CONTRACT_BASELINE']='1cfe42a4e060a5607fadacde5d259f210449ffd6'
run('frontend-contract',['node','scripts/uc-contract-freeze-check.mjs'],ROOT/'frontend')
run('frontend-typecheck-all',['pnpm','--config.verify-deps-before-run=false','run','typecheck'],ROOT/'frontend')
print('MECHANISMS_BUILD_GATES_PASS',flush=True)
