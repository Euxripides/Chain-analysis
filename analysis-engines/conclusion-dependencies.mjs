import { id, unique } from './model.mjs';
import {createHash} from 'node:crypto';
import {gzipSync} from 'node:zlib';

// Large dependency expansions repeat facts already present in paginated rows.
// Keep their complete, exact representation in one checksum-bound artifact.
export function compactConclusionDependencies(result,inlineLimit=2<<20){
  const document={schema_version:'conclusion-dependencies-v1',snapshot:result.snapshot,conclusions:result.sections.conclusions,analysis_gaps:result.sections.analysis_gaps};
  const raw=Buffer.from(JSON.stringify(document));if(raw.length<=inlineLimit)return result;
  const bytes=gzipSync(raw,{level:6}),sha=createHash('sha256').update(bytes).digest('hex'),artifact_id=id('conclusion-dependencies',sha);
  result.sections.artifacts.push({artifact_id,kind:'conclusion_dependencies',filename:'conclusion-dependencies.json.gz',media_type:'application/gzip',content_base64:bytes.toString('base64'),sha256:sha});
  const index=new Map(document.conclusions.map((c,ordinal)=>[c.conclusion_id,{conclusion_id:c.conclusion_id,support:c.support,dependency_artifact_id:artifact_id,dependency_record:ordinal,representation:'complete_dependencies_in_artifact'}]));
  for(const [section,rows]of Object.entries(result.sections))if(!['conclusions','analysis_gaps','artifacts'].includes(section))for(const row of rows)if(row.analysis_dependencies)row.analysis_dependencies=index.get(row.analysis_dependencies.conclusion_id);
  result.sections.conclusions=document.conclusions.map((c,ordinal)=>({conclusion_id:c.conclusion_id,section:c.section,method_id:c.method_id,support:c.support,dependency_record:ordinal}));
  result.sections.analysis_gaps=document.analysis_gaps.map((g,ordinal)=>({gap_id:g.gap_id,reason:g.reason,affected_conclusion_ids:g.affected_conclusion_ids,dependency_record:ordinal}));
  result.sections.dependency_artifacts=[{artifact_id,encoding:'gzip_json',schema_version:document.schema_version,conclusions:document.conclusions.length,gaps:document.analysis_gaps.length,
    uncompressed_bytes:raw.length,uncompressed_sha256:createHash('sha256').update(raw).digest('hex'),record_paths:{conclusions:'/conclusions/{dependency_record}',gaps:'/analysis_gaps/{dependency_record}'}}];
  return result;
}

