"""Run sealed real material against an immutable engine, without any network.

This is a measurement, not a business-quality acceptance assertion. Blind output
is saved for the fixed assessment; it must not be used to tune the engine.
"""
import argparse, datetime, hashlib, json, pathlib, subprocess, time,gzip,base64,io

ROOT = pathlib.Path(__file__).resolve().parents[2]
sha = lambda b: hashlib.sha256(b).hexdigest()


def run(image, phase, context_match="", *, out_base=None):
    corpus=ROOT/'acceptance/contract-analysis-general/corpus'; materials=corpus/'materials'
    manifest_raw=(corpus/'corpus-manifest.json').read_bytes(); seal=json.loads((corpus/'seal.json').read_text())
    assert sha(manifest_raw)==seal['manifest_sha256']
    manifest=json.loads(manifest_raw)
    assert image.startswith('sha256:') and len(image)==71
    out=(pathlib.Path(out_base)if out_base else ROOT/'acceptance/contract-analysis-partial-recovery')/(phase+'-'+image[7:23]); out.mkdir(exist_ok=True)
    index=out/'runs.json'; records=json.loads(index.read_text())if index.exists()else[]
    for sample in manifest['samples']:
        for context in sample['contexts']:
            if context_match and context_match not in context['key']:continue
            d=materials/context['key']
            for name,digest in context['files'].items():assert sha((d/name).read_bytes())==digest
            for channel in ['source','hidden_source']+['submitted_source']:
                key=context['key']+'-'+channel
                previous=next((r for r in records if r['key']==key),None)
                if previous:
                    assert previous['image']==image and previous['corpus_sha256']==seal['manifest_sha256']
                    for name,digest in previous['files'].items():assert sha((out/name).read_bytes())==digest
                    continue
                snapshot={**context['snapshot'],'engine_digest':image}
                payload={'snapshot':snapshot,'bytecode':(d/'runtime.hex').read_text().strip(),'engines':['whatsabi','sevm','solc-ast']}
                if channel=='source':payload['source']=json.loads((d/'source-provider.json').read_text())
                if channel=='submitted_source':
                    doc=json.loads((d/'source-provider.json').read_text());settings=dict(doc['stdJsonInput'].get('settings',{}));settings.pop('outputSelection',None)
                    material={'kind':'source','source':{'language':'Solidity','files':{k:v['content']for k,v in doc['stdJsonInput']['sources'].items()},'compiler_version':doc['compilation']['compilerVersion'],'settings':settings,'target':doc['compilation']['fullyQualifiedName']}}
                    submitted=json.dumps(material,separators=(',',':'))
                    payload={'submitted_input_json':submitted,'snapshot':{'identity_kind':'code_only','input_kind':'source','input_sha256':sha(submitted.encode()),'engine_digest':image,'state_granularity':'symbolic_state','evm_rules':'shanghai'},'engines':['whatsabi','sevm','solc-ast']}
                raw=json.dumps(payload,separators=(',',':')).encode(); (out/(key+'-input.json')).write_bytes(raw)
                record={'key':key,'sample':sample['key'],'set':sample['set'],'channel':channel,'image':image,
                        'corpus_sha256':seal['manifest_sha256'],'started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),
                        'benchmark_conditions':'shared_host_cold_container_2_cpu_limit_no_equivalent_depth_20_percent_performance_claim'}
                name='contract-general-'+phase+'-'+image[7:19]+'-'+key
                args=['docker','run','--rm','--pull','never','-i','--name',name,'--network','none','--read-only','--cap-drop','ALL',
                      '--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g',
                      '--pids-limit','128','--ulimit','core=0','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image]
                started=time.monotonic()
                with (out/(key+'-output.json')).open('wb')as stdout,(out/(key+'-stderr.log')).open('wb')as stderr:
                    try:
                        completed=subprocess.run(args,input=raw,stdout=stdout,stderr=stderr,timeout=300)
                        record['exit_code']=completed.returncode
                    except subprocess.TimeoutExpired:
                        record['exit_code']=124
                        subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=15)
                record['wall_seconds']=time.monotonic()-started;record['provider_http']=0;record['credits']=0;record['estimated_usd']='0'
                resource_lines=[line.split(':',1)[1]for line in(out/(key+'-stderr.log')).read_text(errors='replace').splitlines()if line.startswith('CONTRACT_CONTAINER_RESOURCE_V1:')]
                if record['exit_code']==0:
                    assert len(resource_lines)==1,'Completed container observer record missing'
                    resource=json.loads(resource_lines[0]);wire=(out/(key+'-output.json')).read_bytes()
                    assert resource['stdout_bytes']==len(wire)and resource['stdout_sha256']==sha(wire)and resource['scope']=='after_engine_exit_and_complete_output_forwarding'
                    record['container_execution']=resource
                output=out/(key+'-output.json')
                record['output_limit_exceeded']=output.stat().st_size>64<<20
                wire_file=None
                if record['exit_code']==0 and not record['output_limit_exceeded']:
                    try:
                        value=json.loads(output.read_text())
                        if 'engine_transport'in value.get('sections',{}):
                            envelope=value;transport=value['sections']['engine_transport'][0];assert transport['encoding']=='engine-transport-gzip-v1'
                            assert 0<transport['expanded_bytes']<=128<<20
                            compressed=base64.b64decode(transport['content_base64'],validate=True);assert len(compressed)==transport['byte_size']and sha(compressed)==transport['sha256']
                            with gzip.GzipFile(fileobj=io.BytesIO(compressed))as reader:expanded=reader.read((128<<20)+1)
                            assert len(expanded)==transport['expanded_bytes']and sha(expanded)==transport['expanded_sha256']
                            value=json.loads(expanded);assert value['snapshot']==envelope['snapshot']and value['rule_version']==envelope['rule_version']and 'engine_transport'not in value['sections']
                            wire_file=key+'-wire-output.json';output.rename(out/wire_file);output.write_bytes(expanded);record['wire_encoding']=transport['encoding'];record['expanded_bytes']=len(expanded)
                        record['metrics']=value.get('metrics');record['rule_version']=value['rule_version']
                        record['transport_status']='completed';record['business_assessment']='pending_fixed_label_assessment'
                    except (ValueError,KeyError):record['transport_status']='invalid_output'
                else:record['transport_status']='failed'
                record['files']={f:sha((out/f).read_bytes())for f in [key+'-input.json',key+'-output.json',key+'-stderr.log']+([wire_file]if wire_file else[])}
                records.append(record);index.write_text(json.dumps(records,indent=2))
                print('CORPUS_MEASUREMENT',key,record['transport_status'],round(record['wall_seconds'],2),flush=True)
    print('CORPUS_RUNS_SAVED',len(records),'BUSINESS_QUALITY_NOT_YET_ACCEPTED',flush=True)


if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--image',required=True);p.add_argument('--phase',required=True,choices=['candidate-15']);p.add_argument('--context-match',default='');a=p.parse_args();run(a.image,a.phase,a.context_match)
