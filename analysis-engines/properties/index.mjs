import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {hash,id,unique} from '../model.mjs';
import {propertyTranslator} from './smt.mjs';

export function applyPropertyRules(result) {
  const s=result.sections,queries=[],properties=[],methods=new Map(s.methods.map(m=>[m.method_id,m]));
  const add=(property,build)=>{
    if(properties.length>=24)return;
    Object.assign(property,{proof_scope:'represented_source_model',code_hash:methods.get(property.method_id)?.code_hash,
      storage_address:methods.get(property.method_id)?.storage_address,all_paths_proved:false,status:'not_run'});
    properties.push(property);s.property_obligations.push(property);
    try{queries.push({...build(),property_id:property.property_id});}catch(e){property.status='unsupported';property.reason=String(e.message);}
  };
  for(const guard of s.permissions)for(const c of guard.caller_constraints||[]) {
    const m=methods.get(guard.method_id),derivation=s.dataflow.find(d=>d.dataflow_id===guard.condition_derivation_id);
    if(!m||derivation?.status==='partial')continue;
    add({property_id:id(guard.guard_id,c.constraint_id,'required-caller'),method_id:m.method_id,guard_id:guard.guard_id,
      constraint_id:c.constraint_id,property:'successful_guard_requires_caller_equality',
      proposition:c.principal+' == '+c.target_expression,assumptions:['represented_guard_succeeds','typed_symbolic_state_and_environment'],
      evidence_ids:unique([...guard.evidence_ids,...c.evidence_ids])},()=>{
      const t=propertyTranslator(result,m,derivation?.definition_ids),condition=t.walk(guard.expression_id),target=t.walk(c.target_expression_id);
      const caller=[...t.exprs.values()].find(e=>e.text.replace(/\s/g,'')===c.principal);
      if(!caller)throw new Error('SMT_CALLER_UNAVAILABLE');const principal=t.walk(caller.expression_id);
      return t.query(t.and([condition.ok,target.ok,principal.ok,t.bool(condition)]),'(not (= '+principal.term+' '+target.term+'))');
    });
  }
  for(const guard of s.permissions)for(const predicate of guard.state_predicates||[]) {
    const m=methods.get(guard.method_id),derivation=s.dataflow.find(d=>d.dataflow_id===guard.condition_derivation_id);
    if(!m||derivation?.status==='partial')continue;
    add({property_id:id(guard.guard_id,predicate.predicate_id,'required-state'),method_id:m.method_id,guard_id:guard.guard_id,
      predicate_id:predicate.predicate_id,property:'successful_guard_requires_state_predicate',
      proposition:predicate.expected?predicate.expression:'!('+predicate.expression+')',
      assumptions:['represented_guard_succeeds','typed_symbolic_state_and_environment','mapping_keys_share_storage_identity'],
      evidence_ids:unique([...guard.evidence_ids,...predicate.evidence_ids])},()=>{
      const t=propertyTranslator(result,m,derivation?.definition_ids),condition=t.walk(guard.expression_id),value=t.walk(predicate.expression_id);
      return t.query(t.and([condition.ok,value.ok,t.bool(condition)]),predicate.expected?'(not '+t.bool(value)+')':t.bool(value));
    });
  }
  for(const m of methods.values()) {
    if(m.entry_return_model_status!=='represented_source_dependencies_resolved'||m.outputs.length!==2||m.outputs.some(o=>!/^uint\d*$/.test(o.type)))continue;
    const root=s.expressions.find(e=>e.expression_id===m.entry_return_expression_id),model=root?.entry_return_model;
    if(!model?.paths?.length)continue;
    const exprs=new Map(s.expressions.filter(e=>e.method_id===m.method_id).map(e=>[e.expression_id,e]));
    const peel=key=>{let e=exprs.get(key),seen=new Set();while(e&&!seen.has(e.expression_id)){
      seen.add(e.expression_id);if(e.binding_expression_id)e=exprs.get(e.binding_expression_id);
      else if(e.symbolic_format==='identity')e=exprs.get(e.operands[0]);else break;
    }return e;};
    // Discover a conservation candidate from subtraction and the same returned
    // subtrahend, rather than treating output names as an economic invariant.
    const candidate=model.paths.map(p=>{const net=peel(p.expression_ids[0]),charge=peel(p.expression_ids[1]);
      if(!net||!charge||(net.operator||net.op)!=='-')return null;
      const left=peel(net.operands[0]),right=peel(net.operands[1]);
      if(right?.expression_id!==charge.expression_id)return null;
      return m.parameters.find(p=>p.source_ast_id===left?.referenced_declaration&&/^uint\d*$/.test(p.type));
    }).find(Boolean);
    if(!candidate)continue;
    add({property_id:id(m.method_id,candidate.index,'return-conservation'),method_id:m.method_id,
      property:'returned_components_conserve_input',proposition:'uint(net) + uint(charge) == uint('+candidate.name+')',
      assumptions:['successful_represented_return_path','typed_symbolic_inputs_and_state'],input_index:candidate.index,
      evidence_ids:m.evidence_ids},()=>{
      const t=propertyTranslator(result,m),input=[...t.exprs.values()].find(e=>e.op==='Identifier'&&e.referenced_declaration===candidate.source_ast_id&&!e.binding_expression_id&&!e.unresolved_local_reference);
      if(!input)throw new Error('SMT_INPUT_UNAVAILABLE');const amount=t.walk(input.expression_id);
      const cases=model.paths.map(p=>{const [a,b]=p.expression_ids.map(t.walk),domain=t.and([t.pathDomain(p),a.ok,b.ok,amount.ok]);
        return {domain,violation:'(not (= (+ '+a.term+' '+b.term+') '+amount.term+'))'};});
      return t.query(t.or(cases.map(c=>c.domain)),t.or(cases.map(c=>t.and([c.domain,c.violation]))));
    });
  }
  if(!queries.length)return;
  const started=performance.now(),python=process.env.CONTRACT_PYTHON||'/opt/contract-python/bin/python';
  const run=spawnSync(python,[fileURLToPath(new URL('./solver.py',import.meta.url))],{input:JSON.stringify({queries}),encoding:'utf8',timeout:20000,maxBuffer:2<<20,
    env:{...process.env,PYTHONDONTWRITEBYTECODE:'1'}});
  let solved=[];try{if(run.status!==0)throw new Error(run.error?.code||'SMT_SOLVER_UNAVAILABLE');solved=JSON.parse(run.stdout);}catch(e){for(const p of properties.filter(p=>p.status==='not_run')){p.status='solver_unavailable';p.reason=String(e.message);}}
  for(const query of queries) {
    const property=properties.find(p=>p.property_id===query.property_id),answer=solved.find(r=>r.property_id===query.property_id);
    if(answer){property.solver=answer;
      property.status=answer.domain==='sat'&&answer.decision==='unsat'?'proved_under_declared_model':answer.decision==='sat'?'counterexample':answer.domain==='unsat'?'infeasible_assumptions':'unknown';
      if(answer.counterexample_smt)property.counterexample={inputs:query.inputs,values_smt:answer.counterexample_smt};}
    const artifact_id=id(property.property_id,'proof');property.proof_artifact_id=artifact_id;
    const text=JSON.stringify({schema_version:'contract-property-proof-v1',property,query});
    s.artifacts.push({artifact_id,kind:'property_proof',filename:'property-'+property.property_id+'.json',method_id:property.method_id,media_type:'application/json',text,sha256:hash(text)});
  }
  for(const m of methods.values())m.property_ids=properties.filter(p=>p.method_id===m.method_id).map(p=>p.property_id);
  for(const detail of s.method_details)detail.property_ids=methods.get(detail.method_id)?.property_ids||[];
  s.engines.push({engine:'z3-properties',status:run.status===0?'completed':'unavailable',scope:'declared_source_properties',
    property_count:properties.length,proved:properties.filter(p=>p.status==='proved_under_declared_model').length,
    counterexamples:properties.filter(p=>p.status==='counterexample').length,duration_ms:Math.round(performance.now()-started),version:solved.find(r=>r.version)?.version||null});
}
