import json
from qa_support import ROOT,fixture_clients
import os
from live_api_qa import await_job,all_pages
OUT=ROOT/os.environ.get('CONTRACT_QA_OUT','acceptance/contract-analysis');rows=[]
def check(name,ok,evidence=None):
 rows.append({'scenario':name,'status':'PASS' if ok else 'FAIL','evidence':evidence});(OUT/'execution-live-qa.json').write_text(json.dumps(rows,indent=2));print(name,rows[-1]['status'],flush=True)
 if not ok:raise AssertionError(name)
c=fixture_clients()['investigator'];tx='0xecdc668e16ada76f439b09c9b8c2faa7b76f80244933193c7691ac797e26eae7'
q={'chain':'ethereum','address':'0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2','block':19000000,'tx_hashes':[tx],'idempotency_key':'execution-live-'+(OUT/'engine-image.txt').read_text().strip()[7:19]+os.environ.get('CONTRACT_QA_RUN_SUFFIX',''),'budget':{'max_contracts':3,'max_rpc_requests':120,'max_state_reads':24}}
submission=c.must('/api/v2/contract-analyses',q);aid=submission['analysis_id'];(OUT/'execution-analysis-id.txt').write_text(aid);job=await_job(c,aid,600)
check('real-complex-execution-completes',job['status']=='completed',{'analysis_id':aid,'error':job.get('error_code')})
sections={section:all_pages(c,aid,section,200) for section in ['transactions','execution_calls','executed_funds','token_events','token_balance_changes','reconciliations','replays','methods','storage','coverage','evidence','execution_evidence','artifacts']}
(OUT/'execution-live-sections.json').write_text(json.dumps(sections,indent=2));(OUT/'execution-live-status.json').write_text(json.dumps(job,indent=2))
replay=sections['replays'][0]
check('real-witness-replay-matches-observations',replay['complete'] and replay['status']=='matched_observations' and replay['differences']==[] and replay['witness_gaps']==[],{'gas':replay['replayed']['gas_used'],'logs':len(replay['replayed']['logs']),'slots':replay['state_slots_checked']})
weth='0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2';ledger=next(r for r in sections['reconciliations'] if r['asset']==weth)
check('weth-transfer-and-withdrawal-net-ledger-matches',ledger['status']=='transaction_net_ledger_matched' and ledger['addresses_checked']==2,ledger)
check('weth-withdrawal-receipt-is-code-identified',any(e['event']=='Withdrawal' and e['protocol']=='WETH9' for e in sections['token_events']))
check('net-match-does-not-claim-individual-event-proof',ledger['individual_event_settlement_verified'] is False)
check('all-weth-observed-net-changes-match',all(row['net_matched'] for row in sections['token_balance_changes'] if row['asset']==weth))
check('delegatecall-keeps-code-storage-and-caller-context',any(call['call_type']=='DELEGATECALL' and call['code_address']!=call['storage_address'] and call['effective_caller']=='0x038dc0aea8aaeecac40339ac9a28555825c88d7a' for call in sections['execution_calls']))
check('native-withdraw-and-forwarding-remain-two-edges',len(sections['executed_funds'])==2 and sections['executed_funds'][0]['amount_raw']==sections['executed_funds'][1]['amount_raw'])
check('historic-trace-uses-transaction-prestate-code',all(call['method_identity_status']=='transaction_prestate_code_and_storage_context' for call in sections['execution_calls']))
check('budget-retains-real-usage',job['metadata']['metrics']['rpc_http_requests']<=120 and job['metadata']['metrics']['durable_budget']['reserved_rpc_http_requests']<=120,job['metadata']['metrics'])
check('dependency-cap-is-visible',len(sections['methods'])>12 and job['metadata']['completeness']=='partial' and any(item['complete'] is False for item in sections['coverage']))
evidence={item['evidence_id']:item for item in sections['evidence']+sections['execution_evidence']};artifacts={item['artifact_id'] for item in sections['artifacts']}
for section in ['execution_calls','executed_funds','token_events','token_balance_changes','replays']:
 check('runtime-evidence-resolves-'+section,all(eid in evidence for item in sections[section] for eid in item.get('evidence_ids',[])))
check('runtime-raw-artifact-links-resolve',all(item.get('artifact_id') in artifacts for item in sections['execution_evidence']))
print('EXECUTION_LIVE_QA_PASS',len(rows),aid,flush=True)
