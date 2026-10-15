"""Run final-image execution, source, API, artifact and browser acceptance."""
import concurrent.futures,json,os,pathlib,subprocess,time
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-expressions-20261015'
OUT=ROOT/'acceptance/contract-analysis-expressions'
env=os.environ.copy();env.update({'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+env['PATH'],'GOMAXPROCS':'2','CONTRACT_QA_OUT':'acceptance/contract-analysis-expressions','CONTRACT_QA_ENV':'/home/euripides/.config/blockchain-platform/contract-expressions-qa-20261015.env','CONTRACT_QA_API':'http://127.0.0.1:18091','CONTRACT_QA_CONSOLE':'http://127.0.0.1:18093/app-v2','CONTRACT_QA_WORKER':'blockchain-contract-coverage-qa-worker.service','E2E_BROWSER_CHANNEL':'chromium','npm_config_verify_deps_before_run':'false'})
def run(name,args,cwd=ROOT):
 with (OUT/(name+'-final.log')).open('w')as log:p=subprocess.run(args,cwd=cwd,env=env,stdout=log,stderr=subprocess.STDOUT)
 print('ACCEPT',name,p.returncode,flush=True)
 if p.returncode:print((OUT/(name+'-final.log')).read_text()[-4500:],flush=True);raise RuntimeError(name)
 return {'status':'PASS','exit_code':0}
gates=json.loads((OUT/'build-gates.json').read_text());assert set(gates)=={'engine-unit','go-test','qa-setup','frontend-contract','frontend-typecheck-all'}and all(g['status']=='PASS'for g in gates.values())
run('dependencies',['python3','scripts/contract-analysis/mechanisms_dependency_qa.py'])
def local():
 for name,script in [('expression-runtime','expressions_runtime_qa.mjs'),('mechanism-runtime','mechanisms_runtime_qa.mjs'),('return-runtime','semantics_runtime_qa.mjs'),('runtime-fixtures','runtime_fixture_qa.mjs')]:run(name,['node','scripts/contract-analysis/'+script])
with concurrent.futures.ThreadPoolExecutor(max_workers=3)as pool:
 for job in [pool.submit(local),pool.submit(run,'gold',['node','scripts/contract-analysis/coverage_gold_qa.mjs']),pool.submit(run,'live',['python3','scripts/contract-analysis/coverage_suite.py'])]:job.result()
run('authority-browser',['node','frontend/scripts/contract-analysis-authority-qa.mjs'])
run('mechanism-browser',['node','frontend/scripts/contract-analysis-mechanisms-qa.mjs'])
run('frontend-render-all',['node','scripts/uc-v4-render-check.mjs','--out',str(OUT/'frontend-render-all')],ROOT/'frontend')
render=json.loads((OUT/'frontend-render-all/report.json').read_text());assert render['ok'],render.get('problems')
gates['frontend-render-all']={'status':'PASS'};(OUT/'build-gates.json').write_text(json.dumps(gates,indent=2))
runtime=json.loads((OUT/'expression-runtime/report.json').read_text());assert runtime['complete']and all(c['status']=='PASS'for c in runtime['checks'])
(OUT/'final-acceptance.json').write_text(json.dumps({'status':'PASS','image':(OUT/'engine-image.txt').read_text().strip(),'expression_checks':len(runtime['checks']),'all_paths_proved':False},indent=2));print('EXPRESSIONS_ACCEPTANCE_PASS',flush=True)
