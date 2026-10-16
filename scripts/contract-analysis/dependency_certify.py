"""Freeze the exact implemented scopes, build inputs, runtime and service evidence."""
import hashlib,json,pathlib,re,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-dependency-models-20261016'
BASE=ROOT/'acceptance/contract-analysis-dependency-models';ENGINE=BASE/'frozen-engine';RUNTIME=BASE/'frozen-validation';SERVICE=BASE/'service-validation'
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
image=(ENGINE/'engine-image.txt').read_text().strip();freeze=json.loads((ENGINE/'engine-source-freeze.json').read_text());assert freeze['image']==image and freeze['rule_version']=='funds-semantics-9'
for item in freeze['files']:assert sha(ROOT/item['path'])==item['sha256'],item['path']
cap=json.loads((ENGINE/'capabilities.json').read_text())['sections']['capabilities'][0];assert cap['state_model_verification']['factory_creation_identity']['create2_creator_salt_initcode_hash']and cap['state_model_verification']['whole_authorization_proved']is False and cap['state_model_verification']['whole_protocol_economics_proved']is False
runtime=json.loads((RUNTIME/'final-acceptance.json').read_text());assert runtime['status']=='PASS'and runtime['image']==image and len(runtime['gates'])==14
assert all(g['status']=='PASS'and g['image']==image for g in runtime['gates'].values())
gates=json.loads((SERVICE/'build-gates.json').read_text())
required=['qa-setup','frontend-contract','frontend-typecheck-all','frontend-build-admin','frontend-render-all','backend-current-source-tests','backend-refresh-api','backend-refresh-worker','live-core','verification-evidence','dependencies','verification-browser','authority-browser','mechanism-browser','live-resilience']
assert all(gates.get(name,{}).get('status')=='PASS'and gates[name]['image']==image for name in required)
assert json.loads((SERVICE/'frontend-render/report.json').read_text())['ok']is True
backend=json.loads((SERVICE/'backend-build-freeze.json').read_text());assert backend['image']==image
for item in backend['sources']+backend['binaries']:assert sha(ROOT/item['path'])==item['sha256'],item['path']
counts={}
for name in['authority-live-qa.json','live-api-qa.json','mcp-live-qa.json','execution-live-qa.json','verification-evidence-qa.json','dependency-live-qa.json','lifecycle-qa.json','resource-fault-qa.json','browser/report.json','verification-browser/report.json','authority-browser/report.json','mechanism-browser/report.json']:
 rows=json.loads((SERVICE/name).read_text());assert isinstance(rows,list)and rows and all(r['status']=='PASS'for r in rows),name;counts[name]=len(rows)
for name in['factory-runtime','nonlinear-runtime','assembly-runtime','creation-runtime','signature-runtime','verification-runtime','deepening-runtime','expression-runtime','mechanism-runtime','return-runtime','runtime-fixtures','gold']:
 data=json.loads((RUNTIME/name/'report.json').read_text());rows=data if isinstance(data,list)else data['checks'];assert rows and all(r['status']=='PASS'for r in rows),name
 if isinstance(data,dict):assert data['image']==image,name
 counts[name]=len(rows)