// Add references to the evidence already used by each conclusion. This does not
// upgrade a syntactic model, an interface profile or a solver's unknown status.
export function attachConclusionDependencies(result) {
  const sections=result.sections,methods=new Map(sections.methods.map(m=>[m.method_id,m]));
  const expressions=new Map(sections.expressions.map(e=>[e.expression_id,e]));
  const externalReturns=new Map((sections.external_returns||[]).map(e=>[e.dependency_id,e]));
  const dataflows=new Map((sections.dataflow||[]).map(d=>[d.dataflow_id,d]));
  const funds=new Map(sections.funds.map(f=>[f.fund_id,f]));
  const evidence=new Set(sections.evidence.map(e=>e.evidence_id));
  const storage=new Map(sections.storage.map(s=>[s.storage_id,s]));
  const storageByExpression=new Map();
  for(const row of storage.values()){const refs=storageByExpression.get(row.key_expression_id)||[];refs.push(row.storage_id);storageByExpression.set(row.key_expression_id,refs)}
  const gaps=sections.analysis_gaps||=[],conclusions=sections.conclusions||[];
  const memoryKinds={memory_versions:'memory_version_id',source_memory_versions:'source_memory_version_id',source_memory_phis:'source_memory_phi_id',memory_reads:'memory_read_id',
    call_input_bindings:'call_input_binding_id',abi_values:'abi_value_id',abi_decodings:'abi_decoding_id',call_argument_models:'call_argument_model_id',fund_call_models:'fund_call_model_id',
    storage_references:'storage_reference_id',storage_reference_phis:'storage_reference_phi_id',storage_access_bindings:'storage_access_binding_id',
    source_scalar_versions:'source_scalar_version_id',source_scalar_phis:'source_scalar_phi_id'};
  const memoryRows=new Map();for(const [section,key]of Object.entries(memoryKinds))for(const row of sections[section]||[])memoryRows.set(row[key],{row,key});
  const code={code_hash:result.snapshot.code_hash||null,input_sha256:result.snapshot.input_sha256||null,
    code_phase:result.snapshot.code_phase||'runtime',basis:result.snapshot.code_identity?.basis||'observed_runtime'};
  const state=result.snapshot.identity_kind==='code_only'?{kind:'symbolic_state',observed:false,declared_environment:result.snapshot.declared_environment||null}:
    {kind:result.snapshot.state_granularity,observed:true,chain_id:result.snapshot.chain_id,block_number:result.snapshot.block_number,
      block_hash:result.snapshot.block_hash,storage_address:result.snapshot.storage_address};
  for(const section of ['methods','claims','funds','permissions','flows','operating_flows','control_effects','protocol_formulas','configuration_impacts','state_transitions','authorization_models','economic_models','property_results',
    'calls','call_input_bindings','source_memory_versions','source_memory_phis','memory_reads','abi_decodings','call_argument_models','fund_call_models',
    'storage_references','storage_reference_phis','storage_access_bindings','source_scalar_versions','source_scalar_phis']) {
    for(const row of sections[section]||[]) {
      const method=methods.get(row.method_id),primary=memoryKinds[section]||({calls:'call_id',methods:'method_id'}[section]),conclusion_id=id('conclusion-v1',section,code,row.method_id||null,
        (primary&&row[primary])||row.claim_id||row.fund_id||row.guard_id||row.permission_id||row.edge_id||row.flow_id||row.control_effect_id||row.model_id||row.property_id||row.formula_id||row.impact_id||
        row.call_input_binding_id||row.source_memory_version_id||row.source_memory_phi_id||row.memory_read_id||row.abi_decoding_id||row.call_argument_model_id||row.fund_call_model_id||row.call_id||(section==='methods'?row.method_id:row));
      const expression_ids=new Set(),storage_ids=new Set(),state_ids=new Set(),call_ids=new Set(),frame_ids=new Set(),dataflow_ids=new Set(),memory_ids=new Set();
      const collect=(value,key='')=>{
        if(typeof value==='string'&&(/expression_ids?$/.test(key)||key==='operands')&&expressions.has(value))expression_ids.add(value);
        else if(typeof value==='string'&&/storage_ids?$/.test(key)&&storage.has(value))storage_ids.add(value);
        else if(typeof value==='string'&&/state_ids?$/.test(key))state_ids.add(value);
        else if(typeof value==='string'&&/call_ids?$/.test(key))call_ids.add(value);
        else if(typeof value==='string'&&/frame_ids?$/.test(key))frame_ids.add(value);
        else if(typeof value==='string'&&/(?:derivation|dataflow)_ids?$/.test(key)&&dataflows.has(value))dataflow_ids.add(value);
        else if(typeof value==='string'&&memoryRows.has(value))memory_ids.add(value);
        else if(Array.isArray(value))for(const item of value)collect(item,key);
        // A replaced scalar binding is event history. Its actual value inputs
        // are the captured expression and explicit phi parents, not the old lhs.
        else if(value&&typeof value==='object')for(const [k,v]of Object.entries(value))if(!['analysis_dependencies','raw_node','text','source','bytes','payload_cells','content_base64','encoded_hex','previous_source_scalar_version_id'].includes(k))collect(v,k);
      };collect(row);if(section==='flows'&&funds.has(row.fund_id))collect(funds.get(row.fund_id));
      const expandedMemory=new Set(),expandedExpressions=new Set(),expandedDataflows=new Set();let changed=true;
      while(changed){changed=false;
        for(const ref of memory_ids){if(expandedMemory.size>=512)break;if(!expandedMemory.has(ref)){expandedMemory.add(ref);collect(memoryRows.get(ref).row);changed=true;}}
        for(const ref of dataflow_ids){if(expandedDataflows.size>=4096)break;if(!expandedDataflows.has(ref)){expandedDataflows.add(ref);collect(dataflows.get(ref));changed=true;}}
        for(const ref of expression_ids){if(expandedExpressions.size>=4096)break;if(!expandedExpressions.has(ref)){expandedExpressions.add(ref);collect(expressions.get(ref));changed=true;}}
      }
      for(const ref of expression_ids)for(const storage_id of storageByExpression.get(ref)||[])storage_ids.add(storage_id);
      for(const ref of storage_ids)if(storage.get(ref)?.state_id)state_ids.add(storage.get(ref).state_id);
      const local_evidence=unique([...(row.evidence_ids||[]),...[...expression_ids].flatMap(x=>expressions.get(x)?.evidence_ids||[]),...[...expandedMemory].flatMap(x=>memoryRows.get(x).row.evidence_ids||[])]).filter(x=>evidence.has(x));
      const missing_evidence=unique(row.evidence_ids||[]).filter(x=>!evidence.has(x));
      const unresolved=[...(row.unresolved||[]),...(section==='methods'?(row.completeness?.unresolved||[]):[])];
      if(memory_ids.size>expandedMemory.size)unresolved.push({code:'MEMORY_DEPENDENCY_TRAVERSAL_BUDGET',remaining:memory_ids.size-expandedMemory.size});
      if(dataflow_ids.size>expandedDataflows.size)unresolved.push({code:'DATAFLOW_DEPENDENCY_TRAVERSAL_BUDGET',remaining:dataflow_ids.size-expandedDataflows.size});
      for(const ref of expandedMemory)for(const reason of memoryRows.get(ref).row.unresolved||[])unresolved.push({code:typeof reason==='string'?reason:reason.code||'MEMORY_OR_ABI_DEPENDENCY_UNRESOLVED',memory_or_abi_id:ref});
      if(section!=='methods')for(const gap of method?.completeness?.unresolved||[]) {
        if(/^SOURCE_(?:METHOD|TOTAL)_|^SEVM_CHILD_/.test(gap.code||''))unresolved.push({
          code:'METHOD_ANALYSIS_INCOMPLETE',cause:gap.code,method_id:method.method_id,scope:gap.scope||'method_body',location:gap.location||null,
        });
      }
      for(const ref of dataflow_ids)for(const reason of dataflows.get(ref)?.unresolved||[])unresolved.push({code:reason,dataflow_id:ref,expression_id:dataflows.get(ref).expression_id||null});
      const externalDependencies=[];
      for(const ref of expression_ids){
        const expr=expressions.get(ref);
        if(expr?.unresolved_local_reference)unresolved.push({code:'UNRESOLVED_LOCAL_VALUE',expression_id:ref,location:expr.location||null});
        for(const code of expr?.unresolved_model_reasons||[])unresolved.push({code,expression_id:ref,scope:'represented_expression_dependency'});
        if(expr?.source_aggregate_binding_unavailable)unresolved.push({code:'SOURCE_AGGREGATE_FIELD_VALUE_UNAVAILABLE',expression_id:ref});
        const dependency=externalReturns.get(expr?.external_return_dependency_id);
        if(dependency){externalDependencies.push(dependency.dependency_id);if(dependency.status!=='bound_to_observed_execution')unresolved.push({code:'EXTERNAL_RETURN_REQUIRES_EXECUTION_BINDING',dependency_id:dependency.dependency_id,expression_id:ref,location:dependency.source_callsite,status:dependency.status})}
      }
      if(expression_ids.size>expandedExpressions.size)unresolved.push({code:'DEPENDENCY_TRAVERSAL_BUDGET',remaining:expression_ids.size-expandedExpressions.size});
      if(missing_evidence.length)unresolved.push({code:'MISSING_EVIDENCE_REFERENCE',evidence_ids:missing_evidence});
      const gap_ids=[];
      for(const item of unresolved){
        const gap_id=id('analysis-gap',conclusion_id,item);gap_ids.push(gap_id);
        gaps.push({gap_id,reason:typeof item==='string'?item:item.code||item.reason||'unresolved_dependency',detail:item,
          method_id:row.method_id||null,location:typeof item==='object'?item.location||null:null,
          scope:section==='methods'?'method_completeness':'conclusion_dependency',affected_conclusion_ids:[conclusion_id]});
      }
      const status=row.proof_status||row.status;
      const support=gap_ids.length?'pending':status==='proved_under_declared_model'?'proved_under_declared_model':
        ['unknown','unsupported','incomplete','partial','invalid','invalid_representation','untyped_bytes','solver_unavailable','infeasible_assumptions','pending'].includes(status)?'pending':
        local_evidence.length?'static_derivation':'pending';
      const refs={conclusion_id,code_identity:code,state_context:{...state,storage_address:row.storage_address||method?.storage_address||state.storage_address},
        support,conditions:row.conditions||row.path_conditions||[],assumptions:row.assumptions||[],external_return_dependency_ids:unique(externalDependencies),
        evidence_ids:local_evidence,expression_ids:[...expression_ids],dataflow_ids:[...dataflow_ids],storage_ids:[...storage_ids],state_ids:[...state_ids],
        call_ids:[...call_ids],frame_ids:[...frame_ids],
        ...Object.fromEntries(Object.values(memoryKinds).map(key=>[key+'s',[...memory_ids].filter(ref=>memoryRows.get(ref)?.key===key)])),condition_groups:row.encoding_condition_groups||row.condition_groups||[],
        trace_refs:row.trace_refs||[],proof_obligation_ids:row.obligation_ids||row.proof_obligation_ids||row.obligations||[],gap_ids,
        settlement:section==='funds'?row.settlement||'static_intent':undefined};
      row.analysis_dependencies=refs;
      conclusions.push({conclusion_id,section,method_id:row.method_id||null,...refs});
    }
  }
  sections.analysis_gaps=[...new Map(gaps.map(g=>[g.gap_id,g])).values()];
  sections.conclusions=[...new Map(conclusions.map(c=>[c.conclusion_id,c])).values()];
  return compactConclusionDependencies(result);
}
