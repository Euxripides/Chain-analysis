import{readFileSync}from'node:fs';import{createHash}from'node:crypto';import{compileSource}from'../../analysis-engines/source.mjs';
const raw=readFileSync(0,'utf8'),source=JSON.parse(raw),output=compileSource(source.stdJsonInput,source.compilation.compilerVersion),nodes=new Map(),files=new Map(Object.entries(output.sources).map(([name,v])=>[v.id,name])),conditions={},raw_calls={},compiler_builtin_calls={};
function visit(n,f){if(!n||typeof n!=='object')return;if(n.nodeType)f(n);for(const[k,v]of Object.entries(n))if(k!=='typeDescriptions')if(Array.isArray(v))v.forEach(c=>visit(c,f));else if(v&&typeof v==='object')visit(v,f);}
for(const unit of Object.values(output.sources))visit(unit.ast,n=>nodes.set(n.id,n));
function location(n){const[start,length,file]=n.src.split(':').map(Number);return{file:files.get(file),start,length,ast_id:n.id};}
function text(n){const at=location(n);return Buffer.from(source.stdJsonInput.sources[at.file].content).subarray(at.start,at.start+at.length).toString('utf8');}
const contains=(outer,inner)=>outer.file===inner.file&&outer.start<=inner.start&&inner.start+inner.length<=outer.start+outer.length;
const functions=[...nodes.values()].filter(n=>n.nodeType==='FunctionDefinition'&&n.body),guardFacts=[];
for(const n of nodes.values())if(n.nodeType==='FunctionCall'&&n.expression?.nodeType==='Identifier'&&['require','assert'].includes(n.expression.name)&&!nodes.has(n.expression.referencedDeclaration)&&n.arguments?.[0])
 guardFacts.push({condition:text(n.arguments[0]),location:location(n),predicate_ast_id:n.arguments[0].id});
function bodySpans(expression,budget={complete:true}){
 const calls=[];visit(expression,c=>{if(c.nodeType==='FunctionCall')calls.push(c)});const spans=[],seen=new Set();
 function descendants(c,depth=0){if(depth>=12||seen.size>=4096){budget.complete=false;return;}let target=nodes.get(c.expression?.referencedDeclaration);if(target?.nodeType!=='FunctionDefinition')return;
  const candidates=[target,...[...nodes.values()].filter(f=>f.nodeType==='FunctionDefinition'&&f.baseFunctions?.includes(target.id))];
  for(const f of candidates)if(f.body&&!seen.has(f.id)){seen.add(f.id);spans.push(location(f.body));visit(f.body,child=>{if(child.nodeType==='FunctionCall')descendants(child,depth+1)});}}
 for(const call of calls)descendants(call);return spans;
}
function prefixGuards(n){
 const at=location(n),fn=functions.filter(f=>contains(location(f.body),at)).sort((a,b)=>location(a.body).length-location(b.body).length)[0];if(!fn)return{complete:false,facts:[],function_ast_id:null};
 const body=location(fn.body),spans=[{...body,length:at.start-body.start}],budget={complete:true};
 visit(fn.body,c=>{if(c.nodeType==='FunctionCall'&&location(c).start+location(c).length<=at.start)spans.push(...bodySpans(c,budget));});
 return{complete:budget.complete,facts:guardFacts.filter(g=>spans.some(s=>contains(s,g.location))),function_ast_id:fn.id};
}
for(const n of nodes.values()){
 if(n.nodeType==='FunctionCall'){const callee=n.expression?.nodeType==='FunctionCallOptions'?n.expression.expression:n.expression;
  if(/^t_function_array(?:push|pop)_/.test(callee?.typeDescriptions?.typeIdentifier||''))compiler_builtin_calls[n.id]={
    location:location(n),kind:'storage_array_mutation',operation:callee.memberName,compiler_type_identity:callee.typeDescriptions.typeIdentifier,
    target_text:text(callee.expression),argument_texts:(n.arguments||[]).map(text)};
  if(callee?.nodeType==='MemberAccess'&&['call','callcode','delegatecall','staticcall'].includes(callee.memberName)&&/^address(?: payable)?$/.test(callee.expression?.typeDescriptions?.typeString||''))raw_calls[n.id]={location:location(n),kind:callee.memberName,argument_texts:(n.arguments||[]).map(text)};}
 if(n.nodeType==='BinaryOperation'&&['&&','||'].includes(n.operator))conditions[n.id]={operator:n.operator,required_condition:n.operator==='&&'?text(n.leftExpression):'!('+text(n.leftExpression)+')',
  evaluated_expression_span:location(n.rightExpression),reachable_internal_body_spans:bodySpans(n.rightExpression)};
 if(n.nodeType==='Conditional'){
  const prefix=prefixGuards(n);
  for(const [arm,expression,other]of[['true',n.trueExpression,n.falseExpression],['false',n.falseExpression,n.trueExpression]]){
   const otherBudget={complete:true},otherSpans=[location(other),...bodySpans(other,otherBudget)],otherFacts=guardFacts.filter(g=>otherSpans.some(s=>contains(s,g.location))&&!prefix.facts.some(p=>p.condition===g.condition));
   conditions['ternary:'+n.id+':'+arm]={operator:'?:',arm,required_condition:arm==='true'?text(n.condition):'!('+text(n.condition)+')',evaluated_expression_span:location(expression),reachable_internal_body_spans:bodySpans(expression),
    predecessor_reference_complete:prefix.complete&&otherBudget.complete,enclosing_function_ast_id:prefix.function_ast_id,guards_exclusive_to_unevaluated_arm:otherFacts};
  }
 }
}
process.stdout.write(JSON.stringify({compiler:source.compilation.compilerVersion,source_provider_sha256:createHash('sha256').update(raw).digest('hex'),conditions,raw_calls,compiler_builtin_calls}));
