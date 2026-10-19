"""Audit the nonce candidate's actual engine, runtime, backend and scoped MCP evidence."""
import hashlib,json,pathlib,re,subprocess,datetime
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-permit-nonces-20261016'
BASE=ROOT/'acceptance/contract-analysis-permit-nonces';ENGINE=BASE/'frozen-engine';RUNTIME=BASE/'frozen-validation';SERVICE=BASE/'service-validation'
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
load=lambda p:json.loads(p.read_text())
freeze=load(ENGINE/'engine-source-freeze.json');image=freeze['image'];assert freeze['rule_version']=='funds-semantics-13'
for item in freeze['files']:assert sha(ROOT/item['path'])==item['sha256'],item['path']
engine=load(RUNTIME/'final-acceptance.json');service=load(SERVICE/'acceptance.json')
assert all(d['status']=='PASS'and d['image']==image for d in [engine,service])
for name in ['engine-unit','permit-runtime','signature-runtime','namespace-runtime']:
 assert engine['gates'][name]['status']=='PASS'and engine['gates'][name]['image']==image
unit=(RUNTIME/'engine-unit.log').read_text();assert re.search(r'tests\s+137',unit)and re.search(r'pass\s+137',unit)and re.search(r'fail\s+0',unit)and re.search(r'skipped\s+0',unit)
counts={'engine_unit':137}
reproduction=load(BASE/'interface-reproducer/report.json');assert reproduction['checks'][-1]['label']=='fixed'and reproduction['checks'][-1]['status']=='PASS'and reproduction['checks'][-1]['image']==image
assert reproduction['checks'][0]['nonce_model_present']is False and reproduction['checks'][-1]['nonce_model_present']is True
for name in ['permit-runtime','signature-runtime','namespace-runtime']:
 d=load(RUNTIME/name/'report.json');assert d['complete']and d['image']==image and all(c['status']=='PASS'for c in d['checks']);counts[name]=len(d['checks'])
permit=load(RUNTIME/'permit-runtime/report.json');assert len(permit['runs'])==40 and len(permit['executions'])==120
assert all(any(r['source_group']==g and r['optimizer']==optimized and r['via_ir']==via for r in permit['runs'])for g in ['ordinary','namespace','aliases','unrelated','reset','restored','invalidate','wrap','raw']for optimized in [False,True]for via in [False,True])
assert sum(r['nonce_status']=='counterexample'for r in permit['runs'])==12
assert sum(r['nonce_status']=='incomplete'for r in permit['runs'])==4
environment=load(RUNTIME/'permit-runtime/local-genesis-environment.json');assert environment['gas_canary']['observed']==environment['gas_canary']['expected']==23254
transient=load(RUNTIME/'transient-revert/report.json');assert transient['image']==image and len(transient['checks'])==3 and all(c['status']=='PASS'for c in transient['checks']);counts['transient_revert_state_checks']=3
backend=(SERVICE/'backend-contract-regressions.log').read_text();name='TestContractPermitActualRuntimeProofsSurviveBackendParsingAndScoping'
assert '--- PASS: '+name in backend and '--- SKIP: '+name not in backend
assert '--- PASS: TestContractNamespaceBalanceProjectionActualRuntime'in backend
for name in ['execution-live-qa.json','authority-live-qa.json','live-api-qa.json','mcp-live-qa.json','permit-mcp-qa.json']:
 path=SERVICE/name
 if not path.exists():path=SERVICE/'inherited'/name
 d=load(path);assert d and all(c['status']=='PASS'for c in d),name;counts[('unchanged_platform_basis/'if path.parent.name=='inherited'else'')+name]=len(d)
if(SERVICE/'platform-regression-basis.json').exists():
 for item in load(SERVICE/'platform-regression-basis.json')['sources']:assert sha(ROOT/item['path'])==item['sha256'],item['path']
