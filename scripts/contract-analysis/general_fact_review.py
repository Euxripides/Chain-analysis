"""Produce evidence-linked review packets; never score facts from names/counts."""
import argparse,json,pathlib
ROOT=pathlib.Path(__file__).resolve().parents[2];OUT=ROOT/'acceptance/contract-analysis-general'
p=argparse.ArgumentParser();p.add_argument('--phase',required=True);p.add_argument('--set',required=True,choices=['development','blind']);p.add_argument('--sample');p.add_argument('--channel',default='source',choices=['source','hidden_source','submitted_source']);p.add_argument('--concise',action='store_true');a=p.parse_args()
labels=json.loads((OUT/'corpus'/('labels-'+a.set+'.json')).read_text());directory=OUT/a.phase
reviews=[]
for label in labels:
 if a.sample and label['key']!=a.sample:continue
 for fact in label['business_facts']:
  path=directory/(fact['context']+'-'+a.channel+'-output.json')
  if not path.exists():continue
  try:value=json.loads(path.read_text())
  except ValueError:
   reviews.append({'fact_id':fact['fact_id'],'expected':fact['expected'],'status':'unknown','reason':'engine_did_not_return_valid_result'});continue
  s=value['sections'];by_evidence={e['evidence_id']:e for e in s['evidence']};anchor=fact['evidence']
  methods=[m for m in s['methods']if m.get('name')==fact['function']]
  exact=[m for m in methods if any((by_evidence.get(e,{}).get('location',{}).get('file'),by_evidence.get(e,{}).get('location',{}).get('start'))==(anchor['file'],anchor['start'])for e in m.get('evidence_ids',[]))]
  if exact:methods=exact
  mids={m['method_id']for m in methods}
  packet={'fact_id':fact['fact_id'],'expected':fact['expected'],'dimension':fact['dimension'],'anchor':{k:v for k,v in anchor.items()if k!='text'},
          'source_engine':[e for e in s['engines']if e['engine']=='solc-ast'],'methods':methods,
          'rows':{k:[r for r in s.get(k,[])if r.get('method_id')in mids]for k in ['permissions','storage','funds','calls','dataflow','state_transitions','control_effects','protocol_formulas','steps']},
          'assessment':'pending_human_review_of_structured_facts_and_dependency_gaps'}
  reviews.append(packet)
target=directory/('review-'+a.set+('-'+a.sample if a.sample else'')+(''if a.channel=='source'else'-'+a.channel)+'.json');target.write_text(json.dumps(reviews,indent=2))
for r in reviews:
 print('FACT',r['fact_id'],r['expected'])
 if 'reason'in r:print('UNKNOWN',r['reason']);continue
 print('SOURCE',[(e['status'],e.get('error_code'))for e in r['source_engine']],'METHODS',[(m['method_id'],m.get('signature'),m.get('dispatch_role'))for m in r['methods']])
 fields={'permissions':['condition','expanded_condition','condition_derivation_status'],'storage':['access','key_expression','value_expression','operation','conditions'],
         'funds':['kind','asset','from_expression','to_expression','amount_expression','expanded_amount_expression','amount_derivation_status','settlement'],'calls':['call_type','target_expression','signature','input_arguments','conditions'],
         'dataflow':['kind','original_expression','symbolic_expression','unresolved'],'state_transitions':['complete','status','unresolved'],'steps':['kind','text','conditions']}
 for section,keys in fields.items():
  rows=r['rows'].get(section,[])
  if section=='storage':rows=[row for row in rows if row.get('access')=='write']
  if section=='dataflow':rows=[row for row in rows if row.get('unresolved')or row.get('kind')in['entry_return_derivation','return_derivation']]
  if section=='steps':rows=[row for row in rows if row.get('kind')in['guard','branch','revert','return']]
  values=[{k:row[k]for k in keys if k in row}for row in rows]
  if a.concise:
   if section=='permissions':values=[{k:v for k,v in v.items()if k!='expanded_condition'}for v in values]
   if section=='dataflow':values=[{'kind':v.get('kind'),'original':v.get('original_expression'),'unresolved':v.get('unresolved',[]),'symbolic':v.get('symbolic_expression','')[:360]}for v in values]
   values=[{k:(v[:300]+' [DISPLAY ONLY]'if isinstance(v,str)and len(v)>300 else v)for k,v in value.items()}for value in values]
   if section=='dataflow':values=values[:8]
  text=json.dumps(values,ensure_ascii=False)
  limit=2200 if a.concise else 5000
  print(section,text if len(text)<=limit else text[:limit]+' [DISPLAY TRUNCATED; FULL ROWS IN REVIEW PACKET]')
print('REVIEW_PACKET',target)
