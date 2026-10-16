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
  if(e.symbolic_format==='identity')return e.yul_word_assignment?normalize(walk(e.operands[0]),e):walk(e.operands[0]);
  if(['yul_memory_access','yul_memory_read'].includes(e.symbolic_format)) {
   const args=e.operands.map(walk),mod=1n<<256n,offset=args[0],valid=(p,size)=>{if(p<0n||p+BigInt(size)>mod)fail('memory_range_overflow');};
   valid(offset,e.memory_width||32);if(e.symbolic_format==='yul_memory_access')return 0n;
   for(const w of e.memory_writes)valid(args[w.offset_operand],w.width);
   let output=0n;for(let i=0n;i<32n;i++){
    const address=offset+i,w=[...e.memory_writes].reverse().find(w=>address>=args[w.offset_operand]&&address<args[w.offset_operand]+BigInt(w.width));
    if(!w)fail('YUL_MEMORY_ENTRY_BYTES_UNKNOWN');
    const value=(args[w.value_operand]%mod+mod)%mod,shift=w.width===1?0n:8n*(31n-(address-args[w.offset_operand]));
    output=(output<<8n)|((value>>shift)&255n);
   }return output;
  }
  if(e.symbolic_format==='inductive_affine_loop') {
   const [a,n,c]=e.operands.map(walk);return normalize(e.operator==='+'?a+n*c:a-n*c,e);
  }
  if(e.symbolic_format==='evm_word') {
   const mod=1n<<256n,mask=mod-1n,values=e.operands.map(k=>BigInt(walk(k))&mask),[a,b,c]=values,sign=v=>v>=mod/2n?v-mod:v;
   let v;
   switch(e.operator){
    case 'add':v=a+b;break;case 'sub':v=a-b;break;case 'mul':v=a*b;break;
    case 'div':v=b===0n?0n:a/b;break;case 'mod':v=b===0n?0n:a%b;break;
    case 'sdiv':v=b===0n?0n:sign(a)/sign(b);break;case 'smod':v=b===0n?0n:sign(a)%sign(b);break;
    case 'addmod':v=c===0n?0n:(a+b)%c;break;case 'mulmod':v=c===0n?0n:a*b%c;break;
    case 'not':v=~a;break;case 'and':v=a&b;break;case 'or':v=a|b;break;case 'xor':v=a^b;break;
    case 'lt':v=a<b?1n:0n;break;case 'gt':v=a>b?1n:0n;break;case 'slt':v=sign(a)<sign(b)?1n:0n;break;case 'sgt':v=sign(a)>sign(b)?1n:0n;break;
    case 'eq':v=a===b?1n:0n;break;case 'iszero':v=a===0n?1n:0n;break;
    case 'shl':v=a<256n?b<<a:0n;break;case 'shr':v=a<256n?b>>a:0n;break;case 'sar':v=a<256n?sign(b)>>a:sign(b)<0n?-1n:0n;break;
    case 'byte':v=a<32n?b>>((31n-a)*8n)&255n:0n;break;
    case 'signextend':{if(a>=32n){v=b;break;}const width=8n*(a+1n),bits=1n<<width;v=b&(bits/2n)?b|~(bits-1n):b&(bits-1n);break;}
    case 'exp':{let exponent=b,base=a;v=1n;while(exponent>0n){if(exponent%2n)v=v*base%mod;base=base*base%mod;exponent/=2n;}break;}
    default:fail('MODEL_WORD_OPERATOR',e.operator);
   }return v&mask;
  }
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
