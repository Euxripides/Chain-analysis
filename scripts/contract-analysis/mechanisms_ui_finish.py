"""Validate the final table presentation without changing a renderer's inputs."""
import hashlib,json,os,pathlib,subprocess,time
R=pathlib.Path(__file__).resolve().parents[2];assert R.name=='contract-mechanisms-20261015'
O=R/'acceptance/contract-analysis-mechanisms';F=R/'frontend';stage=F/'apps/user-console/dist-next'
env=os.environ.copy();env.update({'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+env['PATH'],'npm_config_verify_deps_before_run':'false','UC_CONTRACT_BASELINE':'1cfe42a4e060a5607fadacde5d259f210449ffd6','E2E_BROWSER_CHANNEL':'chromium','UC_CHECK_PORT':'5401','UC_CHECK_DIST':str(stage),'CONTRACT_QA_OUT':'acceptance/contract-analysis-mechanisms','CONTRACT_QA_CONSOLE':'http://127.0.0.1:18093/app-v2'})
def run(name,args,cwd=R):
 with(O/(name+'-ui-final.log')).open('w')as log:p=subprocess.run(args,cwd=cwd,env=env,stdout=log,stderr=subprocess.STDOUT)
 print('UI_FINAL',name,p.returncode,flush=True)
 if p.returncode:print((O/(name+'-ui-final.log')).read_text()[-3000:],flush=True);raise SystemExit(p.returncode)
run('frontend-build',['npm','run','build:user','--','--outDir',str(stage)],F)
run('frontend-typecheck-all',['pnpm','--config.verify-deps-before-run=false','run','typecheck'],F)
run('frontend-contract',['node','scripts/uc-contract-freeze-check.mjs'],F)
run('frontend-render-all',['node','scripts/uc-v4-render-check.mjs','--out',str(O/'frontend-render-all-final')],F)
deadline=time.monotonic()+600
while subprocess.run(['systemctl','--user','is-active','contract-mechanisms-render-final-20261015'],stdout=subprocess.DEVNULL).returncode==0:
 if time.monotonic()>deadline:raise RuntimeError('previous renderer did not finish')
 time.sleep(2)
old=F/'apps/user-console/dist';backup=F/'apps/user-console/dist-render-baseline'
assert old.parent.resolve()==stage.parent.resolve()==(F/'apps/user-console').resolve()
assert not backup.exists();old.rename(backup);stage.rename(old)
run('browser',['node','frontend/scripts/contract-analysis-live-qa.mjs'])
run('authority-browser',['node','frontend/scripts/contract-analysis-authority-qa.mjs'])
run('mechanism-browser',['node','frontend/scripts/contract-analysis-mechanisms-qa.mjs'])
g=json.loads((O/'build-gates.json').read_text())
for name in ['frontend-typecheck-all','frontend-contract','frontend-render-all']:g[name]={'status':'PASS'}
(O/'build-gates.json').write_text(json.dumps(g,indent=2))
cert={'status':'PASS','image':(O/'engine-image.txt').read_text().strip(),'runtime_checks':len(json.loads((O/'mechanism-runtime/report.json').read_text())['checks']),'gold_checks':len(json.loads((O/'gold/report.json').read_text())['checks']),'all_paths_proved':False,'console_index_sha256':hashlib.sha256((old/'index.html').read_bytes()).hexdigest()}
(O/'final-acceptance.json').write_text(json.dumps(cert,indent=2));print('UI_FINAL_ACCEPTANCE_PASS',flush=True)
