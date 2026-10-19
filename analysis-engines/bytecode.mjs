import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { whatsabi } from '@shazow/whatsabi';
import { id, hash, makeMethod } from './model.mjs';
import { sevmRequest, mergeSevmResponse, decodeSevmResponse, SEVM_OUTPUT_LIMIT } from './bytecode-sevm-protocol.mjs';

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

export function analyzeBytecode(result, bytecode, signatures = [], options = {}) {
  const started = performance.now(), instructions = disassemble(bytecode);
  const opText = instructions.map(o => `${o.pc.toString(16).padStart(6, '0')} ${o.mnemonic}${o.data ? ' ' + o.data : ''}`).join('\n');
  result.sections.artifacts.push({ artifact_id:id('disassembly',result.snapshot.code_hash),kind:'disassembly',filename:'disassembly.txt',
    media_type:'text/plain; charset=utf-8',text:opText,sha256:hash(opText) });
  if(result.snapshot.code_identity?.unbound_immutable_ranges?.length || result.snapshot.code_identity?.unbound_library_ranges?.length) {
    for(const engine of ['whatsabi','sevm']) result.sections.engines.push({engine,status:'skipped',reason:'compiled_template_has_unbound_deployment_ranges'});
    result.sections.coverage.push({dimension:'bytecode',complete:false,reason:'compiled_template_values_are_not_observed_runtime',code_identity:result.snapshot.code_identity});
    return;
  }
  let selectors = [];
  try {
    selectors = whatsabi.selectorsFromBytecode(bytecode);
    result.sections.engines.push({engine:'whatsabi',version:'0.29.0',adapter_version:'entry-1',status:'completed',selectors,duration_ms:Math.round(performance.now()-started)});
  } catch(error) {
    result.sections.engines.push({engine:'whatsabi',version:'0.29.0',status:'failed',error_code:'SELECTOR_EXTRACTION_FAILED',error_message:String(error.message).slice(0,300)});
  }
  const timeout = Math.min(options.timeoutMs ?? 60000,60000), request = sevmRequest(result,bytecode,signatures,selectors);
  let failure = null, usage;
  if(timeout < 1) failure = {code:'SEVM_CHILD_BUDGET_UNAVAILABLE',detail:'Parent save and interpretation reserve retained'};
  else {
    const run = spawnSync(process.execPath,['--max-old-space-size='+Math.min(options.heapMiB ?? 512,512),
      options.workerPath || fileURLToPath(new URL('./bytecode-sevm-worker.mjs',import.meta.url))],{
      input:JSON.stringify(request),encoding:'utf8',timeout:Math.floor(timeout),killSignal:'SIGKILL',
      maxBuffer:Math.min(options.maxBuffer ?? SEVM_OUTPUT_LIMIT,SEVM_OUTPUT_LIMIT),
      env:{...process.env,NODE_OPTIONS:''},windowsHide:true,
    });
    if(run.error?.code === 'ETIMEDOUT') failure = {code:'SEVM_CHILD_TIMEOUT',timeout_ms:Math.floor(timeout)};
    else if(run.error?.code === 'ENOBUFS') failure = {code:'SEVM_CHILD_OUTPUT_LIMIT'};
    else if(/heap out of memory|reached heap limit|allocation failed.*heap/i.test(run.stderr || '')) failure = {code:'SEVM_CHILD_OOM',heap_limit_mib:Math.min(options.heapMiB ?? 512,512)};
    else if(run.signal) failure = {code:'SEVM_CHILD_SIGNAL',signal:run.signal};
    else if(run.status !== 0) failure = {code:/^SEVM_(?:EXPANDED_)?OUTPUT_LIMIT/.test(run.stderr || '')?'SEVM_CHILD_OUTPUT_LIMIT':'SEVM_CHILD_EXIT',exit_code:run.status,detail:String(run.stderr || run.error?.code || '').slice(0,1000)};
    else {
      try { const decoded=decodeSevmResponse(JSON.parse(run.stdout));usage = {...mergeSevmResponse(result,request,decoded.response),response_transport:decoded.transport}; }
      catch(error) { failure = {code:'SEVM_CHILD_INVALID_OUTPUT',detail:String(error.message).slice(0,500)}; }
    }
  }
  const resource = {timeout_ms:Math.max(0,Math.floor(timeout)),heap_limit_mib:Math.min(options.heapMiB ?? 512,512),output_limit_bytes:Math.min(options.maxBuffer ?? SEVM_OUTPUT_LIMIT,SEVM_OUTPUT_LIMIT),
    wall_ms:Math.round(performance.now()-started),...usage};
  if(!failure) {
    const engine = result.sections.engines.findLast(e => e.engine === 'sevm');
    if(engine) engine.isolation = {kind:'node_child_process',...resource};
    return;
  }
  for(const selector of selectors) if(!result.sections.methods.some(m => m.selector === selector)) {
    result.sections.methods.push(makeMethod(result.snapshot,'selector:'+selector,selector));
  }
  if(!result.sections.methods.length) result.sections.methods.push(makeMethod(result.snapshot,'dispatch','','fallback / dispatcher'));
  const affected = [];
  for(const method of result.sections.methods) if(method.entry_kind !== 'constructor' && !method.code_phase?.startsWith('creation') &&
      method.dispatch_role !== 'overridden_source_definition' && (method.completeness.basis !== 'source' || method.completeness.unresolved.length)) {
    method.completeness.complete = false;
    method.completeness.unresolved.push({...failure,engine:'sevm',scope:'bytecode_method_body'});
    affected.push(method.method_id);
  }
  result.sections.engines.push({engine:'sevm',version:'0.7.4',adapter_version:'isolated-symbolic-2',status:'failed',error_code:failure.code,
    error_message:failure.detail || failure.code,isolation:{kind:'node_child_process',...resource}});
  result.sections.coverage.push({dimension:'bytecode',complete:false,reason:failure.code,discovered_selectors:selectors.length,
    affected_method_ids:affected,preserved:['disassembly','compiler_entries','successful_source_method_models'],...failure});
  result.sections.frontier.push({frontier_id:id(result.snapshot.code_hash,'sevm-child',failure.code),kind:'bytecode_engine',engine:'sevm',
    reason:failure.code,affected_method_ids:affected});
}
