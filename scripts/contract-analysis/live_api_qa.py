import os
"""Real HTTP, durable results, complete pagination and artifact verification."""
import base64, hashlib, io, json, pathlib, time, urllib.parse, zipfile
from qa_support import ROOT, PRIVATE, Client, fixture_clients

OUT=ROOT/os.environ.get('CONTRACT_QA_OUT','acceptance/contract-analysis')
OUT.mkdir(parents=True,exist_ok=True)
results=[]
def check(name, condition, evidence=None):
    results.append({'scenario':name,'status':'PASS' if condition else 'FAIL','evidence':evidence})
    (OUT/'live-api-qa.json').write_text(json.dumps(results,indent=2))
    print(name,results[-1]['status'],flush=True)
    if not condition: raise AssertionError(name)

def await_job(client, analysis_id, seconds=900):
    phase=None;deadline=time.monotonic()+seconds
    while time.monotonic()<deadline:
        job=client.must('/api/v2/contract-analyses/'+analysis_id)
        if job['phase']!=phase:phase=job['phase'];print('job_phase',analysis_id,phase,flush=True)
        if job['status'] in ['completed','failed','cancelled']:return job
        time.sleep(2)
    raise RuntimeError('job wait exceeded bound')

def all_pages(client,analysis_id,section,page_size=37,extra=None):
    items=[];cursor=None;count=None;cursors=set()
    while True:
        q={'section':section,'page_size':page_size,**(extra or {})}
        if cursor:q['cursor']=cursor
        page=client.must('/api/v2/contract-analyses/'+analysis_id+'?'+urllib.parse.urlencode(q))
        if count is None:count=page['record_count']
        assert page['record_count']==count and page['returned_records']==len(page['items'])
        items+=page['items']
        if not page['has_more']:assert page['next_cursor'] is None;break
        cursor=page['next_cursor'];assert cursor and cursor not in cursors;cursors.add(cursor)
    assert len(items)==count
    return items

def artifact_bytes(client,analysis_id,artifact,chunk_size=65536):
    out=bytearray();offset=0
    while True:
        path='/api/v2/contract-analyses/'+analysis_id+'/artifacts/'+artifact['artifact_id']+'?'+urllib.parse.urlencode({'offset':offset,'chunk_size':chunk_size})
        data=client.must(path);part=base64.b64decode(data['content_base64'],validate=True)
        assert data['offset']==offset and len(part)==data['byte_length']
        assert hashlib.sha256(part).hexdigest()==data['chunk_sha256']
        assert data['sha256']==artifact['sha256']
        out.extend(part);offset+=len(part)
        if data['next_offset'] is None:break
        assert part and data['next_offset']==offset
    assert len(out)==artifact['byte_size'] and hashlib.sha256(out).hexdigest()==artifact['sha256']
    return bytes(out)

