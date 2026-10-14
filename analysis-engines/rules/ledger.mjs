import { id, unique } from '../model.mjs';
export function applyLedgerRules(result) {
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
  if (!balanceIDs.size) return;
  for (const method of result.sections.methods) {
    const writes = result.sections.storage.filter(s => s.method_id === method.method_id && s.access === 'write' && balanceIDs.has(s.state_id));
    const events = result.sections.steps.filter(s => s.method_id === method.method_id && s.kind === 'event' && s.event_signature === 'Transfer(address,address,uint256)' && JSON.stringify(s.event_indexed) === '[true,true,false]');
    for (const event of events) {
      const [from,to,amount] = event.event_arguments;
      const compatible = writes.filter(w => w.value_expression === amount);
      if (!compatible.length) continue;
      let kind = 'token_ledger_effect';
      if (/^address\(0\)$|^0x0{40}$/.test(from) && compatible.some(w => w.operation === '+=' && w.key_expression.includes(`[${to}]`))) kind = 'mint';
      if (/^address\(0\)$|^0x0{40}$/.test(to) && compatible.some(w => w.operation === '-=' && w.key_expression.includes(`[${from}]`))) kind = 'burn';
      const fund_id = id(method.method_id,event.step_id,'ledger');
      const fund={fund_id,method_id:method.method_id,kind,asset:result.snapshot.storage_address,
        from_expression:from,to_expression:to,amount_expression:amount,amount_expression_id:event.event_argument_ids?.[2],
        unit:'token_raw',settlement:'static_ledger_and_event',conditions:event.conditions,
        ledger_effects:compatible.map(w=>({storage_id:w.storage_id,key_expression:w.key_expression,operation:w.operation,value_expression:w.value_expression,conditions:w.conditions})),
        evidence_ids:unique([...event.evidence_ids,...compatible.flatMap(w=>w.evidence_ids)])};
      result.sections.funds.push(fund);method.fund_ids.push(fund_id);
    }
  }
}
