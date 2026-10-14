import { existsSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { emptyResult } from './model.mjs';
import { pythonOperation } from './adapters/python.mjs';

export function capabilities(snapshot = {}) {
  const result = emptyResult(snapshot);
  let python = {}, error = null;
  try { python = pythonOperation({ operation: 'versions' }, 10000); }
  catch (e) { error = String(e.message).slice(0, 300); }
  const gigahorse = existsSync((process.env.GIGAHORSE_ROOT || '/opt/gigahorse/gigahorse-toolchain')+'/cache/main.dl_compiled');
  const programs = [
    { vm_family: 'evm', code_analysis: 'available', source_languages: ['Solidity', ...(python.vyper ? ['Vyper'] : [])],
      exact_historical_replay: 'ethereum_rules_with_complete_witness', unsupported_state_status: 'explicit_witness_gaps' },
    { vm_family: 'tron_tvm', code_analysis: 'not_certified', transaction_analysis: 'not_certified', exact_historical_replay: 'not_certified' },
    { vm_family: 'solana_sbf', code_analysis: 'not_certified', transaction_analysis: 'not_certified', exact_historical_replay: 'not_certified' },
  ];
  result.sections.capabilities = [{ schema_version: 'contract-capabilities-v1', programs,
    engines: [{ name: 'solc-ast', status: 'available' }, { name: 'whatsabi', status: 'available' },
      { name: 'sevm', status: 'available' }, { name: 'heimdall', status: existsSync(process.env.HEIMDALL_BINARY || '/usr/local/bin/heimdall') ? 'available' : 'unavailable' },
      { name: 'slither', status: python['slither-analyzer'] ? 'available' : 'unavailable', version: python['slither-analyzer'] || null },
      { name: 'vyper-ast', status: python.vyper ? 'available' : 'unavailable', compiler_versions: python.vyper ? [python.vyper] : [] },
      { name: 'gigahorse', status: gigahorse ? 'available' : 'unavailable',source_commit:'1b06cef82f0a29790e87f576eba6d6d793170d70',scope:'tac_cfg_and_bounded_symbolic_word_dataflow',all_paths_verified:false }],
    verification: { method_parse_is_path_proof: false, historical_coverage_required: true, protocol_adapters_require_identity_and_state: true },
    ...(error ? { optional_engine_probe_error: error } : {}) }];
  result.metrics = {};
  return result;
}
