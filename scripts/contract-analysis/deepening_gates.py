"""Validate the exact candidate with source, EVM, services and durable outputs."""
import concurrent.futures,json,os,pathlib,subprocess,time
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-deepening-20261015'
OUT=ROOT/'acceptance/contract-analysis-deepening';OUT.mkdir(parents=True,exist_ok=True)
env=os.environ.copy();env.update({'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+env['PATH'],'GOMAXPROCS':'2','CONTRACT_QA_OUT':'acceptance/contract-analysis-deepening','CONTRACT_QA_ENV':'/home/euripides/.config/blockchain-platform/contract-deepening-qa-20261015.env','CONTRACT_QA_API':'http://127.0.0.1:18091','CONTRACT_QA_CONSOLE':'http://127.0.0.1:18093/app-v2','CONTRACT_QA_WORKER':'blockchain-contract-coverage-qa-worker.service','E2E_BROWSER_CHANNEL':'chromium','npm_config_verify_deps_before_run':'false','CONTRACT_PYTHON':str(ROOT/'scripts/contract-analysis/engine_python_bridge.py')})
gates={}
def run(name,args,cwd=ROOT,extra=None):
 e={**env,**(extra or {})};started=time.monotonic()
 with (OUT/(name+'.log')).open('w')as log:p=subprocess.run(args,cwd=cwd,env=e,stdout=log,stderr=subprocess.STDOUT)
 gates[name]={'status':'PASS'if p.returncode==0 else'FAIL','exit_code':p.returncode,'wall_seconds':round(time.monotonic()-started,3)}
 (OUT/'build-gates.json').write_text(json.dumps(gates,indent=2));print('GATE',name,p.returncode,flush=True)
 if p.returncode:print((OUT/(name+'.log')).read_text()[-6000:],flush=True);raise RuntimeError(name)
run('engine-build',['python3','scripts/contract-analysis/deepening_prepare.py'])
env['CONTRACT_TEST_IMAGE']=(OUT/'engine-image.txt').read_text().strip()
run('engine-unit',['node','--test','--test-concurrency=2',*map(str,sorted((ROOT/'analysis-engines/test').glob('*.test.mjs')))])
run('go-test',['go','test','./internal/contractanalysis','./internal/api','-run','TestContract','-count=1'],ROOT/'backend')
run('qa-setup',['python3','scripts/contract-analysis/coverage_qa_setup.py'])
run('frontend-contract',['node','scripts/uc-contract-freeze-check.mjs'],ROOT/'frontend',{'UC_CONTRACT_BASELINE':'1cfe42a4e060a5607fadacde5d259f210449ffd6'})
run('frontend-typecheck-all',['pnpm','--config.verify-deps-before-run=false','run','typecheck'],ROOT/'frontend')
def local():
 run('deepening-runtime',['node','scripts/contract-analysis/deepening_runtime_qa.mjs'])
 run('external-return-runtime',['go','test','./internal/api','-run','TestContractReturnSavedRuntime','-count=1','-v'],ROOT/'backend',{'CONTRACT_DEEPENING_RUNTIME':str(OUT/'deepening-runtime')})
 for name,script in [('expression-runtime','expressions_runtime_qa.mjs'),('mechanism-runtime','mechanisms_runtime_qa.mjs'),('return-runtime','semantics_runtime_qa.mjs'),('runtime-fixtures','runtime_fixture_qa.mjs')]:run(name,['node','scripts/contract-analysis/'+script])
with concurrent.futures.ThreadPoolExecutor(max_workers=3)as pool:
 for job in [pool.submit(local),pool.submit(run,'gold',['node','scripts/contract-analysis/coverage_gold_qa.mjs']),pool.submit(run,'live',['python3','scripts/contract-analysis/coverage_suite.py'])]:job.result()
run('dependencies',['python3','scripts/contract-analysis/mechanisms_dependency_qa.py'])
run('authority-browser',['node','frontend/scripts/contract-analysis-authority-qa.mjs'])
run('mechanism-browser',['node','frontend/scripts/contract-analysis-mechanisms-qa.mjs'])
run('frontend-render-all',['node','scripts/uc-v4-render-check.mjs','--out',str(OUT/'frontend-render-all')],ROOT/'frontend')
run('deepening-evidence',['python3','scripts/contract-analysis/deepening_evidence_qa.py'])
runtime=json.loads((OUT/'deepening-runtime/report.json').read_text());assert runtime['complete']and all(c['status']=='PASS'for c in runtime['checks'])
assert all(v['status']=='PASS'for v in gates.values())
(OUT/'final-acceptance.json').write_text(json.dumps({'status':'PASS','image':env['CONTRACT_TEST_IMAGE'],'runtime_checks':len(runtime['checks']),'all_paths_proved':False},indent=2))
print('DEEPENING_ACCEPTANCE_PASS',flush=True)
