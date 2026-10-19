"""Review exact deltas using compiler short-circuit locations and versioned source encoding.

The raw strict comparison remains unchanged. Unknown deltas remain failures.
No amount, recipient, storage root, method or previously complete proof may be
discarded by this review.
"""
import argparse,collections,gc,hashlib,json,pathlib,re,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];BASE=ROOT/'acceptance/contract-analysis-memory';p=argparse.ArgumentParser();p.add_argument('--image');args=p.parse_args();image=args.image or(BASE/'engine-image.txt').read_text().strip();assert re.fullmatch(r'sha256:[a-f0-9]{64}',image)
directory=BASE/'corpus'/('candidate-16-'+image[7:23]);strict=json.loads((directory/'semantic-regression.json').read_text());runs={r['key']:r for r in json.loads((directory/'runs.json').read_text())};out=directory/'semantic-review';out.mkdir(exist_ok=True)
sha=lambda b:hashlib.sha256(b).hexdigest();fields={
 'funds':['kind','asset','from_expression','to_expression','amount_expression','expanded_asset_expression','expanded_from_expression','expanded_to_expression','expanded_amount_expression','unit','settlement','conditions','condition_status'],
 'permissions':['condition','expanded_condition','conditions','failure','condition_status'],
 'storage':['access','slot','offset','type','variable','variable_type','key_expression','value_expression','operation','slot_role','conditions','code_address','storage_address'],
 'calls':['call_type','target','target_expression','value_expression','input_expression','input_arguments','failure_propagation','selector','signature','conditions']}
def method(m):return(m.get('entry')or'<implicit>',m.get('selector')or'',m.get('signature')or m.get('name')or'',m.get('dispatch_role')or'',m.get('entry_kind')or'')
def rows(s,name):
 methods={m['method_id']:method(m)for m in s['methods']};return[(json.dumps([methods.get(r.get('method_id')),{k:r.get(k)for k in fields[name]}],sort_keys=True),r)for r in s.get(name,[])]
def projection(s,r,section,ignored):
 m=next(m for m in s['methods']if m['method_id']==r['method_id']);return json.dumps([method(m),{k:r.get(k)for k in fields[section]if k not in ignored}],sort_keys=True)
def load(folder,key,record):
 raw=(folder/(key+'-output.json')).read_bytes();assert sha(raw)==record['files'][key+'-output.json'];return json.loads(raw)['sections']
reference_cache={}
def reference(key):
 context=key.removesuffix('-submitted_source').removesuffix('-source').removesuffix('-hidden_source')
 if context not in reference_cache:
  path=ROOT/'acceptance/contract-analysis-general/corpus/materials'/context/'source-provider.json';raw=path.read_bytes();value=json.loads(subprocess.check_output(['/home/euripides/.nvm/versions/node/v24.20.0/bin/node','scripts/contract-analysis/p2_memory_condition_reference.mjs'],cwd=ROOT,input=raw))
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
reviewed=[];unreviewed=[]
for changed in strict['regression_candidates']:
 key=changed['key'];prior=pathlib.Path(changed['prior_directory']);old_run=next(r for r in json.loads((prior/'runs.json').read_text())if r['key']==key);before=load(prior,key,old_run);after=load(directory,key,runs[key]);ref=reference(key)
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
   candidates=[(i,r)for i,r in enumerate(new_rows)if i not in consumed and projection(before,row,section,ignored)==projection(after,r,section,ignored)];accepted=None
   for i,replacement in candidates:
    conditions=condition_proof(row,replacement,after,ref)
    if conditions is None:continue
    decode_changed=section=='calls'and any(row.get(k)!=replacement.get(k)for k in['selector','signature','input_arguments']);encoding=encoding_proof(row,replacement,after,ref)if decode_changed else None
    if decode_changed and encoding is None:continue
    accepted={'key':key,'prior_directory':str(prior),'section':section,'before_row_sha256':sha(json.dumps(row,sort_keys=True).encode()),'after_row_sha256':sha(json.dumps(replacement,sort_keys=True).encode()),'short_circuit_proofs':conditions,'encoding_refinement':encoding};consumed.add(i);break
   if accepted:reviewed.append(accepted)
   else:unreviewed.append({'key':key,'prior_directory':str(prior),'section':section,'before_row':row})
 del before,after;gc.collect()
document={'status':'FAIL'if unreviewed else'PASS','image':image,'raw_comparison_status':strict['status'],'raw_comparison_sha256':sha((directory/'semantic-regression.json').read_bytes()),
 'scope':'every strict missing row must be accounted for by a compiler-proven conditional evaluation predicate or a version-bound source ABI refinement; other changes fail',
 'reviewed_rows':reviewed,'unreviewed_rows':unreviewed,'source_condition_reference_sha256':sha((ROOT/'scripts/contract-analysis/p2_memory_condition_reference.mjs').read_bytes()),'verifier_sha256':sha(pathlib.Path(__file__).read_bytes())}
(directory/'semantic-review.json').write_text(json.dumps(document,ensure_ascii=False,indent=2));print('P2_MEMORY_SEMANTIC_REVIEW',document['status'],'REVIEWED',len(reviewed),'UNREVIEWED',len(unreviewed),flush=True)
