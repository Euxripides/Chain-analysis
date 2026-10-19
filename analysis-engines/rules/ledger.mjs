import { id, unique } from '../model.mjs';
export function applyLedgerRules(result) {
  const expressions=new Map(result.sections.expressions.map(e=>[e.expression_id,e]));
  const scalars=new Map((result.sections.source_scalar_versions||[]).map(v=>[v.source_scalar_version_id,v])),opaqueCache=new Map(),identityCache=new Map();
  const opaque=(key,seen=new Set())=>{
    if(!key||seen.has(key)||seen.size>=512)return true;if(opaqueCache.has(key))return opaqueCache.get(key);
    const e=expressions.get(key);if(!e)return true;const next=new Set([...seen,key]);
    const value=Boolean(e.unresolved_return_value||e.external_return_dependency_id||e.source_scalar_binding_unavailable||e.unresolved_evaluation_effect||
      e.source_scalar_unresolved?.length||e.unresolved_model_reasons?.length||
      [...(e.operands||[]),e.binding_expression_id,...(e.paths||[]).map(p=>p.expression_id)].filter(Boolean).some(k=>opaque(k,next)));
    opaqueCache.set(key,value);return value;
  };
  const identities=key=>{
    if(identityCache.has(key))return identityCache.get(key);const values=new Set(),seen=new Set();let current=key;
    while(current&&!seen.has(current)&&seen.size<512){
      seen.add(current);const e=expressions.get(current);if(!e)break;values.add('expression:'+current);
      const v=scalars.get(e.source_scalar_version_id);
      // A shared budget sentinel represents several unavailable values, so it
      // cannot establish that two reads captured the same scalar.
      if(v?.declaration_id!=null&&v.frame_id&&!v.unresolved?.includes('SOURCE_SCALAR_VERSION_BUDGET'))values.add('scalar:'+v.source_scalar_version_id);
      if(e.unresolved_return_value||e.source_scalar_binding_unavailable)break;
      if(e.binding_expression_id)current=e.binding_expression_id;
      else if(e.symbolic_format==='return_cases'&&e.paths?.length===1)current=e.paths[0].expression_id;
      else break;
    }
    identityCache.set(key,values);return values;
  };
  const peel=(key,seen=new Set())=>{if(seen.has(key))return null;seen.add(key);const e=expressions.get(key);if(!e)return null;
    // Its numeric value may be unknown while its captured identity is exact.
    if(e.unresolved_return_value||e.source_scalar_binding_unavailable)return e;
    if(e.binding_expression_id)return peel(e.binding_expression_id,seen);
    if(e.symbolic_format==='return_cases'&&e.paths?.length===1)return peel(e.paths[0].expression_id,seen);
    return e;
  };
  const normalized=text=>(text||'').replace(/\s/g,'');
  const delta=write=>{
    if(['+=','-='].includes(write.operation))return {operation:write.operation[0],value:peel(write.value_expression_id),value_source_expression_id:write.value_expression_id};
    if(write.operation!=='=')return null;
    const value=peel(write.value_expression_id);if(!value||!['+','-'].includes(value.op)||value.operands?.length!==2)return null;
    const left=peel(value.operands[0]),right=peel(value.operands[1]);
    return normalized(left?.text)===normalized(write.key_expression)?{operation:value.op,value:right,value_source_expression_id:value.operands[1]}:null;
  };
  for(const method of result.sections.methods) {
    if(method.state_mutability!=='payable'||!['external','public'].includes(method.visibility)||method.entry_kind==='constructor'||method.dispatch_role==='overridden_source_definition')continue;
    const fund_id=id(method.method_id,'callvalue-context');
    result.sections.funds.push({fund_id,method_id:method.method_id,kind:'native_received_intent',asset:'native',from_expression:'msg.sender',to_expression:'address(this)',amount_expression:'msg.value',unit:'wei',settlement:'payable_entry_static_intent',payment_role:'entry_callvalue_context',conditions:['call_succeeds'],evidence_ids:method.evidence_ids});
    method.fund_ids.push(fund_id);
  }
  // Identify the balance ledger through the verified balanceOf execution body,
  // rather than guessing that a variable named balances is a token balance.
  const getters = result.sections.methods.filter(m => m.verified_signature && m.signature === 'balanceOf(address)');
  const balanceIDs = new Set(result.sections.storage.filter(s => getters.some(m => m.method_id === s.method_id) && s.access === 'read' && /mapping.*address.*uint256/.test(s.variable_type || '')).map(s => s.state_id));
  const ownerMethods=result.sections.methods.filter(m=>m.verified_signature&&m.signature==='ownerOf(uint256)'&&m.outputs?.length===1&&m.outputs[0].type==='address');
  const ownershipReads=result.sections.storage.filter(s=>s.access==='read'&&ownerMethods.some(m=>m.method_id===s.method_id)&&/mapping\s*\(uint256\s*=>\s*address\)/.test(s.variable_type||''));
  for(const write of result.sections.storage.filter(s=>s.access==='write'&&s.operation==='='&&ownershipReads.some(r=>r.state_id===s.state_id))) {
    const method=result.sections.methods.find(m=>m.method_id===write.method_id);if(!method||method.entry_kind==='constructor')continue;
    const key=write.storage_projection?.path?.findLast(p=>p.kind==='mapping');if(!key)continue;
    const fund_id=id(write.storage_id,'ownership-write');result.sections.funds.push({fund_id,method_id:method.method_id,kind:'nft_ownership_assignment',asset:write.storage_address,
      from_expression:'prestate ownership at '+write.key_expression,to_expression:write.value_expression,to_expression_id:write.value_expression_id,
      token_id_expression:key.key_text,amount_expression:'1',unit:'nft_units',settlement:'static_ownership_storage_assignment',
      ownership_storage_id:write.storage_id,conditions:write.conditions,evidence_ids:unique([...write.evidence_ids,...ownershipReads.filter(r=>r.state_id===write.state_id).flatMap(r=>r.evidence_ids)])});method.fund_ids.push(fund_id);
  }
  // An address balance counter accompanying a verified ownership ledger counts
  // NFTs. A token ID of 1 must not turn a matching counter increment into ERC20.
  if (!balanceIDs.size || ownershipReads.length) return;
  for (const method of result.sections.methods) {
    const writes = result.sections.storage.filter(s => s.method_id === method.method_id && s.access === 'write' && balanceIDs.has(s.state_id));
    const events = result.sections.steps.filter(s => s.method_id === method.method_id && s.kind === 'event' && s.event_signature === 'Transfer(address,address,uint256)' && JSON.stringify(s.event_indexed) === '[true,true,false]');
    for (const event of events) {
      const [from,to,amount] = event.event_arguments;
      const eventAmount=peel(event.event_argument_ids?.[2]);
      const compatible = writes.filter(w => {
        const d=delta(w);if(!d?.value||!eventAmount)return false;
        if(opaque(d.value_source_expression_id)||opaque(event.event_argument_ids?.[2])){
          const scope=expressions.get(w.value_expression_id)?.path,eventScope=event.path||expressions.get(event.event_argument_ids?.[2])?.path;
          if(typeof scope!=='string'||!scope||scope!==eventScope||!(w.conditions||[]).every(c=>(event.conditions||[]).includes(c)))return false;
          const a=identities(d.value_source_expression_id),b=identities(event.event_argument_ids?.[2]);return [...a].some(k=>b.has(k));
        }
        return normalized(d.value.text)===normalized(eventAmount.text);
      });
      if (!compatible.length) continue;
      let kind = 'token_ledger_effect';
      if (/^address\(0\)$|^0x0{40}$/.test(from) && compatible.some(w => delta(w).operation === '+' && normalized(w.key_expression).includes(`[${normalized(to)}]`))) kind = 'mint';
      if (/^address\(0\)$|^0x0{40}$/.test(to) && compatible.some(w => delta(w).operation === '-' && normalized(w.key_expression).includes(`[${normalized(from)}]`))) kind = 'burn';
      const fund_id = id(method.method_id,event.step_id,'ledger');
      const fund={fund_id,method_id:method.method_id,kind,asset:result.snapshot.storage_address||'address(this)',
        asset_scope:result.snapshot.storage_address?'snapshot_storage_context':'symbolic_self_storage',
        from_expression:from,to_expression:to,amount_expression:amount,amount_expression_id:event.event_argument_ids?.[2],
        from_expression_id:event.event_argument_ids?.[0],to_expression_id:event.event_argument_ids?.[1],
        unit:'token_raw',settlement:'static_ledger_and_event',conditions:event.conditions,
        ledger_effects:compatible.map(w=>({storage_id:w.storage_id,key_expression:w.key_expression,operation:w.operation,value_expression:w.value_expression,conditions:w.conditions,
          delta_operation:delta(w).operation,delta_expression:delta(w).value.text,delta_expression_id:delta(w).value.expression_id,
          value_identity_basis:opaque(delta(w).value_source_expression_id)||opaque(event.event_argument_ids?.[2])?'shared_opaque_value_in_source_frame':'matching_source_spelling'})),
        evidence_ids:unique([...event.evidence_ids,...compatible.flatMap(w=>w.evidence_ids)])};
      result.sections.funds.push(fund);method.fund_ids.push(fund_id);
    }
  }
}
