import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { analyze } from './analyze.mjs';
import { emptyResult, id, hash, makeMethod, evidence, finalize } from './model.mjs';

import { addSolanaTransactions, addTronTransactions } from './adapters/program-transactions.mjs';
import { addProgramSimulation } from './adapters/svm.mjs';

export function analyzeProgram(input) {
  const started=performance.now(),program=input.program;
  if(!program||program.vm_family!==input.snapshot?.vm_family)throw new Error('PROGRAM_IDENTITY_INVALID');
  if(program.vm_family==='tron_tvm') {
    const result=analyze({...input,engines:['whatsabi','sevm','heimdall','gigahorse']});
    for(const fund of result.sections.funds)if(fund.asset==='native')fund.unit='sun';
    addTronTransactions(result,program.transactions);
    for(const expr of result.sections.expressions)expr.vm_semantics='tvm_evm_compatible_subset';
    result.sections.coverage.push({dimension:'tvm_execution_rules',complete:false,reason:'native_tvm_state_replay_not_available'});
    result.sections.contracts.forEach(c=>{c.vm_family='tron_tvm';c.bytecode_analysis_scope='evm_compatible_subset'});
    return result;
  }
  if(program.vm_family!=='solana_sbf')throw new Error('PROGRAM_VM_UNSUPPORTED');
  const elf=Buffer.from(program.elf_base64||'','base64');
  const sha=createHash('sha256').update(elf).digest('hex');
  if(elf.length>16<<20||input.snapshot.code_sha256!==sha||input.snapshot.code_hash!=='0x'+sha)throw new Error('PROGRAM_CODE_HASH_MISMATCH');
  const result=emptyResult(input.snapshot);
  const run=spawnSync(process.env.SBF_ANALYSIS_BINARY||'/usr/local/bin/contract-sbf-analysis',[],{
    input:JSON.stringify({elf_base64:program.elf_base64}),encoding:'utf8',timeout:90000,maxBuffer:16<<20,
  });
  if(run.status!==0)throw new Error('SBF_ANALYSIS_FAILED:'+(run.stderr||run.error?.message||'').slice(-800));
  const native=JSON.parse(run.stdout);
  const artifact_id=id('sbf-disassembly',sha),text=native.disassembly;
  result.sections.artifacts.push({artifact_id,kind:'sbf_disassembly',filename:'program-disassembly.s',media_type:'text/plain',text,sha256:hash(text)});
  result.sections.artifacts.push({artifact_id:id('sbf-elf',sha),kind:'program_elf',filename:'program.so',media_type:'application/x-elf',content_base64:program.elf_base64,sha256:sha});
  const raw=JSON.stringify(native);
  result.sections.artifacts.push({artifact_id:id('sbf-ir',sha),kind:'sbf_analysis',filename:'program-analysis.json',media_type:'application/json',text:raw,sha256:hash(raw)});
  result.sections.ir=[];
  const functions=[...native.functions].sort((a,b)=>a.pc-b.pc);
  for(let index=0;index<functions.length;index++) {
    const fn=functions[index],next=functions[index+1]?.pc??Infinity;
    const method=makeMethod(input.snapshot,'sbf-function:'+fn.pc,'',fn.name);
    method.entry_kind=fn.pc===native.entrypoint?'program_entry':'sbf_internal_function';
    method.visibility=method.entry_kind==='program_entry'?'external':'internal';
    method.name_basis='elf_symbols_or_generated_labels';method.verified_signature=false;
    method.completeness={complete:false,basis:'sbf_disassembly',unresolved:[{code:'INSTRUCTION_ARGUMENT_AND_ACCOUNT_SEMANTICS_NOT_RECOVERED'}],execution_paths:'not_exhaustively_verified'};
    method.ir_ids=[];
    for(const insn of native.instructions.filter(i=>i.pc>=fn.pc&&i.pc<next)) {
      const eid=evidence(result,method,'sbf_instruction',{artifact_id,pc:insn.pc,byte_offset:insn.pc*8},insn.text);
      const step_id=id(method.method_id,'sbf-step',insn.pc),ir_id=id(method.method_id,'sbf-ir',insn.pc);
      result.sections.steps.push({step_id,method_id:method.method_id,order:method.step_ids.length,kind:'instruction',text:insn.text,conditions:[],evidence_ids:[eid]});
      result.sections.ir.push({ir_id,method_id:method.method_id,engine:'solana-sbpf',...insn,basis:'native_sbf_disassembly',evidence_ids:[eid]});
      method.step_ids.push(step_id);method.ir_ids.push(ir_id);
    }
    result.sections.methods.push(method);
  }
  result.sections.engines.push({engine:'solana-sbpf',version:native.version,source_commit:native.source_commit,adapter_version:'sbf-static-1',status:'completed',
    sbpf_version:native.sbpf_version,instructions:native.instructions.length,functions:native.functions.length});
  result.sections.contracts.push({...input.snapshot,vm_family:'solana_sbf',program_loader:input.snapshot.program_loader,bytecode_bytes:elf.length,
    source_status:'elf_only',method_count:result.sections.methods.length});
  const accountText=JSON.stringify(program.program_accounts);
  result.sections.artifacts.push({artifact_id:id('sbf-accounts',sha),kind:'program_accounts',filename:'program-accounts.json',media_type:'application/json',text:accountText,sha256:hash(accountText)});
  if(program.loader_state) {
    const evidence_id=id('program-loader-state',sha,input.snapshot.slot);
    result.sections.evidence.push({evidence_id,kind:'program_account_snapshot',code_hash:input.snapshot.code_hash,code_address:input.snapshot.code_address,
      location:{artifact_id:id('sbf-accounts',sha),json_path:'$.value[1].data'},text:JSON.stringify(program.loader_state)});
    result.sections.program_control=[{control_id:id(evidence_id,'control'),...program.loader_state,observed_slot:input.snapshot.slot,evidence_ids:[evidence_id],historical_controller_verified:false}];
  }
  result.sections.control_flow=(native.control_flow||[]).map(c=>({cfg_id:id(sha,'sbf-cfg',c.pc),engine:'solana-sbpf',block:c.pc,label:c.label,successors:c.successors,predecessors:c.predecessors,basis:'native_sbf_cfg',complete:false}));
  addSolanaTransactions(result,program.transactions);
  addProgramSimulation(result,program);
  result.sections.coverage.push({dimension:'program_structure',complete:false,reason:'disassembly_does_not_recover_all_instruction_business_semantics'});
  result.sections.coverage.push({dimension:'historical_replay',complete:false,reason:'historical_account_prestate_and_feature_set_unavailable'});
  result.metrics={duration_ms:Math.round(performance.now()-started),peak_rss_bytes:process.resourceUsage().maxRSS*1024};
  finalize(result);
  return result;
}
