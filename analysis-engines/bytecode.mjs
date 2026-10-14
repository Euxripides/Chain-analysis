import { Contract, sol } from 'sevm';
import { whatsabi } from '@shazow/whatsabi';
import { id, hash, makeMethod, evidence, unique } from './model.mjs';

const OPCODES = {
  0x00: 'STOP', 0x01: 'ADD', 0x02: 'MUL', 0x03: 'SUB', 0x04: 'DIV', 0x05: 'SDIV', 0x06: 'MOD', 0x07: 'SMOD',
  0x08: 'ADDMOD', 0x09: 'MULMOD', 0x0a: 'EXP', 0x0b: 'SIGNEXTEND', 0x10: 'LT', 0x11: 'GT', 0x12: 'SLT', 0x13: 'SGT',
  0x14: 'EQ', 0x15: 'ISZERO', 0x16: 'AND', 0x17: 'OR', 0x18: 'XOR', 0x19: 'NOT', 0x1a: 'BYTE', 0x1b: 'SHL', 0x1c: 'SHR', 0x1d: 'SAR',
  0x20: 'KECCAK256', 0x30: 'ADDRESS', 0x31: 'BALANCE', 0x32: 'ORIGIN', 0x33: 'CALLER', 0x34: 'CALLVALUE', 0x35: 'CALLDATALOAD',
  0x36: 'CALLDATASIZE', 0x37: 'CALLDATACOPY', 0x38: 'CODESIZE', 0x39: 'CODECOPY', 0x3a: 'GASPRICE', 0x3b: 'EXTCODESIZE',
  0x3c: 'EXTCODECOPY', 0x3d: 'RETURNDATASIZE', 0x3e: 'RETURNDATACOPY', 0x3f: 'EXTCODEHASH', 0x40: 'BLOCKHASH', 0x41: 'COINBASE',
  0x42: 'TIMESTAMP', 0x43: 'NUMBER', 0x44: 'PREVRANDAO', 0x45: 'GASLIMIT', 0x46: 'CHAINID', 0x47: 'SELFBALANCE',
  0x48: 'BASEFEE', 0x49: 'BLOBHASH', 0x4a: 'BLOBBASEFEE', 0x50: 'POP', 0x51: 'MLOAD', 0x52: 'MSTORE', 0x53: 'MSTORE8',
  0x54: 'SLOAD', 0x55: 'SSTORE', 0x56: 'JUMP', 0x57: 'JUMPI', 0x58: 'PC', 0x59: 'MSIZE', 0x5a: 'GAS', 0x5b: 'JUMPDEST',
  0x5c: 'TLOAD', 0x5d: 'TSTORE', 0x5e: 'MCOPY', 0x5f: 'PUSH0', 0xf0: 'CREATE', 0xf1: 'CALL', 0xf2: 'CALLCODE',
  0xf3: 'RETURN', 0xf4: 'DELEGATECALL', 0xf5: 'CREATE2', 0xfa: 'STATICCALL', 0xfd: 'REVERT', 0xfe: 'INVALID', 0xff: 'SELFDESTRUCT',
};
export function disassemble(bytecode) {
  const bytes = Buffer.from(bytecode.replace(/^0x/, ''), 'hex'), rows = [];
  for (let pc = 0; pc < bytes.length;) {
    const start = pc, op = bytes[pc++];
    const push = op >= 0x60 && op <= 0x7f ? op - 0x5f : 0;
    const mnemonic = push ? `PUSH${push}` : op >= 0x80 && op <= 0x8f ? `DUP${op - 0x7f}` : op >= 0x90 && op <= 0x9f ? `SWAP${op - 0x8f}` : op >= 0xa0 && op <= 0xa4 ? `LOG${op - 0xa0}` : OPCODES[op] || 'UNKNOWN';
    const data = push ? '0x' + bytes.subarray(pc, pc + push).toString('hex') : null;
    rows.push({ pc: start, opcode: op, mnemonic, data, truncated_operand: pc + push > bytes.length }); pc += push;
  }
  return rows;
}
function raw(node, state = { seen: new WeakMap(), sequence: 0 }, depth = 0) {
  if (typeof node === 'bigint') return node.toString();
  if (node == null || typeof node !== 'object') return node;
  if (depth > 24) return { unresolved: 'expression_depth' };
  if (state.seen.has(node)) return { $ref: state.seen.get(node) };
  const nodeID=++state.sequence;state.seen.set(node,nodeID);
  if (Array.isArray(node)) return { $id:nodeID, items:node.map(v => raw(v, state, depth + 1)) };
  return { $id:nodeID,...Object.fromEntries(Object.entries(node).filter(([k, v]) => !['mappings', 'variable', 'contract'].includes(k) && typeof v !== 'function').map(([k, v]) => [k, raw(v, state, depth + 1)])) };
}
function render(node) {
  if (!node) return '';
  let text;try { text=sol`${node.tag ? node.eval() : node}` } catch { text=JSON.stringify(raw(node)) }
  // Large symbolic trees stay in the IR artifact. Their display form is an
  // explicit reference, never a truncated amount formula presented as complete.
  return Buffer.byteLength(text)>4096 ? `symbolic_expression:${hash(text)}` : text;
}
export function analyzeBytecode(result, bytecode, signatures = []) {
  const started = performance.now(), instructions = disassemble(bytecode);
  const opText = instructions.map(o => `${o.pc.toString(16).padStart(6, '0')} ${o.mnemonic}${o.data ? ' ' + o.data : ''}`).join('\n');
  result.sections.artifacts.push({ artifact_id: id('disassembly', result.snapshot.code_hash), kind: 'disassembly', filename: 'disassembly.txt', media_type: 'text/plain; charset=utf-8', text: opText, sha256: hash(opText) });
  let selectors = [];
  try { selectors = whatsabi.selectorsFromBytecode(bytecode); result.sections.engines.push({ engine: 'whatsabi', version: '0.29.0', adapter_version: 'entry-1', status: 'completed', selectors, duration_ms: Math.round(performance.now() - started) }); }
  catch (error) { result.sections.engines.push({ engine: 'whatsabi', version: '0.29.0', status: 'failed', error_code: 'SELECTOR_EXTRACTION_FAILED', error_message: String(error.message).slice(0, 300) }); }
  let contract;
  try { contract = new Contract(bytecode); }
  catch (error) {
    result.sections.engines.push({ engine: 'sevm', version: '0.7.4', status: 'failed', error_code: 'SYMBOLIC_EXECUTION_FAILED', error_message: String(error.message).slice(0, 500), duration_ms: Math.round(performance.now() - started) });
    for (const selector of selectors) {
      if (result.sections.methods.some(m => m.selector === selector)) continue;
      const method = makeMethod(result.snapshot, `selector:${selector}`, selector);
      method.completeness.unresolved.push({ code: 'SYMBOLIC_EXECUTION_FAILED' }); result.sections.methods.push(method);
    }
    result.sections.coverage.push({ dimension: 'bytecode', complete: false, unresolved: 'symbolic_execution_failed', discovered_selectors: selectors.length });
    return;
  }
  let pseudocode = '';
  try { pseudocode = contract.solidify(); } catch (e) { pseudocode = `PSEUDOCODE_RENDER_FAILED: ${e.message}`; }
  result.sections.artifacts.push({ artifact_id: id('pseudocode', result.snapshot.code_hash), kind: 'pseudocode', filename: 'decompiled.sol', media_type: 'text/plain; charset=utf-8', text: pseudocode, sha256: hash(pseudocode) });
  const blocks = [...contract.blocks].map(([pc, b]) => ({ pc, pc_end: b.pcend, opcodes: b.opcodes.map(o => o.opcode.pc), states: b.states.map(s => raw(s.stmts)) }));
  const ir = JSON.stringify({ main: raw(contract.main), functions: Object.fromEntries(Object.entries(contract.functions).map(([k, v]) => [k, raw(v.stmts)])), blocks });
  result.sections.artifacts.push({ artifact_id: id('symbolic-ir', result.snapshot.code_hash), kind: 'symbolic_ir', filename: 'symbolic-ir.json', media_type: 'application/json', text: ir, sha256: hash(ir) });
  const originalSource = result.sections.methods.some(m => m.completeness.basis === 'source');
  const locations = new WeakMap();
  for (const [pc, block] of contract.blocks) for (const state of block.states) for (const stmt of state.stmts) locations.set(stmt, { pc, pc_end: block.pcend, granularity: 'basic_block' });
  const failures = contract.errors.map(e => ({ code: 'UNRESOLVED_SYMBOLIC_PATH', reason: e.err.reason, pc: e.err.opcode?.pc, opcode: e.err.opcode?.mnemonic }));
  const functions = Object.entries(contract.functions).map(([selector, fn]) => ({ selector: '0x' + selector, stmts: fn.stmts, entry: contract.functionBranches.get(selector)?.pc ?? null }));
  functions.push({ selector: '', stmts: contract.main, entry: 0 });
  for (const fn of functions) {
    const sourceMethod=result.sections.methods.find(m=>m.selector===fn.selector&&m.completeness.basis==='source'&&m.dispatch_role!=='overridden_source_definition'&&(fn.selector||['fallback','receive'].includes(m.entry_kind)));
    if (originalSource && (!sourceMethod || !sourceMethod.completeness.unresolved.length)) continue;
    const method = sourceMethod || makeMethod(result.snapshot, fn.selector ? `selector:${fn.selector}` : 'dispatch', fn.selector, fn.selector ? '' : 'fallback / dispatcher');
    if(sourceMethod) {method.completeness.basis='mixed_source_and_bytecode';method.completeness.additional_bases=['source','bytecode_symbolic'];method.completeness.complete=false}
    method.entry_pc = fn.entry;
    method.signature_candidates = signatures.filter(s => s.selector === fn.selector).map(s => ({ signature: s.signature, source: s.source || 'signature_database', verified: false }));
    if(!sourceMethod)result.sections.methods.push(method);
    const exprCache = new WeakMap();
    const recordedCalls = new WeakSet();
    const expr = (node, location, depth = 0) => {
      if (!node || typeof node !== 'object') return null;
      if (exprCache.has(node)) return exprCache.get(node);
      const expression_id = id(method.method_id, exprCache.size || result.sections.expressions.length, raw(node), location);
      exprCache.set(node, expression_id);
      const operands = depth < 24 ? Object.entries(node).filter(([key]) => !['mappings', 'variable'].includes(key)).flatMap(([, v]) => Array.isArray(v) ? v : [v]).filter(v => v?.tag).map(v => expr(v, location, depth + 1)) : [];
      const evidence_id = evidence(result, method, 'bytecode', location, render(node).slice(0, 2048));
      const rawNode=raw(node),serialized=JSON.stringify(rawNode);
      const largeRaw=Buffer.byteLength(serialized)>65536,display=render(node);
      if(largeRaw||display.startsWith('symbolic_expression:'))method.completeness.unresolved.push({code:'EXPRESSION_REQUIRES_IR_ARTIFACT',expression_id,location});
      result.sections.expressions.push({ expression_id, method_id: method.method_id,origin:'bytecode_symbolic', op: node.tag || node.name || 'unresolved', text: display,
        width: 256, signed: node.tag === 'Sar' ? true : null, arithmetic: 'evm_word_wrap', rounding: node.tag === 'Div' ? 'unsigned_floor' : null,
        value: node.tag === 'Val' ? node.val.toString() : null, operands, raw_node: largeRaw ? {artifact_id:location.artifact_id,ir_path:location.ir_path,sha256:hash(serialized),status:'read_original_ir'} : rawNode, evidence_ids: [evidence_id] });
      return expression_id;
    };
    const walk = (stmts, context) => {
      for (let ordinal = 0; ordinal < stmts.length; ordinal++) {
        const stmt = stmts[ordinal], kind = stmt.name || stmt.tag;
        const location = { artifact_id: id('symbolic-ir', result.snapshot.code_hash), ...locations.get(stmt), entry_pc: fn.entry, ir_path: `${context.path}/${ordinal}` };
        const evidence_id = evidence(result, method, 'bytecode_ir', location, render(stmt).slice(0, 2048));
        const step_id = id(method.method_id, location.ir_path);
        method.step_ids.push(step_id);
        result.sections.steps.push({ step_id, method_id: method.method_id, origin:'bytecode_symbolic',order: method.step_ids.length - 1,
          kind: ({ Require: 'guard', If: 'branch', SStore: 'assignment', MappingStore: 'assignment', Return: 'return', Revert: 'revert', Log: 'event' })[kind] || kind,
          text: render(stmt).slice(0, 4096), conditions: context.conditions, path: context.path, evidence_ids: [evidence_id] });
        if (kind === 'Require') {
          const guard_id = id(step_id, 'guard'); method.guard_ids.push(guard_id);
          result.sections.permissions.push({ guard_id, method_id: method.method_id,origin:'bytecode_symbolic', condition: render(stmt.condition), expression_id: expr(stmt.condition, location), conditions: context.conditions, failure: 'revert', evidence_ids: [evidence_id] });
        }
        if (kind === 'If') {
          walk(stmt.trueBlock || [], { path: `${location.ir_path}/true`, conditions: [...context.conditions, render(stmt.condition)] });
          walk(stmt.falseBlock || [], { path: `${location.ir_path}/false`, conditions: [...context.conditions, `!(${render(stmt.condition)})`] });
        }
        if (kind === 'Return') method.return_ids.push(step_id);
        if (kind === 'Revert' || kind === 'Throw' || kind === 'Invalid') method.failure_ids.push(step_id);
        if (kind === 'Throw') method.completeness.unresolved.push({ code: 'UNRESOLVED_SYMBOLIC_PATH', reason: stmt.reason, location });
        scan(stmt, location, context, new Set());
        function scan(node, loc, ctx, seen) {
          if (!node || typeof node !== 'object' || seen.has(node)) return;
          seen.add(node);
          const tag = node.tag || node.name;
          if (['SLoad', 'MappingLoad', 'SStore', 'MappingStore'].includes(tag)) {
            const storage_id = id(step_id, tag, render(node)); const key_expression = render(node.slot);
            if (!result.sections.storage.some(s => s.storage_id === storage_id)) {
              const state_id = id(result.snapshot.storage_address, result.snapshot.code_hash, node.location ?? key_expression);
              result.sections.storage.push({ storage_id, state_id, method_id: method.method_id, access: tag.endsWith('Store') ? 'write' : 'read',
                slot: node.slot?.tag === 'Val' ? node.slot.val.toString() : node.location == null ? null : String(node.location), offset: null, type: 'evm_word',slot_role:tag.startsWith('Mapping')?'layout_base':'direct_slot',
                key_expression, key_expression_id: expr(node.slot, loc), value_expression: render(node.data), value_expression_id: expr(node.data, loc),
                storage_address: result.snapshot.storage_address, code_address: result.snapshot.code_address, layout_status: 'inferred_bytecode',
                conditions: ctx.conditions, evidence_ids: [evidence_id] }); method.storage_ids.push(storage_id);
            }
          }
          if (['Call', 'DelegateCall', 'StaticCall', 'CallCode', 'Create', 'Create2'].includes(tag) && !recordedCalls.has(node)) {
            recordedCalls.add(node);
            const call_id = id(step_id, tag, render(node));
            if (!result.sections.calls.some(c => c.call_id === call_id)) {
              result.sections.calls.push({ call_id, method_id: method.method_id, call_type: tag.toUpperCase(), target_expression: render(node.address),
                target_expression_id: expr(node.address, loc), value_expression: render(node.value), value_expression_id: expr(node.value, loc),
                input_expression: render(node), failure_propagation: node.throwOnFail ? 'reverts_on_failure' : 'returns_success_flag', conditions: [], condition_status: 'symbolic_call_control_unresolved', evidence_ids: [evidence_id] });
              method.call_ids.push(call_id);
              let evaluatedValue;try{evaluatedValue=node.value?.eval()}catch{}
              if (tag === 'Call' && !(evaluatedValue?.tag === 'Val' && evaluatedValue.val === 0n)) {
                const fund_id = id(call_id, 'native');
                result.sections.funds.push({ fund_id, method_id: method.method_id, call_id, kind: 'native_payment', asset: 'native', unit: 'wei',
                  from_expression: 'address(this)', to_expression: render(node.address), amount_expression: render(node.value), amount_expression_id: expr(node.value, loc),
                  settlement: 'static_intent', conditions: [], condition_status: 'symbolic_call_control_unresolved', evidence_ids: [evidence_id] }); method.fund_ids.push(fund_id);
              }
            }
          }
          if (tag === 'CallDataLoad' && node.location?.eval()?.tag === 'Val') {
            const offset = node.location.eval().val;
            if (offset >= 4n && (offset - 4n) % 32n === 0n && offset < 8192n) {
              const index = Number((offset - 4n) / 32n);
              if (!method.parameters.some(p => p.index === index)) method.parameters.push({ index, name: `calldata_word_${index}`, type: 'bytes32', type_status: 'raw_word_only', unit: null, offset: offset.toString(), evidence_ids: [evidence_id] });
            }
          }
          if (node.tag) expr(node, loc);
          for (const [key, value] of Object.entries(node)) {
            if (['trueBlock', 'falseBlock', 'mappings', 'variable', 'contract'].includes(key)) continue;
            for (const child of Array.isArray(value) ? value : [value]) if (child && typeof child === 'object') scan(child, loc, ctx, seen);
          }
        }
      }
    };
    walk(fn.stmts, { path: 'entry', conditions: [] });
    method.completeness.unresolved.push(...failures);
    method.completeness.complete = false;
    method.completeness.execution_paths = 'symbolic_paths_not_exhaustively_verified';
    method.parameters.sort((a, b) => a.index - b.index);
  }
  for (const selector of selectors) if (!result.sections.methods.some(m => m.selector === selector)) {
    const method = makeMethod(result.snapshot, `selector:${selector}`, selector);
    method.completeness.unresolved.push({ code: 'SELECTOR_WITHOUT_PARSED_BODY' }); result.sections.methods.push(method);
  }
  const discovered = unique([...selectors, ...Object.keys(contract.functions).map(s => '0x' + s)]);
  result.sections.engines.push({ engine: 'sevm', version: '0.7.4', adapter_version: 'symbolic-1', status: failures.length ? 'partial' : 'completed',
    duration_ms: Math.round(performance.now() - started), parsed_functions: Object.keys(contract.functions).length, unresolved: failures });
  result.sections.coverage.push({ dimension: 'bytecode', complete: false, discovered_selectors: discovered.length,
    parsed_functions: Object.keys(contract.functions).length, unresolved: failures, basis: 'symbolic_paths_not_exhaustive' });
}
