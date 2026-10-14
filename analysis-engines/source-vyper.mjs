import { pythonOperation } from './adapters/python.mjs';
import { hash, id, makeMethod, evidence, unique } from './model.mjs';

export function compileVyper(input, compilerVersion) {
  return pythonOperation({ operation: 'compile_vyper', input, compiler_version: compilerVersion });
}

function childNodes(node) {
  if (!node || typeof node !== 'object') return [];
  return Object.values(node).flatMap(v => Array.isArray(v) ? v : [v]).filter(v => v && typeof v === 'object' && typeof v.ast_type === 'string');
}
function walk(node, fn) { fn(node); for (const child of childNodes(node)) walk(child, fn); }

export function analyzeVyperSource(result, source, bytecode) {
  const started = performance.now();
  const { output, version } = compileVyper(source.stdJsonInput, source.compilation.compilerVersion);
  const fq = source.compilation.fullyQualifiedName, split = fq.lastIndexOf(':');
  const file = fq.slice(0, split), name = fq.slice(split + 1);
  const contracts = output.contracts?.[file];
  const compiled = contracts?.[name] || (contracts && Object.keys(contracts).length === 1 ? Object.values(contracts)[0] : null);
  if (!compiled) throw new Error('COMPILED_CONTRACT_NOT_FOUND');
  const rebuilt = compiled.evm?.deployedBytecode?.object?.replace(/^0x/, '').toLowerCase();
  if (!rebuilt || rebuilt !== source.runtimeBytecode?.recompiledBytecode?.replace(/^0x/, '').toLowerCase()) throw new Error('RECOMPILATION_BYTECODE_MISMATCH');
  if (source.runtimeBytecode?.onchainBytecode?.toLowerCase() !== bytecode.toLowerCase()) throw new Error('SOURCE_SNAPSHOT_CODE_MISMATCH');
  if(rebuilt!==bytecode.replace(/^0x/,'').toLowerCase())throw new Error('RECOMPILED_RUNTIME_SNAPSHOT_MISMATCH');
  if (!['match', 'exact_match'].includes(source.runtimeMatch) || !['match', 'exact_match'].includes(source.match)) throw new Error('SOURCE_NOT_VERIFIED');
  const ast = output.sources?.[file]?.ast;
  if (!ast?.ast_type) throw new Error('VYPER_AST_UNAVAILABLE');
  const sourceText = source.stdJsonInput.sources[file]?.content;
  if (typeof sourceText !== 'string') throw new Error('VYPER_SOURCE_TEXT_UNAVAILABLE');
  const bytes = Buffer.from(sourceText), artifact_id = id('source', file, hash(sourceText));
  const location = node => {
    const [start, length] = String(node.src || '').split(':').map(Number);
    if (!Number.isSafeInteger(start) || !Number.isSafeInteger(length) || start < 0 || start + length > bytes.length) throw new Error('VYPER_SOURCE_LOCATION_INVALID');
    return { artifact_id, file, start, length, line: node.lineno, ast_id: node.node_id };
  };
  const text = node => node ? bytes.subarray(location(node).start, location(node).start + location(node).length).toString('utf8') : '';
  const root = node => node?.ast_type === 'Attribute' && node.value?.id === 'self' ? node.attr : ['Attribute','Subscript'].includes(node?.ast_type) ? root(node.value) : null;
  const declarations = new Map((ast.body || []).filter(n => ['AnnAssign','VariableDecl'].includes(n.ast_type) && n.target?.ast_type === 'Name').map(n => [n.target.id, n]));
  const layout = compiled.layout?.storage_layout || compiled.layout || {};
  const methods = new Map();
  const functions = (ast.body || []).filter(n => n.ast_type === 'FunctionDef');
  const signatures = compiled.evm?.methodIdentifiers || {};
  for (const fn of functions) {
    const entries = Object.entries(signatures).filter(([signature]) => signature.slice(0, signature.indexOf('(')) === fn.name);
    for (const [signature, rawSelector] of entries.length ? entries : [['', '']]) {
      const selector = rawSelector ? '0x' + rawSelector.replace(/^0x/, '') : '';
      const method = makeMethod(result.snapshot, selector ? `selector:${selector}` : `vyper:${fn.node_id}`, selector, fn.name);
      const abi = (compiled.abi || []).find(a => a.type === 'function' && a.name === fn.name && `${a.name}(${(a.inputs || []).map(x => x.type).join(',')})` === signature);
      const decorators = (fn.decorator_list || []).map(text);
      method.entry_kind = fn.name === '__init__' ? 'constructor' : fn.name === '__default__' ? 'fallback' : 'function';
      method.code_phase = method.entry_kind === 'constructor' ? 'creation_source_not_verified_against_deployment' : 'runtime_source';
      method.visibility = decorators.includes('external') || decorators.includes('deploy') ? 'external' : 'internal';
      method.dispatch_role = selector ? 'external_dispatch' : 'source_definition';
      method.signature = signature || fn.name + '(' + (fn.args?.args || []).map(a => text(a.annotation)).join(',') + ')';
      method.verified_signature = Boolean(selector && abi);
      method.state_mutability = abi?.stateMutability || decorators.find(d => ['payable','view','pure'].includes(d)) || 'nonpayable';
      method.source_location = location(fn); method.compiler_decorators = decorators;
      method.parameters = (fn.args?.args || []).slice(0, abi?.inputs?.length ?? Infinity).map((arg, index) => ({ index, name: arg.arg,
        source_ast_id: arg.node_id, type: abi?.inputs?.[index]?.type || text(arg.annotation), unit: null,
        unit_status: 'not_inferred_from_name', constraints: [], evidence_ids: [evidence(result, method, 'source', location(arg), text(arg))] }));
      method.outputs = (abi?.outputs || []).map((x, index) => ({ index, name: x.name, type: x.type, evidence_ids: [] }));
      method.completeness = { complete: false, unresolved: [], basis: 'source', source_language: 'Vyper', scope: 'syntactic_control_and_data_flow', execution_paths: 'not_exhaustively_verified' };
      evidence(result, method, 'source', location(fn), text(fn).slice(0, 2048));
      result.sections.methods.push(method); methods.set(method.method_id, { method, fn });
    }
  }
  for (const { method, fn } of methods.values()) {
    const refs = new Map();
    const locals = new Map();
    walk(fn, n => { if (n.ast_type === 'AnnAssign' && n.target?.ast_type === 'Name') locals.set(n.target.id, n.target); });
    let expressionContext = { path: 'entry', conditions: [] };
    const expr = node => {
      if (!node) return null;
      const expression_id = id(method.method_id, 'vyper-expr', node.node_id, expressionContext.path);
      if (refs.has(expression_id)) return expression_id;
      const annotation = node.ast_type === 'Name' ? method.parameters.find(p => p.name === node.id)?.type : null;
      const integer = /^(u?int)(\d+)$/.exec(annotation || '');
      const row = { expression_id, method_id: method.method_id, op: node.op?.ast_type || node.ast_type,
        text: text(node), type: annotation || 'unresolved_vyper_type', width: integer ? Number(integer[2]) : null,
        signed: integer ? integer[1] === 'int' : null, arithmetic: 'vyper_checked_or_builtin_specific',
        rounding: ['Div','FloorDiv'].includes(node.op?.ast_type) ? 'toward_zero' : null,
        operands: [], path: expressionContext.path, conditions: [...expressionContext.conditions], step_order: method.step_ids.length,
        evidence_ids: [evidence(result, method, 'source', location(node), text(node).slice(0, 2048))] };
      refs.set(expression_id, row); row.operands = childNodes(node).filter(n => n.src).map(expr).filter(Boolean);
      if (node.ast_type === 'Int') row.value = String(node.value);
      if (node.ast_type === 'Name') {
        const parameter = method.parameters.find(p => p.name === node.id);
        row.parameter_index = parameter?.index ?? null;
        row.referenced_declaration = parameter?.source_ast_id ?? locals.get(node.id)?.node_id ?? null;
        const bound=expressionContext.bindings?.[node.id];
        if(bound&&bound.node_id!==node.node_id){row.binding_expression_id=expr(bound);row.operands.push(row.binding_expression_id);row.bound_argument=text(bound)}
        row.unresolved_local_reference=!parameter&&locals.has(node.id)&&!row.binding_expression_id;
      }
      result.sections.expressions.push(row); return expression_id;
    };
    const readState = (node, access, conditions, value = null, operation = null) => {
      const variable = root(node); if (!variable || !declarations.has(variable)) return;
      const declaration = declarations.get(variable), storage = layout[variable];
      const storage_id = id(method.method_id, node.node_id, access);
      if (result.sections.storage.some(s => s.storage_id === storage_id)) return;
      const row = { storage_id, state_id: id(result.snapshot.code_hash, result.snapshot.storage_address, 'vyper-state', variable),
        method_id: method.method_id, code_address: method.code_address, storage_address: method.storage_address,
        variable, variable_type: text(declaration.annotation), type: text(declaration.annotation), access,
        slot: storage?.slot == null ? null : String(storage.slot), offset: null,
        slot_role: node.ast_type === 'Subscript' || /HashMap|DynArray|\[/.test(text(declaration.annotation)) ? 'layout_base' : 'direct_slot',
        key_expression: text(node), key_expression_id: expr(node), value_expression: text(value),
        value_expression_id: expr(value), operation, conditions: [...conditions],
        layout_status: storage?.slot == null ? 'layout_unavailable' : 'verified_vyper_layout',
        evidence_ids: [evidence(result, method, 'source', location(node), text(node))] };
      result.sections.storage.push(row); method.storage_ids.push(storage_id);
    };
    const step = (node, kind, conditions, path, extra = {}) => {
      expressionContext = { ...expressionContext, conditions, path };
      const step_id = id(method.method_id, node.node_id, kind, path, method.step_ids.length);
      const row = { step_id, method_id: method.method_id, kind, order: method.step_ids.length,
        text: text(node), expression_id: expr(node), conditions: [...conditions], path,
        evidence_ids: [evidence(result, method, 'source', location(node), text(node).slice(0, 2048))], ...extra };
      result.sections.steps.push(row); method.step_ids.push(step_id); return row;
    };
    const calls = (node, conditions, path) => walk(node, call => {
      if (call.ast_type !== 'Call') return;
      const callee = call.func, name = callee?.id || callee?.attr;
      const kwargs = Object.fromEntries((call.keywords || []).map(k => [k.arg, k.value]));
      if(callee?.ast_type==='Attribute'&&callee.value?.id==='self') {
        const target=[...methods.values()].find(item=>item.fn.name===name);
        if(!target)return;
        const callerContext=expressionContext;
        const row=step(call,'internal_call',conditions,path,{target_method_id:target.method.method_id});
        result.sections.flows.push({edge_id:id(row.step_id,'internal-call'),edge_type:'calls',from:method.method_id,to:target.method.method_id,evidence_ids:row.evidence_ids});
        const stack=expressionContext.stack||[];
        if(stack.length>=12||stack.includes(target.fn.node_id)){method.completeness.unresolved.push({code:'RECURSIVE_OR_DEEP_INTERNAL_CALL',ast_id:call.node_id});return}
        const saved=new Map(locals);
        walk(target.fn,n=>{if(n.ast_type==='AnnAssign'&&n.target?.ast_type==='Name')locals.set(n.target.id,n.target)});
        const bindings={...expressionContext.bindings,...Object.fromEntries((target.fn.args?.args||[]).map((p,i)=>[p.arg,call.args?.[i]]))};
        body(target.fn.body,[...conditions],path+'/call:'+call.node_id,bindings,[...stack,target.fn.node_id]);
        locals.clear();for(const [key,value] of saved)locals.set(key,value);
        expressionContext=callerContext;
        return;
      }
      const nativeSend = callee?.ast_type === 'Name' && name === 'send';
      const rawCall = callee?.ast_type === 'Name' && name === 'raw_call';
      const external = callee?.ast_type === 'Attribute' && callee.value?.id !== 'self' && !declarations.has(callee.value?.id);
      if (!nativeSend && !rawCall && !external) return;
      const callType = kwargs.is_delegate_call?.value === true ? 'DELEGATECALL' : kwargs.is_static_call?.value === true ? 'STATICCALL' : 'CALL';
      const target = nativeSend || rawCall ? call.args?.[0] : callee.value;
      const payment = nativeSend ? call.args?.[1] : rawCall ? kwargs.value : null;
      const call_id = id(method.method_id, call.node_id, path, 'vyper-call');
      const row = { call_id, method_id: method.method_id, call_type: callType, target_expression: text(target),
        function_name: name, arguments: (call.args || []).map(text), value_expression: text(payment),
        conditions: [...conditions], evidence_ids: [evidence(result, method, 'source', location(call), text(call))] };
      result.sections.calls.push(row); method.call_ids.push(call_id);
      const iface=callee?.value?.ast_type==='Call'?(ast.body||[]).find(n=>n.ast_type==='InterfaceDef'&&n.name===callee.value.func?.id):null;
      const declared=iface?.body?.find(n=>n.ast_type==='FunctionDef'&&n.name===name);
      const types=(declared?.args?.args||[]).map(a=>text(a.annotation));
      if(external&&['transfer','transferFrom','approve'].includes(name)&&JSON.stringify(types)===JSON.stringify(name==='transferFrom'?['address','address','uint256']:['address','uint256'])) {
        const fund_id=id(call_id,'declared-token-interface'),args=call.args||[];
        result.sections.funds.push({fund_id,method_id:method.method_id,call_id,kind:name==='transfer'?'token_transfer_intent':name==='transferFrom'?'token_transfer_from_intent':'token_approval',
          asset:text(callee.value.args?.[0]),from_expression:name==='transferFrom'?text(args[0]):'address(this)',to_expression:text(args[name==='transferFrom'?1:0]),
          amount_expression:text(args.at(-1)),amount_expression_id:expr(args.at(-1)),unit:text(declared.returns)==='bool'?'token_raw':'asset_quantity_or_token_id',unit_basis:'caller_declared_interface',asset_standard_verified:false,
          settlement:'requires_execution_evidence',conditions:[...conditions],evidence_ids:row.evidence_ids});
        method.fund_ids.push(fund_id);
      }
      if (payment && callType === 'CALL') {
        const fund_id = id(call_id, 'native-payment');
        result.sections.funds.push({ fund_id, method_id: method.method_id, kind: 'native_payment', asset: 'native', unit: 'wei',
          from_expression: 'address(this)', to_expression: text(target), amount_expression: text(payment),
          amount_expression_id: expr(payment), settlement: 'static_intent', conditions: [...conditions], evidence_ids: row.evidence_ids });
        method.fund_ids.push(fund_id);
      }
    });
    const body = (nodes, conditions = [], path = 'entry', bindings={}, stack=[]) => {
      for (const node of nodes || []) {
        expressionContext = { conditions, path, bindings, stack };
        if (node.ast_type === 'Assert') {
          const guard_id = id(method.method_id, 'vyper-guard', node.node_id, path);
          const row = { guard_id, method_id: method.method_id, condition: text(node.test), expression_id: expr(node.test),
            failure: 'revert', conditions: [...conditions], path, evidence_ids: [evidence(result, method, 'source', location(node), text(node))] };
          result.sections.permissions.push(row); method.guard_ids.push(guard_id); step(node, 'guard', conditions, path, { guard_id });
          conditions.push(text(node.test));
        } else if (node.ast_type === 'If') {
          step(node.test, 'branch', conditions, path);
          const yes = body(node.body, [...conditions, text(node.test)], path + '/true:' + node.node_id,bindings,stack);
          const no = body(node.orelse, [...conditions, '!(' + text(node.test) + ')'], path + '/false:' + node.node_id,bindings,stack);
          if (yes.termination && no.termination) return { termination: 'branched_terminal' };
          if (yes.termination) conditions.push('!(' + text(node.test) + ')');
          if (no.termination) conditions.push(text(node.test));
        } else if (node.ast_type === 'For') {
          step(node, 'loop', conditions, path);
          method.completeness.unresolved.push({ code: 'LOOP_PATHS_NOT_UNROLLED', ast_id: node.node_id });
          body(node.body, [...conditions, 'loop(' + text(node.target) + ' in ' + text(node.iter) + ')'], path + '/loop:' + node.node_id,bindings,stack);
        } else if (['Assign','AnnAssign','AugAssign'].includes(node.ast_type)) {
          const assigned = step(node, node.target && root(node.target) || node.targets?.some(root) ? 'assignment' : 'local', conditions, path);
          for (const target of node.targets || [node.target]) if (target?.ast_type === 'Name' && locals.has(target.id)) {
            result.sections.definitions.push({ definition_id: id(assigned.step_id, 'definition'), method_id: method.method_id,
              declaration_id: locals.get(target.id).node_id, name: target.id, value_expression_id: expr(node.value),
              operation: node.ast_type === 'AugAssign' ? node.op?.ast_type : '=', order: assigned.order,
              conditions: [...conditions], path, evidence_ids: assigned.evidence_ids });
          }
          for (const target of node.targets || [node.target]) if (target) readState(target, 'write', conditions, node.value, node.op?.ast_type || '=');
        } else if (['Return','Raise','Break','Continue'].includes(node.ast_type)) {
          if (node.value) {
            calls(node.value, conditions, path);
            walk(node.value, n => { if (root(n) && ['Attribute','Subscript'].includes(n.ast_type)) readState(n, 'read', conditions); });
          }
          const row = step(node, node.ast_type.toLowerCase(), conditions, path);
          if (node.ast_type === 'Return') method.return_ids.push(row.step_id);
          if (node.ast_type === 'Raise') method.failure_ids.push(row.step_id);
          return { termination: node.ast_type.toLowerCase() };
        } else if (node.ast_type === 'Log') step(node, 'event', conditions, path);
        else if (node.ast_type === 'Expr') step(node, 'expression', conditions, path);
        else method.completeness.unresolved.push({ code: 'VYPER_STATEMENT_UNSUPPORTED', ast_type: node.ast_type, ast_id: node.node_id });
        expressionContext = { conditions, path, bindings, stack };
        calls(node.ast_type === 'If' ? node.test : node.ast_type === 'For' ? node.iter : node, conditions, path);
        const readNode = node.ast_type === 'If' ? node.test : node.ast_type === 'For' ? node.iter : ['Assign','AnnAssign'].includes(node.ast_type) ? node.value : node;
        if (readNode) walk(readNode, n => {
          if (root(n) && ['Attribute','Subscript'].includes(n.ast_type)) readState(n, 'read', conditions);
        });
      }
      return { termination: null };
    };
    body(fn.body);
    if (method.state_mutability === 'payable' && method.entry_kind !== 'constructor') {
      const fund_id = id(method.method_id, 'vyper-payable');
      result.sections.funds.push({ fund_id, method_id: method.method_id, kind: 'native_received_intent', asset: 'native', unit: 'wei',
        from_expression: 'msg.sender', to_expression: 'address(this)', amount_expression: 'msg.value', settlement: 'static_intent',
        conditions: [], evidence_ids: method.evidence_ids }); method.fund_ids.push(fund_id);
    }
  }
  for (const [variable, declaration] of declarations) {
    if (declaration.annotation?.ast_type !== 'Call' || declaration.annotation.func?.id !== 'public') continue;
    const entry = Object.entries(signatures).find(([signature]) => signature.startsWith(variable + '('));
    const abi = (compiled.abi || []).find(a => a.type === 'function' && a.name === variable);
    if (!entry || !abi || result.sections.methods.some(m => m.signature === entry[0])) continue;
    const selector = '0x' + entry[1].replace(/^0x/, '');
    const method = makeMethod(result.snapshot, `selector:${selector}`, selector, variable);
    method.signature = entry[0]; method.verified_signature = true;
    method.entry_kind = 'generated_getter'; method.dispatch_role = 'external_dispatch'; method.visibility = 'external';
    method.state_mutability = abi.stateMutability; method.code_phase = 'runtime_source';
    method.source_location = location(declaration);
    method.completeness = { complete: false, unresolved: [], basis: 'source', source_language: 'Vyper', execution_paths: 'not_exhaustively_verified' };
    const eid = evidence(result, method, 'compiler_generated_getter', location(declaration), text(declaration));
    method.parameters = (abi.inputs || []).map((p,index) => ({index,name:p.name||`arg${index}`,type:p.type,unit:null,evidence_ids:[eid]}));
    method.outputs = (abi.outputs || []).map((p,index) => ({index,name:p.name,type:p.type,evidence_ids:[eid]}));
    const storage = layout[variable];
    if (storage?.slot != null) {
      const storage_id = id(method.method_id,'getter-state');
      result.sections.storage.push({storage_id,state_id:id(result.snapshot.code_hash,result.snapshot.storage_address,'vyper-state',variable),
        method_id:method.method_id,code_address:method.code_address,storage_address:method.storage_address,variable,
        variable_type:storage.type,type:storage.type,access:'read',slot:String(storage.slot),offset:null,
        slot_role:method.parameters.length?'layout_base':'direct_slot',key_expression:variable,
        conditions:[],layout_status:'verified_vyper_layout',evidence_ids:[eid]});
      method.storage_ids.push(storage_id);
    }
    const step_id = id(method.method_id,'getter-return');
    result.sections.steps.push({step_id,method_id:method.method_id,order:0,kind:'return',text:`读取并返回 ${variable}`,conditions:[],evidence_ids:[eid]});
    method.step_ids.push(step_id);method.return_ids.push(step_id);result.sections.methods.push(method);
  }
  result.sections.artifacts.push({ artifact_id, kind: 'source', filename: file, media_type: 'text/plain; charset=utf-8', text: sourceText, sha256: hash(sourceText) });
  const compiledText = JSON.stringify(output);
  result.sections.artifacts.push({ artifact_id: id('vyper-output', result.snapshot.code_hash), kind: 'vyper_compilation', filename: 'vyper-compilation.json',
    media_type: 'application/json', text: compiledText, sha256: hash(compiledText) });
  result.sections.engines.push({ engine: 'vyper-ast', version, adapter_version: 'vyper-source-1', status: 'completed',
    source_verification: 'recompiled_and_snapshot_matched', methods: methods.size, duration_ms: Math.round(performance.now() - started) });
}
