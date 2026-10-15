"""Complete final-candidate acceptance, retaining every earlier candidate."""
import concurrent.futures,json,os,pathlib,shutil,subprocess,time
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-mechanisms-20261015'
OUT=ROOT/'acceptance/contract-analysis-mechanisms'
env=os.environ.copy();env.update({'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+env['PATH'],'GOMAXPROCS':'2','CONTRACT_QA_OUT':'acceptance/contract-analysis-mechanisms','CONTRACT_QA_ENV':'/home/euripides/.config/blockchain-platform/contract-mechanisms-qa-20261015.env','CONTRACT_QA_API':'http://127.0.0.1:18091','CONTRACT_QA_CONSOLE':'http://127.0.0.1:18093/app-v2','CONTRACT_QA_WORKER':'blockchain-contract-coverage-qa-worker.service','E2E_BROWSER_CHANNEL':'chromium','npm_config_verify_deps_before_run':'false'})
def run(name,args,cwd=ROOT):
 with (OUT/(name+'-final.log')).open('w')as log:result=subprocess.run(args,cwd=cwd,env=env,stdout=log,stderr=subprocess.STDOUT)
 print('FINAL_STAGE',name,result.returncode,flush=True)
 if result.returncode:print((OUT/(name+'-final.log')).read_text()[-6000:],flush=True);raise RuntimeError(name)
 return {'status':'PASS','exit_code':0,'image':(OUT/'engine-image.txt').read_text().strip()}
deadline=time.monotonic()+1200
while True:
 p=OUT/'suite-all.json';rows=json.loads(p.read_text())if p.exists()else[]
 if any(r['status']=='FAIL'for r in rows):break
 if len(rows)==8:break
 if time.monotonic()>deadline:raise RuntimeError('prior live acceptance did not finish')
 time.sleep(2)
subprocess.run(['tar','-xf',str(ROOT/'final-source.tar'),'-C',str(ROOT)],check=True)
subprocess.run(['gofmt','-w',str(ROOT/'backend/internal/api/contract_analysis_dependencies.go'),str(ROOT/'backend/internal/api/contract_analysis_dependencies_test.go')],env=env,check=True)
run('engine-build',['python3','scripts/contract-analysis/mechanisms_build.py'])
run('build-gates',['python3','scripts/contract-analysis/mechanisms_gates.py'])
run('dependencies',['python3','scripts/contract-analysis/mechanisms_dependency_qa.py'])
def local_runtime():
 run('mechanism-runtime',['node','scripts/contract-analysis/mechanisms_runtime_qa.mjs'])
 run('return-runtime',['node','scripts/contract-analysis/semantics_runtime_qa.mjs'])
 return run('runtime-fixtures',['node','scripts/contract-analysis/runtime_fixture_qa.mjs'])
with concurrent.futures.ThreadPoolExecutor(max_workers=3)as pool:
 jobs=[pool.submit(local_runtime),pool.submit(run,'gold',['node','scripts/contract-analysis/coverage_gold_qa.mjs']),pool.submit(run,'live',['python3','scripts/contract-analysis/coverage_suite.py'])]
 for job in jobs:job.result()
run('authority-browser',['node','frontend/scripts/contract-analysis-authority-qa.mjs'])
run('mechanism-browser',['node','frontend/scripts/contract-analysis-mechanisms-qa.mjs'])
admin=ROOT/'frontend/apps/admin-console/dist';admin_source=pathlib.Path('/home/euripides/projects/blockchain-platform/frontend/apps/admin-console/dist')
if admin.resolve()!=admin_source.resolve():shutil.copytree(admin_source,admin,dirs_exist_ok=True)
run('frontend-render-all',['node','scripts/uc-v4-render-check.mjs','--out',str(OUT/'frontend-render-all')],ROOT/'frontend')
gates=json.loads((OUT/'build-gates.json').read_text());gates['frontend-render-all']={'status':'PASS'};(OUT/'build-gates.json').write_text(json.dumps(gates,indent=2))
(OUT/'final-acceptance.json').write_text(json.dumps({'status':'PASS','image':(OUT/'engine-image.txt').read_text().strip(),'runtime_checks':len(json.loads((OUT/'mechanism-runtime/report.json').read_text())['checks']),'gold_checks':len(json.loads((OUT/'gold/report.json').read_text())['checks']),'all_paths_proved':False},indent=2))
print('FINAL_ACCEPTANCE_PASS',flush=True)
