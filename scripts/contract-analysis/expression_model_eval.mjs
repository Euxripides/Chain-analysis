// Acceptance-only evaluator of saved expression graphs, independent of the AST
// interpreter. It evaluates guards in order and checks exact Solidity integers.
export function evaluateReturn(model,name,inputs) {
 const method=model.sections.methods.find(m=>m.name===name);
 if(!method?.entry_return_expression_id)throw new Error('MODEL_UNAVAILABLE:'+name);
 const expressions=new Map(model.sections.expressions.filter(e=>e.method_id===method.method_id).map(e=>[e.expression_id,e])),cache=new Map();let steps=0;
 const fail=(kind,message=kind)=>{const error=new Error(message);error.kind=kind;throw error;};
 const truth=v=>typeof v==='boolean'?v:v!==0n;
 function normalize(v,e){if(typeof v!=='bigint'||!e.width)return v;const mod=1n<<BigInt(e.width),min=e.signed?-(mod>>1n):0n,max=e.signed?(mod>>1n)-1n:mod-1n;
  if(v<min||v>max){if(e.arithmetic==='checked')fail('panic_0x11');v=((v%mod)+mod)%mod;if(e.signed&&v>max)v-=mod;}return v;}
 function walk(key){
  if(cache.has(key))return cache.get(key);if(++steps>100000)fail('MODEL_BUDGET');const e=expressions.get(key);if(!e)fail('MODEL_MISSING',key);
  const result=compute(e);cache.set(key,result);return result;
 }
 function compute(e){
  if(e.binding_expression_id)return walk(e.binding_expression_id);
  if(e.symbolic_format==='return_cases'){
   for(const path of e.paths){let matches=true;for(const step of path.execution_steps||path.conditions.map(c=>({kind:'guard',...c}))){
     const value=walk(step.expression_id);if(step.kind==='guard'&&truth(value)!==step.expected){if(step.failure)fail(step.failure==='panic'?'panic_0x01':'revert');matches=false;break;}
    }if(matches)return walk(path.expression_id);
   }fail('revert');
  }
  if(e.symbolic_format==='identity')return walk(e.operands[0]);
  if(e.symbolic_format==='tuple'||e.op==='TupleExpression'&&(e.operands.length!==1||e.type?.startsWith('tuple')))return e.operands.map(walk);
  if(e.op==='TupleExpression')return walk(e.operands[0]);
  if(e.symbolic_format==='tuple_component')return walk(e.operands[0])[e.component_index];
  if(e.symbolic_format==='literal'||e.op==='Literal'){const v=e.resolved_value??e.value;return v==='true'?true:v==='false'?false:BigInt(v);}
  if(e.resolved_value!==undefined)return BigInt(e.resolved_value);
  if(e.op==='Identifier'){if(Object.hasOwn(inputs,e.text))return inputs[e.text];fail('MODEL_INPUT',e.text);}
  if(e.op==='FunctionCall'&&e.call_kind==='typeConversion')return normalize(walk(e.argument_expression_ids[0]),{...e,arithmetic:'explicit_cast_truncation'});
  const op=e.operator||e.op;
  if(op==='&&')return truth(walk(e.operands[0]))&&truth(walk(e.operands[1]));
  if(op==='||')return truth(walk(e.operands[0]))||truth(walk(e.operands[1]));
  if(e.symbolic_format==='unary'||e.operands.length===1){const v=walk(e.operands[0]);if(op==='!')return !truth(v);if(op==='-')return normalize(-v,e);if(op==='~')return normalize(~v,{...e,arithmetic:'wrap'});if(op==='+')return v;}
  if(e.operands.length!==2)fail('MODEL_NODE',op);
  const a=walk(e.operands[0]),b=walk(e.operands[1]);let v;
  switch(op){case '+':v=a+b;break;case '-':v=a-b;break;case '*':v=a*b;break;case '/':if(b===0n)fail('panic_0x12');v=a/b;break;case '%':if(b===0n)fail('panic_0x12');v=a%b;break;case '==':return a===b;case '!=':return a!==b;case '<':return a<b;case '<=':return a<=b;case '>':return a>b;case '>=':return a>=b;case '&':v=a&b;break;case '|':v=a|b;break;case '^':v=a^b;break;default:fail('MODEL_OPERATOR',op);}
  return normalize(v,e);
 }
 const value=walk(method.entry_return_expression_id);return Array.isArray(value)?value:[value];
}
