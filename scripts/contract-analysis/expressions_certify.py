"""Certify actual final results and the exact code inside the candidate image."""
import hashlib,json,pathlib,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-expressions-20261015'
OUT=ROOT/'acceptance/contract-analysis-expressions';image=(OUT/'engine-image.txt').read_text().strip()
final=json.loads((OUT/'final-acceptance.json').read_text());assert final['status']=='PASS'and final['image']==image
render=json.loads((OUT/'frontend-render-all/report.json').read_text());assert render['ok']and not render['problems']
for name in ['expression-evidence-qa.json','dependency-live-qa.json','expression-browser/report.json','authority-browser/report.json','mechanism-browser/report.json','browser/report.json']:
 rows=json.loads((OUT/name).read_text());assert rows and all(r['status']=='PASS'for r in rows),name
paths=[ROOT/'analysis-engines'/name for name in ['source.mjs','source-match.mjs','model.mjs','capabilities.mjs']]
for name in ['ir','rules']:paths+=sorted((ROOT/'analysis-engines'/name).glob('*.mjs'))
keys=['/engine/'+str(p.relative_to(ROOT/'analysis-engines'))for p in paths]
code="import hashlib,json,pathlib,sys;print(json.dumps({p:hashlib.sha256(pathlib.Path(p).read_bytes()).hexdigest() for p in sys.argv[1:]}))"
run=subprocess.run(['docker','run','--rm','--pull','never','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','1','--memory','128m','--pids-limit','16','--entrypoint','/opt/contract-python/bin/python',image,'-c',code,*keys],stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=True)
inside=json.loads(run.stdout);files=[]
for key,p in zip(keys,paths):
 sha=hashlib.sha256(p.read_bytes()).hexdigest();assert inside[key]==sha,p
 files.append({'path':str(p.relative_to(ROOT)),'sha256':sha})
(OUT/'engine-code-freeze.json').write_text(json.dumps({'status':'PASS','image':image,'files':files},indent=2))
gates=json.loads((OUT/'build-gates.json').read_text());gates['finish-qa']={'status':'PASS','evidence':'final-acceptance.json'};gates['gap-evidence']={'status':'PASS','evidence':'expression-evidence-qa.json'};gates['expression-browser']={'status':'PASS','evidence':'expression-browser/report.json'};gates['engine-code-freeze']={'status':'PASS','evidence':'engine-code-freeze.json'}
assert all(g['status']=='PASS'for g in gates.values()),gates
(OUT/'build-gates.json').write_text(json.dumps(gates,indent=2));print('EXPRESSION_CERTIFICATE_PASS',image,len(files),flush=True)
