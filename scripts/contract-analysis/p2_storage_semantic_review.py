"""Review exact deltas using compiler short-circuit locations and versioned source encoding.

The raw strict comparison remains unchanged. Unknown deltas remain failures.
No amount, recipient, storage root, method or previously complete proof may be
discarded by this review.
"""
import argparse,collections,gc,hashlib,json,pathlib,re,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];BASE=ROOT/'acceptance/contract-analysis-storage';p=argparse.ArgumentParser();p.add_argument('--image');args=p.parse_args();image=args.image or(BASE/'engine-image.txt').read_text().strip();assert re.fullmatch(r'sha256:[a-f0-9]{64}',image)
directory=BASE/'corpus'/('candidate-17-'+image[7:23]);strict=json.loads((directory/'semantic-regression.json').read_text());runs={r['key']:r for r in json.loads((directory/'runs.json').read_text())};out=directory/'semantic-review';out.mkdir(exist_ok=True)
sha=lambda b:hashlib.sha256(b).hexdigest();fields={
 'funds':['kind','asset','from_expression','to_expression','amount_expression','expanded_asset_expression','expanded_from_expression','expanded_to_expression','expanded_amount_expression','unit','settlement','conditions','condition_status'],
 'permissions':['condition','expanded_condition','conditions','failure','condition_status'],
 'storage':['access','slot','offset','type','variable','variable_type','key_expression','value_expression','operation','slot_role','conditions','code_address','storage_address'],
 'calls':['call_type','target','target_expression','value_expression','input_expression','input_arguments','failure_propagation','selector','signature','conditions']}
def method(m):return(m.get('entry')or'<implicit>',m.get('selector')or'',m.get('signature')or m.get('name')or'',m.get('dispatch_role')or'',m.get('entry_kind')or'')
def rows(s,name):
 methods={m['method_id']:method(m)for m in s['methods']};return[(json.dumps([methods.get(r.get('method_id')),{k:r.get(k)for k in fields[name]},source_occurrence(s,r)],sort_keys=True),r)for r in s.get(name,[])]

def source_occurrence(s,row):
 if '_occurrence_indexes'not in s:s['_occurrence_indexes']=({e['expression_id']:e for e in s['expressions']},{e['evidence_id']:e for e in s['evidence']})
 expressions,evidence=s['_occurrence_indexes'];e=expressions.get(row.get('key_expression_id')or row.get('target_expression_id')or row.get('expression_id'),{})
 locations=[{k:v for k,v in evidence[i]['location'].items()if k!='artifact_id'}for i in row.get('evidence_ids',[])if i in evidence]
 # A row's first source evidence is its call/guard/access site. ABI recovery
 # appends encoder evidence; those supporting spans are not a second occurrence.
 return {'locations':locations[:1],'path':e.get('path')}

def same_source_occurrence(before,after,old,new):
 a=source_occurrence(old,before);b=source_occurrence(new,after)
 # Conditional evaluation gained explicit path segments. Their predicates still
 # require the separate compiler proof below; call and modifier frames stay exact.
 path=lambda p:re.sub(r'/(?:short-circuit:\d+|ternary:\d+:(?:true|false))','',p or'')
 return bool(a['locations'])and a['locations']==b['locations']and path(a['path'])==path(b['path'])
def projection(s,r,section,ignored):
 m=next(m for m in s['methods']if m['method_id']==r['method_id']);return json.dumps([method(m),{k:r.get(k)for k in fields[section]if k not in ignored}],sort_keys=True)
def load(folder,key,record):
 raw=(folder/(key+'-output.json')).read_bytes();assert sha(raw)==record['files'][key+'-output.json'];return json.loads(raw)['sections']
reference_cache={}
def reference(key):
 context=key.removesuffix('-submitted_source').removesuffix('-source').removesuffix('-hidden_source')
 if context not in reference_cache:
  path=ROOT/'acceptance/contract-analysis-general/corpus/materials'/context/'source-provider.json';raw=path.read_bytes();value=json.loads(subprocess.check_output(['/home/euripides/.nvm/versions/node/v24.20.0/bin/node','scripts/contract-analysis/p2_storage_condition_reference.mjs'],cwd=ROOT,input=raw))
  assert value['source_provider_sha256']==sha(raw);(out/(context+'-compiler-conditions.json')).write_text(json.dumps(value,indent=2));reference_cache[context]=value
 return reference_cache[context]
