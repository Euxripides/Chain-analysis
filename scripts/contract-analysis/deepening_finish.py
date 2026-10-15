"""Resume the failed live step after all current-image local checks finish."""
import json,os,pathlib,subprocess,time
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-deepening-20261015'
OUT=ROOT/'acceptance/contract-analysis-deepening'
env=os.environ.copy();env.update({'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+env['PATH'],'GOMAXPROCS':'2','CONTRACT_QA_OUT':'acceptance/contract-analysis-deepening','CONTRACT_QA_ENV':'/home/euripides/.config/blockchain-platform/contract-deepening-qa-20261015.env','CONTRACT_QA_API':'http://127.0.0.1:18091','CONTRACT_QA_CONSOLE':'http://127.0.0.1:18093/app-v2','CONTRACT_QA_WORKER':'blockchain-contract-coverage-qa-worker.service','E2E_BROWSER_CHANNEL':'chromium','npm_config_verify_deps_before_run':'false'})
deadline=time.monotonic()+900
while True:
 gates=json.loads((OUT/'build-gates.json').read_text())
 if gates.get('gold',{}).get('status')=='FAIL':raise RuntimeError('gold failed')
 active=subprocess.run(['systemctl','--user','is-active','--quiet','contract-deepening-acceptance-20261015.service']).returncode==0
 if not active and gates.get('gold',{}).get('status')=='PASS':break
 if time.monotonic()>deadline:raise RuntimeError('local acceptance did not finish')
 time.sleep(3)
for name in ['engine-unit','go-test','qa-setup','frontend-contract','frontend-typecheck-all','deepening-runtime','external-return-runtime','expression-runtime','mechanism-runtime','return-runtime','runtime-fixtures','gold']:assert gates[name]['status']=='PASS',name
(OUT/'live-first-attempt.json').write_text(json.dumps(gates.get('live'),indent=2))
def run(name,args,cwd=ROOT):
 if os.environ.get('CONTRACT_DEEPENING_FINISH_RESUME')=='1' and gates.get(name,{}).get('status')=='PASS':
  print('FINISH_ALREADY_PASSED',name,flush=True);return
 started=time.monotonic()
 with (OUT/(name+'-finish.log')).open('w')as log:p=subprocess.run(args,cwd=cwd,env=env,stdout=log,stderr=subprocess.STDOUT)
 gates[name]={'status':'PASS'if p.returncode==0 else'FAIL','exit_code':p.returncode,'wall_seconds':round(time.monotonic()-started,3)}
 (OUT/'build-gates.json').write_text(json.dumps(gates,indent=2));print('FINISH',name,p.returncode,flush=True)
 if p.returncode:print((OUT/(name+'-finish.log')).read_text()[-6500:],flush=True);raise RuntimeError(name)
run('live',['python3','scripts/contract-analysis/coverage_suite.py'])
run('dependencies',['python3','scripts/contract-analysis/mechanisms_dependency_qa.py'])
run('authority-browser',['node','frontend/scripts/contract-analysis-authority-qa.mjs'])
run('mechanism-browser',['node','frontend/scripts/contract-analysis-mechanisms-qa.mjs'])
run('frontend-render-all',['node','scripts/uc-v4-render-check.mjs','--out',str(OUT/'frontend-render-all')],ROOT/'frontend')
run('deepening-evidence',['python3','scripts/contract-analysis/deepening_evidence_qa.py'])
runtime=json.loads((OUT/'deepening-runtime/report.json').read_text());assert runtime['complete']and all(r['status']=='PASS'for r in runtime['checks'])
(OUT/'final-acceptance.json').write_text(json.dumps({'status':'PASS','image':(OUT/'engine-image.txt').read_text().strip(),'runtime_checks':len(runtime['checks']),'all_paths_proved':False},indent=2))
print('DEEPENING_ACCEPTANCE_PASS',flush=True)
