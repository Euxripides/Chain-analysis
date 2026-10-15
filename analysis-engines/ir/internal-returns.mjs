import {numericLiteral} from '../literals.mjs';

// Bounded symbolic execution of read-only scalar and scalar-tuple returns.
// Compiler AST identities and per-path bindings prevent local variables from
// leaking between calls or branches. Unsupported effects fail the whole model.
export function summarizeInternalReturns({target, call, context, args, build, synthetic, nodes, typeOf}) {
  const fail = code => { throw new Error(code); };
  const output = target.returnParameters?.parameters || [];
  const scalar = d => /^(?:u?int\d*|bool|bytes(?:[1-9]|[12]\d|3[0-2])|address(?: payable)?|contract .+)$/.test(typeOf(d));
  if (!output.length || output.length > 8 || output.some(d => !scalar(d))) fail('INTERNAL_RETURN_NONSCALAR_OR_TUPLE');
  if (!['pure','view'].includes(target.stateMutability) && !target.constant) fail('INTERNAL_RETURN_STATE_EFFECT_UNSUPPORTED');
  if ((target.parameters?.parameters || []).some(p => p.storageLocation === 'storage')) fail('INTERNAL_RETURN_STORAGE_ALIAS_UNSUPPORTED');
  const stack = context.return_stack || [];
  if (stack.length >= 12 || stack.includes(target.id)) fail('INTERNAL_RETURN_RECURSION_OR_DEPTH');
  let count = 0, serial = 0, loopIterations = 0;
  const loops = new Map();
  const results = [], root = `${context.path}/return:${call.id}`;
  const bindings = {...context.parameter_expression_bindings};
  for (const [i,p] of (target.parameters?.parameters || []).entries()) bindings[p.id] = build(args[i], context);
  const initial = {...context, path:root, unchecked:false, return_stack:[...stack,target.id],
    parameter_arguments:{...context.parameter_arguments,...Object.fromEntries((target.parameters?.parameters || []).map((p,i)=>[p.id,args[i]]))},
    parameter_expression_bindings:bindings, compiletime_bindings:{}, return_conditions:[]};
  const expressionChildren = n => Object.entries(n || {}).filter(([k]) => !['typeDescriptions','documentation'].includes(k))
    .flatMap(([,v]) => Array.isArray(v) ? v.filter(x=>x?.nodeType) : v?.nodeType ? [v] : []);
  function validate(n) {
    if (!n || ++count > 1024) fail('INTERNAL_RETURN_EXPRESSION_BUDGET');
    if (['Assignment','NewExpression'].includes(n.nodeType) || n.nodeType === 'UnaryOperation' && ['++','--','delete'].includes(n.operator)) fail('INTERNAL_RETURN_EXPRESSION_SIDE_EFFECT');
    if (n.nodeType === 'FunctionCall' && n.kind !== 'typeConversion') {
      const called = nodes.get(n.expression?.referencedDeclaration);
      if (called?.nodeType === 'FunctionDefinition' && !['pure','view'].includes(called.stateMutability) && !called.constant ||
          ['call','send','transfer','delegatecall','callcode','selfdestruct','suicide'].includes(n.expression?.memberName || n.expression?.name)) fail('INTERNAL_RETURN_EXPRESSION_SIDE_EFFECT');
      if(called?.nodeType!=='FunctionDefinition'&&!['keccak256','sha256','ripemd160','ecrecover','addmod','mulmod'].includes(n.expression?.name)) fail('INTERNAL_RETURN_DYNAMIC_CALL_UNSUPPORTED');
    }
    for (const child of expressionChildren(n)) validate(child);
  }
  const value = (n,s) => { validate(n); return build(n,{...s,path:s.path+'/expr:'+(serial++)}); };
  const fork = (s,suffix) => ({...s,path:s.path+'/'+suffix,parameter_expression_bindings:{...s.parameter_expression_bindings},compiletime_bindings:{...s.compiletime_bindings},return_conditions:[...s.return_conditions]});
  const defaultValue = (declaration,s) => {
    const type=typeOf(declaration);
    if (!scalar(declaration)) fail('INTERNAL_RETURN_LOCAL_TYPE_UNSUPPORTED');
    return synthetic(declaration,'literal',[],s,{value:type==='bool'?'false':/^(address|contract )/.test(type)?'0x'+'0'.repeat(40):/^bytes/.test(type)?'0x'+'00'.repeat(Number(type.slice(5))):'0',type});
  };
  for (const d of output) if (d.name) {initial.parameter_expression_bindings[d.id]=defaultValue(d,initial);initial.compiletime_bindings[d.id]=0n;}
  // This evaluator is used only to prove finite loop control. Unknown state or
  // inputs never become a concrete bound. Values retain Solidity integer rules.
  function integerValue(v,n,s) {
    const integer=/^(u?int)(\d*)$/.exec(typeOf(n));if(!integer)return v;
    const bits=BigInt(integer[2]||256),signed=integer[1]==='int',mod=1n<<bits,min=signed?-(mod>>1n):0n,max=signed?(mod>>1n)-1n:mod-1n;
    if(v<min||v>max){if(!s.unchecked&&!context.legacy_arithmetic)fail('INTERNAL_RETURN_LOOP_CONTROL_OVERFLOW');v=((v%mod)+mod)%mod;if(signed&&v>max)v-=mod;}
    return v;
  }
  function constant(n,s,seen=new Set()) {
    if (!n || seen.has(n.id)) return null;
    const next=new Set([...seen,n.id]);
    if(n.nodeType==='Literal') {
      if(n.kind==='bool')return n.value==='true'?1n:0n;
      if(n.kind==='number'){try{const v=numericLiteral(String(n.value),n.subdenomination).value;return v===null?null:BigInt(v)}catch{return null}}
    }
    if(n.nodeType==='Identifier') {
      if(Object.hasOwn(s.compiletime_bindings,n.referencedDeclaration))return s.compiletime_bindings[n.referencedDeclaration];
      const d=nodes.get(n.referencedDeclaration),bound=s.parameter_arguments?.[n.referencedDeclaration];
      return d?.constant&&d.value?constant(d.value,s,next):bound&&bound.id!==n.id?constant(bound,s,next):null;
    }
    if(n.nodeType==='TupleExpression'&&n.components?.length===1)return constant(n.components[0],s,next);
    if(n.nodeType==='UnaryOperation') {const a=constant(n.subExpression,s,next);if(a===null)return null;return n.operator==='!'?(a===0n?1n:0n):n.operator==='-'?integerValue(-a,n,s):n.operator==='+'?a:null;}
    if(n.nodeType==='BinaryOperation') {
      const a=constant(n.leftExpression,s,next),b=constant(n.rightExpression,s,next);if(a===null||b===null)return null;
      switch(n.operator){case '+':return integerValue(a+b,n,s);case '-':return integerValue(a-b,n,s);case '*':return integerValue(a*b,n,s);case '/':return b===0n?null:integerValue(a/b,n,s);case '%':return b===0n?null:a%b;case '<':return a<b?1n:0n;case '<=':return a<=b?1n:0n;case '>':return a>b?1n:0n;case '>=':return a>=b?1n:0n;case '==':return a===b?1n:0n;case '!=':return a!==b?1n:0n;case '&&':return a!==0n&&b!==0n?1n:0n;case '||':return a!==0n||b!==0n?1n:0n;default:return null;}
    }
    return null;
  }
  function keepConstant(d,n,s,operator='=') {
    let v=constant(n,s);
    const old=s.compiletime_bindings[d.id];
    if(operator!=='='&&v!==null&&old!==undefined){v=operator==='+='?old+v:operator==='-='?old-v:operator==='*='?old*v:operator==='/='&&v!==0n?old/v:null;}
    else if(operator!=='=')v=null;
    const integer=/^(u?int)(\d*)$/.exec(typeOf(d));
    if(v!==null&&integer){const bits=BigInt(integer[2]||256),signed=integer[1]==='int',min=signed?-(1n<<(bits-1n)):0n,max=signed?(1n<<(bits-1n))-1n:(1n<<bits)-1n;
      if(v<min||v>max){if(!s.unchecked&&!context.legacy_arithmetic)fail('INTERNAL_RETURN_LOOP_CONTROL_OVERFLOW');const mod=1n<<bits;v=((v%mod)+mod)%mod;if(signed&&v>max)v-=mod;}
    }
    if(v===null)delete s.compiletime_bindings[d.id];else s.compiletime_bindings[d.id]=v;
  }
  const tupleValues=(n,length,s)=> {
    if(length===1)return [value(n,s)];
    if(n?.nodeType==='TupleExpression'&&n.components?.length===length&&n.components.every(Boolean))return n.components.map(c=>value(c,s));
    const whole=value(n,s);return Array.from({length},(_,i)=>synthetic(n,'tuple_component',[whole],s,{component_index:i}));
  };
  function assign(n,s) {
    if(n.leftHandSide?.nodeType==='TupleExpression') {
      if(n.operator!=='=')fail('INTERNAL_RETURN_TUPLE_COMPOUND_ASSIGNMENT');
      const lhs=n.leftHandSide.components,rhs=tupleValues(n.rightHandSide,lhs.length,s);
      for(const [i,p]of lhs.entries()) {if(!p)continue;const d=nodes.get(p.referencedDeclaration);
        if(p.nodeType!=='Identifier'||!d||d.stateVariable||!Object.hasOwn(s.parameter_expression_bindings,d.id))fail('INTERNAL_RETURN_ASSIGNMENT_TARGET_UNSUPPORTED');
        s.parameter_expression_bindings[d.id]=rhs[i];delete s.compiletime_bindings[d.id];}
      return [s];
    }
    const lhs=n.leftHandSide,decl=nodes.get(lhs?.referencedDeclaration);
    if (lhs?.nodeType !== 'Identifier' || !decl || decl.stateVariable || !Object.hasOwn(s.parameter_expression_bindings,decl.id)) fail('INTERNAL_RETURN_ASSIGNMENT_TARGET_UNSUPPORTED');
    const rhs=value(n.rightHandSide,s);
    keepConstant(decl,n.rightHandSide,s,n.operator);
    s.parameter_expression_bindings[decl.id]=n.operator==='='?rhs:synthetic(n,'binary',[s.parameter_expression_bindings[decl.id],rhs],s,{operator:n.operator.slice(0,-1),type:typeOf(lhs)});
    return [s];
  }
  function execute(n,s) {
    if (++count > 1024 || results.length > 32) fail('INTERNAL_RETURN_PATH_BUDGET');
    if (!n || s.control) return [s];
    if (['Block','UncheckedBlock'].includes(n.nodeType)) {
      const outer=s.unchecked;
      let pending=[{...s,unchecked:s.unchecked || n.nodeType==='UncheckedBlock'}];
      for (const stmt of n.statements || []) {
        pending=pending.flatMap(p=>execute(stmt,p));
        if (pending.length+results.length > 32) fail('INTERNAL_RETURN_PATH_BUDGET');
        if (!pending.length) break;
      }
      return pending.map(p=>({...p,unchecked:outer}));
    }
    if (n.nodeType==='IfStatement') {
      const known=constant(n.condition,s);
      if(known!==null)return execute(known!==0n?n.trueBody:n.falseBody,fork(s,'if:'+n.id+':constant'));
      const condition=value(n.condition,s),yes=fork(s,'if:'+n.id+':true'),no=fork(s,'if:'+n.id+':false');
      yes.return_conditions.push({expression_id:condition,expected:true}); no.return_conditions.push({expression_id:condition,expected:false});
      return [...execute(n.trueBody,yes),...execute(n.falseBody,no)];
    }
    if (n.nodeType==='VariableDeclarationStatement') {
      const ds=n.declarations||[];
      if(!ds.length||ds.some(d=>d&&(d.storageLocation==='storage'||!scalar(d))))fail('INTERNAL_RETURN_LOCAL_TYPE_UNSUPPORTED');
      const rhs=n.initialValue?tupleValues(n.initialValue,ds.length,s):ds.map(d=>d?defaultValue(d,s):null);
      for(const [i,d]of ds.entries())if(d){s.parameter_expression_bindings[d.id]=rhs[i];if(ds.length===1&&n.initialValue)keepConstant(d,n.initialValue,s);else if(!n.initialValue)s.compiletime_bindings[d.id]=0n;}
      return [s];
    }
    if (n.nodeType==='Return') {
      const returned=n.expression?tupleValues(n.expression,output.length,s):output.map(d=>s.parameter_expression_bindings[d.id]);
      if (returned.some(v=>!v)) fail('INTERNAL_RETURN_VALUE_UNAVAILABLE');
      results.push({expression_id:returned[0],expression_ids:returned,conditions:s.return_conditions}); return [];
    }
    if(['ForStatement','WhileStatement','DoWhileStatement'].includes(n.nodeType)) {
      let pending=n.initializationExpression?execute(n.initializationExpression,s):[s],finished=[],iteration=0;
      do {
        const active=[];
        for(const p of pending){const test=n.nodeType==='DoWhileStatement'&&iteration===0?1n:n.condition?constant(n.condition,p):null;
          if(test===null)fail('INTERNAL_RETURN_CONTROL_FLOW_UNSUPPORTED:LOOP_DYNAMIC_BOUND');
          if(test===0n)finished.push(p);else active.push(p);}
        if(!active.length)break;
        if(iteration>=16||++loopIterations>64)fail('INTERNAL_RETURN_LOOP_ITERATION_BUDGET');
        pending=[];
        for(const p of active)for(const q of execute(n.body,fork(p,'loop:'+n.id+':'+iteration))){
          if(q.control==='break'){delete q.control;finished.push(q);continue;}
          if(q.control==='continue')delete q.control;
          pending.push(...(n.loopExpression?execute(n.loopExpression,q):[q]));}
        iteration++;if(pending.length+finished.length+results.length>32)fail('INTERNAL_RETURN_PATH_BUDGET');
      }while(pending.length);
      loops.set(n.id,{ast_id:n.id,iterations:Math.max(loops.get(n.id)?.iterations||0,iteration),bound_basis:'compiletime_values_and_local_counter'});
      return finished;
    }
    if(['Break','Continue'].includes(n.nodeType)){s.control=n.nodeType.toLowerCase();return [s];}
    if (n.nodeType==='RevertStatement') return [];
    if (n.nodeType==='ExpressionStatement') {
      const e=n.expression,name=e?.expression?.name;
      if (e?.nodeType==='Assignment') return assign(e,s);
      if(e?.nodeType==='UnaryOperation'&&['++','--'].includes(e.operator)) {
        const d=nodes.get(e.subExpression?.referencedDeclaration);
        if(e.subExpression?.nodeType!=='Identifier'||!d||d.stateVariable||!Object.hasOwn(s.parameter_expression_bindings,d.id))fail('INTERNAL_RETURN_ASSIGNMENT_TARGET_UNSUPPORTED');
        const one={nodeType:'Literal',kind:'number',value:'1',id:'increment:'+e.id,src:e.src,typeDescriptions:e.subExpression.typeDescriptions};
        keepConstant(d,one,s,e.operator==='++'?'+=':'-=');
        s.parameter_expression_bindings[d.id]=synthetic(e,'binary',[s.parameter_expression_bindings[d.id],synthetic(e,'literal',[],s,{value:'1',type:typeOf(d)})],s,{operator:e.operator==='++'?'+':'-',type:typeOf(d)});
        return [s];
      }
      if (e?.nodeType==='FunctionCall' && ['require','assert'].includes(name)) {
        s.return_conditions.push({expression_id:value(e.arguments[0],s),expected:true,failure:name==='assert'?'panic':'revert'}); return [s];
      }
      if (e?.nodeType==='FunctionCall' && name==='revert') return [];
    }
    fail('INTERNAL_RETURN_CONTROL_FLOW_UNSUPPORTED:'+n.nodeType);
  }
  let starts=[initial];
  for(const invocation of target.modifiers||[]) {
    const modifier=nodes.get(invocation.modifierName?.referencedDeclaration),statements=modifier?.body?.statements;
    if(!statements||statements.filter(n=>n.nodeType==='PlaceholderStatement').length!==1||statements.at(-1)?.nodeType!=='PlaceholderStatement')fail('INTERNAL_RETURN_MODIFIER_UNSUPPORTED');
    for(const p of starts)for(const [i,d]of (modifier.parameters?.parameters||[]).entries()) {
      p.parameter_expression_bindings[d.id]=value(invocation.arguments[i],p);p.parameter_arguments[d.id]=invocation.arguments[i];}
    for(const stmt of statements.slice(0,-1)){starts=starts.flatMap(p=>execute(stmt,p));if(results.length)fail('INTERNAL_RETURN_MODIFIER_UNSUPPORTED');}
  }
  const remaining=starts.flatMap(p=>execute(target.body,p));
  for (const s of remaining) {
    if (output.some(d=>!d.name||!s.parameter_expression_bindings[d.id])) fail('INTERNAL_RETURN_FALLTHROUGH_UNSUPPORTED');
    const values=output.map(d=>s.parameter_expression_bindings[d.id]);results.push({expression_id:values[0],expression_ids:values,conditions:s.return_conditions});
  }
  if (!results.length) fail('INTERNAL_RETURN_NO_SUCCESSFUL_PATH');
  const output_expression_ids=output.map((d,i)=>{const paths=results.map(r=>({...r,expression_id:r.expression_ids[i]}));return synthetic(call,'return_cases',paths.map(r=>r.expression_id),initial,{paths,type:typeOf(d)});});
  return {expression_id:output.length===1?output_expression_ids[0]:synthetic(call,'tuple',output_expression_ids,initial,{type:'tuple('+output.map(typeOf).join(',')+')'}),
    output_expression_ids,paths:results,loops:[...loops.values()],scope:output.length===1?'bounded_scalar_readonly_return_paths':'bounded_scalar_tuple_readonly_return_paths',all_execution_paths_verified:false};
}
