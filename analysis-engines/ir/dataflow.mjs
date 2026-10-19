import { id, unique } from '../model.mjs';

// Substitution must preserve the precedence of an entire value, including a
// conditional returned by a callee. Source spelling alone is not an operand.
function formulaOperand(text) {
  if (/^(?:[A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*|[0-9]+|0x[0-9a-fA-F]+)$/.test(text)) return text;
  return '('+text+')';
}

// Expand only definitions that dominate the use in the represented source path.
// Branch-specific and compound definitions remain explicit unresolved inputs.
export function applyDataflow(result) {
  const allEvidence = new Map(result.sections.evidence.map(e => [e.evidence_id, e]));
  result.sections.dataflow ||= [];
  for (const method of result.sections.methods) {
    const expressions = new Map(result.sections.expressions.filter(e => e.method_id === method.method_id).map(e => [e.expression_id, e]));
    const definitions = (result.sections.definitions || []).filter(d => d.method_id === method.method_id);
    const typedZero=(key,seen=new Set())=>{
      if(!key||seen.has(key)||seen.size>=64)return false;const e=expressions.get(key);if(!e||e.source_scalar_binding_unavailable||e.unresolved_evaluation_effect)return false;const next=new Set([...seen,key]);
      if(e.binding_expression_id)return typedZero(e.binding_expression_id,next);
      const numericType=/^(?:u?int\d*|bytes(?:[1-9]|[12]\d|3[0-2])|address(?: payable)?)$/.test(e.type||'');
      if(e.literal_kind==='number'||e.symbolic_format==='literal'&&numericType){const value=String(e.value??e.raw_literal??'');return /^(?:0+|0x0+)$/i.test(value);}
      if(numericType&&['scalar_cast','identity'].includes(e.symbolic_format)&&e.operands?.length===1)return typedZero(e.operands[0],next);
      if(numericType&&e.call_kind==='typeConversion'&&e.argument_expression_ids?.length===1)return typedZero(e.argument_expression_ids[0],next);
      if(e.symbolic_format==='return_cases'&&e.paths?.length)return e.paths.every(p=>typedZero(p.expression_id,next));
      return false;
    };
    const expansionCache=new Map();
    const collectLoops=key=>{const seen=new Set(),pending=[key],loops=[];while(pending.length&&seen.size<4096){const k=pending.pop();if(seen.has(k))continue;seen.add(k);const e=expressions.get(k);if(!e)continue;
      loops.push(...(e.internal_return_model?.loops||e.entry_return_model?.loops||[]));pending.push(...(e.operands||[]));if(e.binding_expression_id)pending.push(e.binding_expression_id);}
      return [...new Map(loops.map(l=>[l.ast_id+':'+l.iterations,l])).values()];};
    const expand = (key,seen=new Set(),budget={count:0},beforeOrder=Infinity) => {
      const e=expressions.get(key);
      if(!e)return {text:'',unresolved:['EXPRESSION_UNAVAILABLE'],definitions:[],evidence:[]};
      if(seen.has(key)||++budget.count>4096)return {text:e.text,unresolved:['DATAFLOW_BUDGET_OR_CYCLE'],definitions:[],evidence:[...(e.evidence_ids||[])]};
      const cacheKey=key+':'+beforeOrder,cached=expansionCache.get(cacheKey);
      if(cached)return {...cached,unresolved:[...cached.unresolved],definitions:[...cached.definitions],evidence:[...cached.evidence]};
      const value=expandValue(key,seen,budget,beforeOrder);
      // A budget/cycle result depends on its traversal context. Stable DAG
      // expansions are reusable at the same dominance boundary.
      if(!value.unresolved.includes('DATAFLOW_BUDGET_OR_CYCLE')){
        if(expansionCache.size>=1024)expansionCache.delete(expansionCache.keys().next().value);
        expansionCache.set(cacheKey,value);
      }
      return value;
    };
    const expandValue = (key, seen, budget, beforeOrder) => {
      const e = expressions.get(key);
      if (!e) return { text: '', unresolved: ['EXPRESSION_UNAVAILABLE'], definitions: [], evidence: [] };
      const own = { text: e.text, unresolved: [e.unresolved_return_value,e.unresolved_evaluation_effect,...(e.unresolved_model_reasons||[]),...(e.source_scalar_unresolved||[])].filter(Boolean), definitions: [], evidence: [...(e.evidence_ids || [])] };
      const next = new Set([...seen, key]);
      // An unresolved write expression still names its compiler lvalue. Reading
      // the operands for dependency evidence must never rewrite that target as
      // its old value (for example, `z = x + y` becoming `0 = x + y`).
      if(e.unresolved_evaluation_effect==='LOCAL_EXPRESSION_EFFECT_REQUIRES_ORDERED_RETURN_MODEL'&&!e.write_target_expression_id){
        const values=(e.operands||[]).map(k=>expand(k,next,budget,beforeOrder));
        return {...own,unresolved:unique([...own.unresolved,...values.flatMap(v=>v.unresolved)]),
          definitions:unique(values.flatMap(v=>v.definitions)),evidence:unique([...own.evidence,...values.flatMap(v=>v.evidence)])};
      }
      if(e.source_scalar_binding_unavailable)return {...own,unresolved:unique([...own.unresolved,'SOURCE_SCALAR_VALUE_UNAVAILABLE'])};
      if(e.source_aggregate_binding_unavailable)return {...own,unresolved:unique([...own.unresolved,'SOURCE_AGGREGATE_FIELD_VALUE_UNAVAILABLE'])};
      if(e.source_aggregate_reference){
        const references=[e.source_aggregate_reference],missing=[],referenceDependencies=[];let visited=0;
        while(references.length&&++visited<=64){const ref=references.pop();if(!ref)continue;missing.push(...(ref.unresolved||[]));referenceDependencies.push(...(ref.aggregate_return_expression_ids||[]));if(ref.kind==='conditional_aggregate_reference'){referenceDependencies.push(ref.condition_expression_id);references.push(ref.yes,ref.no);}}
        if(references.length)missing.push('SOURCE_AGGREGATE_REFERENCE_BUDGET');
        const ids=unique([...referenceDependencies,...(e.call_kind==='structConstructorCall'?e.argument_expression_ids||[]:[]),...(e.aggregate_return_paths||[]).flatMap(p=>[p.expression_id,...(p.conditions||[]).map(c=>c.expression_id),...(p.execution_steps||[]).map(s=>s.expression_id)])]),values=ids.filter(k=>k&&k!==key).map(k=>expand(k,next,budget,beforeOrder));
        return{...own,text:e.text||'source_aggregate_reference',unresolved:unique([...own.unresolved,...missing,...values.flatMap(v=>v.unresolved)]),definitions:unique(values.flatMap(v=>v.definitions)),evidence:unique([...own.evidence,...values.flatMap(v=>v.evidence)])};
      }
      if(e.symbolic_format==='tuple_component') {
        let whole=expressions.get(e.operands?.[0]),index=e.component_index,tupleSeen=new Set(),chain=[];
        while(whole&&!tupleSeen.has(whole.expression_id)){
          tupleSeen.add(whole.expression_id);chain.push(whole);
          if(!whole.binding_expression_id)break;
          whole=expressions.get(whole.binding_expression_id);
        }
        const gaps=chain.flatMap(v=>[v.unresolved_return_value,v.unresolved_evaluation_effect,...(v.unresolved_model_reasons||[]),...(v.source_scalar_unresolved||[])]).filter(Boolean);
        const component=whole?.abi_component_expression_ids?.[index]||(whole?.tuple_component_expression_ids?whole.tuple_component_expression_ids[index]:(whole?.symbolic_format==='tuple'||whole?.op==='TupleExpression')?whole.operands?.[index]:null);
        const v=expand(component||e.operands?.[0],next,budget,beforeOrder);
        // A tuple is never interchangeable with one of its values. Even an
        // opaque return retains the projection index and the compiler type.
        return {...v,text:component?v.text:'tuple_component('+v.text+', '+index+')',
          unresolved:unique([...own.unresolved,...gaps,...v.unresolved,...(!component?['TUPLE_COMPONENT_UNAVAILABLE']:[])]),
          evidence:unique([...own.evidence,...chain.flatMap(v=>v.evidence_ids||[]),...v.evidence])};
      }
      if(e.symbolic_format) {
        const values=(e.operands||[]).map(k=>expand(k,next,budget,beforeOrder));
        const merged={...own,unresolved:unique([...own.unresolved,...values.flatMap(v=>v.unresolved)]),definitions:unique(values.flatMap(v=>v.definitions)),evidence:unique([...own.evidence,...values.flatMap(v=>v.evidence)])};
        if(e.symbolic_format==='evm_word')return {...merged,text:e.operator+'('+values.map(v=>v.text).join(', ')+')'};
        if(e.symbolic_format==='yul_memory_access')return {...merged,text:'memory_write_'+e.memory_width+'('+values.map(v=>v.text).join(', ')+')'};
        if(e.symbolic_format==='yul_memory_read')return {...merged,text:'mload_after_ordered_writes('+values.map(v=>v.text).join(', ')+')'};
        if(e.symbolic_format==='inductive_affine_loop')return {...merged,text:'('+values[0].text+' '+e.operator+' integer_product('+values[1].text+', '+values[2].text+'))',
          induction_model_id:e.loop_model_id,proof_obligation_ids:e.proof_obligation_ids};
        if(e.symbolic_format==='inductive_polynomial_loop')return {...merged,text:'('+values[0].text+' '+e.operator+' sum_{i=0}^{'+values[1].text+'-1} ('+e.summation_terms.map(t=>t.coefficient+' * '+t.factor_operands.map(k=>values[k].text).concat(['i^'+t.degree]).join(' * ')).join(' + ')+'))',
          induction_model_id:e.loop_model_id,proof_obligation_ids:e.proof_obligation_ids,polynomial_certificate:e.polynomial_certificate};
        if(e.symbolic_format==='source_storage_snapshot'){
          const keyValues=new Map((e.operands||[]).map((key,i)=>[key,values[i]?.text]));let invalid=false,count=0;
          const slot=(term,depth=0)=>{if(!term||++count>128||depth>32){invalid=true;return '?';}if(term.op==='constant'||term.op==='expression'){const value=term.op==='constant'?term.value:keyValues.get(term.expression_id);if(value===undefined)invalid=true;return term.encoding_type?'storage_key<'+term.encoding_type+'>('+value+')':value;}const parts=(term.args||[]).map(t=>slot(t,depth+1));if(term.op==='keccak_words')return 'keccak_words('+parts.join(', ')+')';const op={add:'+',mul:'*',div:'/',mod:'%'}[term.op];if(!op||parts.length!==2){invalid=true;return '?';}return '('+parts[0]+' '+op+' '+parts[1]+')';};
          const text='storage_at_copy<'+e.type+'>('+e.source_storage_copy_point_id+', '+slot(e.storage_projection?.slot_expression)+', byte_offset='+e.storage_projection?.offset+')';
          if(invalid||Buffer.byteLength(text)>16384)return {...merged,text:e.text,unresolved:unique([...merged.unresolved,invalid?'SOURCE_STORAGE_SNAPSHOT_SLOT_UNAVAILABLE':'EXPANDED_EXPRESSION_TOO_LARGE'])};
          return {...merged,text};
        }
        if(e.symbolic_format==='literal')return {...merged,text:e.value};
        if(e.symbolic_format==='entry_scalar')return {...merged,text:e.value};
        if(e.symbolic_format==='conditional')return {...merged,text:'('+values[0].text+' ? '+values[1].text+' : '+values[2].text+')'};
        if(e.symbolic_format==='identity')return {...merged,text:values[0].text};
        if(e.symbolic_format==='scalar_cast')return {...merged,text:e.type+'('+(e.preserve_source_type?e.cast_source_type+'('+values[0].text+')':values[0].text)+')'};
        if(e.symbolic_format==='unary')return {...merged,text:'('+e.operator+formulaOperand(values[0].text)+')'};
        if(e.symbolic_format==='binary')return {...merged,text:'('+formulaOperand(values[0].text)+' '+e.operator+' '+formulaOperand(values[1].text)+')'};
        if(e.symbolic_format==='tuple')return {...merged,text:'('+values.map(v=>v.text).join(', ')+')',
          return_paths:values[0]?.return_paths?.map((p,i)=>({conditions:p.conditions,execution_steps:p.execution_steps||[],value:'('+values.map(v=>v.return_paths?.[i]?.value||v.text).join(', ')+')'}))||[]};
        if(e.symbolic_format==='return_cases') {
          const paths=(e.paths||[]).flatMap((p,i)=>{const conditions=p.conditions.map(c=>{const v=expand(c.expression_id,next,budget,beforeOrder);merged.unresolved.push(...v.unresolved);merged.evidence.push(...v.evidence);merged.definitions.push(...v.definitions);return (c.expected?'':'!')+'('+v.text+')'});
            const execution_steps=(p.execution_steps||[]).map(step=>{const v=expand(step.expression_id,next,{count:0},beforeOrder);merged.unresolved.push(...v.unresolved);merged.evidence.push(...v.evidence);merged.definitions.push(...v.definitions);return {...step,expression:v.text};});
            return values[i].return_paths?.length?values[i].return_paths.map(inner=>({value:inner.value,conditions:unique([...conditions,...inner.conditions]),execution_steps:[...execution_steps,...(inner.execution_steps||[])]})):[{value:values[i].text,conditions,execution_steps}];});
          if(paths.length>32)return {...merged,unresolved:unique([...merged.unresolved,'COMPOSED_RETURN_PATH_BUDGET']),text:e.text};
          let text=paths.at(-1).value;
          for(let i=paths.length-2;i>=0;i--)text='('+paths[i].conditions.join(' && ')+' ? '+paths[i].value+' : '+text+')';
          if(Buffer.byteLength(text)>16384)return {...merged,unresolved:unique([...merged.unresolved,'EXPANDED_EXPRESSION_TOO_LARGE']),return_paths:paths};
          return {...merged,text,unresolved:unique(merged.unresolved),evidence:unique(merged.evidence),definitions:unique(merged.definitions),return_paths:paths};
        }
        return {...merged,unresolved:[...merged.unresolved,'SYMBOLIC_FORMAT_UNSUPPORTED']};
      }
      if(e.resolved_value!==undefined)return {...own,text:e.resolved_value};
      if (e.binding_expression_id) {const bound=expand(e.binding_expression_id,next,budget,beforeOrder),evaluations=unique([...(e.aggregate_evaluation_expression_ids||[]),...(e.aggregate_return_expression_ids||[])]).filter(k=>k!==e.binding_expression_id).map(k=>expand(k,next,budget,beforeOrder));return {...bound,text:e.binding_kind==='function_return'?'('+bound.text+')':formulaOperand(bound.text),unresolved:unique([...own.unresolved,...bound.unresolved,...evaluations.flatMap(v=>v.unresolved)]),definitions:unique([...bound.definitions,...evaluations.flatMap(v=>v.definitions)]),evidence:unique([...own.evidence,...bound.evidence,...evaluations.flatMap(v=>v.evidence)])}}
      // The synthetic entry argument captures the value before any body
      // assignment. Branch-local reassignments cannot redefine that input.
      if(e.entry_argument)return own;
      const local = definitions.filter(d => d.declaration_id === e.referenced_declaration);
      if (!local.length && e.unresolved_local_reference) return { ...own, unresolved: ['LOCAL_DEFINITION_UNAVAILABLE'] };
      if (local.length && ['Identifier','Name'].includes(e.op)) {
        const useOrder = Math.min(beforeOrder, e.step_order ?? Infinity);
        const matching = local.filter(d => d.operation === '=' && d.order < useOrder &&
          (d.path === e.path || e.path?.startsWith(d.path + '/')) && (d.conditions || []).every(c => (e.conditions || []).includes(c)));
        matching.sort((a,b) => b.order - a.order);
        const selected = matching[0];
        const later = local.some(d => d.order < useOrder && d.order > (selected?.order ?? -1));
        if(!selected&&!later&&method.parameters.some(p=>p.source_ast_id===e.referenced_declaration))return own;
        if (!selected || later) return { ...own, unresolved: ['DEFINITION_DOES_NOT_DOMINATE_USE'] };
        const value = expand(selected.value_expression_id, next, budget, selected.order);
        return { ...value, text: '(' + value.text + ')', definitions: unique([...value.definitions, selected.definition_id]),
          evidence: unique([...own.evidence, ...value.evidence, ...selected.evidence_ids]) };
      }
      if (/^symbolic_expression:/.test(e.text || '')) return { ...own, unresolved: ['EXPRESSION_REQUIRES_IR_ARTIFACT'] };
      const location = (e.evidence_ids || []).map(k => allEvidence.get(k)?.location).find(l => Number.isInteger(l?.start));
      if (!location) return own;
      const replacements = [];
      for (const operand of e.operands || []) {
        const child = expressions.get(operand);
        const at = (child?.evidence_ids || []).map(k => allEvidence.get(k)?.location).find(l => l?.file === location.file && Number.isInteger(l.start));
        if (!at || at.start < location.start || at.start + at.length > location.start + location.length) continue;
        const expanded = expand(operand, next, budget, beforeOrder);
        own.unresolved.push(...expanded.unresolved); own.definitions.push(...expanded.definitions); own.evidence.push(...expanded.evidence);
        // Preserve the compiler write target while still expanding read values
        // on the right with their actual caller/frame bindings.
        if(operand===e.write_target_expression_id)continue;
        if (expanded.text !== child.text) replacements.push({ start: at.start - location.start, length: at.length, text: formulaOperand(expanded.text) });
      }
      replacements.sort((a,b) => a.start - b.start || b.length - a.length);
      const disjoint = replacements.filter((r,i,list) => !list.slice(0,i).some(p => p.start <= r.start && p.start + p.length > r.start));
      let bytes = Buffer.from(e.text || '');
      for (const r of disjoint.reverse()) bytes = Buffer.concat([bytes.subarray(0,r.start), Buffer.from(r.text), bytes.subarray(r.start+r.length)]);
      if (bytes.length > 16384) return { ...own, unresolved: [...own.unresolved,'EXPANDED_EXPRESSION_TOO_LARGE'] };
      return { ...own, text: bytes.toString('utf8'), definitions: unique(own.definitions), evidence: unique(own.evidence), unresolved: unique(own.unresolved) };
    };
    for(const e of expressions.values())if(e.internal_return_model&&!e.entry_return_summary) {
      const value=expand(e.expression_id);
      result.sections.dataflow.push({dataflow_id:id(e.expression_id,'internal-return'),method_id:method.method_id,kind:'internal_return_derivation',
        expression_id:e.expression_id,original_expression:e.text,symbolic_expression:value.text,
        successful_return_paths:value.return_paths||[],scope:e.internal_return_model.scope,loops:collectLoops(e.expression_id),
        status:value.unresolved.length?'partial':'represented_source_dependencies_resolved',unresolved:value.unresolved,
        definition_ids:value.definitions,evidence_ids:value.evidence,all_execution_paths_verified:false});
    }
    if(method.entry_return_expression_id) {
      const e=expressions.get(method.entry_return_expression_id),value=expand(e.expression_id),dataflow_id=id(e.expression_id,'entry-return');
      const status=value.unresolved.length?'partial':'represented_source_dependencies_resolved';
      result.sections.dataflow.push({dataflow_id,method_id:method.method_id,kind:'entry_return_derivation',expression_id:e.expression_id,
        original_expression:method.signature,symbolic_expression:value.text,successful_return_paths:value.return_paths||[],loops:collectLoops(e.expression_id),
        scope:e.entry_return_model.scope,status,unresolved:value.unresolved,definition_ids:value.definitions,evidence_ids:value.evidence,all_execution_paths_verified:false});
      method.entry_return_derivation_id=dataflow_id;method.entry_return_model_status=status;method.entry_return_model_unresolved=value.unresolved;
    }
    for(const call of result.sections.calls.filter(c=>c.method_id===method.method_id&&c.target_expression_id)) {
      const target=expand(call.target_expression_id),dataflow_id=id(call.call_id,'target-dataflow');
      result.sections.dataflow.push({dataflow_id,method_id:method.method_id,call_id:call.call_id,kind:'call_target_derivation',expression_id:call.target_expression_id,
        original_expression:call.target_expression,symbolic_expression:target.text,status:target.unresolved.length?'partial':'represented_source_dependencies_resolved',unresolved:target.unresolved,
        definition_ids:target.definitions,evidence_ids:target.evidence,all_execution_paths_verified:false});
      call.expanded_target_expression=target.text;call.target_derivation_id=dataflow_id;call.target_derivation_status=target.unresolved.length?'partial':'represented_source_dependencies_resolved';
    }
    for(const storage of result.sections.storage.filter(r=>r.method_id===method.method_id))for(const p of storage.storage_projection?.path||[])if(p.key_expression_id){const v=expand(p.key_expression_id);p.expanded_key_expression=v.text;p.key_derivation_status=v.unresolved.length?'partial':'represented_source_dependencies_resolved';}
    for (const fund of result.sections.funds.filter(f => f.method_id === method.method_id && f.amount_expression_id)) {
      const amount = expand(fund.amount_expression_id);
      const flow_id = id(fund.fund_id, 'amount-dataflow');
      const row = { dataflow_id: flow_id, method_id: method.method_id, fund_id: fund.fund_id,
        kind: 'amount_derivation', original_expression: fund.amount_expression, symbolic_expression: amount.text,
        expression_id: fund.amount_expression_id, definition_ids: amount.definitions, evidence_ids: amount.evidence,
        status: amount.unresolved.length ? 'partial' : method.completeness.basis.includes('source') ? 'represented_source_dependencies_resolved' : 'provided_bytecode_expression', unresolved: amount.unresolved,
        scope: 'represented_definitions_and_paths', all_execution_paths_verified: false };
      result.sections.dataflow.push(row);
      fund.amount_derivation_id = flow_id;
      fund.expanded_amount_expression = amount.text;
      fund.amount_derivation_status = row.status;
      if(fund.kind==='native_payment'&&!amount.unresolved.length&&(typedZero(fund.amount_expression_id)||/^\(*0\)*$/.test(amount.text.replace(/\s/g,'')))){
        fund.economic_value_status='zero_call_value';
        fund.economic_value_basis='resolved_zero_literal_or_compiler_typed_zero_binding';
        for(const flow of result.sections.flows)if(flow.fund_id===fund.fund_id)flow.economic_value_status='zero_call_value';
      }
    }
    for(const fund of result.sections.funds.filter(f=>f.method_id===method.method_id))for(const field of ['asset','from','to']) {
      const key=fund[field+'_expression_id'];if(!key)continue;
      const value=expand(key),dataflow_id=id(fund.fund_id,field+'-dataflow');
      result.sections.dataflow.push({dataflow_id,method_id:method.method_id,fund_id:fund.fund_id,kind:field+'_derivation',original_expression:fund[field+'_expression']||fund[field],
        symbolic_expression:value.text,status:value.unresolved.length?'partial':'represented_source_dependencies_resolved',unresolved:value.unresolved,
        definition_ids:value.definitions,evidence_ids:value.evidence,all_execution_paths_verified:false});
      fund['expanded_'+field+'_expression']=value.text;fund[field+'_derivation_id']=dataflow_id;
    }
    for(const guard of result.sections.permissions.filter(g=>g.method_id===method.method_id&&g.expression_id)) {
      const condition=expand(guard.expression_id),dataflow_id=id(guard.guard_id,'condition-dataflow');
      result.sections.dataflow.push({dataflow_id,method_id:method.method_id,guard_id:guard.guard_id,kind:'condition_derivation',original_expression:guard.condition,symbolic_expression:condition.text,
        status:condition.unresolved.length?'partial':'represented_source_dependencies_resolved',unresolved:condition.unresolved,definition_ids:condition.definitions,evidence_ids:condition.evidence,all_execution_paths_verified:false});
      guard.expanded_condition=condition.text;guard.condition_derivation_id=dataflow_id;guard.condition_derivation_status=condition.unresolved.length?'partial':'represented_source_dependencies_resolved';
    }
    for(const step of result.sections.steps.filter(s=>s.method_id===method.method_id&&s.kind==='return'&&s.return_scope!=='internal'&&s.expression_id)){
      const returned=expressions.get(step.expression_id),key=returned?.op==='Return'&&returned.operands?.length===1?returned.operands[0]:step.expression_id,value=expand(key);
      const dataflow_id=id(step.step_id,'return-dataflow');result.sections.dataflow.push({dataflow_id,method_id:method.method_id,step_id:step.step_id,kind:'return_derivation',original_expression:expressions.get(key)?.text,
        symbolic_expression:value.text,status:value.unresolved.length?'partial':'represented_source_dependencies_resolved',unresolved:value.unresolved,definition_ids:value.definitions,evidence_ids:value.evidence,all_execution_paths_verified:false});
    }
    for(const version of (result.sections.source_memory_versions||[]).filter(v=>v.method_id===method.method_id&&!v.mutation&&v.argument_expression_ids?.length)){
      version.argument_derivation_ids=[];
      for(const [index,expression_id]of version.argument_expression_ids.entries()){
        const value=expand(expression_id),dataflow_id=id(version.source_memory_version_id,'argument-dataflow',index);
        result.sections.dataflow.push({dataflow_id,method_id:method.method_id,source_memory_version_id:version.source_memory_version_id,kind:'source_abi_argument_derivation',argument_index:index,
          expression_id,original_expression:version.argument_texts[index],symbolic_expression:value.text,status:value.unresolved.length?'partial':'represented_source_dependencies_resolved',
          unresolved:value.unresolved,definition_ids:value.definitions,evidence_ids:value.evidence,all_execution_paths_verified:false});
        version.argument_derivation_ids.push(dataflow_id);
      }
    }
    for(const version of(result.sections.source_scalar_versions||[]).filter(v=>v.method_id===method.method_id&&v.value_expression_id)){
      const value=expand(version.value_expression_id),dataflow_id=id(version.source_scalar_version_id,'value-dataflow');
      result.sections.dataflow.push({dataflow_id,method_id:method.method_id,source_scalar_version_id:version.source_scalar_version_id,kind:'source_scalar_value_derivation',
        expression_id:version.value_expression_id,original_expression:expressions.get(version.value_expression_id)?.text,
        symbolic_expression:value.text,status:value.unresolved.length?'partial':'represented_source_dependencies_resolved',unresolved:value.unresolved,
        definition_ids:value.definitions,evidence_ids:value.evidence,all_execution_paths_verified:false});
      version.value_derivation_id=dataflow_id;version.value_derivation_status=value.unresolved.length?'partial':'represented_source_dependencies_resolved';
    }
    method.return_derivation_ids=result.sections.dataflow.filter(d=>d.method_id===method.method_id&&d.kind==='return_derivation').map(d=>d.dataflow_id);
    method.amount_derivation_ids = result.sections.dataflow.filter(d => d.method_id === method.method_id&&d.kind==='amount_derivation').map(d => d.dataflow_id);
    method.condition_derivation_ids=result.sections.dataflow.filter(d=>d.method_id===method.method_id&&d.kind==='condition_derivation').map(d=>d.dataflow_id);
  }
}
