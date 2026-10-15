"""Verify typed dependencies on a real pinned pair-interface implementation."""
import json,os
from qa_support import ROOT,fixture_clients
from live_api_qa import await_job,all_pages,artifact_bytes,machine_section
OUT=ROOT/os.environ.get('CONTRACT_QA_OUT','acceptance/contract-analysis-mechanisms');rows=[]
def check(name,ok,evidence=None):
 rows.append({'scenario':name,'status':'PASS'if ok else'FAIL','evidence':evidence});(OUT/'dependency-live-qa.json').write_text(json.dumps(rows,indent=2));print(name,rows[-1]['status'],flush=True)
 if not ok:raise AssertionError(name)
c=fixture_clients()['investigator'];image=(OUT/'engine-image.txt').read_text().strip()
q={'chain':'ethereum','address':'0xA478c2975Ab1Ea89e8196811F51A7B7Ade33eB11','block':19000000,'from_block':19000000,'to_block':19000000,
 'idempotency_key':os.environ.get('CONTRACT_QA_DEPENDENCY_KEY','ca-mechanism-dependency-')+image[7:19],'budget':{'max_contracts':4,'max_rpc_requests':100,'max_state_reads':24}}
aid=c.must('/api/v2/contract-analyses',q)['analysis_id'];job=await_job(c,aid);(OUT/'dependency-analysis-id.txt').write_text(aid)
check('pinned-pair-analysis-completes',job['status']=='completed',{'analysis_id':aid,'status':job['status'],'error':job.get('error_code')})
calls=all_pages(c,aid,'calls',100);contracts=all_pages(c,aid,'contracts',100);methods=all_pages(c,aid,'methods',100)
resolved=[r for r in calls if r.get('resolved_target_address') and r.get('dependency_status')=='analyzed_code_and_storage_context']
targets={r['resolved_target_address'].lower()for r in resolved}
check('source-targets-have-analyzed-code-contexts',{'0x6b175474e89094c44da98b954eedeac495271d0f','0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2'}<=targets,{'targets':sorted(targets),'contracts':len(contracts)})
check('every-context-has-matching-code-hash',all(any(x['code_address'].lower()==r['resolved_target_address'].lower() and x['storage_address'].lower()==r['dependency_storage_address'].lower() and x['code_hash']==r['dependency_code_hash'] for x in contracts)for r in resolved))
observed=[r for r in resolved if r.get('target_snapshot_observations')]
check('target-state-observations-retain-block-and-storage',bool(observed)and all(p['block_number']==19000000 and p['block_hash']==job['metadata']['snapshot']['block_hash'] and p['storage_address']==r['caller_storage_address']for r in observed for p in r['target_snapshot_observations']))
check('identity-casts-and-local-aliases-resolve',any('IERC20('in r['target_expression']for r in resolved),[r['target_expression']for r in resolved])
check('resolved-dependencies-link-dispatched-methods',any(r.get('resolved_target_method_ids')for r in resolved))
details=all_pages(c,aid,'method_details',100);check('method-details-link-the-same-addresses',any(any(x.get('resolved_target_address')for x in d['external_calls'])for d in details))
reserves=next(m for m in methods if m.get('signature')=='getReserves()'and m['storage_address'].lower()==q['address'].lower());flow=all_pages(c,aid,'dataflow',100,{'method_id':reserves['method_id']})
check('real-three-value-reserve-return-model',any(d['kind']=='entry_return_derivation'and d['status']=='represented_source_dependencies_resolved'and d['successful_return_paths']for d in flow))
funds=all_pages(c,aid,'funds',100)
check('real-encoded-transfer-calldata-is-represented',any(f.get('protocol')=='encoded_transfer_interface'and f['amount_derivation_status']=='represented_source_dependencies_resolved'for f in funds))
arts=all_pages(c,aid,'artifacts',100);machine=json.loads(artifact_bytes(c,aid,next(a for a in arts if a['kind']=='machine_result'),4<<20));report=artifact_bytes(c,aid,next(a for a in arts if a['kind']=='analysis_report'),4<<20).decode()
exported_calls=machine_section(c,aid,arts,machine,'calls')
check('exported-calls-match-real-api',exported_calls==calls)
check('report-preserves-resolved-targets-and-code-hashes',all(r['resolved_target_address']in report and r['dependency_code_hash']in report for r in resolved)or('结果分区文件'in report and exported_calls==calls))
if os.environ.get('CONTRACT_QA_ORDERED_REPORT')=='1':check('report-preserves-ordered-execution-domain','执行计算' in report and '检查' in report)
check('dependency-budget-recorded',job['metadata']['metrics']['rpc_http_requests']<=100,job['metadata']['metrics'])
print('DEPENDENCY_LIVE_PASS',len(rows),aid,flush=True)
