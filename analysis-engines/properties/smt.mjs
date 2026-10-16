import {id} from '../model.mjs';
import {powerSumScaledSMT} from '../verification/polynomials.mjs';
import {fixedBytesTerm} from '../ir/scalar-casts.mjs';
const and=xs=>xs.length?'(and '+xs.join(' ')+')':'true',or=xs=>xs.length?'(or '+xs.join(' ')+')':'false';
const numeral=n=>{n=BigInt(n);return n<0n?'(- '+(-n)+')':n.toString();};

// Integer terms retain Solidity bounds and exact per-operation rounding.
// Unsupported expressions never become unconstrained proof inputs.
export function propertyTranslator(result,method,definitionIDs=[]) {
  const exprs=new Map(result.sections.expressions.filter(e=>e.method_id===method.method_id).map(e=>[e.expression_id,e]));
  const definitions=(result.sections.definitions||[]).filter(d=>definitionIDs.includes(d.definition_id));
  const cache=new Map(),visiting=new Set(),variables=new Map(),functions=new Map(),lookups=new Map(),lines=[],constraints=[];
  const fail=reason=>{throw new Error(reason);};
  const type=e=>{if(e.type==='bool')return {kind:'Bool'};
    const i=/^(u?int)(\d*)$/.exec(e.type||''),bytes=/^bytes(\d+)$/.exec(e.type||'');
    const width=i?Number(i[2]||256):/^(address|contract )/.test(e.type||'')?160:bytes?Number(bytes[1])*8:e.width;
    if(!width||width>256)fail('SMT_TYPE_UNSUPPORTED:'+e.type);
    const signed=i?i[1]==='int':e.signed===true,mod=1n<<BigInt(width);
    return {kind:'Int',width,signed,mod,min:signed?-(mod>>1n):0n,max:signed?(mod>>1n)-1n:mod-1n};};
  const bool=v=>v.kind==='Bool'?v.term:'(not (= '+v.term+' 0))';
  const signedDiv=(a,b)=>'(ite (< (* '+a+' '+b+') 0) (- (div (abs '+a+') (abs '+b+'))) (div (abs '+a+') (abs '+b+')))';
  function normalized(term,e,ok) {
    const t=type(e);if(t.kind==='Bool')return {term,kind:t.kind,ok};
    if(e.arithmetic==='checked')ok=and([ok,'(<= '+numeral(t.min)+' '+term+')','(<= '+term+' '+numeral(t.max)+')']);
    else {term='(mod '+term+' '+t.mod+')';if(t.signed)term='(ite (>= '+term+' '+(t.mod>>1n)+') (- '+term+' '+t.mod+') '+term+')';}
    return {term,kind:t.kind,ok};
  }
  function variable(e) {
    const t=type(e),identity=e.op==='Identifier'?'declaration:'+e.referenced_declaration:'environment:'+(e.builtin_global||e.text);
    const key='v_'+id(method.code_hash,method.storage_address,identity),prior=variables.get(key);
    if(prior&&prior.kind!==t.kind)fail('SMT_VARIABLE_TYPE_CONFLICT');
    if(!prior){variables.set(key,{name:key,expression:e.text,type:e.type,kind:t.kind});
      if(t.kind==='Int')constraints.push('(and (<= '+numeral(t.min)+' '+key+') (<= '+key+' '+numeral(t.max)+'))');}
    return {term:key,kind:t.kind,ok:'true'};
  }
  function pathDomain(p) {
    return and((p.execution_steps||p.conditions.map(c=>({kind:'guard',...c}))).flatMap(step=>{
      const value=walk(step.expression_id);return [value.ok,...(step.kind==='guard'?[step.expected?bool(value):'(not '+bool(value)+')']:[])];
    }));
  }
  function storagePath(e,seen=new Set()) {
    if(!e||seen.has(e.expression_id))fail('SMT_STORAGE_PATH_UNSUPPORTED');seen.add(e.expression_id);
    if(e.binding_expression_id)return storagePath(exprs.get(e.binding_expression_id),seen);
    if(e.op==='Identifier'&&(result.sections.storage||[]).some(r=>r.method_id===method.method_id&&r.access==='read'&&r.state_ast_id===e.referenced_declaration))return {root:e.referenced_declaration,fields:[],keys:[]};
    if(e.op==='IndexAccess'&&e.operands.length===2){const path=storagePath(exprs.get(e.operands[0]),seen);path.keys.push(walk(e.operands[1]));return path;}
    if(e.op==='MemberAccess'&&e.operands.length===1){const path=storagePath(exprs.get(e.operands[0]),seen);path.fields.push(e.referenced_declaration??e.text.split('.').at(-1));return path;}
    fail('SMT_STORAGE_PATH_UNSUPPORTED');
  }
  function storageValue(e) {
    const path=storagePath(e),t=type(e),name='s_'+id(method.code_hash,method.storage_address,path.root,path.fields),signature=path.keys.map(k=>k.kind).join(' ');
    const declaration='(declare-fun '+name+' ('+signature+') '+t.kind+')';
    if(functions.has(name)&&functions.get(name)!==declaration)fail('SMT_STORAGE_TYPE_CONFLICT');functions.set(name,declaration);
    const term=path.keys.length?'('+name+' '+path.keys.map(k=>k.term).join(' ')+')':name;
    lookups.set(term,{name:term,expression:e.text,type:e.type,kind:t.kind});
    // Scalar mapping values are unconstrained within the type. Their storage
    // identity is one function, so equal symbolic keys cannot disagree.
    const domain=t.kind==='Bool'?'true':'(and (<= '+numeral(t.min)+' '+term+') (<= '+term+' '+numeral(t.max)+'))';
    return {term,kind:t.kind,ok:and([...path.keys.map(k=>k.ok),domain])};
  }
  function compute(e) {
    if(e.unresolved_return_value||e.unresolved_evaluation_effect||e.unresolved_model_reasons?.length)fail('SMT_UNRESOLVED_EXPRESSION');
    if(e.binding_expression_id)return walk(e.binding_expression_id);
    if(e.symbolic_format==='tuple_component') {
      let whole=exprs.get(e.operands[0]),seen=new Set();while(whole?.binding_expression_id&&!seen.has(whole.expression_id)){seen.add(whole.expression_id);whole=exprs.get(whole.binding_expression_id);}
      if(!whole||!['tuple'].includes(whole.symbolic_format)&&whole.op!=='TupleExpression')fail('SMT_TUPLE_UNSUPPORTED');
      return walk(whole.operands[e.component_index]);
    }
    if(e.symbolic_format==='return_cases') {
      const rows=e.paths.map(p=>{const v=walk(p.expression_id);return {v,domain:and([pathDomain(p),v.ok])};});
      if(!rows.length)fail('SMT_EMPTY_PATHS');let term=rows.at(-1).v.kind==='Bool'?'false':'0';
      for(const {v,domain} of [...rows].reverse())term='(ite '+domain+' '+v.term+' '+term+')';
      return {term,kind:rows[0].v.kind,ok:or(rows.map(r=>r.domain))};
    }
    if(e.symbolic_format==='literal'||e.op==='Literal'||e.resolved_value!==undefined) {
      const value=e.resolved_value??e.value;if(value==='true'||value==='false')return {term:value,kind:'Bool',ok:'true'};
      if(value===null||value===undefined)fail('SMT_LITERAL_UNSUPPORTED');return {term:numeral(value),kind:'Int',ok:'true'};
    }
    if(e.op==='Identifier') {
      if(e.unresolved_local_reference) {
        const choices=definitions.filter(d=>d.declaration_id===e.referenced_declaration&&d.order<(e.step_order??Infinity)).sort((a,b)=>b.order-a.order);
        if(!choices.length||choices[0].operation!=='=')fail('SMT_LOCAL_DEFINITION_UNSUPPORTED');return walk(choices[0].value_expression_id);
      }
      return variable(e);
    }
    if(e.op==='MemberAccess'&&['msg.sender','tx.origin','block.timestamp','block.number','msg.value'].includes(e.builtin_global))return variable(e);
    if(['IndexAccess','MemberAccess'].includes(e.op))return storageValue(e);
    if(e.symbolic_format==='identity'){const v=walk(e.operands[0]);return e.yul_word_assignment?normalized(v.term,{...e,arithmetic:'wrap'},v.ok):v;}
    if(e.symbolic_format==='scalar_cast'){const v=walk(e.operands[0]);return normalized(fixedBytesTerm(v.term,e.cast_source_type,e.type),{...e,arithmetic:'wrap'},v.ok);}
    if(['yul_memory_access','yul_memory_read'].includes(e.symbolic_format)) {
      const args=e.operands.map(walk),limit=1n<<256n,checks=args.map(a=>a.ok),offset=args[0].term;
      const range=(p,size)=>checks.push('(and (<= 0 '+p+') (<= (+ '+p+' '+size+') '+limit+'))');
      range(offset,e.memory_width||32);
      if(e.symbolic_format==='yul_memory_access')return {term:'0',kind:'Int',ok:and(checks)};
      if(!e.all_read_bytes_written)fail('SMT_YUL_MEMORY_ENTRY_BYTES_UNKNOWN');
      for(const w of e.memory_writes)range(args[w.offset_operand].term,w.width);
      const bytes=[];for(let i=0;i<32;i++){
        const address='(+ '+offset+' '+i+')';let value='0';
        for(const w of e.memory_writes){const p=args[w.offset_operand].term,v='(mod '+args[w.value_operand].term+' '+limit+')';
          const index=w.offset_equals_read?i:e.read_offset_known!==null&&w.offset_known!==null?Number(BigInt(e.read_offset_known)+BigInt(i)-BigInt(w.offset_known)):null;
          if(index!==null&&(index<0||index>=w.width))continue;
          let byte=w.width===1?'(mod '+v+' 256)':index!==null?'(mod (div '+v+' '+(1n<<BigInt((31-index)*8))+') 256)':'0';
          if(w.width===32&&index===null)for(let j=31;j>=0;j--)byte='(ite (= '+address+' (+ '+p+' '+j+')) (mod (div '+v+' '+(1n<<BigInt((31-j)*8))+') 256) '+byte+')';
          value='(ite (and (>= '+address+' '+p+') (< '+address+' (+ '+p+' '+w.width+'))) '+byte+' '+value+')';}
        bytes.push('(* '+value+' '+(1n<<BigInt((31-i)*8))+')');
      }
      return {term:'(+ '+bytes.join(' ')+')',kind:'Int',ok:and(checks)};
    }
    if(e.symbolic_format==='inductive_affine_loop') {
      const [a,n,c]=e.operands.map(walk);return normalized('('+e.operator+' '+a.term+' (* '+n.term+' '+c.term+'))',e,and([a.ok,n.ok,c.ok]));
    }
    if(e.symbolic_format==='inductive_polynomial_loop') {
      const a=walk(e.operands[0]),n=walk(e.operands[1]);
      const coefficients=new Map();for(const t of e.summation_terms)for(const k of t.factor_operands)if(!coefficients.has(k))coefficients.set(k,walk(e.operands[k]));
      const terms=e.summation_terms.map(t=>'(* '+[numeral(t.coefficient),...t.factor_operands.map(k=>coefficients.get(k).term),powerSumScaledSMT(t.degree,n.term)].join(' ')+')');
      const sum='(div '+(terms.length?'(+ '+terms.join(' ')+')':'0')+' '+e.summation_scale+')';
      const checks=[...coefficients.values()].map(v=>v.ok);
      if(e.arithmetic==='checked')checks.push(walk(e.operands[e.last_increment_operand]).ok);
      return normalized('('+e.operator+' '+a.term+' '+sum+')',e,and([a.ok,n.ok,'(ite (= '+n.term+' 0) true '+and(checks)+')']));
    }
    if(e.op==='FunctionCall'&&e.call_kind==='typeConversion'){const key=e.argument_expression_ids[0],v=walk(key);return normalized(fixedBytesTerm(v.term,exprs.get(key)?.type,e.type),{...e,arithmetic:'wrap'},v.ok);}
    if(e.op==='Conditional'&&e.operands.length===3){
      const keys=[e.condition_expression_id,e.true_expression_id,e.false_expression_id];if(keys.some(k=>!k))fail('SMT_CONDITIONAL_LAYOUT_UNAVAILABLE');
      const [condition,yes,no]=keys.map(walk),terms=[yes,no].map((v,i)=>fixedBytesTerm(v.term,exprs.get(keys[i+1])?.type,e.type));
      return {term:'(ite '+bool(condition)+' '+terms[0]+' '+terms[1]+')',kind:type(e).kind,ok:and([condition.ok,'(ite '+bool(condition)+' '+yes.ok+' '+no.ok+')'])};
    }
    if(e.op==='TupleExpression'&&e.operands.length===1)return walk(e.operands[0]);
    const op=e.operator||e.op;
    if(e.symbolic_format==='evm_word')fail('SMT_YUL_PROPERTY_UNSUPPORTED');
    const values=(e.operands||[]).map((key)=>{const v=walk(key);return /^bytes\d+$/.test(e.common_type||'')?{...v,term:fixedBytesTerm(v.term,exprs.get(key)?.type,e.common_type)}:v;}),[a,b]=values,ok=and(values.map(v=>v.ok));
    if(op==='&&'||op==='||')return {term:'('+ (op==='&&'?'and':'or')+' '+bool(a)+' '+bool(b)+')',kind:'Bool',
      ok:and([a.ok,'(ite '+bool(a)+' '+(op==='&&'?b.ok:'true')+' '+(op==='||'?b.ok:'true')+')'])};
    if(op==='!')return {term:'(not '+bool(a)+')',kind:'Bool',ok};
    if(['==','!=','<','<=','>','>='].includes(op)&&values.length===2)return {term:op==='!='?'(not (= '+a.term+' '+b.term+'))':'('+(op==='=='?'=':op)+' '+a.term+' '+b.term+')',kind:'Bool',ok};
    if(['+','-','*'].includes(op)&&values.length>=1&&values.length<=2)return normalized('('+op+' '+values.map(v=>v.term).join(' ')+')',e,ok);
    if(['/','%'].includes(op)&&values.length===2) {
      const division=signedDiv(a.term,b.term),term=op==='/'?division:'(- '+a.term+' (* '+division+' '+b.term+'))';
      return normalized(term,e,and([ok,'(not (= '+b.term+' 0))']));
    }
    fail('SMT_OPERATION_UNSUPPORTED:'+op);
  }
  function walk(key) {
    if(cache.has(key))return cache.get(key);if(visiting.has(key)||cache.size>4096)fail('SMT_GRAPH_BUDGET_OR_CYCLE');
    const e=exprs.get(key);if(!e)fail('SMT_EXPRESSION_MISSING');visiting.add(key);const value=compute(e);visiting.delete(key);
    const symbol='e_'+key,valid='d_'+key;
    lines.push('(define-fun '+symbol+' () '+value.kind+' '+value.term+')','(define-fun '+valid+' () Bool '+value.ok+')');
    const out={...value,term:symbol,ok:valid};cache.set(key,out);return out;
  }
  return {walk,bool,and,or,pathDomain,exprs,
    query(domain,violation){const declarations=['(set-option :timeout 300)','(set-option :rlimit 1000000)',
      ...[...variables.values()].map(v=>'(declare-const '+v.name+' '+v.kind+')'),...functions.values(),...lines,
      ...constraints.map(c=>'(assert '+c+')')].join('\n');
      if(Buffer.byteLength(declarations)>240000)fail('SMT_QUERY_BUDGET');
      return {declarations,domain,violation,variables:[...variables.keys(),...lookups.keys()],inputs:[...variables.values(),...lookups.values()]};},
  };
}
