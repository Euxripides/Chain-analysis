import {id,unique} from '../model.mjs';
import {asBool,asInt,scalarType,MAX_WORD} from './terms.mjs';
import {and,or,obligation,solveObligations,closeModels} from './solver.mjs';
import {buildCreationTransition} from './state.mjs';
import {signaturePolicies} from './signatures.mjs';
import {namespaceAccessPath,namespaceCalleeID,namespaceGetterPath} from '../namespace-layout.mjs';

const returnNode=fn=>fn?.body?.statements?.length===1&&fn.body.statements[0].nodeType==='Return'?fn.body.statements[0].expression:null;
const identifier=d=>({...d,nodeType:'Identifier',referencedDeclaration:d.id});
function getter(ctx,name,inputs) {
 const fn=ctx.functions.find(f=>f.name===name&&f.parameters?.parameters?.length===inputs&&['public','external'].includes(f.visibility)&&ctx.methodByAST.get(f.id)?.dispatch_role!=='overridden_source_definition');
 if(fn)return {fn,...(namespaceGetterPath(fn,ctx.namespace_registry,ctx.nodes,ctx.functions)||{node:returnNode(fn)})};
 const d=ctx.stateVars.find(v=>v.name===name&&v.visibility==='public');
 return d?{node:identifier(d),declaration:d}:null;
}
function root(node,ctx) {
 const ns=namespaceAccessPath(node,ctx.namespace_registry,{resolveFunction:n=>namespaceCalleeID(n,ctx.nodes,ctx.functions)});if(ns&&!ns.namespace_root)return ns.root;
 if(node?.nodeType==='Identifier')return ctx.nodes.get(node.referencedDeclaration)?.stateVariable?node.referencedDeclaration:null;
 return node?root(node.baseExpression||node.expression,ctx):null;
}

