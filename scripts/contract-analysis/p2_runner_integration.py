"""Validate the real runner with an owned stalled parent and live child."""
import datetime,hashlib,json,os,pathlib,subprocess,time
ROOT=pathlib.Path(__file__).resolve().parents[2];BASE=ROOT/'acceptance/contract-analysis-partial-recovery'
image=(BASE/'engine-image.txt').read_text().strip()
out=BASE/'runner-integration'/image[7:23]/datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ');out.mkdir(parents=True)
(out/'stalled.mjs').write_text("import{readFileSync}from'node:fs';import{spawn}from'node:child_process';readFileSync(0);spawn('/bin/sleep',['3600'],{stdio:'ignore'});setInterval(()=>{},1000);\n")
(out/'Dockerfile').write_text('FROM blockchain-contract-analysis:partial-recovery-development-20261017@'+image+'\nCOPY stalled.mjs /qa-stalled.mjs\nENTRYPOINT ["node","/qa-stalled.mjs"]\n')
with(out/'build.log').open('w')as f:subprocess.run(['docker','build','--network','none','--pull=false','-t','contract-recovery-runner-fault:20261017',str(out)],check=True,stdout=f,stderr=subprocess.STDOUT)
fault=subprocess.check_output(['docker','image','inspect','contract-recovery-runner-fault:20261017','--format','{{.Id}}'],text=True).strip()
env={**os.environ,'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:'+os.environ['PATH'],'GOMAXPROCS':'2','CONTRACT_RUNNER_FAULT_IMAGE':fault}
started=time.monotonic()
with(out/'runner.log').open('w')as f:r=subprocess.run(['go','test','-v','./internal/contractanalysis','-run','TestRunner','-count=1'],cwd=ROOT/'backend',env=env,stdout=f,stderr=subprocess.STDOUT,timeout=180)
record={'status':'PASS'if r.returncode==0 else'FAIL','candidate_image':image,'fault_image':fault,'exit_code':r.returncode,'wall_seconds':time.monotonic()-started,'provider_http':0,'log_sha256':hashlib.sha256((out/'runner.log').read_bytes()).hexdigest()}
(out/'result.json').write_text(json.dumps(record,indent=2));print('P2_REAL_RUNNER',json.dumps(record),flush=True)
if r.returncode:print((out/'runner.log').read_text()[-5000:]);raise SystemExit(r.returncode)
