import {WORD_ARITIES} from './evm-word.mjs';

// Lower compiler-provided Yul into the same immutable local-variable executor.
// Storage, memory and host effects are rejected before a return can be credited.
export function lowerYul(assembly,{nodes,typeOf}) {
  if(!assembly.AST)throw new Error('INTERNAL_RETURN_YUL_AST_UNAVAILABLE');
  const refs=Array.isArray(assembly.externalReferences)?assembly.externalReferences:Object.entries(assembly.externalReferences||{}).map(([src,r])=>({src,...r}));
  let serial=0,visited=0;
  const fail=code=>{throw new Error('INTERNAL_RETURN_YUL_'+code);};
  const ast=(n,nodeType,extra={})=>({nodeType,id:'yul:'+assembly.id+':'+serial++,src:n.src||assembly.src,
    typeDescriptions:{typeString:'uint256'},...extra});
  const decl=(n,name)=>{const d=ast(n,'VariableDeclaration',{name,storageLocation:'default',stateVariable:false});nodes.set(d.id,d);return d;};
  function resolve(n,env) {
    if(env.has(n.name))return env.get(n.name);
    const ref=refs.find(r=>r.src===n.src),d=nodes.get(ref?.declaration);
    if(!d||ref.isSlot||ref.isOffset||ref.suffix||d.stateVariable||!['uint256','int256','bytes32'].includes(typeOf(d)))fail('REFERENCE_OR_DIRTY_BITS_UNSUPPORTED');
    return d;
  }
  const identifier=(n,d)=>ast(n,'Identifier',{name:d.name,referencedDeclaration:d.id,typeDescriptions:d.typeDescriptions});
  function expr(n,env) {
    if(!n||++visited>2048)fail('EXPRESSION_BUDGET');
    if(n.nodeType==='YulLiteral') {
      if(!['number','bool'].includes(n.kind))fail('LITERAL_UNSUPPORTED');
      return ast(n,'Literal',{kind:'number',value:n.kind==='bool'?(n.value==='true'?'1':'0'):n.value});
    }
    if(n.nodeType==='YulIdentifier')return identifier(n,resolve(n,env));
    if(n.nodeType==='YulFunctionCall') {
      const name=n.functionName.name,args=n.arguments.map(a=>expr(a,env));
      if(name==='mload'&&args.length===1)return ast(n,'YulMemoryLoad',{arguments:args});
      if(['mstore','mstore8'].includes(name)&&args.length===2)return ast(n,'YulMemoryStore',{arguments:args,memoryWidth:name==='mstore'?32:1});
      if(WORD_ARITIES[name]===args.length)return ast(n,'YulBuiltin',{functionName:{name},arguments:args});
      const fn=env.get('function:'+name);
      if(!fn)fail('BUILTIN_UNSUPPORTED:'+name);
      return ast(n,'FunctionCall',{kind:'functionCall',expression:identifier(n,fn),arguments:args});
    }
    fail('EXPRESSION_UNSUPPORTED:'+n.nodeType);
  }
  function block(n,outer,shared=false) {
    const env=shared?outer:new Map(outer),statements=[];
    for(const f of n.statements||[])if(f.nodeType==='YulFunctionDefinition') {
      const fn=ast(f,'FunctionDefinition',{name:f.name,visibility:'internal',stateMutability:'pure',modifiers:[]});nodes.set(fn.id,fn);env.set('function:'+f.name,fn);
    }
    for(const f of n.statements||[])if(f.nodeType==='YulFunctionDefinition') {
      const fn=env.get('function:'+f.name),local=new Map([...env].filter(([k])=>k.startsWith('function:')));
      const parameters=(f.parameters||[]).map(p=>{const d=decl(p,p.name);local.set(p.name,d);return d;});
      const returns=(f.returnVariables||[]).map(p=>{const d=decl(p,p.name);local.set(p.name,d);return d;});
      fn.parameters={parameters};fn.returnParameters={parameters:returns};fn.body=block(f.body,local);
      function memoryEffect(n){return ['YulMemoryLoad','YulMemoryStore'].includes(n.nodeType)||Object.values(n).some(v=>Array.isArray(v)?v.some(x=>x?.nodeType&&memoryEffect(x)):v?.nodeType&&memoryEffect(v));}
      if(memoryEffect(fn.body))fn.stateMutability='nonpayable';
    }
    for(const p of n.statements||[])if(p.nodeType!=='YulFunctionDefinition')statements.push(stmt(p,env));
    return ast(n,'Block',{statements});
  }
  function stmt(n,env) {
    if(++visited>2048)fail('STATEMENT_BUDGET');
    switch(n.nodeType) {
      case 'YulBlock':return block(n,env);
      case 'YulVariableDeclaration':{
        const initialValue=n.value?expr(n.value,env):null;
        const declarations=n.variables.map(v=>{const d=decl(v,v.name);env.set(v.name,d);return d;});
        return ast(n,'VariableDeclarationStatement',{declarations,initialValue});
      }
      case 'YulAssignment':{
        const lhs=n.variableNames.map(v=>identifier(v,resolve(v,env))),rhs=expr(n.value,env);
        return ast(n,'ExpressionStatement',{expression:ast(n,'Assignment',{operator:'=',leftHandSide:lhs.length===1?lhs[0]:ast(n,'TupleExpression',{components:lhs}),
          rightHandSide:lhs.length===1?ast(n,'YulWordCast',{expression:rhs,typeDescriptions:lhs[0].typeDescriptions}):rhs})});
      }
      case 'YulIf':return ast(n,'IfStatement',{condition:expr(n.condition,env),trueBody:block(n.body,env),falseBody:null});
      case 'YulSwitch':{
        const d=decl(n,'switch_value'),declaration=ast(n,'VariableDeclarationStatement',{declarations:[d],initialValue:expr(n.expression,env)});
        let body=n.cases.find(c=>c.value==='default');body=body?block(body.body,env):null;
        for(const c of [...n.cases].reverse())if(c.value!=='default')body=ast(c,'IfStatement',{
          condition:ast(c,'BinaryOperation',{operator:'==',leftExpression:identifier(c,d),rightExpression:expr(c.value,env),typeDescriptions:{typeString:'bool'}}),
          trueBody:block(c.body,env),falseBody:body});
        return ast(n,'Block',{statements:[declaration,...(body?[body]:[])]});
      }
      case 'YulForLoop':{
        const local=new Map(env),initializationExpression=block(n.pre,local,true);
        return ast(n,'ForStatement',{initializationExpression,condition:expr(n.condition,local),loopExpression:block(n.post,local),body:block(n.body,local)});
      }
      case 'YulBreak':return ast(n,'Break');case 'YulContinue':return ast(n,'Continue');case 'YulLeave':return ast(n,'Return',{expression:null});
      case 'YulExpressionStatement':{
        if(n.expression?.functionName?.name==='revert') {
          // Only the empty revert has no unread memory or argument effects.
          if(n.expression.arguments?.length!==2||n.expression.arguments.some(a=>a.nodeType!=='YulLiteral'||BigInt(a.value)!==0n))fail('REVERT_MEMORY_UNSUPPORTED');
          return ast(n,'RevertStatement');
        }
        return ast(n,'ExpressionStatement',{expression:expr(n.expression,env)});
      }
      default:fail('STATEMENT_UNSUPPORTED:'+n.nodeType);
    }
  }
  return block(assembly.AST,new Map());
}
