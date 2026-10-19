"""Attach reviewed fact grades and exact result references to sealed materials.

Unknowns remain in every denominator. This is a P1 baseline, not a P2 business
quality assertion. Hidden-source grades report missing semantic linkage rather
than treating ABI/selector discovery as a recovered business fact.
"""
import argparse,collections,gc,hashlib,json,pathlib,subprocess

ROOT=pathlib.Path(__file__).resolve().parents[2];BASE=ROOT/'acceptance/contract-analysis-general'
sha=lambda b:hashlib.sha256(b).hexdigest()
p=argparse.ArgumentParser();p.add_argument('--phase',required=True);p.add_argument('--results-root');p.add_argument('--notes-overlay');p.add_argument('--scope',default='P1_quality_baseline_not_P2_acceptance');a=p.parse_args();directory=(pathlib.Path(a.results_root)if a.results_root else BASE)/a.phase
seal=json.loads((BASE/'corpus/seal.json').read_text());manifest_raw=(BASE/'corpus/corpus-manifest.json').read_bytes();assert sha(manifest_raw)==seal['manifest_sha256']
manifest=json.loads(manifest_raw);labels=[]
for split in ['development','blind']:
 raw=(BASE/'corpus'/('labels-'+split+'.json')).read_bytes();assert sha(raw)==seal['labels']['labels-'+split+'.json']
 for label in json.loads(raw):labels.append({**label,'set':split})
notes=json.loads((ROOT/'scripts/contract-analysis/general_quality_notes.json').read_text())['source_facts'];assert len(notes)==64
overlay={}
if a.notes_overlay:
 overlay=json.loads(pathlib.Path(a.notes_overlay).read_text())['source_facts'];assert set(overlay)<=set(notes)and all(len(v)==2 and v[0]in['static_recovered','partial','unavailable','erroneous_expression']for v in overlay.values());notes.update(overlay)
formulas={}
for channel in ['source','submitted_source']:
 formula_path=directory/('formula-counterexample'+(''if channel=='source'else'-'+channel)+'.json')
 if formula_path.exists():formulas[channel]=json.loads(formula_path.read_text())
