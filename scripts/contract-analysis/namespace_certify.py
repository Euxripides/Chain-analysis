"""Freeze the exact implemented scopes, build inputs, runtime and service evidence."""
import hashlib,json,pathlib,re,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-namespaced-storage-20261016'
BASE=ROOT/'acceptance/contract-analysis-namespaced-storage';ENGINE=BASE/'frozen-engine';RUNTIME=BASE/'frozen-validation';SERVICE=BASE/'service-validation'
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
image=(ENGINE/'engine-image.txt').read_text().strip();freeze=json.loads((ENGINE/'engine-source-freeze.json').read_text());assert freeze['image']==image and freeze['rule_version']=='funds-semantics-12'
for item in freeze['files']:assert sha(ROOT/item['path'])==item['sha256'],item['path']
cap=json.loads((ENGINE/'capabilities.json').read_text())['sections']['capabilities'][0];assert cap['state_model_verification']['factory_creation_identity']['create2_creator_salt_initcode_hash']and cap['state_model_verification']['whole_authorization_proved']is False and cap['state_model_verification']['whole_protocol_economics_proved']is False
runtime=json.loads((RUNTIME/'final-acceptance.json').read_text());assert runtime['status']=='PASS'and runtime['image']==image and len(runtime['gates'])==18
assert all(g['status']=='PASS'and g['image']==image for g in runtime['gates'].values())
gates=json.loads((SERVICE/'build-gates.json').read_text())
required=['qa-setup','frontend-contract','frontend-typecheck-all','frontend-build-admin','frontend-render-all','backend-current-source-tests','backend-refresh-api','backend-refresh-worker','live-core','verification-evidence','history-evidence','history-mcp','dependencies','verification-browser','authority-browser','mechanism-browser','live-resilience']
assert all(gates.get(name,{}).get('status')=='PASS'and gates[name]['image']==image for name in required)
assert json.loads((SERVICE/'frontend-render/report.json').read_text())['ok']is True
backend=json.loads((SERVICE/'backend-build-freeze.json').read_text());assert backend['image']==image
for item in backend['sources']+backend['binaries']:assert sha(ROOT/item['path'])==item['sha256'],item['path']
counts={}
for name in['authority-live-qa.json','live-api-qa.json','mcp-live-qa.json','execution-live-qa.json','verification-evidence-qa.json','history-evidence-qa.json','history-mcp-qa.json','dependency-live-qa.json','lifecycle-qa.json','resource-fault-qa.json','browser/report.json','verification-browser/report.json','authority-browser/report.json','mechanism-browser/report.json']:
 rows=json.loads((SERVICE/name).read_text());assert isinstance(rows,list)and rows and all(r['status']=='PASS'for r in rows),name;counts[name]=len(rows)
for name in['namespace-runtime','coupled-runtime','factory-runtime','nonlinear-runtime','assembly-runtime','creation-runtime','signature-runtime','verification-runtime','deepening-runtime','expression-runtime','mechanism-runtime','return-runtime','runtime-fixtures','gold']:
 data=json.loads((RUNTIME/name/'report.json').read_text());rows=data if isinstance(data,list)else data['checks'];assert rows and all(r['status']=='PASS'for r in rows),name
 if isinstance(data,dict):assert data['image']==image,name
 counts[name]=len(rows)
for name,minimum in{'factory-runtime':210,'nonlinear-runtime':922,'assembly-runtime':103,'creation-runtime':258,'signature-runtime':238,'gold':168}.items():assert counts[name]>=minimum,(name,counts[name])
factory=json.loads((RUNTIME/'factory-runtime/report.json').read_text());assert factory['complete']and len(factory['runs'])==32
assert sum(r['expected_error']is not None for r in factory['runs'])==4
assert len(list((RUNTIME/'factory-runtime').glob('*-binding-case.json')))==32
log=(RUNTIME/'engine-unit.log').read_text();matches=re.findall(r'(?:#|ℹ) tests\s+(\d+)',log);assert matches and int(matches[-1])>=125,matches;counts['engine_unit']=int(matches[-1])
assert len(json.loads((RUNTIME/'gold/report.json').read_text())['runs'])==29
geth=json.loads((BASE/'inherited-geth-runtime/geth-image.json').read_text());assert geth['image']=='sha256:2dd7ef210a1a3fb87887676f7ead1b98161af83810928cdf6fc5b84bcdcc8ef4'and geth['dev_chain_hardfork']=='osaka'
capture=json.loads((BASE/'inherited-geth-prestate/report.json').read_text());assert capture['complete']and len(capture['rows'])==5 and all(r['status']=='PASS'for r in capture['checks'])
replayed=json.loads((RUNTIME/'geth-replay/report.json').read_text());assert replayed['complete']and replayed['image']==image and len(replayed['checks'])==11 and all(r['status']=='PASS'for r in replayed['checks'])
counts['actual_geth_capture']=len(capture['checks']);counts['actual_geth_replay']=len(replayed['checks'])
for salt in ['7','8','9','10','11']:
 collected=json.loads((BASE/('inherited-geth-prestate/salt-'+salt+'-production-collector.json')).read_text());assert collected['task_state_reservations']==11 and collected['local_rpc_requests']==27
 result=json.loads((RUNTIME/('geth-replay/salt-'+salt+'-result.json')).read_text());row=result['sections']['replays'][0]
 assert row['complete']and row['original_call_trace_matches']and row['state_diff_complete']and row['environment']['executed_header_matches']and row['environment']['whole_block_execution_verified']is False
 artifact=next(a for a in result['sections']['artifacts']if a['kind']=='transaction_state_diff');assert hashlib.sha256(artifact['text'].encode()).hexdigest()==artifact['sha256'];assert json.loads(artifact['text'])['comparison']['matched']
