"""Compare saved semantic rows against the frozen P1 candidate, by method."""
import argparse,collections,gc,hashlib,json,pathlib
p=argparse.ArgumentParser();p.add_argument('--context-match',default='');args=p.parse_args()
ROOT=pathlib.Path(__file__).resolve().parents[2];BASE=ROOT/'acceptance/contract-analysis-partial-recovery'
image=(BASE/'engine-image.txt').read_text().strip();out=BASE/('candidate-15-'+image[7:23])
prior=ROOT/'acceptance/contract-analysis-general/candidate-14-2c294c4501eab28c'
runs=json.loads((out/'runs.json').read_text());old={r['key']:r for r in json.loads((prior/'runs.json').read_text())}
assert len(old)==54
if args.context_match:
 assert all(c.isalnum()or c=='-'for c in args.context_match)
 runs=[r for r in runs if args.context_match in r['key']];assert runs
else:assert len(runs)==54
fields={
 'funds':['kind','asset','from_expression','to_expression','amount_expression','expanded_asset_expression','expanded_from_expression','expanded_to_expression','expanded_amount_expression','unit','settlement','conditions','condition_status'],
 'permissions':['condition','expanded_condition','conditions','failure','condition_status'],
 'storage':['access','slot','offset','type','variable','variable_type','key_expression','value_expression','operation','slot_role','conditions','code_address','storage_address'],
 'calls':['call_type','target','target_expression','value_expression','input_expression','input_arguments','failure_propagation','selector','signature','conditions'],
}
def load(directory,run):
 path=directory/(run['key']+'-output.json');raw=path.read_bytes();assert hashlib.sha256(raw).hexdigest()==run['files'][path.name]
 return json.loads(raw)['sections']
def method_key(m):return (m.get('entry')or'<implicit>',m.get('selector')or'',m.get('signature')or m.get('name')or'',m.get('dispatch_role')or'',m.get('entry_kind')or'')
def row_counter(sections,section,keys):
 methods={m['method_id']:method_key(m)for m in sections['methods']}
 return collections.Counter(json.dumps({'method':methods.get(r.get('method_id')),'row':{k:r.get(k)for k in keys}},sort_keys=True,separators=(',',':'))for r in sections.get(section,[]))
results=[]
for run in runs:
 assert run['transport_status']=='completed'and run.get('container_execution',{}).get('scope')=='after_engine_exit_and_complete_output_forwarding'
 p=old[run['key']]
 if p['transport_status']!='completed':
  results.append({'key':run['key'],'prior_transport':p['transport_status'],'current_transport':run['transport_status'],'new_readable_result':True});continue
 before=load(prior,p);after=load(out,run)
 missing={}
 old_methods={method_key(m)for m in before['methods']};current_methods={method_key(m)for m in after['methods']}
 if old_methods-current_methods:missing['methods']=[list(m)for m in sorted(old_methods-current_methods)]
 for section,keys in fields.items():
  absent=row_counter(before,section,keys)-row_counter(after,section,keys)
  if absent:missing[section]={'missing_rows':sum(absent.values()),'examples':[{'count':n,**json.loads(v)}for v,n in list(absent.items())[:4]]}
 prior_proofs=collections.Counter((m.get('kind'),m.get('scope'))for section in['authorization_models','protocol_invariants']for m in before.get(section,[])if m.get('complete'))
 current_proofs=collections.Counter((m.get('kind'),m.get('scope'))for section in['authorization_models','protocol_invariants']for m in after.get(section,[])if m.get('complete'))
 if prior_proofs-current_proofs:missing['prior_complete_models']=[{'kind':kind,'scope':scope,'missing':n}for(kind,scope),n in(prior_proofs-current_proofs).items()]
 results.append({'key':run['key'],'missing_prior_semantics':missing,'sevm':next((e for e in after['engines']if e['engine']=='sevm'),None),
  'source':next((e for e in after['engines']if e['engine']=='solc-ast'),None),'wall_seconds':run['wall_seconds'],
  'prior_wall_seconds':p['wall_seconds'],'container_execution':run['container_execution']})
 del before,after;gc.collect()
regressions=[r['key']for r in results if r.get('missing_prior_semantics')]
document={'status':'PASS'if not regressions else'REVIEW_REQUIRED','image':image,'prior_image':'sha256:2c294c4501eab28cfb7947154b79dbca74cc0350167c4e664d6761cacca9b7ec',
 'scope':'method identity, guards, state effects, calls, precise money expressions and previously complete local model statuses',
 'runs':results,'regression_candidates':regressions,'business_quality':'separate sealed-fact assessment; no fresh blind claim'}
(out/('semantic-regression'+('-'+args.context_match if args.context_match else'')+'.json')).write_text(json.dumps(document,ensure_ascii=False,indent=2))
print(json.dumps({'status':document['status'],'runs':len(results),'regression_candidates':regressions,'recovered_transport_runs':sum(r.get('new_readable_result',False)for r in results)},ensure_ascii=False),flush=True)