def condition_proof(before,after,s,ref):
 old=list(before.get('conditions')or[]);new=list(after.get('conditions')or[]);used=[];cursor=0;removed=[]
 expressions={e['expression_id']:e for e in s['expressions']};e=expressions.get(after.get('key_expression_id')or after.get('target_expression_id')or after.get('expression_id'),{});path=after.get('path')or e.get('path','')
 ids=[a or'ternary:'+b+':'+c for a,b,c in re.findall(r'/(?:short-circuit:(\d+)|ternary:(\d+):(true|false))',path)];choices=[(key,ref['conditions'][key])for key in ids if key in ref['conditions']];evidence={e['evidence_id']:e for e in s['evidence']};locations=[evidence[k]['location']for k in after.get('evidence_ids',[])if k in evidence];proofs=[]
 def inside(candidate):
  spans=[candidate['evaluated_expression_span'],*candidate['reachable_internal_body_spans']]
  return any(a.get('file')==b['file']and b['start']<=a.get('start',-1)and a.get('start',-1)+a.get('length',0)<=b['start']+b['length']for a in locations for b in spans)
 for condition in old:
  try:index=new.index(condition,cursor)
  except ValueError:
   origin=next(({'condition':condition,'evaluation_ast_id':int(key.split(':')[1]),'operator':'?:','arm':c['arm'],'kind':'removed_guard_from_unevaluated_arm',
     'source_guard':g,'enclosing_function_ast_id':c['enclosing_function_ast_id']}for key,c in choices if c['operator']=='?:'and c.get('predecessor_reference_complete')and c['required_condition']in new and inside(c)
     for g in c.get('guards_exclusive_to_unevaluated_arm',[])if g['condition']==condition),None)
   if not origin:return None
   removed.append(origin);continue
  used.append(index);cursor=index+1
 extra=[c for i,c in enumerate(new)if i not in used]
 proofs.extend(removed)
 for condition in extra:
  match=None
  for key,candidate in choices:
   if candidate['required_condition']!=condition:continue
   if inside(candidate):match={'condition':condition,'evaluation_ast_id':int(key.split(':')[1])if key.startswith('ternary:')else int(key),'operator':candidate['operator'],'arm':candidate.get('arm'),'path':path,'evidence_ids':after.get('evidence_ids',[])};break
  if not match:return None
  proofs.append(match)
 return proofs
def duplicate_conditions_proof(before,after,old,new):
 # A source row's serialized predicate list is a set-valued projection of its
 # expression context. Retain the original multiplicity in this review; no
 # distinct predicate, bound expression, money field or state effect is erased.
 a=list(before.get('conditions')or[]);b=list(after.get('conditions')or[])
 if len(a)==len(set(a))and len(b)==len(set(b)):return None
 def indexes(s):
  if '_review_indexes'not in s:s['_review_indexes']=({e['expression_id']:e for e in s['expressions']},{e['evidence_id']:e for e in s['evidence']})
  return s['_review_indexes']
 old_expressions,old_evidence=indexes(old);new_expressions,new_evidence=indexes(new)
 old_e=old_expressions.get(before.get('key_expression_id')or before.get('target_expression_id')or before.get('expression_id'))
 new_e=new_expressions.get(after.get('key_expression_id')or after.get('target_expression_id')or after.get('expression_id'))
 if old_e is None or new_e is None:return None
 anchor=lambda row,table:[{k:v for k,v in table[e]['location'].items()if k!='artifact_id'}for e in row.get('evidence_ids',[])if e in table]
 if not anchor(before,old_evidence)or anchor(before,old_evidence)!=anchor(after,new_evidence):return None
 if list(old_e.get('conditions')or[])!=a:return None
 if list(dict.fromkeys(new_e.get('conditions')or[]))!=list(dict.fromkeys(b)):return None
 return {'kind':'duplicate_serialized_context_predicates','original_conditions':a,'replacement_conditions':b,
  'before_expression_id':old_e['expression_id'],'after_expression_id':new_e['expression_id'],
  'before_path':old_e.get('path'),'after_path':new_e.get('path'),'source_locations':anchor(after,new_evidence),
  'scope':'predicate spelling and multiplicity only; binding and execution-order proofs are not asserted'}

