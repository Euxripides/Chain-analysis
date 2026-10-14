import {id,unique} from '../model.mjs';

export function applyAuthorityRules(result) {
  const s=result.sections,expressions=new Map(s.expressions.map(e=>[e.expression_id,e]));
  const definitions=new Map((s.definitions||[]).map(d=>[d.definition_id,d])),derivations=new Map(s.dataflow.map(d=>[d.dataflow_id,d]));
  const dependencies=(roots,derivation)=>{
    const ids=new Set(),declarations=new Set(),pending=roots.filter(Boolean);
    const selected=(derivation?.definition_ids||[]).map(k=>definitions.get(k)).filter(Boolean),partial=derivation?.status==='partial';
    while(pending.length&&ids.size<4096){const key=pending.pop();if(ids.has(key))continue;ids.add(key);const e=expressions.get(key);if(!e)continue;
      if(e.referenced_declaration!=null){declarations.add(e.referenced_declaration);pending.push(...selected.filter(d=>d.declaration_id===e.referenced_declaration).map(d=>d.value_expression_id));
        if(partial)pending.push(...[...definitions.values()].filter(d=>d.method_id===e.method_id&&d.declaration_id===e.referenced_declaration&&d.order<(e.step_order??Infinity)).map(d=>d.value_expression_id));
      }
      pending.push(...(e.operands||[]));if(e.binding_expression_id)pending.push(e.binding_expression_id);
      for(const p of e.paths||[])pending.push(...p.conditions.map(c=>c.expression_id));
    }
    return {ids,declarations};
  };
  const related=(read,dep)=>dep.ids.has(read.key_expression_id)||read.state_ast_id!=null&&dep.declarations.has(read.state_ast_id);
  const unwrap=(key,seen=new Set())=>{if(seen.has(key))return null;seen.add(key);const e=expressions.get(key);if(!e)return null;
    if(e.binding_expression_id)return unwrap(e.binding_expression_id,seen);
    if(e.symbolic_format==='return_cases'&&e.paths?.length===1&&!e.paths[0].conditions.length)return unwrap(e.paths[0].expression_id,seen);
    return e;
  };
  for(const guard of s.permissions) {
    const reads=s.storage.filter(r=>r.method_id===guard.method_id&&r.access==='read'),derivation=derivations.get(guard.condition_derivation_id),dep=dependencies([guard.expression_id],derivation);
    const states=reads.filter(r=>related(r,dep));guard.state_read_ids=states.map(r=>r.storage_id);guard.state_ids=unique(states.map(r=>r.state_id));
    guard.caller_constraints=[];
    function inspect(key,scope='whole_guard',seen=new Set()) {
      if(seen.has(key))return;const e=unwrap(key);if(!e)return;const next=new Set([...seen,key]);
      if(e.op==='=='&&e.operands?.length===2) {
        for(const [callerKey,targetKey] of [[e.operands[0],e.operands[1]],[e.operands[1],e.operands[0]]]) {
          const caller=unwrap(callerKey),principal=caller?.text?.replace(/\s/g,'');
          if(!['msg.sender','tx.origin'].includes(principal))continue;
          const target=unwrap(targetKey),targetDep=dependencies([targetKey],derivation),stateReads=reads.filter(r=>related(r,targetDep));
          guard.caller_constraints.push({constraint_id:id(guard.guard_id,key,principal),principal,relation:'equals',condition_scope:scope,
            target_expression_id:targetKey,target_expression:target?.text||expressions.get(targetKey)?.text,
            state_read_ids:stateReads.map(r=>r.storage_id),state_ids:unique(stateReads.map(r=>r.state_id)),
            target_value_resolution:target?.op==='Identifier'&&stateReads.some(r=>r.state_ast_id===target.referenced_declaration&&r.slot_role==='direct_slot'&&/^(address|contract )/.test(r.type))?'direct_state_address':'expression_requires_evaluation',
            target_resolution_basis:target?.value_basis||'represented_source_expression',
            resolved_literal:target?.resolved_value||target?.op==='Literal'&&/^0x[0-9a-fA-F]{40}$/.test(target.value||'')&&target.value||null,
            evidence_ids:unique([...(e.evidence_ids||[]),...stateReads.flatMap(r=>r.evidence_ids||[])]),authorization_sufficiency_verified:false});
        }
      } else if(e.op==='&&')for(const k of e.operands||[])inspect(k,scope==='whole_guard'?'conjunct':scope,next);
      else if(e.op==='||')for(const k of e.operands||[])inspect(k,'alternative_in_compound_condition',next);
    }
    inspect(guard.expression_id);
  }
  for(const effect of s.control_effects) {
    const writer=s.storage.find(r=>r.storage_id===effect.write_storage_id),stateID=effect.state_id;
    effect.dependency_scope='shared_state_declaration';effect.affected_operations=[];
    for(const mid of effect.affected_method_ids) {
      const reads=s.storage.filter(r=>r.method_id===mid&&r.access==='read'&&r.state_id===stateID),funds=s.funds.filter(f=>f.method_id===mid),guards=s.permissions.filter(g=>g.method_id===mid);
      for(const f of funds)for(const [field,meaning] of [['amount','amount_formula'],['to','recipient'],['from','sender'],['asset','asset_contract']]) {
        const dep=dependencies([f[field+'_expression_id']],derivations.get(f[field+'_derivation_id']));const matched=reads.filter(r=>related(r,dep));if(!matched.length)continue;
        effect.affected_operations.push({method_id:mid,fund_id:f.fund_id,impact:meaning,read_storage_ids:matched.map(r=>r.storage_id),expression:f['expanded_'+field+'_expression']||f[field+'_expression']||f[field],
          dependency_status:derivations.get(f[field+'_derivation_id'])?.status==='partial'?'possible_dependency_in_unresolved_definitions':'represented_expression_dependency'});
      }
      for(const guard of guards)if(guard.state_ids?.includes(stateID))effect.affected_operations.push({method_id:mid,guard_id:guard.guard_id,impact:'execution_condition',read_storage_ids:guard.state_read_ids,expression:guard.expanded_condition||guard.condition});
    }
    effect.impact_types=unique(effect.affected_operations.map(o=>o.impact));
    effect.caller_constraints=unique(effect.guard_ids.flatMap(key=>s.permissions.find(g=>g.guard_id===key)?.caller_constraints||[]));
    effect.interpretation=effect.impact_types.length?'此方法修改'+effect.impact_types.map(k=>({amount_formula:'资金计算参数',recipient:'收款对象',sender:'扣款对象',asset_contract:'资产合约',execution_condition:'执行条件'})[k]).join('、'):'此方法修改的状态被资金方法读取';
    if(writer)effect.write_storage_id=writer.storage_id;
  }
  // Address-control setters affect the guarded configuration methods and then
  // their funds operations. Keep the intermediate guard and effect identities.
  const directEffects=[...s.control_effects];
  for(const write of s.storage.filter(r=>r.access==='write'&&r.state_id&&s.methods.find(m=>m.method_id===r.method_id)?.entry_kind!=='constructor')) {
    const controlled=s.permissions.filter(g=>g.method_id!==write.method_id&&g.caller_constraints.some(c=>c.state_ids.includes(write.state_id)));
    const downstream=directEffects.filter(e=>controlled.some(g=>g.method_id===e.method_id));
    if(!downstream.length)continue;
    const key=id(write.storage_id,'funds-control'),existing=s.control_effects.find(e=>e.control_effect_id===key);
    const guards=s.permissions.filter(g=>g.method_id===write.method_id&&(write.conditions||[]).includes(g.condition));
    const effect=existing||{control_effect_id:key,write_storage_id:write.storage_id,method_id:write.method_id,state_id:write.state_id,
      configuration_key:write.key_expression,new_value_expression:write.value_expression,operation:write.operation,conditions:write.conditions||[],guard_ids:guards.map(g=>g.guard_id),
      affected_method_ids:[],affected_operations:[],impact_types:[],caller_constraints:guards.flatMap(g=>g.caller_constraints),current_controller_status:'requires_pinned_state',
      caller_condition_basis:guards.length?'represented_caller_condition':'caller_authorization_not_established',dependency_scope:'shared_state_declaration',
      basis:s.methods.find(m=>m.method_id===write.method_id)?.completeness.basis,evidence_ids:unique([...write.evidence_ids,...controlled.flatMap(g=>g.evidence_ids)])};
    effect.controlled_method_ids=unique(controlled.map(g=>g.method_id));effect.via_control_effect_ids=downstream.map(e=>e.control_effect_id);
    effect.affected_method_ids=unique([...effect.affected_method_ids,...downstream.flatMap(e=>e.affected_method_ids)]);
    effect.impact_types=unique([...effect.impact_types,'caller_authorization']);effect.interpretation='此方法修改资金配置方法的调用地址条件';
    effect.affected_operations.push(...controlled.map(g=>({method_id:g.method_id,guard_id:g.guard_id,impact:'caller_authorization',expression:g.expanded_condition||g.condition,read_storage_ids:g.state_read_ids})));
    if(!existing)s.control_effects.push(effect);
  }
  for(const detail of s.method_details) {
    detail.control_effect_ids=s.control_effects.filter(e=>e.method_id===detail.method_id).map(e=>e.control_effect_id);
    detail.configuration_dependency_ids=s.control_effects.filter(e=>e.affected_method_ids.includes(detail.method_id)).map(e=>e.control_effect_id);
    detail.caller_constraint_ids=s.permissions.filter(g=>g.method_id===detail.method_id).flatMap(g=>g.caller_constraints.map(c=>c.constraint_id));
  }
}