export function analyzePolicies(result,ctx,transitions) {
 const models=[],queries=[];
 const getterRoot=g=>g?.namespace_path?.root??root(g?.node,ctx);
 function getterValue(t,g,state){
  if(g.namespace_path&&g.fn){const rows=t.call(g.fn,[],state);if(rows.length!==1||rows[0].values.length!==1||rows[0].state.storage!==state.storage)throw Error('NAMESPACE_GETTER_VALUE_NOT_SINGLE_READ');return rows[0].values[0];}
  return t.expr(g.node,state);
 }
 function getterLocation(t,g,state){
  const p=g.namespace_path;if(!p)return t.locate(g.node,state);
  if(p.static_slot===null||p.keys.length)throw Error('NAMESPACE_GETTER_REQUIRES_KEY_MODEL');return{...p,slot:p.static_slot};
 }
 function model(kind,scope,extra={}) {
  const m={model_id:id(result.snapshot.code_hash,result.snapshot.storage_address,kind),kind,scope,code_hash:result.snapshot.code_hash,
   code_address:result.snapshot.code_address,storage_address:result.snapshot.storage_address,
   assumptions:['declared_symbolic_entry_state','all_mutating_entries_must_have_complete_transitions','keccak_storage_layout_collision_free','gas_feasibility_and_creation_state_not_proved'],
   evidence_ids:unique(transitions.flatMap(t=>t.model.evidence_ids||[])),obligations:[],unresolved:[],entry_coverage:transitions.map(t=>({method_id:t.model.method_id,complete:t.model.complete,unresolved:t.model.unresolved})),...extra};
  for(const t of transitions)if(!t.model.complete)m.unresolved.push('ENTRY_TRANSITION_INCOMPLETE:'+t.model.method_id);
  if(!transitions.length)m.unresolved.push('NO_MUTATING_ENTRY_MODEL');models.push(m);return m;
 }
 function add(m,t,kind,violation) {
  const local={...m,method_id:t.model.method_id,obligations:m.obligations,evidence_ids:t.model.evidence_ids};
  const query=obligation(local,kind,t.terms.smt(),t.terms.domain(),violation,t.terms.inputs);
  // Queries are materialized after all policy reads have introduced their
  // storage-path constraints and SSA definitions into this transition.
  query.transition=t;queries.push(query);
 }
 const owner=getter(ctx,'owner',0);
 if(owner?.node&&getterRoot(owner)!==null&&/^address(?: payable)?$/.test(owner.node.typeDescriptions?.typeString||owner.declaration?.typeDescriptions?.typeString||'')) {
  const m=model('owner_identity_integrity','all_mutating_entries_preserve_owner_for_unauthorized_callers');
  m.proposition='caller != owner@entry implies owner@exit == owner@entry';
  m.owner_state_ast_id=getterRoot(owner);
  for(const t of transitions) {
   if(!t.model.complete)continue;
   try{
    const state=t.initialState(),before=getterValue(t,owner,state);
    const violations=t.model.paths.map(p=>and([p.condition,'(distinct '+t.caller.term+' '+asInt(before)+')',
     '(distinct '+asInt(getterValue(t,owner,{...state,storage:p.storage_after}))+' '+asInt(before)+')']));
    add(m,t,'unauthorized_caller_cannot_change_owner',or(violations));
   }catch(e){m.unresolved.push(String(e.message)+':'+t.model.method_id);}
  }
 }
 const hasRole=getter(ctx,'hasRole',2),roleAdmin=getter(ctx,'getRoleAdmin',1);
 if(hasRole?.fn&&hasRole.node?.nodeType==='IndexAccess'&&roleAdmin?.fn&&getterRoot(hasRole)!==null&&getterRoot(hasRole)===getterRoot(roleAdmin)&&
  hasRole.node.indexExpression?.referencedDeclaration===hasRole.fn.parameters.parameters[1].id&&
  hasRole.node.baseExpression?.expression?.indexExpression?.referencedDeclaration===hasRole.fn.parameters.parameters[0].id&&
  roleAdmin.node?.expression?.indexExpression?.referencedDeclaration===roleAdmin.fn.parameters.parameters[0].id) {
  const roleRoot=getterRoot(hasRole),member=hasRole.node.baseExpression,memberField=member?.nodeType==='MemberAccess'?member.referencedDeclaration:null;
  const adminField=roleAdmin.node?.nodeType==='MemberAccess'?roleAdmin.node.referencedDeclaration:null;
  if(memberField&&adminField) {
   const m=model('access_control_admin_integrity','all_mutating_entries_under_declared_role_admin_policy');
   m.proposition='membership changes require the current role administrator; self renunciation may only clear membership';
   m.role_state_ast_id=roleRoot;
   for(const t of transitions){if(!t.model.complete)continue;
    try{
     const violations=[];
     if(t.model.paths.some(p=>p.writes.some(w=>w.root_ast_id===null||w.root_ast_id===undefined)))throw new Error('UNCLASSIFIED_RAW_STORAGE_WRITE');
     for(const p of t.model.paths)for(const write of p.writes.filter(w=>w.root_ast_id===roleRoot)) {
      const role={term:write.keys[0],sort:'Int',bits:256,min:0n,max:MAX_WORD,mod:MAX_WORD+1n,type:'bytes32'},state=t.initialState();
      if(!role.term)throw new Error('ROLE_WRITE_KEY_UNAVAILABLE');
      let allowed;
      if(write.fields.includes(memberField)) {
       const admins=t.call(roleAdmin.fn,[role],state);if(admins.length!==1||admins[0].values.length!==1)throw new Error('ROLE_ADMIN_GETTER_NOT_SINGLE_VALUE');
       const checks=t.call(hasRole.fn,[admins[0].values[0],t.caller],state);if(checks.length!==1||checks[0].values.length!==1)throw new Error('ROLE_MEMBERSHIP_GETTER_NOT_SINGLE_VALUE');
       if(!write.keys[1])throw new Error('ROLE_MEMBER_KEY_UNAVAILABLE');
       allowed=or([asBool(checks[0].values[0]),and(['(= '+write.new_value+' 0)','(= '+write.keys[1]+' '+t.caller.term+')'])]);
      }else if(write.fields.includes(adminField)){
       const checks=t.call(hasRole.fn,[{...role,term:'0'},t.caller],state);if(checks.length!==1||checks[0].values.length!==1)throw new Error('DEFAULT_ADMIN_GETTER_NOT_SINGLE_VALUE');allowed=asBool(checks[0].values[0]);
      }else throw new Error('ROLE_STORAGE_FIELD_UNCLASSIFIED');
      violations.push(and([p.condition,'(not '+allowed+')','(distinct '+write.old_value+' '+write.new_value+')']));
     }
     add(m,t,'role_mutations_respect_admin_or_self_renunciation',or(violations));
    }catch(e){m.unresolved.push(String(e.message)+':'+t.model.method_id);}
   }
  }
 }
 const balance=getter(ctx,'balanceOf',1),supply=getter(ctx,'totalSupply',0);
 const balanceGetterValid=balance?.declaration||balance?.fn&&balance.node?.nodeType==='IndexAccess'&&balance.node.indexExpression?.referencedDeclaration===balance.fn.parameters.parameters[0].id;
 if(balanceGetterValid&&
  (supply?.node?.nodeType==='Identifier'||supply?.namespace_path?.static_slot!==undefined)&&getterRoot(balance)!==null&&getterRoot(supply)!==null&&/^uint/.test(supply.node.typeDescriptions?.typeString||supply.declaration?.typeDescriptions?.typeString||'')) {
  const balanceRoot=getterRoot(balance),supplyRoot=getterRoot(supply),layoutItem=ctx.layout.storage.find(r=>r.astId===balanceRoot),balanceType=ctx.layout.types?.[layoutItem?.type];
  if(balanceType?.encoding==='mapping'&&/^address/.test(ctx.layout.types[balanceType.key]?.label||'')&&/^uint/.test(ctx.layout.types[balanceType.value]?.label||'')){
   const m=model('erc20_ledger_conservation','inductive_preservation_of_declared_supply_equals_account_sum');
   m.proposition='sum(account balance changes) == totalSupply@exit - totalSupply@entry';
   m.balance_state_ast_id=balanceRoot;m.supply_state_ast_id=supplyRoot;m.initial_invariant_basis='declared_precondition';
   const creation=ctx.creationTransition||buildCreationTransition(result,ctx),initial={model_id:id(m.model_id,'source-initialization'),kind:'erc20_source_initialization',
    scope:'source_constructor_establishes_supply_equals_account_sum_from_zero_storage',proposition:'totalSupply@creation_exit == sum(account balances@creation_exit)',code_hash:m.code_hash,code_address:m.code_address,storage_address:m.storage_address,
    assumptions:['creation_storage_starts_zero','source_constructor_and_state_initializers','keccak_storage_layout_collision_free','historical_deployment_identity_not_verified'],
    evidence_ids:creation.model?.evidence_ids||[],obligations:[],unresolved:[],entry_coverage:[],source_transition_model_id:creation.model?.model_id,deployment_identity_verified:false};
   models.push(initial);m.source_initialization_model_id=initial.model_id;
   if(!creation.model?.complete)initial.unresolved.push(creation.reason||creation.model.unresolved.join(','));
   else try{
    const state=creation.initialState(),loc=getterLocation(creation,supply,state),violations=[];
    for(const p of creation.model.paths){
     if(p.writes.some(w=>w.root_ast_id===null||w.root_ast_id===undefined))throw new Error('UNCLASSIFIED_CREATION_STORAGE_WRITE');
     const deltas=p.writes.filter(w=>w.root_ast_id===balanceRoot).map(w=>'(- '+w.new_value+' '+w.old_value+')');
     const total=deltas.length?'(+ '+deltas.join(' ')+')':'0';
     violations.push(and([p.condition,'(distinct '+total+' '+asInt(creation.readLocation(loc,{...state,storage:p.storage_after}))+')']));
    }
    add(initial,creation,'constructor_establishes_ledger_invariant',or(violations));
   }catch(error){initial.unresolved.push(String(error.message));}
   for(const t of transitions){if(!t.model.complete)continue;
    try{
     const state=t.initialState(),loc=getterLocation(t,supply,state),before=t.readLocation(loc,state),violations=[];
     for(const p of t.model.paths){
      if(p.writes.some(w=>w.root_ast_id===null||w.root_ast_id===undefined))throw new Error('UNCLASSIFIED_RAW_STORAGE_WRITE');
      const deltas=p.writes.filter(w=>w.root_ast_id===balanceRoot).map(w=>'(- '+w.new_value+' '+w.old_value+')');
      const delta=deltas.length?'(+ '+deltas.join(' ')+')':'0',after=t.readLocation(loc,{...state,storage:p.storage_after});
      violations.push(and([p.condition,'(distinct '+delta+' (- '+asInt(after)+' '+asInt(before)+'))']));
     }
     add(m,t,'all_balance_mutations_match_supply_change',or(violations));
    }catch(e){m.unresolved.push(String(e.message)+':'+t.model.method_id);}
   }
  }
 }
 const signatures=signaturePolicies(result,ctx,transitions,getter);models.push(...signatures.models);queries.push(...signatures.queries);
 for(const item of queries){item.query.declarations='(set-option :timeout 300)\n(set-option :rlimit 1000000)\n'+item.transition.terms.smt();item.query.domain=item.transition.terms.domain();item.query.variables=item.transition.terms.inputs.map(i=>i.name);item.query.inputs=item.transition.terms.inputs;}
 const solved=solveObligations(result,queries);closeModels(models,solved);
 result.sections.authorization_models||=[];result.sections.protocol_invariants||=[];
 for(const m of models){
  if(m.source_initialization_model_id&&models.find(i=>i.model_id===m.source_initialization_model_id)?.complete)m.initial_invariant_basis='proved_source_creation_deployment_identity_pending';
  (m.kind.startsWith('erc20_')?result.sections.protocol_invariants:result.sections.authorization_models).push(m);
  result.sections.coverage.push({dimension:'declared_model_proof',model_id:m.model_id,kind:m.kind,scope:m.scope,complete:m.complete,status:m.status});
 }
 result.sections.verification_closure||=[];
 result.sections.verification_closure.push({closure_id:id(result.snapshot.code_hash,result.snapshot.storage_address,'verification-closure'),code_hash:result.snapshot.code_hash,
  state_transition_entries:transitions.length,complete_transition_entries:transitions.filter(t=>t.model.complete).length,
  models:models.map(m=>({model_id:m.model_id,kind:m.kind,complete:m.complete,scope:m.scope,unresolved:m.unresolved,proof_counts:m.proof_counts})),
  all_declared_models_complete:Boolean(models.length)&&models.every(m=>m.complete),whole_authorization_proved:false,whole_protocol_economics_proved:false,
  remaining_requirements:['creation_or_initial_state_proof','external_dependency_and_reentrancy_models','signature_domain_nonce_and_threshold_models','protocol_specific_invariants_beyond_ledger_preservation']});
 return models;
}
