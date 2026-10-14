"""Validate the formerly failing 24KB bytecode, full method views and export index."""
import hashlib,io,json,os,pathlib,sys,zipfile
os.environ.setdefault('CONTRACT_QA_API','http://127.0.0.1:18091');os.environ.setdefault('CONTRACT_QA_OUT','acceptance/contract-analysis-coverage')
from qa_support import ROOT,fixture_clients
from live_api_qa import all_pages,artifact_bytes
OUT=ROOT/'acceptance/contract-analysis-coverage';rows=[]
def check(name,ok,evidence=None):
 rows.append({'scenario':name,'status':'PASS'if ok else'FAIL','evidence':evidence});(OUT/'large-export-qa.json').write_text(json.dumps(rows,indent=2));print(name,rows[-1]['status'],flush=True)
 if not ok:raise AssertionError(name)
c=fixture_clients()['investigator'];aid='4ab9ed89-5202-468d-a071-7020eb0428e4';job=c.must('/api/v2/contract-analyses/'+aid)
check('previously_oversized_contract_completes',job['status']=='completed',{'analysis_id':aid,'counts':job['metadata'].get('sections')})
artifacts=all_pages(c,aid,'artifacts',100);byid={a['artifact_id']:a for a in artifacts};details=all_pages(c,aid,'method_details',100)
large=[d for d in details if d.get('full_view_artifact_id')];check('large_views_are_complete_references',bool(large))
for detail in large:
 data=artifact_bytes(c,aid,byid[detail['full_view_artifact_id']],4<<20);original=json.loads(data)
 check('complete_detail_hash_'+detail['method_id'],hashlib.sha256(data).hexdigest()==detail['full_view_sha256']and original['method_id']==detail['method_id'],len(data))
 for field,reference in detail['field_references'].items():
  values=all_pages(c,aid,reference['section'],200,{'method_id':detail['method_id']})
  check('referenced_'+field+'_'+detail['method_id'],len(original.get(field,[]))==reference['represented_items']and(len(values)>=reference['represented_items']or field=='returned_values'),{'view_items':reference['represented_items'],'section_items':len(values)})
machine=next(a for a in artifacts if a['kind']=='machine_result');index=json.loads(artifact_bytes(c,aid,machine,4<<20))
check('large_machine_result_has_section_index',index.get('representation')=='complete_section_artifacts_v1')
for section,parts in index['section_artifacts'].items():
 count=0
 for part in parts:
  data=artifact_bytes(c,aid,byid[part['artifact_id']],4<<20);values=json.loads(data)
  assert len(values)==part['record_count']and count==part['from_ordinal']and hashlib.sha256(data).hexdigest()==part['sha256'];count+=len(values)
 check('export_section_complete_'+section,count==job['metadata']['sections'][section],count)
bundle=next(a for a in artifacts if a['kind']=='evidence_bundle');data=artifact_bytes(c,aid,bundle,4<<20)
with zipfile.ZipFile(io.BytesIO(data))as archive:
 manifest=json.loads(archive.read('manifest.json'));assert set(archive.namelist())=={'manifest.json'}|{e['path']for e in manifest['entries']}
 for entry in manifest['entries']:
  blob=archive.read(entry['path']);assert len(blob)==entry['byte_size']and hashlib.sha256(blob).hexdigest()==entry['sha256']
check('large_bundle_every_file_hash',True,{'files':len(manifest['entries']),'bytes':len(data),'sha256':hashlib.sha256(data).hexdigest()})
print('LARGE_EXPORT_QA_PASS',len(rows),flush=True)
