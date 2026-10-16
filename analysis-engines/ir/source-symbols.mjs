export function unparenthesized(node){let n=node;for(let i=0;i<12&&n?.nodeType==='TupleExpression'&&n.components?.length===1;i++)n=n.components[0];return n;}
// A spelling alone never identifies a compiler-provided symbol. Both negative
// and legacy unsigned builtin declaration IDs lie outside the compiled AST.
export function builtin(node,name,nodes){const n=unparenthesized(node);return n?.nodeType==='Identifier'&&n.name===name&&!nodes.has(n.referencedDeclaration);}
export function builtinMember(node,nodes){
 if(node?.nodeType!=='MemberAccess')return null;const base=unparenthesized(node.expression);
 return ['msg','tx','block','abi'].some(name=>builtin(base,name,nodes))?base.name+'.'+node.memberName:null;
}
export function internalDispatch(definition,callee,nodes){
 if(definition?.nodeType!=='FunctionDefinition'||!definition.body)return false;
 const parent=nodes.get(definition.scope);
 if(parent?.contractKind==='library'&&!['internal','private'].includes(definition.visibility))return false;
 if(callee?.nodeType==='Identifier')return true;
 if(callee?.nodeType!=='MemberAccess')return false;
 const receiver=unparenthesized(callee.expression);
 return builtin(receiver,'super',nodes)||nodes.get(receiver?.referencedDeclaration)?.nodeType==='ContractDefinition'||parent?.contractKind==='library';
}
