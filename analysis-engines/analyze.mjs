import { emptyResult, finalize, hash, id } from './model.mjs';
import { analyzeSource } from './source.mjs';
import { analyzeBytecode } from './bytecode.mjs';
import { heimdall } from './external.mjs';
import { applyFundsRules } from './rules/funds.mjs';
import { applyLedgerRules } from './rules/ledger.mjs';
import { analyzeSlither } from './adapters/python.mjs';
import { analyzeVyperSource } from './source-vyper.mjs';
import { applyDataflow } from './ir/dataflow.mjs';
import { analyzeGigahorse } from './adapters/gigahorse.mjs';
import { applyBusinessRules } from './rules/business.mjs';
import { normalizeGigahorseDataflow } from './ir/gigahorse-dataflow.mjs';
import { applyPropertyRules } from './properties/index.mjs';
import { normalizeSubmittedInput, inputOnlyResult } from './input.mjs';
import { attachConclusionDependencies } from './conclusion-dependencies.mjs';
import {analyzeCallAbi}from'./ir/call-abi.mjs';

export function analyze(input, options = {}) {
  const entered = performance.now();
  const remaining = Number(process.env.CONTRACT_ANALYSIS_REMAINING_MS);
  const deadline = entered + (Number.isFinite(remaining) && remaining > 0 ? Math.min(remaining,300000) : 300000);
  const normalized=normalizeSubmittedInput(input);
  if(normalized.terminal)return attachConclusionDependencies(inputOnlyResult(normalized));
  input=normalized.input;
  if (!input?.snapshot || !/^0x(?:[0-9a-fA-F]{2}){1,49152}$/.test(input.bytecode || '')) throw new Error('INVALID_BYTECODE_INPUT');
  const result = emptyResult(input.snapshot);
  result.sections.artifacts.push(...normalized.artifacts);
  result.sections.coverage.push(...normalized.gaps);
  const started = performance.now();
  if(input.source) {
    const original=JSON.stringify(input.source);
    result.sections.artifacts.push({artifact_id:id('source-provider',input.snapshot.code_hash),kind:input.source.origin==='submitted_source'?'submitted_compilation':'source_provider_document',filename:input.source.origin==='submitted_source'?'submitted-compilation.json':'source-provider.json',media_type:'application/json',text:original,sha256:hash(original)});
  }
  if (input.source && (!input.engines || input.engines.includes('solc-ast') || input.engines.includes('vyper-ast'))) {
    const sourceResult = emptyResult(input.snapshot);
    try {
      if (input.source.stdJsonInput?.language === 'Vyper') analyzeVyperSource(sourceResult, input.source, input.bytecode);
      else analyzeSource(sourceResult, input.source, input.bytecode, {...options.source,deadline:deadline-20000});
      for (const key of Object.keys(sourceResult.sections)) { result.sections[key] ||= []; result.sections[key].push(...sourceResult.sections[key]); }
    }
    catch (error) {
      result.sections.engines.push({ engine: input.source.stdJsonInput?.language === 'Vyper' ? 'vyper-ast' : 'solc-ast', status: 'failed', error_code: String(error.message).split(':')[0], error_message: String(error.message).slice(0, 500) });
      result.sections.coverage.push({ dimension: 'source', complete: false, reason: String(error.message).split(':')[0] });
      if (/MISMATCH|NOT_VERIFIED/.test(error.message)) result.sections.conflicts.push({ conflict_id: id(input.snapshot.code_hash, 'source_mismatch'), kind: 'source_mismatch', reason: String(error.message).split(':')[0] });
    }
  } else result.sections.coverage.push({ dimension: 'source', complete: false, reason: 'verified_source_unavailable' });
  analyzeBytecode(result, input.bytecode, input.signatures || [], {...options.sevm,
    timeoutMs:Math.min(options.sevm?.timeoutMs ?? 60000,deadline-performance.now()-20000)});
  if (input.engines?.includes('slither')) {
    if (input.source && result.sections.engines.some(e => ['solc-ast','vyper-ast'].includes(e.engine) && e.status === 'completed')) analyzeSlither(result, input.source);
    else result.sections.engines.push({ engine: 'slither', status: 'skipped', reason: 'verified_recompiled_source_required' });
  }
  const unboundTemplate=input.snapshot.code_identity?.unbound_immutable_ranges?.length||input.snapshot.code_identity?.unbound_library_ranges?.length;
  if (input.engines?.includes('heimdall')&&!unboundTemplate) heimdall(result, input.bytecode);
  if (input.engines?.includes('gigahorse')&&!unboundTemplate) analyzeGigahorse(result, input.bytecode);
  if(unboundTemplate)for(const engine of ['heimdall','gigahorse'].filter(e=>input.engines?.includes(e)))result.sections.engines.push({engine,status:'skipped',reason:'compiled_template_has_unbound_deployment_ranges'});
  normalizeGigahorseDataflow(result,{bytecode:input.bytecode});
  analyzeCallAbi(result);
  const sourceEngine=result.sections.engines.find(e=>['solc-ast','vyper-ast'].includes(e.engine)&&['completed','partial'].includes(e.status));
  result.sections.contracts.push({ ...input.snapshot, bytecode_bytes: (input.bytecode.length - 2) / 2,
    method_count: result.sections.methods.length, source_status: sourceEngine ?
      (input.source?.origin==='submitted_source'?(input.snapshot.identity_kind==='code_only'?'submitted_source_compiled':'submitted_source_matches_snapshot'):'verified_and_recompiled')+(sourceEngine.status==='partial'?'_partial':'') : 'bytecode_only' });
  result.sections.artifacts.push({ artifact_id: id('bytecode', input.snapshot.code_hash), kind: 'bytecode', filename: 'runtime-bytecode.hex', media_type: 'text/plain', text: input.bytecode, sha256: hash(input.bytecode) });
  result.metrics = { duration_ms: Math.round(performance.now() - started), peak_rss_bytes: process.resourceUsage().maxRSS * 1024,
    cpu_user_us: process.resourceUsage().userCPUTime, cpu_system_us: process.resourceUsage().systemCPUTime, input_bytes: Buffer.byteLength(JSON.stringify(input)) };
  applyLedgerRules(result);
  if(input.snapshot.vm_family==='tron_tvm')for(const fund of result.sections.funds)if(fund.asset==='native')fund.unit='sun';
  finalize(result);
  applyDataflow(result);
  applyFundsRules(result);
  applyBusinessRules(result);
  applyPropertyRules(result);
  result.metrics.duration_ms=Math.round(performance.now()-started);
  result.metrics.peak_rss_bytes=process.resourceUsage().maxRSS*1024;
  return attachConclusionDependencies(result);
}
