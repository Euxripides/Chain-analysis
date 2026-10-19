import { id, unique } from '../model.mjs';
import {evmWord,WORD_ARITIES}from'./evm-word.mjs';
import {analyzeTacMemory}from'./tac-memory.mjs';

export function normalizeGigahorseDataflow(result,{bytecode}={}) {
  result.sections.ir_dataflow||=[];result.sections.ir_effects||=[];
  const memoryDeadline=performance.now()+15000;
  const binary={ADD:'+',SUB:'-',MUL:'*',DIV:'/',SDIV:'signed_div',MOD:'%',SMOD:'signed_mod',LT:'<',GT:'>',SLT:'signed_lt',SGT:'signed_gt',EQ:'==',AND:'&',OR:'|',XOR:'^',SHL:'shift_left',SHR:'shift_right',SAR:'signed_shift_right'};
  for(const method of result.sections.methods) {
    const rows=(result.sections.ir||[]).filter(r=>r.method_id===method.method_id&&r.engine==='gigahorse');if(!rows.length)continue;
    const definitions=new Map(),constants=new Map();
    for(const r of rows){for(const v of r.definitions||[]){const list=definitions.get(v)||[];list.push(r);definitions.set(v,list)}
      for(const c of r.constants||[]){const values=constants.get(c.variable)||new Set();values.add(c.value);constants.set(c.variable,values)}
      for(const c of[...(r.constant_candidates||[]),...(r.constant_definitions||[])]){const values=constants.get(c.variable)||new Set();for(const v of c.values)values.add(v);constants.set(c.variable,values)}}
    const memo=new Map(),memoryLoads=new Map();let nodes=0;
    const derive=(variable,seen=new Set())=>{
      if(memo.has(variable))return memo.get(variable);
      const own={text:variable,unresolved:[],evidence_ids:[]};
      if(seen.has(variable)||seen.size>32||++nodes>10000)return {...own,unresolved:['TAC_DATAFLOW_CYCLE_OR_BUDGET']};
      const fixed=constants.get(variable);
      if(fixed?.size===1){const value=[...fixed][0];if(/^0x[\da-f]+$/i.test(value))return {text:BigInt(value).toString(),constant_hex:value,unresolved:[],evidence_ids:[]}}
      if(fixed?.size>1)return {...own,unresolved:['TAC_CONSTANT_VALUES_AMBIGUOUS']};
      const list=definitions.get(variable)||[];
      if(list.length!==1)return {...own,unresolved:[list.length?'TAC_DEFINITIONS_AMBIGUOUS':'TAC_ARGUMENT_OR_DEFINITION_UNAVAILABLE']};
      const r=list[0],next=new Set([...seen,variable]),args=(r.reads||[]).map(v=>derive(v,next));
      const value={...own,evidence_ids:unique([...r.evidence_ids,...args.flatMap(a=>a.evidence_ids)]),unresolved:unique(args.flatMap(a=>a.unresolved))};
      const environments={CALLER:'msg.sender',ORIGIN:'tx.origin',ADDRESS:'address(this)',CALLVALUE:'msg.value',TIMESTAMP:'block.timestamp',NUMBER:'block.number',SELFBALANCE:'address(this).balance'};
      if(WORD_ARITIES[r.op.toLowerCase()]===args.length&&args.every(a=>a.constant_hex!==undefined||a.constant_value!==undefined)){
        const n=evmWord(r.op.toLowerCase(),args.map(a=>a.constant_hex??a.constant_value));value.text=n.toString();value.constant_value=n.toString();value.unresolved=[];
      }
      else if(environments[r.op])value.text=environments[r.op];
      else if(binary[r.op]&&args.length===2)value.text=['ADD','SUB','MUL','DIV','MOD','LT','GT','EQ','AND','OR','XOR'].includes(r.op)?`(${args[0].text} ${binary[r.op]} ${args[1].text})`:`${binary[r.op]}(${args.map(a=>a.text).join(', ')})`;
      else if(r.op==='ISZERO'&&args.length===1)value.text=`(${args[0].text} == 0)`;
      else if(r.op==='NOT'&&args.length===1)value.text=`(~${args[0].text})`;
      else if(['CONST','COPY'].includes(r.op)&&args.length===1)value.text=args[0].text;
      else if(r.op==='SLOAD'&&args.length===1)value.text=`storage[${args[0].text}]`;
      else if(r.op==='CALLDATALOAD'&&args.length===1){value.text=`calldataload(${args[0].text})`;value.unresolved.push('CALLDATA_ARGUMENT_TYPE_UNVERIFIED')}
      else if(r.op==='MLOAD'&&args.length===1){
        const load=memoryLoads.get(r.statement);
        if(load?.cases.length===1&&load.cases[0].expression){value.text=load.cases[0].expression;value.memory_read_id=load.memory_read_id;
          if(load.cases[0].value!==null)value.constant_value=load.cases[0].value;value.unresolved=unique([...value.unresolved,...load.unresolved]);}
        else{value.text=`memory[${args[0].text}]`;value.unresolved.push(...(load?.unresolved||['MEMORY_VERSION_NOT_AVAILABLE']),...(load?.cases.length>1?['MEMORY_CONDITIONAL_WORD']:[]));}
      }
      else{value.text=`${r.op.toLowerCase()}(${args.map(a=>a.text).join(', ')})`;value.unresolved.push('TAC_OPERATOR_OR_CONTROL_MERGE_UNRESOLVED')}
      if(value.text.length>8192){value.text=variable;value.unresolved.push('TAC_EXPRESSION_SIZE_BUDGET')}
      value.unresolved=unique(value.unresolved);memo.set(variable,value);return value;
    };
    analyzeTacMemory(result,method,rows,{derive,bytecode,deadline:memoryDeadline,onLoad:(r,read,memory_read_id)=>{memoryLoads.set(r.statement,{...read,memory_read_id});memo.clear();}});
    const memoryBindings=new Map((result.sections.call_input_bindings||[]).filter(b=>b.method_id===method.method_id).map(b=>[b.ir_id,b]));
    for(const r of rows) {
      const operands=(r.reads||[]).map(v=>derive(v));
      for(const variable of r.definitions||[]){const value=derive(variable);result.sections.ir_dataflow.push({ir_dataflow_id:id(r.ir_id,variable),method_id:method.method_id,engine:'gigahorse',variable,operator:r.op,symbolic_expression:value.text,
        width:256,signed:['SDIV','SMOD','SAR','SLT','SGT'].includes(r.op),arithmetic:'vm_word_operator_with_separate_checks',rounding:['DIV','SDIV'].includes(r.op)?'toward_zero':null,
        status:value.unresolved.length?'partial':'represented_tac_dependencies_resolved',memory_read_id:value.memory_read_id,unresolved:value.unresolved,evidence_ids:value.evidence_ids,all_execution_paths_verified:false})}
      let effect;
      if(['CALL','CALLCODE','DELEGATECALL','STATICCALL'].includes(r.op))effect={kind:'external_call',call_type:r.op,target_expression:operands[1]?.text,value_expression:['CALL','CALLCODE'].includes(r.op)?operands[2]?.text:null,
        value_unit:result.snapshot.vm_family==='tron_tvm'?'sun':'wei',fund_settlement:'requires_successful_execution_evidence',memory_input_status:memoryBindings.get(r.ir_id)?.status||'requires_memory_dataflow',
        call_input_binding_id:memoryBindings.get(r.ir_id)?.call_input_binding_id};
      if(['SLOAD','SSTORE'].includes(r.op))effect={kind:r.op==='SSTORE'?'state_write':'state_read',slot_expression:operands[0]?.text,value_expression:r.op==='SSTORE'?operands[1]?.text:null};
      if(r.op==='JUMPI')effect={kind:'branch',condition_expression:operands[1]?.text,jump_destination_expression:operands[0]?.text};
      if(effect)result.sections.ir_effects.push({ir_effect_id:id(r.ir_id,'effect'),method_id:method.method_id,engine:'gigahorse',statement:r.statement,block:r.block,...effect,
        operand_derivations:operands.map(p=>({expression:p.text,unresolved:p.unresolved})),path_conditions_status:'cfg_available_path_guards_not_solved',
        evidence_ids:unique([...r.evidence_ids,...operands.flatMap(p=>p.evidence_ids)]),basis:'decompiled_tac',all_execution_paths_verified:false});
    }
  }
}
