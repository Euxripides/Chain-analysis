"""Independently evaluate the saved native reimbursement display expression."""
import argparse,hashlib,json,pathlib,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];OUT=ROOT/'acceptance/contract-analysis-general'
p=argparse.ArgumentParser();p.add_argument('--phase',required=True);p.add_argument('--channel',default='source',choices=['source','submitted_source']);p.add_argument('--results-root');a=p.parse_args();directory=(pathlib.Path(a.results_root)if a.results_root else OUT)/a.phase
raw=(directory/('safe-singleton-'+a.channel+'-output.json')).read_bytes();result=json.loads(raw);s=result['sections']
method=next(m for m in s['methods']if m.get('name')=='handlePayment')
fund=next(f for f in s['funds']if f['method_id']==method['method_id']and f['kind']=='native_payment')
expression=fund['expanded_amount_expression']
js=r"""const fs=require('node:fs');const expression=JSON.parse(fs.readFileSync(0,'utf8')).expression;const evaluate=new Function('gasUsed','baseGas','gasPrice','tx','uint256','return '+expression.replace(/\b(0x[0-9a-fA-F]+|[0-9]+)\b/g,'$1n')),vectors=[];for(const gas of [0n,1n,21000n,9007199254740993n])for(const base of [0n,7n])for(const price of [0n,2n,100n])for(const cap of [0n,3n,17n]){const expected=(gas+base)*(price<cap?price:cap),actual=evaluate(gas,base,price,{gasprice:cap},v=>BigInt.asUintN(256,v));vectors.push({gasUsed:String(gas),baseGas:String(base),gasPrice:String(price),tx_gasprice:String(cap),expected:String(expected),actual:String(actual),equal:expected===actual});}process.stdout.write(JSON.stringify(vectors));"""
vectors=json.loads(subprocess.check_output(['/home/euripides/.nvm/versions/node/v24.20.0/bin/node','-e',js],input=json.dumps({'expression':expression}).encode()))
document={'phase':a.phase,'channel':a.channel,'fact_id':'safe-singleton:4','scope':'native_reimbursement_expression_only_token_payment_and_whole_economics_remain_unverified',
 'result_sha256':hashlib.sha256(raw).hexdigest(),'method_id':method['method_id'],'fund_id':fund['fund_id'],'expression':expression,
 'engine_derivation_status':fund['amount_derivation_status'],'reference':'(gasUsed+baseGas)*min(gasPrice,tx.gasprice), exact nonnegative integers below uint256 overflow',
 'vectors':vectors,'status':'PASS'if all(v['equal']for v in vectors)else'FAIL','mismatches':sum(not v['equal']for v in vectors)}
(directory/('formula-counterexample'+(''if a.channel=='source'else'-'+a.channel)+'.json')).write_text(json.dumps(document,indent=2));print(json.dumps({k:v for k,v in document.items()if k!='vectors'},indent=2))
