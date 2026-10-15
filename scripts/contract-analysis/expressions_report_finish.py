"""Validate the additive report change before the final browser gates."""
import json,os,pathlib,subprocess,time,urllib.request
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-expressions-20261015'
OUT=ROOT/'acceptance/contract-analysis-expressions';gates=json.loads((OUT/'build-gates.json').read_text())
env=os.environ.copy();env.update({'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+env['PATH'],'GOMAXPROCS':'2','CONTRACT_QA_OUT':'acceptance/contract-analysis-expressions','CONTRACT_QA_ENV':'/home/euripides/.config/blockchain-platform/contract-expressions-qa-20261015.env','CONTRACT_QA_API':'http://127.0.0.1:18091','CONTRACT_QA_DEPENDENCY_KEY':'ca-expression-report-','CONTRACT_QA_ORDERED_REPORT':'1'})
def run(name,args,cwd=ROOT):
 with (OUT/(name+'.log')).open('w')as log:p=subprocess.run(args,cwd=cwd,env=env,stdout=log,stderr=subprocess.STDOUT)
 gates.update(json.loads((OUT/'build-gates.json').read_text()));gates[name]={'status':'PASS'if p.returncode==0 else'FAIL','exit_code':p.returncode};(OUT/'build-gates.json').write_text(json.dumps(gates,indent=2));print('REPORT_GATE',name,p.returncode,flush=True)
 if p.returncode:print((OUT/(name+'.log')).read_text()[-4500:],flush=True);raise SystemExit(p.returncode)
if os.environ.get('CONTRACT_QA_REPORT_RESUME')!='1':
 run('gofmt-report',['gofmt','-w','internal/api/contract_analysis_artifacts.go'],ROOT/'backend')
 run('report-go-test',['go','test','./internal/contractanalysis','./internal/api','-count=1'],ROOT/'backend')
 for name,target in [('api','api'),('worker','contract-worker')]:run('report-'+name+'-build',['go','build','-o','bin/contract-analysis-'+name,'./cmd/'+target],ROOT/'backend')
 run('qa-report-restart',['systemctl','--user','restart','blockchain-contract-coverage-qa-api.service','blockchain-contract-coverage-qa-worker.service'])
else:assert all(gates[k]['status']=='PASS'for k in ['gofmt-report','report-go-test','report-api-build','report-worker-build','qa-report-restart'])
end=time.monotonic()+40
while True:
 try:
  with urllib.request.urlopen('http://127.0.0.1:18091/health/ready',timeout=2)as response:assert response.status==200
  break
 except Exception:
  if time.monotonic()>end:raise RuntimeError('QA API readiness timeout')
  time.sleep(.5)
run('report-dependency',['python3','scripts/contract-analysis/mechanisms_dependency_qa.py'])
run('finish-qa',['python3','scripts/contract-analysis/expressions_finish_qa.py'])
