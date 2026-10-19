"""Read the original campaign ledger, including P1 non-analysis material RPC.

Free public source HTTP and a conservative reservation for live source fetches
are separate from billed provider records. None resets the original start.
"""
import argparse,datetime,json,os,pathlib,subprocess,urllib.parse
from decimal import Decimal
from qa_support import dotenv
ROOT=pathlib.Path(__file__).resolve().parents[2];OUT=ROOT/'acceptance/contract-analysis-general'
START='2026-10-14T05:33:05.139606+00:00';P1START='2026-10-16T22:30:47+00:00'
p=argparse.ArgumentParser();p.add_argument('--reserve',type=int,default=0);p.add_argument('--source-upper-bound',type=int,default=0);p.add_argument('--output');a=p.parse_args()
assert 0<=a.reserve<=36 and 0<=a.source_upper_bound<=3
values=dotenv('/home/euripides/.config/blockchain-platform/contract-general-input-qa-20261017.env')
d=urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']);assert d.path=='/contract_analysis_qa_20261014'
query="SELECT json_build_object('http_requests',coalesce(sum(http_requests),0),'credits',coalesce(sum(credits)FILTER(WHERE NOT cache_hit AND NOT singleflight_hit),0),'estimated_usd',coalesce(sum(estimated_cost_usd)FILTER(WHERE NOT cache_hit AND NOT singleflight_hit),0))FROM provider_usage WHERE(usage_type='contract_analysis'AND request_time>='"+START+"')OR(usage_type<>'contract_analysis'AND request_time>='"+P1START+"');"
raw=subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',d.username,'-d',d.path[1:]],input=query.encode(),env={**os.environ,'PGPASSWORD':urllib.parse.unquote(d.password)})
qa=json.loads(raw,parse_float=Decimal)
prior=json.loads((ROOT.parent/'contract-namespaced-storage-20261016/acceptance/contract-analysis-namespaced-storage/publication-summary.json').read_text(),parse_float=Decimal)['usage']['previous_production']
assert prior['http_requests']==123
actual={k:Decimal(str(qa[k]))+Decimal(str(prior[k]))for k in qa};actual['http_requests']=int(actual['http_requests'])
public=json.loads((OUT/'corpus/materials/external-http-ledger.json').read_text());free_http=len(public['requests'])
reservations=OUT/'source-http-reservations.json'
if a.source_upper_bound:
 document={'source_http_upper_bound':a.source_upper_bound,'scope':'one address-selected task max_contracts=3; source-bound single root uses submitted source',
           'basis':'conservative_live_source_fetch_upper_bound_not_a_claim_of_actual_requests','provider_http':0,'credits':0,'estimated_usd':'0'}
 if reservations.exists():assert json.loads(reservations.read_text())==document
 else:reservations.write_text(json.dumps(document,indent=2))
source_reserved=json.loads(reservations.read_text())['source_http_upper_bound'] if reservations.exists() else 0
conservative=actual['http_requests']+free_http+source_reserved
assert conservative+a.reserve<=2000 and conservative+a.reserve-1929<=70,(actual,free_http,source_reserved,a.reserve)
assert actual['credits']+a.reserve*200<=1000000 and actual['credits']+a.reserve*200-385800<=50000
assert actual['estimated_usd']+Decimal(a.reserve)*Decimal('0.00002')<=10 and actual['estimated_usd']-Decimal('0.0385797381')+Decimal(a.reserve)*Decimal('0.00002')<=1
document={'as_of_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'original_start_utc':START,'p1_start_utc':P1START,
          'persisted_qa':qa,'carried_production':prior,'billed_provider_actual':actual,'public_material_http_actual':free_http,
          'live_source_http_upper_bound':source_reserved,'conservative_http_including_upper_bound':conservative,
          'p1_conservative_http':conservative-1929,'next_provider_reservation':a.reserve,'limits':{'original_http':2000,'p1_http':70,'credits':1000000,'p1_credits':50000},
          'material_allocation_deviation':{'planned_requests':18,'conservative_actual_requests':34,'chain_rpc_attempts':18,'sourcify_http_attempts':16}}
background=ROOT/'acceptance/contract-analysis-partial-recovery/background-management-audit.json'
if background.exists():
 assert a.reserve==0,'Background management HTTP is not fully reconciled; no further reservation'
 audit=json.loads(background.read_text())
 document['all_external_http_status']='NOT_VERIFIED_previous_total_excluded_background_management'
 document['available_new_material_request_budget']=0
 document['background_management']={k:audit[k]for k in['management_snapshots','p2_management_snapshots','completed_sync_logical_api_calls','p2_completed_sync_logical_api_calls','management_http_response_lower_bound','management_credits_and_usd','disabled_at_utc']}
text=json.dumps(document,indent=2,default=str)
target=pathlib.Path(a.output)if a.output else OUT/'live-usage.json'
assert target.resolve().is_relative_to(OUT.resolve())or target.resolve().is_relative_to((ROOT/'acceptance/contract-analysis-partial-recovery').resolve())
target.write_text(text)
print(text)
