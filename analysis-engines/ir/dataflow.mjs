import { id, unique } from '../model.mjs';

// Expand only definitions that dominate the use in the represented source path.
// Branch-specific and compound definitions remain explicit unresolved inputs.
export function applyDataflow(result) {
  const allEvidence = new Map(result.sections.evidence.map(e => [e.evidence_id, e]));
  result.sections.dataflow ||= [];
  for (const method of result.sections.methods) {
    const expressions = new Map(result.sections.expressions.filter(e => e.method_id === method.method_id).map(e => [e.expression_id, e]));
    const definitions = (result.sections.definitions || []).filter(d => d.method_id === method.method_id);
    const collectLoops=key=>{const seen=new Set(),pending=[key],loops=[];while(pending.length&&seen.size<4096){const k=pending.pop();if(seen.has(k))continue;seen.add(k);const e=expressions.get(k);if(!e)continue;
      loops.push(...(e.internal_return_model?.loops||e.entry_return_model?.loops||[]));pending.push(...(e.operands||[]));if(e.binding_expression_id)pending.push(e.binding_expression_id);}
      return [...new Map(loops.map(l=>[l.ast_id+':'+l.iterations,l])).values()];};
    const expand = (key, seen = new Set(), budget = { count: 0 }, beforeOrder = Infinity) => {
      const e = expressions.get(key);
      if (!e) return { text: '', unresolved: ['EXPRESSION_UNAVAILABLE'], definitions: [], evidence: [] };
      const own = { text: e.text, unresolved: [e.unresolved_return_value,e.unresolved_evaluation_effect].filter(Boolean), definitions: [], evidence: [...(e.evidence_ids || [])] };
      if (seen.has(key) || ++budget.count > 512) return { ...own, unresolved: ['DATAFLOW_BUDGET_OR_CYCLE'] };
      const next = new Set([...seen, key]);
      if(e.symbolic_format==='tuple_component') {
        let whole=expressions.get(e.operands?.[0]),index=e.component_index,tupleSeen=new Set();
        while(whole?.binding_expression_id&&!tupleSeen.has(whole.expression_id)){tupleSeen.add(whole.expression_id);whole=expressions.get(whole.binding_expression_id);}
        const component=(whole?.symbolic_format==='tuple'||whole?.op==='TupleExpression')?whole.operands?.[index]:null;
        if(!component)return {...own,unresolved:['TUPLE_COMPONENT_UNAVAILABLE']};
        const v=expand(component,next,budget,beforeOrder);return {...v,evidence:unique([...own.evidence,...v.evidence])};
      }
      if(e.symbolic_format) {
        const values=(e.operands||[]).map(k=>expand(k,next,budget,beforeOrder));
        const merged={...own,unresolved:unique(values.flatMap(v=>v.unresolved)),definitions:unique(values.flatMap(v=>v.definitions)),evidence:unique([...own.evidence,...values.flatMap(v=>v.evidence)])};
        if(e.symbolic_format==='literal')return {...merged,text:e.value};
        if(e.symbolic_format==='identity')return {...merged,text:values[0].text};
        if(e.symbolic_format==='unary')return {...merged,text:'('+e.operator+values[0].text+')'};
        if(e.symbolic_format==='binary')return {...merged,text:'('+values[0].text+' '+e.operator+' '+values[1].text+')'};
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
      if (e.binding_expression_id) {const bound=expand(e.binding_expression_id,next,budget,beforeOrder);return {...bound,text:e.binding_kind==='function_return'?'('+bound.text+')':bound.text,evidence:unique([...own.evidence,...bound.evidence])}}
      const local = definitions.filter(d => d.declaration_id === e.referenced_declaration);
      if (!local.length && e.unresolved_local_reference) return { ...own, unresolved: ['LOCAL_DEFINITION_UNAVAILABLE'] };
      if (local.length && ['Identifier','Name'].includes(e.op)) {
        const useOrder = Math.min(beforeOrder, e.step_order ?? Infinity);
        const matching = local.filter(d => d.operation === '=' && d.order < useOrder &&
          (d.path === e.path || e.path?.startsWith(d.path + '/')) && (d.conditions || []).every(c => (e.conditions || []).includes(c)));
        matching.sort((a,b) => b.order - a.order);
        const selected = matching[0];
        const later = local.some(d => d.order < useOrder && d.order > (selected?.order ?? -1));
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
        if (expanded.text !== child.text) replacements.push({ start: at.start - location.start, length: at.length, text: expanded.text });
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
    method.return_derivation_ids=result.sections.dataflow.filter(d=>d.method_id===method.method_id&&d.kind==='return_derivation').map(d=>d.dataflow_id);
    method.amount_derivation_ids = result.sections.dataflow.filter(d => d.method_id === method.method_id&&d.kind==='amount_derivation').map(d => d.dataflow_id);
    method.condition_derivation_ids=result.sections.dataflow.filter(d=>d.method_id===method.method_id&&d.kind==='condition_derivation').map(d=>d.dataflow_id);
  }
}
