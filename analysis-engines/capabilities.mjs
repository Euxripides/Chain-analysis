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
    source_return_analysis:{language:'Solidity',scope:'scalar_readonly_paths_with_bounded_execution_or_discharged_inductive_summary',max_paths:32,max_tuple_components:8,max_loop_iterations:16,max_aggregate_loop_steps:64,
      local_assignments:true,named_returns:true,early_returns:true,conditional_returns:true,tuple_destructuring:true,guard_prefix_modifiers:true,
      constant_bound_for_while_do_loops:true,entry_return_models:true,
      symbolic_loop_exit_paths:true,integer_range_loop_bounds:true,unbounded_loop_remainder:'saved_frontier_unless_all_induction_obligations_discharged',
      induction:{zero_based_monotone_uint_counter:true,iteration_independent_affine_locals:true,initialization:true,preservation:true,exit:true,ranking_function:true,
        counter_polynomials_max_degree:3,exact_scaled_power_sums:true,word_ring_projection_obligations:true,checked_intermediate_overflow_preserved:true,
        summary_requires_all_obligations:true,summary_execution:'for_and_while_loops_with_discharged_scalar_recurrences',gas_feasibility_proved:false},
      structured_yul:{word_bits:256,local_functions:true,scalar_tuples:true,if_switch:true,bounded_for:true,zero_divisor:'returns_zero',
        readonly_memory:'byte_ordered_mstore_mstore8_mload_with_complete_written_range',compiler_reserved_memory_alias:'explicit_gap',host_effects:'require_state_or_execution_model'},
      expression_local_assignments:true,prefix_and_postfix_local_updates:true,short_circuit_local_effects:true,conditional_local_effects:true,
      ordered_evaluations_preserved:true,unspecified_sibling_read_write_order:'explicit_gap',
      fixed_bytes_width_conversions:'right_padding_and_truncation_in_arguments_locals_returns_and_comparisons',conditional_value_branches:'explicit_compiler_ast_fields_with_lazy_guards',
      unsupported:['unproved_loop_remainders','unmodeled_assembly_memory_and_host_effects','storage_aliases','stateful_returns','dynamic_tuple_values','post_body_or_conditional_modifiers','unbound_dynamic_calls'],all_paths_proved:false},
    funds_control_analysis:{configuration_impacts:['amount_formula','recipient','sender','asset_contract','execution_condition','caller_authorization'],
      address_observation:'verified_layout_and_pinned_direct_slot',boolean_mapping_conditions:true,state_numeric_conditions:true,role_membership_enumerated:false,authorization_sufficiency_verified:false},
    assembly_state_analysis:{entry_memory:'symbolic_compiler_entry_memory_and_extent',byte_memory:true,overlapping_writes:true,unaligned_reads:true,
      persistent_storage:'shared_physical_storage_array_with_solidity',packed_slots:true,local_yul_functions:true,atomic_reverts:true,evm_return_halts_modifiers:true,
      mapping_storage:'mstore_keccak256_sstore_with_exact_word_provenance_and_canonical_keys',nested_mapping_struct_members:true,
      raw_hash_word_lengths:[32,64],narrow_external_words:'symbolic_high_bits_with_typed_low_bits',overlapping_copy_provenance:'snapshot_then_overlap_invalidation',
      transient_storage:'verified_cancun_or_later_rules',copy_max_bytes:256,max_paths:32,max_writes:64,unsupported_effects:'explicit_model_gaps'},
    dependency_analysis:{target_basis:['verified_source_literal','runtime_immutable_or_link','typed_pinned_state','dominating_local_alias','identity_address_or_contract_cast'],
      delegate_storage_context_preserved:true,dynamic_return_values_bound_to_execution:true,return_binding_scope:'exact_execution_frame_with_verified_output_schema',
      source_callsite_binding:'exact_pc_source_map_and_verified_complete_offline_replay',same_pc_repeated_invocations:true,native_payment_callsite_binding:true,
      generated_or_ambiguous_source:'unbound',internal_context_ambiguity:'callsite_only_no_expression_substitution'},
    opcode_witness_analysis:{scope:'one_exact_transaction_execution',requires_complete_receipt_gas_state_environment_and_call_tree_match:true,
      byte_memory_reconstruction:true,storage_journal_and_ancestor_reverts:true,max_steps:20000,max_rpc_bytes:8388608,max_memory_bytes_per_frame:65536},
    property_analysis:{solver:python.z3_shared_library?'z3_shared_library':'unavailable',max_properties:24,timeout_ms_per_check:300,
      properties:['successful_guard_requires_caller_equality','successful_guard_requires_state_predicate','returned_components_conserve_input'],statuses:['proved_under_declared_model','counterexample','infeasible_assumptions','unknown','unsupported','solver_unavailable'],
      counterexamples_and_smt_artifacts:true,complete_authorization_proved:false,complete_protocol_economics_proved:false},
    state_model_verification:{solver:python.z3_shared_library?'z3_shared_library':'unavailable',max_obligations_per_group:48,timeout_ms_per_check:300,
      builtin_identity:'compiler_AST_declaration_identity_with_shadowed_names_preserved',
      authorization_models:['owner_identity_integrity','access_control_admin_integrity','erc2612_permit_integrity'],all_mutating_entry_coverage_required:true,
      signature_entry_scope:'all_successful_paths_of_identified_permit_entry',signature_crypto_axioms_added:[],
      signature_state_analysis:{abi_scalar_encodings:['abi.encode','abi.encodePacked'],known_finite_dynamic_bytes:true,max_bytes:2048,
        exact_concrete_ecdsa_recovery:true,symbolic_ecdsa_recovery:'conservative_precompile_function_with_invalid_input_zero',
        high_s_signatures:'accepted_by_evm_precompile',domain_nonce_deadline_and_allowance_obligations:true,
        hash_collision_resistance_and_signature_unforgeability_proved:false,unknown_dynamic_calldata_and_contract_signatures:'explicit_model_gaps'},
      protocol_models:['erc20_ledger_conservation','erc20_source_initialization'],source_creation:'zero_storage_C3_constructors_base_arguments_and_compiler_specific_initializer_order',
      inherited_creation_compilers:['0.8.30'],creation_immutables:'separate_values_bound_to_verified_runtime_words',
      historical_creation_identity:'backend_exact_initcode_static_ABI_CREATE_address_runtime_code_and_complete_isolated_replay',
      historical_creation_identity_verified:false,raw_unclassified_storage_writes_block_ledger_and_role_closure:true,
      closure_requires_all_obligations_and_no_gaps:true,counterexample_entry_storage_preserved:true,
      remaining:['factory_creation_proxy_initialization_upgrades_and_history_continuity','cached_domain_history_contract_signatures_multisig_and_threshold','external_asset_and_reentrancy','protocol_specific_fees_prices_rounding_and_liquidation'],
      whole_authorization_proved:false,whole_protocol_economics_proved:false},
    protocol_analysis:{erc4626:'source_derived_method_formulas_and_preview_execution_obligations',erc20:'all_mutating_entry_ledger_delta_preservation_and_source_initialization',
      economic_models_require_state_and_execution:true,all_protocol_economics_verified:false},
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
