import { existsSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { emptyResult, RULE_VERSION } from './model.mjs';
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
    semantic_rules_version:RULE_VERSION,
    source_return_analysis:{language:'Solidity',scope:'bounded_scalar_and_scalar_tuple_readonly_return_paths',max_paths:32,max_tuple_components:8,max_loop_iterations:16,max_aggregate_loop_steps:64,
      local_assignments:true,named_returns:true,early_returns:true,conditional_returns:true,tuple_destructuring:true,guard_prefix_modifiers:true,
      constant_bound_for_while_do_loops:true,entry_return_models:true,
      unsupported:['dynamic_loop_bounds','assembly','storage_aliases','stateful_returns','dynamic_tuple_values','post_body_or_conditional_modifiers','dynamic_calls'],all_paths_proved:false},
    funds_control_analysis:{configuration_impacts:['amount_formula','recipient','sender','asset_contract','execution_condition','caller_authorization'],
      address_observation:'verified_layout_and_pinned_direct_slot',boolean_mapping_conditions:true,state_numeric_conditions:true,role_membership_enumerated:false,authorization_sufficiency_verified:false},
    dependency_analysis:{target_basis:['verified_source_literal','runtime_immutable_or_link','typed_pinned_state','dominating_local_alias','identity_address_or_contract_cast'],
      delegate_storage_context_preserved:true,dynamic_return_values_bound_to_execution:false},
    protocol_analysis:{erc4626:'source_derived_method_formulas_and_preview_execution_obligations',economic_models_require_state_and_execution:true,all_protocol_economics_verified:false},
    calldata_analysis:{source_encodings:['abi.encodeCall','abi.encodeWithSelector','abi.encodeWithSignature'],transfer_selectors_require_static_abi_words:true,unverified_asset_units:'asset_quantity_or_token_id'},
    source_verification:{compiler_auxdata_tail:'recompiled_range_and_valid_solc_cbor_with_unchanged_code_prefix'},
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
