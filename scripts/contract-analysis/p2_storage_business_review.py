"""Save exact, focused real-business evidence for manual review, without scoring by row counts."""
import datetime,hashlib,json,pathlib
ROOT=pathlib.Path(__file__).resolve().parents[2];BASE=ROOT/'acceptance/contract-analysis-storage';image=(BASE/'engine-image.txt').read_text().strip();directory=BASE/'corpus'/('candidate-17-'+image[7:23]);runs={r['key']:r for r in json.loads((directory/'runs.json').read_text())};labels=[]
for name in['development','blind']:labels+=json.loads((ROOT/'acceptance/contract-analysis-general/corpus'/('labels-'+name+'.json')).read_text())
selected=['sushi-masterchef:3','uniswap-v3-staker:4','safe-singleton:4','uniswap-v2-pair:2','sdai:1'];packets=[]
for label in labels:
 for fact in label['business_facts']:
  if fact['fact_id']not in selected:continue
  for channel in['source','submitted_source']:
   key=fact['context']+'-'+channel;run=runs.get(key)
   if not run:packets.append({'fact_id':fact['fact_id'],'channel':channel,'status':'NOT_RUN'});continue
   raw=(directory/(key+'-output.json')).read_bytes();assert hashlib.sha256(raw).hexdigest()==run['files'][key+'-output.json'];result=json.loads(raw);s=result['sections'];evidence={e['evidence_id']:e for e in s['evidence']};anchor=fact['evidence']
   methods=[m for m in s['methods']if m.get('name')==fact['function']];exact=[m for m in methods if any((evidence.get(e,{}).get('location',{}).get('file'),evidence.get(e,{}).get('location',{}).get('start'))==(anchor['file'],anchor['start'])for e in m['evidence_ids'])]
   if exact:methods=exact
   mids={m['method_id']for m in methods};funds=[f for f in s['funds']if f.get('method_id')in mids];refs={v for f in funds for v in[f.get('source_memory_version_id'),*(f.get('source_memory_version_ids')or[])]if v}
   packets.append({'fact_id':fact['fact_id'],'channel':channel,'expected':fact['expected'],'status':'PENDING_MANUAL_REVIEW','result_sha256':hashlib.sha256(raw).hexdigest(),
    'source_anchor':{k:v for k,v in anchor.items()if k!='text'},'methods':[{k:m.get(k)for k in['method_id','signature','source_checkpoint','completeness']}for m in methods],
    'funds':funds,'source_bytes':[{k:v.get(k)for k in['source_memory_version_id','encoding','selector','argument_types','argument_texts','argument_expression_ids','argument_derivation_ids','byte_length','status','unresolved','valid_encoding','conditions','evidence_ids']}for v in s.get('source_memory_versions',[])if v['source_memory_version_id']in refs],
    'conditions':[{k:p.get(k)for k in['guard_id','condition','expanded_condition','condition_derivation_status','conditions','evidence_ids']}for p in s['permissions']if p.get('method_id')in mids],
    'state_writes':[{k:v.get(k)for k in['storage_id','key_expression','value_expression','operation','slot','storage_projection','conditions','evidence_ids']}for v in s['storage']if v.get('method_id')in mids and v['access']=='write'],
    'open_dataflow':[{k:d.get(k)for k in['dataflow_id','kind','original_expression','symbolic_expression','unresolved','evidence_ids']}for d in s['dataflow']if d.get('method_id')in mids and d.get('unresolved')],
    'full_authorization_proved':False,'full_protocol_economics_proved':False})
out=BASE/'business-review'/image[7:23]/datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ');out.mkdir(parents=True);(out/'packets.json').write_text(json.dumps({'image':image,'scope':'previously exposed real source facts; manual semantic assessment required','packets':packets},ensure_ascii=False,indent=2));print('P2_STORAGE_REVIEW_PACKETS',len(packets),str(out),flush=True)
