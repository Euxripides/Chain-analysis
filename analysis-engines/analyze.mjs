import { emptyResult, finalize, hash, id } from './model.mjs';
import { analyzeSource } from './source.mjs';
import { analyzeBytecode } from './bytecode.mjs';
import { heimdall } from './external.mjs';
import { applyFundsRules } from './rules/funds.mjs';
import { applyLedgerRules } from './rules/ledger.mjs';

export function analyze(input) {
  if (!input?.snapshot || !/^0x(?:[0-9a-fA-F]{2}){1,49152}$/.test(input.bytecode || '')) throw new Error('INVALID_BYTECODE_INPUT');
  const result = emptyResult(input.snapshot);
  const started = performance.now();
  if(input.source) {
    const original=JSON.stringify(input.source);
    result.sections.artifacts.push({artifact_id:id('source-provider',input.snapshot.code_hash),kind:'source_provider_document',filename:'source-provider.json',media_type:'application/json',text:original,sha256:hash(original)});
  }
  if (input.source && (!input.engines || input.engines.includes('solc-ast'))) {
    const sourceResult = emptyResult(input.snapshot);
    try { analyzeSource(sourceResult, input.source, input.bytecode); for (const key of Object.keys(result.sections)) result.sections[key].push(...sourceResult.sections[key]); }
    catch (error) {
      result.sections.engines.push({ engine: 'solc-ast', status: 'failed', error_code: String(error.message).split(':')[0], error_message: String(error.message).slice(0, 500) });
      result.sections.coverage.push({ dimension: 'source', complete: false, reason: String(error.message).split(':')[0] });
      if (/MISMATCH|NOT_VERIFIED/.test(error.message)) result.sections.conflicts.push({ conflict_id: id(input.snapshot.code_hash, 'source_mismatch'), kind: 'source_mismatch', reason: String(error.message).split(':')[0] });
    }
  } else result.sections.coverage.push({ dimension: 'source', complete: false, reason: 'verified_source_unavailable' });
  analyzeBytecode(result, input.bytecode, input.signatures || []);
  if (input.engines?.includes('heimdall')) heimdall(result, input.bytecode);
  result.sections.contracts.push({ ...input.snapshot, bytecode_bytes: (input.bytecode.length - 2) / 2,
    method_count: result.sections.methods.length, source_status: result.sections.engines.some(e=>e.engine==='solc-ast'&&e.status==='completed') ? 'verified_and_recompiled' : 'bytecode_only' });
  result.sections.artifacts.push({ artifact_id: id('bytecode', input.snapshot.code_hash), kind: 'bytecode', filename: 'runtime-bytecode.hex', media_type: 'text/plain', text: input.bytecode, sha256: hash(input.bytecode) });
  result.metrics = { duration_ms: Math.round(performance.now() - started), peak_rss_bytes: process.resourceUsage().maxRSS * 1024,
    cpu_user_us: process.resourceUsage().userCPUTime, cpu_system_us: process.resourceUsage().systemCPUTime, input_bytes: Buffer.byteLength(JSON.stringify(input)) };
  applyLedgerRules(result);
  finalize(result);
  applyFundsRules(result);
  return result;
}