for name,minimum in{'factory-runtime':210,'nonlinear-runtime':922,'assembly-runtime':103,'creation-runtime':258,'signature-runtime':238,'gold':168}.items():assert counts[name]>=minimum,(name,counts[name])
factory=json.loads((RUNTIME/'factory-runtime/report.json').read_text());assert factory['complete']and len(factory['runs'])==32
assert sum(r['expected_error']is not None for r in factory['runs'])==4
assert len(list((RUNTIME/'factory-runtime').glob('*-binding-case.json')))==32
log=(RUNTIME/'engine-unit.log').read_text();matches=re.findall(r'(?:#|ℹ) tests\s+(\d+)',log);assert matches and int(matches[-1])>=90,matches;counts['engine_unit']=int(matches[-1])
assert len(json.loads((RUNTIME/'gold/report.json').read_text())['runs'])==29
usage=json.loads((SERVICE/'live-usage.json').read_text());previous=json.loads((SERVICE/'production-usage-baseline.json').read_text())['actual'];total={k:usage['actual'][k]+previous[k]for k in['http_requests','credits','estimated_usd']}
assert total['http_requests']+160<=2000 and total['credits']+100000<=1000000 and total['estimated_usd']<=10,total
assert(SERVICE/'live-usage-start.txt').read_text()==(ROOT.parent/'contract-model-closure-20261016/acceptance/contract-analysis-model-closure/symbol-service-validation/live-usage-start.txt').read_text()
pids=json.loads((SERVICE/'production-pids-before.json').read_text());assert all(int(v)>0 for v in pids.values())
assert pids=={unit:subprocess.check_output(['systemctl','--user','show',unit,'-p','MainPID','--value'],text=True).strip()for unit in pids},'production changed during isolated acceptance'
paths=set(subprocess.check_output(['git','ls-files','-z'],cwd=ROOT).decode().split('\0'))|set(subprocess.check_output(['git','ls-files','--others','--exclude-standard','-z'],cwd=ROOT).decode().split('\0'))
sources=[]
for name in sorted(paths):
 p=pathlib.PurePosixPath(name)
 if not name or p.parts[0]not in{'analysis-engines','backend','mcp','frontend','scripts','docs','openapi','migrations'}or{'node_modules','dist','bin','acceptance','acceptance-evidence','__pycache__'}.intersection(p.parts)or p.name.startswith('.env')or not(ROOT/name).is_file():continue
 sources.append({'path':name,'sha256':sha(ROOT/name)})
files=[ROOT/'backend/bin/contract-analysis-api',ROOT/'backend/bin/contract-analysis-worker',*ROOT.joinpath('mcp/dist').rglob('*.js'),*ROOT.joinpath('frontend/apps/user-console/dist').rglob('*')]
built=[{'path':str(p.relative_to(ROOT)),'sha256':sha(p),'byte_size':p.stat().st_size}for p in sorted(files)if p.is_file()]
assert all((ROOT/n).is_file()for n in['backend/bin/contract-analysis-api','backend/bin/contract-analysis-worker','mcp/dist/server.js','frontend/apps/user-console/dist/index.html'])
evidence=[{'path':str(p.relative_to(ROOT)),'sha256':sha(p),'byte_size':p.stat().st_size}for base in[ENGINE,RUNTIME,SERVICE]for p in sorted(base.rglob('*'))if p.is_file()and not p.is_symlink()]
manifest={'status':'PASS','scope':'implemented_factory_creation_frames_and_isolated_services','rule_version':'funds-semantics-9','image':image,'source_commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),'sources':sources,'built_files':built,'evidence_files':evidence,'engine_files':freeze['files'],'counts':counts,'usage':{'isolated_qa':usage['actual'],'previous_production':previous,'actual':total,'limits':usage['limits']},'production_pids':pids,'whole_goal_complete':False,'remaining':['missing_creation_prestate_requires_exact_transaction_position_witness','dynamic_constructor_arguments_proxy_initialization_and_upgrade_continuity','coupled_heap_and_general_nonlinear_loop_invariants','arbitrary_host_effects_external_dependencies_and_callbacks','cached_domains_contract_signatures_and_multisig_thresholds','protocol_specific_assets_prices_fees_rounding_debt_and_liquidation_models','full_declared_authorization_and_economics_closure']}
(BASE/'candidate-freeze.json').write_text(json.dumps(manifest,indent=2));print('DEPENDENCY_CANDIDATE_CERTIFIED',image,'SOURCE_FILES',len(sources),'BUILT_FILES',len(built),'EVIDENCE_FILES',len(evidence),'WHOLE_GOAL_REMAINS_ACTIVE',flush=True)
