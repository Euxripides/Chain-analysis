import { createRequire } from 'node:module';
import { hash, id, unique, makeMethod, evidence, claim } from './model.mjs';
import { legacyLayout } from './layout.mjs';
import { numericLiteral } from './literals.mjs';
const require = createRequire(import.meta.url);
const compilers = new Set(Object.keys(require('./package.json').dependencies).filter(k => k.startsWith('solc-')).map(k => k.slice(5)));

export function compileSource(input, compilerVersion) {
  if(input?.language!=='Solidity')throw new Error('SOURCE_LANGUAGE_UNSUPPORTED');
  const version = compilerVersion.split('+')[0];
  if (!compilers.has(version)) throw new Error(`COMPILER_UNAVAILABLE:${version}`);
  const solc = require(`solc-${version}`);
  if (!solc.version().startsWith(compilerVersion)) throw new Error('COMPILER_IDENTITY_MISMATCH');
  const settings = structuredClone(input.settings || {});
  settings.outputSelection = { '*': { '*': ['abi', 'evm.deployedBytecode', 'evm.methodIdentifiers', 'storageLayout'], '': ['ast', 'legacyAST'] } };
  const payload = JSON.stringify({ ...input, settings });
  const compile = solc.compileStandardWrapper || solc.compile;
  const output = JSON.parse(compile.call(solc, payload));
  const errors = (output.errors || []).filter(e => e.severity === 'error');
  if (errors.length) throw new Error(`COMPILATION_FAILED:${errors.map(e => e.message).join('; ').slice(0, 500)}`);
  return output;
}