coupled=json.loads((RUNTIME/'coupled-runtime/report.json').read_text());assert coupled['complete']and coupled['image']==image and len(coupled['checks'])==286 and len(coupled['runs'])==32
assert cap['source_return_analysis']['induction']['coupled_checked_locals']['max_accumulators']==4 and cap['source_return_analysis']['induction']['coupled_unchecked_locals']is False
namespace=json.loads((RUNTIME/'namespace-runtime/report.json').read_text());assert namespace['complete']and namespace['image']==image and len(namespace['runs'])==40
assert cap['state_model_verification']['namespaced_storage']['actual_pointer_assignment_required']is True
backend_log=(RUNTIME/'namespace-backend.log').read_text();assert '--- PASS: TestContractNamespaceBalanceProjectionActualRuntime' in backend_log and '--- SKIP: TestContractNamespaceBalanceProjectionActualRuntime'not in backend_log
for run in namespace['runs']:
 result=json.loads((RUNTIME/('namespace-runtime/'+run['label']+'-analysis.json')).read_text())
 layout=json.loads(next(a['text']for a in result['sections']['artifacts']if a['kind']=='storage_layout'));ns=layout['namespace_storage']
 assert ns['complete']==(run['group']not in['bad-root','override'])
 if run['group']in['good','aliases','assembly','coupled']:
  assert all(t['complete']for t in result['sections']['state_transitions'])
  assert next(m for m in result['sections']['authorization_models']if m['kind']=='owner_identity_integrity')['complete']
  assert next(m for m in result['sections']['protocol_invariants']if m['kind']=='erc20_ledger_conservation')['complete']
  evidence={e['evidence_id']for e in result['sections']['evidence']}
  assert all(n['evidence_ids']and set(n['evidence_ids']).issubset(evidence)for n in ns['namespaces'])
 assert all(m['whole_authorization_proved']is False and m['whole_protocol_economics_proved']is False for m in result['sections']['verification_closure'])
basis=json.loads((BASE/'inherited-history-basis.json').read_text());assert basis['prior_rule_version']=='funds-semantics-10'and sha(BASE/'inherited-history-candidate-freeze.json')==basis['prior_candidate_freeze_sha256']
for item in basis['files']:assert sha(ROOT/item['path'])==item['sha256'],item['path']
usage=json.loads((SERVICE/'live-usage.json').read_text());previous=json.loads((SERVICE/'production-usage-baseline.json').read_text())['actual'];total={k:usage['actual'][k]+previous[k]for k in['http_requests','credits','estimated_usd']}
assert total['http_requests']<=2000 and total['credits']<=1000000 and total['estimated_usd']<=10,total
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
evidence=[{'path':str(p.relative_to(ROOT)),'sha256':sha(p),'byte_size':p.stat().st_size}for base in[ENGINE,RUNTIME,SERVICE,BASE/'inherited-geth-runtime',BASE/'inherited-geth-prestate',BASE/'source-validation',BASE/'prior-attempts']for p in sorted(base.rglob('*'))if p.is_file()and not p.is_symlink()]
evidence.extend({'path':str(p.relative_to(ROOT)),'sha256':sha(p),'byte_size':p.stat().st_size}for p in [BASE/'inherited-history-basis.json',BASE/'inherited-history-candidate-freeze.json',BASE/'qa-environment-restoration.json'])
manifest={'status':'PASS','scope':'verified_namespace_roots_packed_storage_aliases_authorization_ledger_and_settlement_projection','rule_version':'funds-semantics-12','image':image,'source_commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),'sources':sources,'built_files':built,'evidence_files':evidence,'engine_files':freeze['files'],'counts':counts,'usage':{'isolated_qa':usage['actual'],'previous_production':previous,'actual':total,'limits':usage['limits']},'production_pids':pids,'whole_goal_complete':False,'remaining':['namespace_dynamic_data_operations_unverified_roots_and_unmodeled_pointer_paths','unavailable_transaction_prestate_and_unregistered_block_rules_remain_explicit_gaps','dynamic_constructor_arguments_proxy_initialization_and_upgrade_continuity','coupled_unchecked_feedback_heap_and_general_loop_invariants','arbitrary_host_effects_external_dependencies_and_callbacks','cached_domains_contract_signatures_and_multisig_thresholds','protocol_specific_assets_prices_fees_rounding_debt_and_liquidation_models','full_declared_authorization_and_economics_closure']}
manifest['remaining'].insert(0,'namespace_permit_nonce_allowance_policy_roots_and_cross_entry_nonce_integrity')
(BASE/'candidate-freeze.json').write_text(json.dumps(manifest,indent=2));print('NAMESPACE_CANDIDATE_CERTIFIED',image,'SOURCE_FILES',len(sources),'BUILT_FILES',len(built),'EVIDENCE_FILES',len(evidence),'WHOLE_GOAL_REMAINS_ACTIVE',flush=True)
