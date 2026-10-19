"""Compare exact semantic fields to four retained candidates, without re-labelling exposed material as blind."""
import collections,gc,hashlib,json,pathlib
ROOT=pathlib.Path(__file__).resolve().parents[2];BASE=ROOT/'acceptance/contract-analysis-aggregates';image=(BASE/'engine-image.txt').read_text().strip();out=BASE/'corpus'/('candidate-19-'+image[7:23]);runs=json.loads((out/'runs.json').read_text());assert len(runs)==54
old15=ROOT/'acceptance/contract-analysis-partial-recovery';image15=(old15/'engine-image.txt').read_text().strip();prior15=old15/('candidate-15-'+image15[7:23]);prior14=ROOT/'acceptance/contract-analysis-general/candidate-14-2c294c4501eab28c'
fields={
 'funds':['kind','asset','from_expression','to_expression','amount_expression','expanded_asset_expression','expanded_from_expression','expanded_to_expression','expanded_amount_expression','unit','settlement','conditions','condition_status'],
 'permissions':['condition','expanded_condition','conditions','failure','condition_status'],
 'storage':['access','slot','offset','type','variable','variable_type','key_expression','value_expression','operation','slot_role','conditions','code_address','storage_address'],
 'calls':['call_type','target','target_expression','value_expression','input_expression','input_arguments','failure_propagation','selector','signature','conditions']}
def load(folder,r):
 p=folder/(r['key']+'-output.json');raw=p.read_bytes();assert hashlib.sha256(raw).hexdigest()==r['files'][p.name];return json.loads(raw)['sections']
def method_key(m):return(m.get('entry')or'<implicit>',m.get('selector')or'',m.get('signature')or m.get('name')or'',m.get('dispatch_role')or'',m.get('entry_kind')or'')
def counter(s,section):
 methods={m['method_id']:method_key(m)for m in s['methods']};return collections.Counter(json.dumps({'method':methods.get(r.get('method_id')),'row':{k:r.get(k)for k in fields[section]}},sort_keys=True,separators=(',',':'))for r in s.get(section,[]))
prior16=ROOT/'acceptance/contract-analysis-memory/corpus/candidate-16-1a7a80312d9f72e4'
results=[]
prior17=ROOT/'acceptance/contract-analysis-storage/corpus/candidate-17-e0bef21df97cca9c'
prior18=ROOT/'acceptance/contract-analysis-scalars/corpus/candidate-18-11a3faf50a22aa84'
for prior in[prior14,prior15,prior16,prior17,prior18]:
 previous={r['key']:r for r in json.loads((prior/'runs.json').read_text())};assert len(previous)==54
 for run in runs:
  assert run['image']==image and run['transport_status']=='completed'and run.get('container_execution',{}).get('scope')=='after_engine_exit_and_complete_output_forwarding'
  beforeRun=previous[run['key']]
  if beforeRun['transport_status']!='completed':results.append({'prior_directory':str(prior),'key':run['key'],'new_readable_result':True});continue
  before=load(prior,beforeRun);after=load(out,run);missing={};additions={}
  prior_methods={method_key(m)for m in before['methods']};current_methods={method_key(m)for m in after['methods']}
  if prior_methods-current_methods:missing['methods']=[list(m)for m in sorted(prior_methods-current_methods)]
  for section in fields:
   old=counter(before,section);new=counter(after,section);removed=old-new;added=new-old
   if removed:missing[section]={'count':sum(removed.values()),'examples':[{'count':n,**json.loads(v)}for v,n in list(removed.items())[:6]]}
   if added:additions[section]={'count':sum(added.values()),'examples':[{'count':n,**json.loads(v)}for v,n in list(added.items())[:6]]}
  oldProofs=collections.Counter((m.get('kind'),m.get('scope'))for section in['authorization_models','protocol_invariants']for m in before.get(section,[])if m.get('complete'))
  newProofs=collections.Counter((m.get('kind'),m.get('scope'))for section in['authorization_models','protocol_invariants']for m in after.get(section,[])if m.get('complete'))
  if oldProofs-newProofs:missing['prior_complete_models']=[{'kind':k,'scope':s,'missing':n}for(k,s),n in(oldProofs-newProofs).items()]
  results.append({'prior_directory':str(prior),'key':run['key'],'missing':missing,'added':additions,'new_memory_versions':len(after.get('memory_versions',[])),
   'new_source_bytes_versions':len(after.get('source_memory_versions',[])),'new_call_input_bindings':len(after.get('call_input_bindings',[])),'new_ABI_values':len(after.get('abi_values',[])),
   'wall_seconds':run['wall_seconds'],'container_execution':run['container_execution']})
  del before,after;gc.collect()
regressions=[{'prior_directory':r['prior_directory'],'key':r['key'],'missing':r['missing']}for r in results if r.get('missing')];document={'status':'PASS'if not regressions else'REVIEW_REQUIRED','image':image,
 'scope':'exact method identities, guards, state effects, calls, money expressions and prior complete local models; additions are not whole business acceptance',
 'fresh_blind_samples':0,'runs':results,'regression_candidates':regressions}
(out/'semantic-regression.json').write_text(json.dumps(document,ensure_ascii=False,indent=2));print('P2_AGGREGATE_SEMANTIC_COMPARE',document['status'],'REGRESSIONS',len(regressions),flush=True)