def builtin_mutation_proof(row,before,after,ref):
 if row.get('call_type')!='CALL'or row.get('selector')or row.get('signature')or row.get('value_expression')is not None:return None
 evidence={e['evidence_id']:e for e in before['evidence']};locations=[evidence[e]['location']for e in row.get('evidence_ids',[])if e in evidence]
 m=next(m for m in before['methods']if m['method_id']==row['method_id']);replacement=next((r for r in after['methods']if method(r)==method(m)),None)
 if replacement is None:return None
 for at in locations:
  c=ref.get('compiler_builtin_calls',{}).get(str(at.get('ast_id')))
  if not c or c['target_text']!=row.get('target_expression')or c['argument_texts']!=row.get('input_arguments'):continue
  if c['location']!={k:v for k,v in at.items()if k not in['artifact_id','line']}:continue
  reason='SOURCE_ARRAY_MUTATION_REQUIRES_LENGTH_AND_ELEMENT_VERSION'
  frontiers=[f for f in after.get('frontier',[])if f.get('method_id')==replacement['method_id']and f.get('reason')==reason and f.get('operation')==c['operation']]
  new_evidence={e['evidence_id']:e for e in after['evidence']}
  frontiers=[f for f in frontiers if any(new_evidence.get(e,{}).get('location',{}).get('ast_id')==at['ast_id']for e in f.get('evidence_ids',[]))]
  if not frontiers or not any(g.get('code')==reason and g.get('ast_id')==at['ast_id']for g in replacement['completeness']['unresolved']):continue
  return {'kind':'compiler_identified_storage_array_builtin_not_external_CALL','compiler':ref['compiler'],
   'compiler_builtin':c,'frontier_ids':[f['frontier_id']for f in frontiers],'unimplemented_effect':reason}
 return None

def encoding_proof(before,after,s,ref):
 if before.get('selector')or before.get('signature'):return None
 if before.get('input_arguments'):
  evidence={e['evidence_id']:e for e in s['evidence']};raw_calls=ref.get('raw_calls',{});sources=[raw_calls.get(str(evidence.get(k,{}).get('location',{}).get('ast_id')))for k in after.get('evidence_ids',[])];
  if not any(call and call['kind']=='call'and call['argument_texts']==before['input_arguments']for call in sources):return None
 v=next((v for v in s.get('source_memory_versions',[])if v['source_memory_version_id']==after.get('source_memory_version_id')),None)
 if not v or not v.get('payload_complete')or v.get('mutation')or v.get('selector')!=after.get('selector')or v.get('argument_texts')!=after.get('input_arguments'):return None
 verified=v.get('valid_encoding');conditional=v.get('allocator_status')=='unbound_internal_entry'and v.get('unresolved')==['SOURCE_BYTES_ALLOCATOR_UNVERIFIED']and'caller_preserves_compiler_memory_allocator'in after.get('assumptions',[])
 if not(verified or conditional):return None
 types=v.get('argument_types');expected={'0xa9059cbb':(['address','uint256'],'transfer(address,uint256)'),'0x23b872dd':(['address','address','uint256'],'transferFrom(address,address,uint256)'),'0x095ea7b3':(['address','uint256'],'approve(address,uint256)')}.get(after.get('selector'))
 if not expected or types!=expected[0]or after.get('signature')!=expected[1]:return None
 return{'source_memory_version_id':v['source_memory_version_id'],'origin_expression_id':v['origin_expression_id'],'payload_length':v['byte_length'],'type_basis':types,'allocator_assumption_preserved':bool(conditional),'evidence_ids':v['evidence_ids']}
