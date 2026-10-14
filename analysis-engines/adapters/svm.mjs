import { spawnSync } from 'node:child_process';
import { hash, id } from '../model.mjs';

export function simulateSVM(environment) {
  const run=spawnSync(process.env.SVM_SIMULATION_BINARY||'/usr/local/bin/contract-svm-simulate',[],{
    input:JSON.stringify(environment),encoding:'utf8',timeout:90000,maxBuffer:24<<20,env:{...process.env,RUST_LOG:'error'},
  });
  if(run.status!==0)throw new Error('SVM_SIMULATION_FAILED:'+(run.stderr||run.error?.message||'').slice(-1000));
  return JSON.parse(run.stdout);
}
export function addProgramSimulation(result,program) {
  if(!program.simulation)return;
  const started=performance.now(),engine={engine:'mollusk',source_commit:'bd75dd7937a8e51fa3b655a39b76a9c6170f9d2c',adapter_version:'local-instruction-1'};
  try {
    const simulation=program.simulation,environment={mode:'local_instruction_simulation',accounts:simulation.accounts,instructions:simulation.instructions,
      programs:[{program_id:result.snapshot.code_address,loader:result.snapshot.program_loader,elf_base64:program.elf_base64},...(simulation.dependencies||[])]};
    const output=simulateSVM(environment),text=JSON.stringify(output),artifact_id=id('svm-result',hash(environment));
    result.sections.artifacts.push({artifact_id,kind:'local_svm_simulation',filename:'local-svm-simulation.json',media_type:'application/json',text,sha256:hash(text)});
    const evidence_id=id(artifact_id,'evidence');
    result.sections.execution_evidence||=[];result.sections.execution_evidence.push({evidence_id,kind:'local_svm_simulation',artifact_id});
    result.sections.local_simulations=[{simulation_id:id(artifact_id,'simulation'),engine:'mollusk',success:output.success,program_result:output.program_result,
      compute_units_consumed:output.compute_units_consumed,environment:output.environment,environment_sha256:hash(environment),
      signer_basis:'declared_simulation_permissions',state_basis:'caller_supplied_accounts',transaction_atomicity:'independent_instruction_sequence',network_fee_included:false,
      historical_transaction_replay_verified:false,evidence_ids:[evidence_id]}];
    result.sections.simulation_accounts=output.accounts.map(a=>({change_id:id(artifact_id,a.address),...a,basis:'local_simulator_result',historical_state_verified:false,evidence_ids:[evidence_id]}));
    Object.assign(engine,{status:'completed',version:output.version,artifact_id,execution_result:output.success?'success':'program_error'});
  }catch(error){Object.assign(engine,{status:'failed',error_code:'SVM_SIMULATION_FAILED',error_message:String(error.message).slice(-1000)});
    result.sections.coverage.push({dimension:'local_svm_simulation',complete:false,reason:engine.error_message})}
  engine.duration_ms=Math.round(performance.now()-started);result.sections.engines.push(engine);
}
