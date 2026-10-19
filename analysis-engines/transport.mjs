import{gzipSync}from'node:zlib';
import{createHash}from'node:crypto';
import{id,stringify}from'./model.mjs';
const sha=b=>createHash('sha256').update(b).digest('hex');
// These four schemas are parsed and scoped by the backend before storage.
// Ordinary text artifacts can use the binary artifact path without changing
// their kind or ID; the index retains the original name, type and checksum.
export function compressLargeTextArtifacts(result){
 const parsed=new Set(['property_proof','state_transition_semantics','source_pc_map','conclusion_dependencies']),records=[];
 const artifacts=(result.sections?.artifacts||[]).map(artifact=>{
  if(typeof artifact.text!=='string'||typeof artifact.filename!=='string'||!artifact.filename||typeof artifact.media_type!=='string'||!artifact.media_type||Object.hasOwn(artifact,'content_base64')||parsed.has(artifact.kind))return artifact;
  const raw=Buffer.from(artifact.text);if(raw.length<(16<<10)||raw.length>(32<<20)||sha(raw)!==artifact.sha256)return artifact;
  const bytes=gzipSync(raw,{level:6});if(bytes.length>=raw.length)return artifact;
  const digest=sha(bytes),filename=artifact.filename+'.gz';records.push({kind:artifact.kind,filename,compressed_sha256:digest,original_filename:artifact.filename,original_media_type:artifact.media_type,original_sha256:artifact.sha256,uncompressed_sha256:sha(raw),uncompressed_bytes:raw.length,compressed_bytes:bytes.length});
  return Object.fromEntries(Object.entries(artifact).map(([key,value])=>key==='text'?['content_base64',bytes.toString('base64')]:[key,key==='filename'?filename:key==='media_type'?'application/gzip':key==='sha256'?digest:value]));
 });
 if(!records.length)return result;
 const document={schema_version:'text-artifact-compression-v1',snapshot:result.snapshot,records},text=stringify(document),digest=sha(text),index={artifact_id:id(document.schema_version,digest),kind:'text_artifact_compression',filename:'text-artifact-compression.json',media_type:'application/json',text,sha256:digest};
 return{...result,sections:{...result.sections,artifacts:[...artifacts,index]},metrics:{...result.metrics,text_artifact_transport:{schema_version:document.schema_version,records:records.length,uncompressed_bytes:records.reduce((n,r)=>n+r.uncompressed_bytes,0),compressed_bytes:records.reduce((n,r)=>n+r.compressed_bytes,0),index_kind:index.kind}}};
}
// Keep the complete expanded return paths in an ordinary evidence artifact. The row
// order and all semantic fields stay intact; only an oversized packet uses this
// representation, so existing ordinary results remain byte-for-byte unchanged.
export function compactReturnPaths(result){
 const selected=[];
 for(const row of result.sections?.dataflow||[]){
  if(typeof row.dataflow_id!=='string'||Object.hasOwn(row,'successful_return_paths_reference')||!Array.isArray(row.successful_return_paths)||!row.successful_return_paths.length)continue;
  const raw=Buffer.from(stringify(row.successful_return_paths));if(raw.length<1024)continue;
  selected.push({row,raw,ordinal:selected.length});
 }
 if(!selected.length)return result;
 const document={schema_version:'dataflow-return-paths-v1',snapshot:result.snapshot,records:selected.map(({row})=>({dataflow_id:row.dataflow_id,method_id:row.method_id,successful_return_paths:row.successful_return_paths}))},raw=Buffer.from(stringify(document)),bytes=gzipSync(raw,{level:6}),digest=sha(bytes),artifact_id=id('dataflow-return-paths-v1',digest),index=new Map(selected.map(v=>[v.row,v]));
 const dataflow=result.sections.dataflow.map(row=>{
  const item=index.get(row);if(!item)return row;
  return Object.fromEntries(Object.entries(row).map(([key,value])=>key==='successful_return_paths'?['successful_return_paths_reference',{artifact_id,record_path:'/records/'+item.ordinal+'/successful_return_paths',sha256:sha(item.raw),record_count:item.row.successful_return_paths.length}]:[key,value]));
 });
 const artifact={artifact_id,kind:'dataflow_return_paths',filename:'dataflow-return-paths.json.gz',media_type:'application/gzip',content_base64:bytes.toString('base64'),sha256:digest};
 return{...result,sections:{...result.sections,dataflow,artifacts:[...(result.sections.artifacts||[]),artifact]},metrics:{...result.metrics,return_path_transport:{schema_version:document.schema_version,records:selected.length,uncompressed_bytes:raw.length,uncompressed_sha256:sha(raw),artifact_kind:artifact.kind}}};
}

