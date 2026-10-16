"""Freeze all runtime engine sources against the tested content digest."""
import hashlib,json,pathlib,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-verification-20261015'
OUT=ROOT/'acceptance/contract-analysis-verification';image=(OUT/'engine-image.txt').read_text().strip()
final=json.loads((OUT/'final-acceptance.json').read_text());assert final['status']=='PASS'and final['image']==image
gates=json.loads((OUT/'build-gates.json').read_text());assert all(g['status']=='PASS'for g in gates.values())
engine=ROOT/'analysis-engines';paths=sorted(engine.glob('*.mjs'))
for folder in ['ir','rules','properties','verification','adapters']:paths+=sorted(p for p in(engine/folder).iterdir()if p.suffix in['.py','.mjs'])
keys=['/engine/'+str(p.relative_to(engine))for p in paths]
code='import hashlib,json,pathlib,sys;print(json.dumps({p:hashlib.sha256(pathlib.Path(p).read_bytes()).hexdigest() for p in sys.argv[1:]}))'
run=subprocess.run(['docker','run','--rm','--pull','never','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','1','--memory','128m','--pids-limit','16','--entrypoint','/opt/contract-python/bin/python',image,'-c',code,*keys],capture_output=True,check=True)
inside=json.loads(run.stdout);files=[]
for key,p in zip(keys,paths):
 sha=hashlib.sha256(p.read_bytes()).hexdigest();assert inside[key]==sha,p;files.append({'path':str(p.relative_to(ROOT)),'sha256':sha})
(OUT/'engine-code-freeze.json').write_text(json.dumps({'status':'PASS','image':image,'files':files},indent=2))
gates['engine-code-freeze']={'status':'PASS'};(OUT/'build-gates.json').write_text(json.dumps(gates,indent=2))
runtime=json.loads((OUT/'verification-runtime/report.json').read_text());gold=json.loads((OUT/'gold/report.json').read_text())
summary={'status':'PASS','rule_version':'funds-semantics-7','image':image,'runtime_checks':len(runtime['checks']),
 'previous_runtime_checks':len(json.loads((OUT/'deepening-runtime/report.json').read_text())['checks']),
 'gold_checks':len(gold['checks']),'gold_samples':len(gold['runs']),'engine_source_files':len(files),
 'exact_callsite_bindings':len([o for o in json.loads((OUT/'verification-runtime/exact-return-observations.json').read_text())if o['source_binding_status']=='exact_program_counter_and_execution_context']),
 'rpc_usage':json.loads((OUT/'live-usage.json').read_text())['actual'],'all_paths_proved':False,
 'remaining':['non_affine_or_heap_dependent_loop_invariants','arbitrary_assembly_host_effects_and_compiler_memory_aliases','generated_shared_call_helper_source_context',
  'historical_creation_proxy_initialization_upgrade_and_callback_models','signature_domain_nonce_threshold_models','protocol_specific_external_assets_fees_prices_and_rounding']}
(OUT/'acceptance-summary.json').write_text(json.dumps(summary,indent=2));print('VERIFICATION_CERTIFIED',json.dumps(summary),flush=True)
