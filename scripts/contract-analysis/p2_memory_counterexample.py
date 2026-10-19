"""Retain an actual skipped-branch memory counterexample to the rejected image."""
import argparse,datetime,hashlib,json,os,pathlib,subprocess
p=argparse.ArgumentParser();p.add_argument('--kind',choices=['short_circuit','loop_initializer'],default='short_circuit');args=p.parse_args();short=args.kind=='short_circuit'
ROOT=pathlib.Path(__file__).resolve().parents[2];BASE=ROOT/'acceptance/contract-analysis-memory';image='sha256:8444f30aea10de781808f41a13fc406dd3d9514469391f8dfa7491ce594214ae'if short else'sha256:52f12536003bbd002411d49b00d776ed17709654bd20a2e7e2eb4f24aea1ecff';runs=BASE/'source-execution';before=set(runs.iterdir())
out=BASE/'rejected-candidates'/image[7:23]/datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ');out.mkdir(parents=True)
env={**os.environ,'PATH':'/home/euripides/.nvm/versions/node/v24.20.0/bin:'+os.environ['PATH'],'CONTRACT_TEST_IMAGE':image,'P2_SOURCE_METHODS':'shortAnd'if short else'loopIndex'}
with(out/'reproduction.log').open('w')as f:p=subprocess.run(['node','scripts/contract-analysis/p2_source_bytes_runtime.mjs'],cwd=ROOT,env=env,stdout=f,stderr=subprocess.STDOUT,timeout=180)
assert p.returncode!=0
added=set(runs.iterdir())-before;assert len(added)==1;directory=added.pop();failure=json.loads((directory/'failure.json').read_text());case=failure['current_case'];assert failure['engine_digest']==image and case['name']==('shortAnd'if short else'loopIndex')and case['flag']is False
trace=json.loads((directory/(case['label']+'-trace.json')).read_text());calls=[s for s in trace['structLogs']if s['depth']==1 and s['op']=='CALL'];call=calls[0 if short else 1];at=int(call['stack'][-4],16);length=int(call['stack'][-5],16);observed=bytes.fromhex(''.join(s.removeprefix('0x')for s in call['memory']))[at:at+length]
model=json.loads((directory/'result.json').read_text());method=next(m for m in model['sections']['methods']if m['name']==case['name']);binding=next(b for b in model['sections']['call_input_bindings']if b['method_id']==method['method_id']);assert binding['status']=='exact'and not trace['failed']
if short:
 modeled=binding['cases'][0]['bytes'][0];assert modeled['kind']=='constant'and modeled['value']==0 and observed[0]==0xa9;observed_value=observed[0];model_value=modeled['value']
else:
 assert case['amount']=='2';observed_value=int.from_bytes(observed[36:68],'big');version=binding['source_memory_version_ids'][0];entry=next(v for v in model['sections']['source_memory_versions']if v['source_memory_version_id']==version)
 d=next(d for d in model['sections']['dataflow']if d['dataflow_id']==entry['argument_derivation_ids'][1]);assert d['symbolic_expression'].replace('(','').replace(')','')=='0';model_value=0;assert observed_value==1
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest();record={'status':'PASS_COUNTEREXAMPLE_REPRODUCED','rejected_image':image,'kind':args.kind,'scope':'skipped RHS byte mutation'if short else'actual second loop CALL uses index 1 while the rejected model substituted initializer 0',
 'observed_value':observed_value,'rejected_model_value':model_value,'actual_call_input_hex':observed.hex(),'output_directory':str(directory),'trace_sha256':sha(directory/(case['label']+'-trace.json')),'analysis_sha256':sha(directory/'result.json'),'verifier_exit_code':p.returncode,'external_provider_requests':0}
(out/'result.json').write_text(json.dumps(record,indent=2));print('P2_REJECTED_IMAGE_MEMORY_COUNTEREXAMPLE_PASS',str(out),flush=True)
