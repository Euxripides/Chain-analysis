import { id, unique } from '../model.mjs';
import { applyAuthorityRules } from './authority.mjs';

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
      returned_values:sections.dataflow.filter(d=>d.method_id===m.method_id&&(m.entry_return_derivation_id?d.kind==='entry_return_derivation':d.kind==='return_derivation')).map(d=>({dataflow_id:d.dataflow_id,expression:d.symbolic_expression,status:d.status,unresolved:d.unresolved,successful_return_paths:d.successful_return_paths||[],loops:d.loops||[]})),
      return_model_status:m.entry_return_model_status||'represented_return_statements',return_model_unresolved:m.entry_return_model_unresolved||[],
      calculation_rules:rows('expressions').filter(e=>e.symbolic_format==='binary'||['/','%','*','+','-'].includes(e.op)||e.arithmetic==='explicit_cast_truncation').map(e=>({expression_id:e.expression_id,expression:e.text,operator:e.operator||e.op,width:e.width,signed:e.signed,arithmetic:e.arithmetic,rounding:e.rounding||(e.operator==='/'?'toward_zero':null),evidence_ids:e.evidence_ids})),
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
    sections.control_effects.push({control_effect_id:id(write.storage_id,'funds-control'),write_storage_id:write.storage_id,method_id:write.method_id,state_id:write.state_id,
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
    const extra=profile==='erc4626'?['previewDeposit(uint256)','previewMint(uint256)','previewWithdraw(uint256)','previewRedeem(uint256)','mint(uint256,address)','withdraw(uint256,address,address)','maxDeposit(address)','maxMint(address)','maxWithdraw(address)','maxRedeem(address)']:[];
    const represented=[...matched,...extra.map(signature=>dispatch.find(m=>m.signature===signature)).filter(Boolean)];
    const method_models=represented.map(m=>{const detail=sections.method_details.find(d=>d.method_id===m.method_id);return {method_id:m.method_id,signature:m.signature,
      return_formulas:detail.returned_values,state_inputs:detail.state_reads,asset_operations:detail.asset_effects,execution_conditions:detail.conditions,arithmetic_rules:detail.calculation_rules,
      formula_status:m.entry_return_model_status||'represented_asset_operations',unresolved:m.entry_return_model_unresolved||m.completeness.unresolved,evidence_ids:detail.evidence_ids};});
    const protocol_id=id(result.snapshot.code_hash,result.snapshot.code_address,result.snapshot.storage_address,profile);
    if(profile==='erc4626')for(const [preview,actual,relation,property]of [['previewDeposit(uint256)','deposit(uint256,address)','preview <= actual','erc4626_deposit_preview_bound'],['previewMint(uint256)','mint(uint256,address)','preview >= actual','erc4626_mint_preview_bound'],['previewWithdraw(uint256)','withdraw(uint256,address,address)','preview >= actual','erc4626_withdraw_preview_bound'],['previewRedeem(uint256)','redeem(uint256,address,address)','preview <= actual','erc4626_redeem_preview_bound']]) {
      const p=represented.find(m=>m.signature===preview),a=represented.find(m=>m.signature===actual);if(!p||!a)continue;
      sections.property_obligations.push({property_id:id(protocol_id,property),protocol_id,property,preview_method_id:p.method_id,actual_method_id:a.method_id,relation,
        assumptions:['same_execution_prestate','same_input_amount','successful_actual_operation','underlying_asset_and_share_units_verified'],
        status:'requires_execution_with_declared_state',requirement_source:'https://eips.ethereum.org/EIPS/eip-4626',all_paths_proved:false,evidence_ids:unique([...p.evidence_ids,...a.evidence_ids])});
    }
    sections.protocols.push({protocol_id,profile,status:'verified_source_interface_with_represented_code',
      interface_methods:method_ids,asset_effect_ids:effects.map(f=>f.fund_id),
      economic_model_verified:false,deployed_protocol_identity_verified:false,method_models,
      economic_model_status:method_models.some(m=>m.return_formulas.length)?'code_derived_formulas_with_execution_obligations':'represented_operations_with_unresolved_formulas',
      evidence_ids:unique(matched.flatMap(m=>m.evidence_ids)),scope:'interface_and_analyzed_implementation_operations'});
  }
  applyAuthorityRules(result);
}
