"""Freeze the tested engine code and report measured acceptance only."""
import hashlib,json,pathlib,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];assert ROOT.name=='contract-deepening-20261015'
OUT=ROOT/'acceptance/contract-analysis-deepening';image=(OUT/'engine-image.txt').read_text().strip()
final=json.loads((OUT/'final-acceptance.json').read_text());assert final['status']=='PASS'and final['image']==image
gates=json.loads((OUT/'build-gates.json').read_text());assert all(g['status']=='PASS'for g in gates.values())
paths=[ROOT/'analysis-engines'/name for name in ['analyze.mjs','source.mjs','source-match.mjs','model.mjs','capabilities.mjs','adapters/python-engine.py']]
for folder in ['ir','rules','properties']:paths+=sorted(p for p in (ROOT/'analysis-engines'/folder).iterdir()if p.suffix in ['.py','.mjs'])
keys=['/engine/'+str(p.relative_to(ROOT/'analysis-engines'))for p in paths]
code='import hashlib,json,pathlib,sys;print(json.dumps({p:hashlib.sha256(pathlib.Path(p).read_bytes()).hexdigest() for p in sys.argv[1:]}))'
run=subprocess.run(['docker','run','--rm','--pull','never','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','1','--memory','128m','--pids-limit','16','--entrypoint','/opt/contract-python/bin/python',image,'-c',code,*keys],capture_output=True,check=True)
inside=json.loads(run.stdout);files=[]
for key,p in zip(keys,paths):
 sha=hashlib.sha256(p.read_bytes()).hexdigest();assert inside[key]==sha,p
 files.append({'path':str(p.relative_to(ROOT)),'sha256':sha})
(OUT/'engine-code-freeze.json').write_text(json.dumps({'status':'PASS','image':image,'files':files},indent=2))
gates['engine-code-freeze']={'status':'PASS'};(OUT/'build-gates.json').write_text(json.dumps(gates,indent=2))
runtime=json.loads((OUT/'deepening-runtime/report.json').read_text())
summary={'status':'PASS','rule_version':'funds-semantics-6','image':image,'runtime_checks':len(runtime['checks']),
 'legacy_expression_checks':len(json.loads((OUT/'expression-runtime/report.json').read_text())['checks']),
 'mechanism_checks':len(json.loads((OUT/'mechanism-runtime/report.json').read_text())['checks']),
 'gold_checks':len(json.loads((OUT/'gold/report.json').read_text())['checks']),
 'engine_files':len(files),'rpc_usage':json.loads((OUT/'live-usage.json').read_text())['actual'],
 'boundaries':['loop_remainders_require_invariants','assembly_memory_and_host_effects_require_models','source_return_substitution_requires_pc_witness','role_enumeration_and_signature_authorization_require_evidence','whole_protocol_economics_requires_explicit_invariants'],
 'all_paths_proved':False}
(OUT/'acceptance-summary.json').write_text(json.dumps(summary,indent=2));print('DEEPENING_CERTIFIED',json.dumps(summary),flush=True)
