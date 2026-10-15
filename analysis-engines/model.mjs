import { createHash } from 'node:crypto';

export const VERSION = 'contract-analysis-v1';
export const RULE_VERSION = 'funds-semantics-6';
export const hash = value => createHash('sha256').update(typeof value === 'string' ? value : JSON.stringify(value)).digest('hex');
export const id = (...parts) => hash(parts).slice(0, 40);
export const stringify = value => JSON.stringify(value, (_, v) => typeof v === 'bigint' ? v.toString() : v);
export const unique = values => [...new Set(values)];
export function emptyResult(snapshot) {
  return { version: VERSION, rule_version: RULE_VERSION, snapshot, sections: Object.fromEntries([
    'contracts', 'methods', 'steps', 'expressions', 'storage', 'calls', 'permissions', 'funds', 'flows',
    'claims', 'evidence', 'coverage', 'conflicts', 'engines', 'transactions', 'versions', 'frontier', 'artifacts', 'definitions', 'dataflow',
  ].map(key => [key, []])) };
}
export function makeMethod(snapshot, entry, selector = '', name = '') {
  const method_id = id(snapshot.code_hash, snapshot.code_address, snapshot.storage_address, entry, selector);
  return { method_id, selector, name, signature: '', signature_candidates: [], verified_signature: false,
    code_address: snapshot.code_address, storage_address: snapshot.storage_address,
    code_hash: snapshot.code_hash, entry, visibility: 'bytecode', parameters: [], outputs: [],
    proxy_route_status: snapshot.proxy?.proxy_type==='eip2535_facet' ? snapshot.proxy.routes?.[selector] || 'not_verified_loupe_entry' : null,
    purpose: '', behaviors: [], evidence_ids: [], step_ids: [], guard_ids: [], storage_ids: [],
    call_ids: [], fund_ids: [], return_ids: [], failure_ids: [], dependency_methods: [],
    historical_calls: { status: 'not_requested', count: null }, observed_transactions: [],
    completeness: { complete: false, unresolved: [], basis: 'bytecode' },
  };
}
export function evidence(result, method, kind, location, text = '') {
  const evidence_id = id(method.method_id, kind, location);
  if (!result.sections.evidence.some(row => row.evidence_id === evidence_id)) {
    result.sections.evidence.push({ evidence_id, method_id: method.method_id, kind,
      code_hash: method.code_hash, code_address: method.code_address, location, text });
  }
  method.evidence_ids.push(evidence_id);
  return evidence_id;
}
export function claim(result, method, kind, text, evidence_ids, rule_id = RULE_VERSION) {
  const claim_id = id(method.method_id, kind, text, evidence_ids);
  result.sections.claims.push({ claim_id, method_id: method.method_id, kind, text, evidence_ids,
    rule_id, rule_version: RULE_VERSION, human_status: 'unreviewed', conflicts: [] });
  return claim_id;
}
export function finalize(result) {
  const { methods, steps, storage, calls, permissions, funds, flows } = result.sections;
  for (const method of methods) {
    const ms = steps.filter(s => s.method_id === method.method_id);
    method.evidence_ids = unique(method.evidence_ids);
    const mf = funds.filter(f => f.method_id === method.method_id);
    const mc = calls.filter(c => c.method_id === method.method_id);
    const mp = permissions.filter(p => p.method_id === method.method_id);
    const mw = storage.filter(s => s.method_id === method.method_id && s.access === 'write');
    const mr = storage.filter(s => s.method_id === method.method_id && s.access === 'read');
    // Classifications depend on the executable body and expressions, never the method name.
    const behaviors = [];
    if (mf.some(f => f.kind === 'native_payment')) behaviors.push('原生币支付');
    if (mf.some(f => f.kind === 'native_received_intent')) behaviors.push('可接收原生币');
    if (mf.some(f => f.kind === 'token_transfer_intent')) behaviors.push('代币转出调用');
    if (mf.some(f => f.kind === 'token_transfer_from_intent')) behaviors.push('代币代扣调用');
    if (mf.some(f => f.kind === 'token_ledger_effect')) behaviors.push('代币余额账本变更');
    if (mf.some(f => f.kind === 'mint')) behaviors.push('增发');
    if (mf.some(f => f.kind === 'burn')) behaviors.push('销毁');
    if (mw.length) behaviors.push('修改合约状态');
    if (mr.length && !mw.length && !mf.length) behaviors.push('读取合约状态');
    if (mp.some(p => p.condition?.includes('block.timestamp'))) behaviors.push('时间条件');
    if (mp.some(p => p.condition?.includes('msg.sender'))) behaviors.push('调用者条件');
    if (mc.some(c => c.call_type === 'DELEGATECALL')) behaviors.push('委托执行');
    if (mc.some(c => c.call_type === 'CREATE' || c.call_type === 'CREATE2')) behaviors.push('创建合约');
    if (mc.length && !mf.length) behaviors.push('外部合约交互');
    if (ms.some(s => s.kind === 'return')) behaviors.push('返回计算结果');
    method.behaviors = unique(behaviors);
    const descriptions = [];
    if (mp.length) descriptions.push(`执行 ${mp.length} 项条件检查`);
    if (mr.length) descriptions.push(`读取 ${unique(mr.map(s => s.variable || s.key_expression || s.slot)).length} 项状态`);
    if (mw.length) descriptions.push(`写入 ${unique(mw.map(s => s.variable || s.key_expression || s.slot)).length} 项状态`);
    if (mf.length) descriptions.push(`包含 ${mf.length} 项资金操作`);
    if (mc.length) descriptions.push(`包含 ${mc.length} 项外部调用`);
    method.purpose = descriptions.join('，') || (method.completeness.unresolved.length ? '入口已发现，执行体存在待解析部分' : '执行体和返回结果见逐步分析');
    claim(result, method, method.completeness.basis === 'source' ? 'source_behavior' : 'bytecode_behavior', method.purpose, method.evidence_ids);
  }
  const keys = new Map();
  for (const item of storage) {
    const key = item.state_id;
    if (!key) continue;
    const group = keys.get(key) || { reads: [], writes: [] };
    group[item.access === 'write' ? 'writes' : 'reads'].push(item);
    keys.set(key, group);
    flows.push({ edge_id: id(item.storage_id, 'storage'), edge_type: item.access === 'write' ? 'writes' : 'reads',
      from: item.access === 'write' ? item.method_id : key, to: item.access === 'write' ? key : item.method_id,
      evidence_ids: item.evidence_ids, expression: item.key_expression });
  }
  for (const [key, group] of keys) for (const w of group.writes) for (const r of group.reads) {
    if (w.method_id === r.method_id) continue;
    const target = methods.find(m => m.method_id === r.method_id);
    if (target) target.dependency_methods.push(w.method_id);
    flows.push({ edge_id: id(w.storage_id, r.storage_id), edge_type: 'state_dependency', from: w.method_id,
      to: r.method_id, state_id: key, evidence_ids: unique([...w.evidence_ids, ...r.evidence_ids]) });
  }
  for (const call of calls) flows.push({ edge_id: id(call.call_id, 'call'), edge_type: call.call_type === 'DELEGATECALL' ? 'delegates' : 'calls',
    from: call.method_id, to: call.target || call.target_expression, evidence_ids: call.evidence_ids, call_id: call.call_id });
  for (const fund of funds) flows.push({ edge_id: id(fund.fund_id, 'fund'), edge_type: 'transfers',
    from: fund.from_expression, to: fund.to_expression, method_id: fund.method_id, asset: fund.asset,
    amount_expression: fund.amount_expression, settlement: 'static_intent', evidence_ids: fund.evidence_ids });
  for (const method of methods) method.dependency_methods = unique(method.dependency_methods);
  result.sections.flows = [...new Map(flows.map(e => [e.edge_id, e])).values()];
  return result;
}
