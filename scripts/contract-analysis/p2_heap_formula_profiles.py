"""Independent arithmetic vectors for exposed USDT scalar fee refinements."""
import datetime,hashlib,json,pathlib,re,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];BASE=ROOT/'acceptance/contract-analysis-heap-writes'
image=(BASE/'engine-image.txt').read_text().strip();directory=BASE/'corpus'/('candidate-20-'+image[7:23]);prior=ROOT/'acceptance/contract-analysis-storage/corpus/candidate-17-e0bef21df97cca9c'
sha=lambda b:hashlib.sha256(b).hexdigest();checks=[]
program=r"""const fs=require('node:fs'),{expression,profile}=JSON.parse(fs.readFileSync(0,'utf8'));if(!/^[A-Za-z0-9_\s()+*/?:<>=!%&|.\-]+$/.test(expression))throw Error('formula grammar');for(const n of expression.match(/[A-Za-z_]\w*/g)||[])if(!['_value','basisPointsRate','maximumFee','uint256','true','false'].includes(n))throw Error('formula input '+n);
const evaluate=new Function('_value','basisPointsRate','maximumFee','uint256','return '+expression.replace(/\b[0-9]+\b/g,'$&n')),rows=[];
for(const value of[0n,1n,17n,9007199254740993n,((1n<<256n)-1n)/10000n])for(const rate of[0n,1n,20n,10000n])for(const cap of[0n,3n,9007199254740993n]){const raw=value*rate/10000n,fee=raw>cap?cap:raw,expected=profile==='fee'?fee:value-fee,actual=evaluate(value,rate,cap,v=>BigInt.asUintN(256,v));rows.push({value:String(value),rate:String(rate),cap:String(cap),expected:String(expected),actual:String(actual),equal:expected===actual});}process.stdout.write(JSON.stringify(rows));"""
for channel in['source','submitted_source']:
 name='usdt-'+channel+'-output.json';raw=(directory/name).read_bytes();current=json.loads(raw);old=json.loads((prior/name).read_text());before={f['fund_id']:f for f in old['sections']['funds']};methods={m['method_id']:m for m in current['sections']['methods']}
 for fund in current['sections']['funds']:
  previous=before[fund['fund_id']]
  if fund.get('expanded_amount_expression')==previous.get('expanded_amount_expression'):continue
  assert fund['kind']==previous['kind']=='token_ledger_effect'and fund['amount_expression']==previous['amount_expression']
  assert methods[fund['method_id']]['name']in['transfer','transferFrom']and fund['amount_derivation_status']=='represented_source_dependencies_resolved'
  old_expression=previous['expanded_amount_expression'];compact=re.sub(r'[()\s]','',old_expression);profile={'fee':'fee','_value-fee':'net'}[compact]
  vectors=json.loads(subprocess.check_output(['/home/euripides/.nvm/versions/node/v24.20.0/bin/node','-e',program],input=json.dumps({'expression':fund['expanded_amount_expression'],'profile':profile}).encode()));assert len(vectors)==60 and all(v['equal']for v in vectors)
  checks.append({'status':'PASS','channel':channel,'fund_id':fund['fund_id'],'method_id':fund['method_id'],'result_sha256':sha(raw),'field':'expanded_amount_expression',
   'before':old_expression,'after':fund['expanded_amount_expression'],'profile':profile,'reference':'min(value*rate/10000,cap)'if profile=='fee'else'value-min(value*rate/10000,cap)',
   'evidence_ids':fund['evidence_ids'],'amount_derivation_id':fund['amount_derivation_id'],'vectors':vectors})
assert len(checks)==16
record={'status':'PASS','image':image,'checks':checks,'cases':sum(len(c['vectors'])for c in checks),'verifier_sha256':sha(pathlib.Path(__file__).read_bytes()),
 'scope':'exact saved USDT fee/net numeric expressions on non-overflowing concrete integer vectors; ledger settlement, roles and arbitrary uint256 arithmetic not proved'}
out=BASE/'formula-profiles'/image[7:23]/datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S%fZ');out.mkdir(parents=True);(out/'usdt.json').write_text(json.dumps(record,indent=2));(BASE/'formula-profiles-current.txt').write_text(str(out)+'\n')
print('P2_AGGREGATE_USDT_FORMULA_PASS',len(checks),record['cases'],str(out),flush=True)
