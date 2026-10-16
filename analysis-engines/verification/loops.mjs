import {id,unique} from '../model.mjs';
import {and,number,obligation,solveObligations,closeModels} from './solver.mjs';

const children=n=>Object.entries(n||{}).filter(([k])=>!['typeDescriptions','documentation'].includes(k)).flatMap(([,v])=>Array.isArray(v)?v.filter(x=>x?.nodeType):v?.nodeType?[v]:[]);
const uint=n=>{const m=/^uint(\d*)$/.exec(n?.typeDescriptions?.typeString||'');return m?Number(m[1]||256):null;};
const flatten=n=>['Block','UncheckedBlock'].includes(n?.nodeType)?n.statements.flatMap(flatten):n?[n]:[];
const scoped=(n,unchecked=false)=>['Block','UncheckedBlock'].includes(n?.nodeType)?n.statements.flatMap(c=>scoped(c,unchecked||n.nodeType==='UncheckedBlock')):n?[{node:n,unchecked}]:[];
const expression=n=>n?.nodeType==='ExpressionStatement'?n.expression:n;
const literal=n=>{try{return n?.nodeType==='Literal'&&n.kind==='number'?BigInt(n.value):null;}catch{return null;}};
const same=(n,key)=>n?.nodeType==='Identifier'&&n.referencedDeclaration===key;

export function detectAffineLoop(loop,nodes,{unchecked=false,bindings=new Map()}={}) {
 const fail=reason=>{throw new Error(reason);};
 let initial=loop.initializationExpression,counter,start;
 if(initial?.nodeType==='VariableDeclarationStatement'&&initial.declarations.length===1){counter=initial.declarations[0];start=initial.initialValue?literal(initial.initialValue):0n;}
 else if(initial?.nodeType==='ExpressionStatement'&&initial.expression.nodeType==='Assignment') {
  counter=nodes.get(initial.expression.leftHandSide?.referencedDeclaration);start=literal(initial.expression.rightHandSide);
 }
 const condition=loop.condition;
 if(!counter&&condition?.leftExpression?.nodeType==='Identifier'){counter=nodes.get(condition.leftExpression.referencedDeclaration);start=bindings.get(counter?.id);}
 if(!counter||counter.stateVariable||!uint(counter)||start!==0n||condition?.nodeType!=='BinaryOperation'||condition.operator!=='<'||!same(condition.leftExpression,counter.id))fail('LOOP_NOT_ZERO_BASED_MONOTONE_COUNTER');
 const bound=condition.rightExpression;
 if(bound.nodeType!=='Identifier'||!uint(nodes.get(bound.referencedDeclaration)||bound))fail('LOOP_BOUND_NOT_STABLE_UINT');
 const body=scoped(loop.body,unchecked),post=flatten(loop.loopExpression).map(expression),bodyExpressions=body.map(x=>expression(x.node));
 const update=post.length?post:bodyExpressions.slice(-1);
 if(update.length!==1||update[0]?.nodeType!=='UnaryOperation'||update[0].operator!=='++'||!same(update[0].subExpression,counter.id))fail('LOOP_COUNTER_UPDATE_UNSUPPORTED');
 const statements=post.length?bodyExpressions:bodyExpressions.slice(0,-1),recurrences=[];
 for(const [index,n] of statements.entries()) {
  if(n?.nodeType!=='Assignment'||!['+=','-='].includes(n.operator))fail('LOOP_BODY_NOT_AFFINE_LOCAL_ASSIGNMENTS');
  const d=nodes.get(n.leftHandSide?.referencedDeclaration);
  if(n.leftHandSide?.nodeType!=='Identifier'||!d||d.stateVariable||!uint(d)||d.id===counter.id||d.id===bound.referencedDeclaration)fail('LOOP_NONLOCAL_OR_CONTROL_WRITE');
  const visited=[];function valid(x){
   if(x.nodeType==='Literal'&&x.kind==='number')return;
   if(x.nodeType==='Identifier'&&x.referencedDeclaration!==counter.id&&uint(nodes.get(x.referencedDeclaration)||x)){visited.push(x.referencedDeclaration);return;}
   if(x.nodeType==='BinaryOperation'&&['+','-','*','/','%'].includes(x.operator)){valid(x.leftExpression);valid(x.rightExpression);return;}
   throw new Error('LOOP_INCREMENT_NOT_ITERATION_INDEPENDENT');
  }valid(n.rightHandSide);
  recurrences.push({declaration:d.id,name:d.name,width:uint(d),operator:n.operator[0],increment:n.rightHandSide,reads:visited,unchecked:body[index].unchecked,
   total_increment:['Identifier','Literal'].includes(n.rightHandSide.nodeType)});
 }
 const changed=new Set(recurrences.map(r=>r.declaration));
 if(changed.size!==recurrences.length||recurrences.some(r=>r.reads.some(k=>changed.has(k))))fail('LOOP_RECURRENCE_DEPENDENCY_UNSUPPORTED');
 return {counter:counter.id,counter_name:counter.name,counter_width:uint(counter),bound,bound_width:uint(nodes.get(bound.referencedDeclaration)||bound),recurrences,unchecked};
}

