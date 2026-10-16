"""Certify the implemented candidate scopes and freeze their exact evidence."""
import hashlib,json,pathlib,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-model-closure-20261016'
BASE=ROOT/'acceptance/contract-analysis-model-closure';ENGINE=BASE/'symbol-engine';RUNTIME=BASE/'symbol-validation';SERVICE=BASE/'symbol-service-validation'
image=(ENGINE/'engine-image.txt').read_text().strip();freeze=json.loads((ENGINE/'engine-source-freeze.json').read_text())
def sha(path):return hashlib.sha256(path.read_bytes()).hexdigest()
assert freeze['image']==image and freeze['rule_version']=='funds-semantics-8'
for file in freeze['files']:assert sha(ROOT/file['path'])==file['sha256'],file['path']
runtime=json.loads((RUNTIME/'final-acceptance.json').read_text());assert runtime['status']=='PASS'and runtime['image']==image
assert runtime['gates']and all(g['status']=='PASS'and g['image']==image for g in runtime['gates'].values())
gates=json.loads((SERVICE/'build-gates.json').read_text())
required=['qa-setup','frontend-contract','frontend-typecheck-all','frontend-build-admin','frontend-render-all','live-core','verification-evidence','dependencies','verification-browser','authority-browser','mechanism-browser','live-resilience']
assert all(gates.get(name,{}).get('status')=='PASS'and gates[name]['image']==image for name in required)
counts={}
for name in ['authority-live-qa.json','live-api-qa.json','mcp-live-qa.json','execution-live-qa.json','verification-evidence-qa.json','dependency-live-qa.json','lifecycle-qa.json','resource-fault-qa.json','browser/report.json','verification-browser/report.json','authority-browser/report.json','mechanism-browser/report.json']:
 rows=json.loads((SERVICE/name).read_text());assert isinstance(rows,list)and rows and all(r['status']=='PASS'for r in rows),name;counts[name]=len(rows)
for name in ['nonlinear-runtime','assembly-runtime','creation-runtime','signature-runtime','verification-runtime','deepening-runtime','expression-runtime','mechanism-runtime','return-runtime','runtime-fixtures','gold']:
 data=json.loads((RUNTIME/name/'report.json').read_text());rows=data if isinstance(data,list)else data['checks'];assert rows and all(r['status']=='PASS'for r in rows),name
 if isinstance(data,dict):assert data['image']==image,name
 counts[name]=len(rows)
gold=json.loads((RUNTIME/'gold/report.json').read_text());assert len(gold['runs'])==29
for name,minimum in{'nonlinear-runtime':922,'assembly-runtime':103,'creation-runtime':258,'signature-runtime':241,'gold':168}.items():assert counts[name]>=minimum,(name,counts[name])
signature=json.loads((RUNTIME/'signature-runtime/report.json').read_text());assert signature['complete']and len(signature['runs'])==18
for name in ['require','assert','msg']:
 result=json.loads((RUNTIME/'signature-runtime'/('shadow-'+name+'-analysis.json')).read_text());model=next(m for m in result['sections']['authorization_models']if m['kind']=='owner_identity_integrity');assert model['status']=='counterexample'and not model['complete']
 result=json.loads((BASE/'shadow-baseline'/(name+'-analysis.json')).read_text());assert any(m['kind']=='owner_identity_integrity'and m['complete']for m in result['sections']['authorization_models'])
usage=json.loads((SERVICE/'live-usage.json').read_text());assert usage['actual']['http_requests']<=2000 and usage['actual']['credits']<=1000000 and usage['actual']['estimated_usd']<=10
previous_usage=json.loads((SERVICE/'production-usage-baseline.json').read_text())['actual']
assert usage['actual']['http_requests']+previous_usage['http_requests']+160<=2000 and usage['actual']['credits']+previous_usage['credits']+100000<=1000000 and usage['actual']['estimated_usd']+previous_usage['estimated_usd']<10
assert(SERVICE/'live-usage-start.txt').read_text()==(ROOT.parent/'contract-verification-20261015/acceptance/contract-analysis-verification/live-usage-start.txt').read_text()
pids=json.loads((SERVICE/'production-pids-before.json').read_text());assert pids and all(int(v)>0 for v in pids.values())
for unit,pid in pids.items():assert subprocess.check_output(['systemctl','--user','show',unit,'-p','MainPID','--value']).decode().strip()==pid,unit
paths=set(subprocess.check_output(['git','ls-files','-z'],cwd=ROOT).decode().split('\0'))|set(subprocess.check_output(['git','ls-files','--others','--exclude-standard','-z'],cwd=ROOT).decode().split('\0'))
prefixes={'analysis-engines','backend','mcp','frontend','scripts','docs','openapi','migrations'}
sources=[]
for name in sorted(paths):
 p=pathlib.PurePosixPath(name)
 if not name or p.parts[0]not in prefixes or {'node_modules','dist','bin','acceptance','acceptance-evidence'}.intersection(p.parts)or p.name.startswith('.env')or not(ROOT/name).is_file():continue
 sources.append({'path':name,'sha256':sha(ROOT/name)})
artifacts=[ROOT/'backend/bin/contract-analysis-api',ROOT/'backend/bin/contract-analysis-worker',*ROOT.joinpath('mcp/dist').rglob('*.js'),*ROOT.joinpath('frontend/apps/user-console/dist').rglob('*')]
built=[{'path':str(p.relative_to(ROOT)),'sha256':sha(p),'byte_size':p.stat().st_size}for p in sorted(artifacts)if p.is_file()]
assert all((ROOT/n).is_file()for n in['backend/bin/contract-analysis-api','backend/bin/contract-analysis-worker','mcp/dist/server.js','frontend/apps/user-console/dist/index.html'])
evidence=[]
for base in[ENGINE,RUNTIME,SERVICE,BASE/'shadow-baseline']:
 for p in sorted(base.rglob('*')):
  if p.is_file()and not p.is_symlink():evidence.append({'path':str(p.relative_to(ROOT)),'sha256':sha(p),'byte_size':p.stat().st_size})
manifest={'status':'PASS','scope':'implemented_model_scopes_and_isolated_service_acceptance','rule_version':'funds-semantics-8','image':image,'source_commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT).decode().strip(),
 'sources':sources,'built_files':built,'evidence_files':evidence,'engine_files':freeze['files'],'counts':counts,'usage':usage,'production_pids':pids,'whole_goal_complete':False,
 'remaining':['coupled_heap_and_general_nonlinear_loop_invariants','arbitrary_host_effects_external_dependencies_and_callbacks','factory_creation_proxy_initialization_and_upgrade_continuity','cached_signature_domains_dynamic_signatures_contract_signatures_and_multisig_thresholds','protocol_specific_assets_prices_fees_rounding_debt_and_liquidation_models','full_declared_authorization_and_economics_closure']}
(BASE/'candidate-freeze.json').write_text(json.dumps(manifest,indent=2))
print('MODEL_CANDIDATE_CERTIFIED',image,'SOURCE_FILES',len(sources),'BUILT_FILES',len(built),'EVIDENCE_FILES',len(evidence),'WHOLE_GOAL_REMAINS_ACTIVE',flush=True)