runs=json.loads((directory/'runs.json').read_text());index={r['key']:r for r in runs}
abis={c['key']:c['abi']for sample in manifest['samples']for c in sample['contexts']}
node="""const fs=require('node:fs'),{keccak_256}=require('@noble/hashes/sha3.js');const type=p=>p.type.startsWith('tuple')?'('+p.components.map(type).join(',')+')'+p.type.slice(5):p.type;const result={};for(const[k,rows]of Object.entries(JSON.parse(fs.readFileSync(0,'utf8'))))result[k]=rows.filter(r=>r.type==='function').map(r=>{const s=r.name+'('+r.inputs.map(type).join(',')+')';return{name:r.name,signature:s,selector:'0x'+Buffer.from(keccak_256(Buffer.from(s))).toString('hex').slice(0,8)}});process.stdout.write(JSON.stringify(result));"""
reference=json.loads(subprocess.check_output(['/home/euripides/.nvm/versions/node/v24.20.0/bin/node','-e',node],input=json.dumps(abis).encode(),cwd=ROOT/'analysis-engines'))
rows=[];packets=[]
for label in labels:
 for channel in sorted({r['channel']for r in runs}):
  contexts={f['context']for f in label['business_facts']}
  for context in contexts:
   run=index[context+'-'+channel];output=directory/(run['key']+'-output.json');value=None
   if run['transport_status']=='completed':value=json.loads(output.read_text())
   s=value['sections']if value else {};evidence={e['evidence_id']:e for e in s.get('evidence',[])}
   source_engine=[e for e in s.get('engines',[])if e['engine']=='solc-ast']
   for fact in [f for f in label['business_facts']if f['context']==context]:
    anchor=fact['evidence'];grade,reason=notes[fact['fact_id']]
    formula=formulas.get(channel)
    if fact['fact_id']=='safe-singleton:4'and formula and formula['status']=='PASS'and formula['result_sha256']==run['files'].get(output.name):
     grade='partial';reason=overlay.get(fact['fact_id'],[None,'原生币付款括号错误已由保存结果的 72 组独立整数求值修复验证；代币付款效果仍未闭合。'])[1]
    selected=[m for m in s.get('methods',[])if m.get('name')==fact['function']]
    exact=[m for m in selected if any((evidence.get(i,{}).get('location',{}).get('file'),evidence.get(i,{}).get('location',{}).get('start'))==(anchor['file'],anchor['start'])for i in m.get('evidence_ids',[]))]
    if exact:selected=exact
    expected_selectors=[r['selector']for r in reference[context]if r['name']==fact['function']]
    if channel=='hidden_source':selected=[m for m in s.get('methods',[])if m.get('selector')in expected_selectors]
    if not value:grade='unavailable';reason='引擎进程未返回有效结果，退出码 '+str(run['exit_code'])+'；保留在业务事实分母。'
    elif channel=='hidden_source':
     grade='not_recovered';reason=('存在对应入口及 PC 模型，尚未完整建立封存业务事实中的角色、条件、账本或资金公式关联。'if selected else'没有恢复该内部方法边界或对应业务切片；入口识别不替代内部业务事实。')
    elif not any(e['status']in['completed','partial']for e in source_engine)or not selected:
     grade='unavailable';reason='未取得匹配该源码锚点的已验证源码方法；'+','.join(e.get('error_code',e['status'])for e in source_engine)
    elif all(m.get('source_checkpoint')=='compiled_entry_only'for m in selected):
     grade='unavailable';reason='编译入口保留，但该方法源码体达到预算后整体回退，尚无完整业务模型。'
    elif grade=='unavailable'and any(m.get('source_checkpoint')=='method_body_saved'for m in selected):
     grade='partial';reason='本轮已保留该方法源码模型；原先未完成的整项业务事实仍待独立验证。'
    mids={m['method_id']for m in selected};references={}
    for name in ['permissions','storage','funds','calls','steps','dataflow','state_transitions','control_effects','protocol_formulas']:
     selected_rows=[(i,r)for i,r in enumerate(s.get(name,[]))if r.get('method_id')in mids]
     references[name]=[{'ordinal':i,'row_sha256':sha(json.dumps(r,sort_keys=True,separators=(',',':')).encode()),'evidence_ids':r.get('evidence_ids',[])}for i,r in selected_rows]
    used={i for m in selected for i in m.get('evidence_ids',[])}
    used.update(i for items in references.values()for r in items for i in r['evidence_ids'])
    row={'fact_id':fact['fact_id'],'sample':label['key'],'set':label['set'],'channel':channel,'dimension':fact['dimension'],
         'expected':fact['expected'],'status':grade,'reason':reason,'run_key':run['key'],'result_sha256':run['files'].get(output.name),
         'source_anchor':{k:v for k,v in anchor.items()if k!='text'},'method_ids':sorted(mids),'reference_selectors':expected_selectors,
         'evidence_reference_count':len(used),'missing_evidence_ids':sorted(used-evidence.keys()),'structured_rows':references,
         'validation_scope':'static_fact_representation_only_no_whole_proof_or_unperformed_execution_claim'}
    rows.append(row)
    packets.append({'fact_id':fact['fact_id'],'channel':channel,'run_key':run['key'],'status':grade,'method_ids':sorted(mids),
                    'permissions':[r.get('expanded_condition',r.get('condition'))for r in s.get('permissions',[])if r.get('method_id')in mids],
                    'funds':[{k:r.get(k)for k in ['kind','asset','expanded_from_expression','expanded_to_expression','expanded_amount_expression','amount_derivation_status']}for r in s.get('funds',[])if r.get('method_id')in mids],
                    'writes':[{k:r.get(k)for k in ['key_expression','value_expression','operation']}for r in s.get('storage',[])if r.get('method_id')in mids and r.get('access')=='write']})
   del value,s;gc.collect()
aggregate=[]
for split in ['development','blind']:
 for channel in sorted({r['channel']for r in rows}):
  subset=[r for r in rows if r['set']==split and r['channel']==channel]
  aggregate.append({'set':split,'channel':channel,'facts':len(subset),'grades':dict(collections.Counter(r['status']for r in subset)),
                    'by_dimension':{d:dict(collections.Counter(r['status']for r in subset if r['dimension']==d))for d in sorted({r['dimension']for r in subset})}})
document={'corpus_sha256':seal['manifest_sha256'],'phase':a.phase,'scope':a.scope,
          'grading':'static_recovered requires the complete sealed local fact; partial, unavailable, not_recovered and erroneous_expression all remain non-passing',
          'hidden_source':'No fully recovered business fact claimed; exact selector and PC evidence saved separately from missing semantic associations',
          'blind_integrity':'Initial labels unchanged; outputs have now been read for assessment. Post-assessment formula corrections are regression evaluation and cannot be described as a fresh blind pass.',
          'full_authorization_proved':False,'full_protocol_economics_proved':False,'aggregate':aggregate,'facts':rows}
(directory/'business-quality.json').write_text(json.dumps(document,ensure_ascii=False,indent=2))
(directory/'business-review-packets.json').write_text(json.dumps(packets,ensure_ascii=False,indent=2))
print(json.dumps({'phase':a.phase,'aggregate':aggregate,'dangling_fact_evidence':sum(len(r['missing_evidence_ids'])for r in rows)},ensure_ascii=False,indent=2))