// Match the backend's established complete_method_details representation.
// Paginated executable sections remain intact; only their duplicated view is
// moved to a complete artifact before the normal text-artifact compression.
export function compactMethodDetails(result){
 const artifacts=[],details=[],records=[];
 const fields={sequence:'steps',conditions:'permissions',asset_effects:'funds',state_reads:'storage',state_writes:'storage',returned_values:'dataflow',calculation_rules:'expressions'};
 for(const row of result.sections?.method_details||[]){
  if(typeof row.method_id!=='string'||row.full_view_artifact_id){details.push(row);continue;}
  const text=stringify(row),byte_size=Buffer.byteLength(text);if(byte_size<16<<10||byte_size>32<<20){details.push(row);continue;}
  const digest=sha(text),artifact_id=id('complete-method-details',row.method_id,digest),field_references={};
  for(const[field,section]of Object.entries(fields))field_references[field]={section,method_id:row.method_id,represented_items:Array.isArray(row[field])?row[field].length:0,selection:field};
  const compact=Object.fromEntries(Object.entries(row).map(([key,value])=>[key,Object.hasOwn(fields,key)||key==='evidence_ids'?[]:value]));
  for(const field of Object.keys(fields))if(!Object.hasOwn(compact,field))compact[field]=[];
  compact.evidence_ids=[];compact.evidence_reference={source_sections:['steps','funds','storage','permissions','calls'],method_id:row.method_id,lookup:'follow_each_saved_row_evidence_ids'};
  Object.assign(compact,{field_references,representation:'complete_view_in_artifact_and_paginated_method_sections',full_view_scope:'engine_static_view_with_live_call_dependencies_inline',full_view_artifact_id:artifact_id,full_view_sha256:digest,full_view_byte_size:byte_size});
  // The backend enriches external_calls in-place. Keep them inline and keep
  // this row below its existing1MiB re-compaction threshold.
  if(Buffer.byteLength(stringify(compact))>1<<20){details.push(row);continue;}
  artifacts.push({artifact_id,kind:'complete_method_details',filename:'method-'+row.method_id+'-details.json',media_type:'application/json',text,sha256:digest,method_id:row.method_id});details.push(compact);records.push({method_id:row.method_id,artifact_id,original_sha256:digest,original_bytes:byte_size});
 }
 if(!records.length)return result;
 return{...result,sections:{...result.sections,method_details:details,artifacts:[...(result.sections.artifacts||[]),...artifacts]},metrics:{...result.metrics,method_detail_transport:{representation:'complete_view_in_artifact_and_paginated_method_sections',records,minimum_view_bytes:16<<10,preserved_inline_fields:['external_calls']}}};
}
export function encodeEngineResult(result,wireLimit=64<<20,expandedLimit=128<<20){
 let text=stringify(result),expandedBytes=Buffer.byteLength(text);
 if(expandedBytes>expandedLimit){text='';let compact=compressLargeTextArtifacts(compactMethodDetails(result));text=stringify(compact);expandedBytes=Buffer.byteLength(text);if(expandedBytes>expandedLimit){text='';compact=compactReturnPaths(compact);text=stringify(compact);expandedBytes=Buffer.byteLength(text);}}
 if(expandedBytes>expandedLimit)throw Error('ENGINE_EXPANDED_OUTPUT_LIMIT_EXCEEDED');
 if(expandedBytes<=wireLimit)return text;
 const raw=Buffer.from(text);
 const bytes=gzipSync(raw,{level:6});if(bytes.length>32<<20)throw Error('ENGINE_COMPRESSED_OUTPUT_LIMIT_EXCEEDED');
 const envelope=stringify({version:result.version,rule_version:result.rule_version,snapshot:result.snapshot,sections:{engine_transport:[{
  encoding:'engine-transport-gzip-v1',content_base64:bytes.toString('base64'),byte_size:bytes.length,sha256:sha(bytes),expanded_bytes:raw.length,expanded_sha256:sha(raw),
 }]},metrics:{transport:'gzip_json',expanded_bytes:raw.length,wire_compressed_bytes:bytes.length}});
 if(Buffer.byteLength(envelope)>wireLimit)throw Error('ENGINE_WIRE_OUTPUT_LIMIT_EXCEEDED');
 return envelope;
}
