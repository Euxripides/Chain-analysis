import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {hash,id} from '../model.mjs';

export const and=xs=>xs.length?'(and '+xs.join(' ')+')':'true';
export const or=xs=>xs.length?'(or '+xs.join(' ')+')':'false';
export const number=n=>BigInt(n)<0n?'(- '+(-BigInt(n))+')':String(n);
export function obligation(model,kind,declarations,domain,violation,inputs=[]) {
 const property_id=id(model.model_id,kind,model.obligations?.length||0);
 const property={property_id,model_id:model.model_id,method_id:model.method_id,property:kind,
  code_hash:model.code_hash,code_address:model.code_address,storage_address:model.storage_address,
  assumptions:[...(model.assumptions||[])],proof_scope:model.scope,all_paths_proved:false,status:'not_run',evidence_ids:[...(model.evidence_ids||[])]};
 const query={property_id,declarations:'(set-option :timeout 300)\n(set-option :rlimit 1000000)\n'+declarations,domain,violation,
  variables:inputs.map(i=>i.name),inputs};
 model.obligations||=[];model.obligations.push(property_id);
 return {property,query};
}

export function solveObligations(result,items,{limit=48}={}) {
 result.sections.verification_obligations||=[];
 const available=items.slice(0,limit),python=process.env.CONTRACT_PYTHON||'/opt/contract-python/bin/python';
 for(let from=0;from<available.length;from+=24) {
  const batch=available.slice(from,from+24),run=spawnSync(python,[fileURLToPath(new URL('../properties/solver.py',import.meta.url))],
   {input:JSON.stringify({queries:batch.map(x=>x.query)}),encoding:'utf8',timeout:20000,maxBuffer:2<<20,env:{...process.env,PYTHONDONTWRITEBYTECODE:'1'}});
  let answers=[];try{if(run.status===0)answers=JSON.parse(run.stdout);}catch{}
  for(const {property,query}of batch) {
   const answer=answers.find(x=>x.property_id===property.property_id);
   property.status=!answer?'solver_unavailable':answer.domain==='sat'&&answer.decision==='unsat'?'proved_under_declared_model':
    answer.domain==='sat'&&answer.decision==='sat'?'counterexample':answer.domain==='unsat'?'infeasible_assumptions':'unknown';
   if(answer)property.solver=answer;
   else property.reason=String(run.error?.code||'VERIFICATION_SOLVER_UNAVAILABLE');
   if(answer?.counterexample_smt)property.counterexample={inputs:query.inputs,values_smt:answer.counterexample_smt};
  }
 }
 for(const [index,{property,query}]of items.entries()) {
  if(index>=limit){property.status='budget_exhausted';property.reason='VERIFICATION_OBLIGATION_BUDGET';}
  const artifact_id=id(property.property_id,'proof');property.proof_artifact_id=artifact_id;
  const text=JSON.stringify({schema_version:'contract-property-proof-v1',property,query});
  result.sections.artifacts.push({artifact_id,kind:'property_proof',filename:'verification-'+property.property_id+'.json',media_type:'application/json',text,sha256:hash(text)});
  result.sections.verification_obligations.push(JSON.parse(text).property);
 }
 return new Map(items.map(x=>[x.property.property_id,x.property]));
}

export function closeModels(models,solved) {
 for(const model of models) {
  const rows=(model.obligations||[]).map(key=>solved.get(key));
  model.complete=Boolean(rows.length)&&!(model.unresolved||[]).length&&rows.every(p=>p?.status==='proved_under_declared_model');
  model.proof_counts=Object.fromEntries([...new Set(rows.map(p=>p?.status||'not_run'))].map(s=>[s,rows.filter(p=>(p?.status||'not_run')===s).length]));
  model.status=model.complete?'proved_under_declared_model':rows.some(p=>p?.status==='counterexample')?'counterexample':'incomplete';
 }
}
