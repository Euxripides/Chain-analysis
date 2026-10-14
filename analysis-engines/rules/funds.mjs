import { id, unique } from '../model.mjs';

// Each rule derives an explanation from executable expressions and effects.
// Names/comments are deliberately absent from the rule inputs.
export function applyFundsRules(result) {
  result.sections.semantics = [];
  for (const method of result.sections.methods) {
    const match = section => result.sections[section].filter(r => r.method_id === method.method_id);
    const funds = match('funds'), guards = match('permissions'), stores = match('storage'), expressions = match('expressions'), calls = match('calls'), steps = match('steps');
    const byID=new Map(expressions.map(e=>[e.expression_id,e]));
    const declarations=key=>{const refs=new Set(),visited=new Set();const scan=id=>{if(!id||visited.has(id))return;visited.add(id);const e=byID.get(id);if(!e)return;if(e.referenced_declaration!=null)refs.add(e.referenced_declaration);for(const child of e.operands||[])scan(child)};scan(key);return refs};
    for(const parameter of method.parameters) {
      parameter.constraints=guards.filter(g=>declarations(g.expression_id).has(parameter.source_ast_id)).map(g=>({guard_id:g.guard_id,condition:g.condition,path:g.path,conditions:g.conditions,evidence_ids:g.evidence_ids}));
    }
    for(const fund of funds){
      const references=new Set(),seen=new Set();const walk=key=>{if(!key||seen.has(key))return;seen.add(key);const e=byID.get(key);if(!e)return;if(e.referenced_declaration!=null)references.add(e.referenced_declaration);for(const operand of e.operands||[])walk(operand)};walk(fund.amount_expression_id);
      let direct=byID.get(fund.amount_expression_id);const visited=new Set();while(direct?.binding_expression_id&&!visited.has(direct.expression_id)){visited.add(direct.expression_id);direct=byID.get(direct.binding_expression_id)}
      for(const parameter of method.parameters)if(references.has(parameter.source_ast_id)){
        parameter.amount_expression_ids=unique([...(parameter.amount_expression_ids||[]),fund.amount_expression_id]);
        parameter.unit_status='participates_in_amount_expression';
        if(direct?.op==='Identifier'&&direct.referenced_declaration===parameter.source_ast_id){parameter.unit=fund.unit;parameter.unit_status='direct_amount_argument';parameter.unit_evidence_ids=unique([...(parameter.unit_evidence_ids||[]),...fund.evidence_ids])}
      }
    }
    const add = (rule_id, behavior, text, rows) => {
      const evidence_ids = unique(rows.flatMap(r => r.evidence_ids || []));
      result.sections.semantics.push({ semantic_id: id(method.method_id, rule_id), method_id: method.method_id,
        rule_id, version: 'funds-domain-1', behavior, text, evidence_ids,
        preconditions: guards.map(g => ({ guard_id: g.guard_id, condition: g.condition, conditions: g.conditions })),
        effects: rows.map(r => r.fund_id || r.storage_id || r.expression_id || r.call_id),
        kind: method.completeness.basis === 'source' ? 'source_behavior' : 'bytecode_behavior',
      });
    };
    const deposits = funds.filter(f => f.kind === 'token_transfer_from_intent' && f.to_expression === 'address(this)');
    const nativeDeposits=funds.filter(f=>f.kind==='native_received_intent');
    const valueWrites=stores.filter(s=>s.access==='write'&&s.value_expression?.includes('msg.value'));
    if(nativeDeposits.length&&valueWrites.length)add('native-inbound-ledger','原生币入金与状态登记','成功执行时接收 msg.value，并按该值更新 '+unique(valueWrites.map(s=>s.key_expression)).join('、'),[...nativeDeposits,...valueWrites]);
    if (deposits.length && stores.some(s => s.access === 'write')) add('inbound-asset-ledger', '入金与账内登记',
      deposits.map(f => `通过 ${f.asset} 的代扣接口，把 ${f.from_expression} 的 ${f.amount_expression} 转入 address(this)，并修改账内状态`).join('；'), [...deposits, ...stores.filter(s => s.access === 'write')]);
    const exits = funds.filter(f => ['native_payment', 'token_transfer_intent'].includes(f.kind) && f.to_expression === 'msg.sender');
    if (exits.length && stores.some(s => s.access === 'read')) add('outbound-caller-ledger', '向调用者结算',
      exits.map(f => `读取账内状态后向调用者发送 ${f.amount_expression}，资产为 ${f.asset}`).join('；'), [...exits, ...stores]);
    const ratios = expressions.filter(e => e.op === '/' || e.op === 'Div');
    if (ratios.length) add('integer-ratio', '比例与取整计算', ratios.map(e => `${e.text}；除法处理为 ${e.rounding || '见原始 EVM 表达式'}`).join('；'), ratios);
    if (funds.filter(f => f.kind !== 'token_approval').length > 1) add('multiple-recipients', '多笔资金分配', funds.map(f => `${f.to_expression} 接收 ${f.amount_expression}`).join('；'), funds);
    const addressMappings = stores.filter(s => s.access === 'read' && /mapping\s*\([^)]*=>\s*address\s*\)/.test(s.variable_type || ''));
    if (addressMappings.length && funds.length) add('address-mapping-recipient', '关联地址参与结算', `读取 ${unique(addressMappings.map(s => s.key_expression)).join('、')}，收款对象和金额见资金表达式及局部变量计算`, [...addressMappings, ...funds]);
    const timeGuards = guards.filter(g => /block\.timestamp|block\.number/.test(g.condition || ''));
    if (timeGuards.length) add('time-gated-effects', '时间或区块条件', timeGuards.map(g => g.condition).join('；'), timeGuards);
    const callerGuards = guards.filter(g => /msg\.sender|tx\.origin|ecrecover/.test(g.condition || ''));
    const gatedWrites=stores.filter(s=>s.access==='write'&&callerGuards.some(g=>s.conditions?.includes(g.condition)));
    if (callerGuards.length && gatedWrites.length) add('caller-gated-configuration', '受调用者条件限制的状态修改', callerGuards.map(g => g.condition).join('；'), [...callerGuards, ...gatedWrites]);
    const constraints = guards.filter(g => stores.some(s => s.access === 'read' && g.condition?.includes(s.key_expression)));
    if (constraints.length) add('state-gated-execution', '状态与额度条件', constraints.map(g => g.condition).join('；'), constraints);
    const delegates = calls.filter(c => ['DELEGATECALL', 'CREATE', 'CREATE2'].includes(c.call_type));
    if (delegates.length) add('delegate-or-create', '委托或创建流程', delegates.map(c => `${c.call_type} ${c.target_expression}`).join('；'), delegates);

    const descriptions = [];
    const primaryGuards = unique(guards.filter(g => !/transfer\(/.test(g.condition || '')).map(g => g.condition)).slice(0, 3);
    if (primaryGuards.length) descriptions.push(`执行条件包括 ${primaryGuards.join('、')}`);
    const calculations = steps.filter(s => s.kind === 'local' && /[*/+-]/.test(s.text || '')).slice(0, 3);
    descriptions.push(...calculations.map(s => s.text.replace(/^计算局部变量：/, '计算 ')));
    descriptions.push(...funds.filter(f => f.kind !== 'token_approval').slice(0, 4).map(f => `${f.asset === 'native' ? '原生币' : f.asset} 从 ${f.from_expression} 向 ${f.to_expression} 发送 ${f.amount_expression}`));
    const writes = steps.filter(s => s.kind === 'assignment').slice(0, 3);
    descriptions.push(...writes.map(s => s.text));
    if (!funds.length && !writes.length) {
      const returns = steps.filter(s => s.kind === 'return').slice(0, 2); descriptions.push(...returns.map(s => s.text));
    }
    if (descriptions.length) method.purpose = descriptions.join('；');
    method.business_rules = result.sections.semantics.filter(s => s.method_id === method.method_id).map(s => s.semantic_id);
  }
}
