import { id, unique } from '../model.mjs';

// Business descriptions are assembled from represented operations. Each effect
// keeps its own path conditions; branch guards never become universal claims.
export function applyBusinessRules(result) {
  for(const key of ['method_details','operating_flows','control_effects','protocols','property_obligations'])result.sections[key]||=[];
  const sections=result.sections,methods=new Map(sections.methods.map(m=>[m.method_id,m]));
  for(const m of methods.values()) {
    const rows=name=>(sections[name]||[]).filter(r=>r.method_id===m.method_id);
    const steps=rows('steps').sort((a,b)=>a.order-b.order),funds=rows('funds'),writes=rows('storage').filter(s=>s.access==='write'),guards=rows('permissions');
    const formulas=funds.map(f=>({fund_id:f.fund_id,asset:f.asset,unit:f.unit,
      amount:f.expanded_amount_expression||f.amount_expression,recipient:f.expanded_to_expression||f.to_expression,sender:f.expanded_from_expression||f.from_expression,
      resolved_asset:f.expanded_asset_expression||f.asset,
      derivation_status:f.amount_derivation_status||'amount_expression_not_expanded',conditions:f.conditions||[],
      settlement:f.settlement,evidence_ids:f.evidence_ids}));
    sections.method_details.push({detail_id:id(m.method_id,'business-details'),method_id:m.method_id,
      schema_version:'contract-method-details-v1',signature:m.signature,code_address:m.code_address,storage_address:m.storage_address,
      inputs:m.parameters,outputs:m.outputs,conditions:guards.map(g=>({guard_id:g.guard_id,condition:g.expanded_condition||g.condition,original_condition:g.condition,path:g.path,parent_conditions:g.conditions,evidence_ids:g.evidence_ids})),
      returned_values:sections.dataflow.filter(d=>d.method_id===m.method_id&&d.kind==='return_derivation').map(d=>({dataflow_id:d.dataflow_id,expression:d.symbolic_expression,status:d.status,unresolved:d.unresolved})),
      state_reads:rows('storage').filter(s=>s.access==='read').map(s=>({storage_id:s.storage_id,key:s.key_expression,slot:s.slot,offset:s.offset,type:s.type,evidence_ids:s.evidence_ids})),
      external_calls:rows('calls').map(c=>({call_id:c.call_id,type:c.call_type,target:c.target_expression,signature:c.signature,arguments:c.input_arguments,failure:c.failure_propagation,conditions:c.conditions,evidence_ids:c.evidence_ids})),
      sequence:steps.map(s=>({step_id:s.step_id,order:s.order,kind:s.kind,operation:s.text,path:s.path,conditions:s.conditions,evidence_ids:s.evidence_ids})),
      asset_effects:formulas,state_writes:writes.map(s=>({storage_id:s.storage_id,state_id:s.state_id,key:s.key_expression,operation:s.operation,value:s.value_expression,conditions:s.conditions,evidence_ids:s.evidence_ids})),
      source_basis:m.completeness.basis,unresolved:m.completeness.unresolved,all_execution_paths_verified:false,
      evidence_ids:unique([...steps,...funds,...writes].flatMap(s=>s.evidence_ids||[]))});
    const paths=new Map();for(const s of steps){const path=s.path||'represented';const row=paths.get(path)||[];row.push(s);paths.set(path,row)}
    for(const [path,list] of paths)sections.operating_flows.push({operating_flow_id:id(m.method_id,'operating-flow',path),method_id:m.method_id,path,
      operation_sequence:list.map(s=>s.step_id),conditions:unique(list.flatMap(s=>s.conditions||[])),scope:'represented_path_operations',
      path_feasibility_verified:false,evidence_ids:unique(list.flatMap(s=>s.evidence_ids||[]))});
    for(const f of funds)sections.property_obligations.push({property_id:id(f.fund_id,'obligation'),method_id:m.method_id,fund_id:f.fund_id,
      property:'asset_effect_matches_represented_expression',amount_expression:f.expanded_amount_expression||f.amount_expression,
      recipient_expression:f.to_expression,asset:f.asset,conditions:f.conditions||[],status:'requires_execution_with_declared_state',
      evidence_ids:f.evidence_ids,all_paths_proved:false});
  }
  for(const write of sections.storage.filter(s=>s.access==='write'&&s.state_id&&methods.get(s.method_id)?.entry_kind!=='constructor')) {
    const dependent=sections.storage.filter(s=>s.access==='read'&&s.state_id===write.state_id&&s.method_id!==write.method_id);
    const affected=unique(dependent.map(s=>s.method_id)).filter(mid=>sections.funds.some(f=>f.method_id===mid));
    if(!affected.length)continue;
    const author=methods.get(write.method_id),guards=sections.permissions.filter(g=>g.method_id===write.method_id&&(write.conditions||[]).includes(g.condition));
    sections.control_effects.push({control_effect_id:id(write.storage_id,'funds-control'),method_id:write.method_id,state_id:write.state_id,
      configuration_key:write.key_expression,new_value_expression:write.value_expression,operation:write.operation,
      conditions:write.conditions||[],guard_ids:guards.map(g=>g.guard_id),affected_method_ids:affected,
      interpretation:'此方法修改的状态被资金方法读取',caller_condition_basis:guards.some(g=>/msg\.sender|tx\.origin|ecrecover/.test(g.expanded_condition||g.condition))?'represented_caller_condition':'caller_authorization_not_established',
      current_controller_status:'requires_pinned_state',basis:author?.completeness.basis,
      evidence_ids:unique([...write.evidence_ids,...dependent.flatMap(s=>s.evidence_ids||[]),...guards.flatMap(g=>g.evidence_ids||[])])});
  }
  const dispatch=sections.methods.filter(m=>m.verified_signature&&m.dispatch_role!=='overridden_source_definition');
  const profiles=[
    ['erc20',['balanceOf(address)','totalSupply()','transfer(address,uint256)','transferFrom(address,address,uint256)','approve(address,uint256)']],
    ['erc721',['ownerOf(uint256)','balanceOf(address)','transferFrom(address,address,uint256)','approve(address,uint256)']],
    ['erc1155',['balanceOf(address,uint256)','safeTransferFrom(address,address,uint256,uint256,bytes)']],
    ['erc4626',['asset()','totalAssets()','convertToShares(uint256)','convertToAssets(uint256)','deposit(uint256,address)','redeem(uint256,address,address)']],
    ['uniswap_v2_pair_interface',['token0()','token1()','getReserves()','swap(uint256,uint256,address,bytes)']],
    ['uniswap_v3_pool_interface',['token0()','token1()','slot0()','swap(address,bool,int256,uint160,bytes)']],
  ];
  for(const [profile,required] of profiles) {
    const matched=required.map(signature=>dispatch.find(m=>m.signature===signature));if(matched.some(m=>!m))continue;
    const method_ids=matched.map(m=>m.method_id),effects=sections.funds.filter(f=>method_ids.includes(f.method_id));
    sections.protocols.push({protocol_id:id(result.snapshot.code_hash,result.snapshot.code_address,result.snapshot.storage_address,profile),profile,status:'verified_source_interface_with_represented_code',
      interface_methods:method_ids,asset_effect_ids:effects.map(f=>f.fund_id),
      economic_model_verified:false,deployed_protocol_identity_verified:false,
      evidence_ids:unique(matched.flatMap(m=>m.evidence_ids)),scope:'interface_and_analyzed_implementation_operations'});
  }
}
