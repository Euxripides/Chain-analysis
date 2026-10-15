"""Finish interrupted acceptance without repeating completed live-provider work."""
import json,os,pathlib,subprocess,time
ROOT=pathlib.Path(__file__).resolve().parents[2];OUT=ROOT/'acceptance/contract-analysis-expressions'
env=os.environ.copy();env.update({'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+env['PATH'],'GOMAXPROCS':'2','CONTRACT_QA_OUT':'acceptance/contract-analysis-expressions','CONTRACT_QA_ENV':'/home/euripides/.config/blockchain-platform/contract-expressions-qa-20261015.env','CONTRACT_QA_API':'http://127.0.0.1:18091','CONTRACT_QA_CONSOLE':'http://127.0.0.1:18093/app-v2','CONTRACT_QA_WORKER':'blockchain-contract-coverage-qa-worker.service','E2E_BROWSER_CHANNEL':'chromium','npm_config_verify_deps_before_run':'false'})
def run(name,args,cwd=ROOT):
 with (OUT/(name+'-final.log')).open('w')as log:p=subprocess.run(args,cwd=cwd,env=env,stdout=log,stderr=subprocess.STDOUT)
 print('ACCEPT',name,p.returncode,flush=True)
 if p.returncode:print((OUT/(name+'-final.log')).read_text()[-4500:],flush=True);raise RuntimeError(name)
for name,script in [('mechanism-runtime','mechanisms_runtime_qa.mjs'),('return-runtime','semantics_runtime_qa.mjs'),('runtime-fixtures','runtime_fixture_qa.mjs')]:
 p=OUT/name/'report.json';saved=json.loads(p.read_text())if p.exists()else None
 checks=saved.get('checks',[])if isinstance(saved,dict)else saved
 if checks and all(c['status']=='PASS'for c in checks)and (not isinstance(saved,dict)or saved['image']==(OUT/'engine-image.txt').read_text().strip()):print('RETAIN_PASSED_EXECUTION',name,len(checks),flush=True)
 else:run(name,['node','scripts/contract-analysis/'+script])
end=time.monotonic()+1200
while True:
 p=OUT/'suite-all.json';rows=json.loads(p.read_text())if p.exists()else[]
 if any(r['status']=='FAIL'for r in rows):raise RuntimeError('live acceptance failed')
 runtime=json.loads((OUT/'expression-runtime/report.json').read_text())
 if len(rows)==8 and runtime['complete']:break
 if time.monotonic()>end:raise RuntimeError('live/runtime acceptance did not finish')
 time.sleep(2)
gold=json.loads((OUT/'gold/report.json').read_text());assert len(gold['runs'])==29 and all(c['status']=='PASS'for c in gold['checks'])
assert gold['image']==runtime['image']==(OUT/'engine-image.txt').read_text().strip()and all(c['status']=='PASS'for c in runtime['checks'])
run('gap-evidence',['python3','scripts/contract-analysis/expressions_gap_qa.py'])
run('authority-browser',['node','frontend/scripts/contract-analysis-authority-qa.mjs'])
run('mechanism-browser',['node','frontend/scripts/contract-analysis-mechanisms-qa.mjs'])
run('frontend-render-all',['node','scripts/uc-v4-render-check.mjs','--out',str(OUT/'frontend-render-all')],ROOT/'frontend')
render=json.loads((OUT/'frontend-render-all/report.json').read_text());assert render['ok'],render.get('problems')
gates=json.loads((OUT/'build-gates.json').read_text());gates['frontend-render-all']={'status':'PASS'};gates['finish-qa']={'status':'PASS'};gates['gap-evidence']={'status':'PASS'};(OUT/'build-gates.json').write_text(json.dumps(gates,indent=2))
(OUT/'final-acceptance.json').write_text(json.dumps({'status':'PASS','image':runtime['image'],'expression_checks':len(runtime['checks']),'all_paths_proved':False},indent=2));print('EXPRESSIONS_ACCEPTANCE_PASS',flush=True)
