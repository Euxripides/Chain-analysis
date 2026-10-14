// Bounded symbolic execution of scalar, read-only internal return values.
// Compiler AST identities and per-path bindings prevent local variables from
// leaking between calls or branches. Unsupported effects fail the whole model.
export function summarizeInternalReturns({target, call, context, args, build, synthetic, nodes, typeOf}) {
  const fail = code => { throw new Error(code); };
  const output = target.returnParameters?.parameters || [];
  if (output.length !== 1 || !/^(?:u?int\d*|bool|address(?: payable)?|contract .+)$/.test(typeOf(output[0]))) fail('INTERNAL_RETURN_NONSCALAR_OR_TUPLE');
  if ((target.modifiers || []).length) fail('INTERNAL_RETURN_MODIFIER_UNSUPPORTED');
  if (!['pure','view'].includes(target.stateMutability) && !target.constant) fail('INTERNAL_RETURN_STATE_EFFECT_UNSUPPORTED');
  if ((target.parameters?.parameters || []).some(p => p.storageLocation === 'storage')) fail('INTERNAL_RETURN_STORAGE_ALIAS_UNSUPPORTED');
  const stack = context.return_stack || [];
  if (stack.length >= 12 || stack.includes(target.id)) fail('INTERNAL_RETURN_RECURSION_OR_DEPTH');
  let count = 0, serial = 0;
  const results = [], root = `${context.path}/return:${call.id}`;
  const bindings = {...context.parameter_expression_bindings};
  for (const [i,p] of (target.parameters?.parameters || []).entries()) bindings[p.id] = build(args[i], context);
  const initial = {...context, path:root, unchecked:false, return_stack:[...stack,target.id],
    parameter_arguments:{...context.parameter_arguments,...Object.fromEntries((target.parameters?.parameters || []).map((p,i)=>[p.id,args[i]]))},
    parameter_expression_bindings:bindings, return_conditions:[]};
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
  const fork = (s,suffix) => ({...s,path:s.path+'/'+suffix,parameter_expression_bindings:{...s.parameter_expression_bindings},return_conditions:[...s.return_conditions]});
  const defaultValue = (declaration,s) => {
    const type=typeOf(declaration);
    if (!/^(?:u?int\d*|bool|address(?: payable)?|contract .+)$/.test(type)) fail('INTERNAL_RETURN_LOCAL_TYPE_UNSUPPORTED');
    return synthetic(declaration,'literal',[],s,{value:type==='bool'?'false':/^(address|contract )/.test(type)?'0x'+'0'.repeat(40):'0',type});
  };
  if (output[0].name) initial.parameter_expression_bindings[output[0].id] = defaultValue(output[0],initial);
  function assign(n,s) {
    const lhs=n.leftHandSide,decl=nodes.get(lhs?.referencedDeclaration);
    if (lhs?.nodeType !== 'Identifier' || !decl || decl.stateVariable || !Object.hasOwn(s.parameter_expression_bindings,decl.id)) fail('INTERNAL_RETURN_ASSIGNMENT_TARGET_UNSUPPORTED');
    const rhs=value(n.rightHandSide,s);
    s.parameter_expression_bindings[decl.id]=n.operator==='='?rhs:synthetic(n,'binary',[s.parameter_expression_bindings[decl.id],rhs],s,{operator:n.operator.slice(0,-1),type:typeOf(lhs)});
    return [s];
  }
  function execute(n,s) {
    if (++count > 1024 || results.length > 32) fail('INTERNAL_RETURN_PATH_BUDGET');
    if (!n) return [s];
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
      const condition=value(n.condition,s),yes=fork(s,'if:'+n.id+':true'),no=fork(s,'if:'+n.id+':false');
      yes.return_conditions.push({expression_id:condition,expected:true}); no.return_conditions.push({expression_id:condition,expected:false});
      return [...execute(n.trueBody,yes),...execute(n.falseBody,no)];
    }
    if (n.nodeType==='VariableDeclarationStatement') {
      if (n.declarations?.length!==1 || !n.declarations[0]) fail('INTERNAL_RETURN_TUPLE_ASSIGNMENT_UNSUPPORTED');
      const decl=n.declarations[0];
      if (decl.storageLocation==='storage' || !/^(?:u?int\d*|bool|address(?: payable)?|contract .+)$/.test(typeOf(decl))) fail('INTERNAL_RETURN_LOCAL_TYPE_UNSUPPORTED');
      s.parameter_expression_bindings[decl.id]=n.initialValue?value(n.initialValue,s):defaultValue(decl,s);
      return [s];
    }
    if (n.nodeType==='Return') {
      const returned=n.expression?value(n.expression,s):s.parameter_expression_bindings[output[0].id];
      if (!returned) fail('INTERNAL_RETURN_VALUE_UNAVAILABLE');
      results.push({expression_id:returned,conditions:s.return_conditions}); return [];
    }
    if (n.nodeType==='RevertStatement') return [];
    if (n.nodeType==='ExpressionStatement') {
      const e=n.expression,name=e?.expression?.name;
      if (e?.nodeType==='Assignment') return assign(e,s);
      if (e?.nodeType==='FunctionCall' && ['require','assert'].includes(name)) {
        s.return_conditions.push({expression_id:value(e.arguments[0],s),expected:true,failure:name==='assert'?'panic':'revert'}); return [s];
      }
      if (e?.nodeType==='FunctionCall' && name==='revert') return [];
    }
    fail('INTERNAL_RETURN_CONTROL_FLOW_UNSUPPORTED:'+n.nodeType);
  }
  const remaining=execute(target.body,initial);
  for (const s of remaining) {
    if (!output[0].name || !s.parameter_expression_bindings[output[0].id]) fail('INTERNAL_RETURN_FALLTHROUGH_UNSUPPORTED');
    results.push({expression_id:s.parameter_expression_bindings[output[0].id],conditions:s.return_conditions});
  }
  if (!results.length) fail('INTERNAL_RETURN_NO_SUCCESSFUL_PATH');
  return {expression_id:synthetic(call,'return_cases',results.map(r=>r.expression_id),initial,{paths:results,type:typeOf(output[0])}),
    paths:results, scope:'bounded_scalar_readonly_return_paths', all_execution_paths_verified:false};
}