saved=load(SERVICE/'permit-mcp-model.json');m=saved['model'];assert m['kind']=='permit_nonce_monotonicity'and m['permit_model_id']and m['entry_coverage']
assert saved['proof']['property']['property_id']==saved['obligation']['property_id']and saved['proof']['property']['proof_artifact_id']==saved['artifact']['artifact_id']
if m['status']=='counterexample':
 assert saved['obligation']['status']=='counterexample'and saved['proof']['property']['counterexample']['values_smt']==saved['obligation']['counterexample']['values_smt']and saved['proof']['property']['solver']['decision']=='sat'
assert not m.get('whole_authorization_proved',False)
usage=load(SERVICE/'live-usage.json');assert usage['actual']['http_requests']<=2000 and usage['actual']['credits']<=1000000 and usage['actual']['estimated_usd']<10
baseline=load(SERVICE/'production-pids-before.json');current={u:subprocess.check_output(['systemctl','--user','show',u,'-p','MainPID','--value'],text=True).strip()for u in baseline};assert baseline==current
paths=set(subprocess.check_output(['git','ls-files','-z'],cwd=ROOT).decode().split('\0'))|set(subprocess.check_output(['git','ls-files','--others','--exclude-standard','-z'],cwd=ROOT).decode().split('\0'))
sources=[]
for name in sorted(paths):
 p=pathlib.PurePosixPath(name)
 if not name or p.parts[0]not in ['analysis-engines','backend','mcp','frontend','scripts','docs','openapi']or {'node_modules','dist','bin','acceptance','acceptance-evidence','__pycache__'}.intersection(p.parts)or p.name.startswith('.env')or not(ROOT/name).is_file():continue
 sources.append({'path':name,'sha256':sha(ROOT/name)})
built=[ROOT/'backend/bin/contract-analysis-api',ROOT/'backend/bin/contract-analysis-worker',*ROOT.joinpath('mcp/dist').rglob('*.js'),*ROOT.joinpath('frontend/apps/user-console/dist').rglob('*')]
binaries=[{'path':str(p.relative_to(ROOT)),'sha256':sha(p),'bytes':p.stat().st_size}for p in sorted(built)if p.is_file()]
assert all((ROOT/n).is_file()for n in ['backend/bin/contract-analysis-api','backend/bin/contract-analysis-worker','mcp/dist/server.js','frontend/apps/user-console/dist/index.html'])
evidence=[{'path':str(p.relative_to(ROOT)),'sha256':sha(p),'bytes':p.stat().st_size}for p in sorted(BASE.rglob('*'))if p.is_file()and not p.is_symlink()and p.name not in ['candidate-freeze.json','publication-summary.json']and p.suffix not in ['.gz','.sha256']]
summary={'status':'isolated_candidate_accepted_not_deployed','rule_version':'funds-semantics-13','image':image,'production_rule_version':'funds-semantics-8','source_commit_base':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),'source_state':'preserved_worktree_with_frozen_incremental_changes','time_beijing':datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8))).isoformat(),'counts':counts,'nonce_runtime':{'compile_deploy_variants':40,'transactions':120,'development_scenarios':10,'independent_real_business_implementations':0},'real_mcp_nonce_sample':{'analysis_id':saved['analysis_id'],'status':m['status'],'unresolved':m['unresolved']},'usage':usage,'whole_goal_complete':False,'remaining':['direct_source_and_bytecode_MCP_submission_pending_new_P1_approval','general_memory_ABI_storage_merge_and_interprocedural_cross_contract_dataflow','cached_domains_contract_signatures_multisig_and_threshold_models','initialization_upgrade_and_historical_state_continuity','general_loop_host_callback_price_fee_and_liquidation_models','whole_authorization_and_protocol_economics_closure']}
(BASE/'publication-summary.json').write_text(json.dumps(summary,indent=2))
(BASE/'candidate-freeze.json').write_text(json.dumps({**summary,'engine_files':freeze['files'],'sources':sources,'built_files':binaries,'evidence_files':evidence,'production_pids':current},indent=2))
print('PERMIT_CANDIDATE_CERTIFIED',image,'SOURCE_FILES',len(sources),'EVIDENCE_FILES',len(evidence),'WHOLE_GOAL_REMAINS_ACTIVE',flush=True)
