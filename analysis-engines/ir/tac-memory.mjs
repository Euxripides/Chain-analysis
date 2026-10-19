import{id,unique}from'../model.mjs';
import{ByteMemory,joinMemory,memorySource,MemoryFault}from'./memory.mjs';
import{buildTacOrder}from'./tac-order.mjs';

export function analyzeTacMemory(result,method,rows,{derive,onLoad,bytecode,deadline=Infinity}={}) {
 for(const name of['memory_versions','memory_reads','call_input_bindings','memory_frontier'])result.sections[name]||=[];
 const graph=buildTacOrder(rows,(result.sections.control_flow||[]).filter(r=>r.method_id===method.method_id&&r.engine==='gigahorse'),method.gigahorse_function_entries||[]);
 const frame_id=id(method.method_id,'gigahorse-frame'),zeroEntry=method.gigahorse_function_entries?.length===1&&method.gigahorse_function_entries[0]==='0x0'&&rows.some(r=>r.original_statement==='0x0');
 const initial=new ByteMemory({frameId:frame_id,entry:zeroEntry?'frame_start':'solidity_assembly_entry',byteBudget:4096,historyBudget:256,caseBudget:8});
 const versions=new Set(),after=new Map(),entrySet=new Set(graph.entryStatements),start=performance.now();let visited=0;
 const context=r=>({ir_id:r.ir_id,statement:r.statement,evidence_ids:r.evidence_ids||[]});
 const save=memory=>{for(const record of memory.transition_records)if(!versions.has(record.memory_version_id)){
  if(versions.size>=12000)throw new MemoryFault('TAC_MEMORY_VERSION_BUDGET');versions.add(record.memory_version_id);
  result.sections.memory_versions.push({...record,method_id:method.method_id,code_hash:result.snapshot.code_hash,engine:'gigahorse',frame_basis:'symbolic_method_analysis_context_not_observed_EVM_frame',evidence_ids:record.context?.evidence_ids||[]});
 }return memory;};
 save(initial);
 const front=(r,reason)=>{const row={memory_frontier_id:id(method.method_id,'tac-memory-frontier',r?.ir_id||'',reason),method_id:method.method_id,engine:'gigahorse',reason,
  statement:r?.statement||null,ir_id:r?.ir_id||null,evidence_ids:r?.evidence_ids||[],all_execution_paths_verified:false};
  if(!result.sections.memory_frontier.some(f=>f.memory_frontier_id===row.memory_frontier_id))result.sections.memory_frontier.push(row);
  return row;};
 for(const gap of graph.gaps)front(null,gap.code);
 const incomingEdges=new Map();for(const edge of graph.edges){const list=incomingEdges.get(edge.to)||[];list.push(edge);incomingEdges.set(edge.to,list);}
 const badBlocks=new Set(graph.gaps.filter(g=>g.code==='TAC_BLOCK_ORDER_AMBIGUOUS').map(g=>g.block));
 const isCall=r=>['CALL','CALLCODE','DELEGATECALL','STATICCALL'].includes(r.op);
 const bindingFront=(r,reason)=>{if(isCall(r))result.sections.call_input_bindings.push({call_input_binding_id:id(r.ir_id,'memory-input-frontier'),method_id:method.method_id,ir_id:r.ir_id,
  statement:r.statement,original_statement:r.original_statement,call_type:r.op,status:'partial',unresolved:[reason],evidence_ids:r.evidence_ids||[],all_execution_paths_verified:false});};
 const edgeConditions=edge=>{if(!edge.condition)return[];const c=edge.condition;
  if(c.unresolved||c.expected===null)return[{expression:null,condition_variable:c.variable,expected:null,statement:c.statement,unresolved:c.unresolved}];
  const value=derive(c.variable);return[{expression:'('+value.text+') != 0',expected:c.expected,statement:c.statement,evidence_ids:value.evidence_ids}];};
 const value=operand=>operand?.constant_hex!==undefined?BigInt(operand.constant_hex):operand?.constant_value!==undefined?BigInt(operand.constant_value):{expression:operand?.text||'tac_value_unavailable',evidence_ids:operand?.evidence_ids||[]};
 const address=operand=>operand?.constant_hex!==undefined?BigInt(operand.constant_hex):operand?.constant_value!==undefined?BigInt(operand.constant_value):{expression:operand?.text||'tac_offset_unavailable'};
 const emptyReturn=memorySource('returndata',{sourceId:id(frame_id,'initial-return-buffer'),frameId:frame_id,...(zeroEntry?{bytes:[]}:{})});
 const source=(kind,r)=>memorySource(kind,{sourceId:id(frame_id,kind,kind==='returndata'?r.statement:'entry'),frameId:frame_id,
  ...(kind==='code'&&bytecode?{bytes:Buffer.from(bytecode.replace(/^0x/,''),'hex')}:{})});
 for(const statement of graph.ordered){const r=graph.byStatement.get(statement);
  if(++visited>10000||performance.now()-start>2000||performance.now()>deadline){front(r,'TAC_MEMORY_ANALYSIS_BUDGET');break;}
  try{
   const incoming=(incomingEdges.get(statement)||[]).filter(e=>graph.reachable.has(e.from));
   if(incoming.some(e=>!after.has(e.from))){front(r,'TAC_MEMORY_PREDECESSOR_UNAVAILABLE');bindingFront(r,'TAC_MEMORY_PREDECESSOR_UNAVAILABLE');continue;}
   const states=incoming.map(edge=>({memory:after.get(edge.from).memory,conditions:edgeConditions(edge)}));
   if(entrySet.has(statement))states.push({memory:initial,conditions:[]});
   if(!states.length){front(r,'TAC_MEMORY_ENTRY_UNAVAILABLE');continue;}
   let memory=states.length===1&&!states[0].conditions.length?states[0].memory:save(joinMemory(states));
   const returns=incoming.map(e=>after.get(e.from).return_source),return_source=returns.length&&returns.every(s=>s.source_id===returns[0].source_id)?returns[0]:incoming.length?source('returndata',{statement:'merge-'+statement}):emptyReturn;
   let nextReturn=return_source;const operands=(r.reads||[]).map(v=>derive(v)),ctx=context(r);
   if(badBlocks.has(r.block)){memory=save(memory.invalidate(null,null,'TAC_BLOCK_ORDER_AMBIGUOUS',ctx));front(r,'TAC_BLOCK_ORDER_AMBIGUOUS');bindingFront(r,'TAC_BLOCK_ORDER_AMBIGUOUS');}
   else if(['MSTORE','MSTORE8'].includes(r.op))memory=save(memory[r.op==='MSTORE'?'mstore':'mstore8'](address(operands[0]),value(operands[1]),ctx));
   else if(r.op==='MLOAD'){
    const read=memory.loadWord(address(operands[0]),ctx);memory=save(read.memory);
    const memory_read_id=id(r.ir_id,'word-read',read.memory_version_id);onLoad(r,read,memory_read_id);
    result.sections.memory_reads.push({memory_read_id,method_id:method.method_id,ir_id:r.ir_id,statement:r.statement,frame_id,memory_version_id:read.memory_version_id,
     offset_expression:operands[0]?.text,byte_length:'32',status:read.status,cases:read.cases,equivalent_cases:read.equivalent_cases||[],unresolved:read.unresolved,evidence_ids:r.evidence_ids||[],basis:'versioned_tac_memory',all_execution_paths_verified:false});
   }else if(r.op==='MCOPY')memory=save(memory.mcopy(address(operands[0]),address(operands[1]),address(operands[2]),ctx));
   else if(['CALLDATACOPY','CODECOPY','RETURNDATACOPY','EXTCODECOPY'].includes(r.op)){
    if(r.op==='EXTCODECOPY')memory=save(memory.invalidate(address(operands[1]),address(operands[3]),'EXTERNAL_CODE_IDENTITY_UNBOUND',ctx));
    else memory=save(memory.copyFrom(address(operands[0]),address(operands[1]),address(operands[2]),r.op==='RETURNDATACOPY'?return_source:source(r.op==='CODECOPY'?'code':'calldata',r),ctx));
   }else if(isCall(r)){
    const valueCall=['CALL','CALLCODE'].includes(r.op),offset=valueCall?3:2,length=offset+1,read=memory.read(address(operands[offset]),address(operands[length]));
    const binding={call_input_binding_id:id(r.ir_id,'memory-input',memory.version_id),method_id:method.method_id,ir_id:r.ir_id,statement:r.statement,original_statement:r.original_statement,
     frame_id,frame_basis:'symbolic_method_analysis_context_not_observed_EVM_frame',memory_version_id:memory.version_id,call_type:r.op,target_expression:operands[1]?.text,value_expression:valueCall?operands[2]?.text:'0',
     input_offset_expression:operands[offset]?.text,input_length_expression:operands[length]?.text,status:read.status,cases:read.cases,equivalent_cases:read.equivalent_cases||[],
     operand_derivations:operands.map(p=>({expression:p.text,unresolved:p.unresolved})),unresolved:unique([...read.unresolved,...memory.unresolved]),
     settlement:'call_encoding_only_execution_success_and_target_semantics_unverified',evidence_ids:unique([...(r.evidence_ids||[]),...operands.flatMap(p=>p.evidence_ids)]),all_execution_paths_verified:false};
    result.sections.call_input_bindings.push(binding);nextReturn=source('returndata',r);
    memory=save(memory.invalidate(address(operands[length+1]),address(operands[length+2]),'CALL_OUTPUT_RETURN_SIZE_UNRESOLVED',{...ctx,call_input_binding_id:binding.call_input_binding_id}));
   }else if(['CALLPRIVATE','CALLPRIVATESTATIC','CREATE','CREATE2'].includes(r.op)){
    if(r.op.startsWith('CALLPRIVATE'))memory=save(memory.invalidate(null,null,'TAC_INTERNAL_MEMORY_SUMMARY_UNAVAILABLE',ctx));nextReturn=source('returndata',r);
   }
   after.set(statement,{memory,return_source:nextReturn});
  }catch(error){front(r,error.code||'TAC_MEMORY_ANALYSIS_FAILED');bindingFront(r,error.code||'TAC_MEMORY_ANALYSIS_FAILED');}
 }
 for(const statement of graph.reachable)if(!after.has(statement))bindingFront(graph.byStatement.get(statement),graph.frontier.includes(statement)?'TAC_MEMORY_CFG_CYCLE_FRONTIER':'TAC_MEMORY_ANALYSIS_FRONTIER');
 method.memory_read_ids=unique(result.sections.memory_reads.filter(r=>r.method_id===method.method_id).map(r=>r.memory_read_id));
 method.call_input_binding_ids=unique(result.sections.call_input_bindings.filter(r=>r.method_id===method.method_id).map(r=>r.call_input_binding_id));
 result.sections.coverage.push({dimension:'tac_byte_memory',method_id:method.method_id,complete:false,represented_statements:after.size,reachable_statements:graph.reachable.size,
  cyclic_frontier_statements:graph.frontier.length,reason:'represented_CFG_only_external_entry_context_and_execution_paths_not_exhaustively_verified'});
 return{graph,after};
}
