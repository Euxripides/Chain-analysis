"""Summarize actual gate records; incomplete evidence cannot become a PASS."""
import json,pathlib,re
ROOT=pathlib.Path(__file__).resolve().parents[2];OUT=ROOT/'acceptance/contract-analysis-expressions'
read=lambda p:json.loads((OUT/p).read_text())
image=(OUT/'engine-image.txt').read_text().strip()
assert read('final-acceptance.json')['status']=='PASS'and read('final-acceptance.json')['image']==image
groups={}
for name,path in {'api':'live-api-qa.json','mcp':'mcp-live-qa.json','transaction':'execution-live-qa.json','lifecycle':'lifecycle-qa.json','fault':'resource-fault-qa.json','authority':'authority-live-qa.json','dependencies':'dependency-live-qa.json','browser':'browser/report.json','authority_browser':'authority-browser/report.json','mechanism_browser':'mechanism-browser/report.json','existing_runtime':'runtime-fixtures/report.json','expression_evidence':'expression-evidence-qa.json'}.items():
 rows=read(path);assert rows and all(r['status']=='PASS'for r in rows),path;groups[name]=len(rows)
gold=read('gold/report.json');assert gold['image']==image and len(gold['runs'])==29 and all(r['status']=='PASS'for r in gold['checks'])
runtime=read('mechanism-runtime/report.json');assert runtime['image']==image and runtime['complete']and all(r['status']=='PASS'for r in runtime['checks'])
old=read('return-runtime/report.json');assert old['image']==image and all(r['status']=='PASS'for r in old['checks'])
gates=read('build-gates.json');assert all(gates[k]['status']=='PASS'for k in ['engine-unit','go-test','qa-setup','frontend-contract','frontend-typecheck-all','frontend-render-all','report-go-test','report-api-build','report-worker-build','report-dependency'])
log=(OUT/'engine-unit.log').read_text();tests=re.search(r'tests (\d+)',log);passed=re.search(r'pass (\d+)',log);skipped=re.search(r'skipped (\d+)',log)
assert tests and passed and skipped and tests[1]==passed[1]and skipped[1]=='0'
summary={'qa':'PASS','rules':'funds-semantics-5','image':image,'engine_tests':int(tests[1]),'gold_samples':29,'gold_checks':len(gold['checks']),'local_execution':{'new_checks':len(runtime['checks']),'return_regression_checks':len(old['checks']),'existing_execution_replay_checks':groups['existing_runtime'],'simulation':True,'all_paths_proved':False},'live_checks':groups,'gates':gates,'usage':read('live-usage.json'),'remaining':['dynamic_loop_bounds','complex_assembly','stateful_return_models','storage_aliases','dynamic_external_return_binding','complete_role_enumeration','complex_signature_and_multisig_authorization','general_protocol_economic_proofs','missing_historical_witnesses']}
new=read('expression-runtime/report.json');assert new['image']==image and new['complete']and all(r['status']=='PASS'for r in new['checks']);summary['local_execution']['expression_checks']=len(new['checks']);summary['local_execution']['total_checks']=len(new['checks'])+len(runtime['checks'])+len(old['checks'])+groups['existing_runtime'];summary['gap_comparison']=read('semantic-gap-comparison.json')
for name,path in {'production_mcp':'release/production-qa.json','production_browser':'release/production-render-qa.json','production_integrity':'release/production-integrity-qa.json'}.items():
 if(OUT/path).exists():
  rows=read(path);assert rows and all(r['status']=='PASS'for r in rows);summary['live_checks'][name]=len(rows)
if(OUT/'release/manifest.json').exists():
 manifest=read('release/manifest.json');summary['release']={k:manifest[k]for k in ['status','source_commit','image','original_pids']};assert manifest['image']==image
if(OUT/'release/mcp-sync-check.json').exists():
 sync=read('release/mcp-sync-check.json');summary['mcp_sync']=sync
(OUT/'acceptance-summary.json').write_text(json.dumps(summary,indent=2));print(json.dumps({k:summary[k]for k in ['qa','rules','engine_tests','gold_samples','gold_checks','local_execution','live_checks']},indent=2))