reviewed=[];unreviewed=[];current_key=None;current_sections=None
for changed in sorted(strict['regression_candidates'],key=lambda r:(r['key'],r['prior_directory'])):
 key=changed['key'];prior=pathlib.Path(changed['prior_directory']);old_run=next(r for r in json.loads((prior/'runs.json').read_text())if r['key']==key);before=load(prior,key,old_run)
 if current_key!=key:current_sections=load(directory,key,runs[key]);current_key=key
 after=current_sections;ref=reference(key)
 if set(changed['missing'])-{'permissions','storage','calls'}:unreviewed.append({'key':key,'prior_directory':str(prior),'reason':'non_refinable_section_delta','missing':changed['missing']});continue
 for section in changed['missing']:
  old=rows(before,section);new=rows(after,section);old_count=collections.Counter(k for k,r in old);new_count=collections.Counter(k for k,r in new);removed=old_count-new_count;added=new_count-old_count
  old_rows=[];new_rows=[]
  for signature,row in old:
   if removed[signature]:old_rows.append(row);removed[signature]-=1
  for signature,row in new:
   if added[signature]:new_rows.append(row);added[signature]-=1
  consumed=set();ignored={'conditions'}|({'selector','signature','input_arguments'}if section=='calls'else set())
  for row in old_rows:
   candidates=[(i,r)for i,r in enumerate(new_rows)if i not in consumed and projection(before,row,section,ignored)==projection(after,r,section,ignored)and same_source_occurrence(row,r,before,after)];accepted=None
   for i,replacement in candidates:
    duplicates=duplicate_conditions_proof(row,replacement,before,after)
    normalized_before={**row,'conditions':list(dict.fromkeys(row.get('conditions')or[]))}if duplicates else row
    normalized_after={**replacement,'conditions':list(dict.fromkeys(replacement.get('conditions')or[]))}if duplicates else replacement
    conditions=condition_proof(normalized_before,normalized_after,after,ref)
    if conditions is None:continue
    decode_changed=section=='calls'and any(row.get(k)!=replacement.get(k)for k in['selector','signature','input_arguments']);encoding=encoding_proof(row,replacement,after,ref)if decode_changed else None
    if decode_changed and encoding is None:continue
    accepted={'key':key,'prior_directory':str(prior),'section':section,'before_row_sha256':sha(json.dumps(row,sort_keys=True).encode()),'after_row_sha256':sha(json.dumps(replacement,sort_keys=True).encode()),'short_circuit_proofs':conditions,'duplicate_conditions':duplicates,'encoding_refinement':encoding};consumed.add(i);break
   if accepted is None and section=='calls':
    builtin=builtin_mutation_proof(row,before,after,ref)
    if builtin:accepted={'key':key,'prior_directory':str(prior),'section':section,'before_row_sha256':sha(json.dumps(row,sort_keys=True).encode()),'compiler_builtin_refinement':builtin}
   if accepted:reviewed.append(accepted)
   else:unreviewed.append({'key':key,'prior_directory':str(prior),'section':section,'before_row':row})
 del before,after;gc.collect()
document={'status':'FAIL'if unreviewed else'PASS','image':image,'raw_comparison_status':strict['status'],'raw_comparison_sha256':sha((directory/'semantic-regression.json').read_bytes()),
 'scope':'Every strict missing row is matched without changing method, money, state location, operation or value; only compiler-proven conditional predicates, serialization duplicate predicates, version-bound ABI refinement and compiler-identified array builtins with retained unresolved effects are accepted',
 'reviewed_rows':reviewed,'unreviewed_rows':unreviewed,'source_condition_reference_sha256':sha((ROOT/'scripts/contract-analysis/p2_storage_condition_reference.mjs').read_bytes()),'verifier_sha256':sha(pathlib.Path(__file__).read_bytes())}
import datetime,shutil
attempt=directory/'semantic-review-attempts'/datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ');attempt.mkdir(parents=True)
(attempt/'semantic-review.json').write_text(json.dumps(document,ensure_ascii=False,indent=2))
for source in[pathlib.Path(__file__),ROOT/'scripts/contract-analysis/p2_storage_condition_reference.mjs']:shutil.copy2(source,attempt/source.name)
(directory/'semantic-review.json').write_text(json.dumps(document,ensure_ascii=False,indent=2));print('P2_STORAGE_SEMANTIC_REVIEW',document['status'],'REVIEWED',len(reviewed),'UNREVIEWED',len(unreviewed),flush=True)