def main():
    clients=fixture_clients();investigator=clients['investigator'];viewer=clients['viewer'];other=clients['other']
    capabilities=investigator.must('/api/v2/contract-analyses/capabilities')
    check('actual_runtime_capabilities',capabilities['engine_digest']==(OUT/'engine-image.txt').read_text().strip() and all(any(e['name']==name and e['status']=='available' for e in capabilities['engines']) for name in ['slither','gigahorse','vyper-ast']),capabilities)
    check('native_program_entry_disabled',investigator.call('/api/v2/contract-analyses/programs',{'vm_family':'solana_sbf','address':'11111111111111111111111111111111'})[0]==503)
    q={'chain':'ethereum','address':'0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2','block':19000000,
       'from_block':19000000,'to_block':19000000,'idempotency_key':'ca-live-api-qa-'+(OUT/'engine-image.txt').read_text().strip()[7:19],
       'budget':{'max_contracts':2,'max_rpc_requests':100,'max_state_reads':24}}
    submission=investigator.must('/api/v2/contract-analyses',q);analysis_id=submission['analysis_id']
    (OUT/'ui-analysis-id.txt').write_text(analysis_id)
    reused=investigator.must('/api/v2/contract-analyses',q)
    check('idempotent_submission',reused['analysis_id']==analysis_id and reused['reused'] is True,analysis_id)
    status,error=investigator.call('/api/v2/contract-analyses',{**q,'address':'0x0000000000000000000000000000000000000001'})
    check('changed_idempotent_request_conflict',status==409,error.get('error'))
    check('viewer_cannot_submit',viewer.call('/api/v2/contract-analyses',q)[0]==403)
    check('csrf_required',investigator.call('/api/v2/contract-analyses/'+analysis_id+'/cancel',{},csrf=False)[0]==403)
    check('other_workspace_cannot_read',other.call('/api/v2/contract-analyses/'+analysis_id)[0]==404)
    check('anonymous_cannot_read',Client().call('/api/v2/contract-analyses/'+analysis_id)[0]==401)
    job=await_job(investigator,analysis_id)
    check('durable_worker_completion',job['status']=='completed',{'analysis_id':analysis_id,'status':job['status'],'error':job.get('error_code')})
    (OUT/'ui-analysis-status.json').write_text(json.dumps(job,indent=2))
    sections={}
    for section,count in sorted(job['metadata']['sections'].items()):
        sections[section]=all_pages(investigator,analysis_id,section)
        check('pagination_'+section,len(sections[section])==count,{'count':count})
    mids=[m['method_id'] for m in sections['methods']]
    check('expanded_result_sections_saved',all(sections.get(s) for s in ['source_verification','method_details','operating_flows','dataflow','ir','ir_dataflow','ir_effects','control_flow']))
    check('six_default_engines_actually_ran',all(any(e['engine']==name and e['status'] in ['completed','partial'] for e in sections['engines']) for name in ['whatsabi','sevm','solc-ast','heimdall','slither','gigahorse']),sections['engines'])
    check('aggregate_rule_version_matches_runtime',job['metadata']['rule_version']==capabilities.get('semantic_rules_version','funds-semantics-2'))
    check('unique_method_identity',len(mids)==len(set(mids)))
    withdraw=next(m for m in sections['methods'] if m['signature']=='withdraw(uint256)' and m['verified_signature'])
    detail=investigator.must('/api/v2/contract-analyses/'+analysis_id+'/methods/'+withdraw['method_id'])
    check('verified_withdraw_method',len(detail['items'])==1,{'signature':withdraw['signature'],'purpose':withdraw['purpose']})
    steps=all_pages(investigator,analysis_id,'steps',extra={'method_id':withdraw['method_id']})
    payments=all_pages(investigator,analysis_id,'funds',extra={'method_id':withdraw['method_id']})
    check('withdraw_guards_and_native_settlement_intent',any('balanceOf[msg.sender] >= wad' in s.get('text','') for s in steps) and any(p['asset']=='native' and p['to_expression']=='msg.sender' and p['amount_expression']=='wad' for p in payments))
    evidence={e['evidence_id']:e for e in sections.get('evidence',[])}
    check('all_static_evidence_resolves',all(eid in evidence for m in sections['methods'] for eid in m.get('evidence_ids',[])))
    first=investigator.must('/api/v2/contract-analyses/'+analysis_id+'?section=evidence&page_size=1')
    token=first['next_cursor']
    check('cursor_cannot_cross_section',investigator.call('/api/v2/contract-analyses/'+analysis_id+'?section=steps&cursor='+urllib.parse.quote(token))[0]==400)
    check('cursor_cannot_change_filter',investigator.call('/api/v2/contract-analyses/'+analysis_id+'?section=evidence&method_id='+withdraw['method_id']+'&cursor='+urllib.parse.quote(token))[0]==400)
    check('tampered_cursor_rejected',investigator.call('/api/v2/contract-analyses/'+analysis_id+'?section=evidence&cursor='+urllib.parse.quote(token[:-2]+'AA'))[0]==400)
    artifact=next(a for a in sections['artifacts'] if a['kind']=='verified_source')
    preview='/api/v2/contract-analyses/'+analysis_id+'/artifacts/'+artifact['artifact_id']+'/preview?chunk_size=4096'
    check('viewer_source_preview',viewer.call(preview)[0]==200)
    check('viewer_export_denied',viewer.call('/api/v2/contract-analyses/'+analysis_id+'/artifacts/'+artifact['artifact_id'])[0]==403)
    check('other_workspace_artifact_denied',other.call(preview)[0]==404)
    bundle=next(a for a in sections['artifacts'] if a['kind']=='evidence_bundle')
    content=artifact_bytes(investigator,analysis_id,bundle)
    with zipfile.ZipFile(io.BytesIO(content)) as z:
        manifest=json.loads(z.read('manifest.json'))
        expected={e['path'] for e in manifest['entries']}|{'manifest.json'}
        assert expected==set(z.namelist())
        for entry in manifest['entries']:
            data=z.read(entry['path']);assert len(data)==entry['byte_size'] and hashlib.sha256(data).hexdigest()==entry['sha256']
        check('zip_every_file_sha256',True,{'files':len(manifest['entries']),'zip_sha256':hashlib.sha256(content).hexdigest(),'bytes':len(content)})
    (OUT/'weth-contract-evidence.zip').write_bytes(content)
    machine=next(a for a in sections['artifacts'] if a['kind']=='machine_result')
    model=json.loads(artifact_bytes(investigator,analysis_id,machine))
    check('export_matches_api_methods',{m['method_id'] for m in model['sections']['methods']}==set(mids))
    check('fixed_snapshot_identity',job['metadata']['snapshot']['block_number']==19000000 and job['metadata']['snapshot']['block_hash']=='0xcf384012b91b081230cdf17a3f7dd370d8e67056058af6b272b3d54aa2714fac')
    check('actual_cost_recorded',job['metadata']['metrics']['credits'] is not None and job['metadata']['metrics']['rpc_http_requests']>=1,job['metadata']['metrics'])
    check('history_missing_coverage_is_explicit',any(c['dimension']=='historical_calls' and c['complete'] is False for c in sections['coverage']))
    (OUT/'ui-analysis-sections.json').write_text(json.dumps(sections,indent=2))
    print('LIVE_API_QA_PASS',len(results),analysis_id,flush=True)

if __name__=='__main__':main()
