"""Compare saved source formulas for the same real pinned contract sample."""
import collections,json,os,pathlib
from qa_support import ROOT,fixture_clients
from live_api_qa import all_pages,artifact_bytes
OUT=ROOT/os.environ.get('CONTRACT_QA_OUT','acceptance/contract-analysis-expressions');rows=[]
def check(name,ok,evidence=None):
 rows.append({'scenario':name,'status':'PASS'if ok else'FAIL','evidence':evidence});(OUT/'expression-evidence-qa.json').write_text(json.dumps(rows,indent=2));print(name,rows[-1]['status'],flush=True)
 if not ok:raise AssertionError(name)
c=fixture_clients()['investigator']
def load(aid):
 arts=all_pages(c,aid,'artifacts',100);a=next(a for a in arts if a['kind']=='machine_result');return json.loads(artifact_bytes(c,aid,a,1<<20))
aid=(OUT/'dependency-analysis-id.txt').read_text().strip();current=load(aid)
previous_id=(ROOT.parent/'contract-mechanisms-20261015/acceptance/contract-analysis-mechanisms/dependency-analysis-id.txt').read_text().strip();previous=load(previous_id)
check('same-public-code-and-pinned-block',current['snapshot']['code_hash']==previous['snapshot']['code_hash']and current['snapshot']['block_hash']==previous['snapshot']['block_hash'],{'current':aid,'previous':previous_id,'code_hash':current['snapshot']['code_hash'],'block_hash':current['snapshot']['block_hash']})
def inventory(model):
 methods={m['method_id']:m for m in model['sections']['methods']};entries={}
 for d in model['sections']['dataflow']:
  if d['kind']!='entry_return_derivation':continue
  m=methods[d['method_id']];entries[(m['code_address'].lower(),m['signature'])]={'method':m['signature'],'status':d['status'],'paths':len(d.get('successful_return_paths',[])),'unresolved':d.get('unresolved',[]),'ordered_evaluations':sum(len(p.get('execution_steps',[]))for p in d.get('successful_return_paths',[]))}
 for m in methods.values():
  if m.get('entry_return_model_unresolved')and (m['code_address'].lower(),m['signature'])not in entries:entries[(m['code_address'].lower(),m['signature'])]={'method':m['signature'],'status':'partial','unresolved':m['entry_return_model_unresolved'],'ordered_evaluations':0}
 return entries
before,after=inventory(previous),inventory(current)
improved=[{'code_address':key[0],**value,'previous_status':before.get(key,{}).get('status'),'previous_unresolved':before.get(key,{}).get('unresolved')}for key,value in after.items()if value['status']=='represented_source_dependencies_resolved'and before.get(key,{}).get('status')!='represented_source_dependencies_resolved']
def calls(model):
 methods={m['method_id']:m for m in model['sections']['methods']};flows={d.get('expression_id'):d for d in model['sections']['dataflow']if d['kind']=='internal_return_derivation'};found={}
 for e in model['sections']['expressions']:
  if e.get('op')!='FunctionCall'or e.get('entry_return_summary')or not any('.'+op+'('in e['text']for op in ['add','sub','mul']):continue
  m=methods[e['method_id']];key=(m['code_address'].lower(),m['signature'],e['path'],e['text']);found[key]=(e,flows.get(e['expression_id'],{}))
 return found
old_calls,new_calls=calls(previous),calls(current);closed=[]
for key,(e,flow)in new_calls.items():
 old=old_calls.get(key,({},{}))[0]
 if old.get('unresolved_return_value')=='INTERNAL_RETURN_EXPRESSION_SIDE_EFFECT'and e.get('internal_return_model')and not e.get('unresolved_return_value'):
  closed.append({'code_address':key[0],'method':key[1],'call':key[3],'previous_gap':old['unresolved_return_value'],'current_status':flow.get('status'),'remaining':flow.get('unresolved',[])})
check('real-safemath-assignment-gaps-closed',all(any('.'+op+'('in row['call']for row in closed)for op in ['add','sub','mul']),closed)
ordered=[v for v in after.values()if v['ordered_evaluations']>0];check('ordered-calculations-persist-in-real-artifact',bool(ordered),{'methods':len(ordered),'evaluation_steps':sum(v['ordered_evaluations']for v in ordered)})
details=all_pages(c,aid,'method_details',100)
check('method-details-persist-calculation-domain',any(any(v.get('successful_return_paths')for v in d.get('returned_values',[]))for d in details),{'method_details':len(details)})
(OUT/'semantic-gap-comparison.json').write_text(json.dumps({'sample':'Uniswap v2 pair-interface and pinned dependencies','current_analysis_id':aid,'previous_analysis_id':previous_id,'improved_methods':improved,'closed_safemath_expression_gaps':closed,'before_statuses':dict(collections.Counter(v['status']for v in before.values())),'after_statuses':dict(collections.Counter(v['status']for v in after.values())),'all_contracts_coverage_rate':None},indent=2))
