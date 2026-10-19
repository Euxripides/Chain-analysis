"""Independent frozen-ABI discovery measurement, separate from business quality."""
import argparse,gc,json,pathlib,re,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];BASE=ROOT/'acceptance/contract-analysis-general'
p=argparse.ArgumentParser();p.add_argument('--phase',required=True);p.add_argument('--results-root');a=p.parse_args();directory=(pathlib.Path(a.results_root)if a.results_root else BASE)/a.phase
manifest=json.loads((BASE/'corpus/corpus-manifest.json').read_text());runs=json.loads((directory/'runs.json').read_text())
abi={c['key']:c['abi']for sample in manifest['samples']for c in sample['contexts']}
script="""const fs=require('node:fs'),{keccak_256}=require('@noble/hashes/sha3.js');const select=s=>'0x'+Buffer.from(keccak_256(Buffer.from(s))).toString('hex').slice(0,8);if(select('transfer(address,uint256)')!=='0xa9059cbb')throw Error('REFERENCE_SELECTOR_FAILED');const type=p=>p.type.startsWith('tuple')?'('+p.components.map(type).join(',')+')'+p.type.slice(5):p.type;const abi=JSON.parse(fs.readFileSync(0,'utf8'));process.stdout.write(JSON.stringify(Object.fromEntries(Object.entries(abi).map(([k,rows])=>[k,rows.filter(r=>r.type==='function').map(r=>({signature:r.name+'('+r.inputs.map(type).join(',')+')',selector:select(r.name+'('+r.inputs.map(type).join(',')+')')}))]))));"""
reference=json.loads(subprocess.check_output(['/home/euripides/.nvm/versions/node/v24.20.0/bin/node','-e',script],input=json.dumps(abi).encode(),cwd=ROOT/'analysis-engines'))
measurements=[]
for run in runs:
 context=run['key'].removesuffix('-'+run['channel']);expected={f['selector']for f in reference[context]};found=set();source=[];resources=run.get('metrics',{})
 if run['transport_status']=='completed':
  value=json.loads((directory/(run['key']+'-output.json')).read_text());s=value['sections'];found={m['selector'].lower()for m in s['methods']if re.fullmatch(r'0x[0-9a-fA-F]{8}',m.get('selector',''))and m.get('dispatch_role')!='overridden_source_definition'}
  source=[e for e in s['engines']if e['engine']=='solc-ast'];del value,s;gc.collect()
 measurements.append({'key':run['key'],'sample':run['sample'],'set':run['set'],'channel':run['channel'],'transport_status':run['transport_status'],
  'reference_selectors':len(expected),'discovered_expected':len(expected&found),'missing':sorted(expected-found),'unexpected':sorted(found-expected),
  'recall_numerator':len(expected&found),'recall_denominator':len(expected),'precision_numerator':len(expected&found),'precision_denominator':len(found),
  'source_engine':source,'wall_seconds':run['wall_seconds'],'resources':resources,'provider_http':0,'credits':0,'estimated_provider_usd':'0'})
aggregates=[]
for split in ['development','blind']:
 for channel in sorted({r['channel']for r in measurements}):
  rows=[r for r in measurements if r['set']==split and r['channel']==channel]
  aggregates.append({'set':split,'channel':channel,'contexts':len(rows),'independent_implementations':len({r['sample']for r in rows}),
    **{k:sum(r[k]for r in rows)for k in ['reference_selectors','discovered_expected','recall_numerator','recall_denominator','precision_numerator','precision_denominator']},
    'engine_transport_completed':sum(r['transport_status']=='completed'for r in rows),'wall_seconds':sum(r['wall_seconds']for r in rows)})
document={'scope':'ABI_entry_discovery_only_not_business_quality','reference':'sealed_provider_ABI_independent_keccak_with_known_vector',
 'independent_implementations':16,'contexts':18,'business_reference_facts':64,'business_quality':'requires_separate_fixed_fact_assessment_unknown_and_failed_cases_remain_in_denominator',
 'aggregate':aggregates,'runs':measurements}
(directory/'discovery-metrics.json').write_text(json.dumps(document,indent=2))
print(json.dumps({'scope':document['scope'],'aggregate':aggregates},indent=2))