export function analyzeLoopInvariants(result,{functions,nodes,methodByAST,location,getEvidence,legacyArithmetic=false}) {
 const models=[],queries=[];
 for(const fn of functions) {
  const method=methodByAST.get(fn.id);if(!method||method.code_phase!=='runtime_source')continue;
  function scan(n,unchecked=false,bindings=new Map()) {
   if(!n)return;
   if(n.nodeType==='VariableDeclarationStatement'&&n.declarations?.length===1)bindings.set(n.declarations[0]?.id,n.initialValue?literal(n.initialValue):0n);
   if(n.nodeType==='ExpressionStatement'&&n.expression?.nodeType==='Assignment')bindings.delete(n.expression.leftHandSide?.referencedDeclaration);
   if(['ForStatement','WhileStatement','DoWhileStatement'].includes(n.nodeType)) {
    const model={model_id:id(method.method_id,n.id,'loop-induction'),method_id:method.method_id,kind:'loop_invariant',
     code_hash:method.code_hash,code_address:method.code_address,storage_address:method.storage_address,
     source_location:location(n),scope:'all_successful_loop_iterations',
     assumptions:['typed_uint_loop_entry_state','successful_checked_arithmetic_or_explicit_word_wrap','gas_execution_feasibility_not_proved'],
     evidence_ids:[getEvidence(method,n)],unresolved:[],obligations:[],complete:false};models.push(model);
    try {
     if(n.nodeType==='DoWhileStatement')throw new Error('LOOP_DO_WHILE_INDUCTION_PENDING');
     const d=detectAffineLoop(n,nodes,{unchecked,bindings});
     model.counter_ast_id=d.counter;model.bound_ast_id=d.bound.id;model.counter=d.counter_name;
     model.bound=d.bound.name;model.recurrences=d.recurrences.map(r=>({declaration:r.declaration,name:r.name,width:r.width,operator:r.operator,increment_ast_id:r.increment.id,arithmetic:r.unchecked?'wrap':'checked'}));
     model.summary={iteration_count:d.bound.name,counter_exit:d.bound.name,values:d.recurrences.map(r=>({declaration:r.declaration,
      mathematical_expression:r.name+'@entry '+r.operator+' '+d.bound.name+' * increment@'+r.increment.id,word_bits:r.width,arithmetic:r.unchecked?'wrap':'checked'}))};
     const maxN=(1n<<BigInt(d.bound_width))-1n,maxI=(1n<<BigInt(d.counter_width))-1n;
     const declarations='(declare-const n Int)\n(declare-const i Int)';
     const domain=and(['(<= 0 n)','(<= n '+maxN+')','(<= 0 i)','(<= i n)','(<= i '+maxI+')']);
     const inputs=[{name:'n',expression:d.bound.name,type:'uint'+d.bound_width},{name:'i',expression:d.counter_name,type:'uint'+d.counter_width}];
     queries.push(obligation(model,'loop_initialization',declarations,and(['(<= 0 n)','(<= n '+maxN+')']),
      '(not (and (<= 0 n) (<= 0 '+maxI+')))',inputs));
     queries.push(obligation(model,'loop_counter_preservation_and_no_wrap',declarations,and([domain,'(< i n)']),
      '(not (and (<= (+ i 1) n) (<= (+ i 1) '+maxI+')))',inputs));
     queries.push(obligation(model,'loop_ranking_function',declarations,and([domain,'(< i n)']),
      '(not (and (> (- n i) 0) (< (- n (+ i 1)) (- n i)) (>= (- n (+ i 1)) 0)))',inputs));
     queries.push(obligation(model,'loop_exit_counter_equals_bound',declarations,and([domain,'(not (< i n))']),
      '(not (= i n))',inputs));
     for(const r of d.recurrences) {
      // The increment denotes the exact, iteration-independent value evaluated
      // by the source expression. Body expression validity remains a separate
      // successful-arithmetic condition; no arbitrary unknown node is admitted.
      const mod=1n<<BigInt(r.width),max=mod-1n,sign=r.operator==='+'?'+':'-';
      const form=k=>'('+sign+' a0 (* '+k+' c))';
      const inv=k=>r.unchecked?'(mod '+form(k)+' '+mod+')':form(k);
      const after=r.unchecked?'(mod ('+sign+' a c) '+mod+')':'('+sign+' a c)';
      const decl=declarations+'\n(declare-const a0 Int)\n(declare-const a Int)\n(declare-const c Int)';
      const scope=and([domain,'(<= 0 a0)','(<= a0 '+max+')','(<= 0 a)','(<= a '+max+')','(<= 0 c)','(<= c '+max+')']);
      const args=[...inputs,{name:'a0',expression:r.name+'@entry',type:'uint'+r.width},{name:'a',expression:r.name+'@head',type:'uint'+r.width},{name:'c',expression:'increment@'+r.increment.id,type:'uint'+r.width}];
      queries.push(obligation(model,'loop_accumulator_initialization_'+r.declaration,decl,scope,
       '(not (= a0 '+inv('0')+'))',args));
      queries.push(obligation(model,'loop_accumulator_preservation_'+r.declaration,decl,
       and([scope,'(< i n)','(= a '+inv('i')+')',...(r.unchecked?[]:['(<= 0 '+after+')','(<= '+after+' '+max+')'])]),
       '(not (= '+after+' '+inv('(+ i 1)')+'))',args));
      queries.push(obligation(model,'loop_accumulator_exit_'+r.declaration,decl,and([scope,'(= a '+inv('i')+')','(not (< i n))']),
       '(not (= a '+inv('n')+'))',args));
     }
    }catch(error){model.unresolved.push(String(error.message));}
   }
   for(const child of children(n))scan(child,unchecked||n.nodeType==='UncheckedBlock',new Map(bindings));
  }
  // Sequential declarations establish only literal counter initializers. Other
  // control-flow mutations invalidate the initializer rather than guessing it.
  const bindings=new Map();for(const statement of fn.body?.statements||[]){
   scan(statement,legacyArithmetic,bindings);
   function invalidate(n){if(n.nodeType==='Assignment')bindings.delete(n.leftHandSide?.referencedDeclaration);
    if(n.nodeType==='UnaryOperation'&&['++','--','delete'].includes(n.operator))bindings.delete(n.subExpression?.referencedDeclaration);
    for(const c of children(n))invalidate(c);
   }invalidate(statement);
  }
 }
 const solved=solveObligations(result,queries);closeModels(models,solved);
 result.sections.loop_invariants||=[];result.sections.loop_invariants.push(...models);
 if(models.length)result.sections.coverage.push({dimension:'loop_induction',scope:'recognized_source_loops',complete:models.every(m=>m.complete),loops:models.length,proved:models.filter(m=>m.complete).length});
 return new Map(models.filter(m=>m.complete).map(m=>[m.source_location.ast_id,m]));
}