export function analyzeSource(result, source, bytecode) {
  const started = performance.now();
  const output = compileSource(source.stdJsonInput, source.compilation.compilerVersion);
  const fq = source.compilation.fullyQualifiedName;
  const split = fq.lastIndexOf(':');
  const file = fq.slice(0, split), contractName = fq.slice(split + 1);
  const compiled = output.contracts?.[file]?.[contractName];
  if (!compiled) throw new Error('COMPILED_CONTRACT_NOT_FOUND');
  const compiledCode = compiled.evm?.deployedBytecode?.object;
  const providedCode = source.runtimeBytecode?.recompiledBytecode?.replace(/^0x/, '').toLowerCase();
  if (!compiledCode || compiledCode.toLowerCase() !== providedCode) throw new Error('RECOMPILATION_BYTECODE_MISMATCH');
  if (source.runtimeBytecode?.onchainBytecode?.toLowerCase() !== bytecode.toLowerCase()) throw new Error('SOURCE_SNAPSHOT_CODE_MISMATCH');
  // Transformations must be backed by Sourcify's recorded verification, never inferred locally.
  if (!['match', 'exact_match'].includes(source.runtimeMatch) || !['match', 'exact_match'].includes(source.match)) throw new Error('SOURCE_NOT_VERIFIED');
  const sources = source.stdJsonInput.sources;
  const nodes = new Map(), fileIDs = new Map();
  for (const [path, unit] of Object.entries(output.sources || {})) {
    const ast = unit.ast;
    if (!ast?.nodeType) throw new Error('SOURCE_AST_UNAVAILABLE');
    fileIDs.set(unit.id, path);
    visit(ast, node => nodes.set(node.id, node));
  }
  const contracts = [...nodes.values()].filter(n => n.nodeType === 'ContractDefinition');
  const mainContract = contracts.find(n => n.name === contractName && fileIDs.get(Number(n.src.split(':')[2])) === file);
  if (!mainContract) throw new Error('SOURCE_CONTRACT_AST_UNAVAILABLE');
  const parents = new Set(mainContract.linearizedBaseContracts || [mainContract.id]);
  const orderedContracts = (mainContract.linearizedBaseContracts || [mainContract.id]).map(id => contracts.find(n => n.id === id)).filter(Boolean);
  const functions = orderedContracts.flatMap(n => (n.nodes || []).filter(n => n.nodeType === 'FunctionDefinition'));
  const stateVars = [...nodes.values()].filter(n => n.nodeType === 'VariableDeclaration' && n.stateVariable && parents.has(n.scope));
  const sourceText = node => {
    if (!node?.src) return '';
    const [start, len, fileId] = node.src.split(':').map(Number);
    const path = fileIDs.get(fileId);
    return Buffer.from(sources[path]?.content || '').subarray(start, start + len).toString('utf8');
  };
  const location = node => {
    const [start, length, fileId] = node.src.split(':').map(Number);
    const path = fileIDs.get(fileId), text = sources[path]?.content || '';
    const line = Buffer.from(text).subarray(0, start).toString('utf8').split('\n').length;
    return { artifact_id: id('source', path, hash(text)), file: path, start, length, line, ast_id: node.id };
  };
  const getEvidence = (method, node) => evidence(result, method, 'source', location(node), sourceText(node).slice(0, 2048));
  const typeOf = node => node?.typeDescriptions?.typeString || sourceText(node?.typeName) || 'unresolved';
  const layout = compiled.storageLayout?.storage ? compiled.storageLayout : legacyLayout(orderedContracts,typeOf);
  const layoutByID = new Map((layout?.storage || []).map(v => [v.astId, v]));
  const methodByAST = new Map();
  const dispatchedSelectors = new Set();
  for (const fn of functions) {
    if (!fn.implemented && !fn.body) continue;
    const canonical = n => typeOf(n).replace(/ (memory|storage|calldata)( ref| pointer)?/g, '').replace(/^contract .+$/, 'address');
    const abiSignature = fn.name && ['external', 'public'].includes(fn.visibility) ? `${fn.name}(${(fn.parameters?.parameters || []).map(canonical).join(',')})` : '';
    const compiledSelector = compiled.evm?.methodIdentifiers?.[abiSignature];
    const selector = fn.functionSelector ? '0x' + fn.functionSelector : compiledSelector ? '0x' + compiledSelector : '';
    const overridden = Boolean(selector && dispatchedSelectors.has(selector));
    if (selector) dispatchedSelectors.add(selector);
    const method = makeMethod(result.snapshot, selector && !overridden ? `selector:${selector}` : `source:${fn.id}`, selector, fn.name || fn.kind || (fn.isConstructor ? 'constructor' : 'fallback'));
    method.dispatch_role = overridden ? 'overridden_source_definition' : selector ? 'external_dispatch' : 'source_definition';
    method.visibility = fn.visibility;
    method.entry_kind = fn.kind || (fn.isConstructor ? 'constructor' : fn.name ? 'function' : 'fallback');
    method.code_phase=method.entry_kind==='constructor'?'creation_source_not_verified_against_deployment':'runtime_source';
    method.state_mutability = fn.stateMutability || (fn.payable ? 'payable' : fn.constant ? 'view' : 'nonpayable');
    const signature = Object.entries(compiled.evm?.methodIdentifiers || {}).find(([, value]) => selector === '0x' + value)?.[0];
    method.signature = signature || `${method.name}(${(fn.parameters?.parameters || []).map(typeOf).join(',')})`;
    method.verified_signature = Boolean(selector && signature);
    method.parameters = (fn.parameters?.parameters || []).map((n, index) => ({ index, source_ast_id: n.id, name: n.name || `arg${index}`, type: typeOf(n),
      unit: null, unit_status: 'not_inferred_from_name', constraints: [], evidence_ids: [getEvidence(method, n)] }));
    method.outputs = (fn.returnParameters?.parameters || []).map((n, index) => ({ index, name: n.name, type: typeOf(n), evidence_ids: [getEvidence(method, n)] }));
    method.completeness = { complete: false, unresolved: [], basis: 'source', scope: 'syntactic_control_and_data_flow', execution_paths: 'not_exhaustively_verified' };
    method.source_location = location(fn);
    getEvidence(method, fn);
    result.sections.methods.push(method);
    methodByAST.set(fn.id, method);
  }
  let expressionNumber = 0;
  const expression = (method, node, context = {}) => {
    if (!node) return null;
    const type = typeOf(node), integer = /\b(u?int)(\d*)\b/.exec(type);
    const expression_id = id(method.method_id, node.id, node.src, context.path, context.unchecked || false);
    if (result.sections.expressions.some(e => e.expression_id === expression_id)) return expression_id;
    const operands = children(node).filter(n => !['TypeName', 'VariableDeclaration'].some(t => n.nodeType?.endsWith(t)))
      .map(n => expression(method, n, context)).filter(Boolean);
    const bound=node.nodeType==='Identifier'?context.parameter_arguments?.[node.referencedDeclaration]:null;
    const binding_expression_id=bound&&bound.id!==node.id?expression(method,bound,context):null;
    if(binding_expression_id)operands.push(binding_expression_id);
    const row = { expression_id, method_id: method.method_id, op: node.operator || node.nodeType,
      text: sourceText(node).slice(0, 4096), type, width: integer ? Number(integer[2] || '256') : null,
      signed: integer ? integer[1] === 'int' : null,
      arithmetic: integer ? node.kind==='typeConversion'?'explicit_cast_truncation':(context.unchecked || Number(source.compilation.compilerVersion.split('.')[1]) < 8 ? 'wrap' : 'checked') : 'not_applicable',
      rounding: node.operator === '/' ? 'toward_zero' : null, operands, referenced_declaration: node.referencedDeclaration ?? null,
      evidence_ids: [getEvidence(method, node)] };
    if(binding_expression_id){row.binding_expression_id=binding_expression_id;row.bound_argument=sourceText(bound)}
    if (node.nodeType === 'Literal') {
      row.raw_literal=String(node.value??node.hexValue??'');row.literal_kind=node.kind;row.subdenomination=node.subdenomination||null;
      if(node.kind==='number')Object.assign(row,numericLiteral(row.raw_literal,row.subdenomination));else row.value=row.raw_literal;
    }
    result.sections.expressions.push(row);
    if (++expressionNumber > 50000) throw new Error('EXPRESSION_BUDGET_EXCEEDED');
    return expression_id;
  };
  const stateAccess = (method, node, access, context, valueNode, operation = null) => {
    const base = rootDeclaration(node), variable = nodes.get(base);
    if (!variable?.stateVariable || variable.constant || variable.mutability === 'immutable') return;
    const evidence_id = getEvidence(method, node), slot = layoutByID.get(base);
    const storage_id = id(method.method_id, node.id, access, context.path);
    if (result.sections.storage.some(s => s.storage_id === storage_id)) return;
    const state_id = id(result.snapshot.code_hash, result.snapshot.storage_address, 'state', base);
    const accessText=node.nodeType==='Identifier'?node.name:sourceText(node);
    const baseOnly=accessText!==variable.name||/^(mapping|struct)/.test(typeOf(variable))||typeOf(variable).includes('[');
    const row = { storage_id, state_id, method_id: method.method_id, variable: variable.name,
      code_address: result.snapshot.code_address, storage_address: result.snapshot.storage_address,
      access, slot: slot?.slot ?? null, offset: slot?.offset ?? null, type: typeOf(node), variable_type: typeOf(variable),
      slot_role:baseOnly?'layout_base':'direct_slot',key_expression: accessText, key_expression_id: expression(method, node, context),
      value_expression: valueNode ? sourceText(valueNode) : null, value_expression_id: expression(method, valueNode, context), operation,
      conditions: [...context.conditions], evidence_ids: [evidence_id], layout_status: slot ? slot.layout_basis || 'verified_compiler_layout' : 'layout_unavailable' };
    result.sections.storage.push(row);
    method.storage_ids.push(storage_id);
  };
  const addStep = (method, node, kind, text, context, extra = {}) => {
    const step_id = id(method.method_id, node.id, kind, context.path, method.step_ids.length);
    const row = { step_id, method_id: method.method_id, order: method.step_ids.length, kind, text,
      expression_id: expression(method, node, context), conditions: [...context.conditions], path: context.path,
      evidence_ids: [getEvidence(method, node)], ...extra };
    result.sections.steps.push(row); method.step_ids.push(step_id);
    claim(result, method, 'source_behavior', text, row.evidence_ids, `ast-${kind}`);
    return row;
  };
  const guard = (method, node, condition, context, reason) => {
    const guard_id = id(method.method_id, node.id, context.path, 'guard');
    result.sections.permissions.push({ guard_id, method_id: method.method_id,
      condition: sourceText(condition), expression_id: expression(method, condition, context),
      conditions: [...context.conditions], path: context.path, failure: reason, evidence_ids: [getEvidence(method, node)] });
    method.guard_ids.push(guard_id);
    addStep(method, node, 'guard', `检查 ${sourceText(condition)}；不满足时 ${reason}`, context, { guard_id });
    context.conditions.push(sourceText(condition));
  };
  const analyzeCall = (method, node, context) => {
    let callee = node.expression, options = {};
    if (callee?.nodeType === 'FunctionCallOptions') { options = Object.fromEntries((callee.names || []).map((n, i) => [n, callee.options[i]])); callee = callee.expression; }
    const name = callee?.name || callee?.memberName;
    const baseType = typeOf(callee?.expression);
    if (['require', 'assert'].includes(name)) { guard(method, node, node.arguments?.[0], context, name === 'assert' ? '触发 panic' : 'revert'); return; }
    if (name === 'revert') { const step = addStep(method, node, 'revert', `回滚：${sourceText(node)}`, context); method.failure_ids.push(step.step_id); return; }
    const targetNode = nodes.get(callee?.referencedDeclaration);
    if (targetNode?.nodeType === 'EventDefinition') { eventStep(method,node,node,context,targetNode); return; }
    if (targetNode?.nodeType === 'FunctionDefinition' && (callee?.nodeType === 'Identifier' || callee?.expression?.name === 'super')) {
      const called = methodByAST.get(targetNode.id);
      if (called) {
        const callStep = addStep(method, node, 'internal_call', `调用内部方法 ${called.signature}`, context, { target_method_id: called.method_id });
        result.sections.flows.push({ edge_id: id(callStep.step_id, 'internal'), edge_type: 'calls', from: method.method_id, to: called.method_id, evidence_ids: callStep.evidence_ids });
        if (context.stack.length < 12 && !context.stack.includes(targetNode.id)) {
          analyzeBlock(method, targetNode.body, { ...context, path: `${context.path}/call:${node.id}`, stack: [...context.stack, targetNode.id], parameter_bindings: Object.fromEntries((targetNode.parameters?.parameters || []).map((p, i) => [p.id, sourceText(node.arguments?.[i])])),parameter_arguments:{...context.parameter_arguments,...Object.fromEntries((targetNode.parameters?.parameters || []).map((p,i)=>[p.id,node.arguments?.[i]]))} });
        } else method.completeness.unresolved.push({ code: 'RECURSIVE_OR_DEEP_INTERNAL_CALL', ast_id: node.id });
      }
      return;
    }
    if(['selfdestruct','suicide'].includes(name)&&callee?.nodeType==='Identifier'&&/t_function_(selfdestruct|suicide)_/.test(callee.typeDescriptions?.typeIdentifier||'')) {
      const to=sourceText(node.arguments?.[0]),step=addStep(method,node,'selfdestruct',`SELFDESTRUCT：将合约原生币余额交给 ${to}`,context);
      const fund_id=id(method.method_id,node.id,context.path,'selfdestruct');
      result.sections.funds.push({fund_id,method_id:method.method_id,kind:'selfdestruct_balance_transfer',asset:'native',unit:'wei',from_expression:'address(this)',to_expression:to,amount_expression:'address(this).balance',settlement:'static_intent',execution_rule_dependency:'EIP-6780',conditions:[...context.conditions],evidence_ids:step.evidence_ids});method.fund_ids.push(fund_id);return;
    }
    if (node.kind === 'typeConversion') return;
    if (callee?.nodeType !== 'MemberAccess' && node.kind !== 'structConstructorCall') {
      if (callee?.nodeType === 'NewExpression') addExternal('CREATE', null);
      return;
    }
    if (callee?.nodeType !== 'MemberAccess') return;
    if (/\b(abi|type)\b/.test(baseType) || ['encode', 'encodePacked', 'encodeWithSelector', 'encodeWithSignature', 'decode', 'concat'].includes(name)) return;
    const external = /\b(contract|address)\b/.test(baseType) || ['call', 'delegatecall', 'staticcall', 'callcode', 'send', 'transfer'].includes(name);
    if (!external) return;
    let kind = name === 'delegatecall' ? 'DELEGATECALL' : name === 'staticcall' || targetNode?.stateMutability === 'view' || targetNode?.stateMutability === 'pure' ? 'STATICCALL' : 'CALL';
    if (name === 'callcode') kind = 'CALLCODE';
    addExternal(kind, callee.expression);
    function addExternal(kind, target) {
      const call_id = id(method.method_id, node.id, context.path, 'external');
      const args = (node.arguments || []).map(n => sourceText(n));
      const target_expression = sourceText(target);
      const target_signature = targetNode?.nodeType === 'FunctionDefinition' ? `${targetNode.name}(${(targetNode.parameters?.parameters || []).map(typeOf).join(',')})` : '';
      const call = { call_id, method_id: method.method_id, call_type: kind, target_expression,
        target_expression_id: expression(method, target, context), selector: targetNode?.functionSelector ? '0x' + targetNode.functionSelector : '',
        signature: target_signature, input_arguments: args, input_expression: sourceText(node),
        value_expression: options.value ? sourceText(options.value) : null, value_expression_id: expression(method, options.value, context),
        failure_propagation: ['call', 'send', 'delegatecall', 'staticcall', 'callcode'].includes(name) ? 'returns_success_flag' : 'reverts_on_failure',
        conditions: [...context.conditions], parameter_bindings: context.parameter_bindings || {}, evidence_ids: [getEvidence(method, node)] };
      result.sections.calls.push(call); method.call_ids.push(call_id);
      addStep(method, node, 'external_call', `${kind} 调用 ${target_expression || '新合约'}：${sourceText(node)}`, context, { call_id });
      let fund;
      const isNative = /address/.test(baseType) && ['transfer', 'send'].includes(name);
      if ((isNative || options.value) && kind === 'CALL') {
        const amount = isNative ? node.arguments?.[0] : options.value;
        fund = { kind: 'native_payment', asset: 'native', from_expression: 'address(this)', to_expression: target_expression,
          amount_expression: sourceText(amount), amount_expression_id: expression(method, amount, context), unit: 'wei' };
      } else if (targetNode?.nodeType === 'FunctionDefinition' && ['transfer', 'transferFrom', 'approve'].includes(name)) {
        const params = targetNode.parameters?.parameters || [];
        const paramTypes = params.map(typeOf);
        const compatible = name === 'transferFrom' ? paramTypes.length === 3 && paramTypes[0] === 'address' && paramTypes[1] === 'address' && paramTypes[2] === 'uint256' : paramTypes.length === 2 && paramTypes[0] === 'address' && paramTypes[1] === 'uint256';
        if (compatible && /contract/.test(baseType)) {
          fund = { kind: name === 'transfer' ? 'token_transfer_intent' : name === 'transferFrom' ? 'token_transfer_from_intent' : 'token_approval',
            asset: target_expression, from_expression: name === 'transferFrom' ? args[0] : 'address(this)',
            to_expression: name === 'transferFrom' ? args[1] : args[0], amount_expression: args.at(-1),
            amount_expression_id: expression(method, node.arguments?.at(-1), context), unit: 'token_raw',
            protocol: 'transfer_interface',unit_basis:'caller_declared_interface',asset_standard_verified:false, settlement: 'requires_execution_evidence' };
        }
      }
      if (fund) {
        const fund_id = id(call_id, 'fund');
        result.sections.funds.push({ fund_id, method_id: method.method_id, call_id, conditions: [...context.conditions],
          parameter_bindings: context.parameter_bindings || {}, evidence_ids: call.evidence_ids, ...fund });
        method.fund_ids.push(fund_id);
      }
    }
  };
  const scanExpression = (method, node, context, access = 'read') => {
    if (!node) return;
    if (node.nodeType === 'Assignment') {
      scanExpression(method, node.leftHandSide, context, node.operator === '=' ? 'write' : 'readwrite');
      stateAccess(method, node.leftHandSide, 'write', context, node.rightHandSide, node.operator);
      scanExpression(method, node.rightHandSide, context);
      addStep(method, node, 'assignment', `执行 ${sourceText(node)}`, context);
      return;
    }
    if (node.nodeType === 'UnaryOperation' && ['++', '--', 'delete'].includes(node.operator)) {
      stateAccess(method, node.subExpression, 'write', context, node, node.operator);
      if (node.operator !== 'delete') stateAccess(method, node.subExpression, 'read', context);
      addStep(method, node, 'assignment', `执行 ${sourceText(node)}`, context); return;
    }
    if (['Identifier', 'IndexAccess', 'MemberAccess'].includes(node.nodeType)) {
      if (access !== 'write') stateAccess(method, node, 'read', context);
      if (node.nodeType === 'IndexAccess') scanExpression(method, node.indexExpression, context);
    }
    for (const child of children(node)) scanExpression(method, child, context, access);
    if (node.nodeType === 'FunctionCall') analyzeCall(method, node, context);
  };
  const analyzeBlock = (method, node, context) => {
    if (!node) return;
    if (node.nodeType === 'Block' || node.nodeType === 'UncheckedBlock') {
      for (const stmt of node.statements || []) {
        const flow=analyzeBlock(method, stmt, { ...context, unchecked: context.unchecked || node.nodeType === 'UncheckedBlock' });
        if(flow)return flow;
      }
      return;
    }
    if (node.nodeType === 'IfStatement') {
      const condition = sourceText(node.condition);
      scanExpression(method, node.condition, context);
      addStep(method, node.condition, 'branch', `按条件 ${condition} 选择分支`, context);
      const yes=analyzeBlock(method, node.trueBody, { ...context, path: `${context.path}/if:${node.id}:true`, conditions: [...context.conditions, condition] });
      const no=analyzeBlock(method, node.falseBody, { ...context, path: `${context.path}/if:${node.id}:false`, conditions: [...context.conditions, `!(${condition})`] });
      if(yes&&no)return yes===no?yes:'terminated';
      if(yes)context.conditions.push(`!(${condition})`);
      if(no)context.conditions.push(condition);
      return;
    }
    if (['ForStatement', 'WhileStatement', 'DoWhileStatement'].includes(node.nodeType)) {
      const condition = sourceText(node.condition);
      addStep(method, node, 'loop', `循环条件 ${condition || '无显式条件'}`, context);
      scanExpression(method, node.initializationExpression, context);
      scanExpression(method, node.condition, context);
      analyzeBlock(method, node.body, { ...context, path: `${context.path}/loop:${node.id}`, conditions: [...context.conditions, `循环：${condition}`] });
      scanExpression(method, node.loopExpression, context);
      return;
    }
    if (node.nodeType === 'Return') {
      scanExpression(method, node.expression, context);
      const step = addStep(method, node, 'return', `返回 ${sourceText(node.expression) || '已声明的返回变量'}`, context);
      method.return_ids.push(step.step_id); return 'return';
    }
    if (node.nodeType === 'EmitStatement') { const callee=node.eventCall?.expression;eventStep(method,node,node.eventCall,context,nodes.get(callee?.referencedDeclaration)); return; }
    if (node.nodeType === 'RevertStatement') { const step = addStep(method, node, 'revert', `回滚 ${sourceText(node.errorCall)}`, context); method.failure_ids.push(step.step_id); return 'revert'; }
    if (node.nodeType === 'InlineAssembly' || node.nodeType === 'TryStatement') {
      addStep(method, node, node.nodeType === 'InlineAssembly' ? 'assembly' : 'try', sourceText(node).slice(0, 2048), context);
      method.completeness.unresolved.push({ code: node.nodeType === 'InlineAssembly' ? 'ASSEMBLY_SEMANTICS_UNRESOLVED' : 'TRY_CATCH_PATHS_UNRESOLVED', ast_id: node.id });
      if (node.nodeType === 'TryStatement') for (const child of children(node)) analyzeBlock(method, child, context);
      return;
    }
    if (node.nodeType === 'VariableDeclarationStatement') {
      scanExpression(method, node.initialValue, context); addStep(method, node, 'local', `计算局部变量：${sourceText(node)}`, context); return;
    }
    if (node.nodeType === 'ExpressionStatement') { scanExpression(method, node.expression, context);const name=node.expression?.expression?.name;return name==='revert'?'revert':['selfdestruct','suicide'].includes(name)&&/t_function_(selfdestruct|suicide)_/.test(node.expression?.expression?.typeDescriptions?.typeIdentifier||'')?'jump':undefined; }
    if (['Break', 'Continue', 'PlaceholderStatement'].includes(node.nodeType)) { addStep(method, node, 'control', sourceText(node), context); return node.nodeType==='PlaceholderStatement'?undefined:'jump'; }
    for (const child of children(node)) scanExpression(method, child, context);
  };
  function eventStep(method,node,call,context,definition) {
    const parameters=definition?.parameters?.parameters || [];
    const signature=definition ? `${definition.name}(${parameters.map(typeOf).join(',')})` : '';
    addStep(method,node,'event',`发出事件 ${sourceText(call)}`,context,{event_name:definition?.name,event_signature:signature,
      event_arguments:(call?.arguments || []).map(sourceText),event_argument_ids:(call?.arguments || []).map(n=>expression(method,n,context)),
      event_indexed:parameters.map(p=>Boolean(p.indexed))});
  }
  for (const fn of functions) {
    const method = methodByAST.get(fn.id);
    if (!method) continue;
    const context = { path: 'entry', conditions: [], stack: [fn.id], unchecked: false };
    const modifiers = (fn.modifiers || []).map(m => ({ invocation: m, definition: nodes.get(m.modifierName?.referencedDeclaration) })).filter(m => m.definition);
    // Expand modifier wrappers at each placeholder, preserving post-body modifiers and conditional execution.
    const execute = (index, activeContext = context) => {
      if (index >= modifiers.length) return analyzeBlock(method, fn.body, activeContext);
      const { invocation, definition } = modifiers[index];
      const body = definition.body;
      const expand = (n, ctx) => {
        if (!n) return;
        if (n.nodeType === 'PlaceholderStatement') {
          const flow=execute(index + 1,{...ctx,path:`${ctx.path}/placeholder:${n.id}`});
          if(flow==='revert')return flow;
          if(flow==='terminated')method.completeness.unresolved.push({code:'MODIFIER_TERMINATION_PATHS',ast_id:n.id});
          return;
        }
        if (n.nodeType === 'Block') { for (const stmt of n.statements || []) { const flow=expand(stmt, ctx);if(flow)return flow } return; }
        if (n.nodeType === 'IfStatement') {
          scanExpression(method, n.condition, ctx);
          const cond = sourceText(n.condition);
          const yes=expand(n.trueBody, { ...ctx, path:`${ctx.path}/if:${n.id}:true`,conditions: [...ctx.conditions, cond] });
          const no=expand(n.falseBody, { ...ctx, path:`${ctx.path}/if:${n.id}:false`,conditions: [...ctx.conditions, `!(${cond})`] });
          if(yes&&no)return yes===no?yes:'terminated';
          if(yes)ctx.conditions.push(`!(${cond})`);
          if(no)ctx.conditions.push(cond);
          return;
        }
        return analyzeBlock(method, n, ctx);
      };
      return expand(body, { ...activeContext, path: `${activeContext.path}/modifier:${definition.id}`, parameter_bindings: { ...activeContext.parameter_bindings, ...Object.fromEntries((definition.parameters?.parameters || []).map((p, i) => [p.id, sourceText(invocation.arguments?.[i])])) },parameter_arguments:{...activeContext.parameter_arguments,...Object.fromEntries((definition.parameters?.parameters || []).map((p,i)=>[p.id,invocation.arguments?.[i]]))} });
    };
    execute(0);
    method.completeness.complete = method.completeness.unresolved.length === 0;
  }
  for (const [signature, value] of Object.entries(compiled.evm?.methodIdentifiers || {})) {
    const selector = '0x' + value;
    if (result.sections.methods.some(m => m.selector === selector)) continue;
    const name = signature.split('(')[0], variable = stateVars.find(v => v.name === name && v.visibility === 'public');
    if (!variable) continue;
    const method = makeMethod(result.snapshot, `selector:${selector}`, selector, name);
    method.signature = signature; method.verified_signature = true; method.visibility = 'external'; method.entry_kind = 'generated_getter'; method.state_mutability = 'view';
    method.completeness = { complete: true, unresolved: [], basis: 'source', scope: 'compiler_generated_getter', execution_paths: 'not_exhaustively_verified' };
    const abi = compiled.abi.find(a => a.type === 'function' && a.name === name);
    const eid = getEvidence(method, variable);
    method.parameters = (abi?.inputs || []).map((p, index) => ({ index, name: p.name || `arg${index}`, type: p.type, unit: null, evidence_ids: [eid] }));
    method.outputs = (abi?.outputs || []).map((p, index) => ({ index, name: p.name, type: p.type, evidence_ids: [eid] }));
    const context = { path: 'getter', conditions: [], unchecked: false };
    if (!variable.constant && variable.mutability !== 'immutable') stateAccess(method, { ...variable, nodeType: 'Identifier', referencedDeclaration: variable.id }, 'read', context);
    const step = addStep(method, variable, 'return', `读取并返回 ${name}${method.parameters.length ? ' 对应键值' : ''}`, context);
    method.return_ids.push(step.step_id); result.sections.methods.push(method);
  }
  for (const [path, value] of Object.entries(sources)) result.sections.artifacts.push({ artifact_id: id('source', path, hash(value.content)),
    kind: 'verified_source', filename: path, media_type: 'text/plain; charset=utf-8', text: value.content, sha256: hash(value.content) });
  result.sections.artifacts.push({ artifact_id: id('layout', result.snapshot.code_hash), kind: 'storage_layout', filename: 'storage-layout.json', media_type: 'application/json', text: JSON.stringify(layout || {}), sha256: hash(JSON.stringify(layout || {})) });
  result.sections.engines.push({ engine: 'solc-ast', version: source.compilation.compilerVersion, adapter_version: 'source-1',
    status: 'completed', duration_ms: Math.round(performance.now() - started), methods: methodByAST.size, source_verification: 'recompiled_and_snapshot_matched' });
  result.sections.coverage.push({ dimension: 'source', complete: true, basis: 'recompiled_runtime_and_verified_snapshot', methods: methodByAST.size });
  if(layout.basis)result.sections.coverage.push({dimension:'storage_layout',basis:layout.basis,complete:layout.unresolved.length===0,unresolved:layout.unresolved});
}

function children(node) {
  return Object.entries(node || {}).filter(([key]) => !['typeDescriptions', 'documentation'].includes(key)).flatMap(([, value]) =>
    Array.isArray(value) ? value.filter(v => v?.nodeType) : value?.nodeType ? [value] : []);
}
function visit(node, fn) { if (!node?.nodeType) return; fn(node); for (const child of children(node)) visit(child, fn); }
function rootDeclaration(node) {
  if (!node) return null;
  if (node.nodeType === 'Identifier') return node.referencedDeclaration;
  if (node.nodeType === 'IndexAccess') return rootDeclaration(node.baseExpression);
  if (node.nodeType === 'MemberAccess') return rootDeclaration(node.expression);
  return null;
}
