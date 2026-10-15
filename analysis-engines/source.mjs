import { createRequire } from 'node:module';
import { hash, id, unique, makeMethod, evidence, claim } from './model.mjs';
import { legacyLayout } from './layout.mjs';
import { numericLiteral } from './literals.mjs';
import { verifySolidityRuntime } from './source-match.mjs';
import { summarizeInternalReturns } from './ir/internal-returns.mjs';
const require = createRequire(import.meta.url);
const {keccak_256}=require('@noble/hashes/sha3.js');
const compilers = new Set(Object.keys(require('./package.json').dependencies).filter(k => k.startsWith('solc-')).map(k => k.slice(5)));
const compilationCache=new Map();let cachedBytes=0;

export function compileSource(input, compilerVersion) {
  if(input?.language!=='Solidity')throw new Error('SOURCE_LANGUAGE_UNSUPPORTED');
  const version = compilerVersion.split('+')[0];
  if (!compilers.has(version)) throw new Error(`COMPILER_UNAVAILABLE:${version}`);
  const solc = require(`solc-${version}`);
  if (!solc.version().startsWith(compilerVersion)) throw new Error('COMPILER_IDENTITY_MISMATCH');
  const settings = structuredClone(input.settings || {});
  settings.outputSelection = { '*': { '*': ['abi', 'evm.deployedBytecode', 'evm.methodIdentifiers', 'storageLayout'], '': ['ast', 'legacyAST'] } };
  const payload = JSON.stringify({ ...input, settings });
  const cacheKey=hash(compilerVersion+'\n'+payload);if(compilationCache.has(cacheKey))return compilationCache.get(cacheKey).output;
  const compile = solc.compileStandardWrapper || solc.compile;
  const output = JSON.parse(compile.call(solc, payload));
  const errors = (output.errors || []).filter(e => e.severity === 'error');
  if (errors.length) throw new Error(`COMPILATION_FAILED:${errors.map(e => e.message).join('; ').slice(0, 500)}`);
  const size=Buffer.byteLength(JSON.stringify(output));if(size<=32<<20){while(compilationCache.size>=8||cachedBytes+size>64<<20){const key=compilationCache.keys().next().value;cachedBytes-=compilationCache.get(key).size;compilationCache.delete(key)}compilationCache.set(cacheKey,{output,size});cachedBytes+=size}
  return output;
}

export function analyzeSource(result, source, bytecode) {
  const started = performance.now();
  const output = compileSource(source.stdJsonInput, source.compilation.compilerVersion);
  const fq = source.compilation.fullyQualifiedName;
  const split = fq.lastIndexOf(':');
  const file = fq.slice(0, split), contractName = fq.slice(split + 1);
  const compiled = output.contracts?.[file]?.[contractName];
  if (!compiled) throw new Error('COMPILED_CONTRACT_NOT_FOUND');
  const runtimeVerification=verifySolidityRuntime(compiled.evm?.deployedBytecode||{},source,bytecode,source.compilation.compilerVersion,
    {isLibrary:output.sources?.[file]?.ast?.nodes?.some(n=>n.nodeType==='ContractDefinition'&&n.name===contractName&&n.contractKind==='library'),codeAddress:result.snapshot.code_address});
  result.sections.source_verification=[{verification_id:id(result.snapshot.code_hash,'source-verification'),compiler:source.compilation.compilerVersion,...runtimeVerification}];
  const sources = source.stdJsonInput.sources;
  const sourceBuffers=new Map(Object.entries(sources).map(([path,s])=>[path,Buffer.from(s.content||'')]));
  const textCache=new Map(),locationCache=new Map(),evidenceCache=new Map();
  const nodes = new Map(), fileIDs = new Map();
  for (const [path, unit] of Object.entries(output.sources || {})) {
    const ast = unit.ast;
    if (!ast?.nodeType) throw new Error('SOURCE_AST_UNAVAILABLE');
    fileIDs.set(unit.id, path);
    visit(ast, node => nodes.set(node.id, node));
  }
  const contracts = [...nodes.values()].filter(n => n.nodeType === 'ContractDefinition');
  const mainContract = contracts.find(n => n.name === contractName && fileIDs.get(Number(n.src.split(':')[2])) === file);
  if (!mainContract) throw new Error('SOURCE_CONTRACT_AST_UNAVAILABLE');
  const parents = new Set(mainContract.linearizedBaseContracts || [mainContract.id]);
  const orderedContracts = (mainContract.linearizedBaseContracts || [mainContract.id]).map(id => contracts.find(n => n.id === id)).filter(Boolean);
  const functions = orderedContracts.flatMap(n => (n.nodes || []).filter(n => n.nodeType === 'FunctionDefinition'));
  const stateVars = [...nodes.values()].filter(n => n.nodeType === 'VariableDeclaration' && n.stateVariable && parents.has(n.scope));
  const sourceText = node => {
    if (!node?.src) return '';
    if(textCache.has(node.src))return textCache.get(node.src);
    const [start, len, fileId] = node.src.split(':').map(Number);
    const path = fileIDs.get(fileId);
    const text=(sourceBuffers.get(path)||Buffer.alloc(0)).subarray(start,start+len).toString('utf8');textCache.set(node.src,text);return text;
  };
  const location = node => {
    const key=node.id+':'+node.src;if(locationCache.has(key))return locationCache.get(key);
    const [start, length, fileId] = node.src.split(':').map(Number);
    const path = fileIDs.get(fileId), text = sources[path]?.content || '';
    const line = (sourceBuffers.get(path)||Buffer.alloc(0)).subarray(0,start).toString('utf8').split('\n').length;
    const value={ artifact_id: id('source', path, hash(text)), file: path, start, length, line, ast_id: node.id };locationCache.set(key,value);return value;
  };
  const getEvidence = (method, node) => {const key=method.method_id+':'+node.id+':'+node.src;if(evidenceCache.has(key))return evidenceCache.get(key);
    const value=evidence(result,method,'source',location(node),sourceText(node).slice(0,2048));evidenceCache.set(key,value);return value;};
  const typeOf = node => node?.typeDescriptions?.typeString || sourceText(node?.typeName) || 'unresolved';
  const layout = compiled.storageLayout?.storage ? compiled.storageLayout : legacyLayout(orderedContracts,typeOf);
  const layoutByID = new Map((layout?.storage || []).map(v => [v.astId, v]));
  const runtimeValue=(declaration)=>{
    const binding=runtimeVerification.bindings[declaration?.id];if(!binding)return null;
    const type=typeOf(declaration),word=BigInt(binding.word_hex),integer=/^(u?int)(\d*)$/.exec(type);
    if(/^address|^contract /.test(type))return '0x'+(word&((1n<<160n)-1n)).toString(16).padStart(40,'0');
    if(type==='bool')return word===0n?'false':'true';
    if(integer){const bits=BigInt(integer[2]||256),mask=(1n<<bits)-1n;let n=word&mask;if(integer[1]==='int'&&(n&(1n<<(bits-1n))))n-=1n<<bits;return n.toString()}
    if(/^bytes\d+$/.test(type))return binding.word_hex.slice(0,2+Number(type.slice(5))*2);
    return null;
  };
  const resolveArgument=(node,context,seen=new Set())=>{
    if(!node||seen.has(node.id))return node;seen.add(node.id);
    const next=node.nodeType==='Identifier'?context.parameter_arguments?.[node.referencedDeclaration]:null;
    return next&&next.id!==node.id?resolveArgument(next,context,seen):node;
  };
  const internalArguments=(target,callee,args)=>{
    const parent=nodes.get(target?.scope),library=parent?.contractKind==='library';
    const direct=callee?.nodeType==='Identifier'||callee?.expression?.name==='super';
    const internal=target?.nodeType==='FunctionDefinition'&&target.body&&(direct||library&&['internal','private'].includes(target.visibility));
    const receiver=library&&callee?.nodeType==='MemberAccess'&&callee.expression?.referencedDeclaration!==parent.id;
    return {internal,args:receiver?[callee.expression,...args]:args};
  };
  const projectStorage = (node,context={},method) => {
    if(node?.nodeType==='Identifier') {
      const item=layoutByID.get(node.referencedDeclaration),type=layout.types?.[item?.type];
      return item&&type?{base_slot:item.slot,type_id:item.type,offset:item.offset||0,path:[],scalar_type:type.label}:null;
    }
    const parent=projectStorage(node?.baseExpression||node?.expression,context,method);if(!parent)return null;
    const type=layout.types?.[parent.type_id];if(!type)return null;
    if(node.nodeType==='IndexAccess'&&type.encoding==='mapping') {
      const child=layout.types?.[type.value];
      const key=resolveArgument(node.indexExpression,context);
      return {...parent,type_id:type.value,offset:0,scalar_type:child?.label,path:[...parent.path,{kind:'mapping',key_type:layout.types?.[type.key]?.label,key_ast_id:key?.id,key_referenced_declaration:key?.referencedDeclaration??null,key_text:sourceText(key),key_expression_id:expression(method,key,context),key_literal:key?.nodeType==='Literal'?String(key.value):null}]};
    }
    if(node.nodeType==='MemberAccess'&&type.members) {
      const field=type.members.find(m=>m.astId===node.referencedDeclaration||m.label===node.memberName),child=layout.types?.[field?.type];
      return field?{...parent,type_id:field.type,offset:field.offset||0,scalar_type:child?.label,path:[...parent.path,{kind:'member',name:field.label,relative_slot:field.slot,byte_offset:field.offset}]}:null;
    }
    return null;
  };
  const methodByAST = new Map();
  const dispatchedSelectors = new Set();
  for (const fn of functions) {
    if (!fn.implemented && !fn.body) continue;
    const canonical = n => typeOf(n).replace(/ (memory|storage|calldata)( ref| pointer)?/g, '').replace(/^contract [^\[\]\s]+/, 'address').replace(/^address payable/, 'address');
    const abiSignature = fn.name && ['external', 'public'].includes(fn.visibility) ? `${fn.name}(${(fn.parameters?.parameters || []).map(canonical).join(',')})` : '';
    const compiledSelector = compiled.evm?.methodIdentifiers?.[abiSignature];
    const selector = fn.functionSelector ? '0x' + fn.functionSelector : compiledSelector ? '0x' + compiledSelector : '';
    const overridden = Boolean(selector && dispatchedSelectors.has(selector));
    if (selector) dispatchedSelectors.add(selector);
    const method = makeMethod(result.snapshot, selector && !overridden ? `selector:${selector}` : `source:${fn.id}`, selector, fn.name || fn.kind || (fn.isConstructor ? 'constructor' : 'fallback'));
    method.dispatch_role = overridden ? 'overridden_source_definition' : selector ? 'external_dispatch' : 'source_definition';
    method.visibility = fn.visibility;
    method.entry_kind = fn.kind || (fn.isConstructor ? 'constructor' : fn.name ? 'function' : 'fallback');
    method.code_phase=method.entry_kind==='constructor'?'creation_source_not_verified_against_deployment':'runtime_source';
    method.state_mutability = fn.stateMutability || (fn.payable ? 'payable' : fn.constant ? 'view' : 'nonpayable');
    const signature = Object.entries(compiled.evm?.methodIdentifiers || {}).find(([, value]) => selector === '0x' + value)?.[0];
    method.signature = signature || `${method.name}(${(fn.parameters?.parameters || []).map(typeOf).join(',')})`;
    method.verified_signature = Boolean(selector && signature);
    method.parameters = (fn.parameters?.parameters || []).map((n, index) => ({ index, source_ast_id: n.id, name: n.name || `arg${index}`, type: typeOf(n),
      unit: null, unit_status: 'not_inferred_from_name', constraints: [], evidence_ids: [getEvidence(method, n)] }));
    method.outputs = (fn.returnParameters?.parameters || []).map((n, index) => ({ index, name: n.name, type: typeOf(n), evidence_ids: [getEvidence(method, n)] }));
    method.completeness = { complete: false, unresolved: [], basis: 'source', scope: 'syntactic_control_and_data_flow', execution_paths: 'not_exhaustively_verified' };
    method.source_location = location(fn);
    getEvidence(method, fn);
    result.sections.methods.push(method);
    methodByAST.set(fn.id, method);
  }
  let expressionNumber = 0;
  let syntheticNumber = 0;
  const expressionByID=new Map();
  const synthetic = (method,node,format,operands,context,extra={}) => {
    const expression_id=id(method.method_id,node.id,context.path,'return-model',syntheticNumber++);
    const type=extra.type||typeOf(node),integer=/^(u?int)(\d*)$/.exec(type);
    result.sections.expressions.push({expression_id,method_id:method.method_id,op:'InternalReturnModel',symbolic_format:format,
      text:sourceText(node),operands,path:context.path,conditions:[...(context.conditions||[])],
      width:integer?Number(integer[2]||256):null,signed:integer?integer[1]==='int':null,type,
      arithmetic:context.unchecked||Number(source.compilation.compilerVersion.split('.')[1])<8?'wrap':'checked',
      evidence_ids:[getEvidence(method,node)],...extra});
    expressionByID.set(expression_id,result.sections.expressions.at(-1));
    if (++expressionNumber>50000) throw new Error('EXPRESSION_BUDGET_EXCEEDED');
    return expression_id;
  };
  const expression = (method, node, context = {}) => {
    if (!node) return null;
    const type = typeOf(node), integer = /\b(u?int)(\d*)\b/.exec(type);
    const expression_id = id(method.method_id, node.id, node.src, context.path, context.unchecked || false);
    if (expressionByID.has(expression_id)) return expression_id;
    const operands = children(node).filter(n => !['TypeName', 'VariableDeclaration'].some(t => n.nodeType?.endsWith(t)))
      .map(n => expression(method, n, context)).filter(Boolean);
    const bound=node.nodeType==='Identifier'?context.parameter_arguments?.[node.referencedDeclaration]:null;
    const binding_expression_id=node.nodeType==='Identifier'&&context.parameter_expression_bindings?.[node.referencedDeclaration]||
      (bound&&bound.id!==node.id?expression(method,bound,context):null);
    if(binding_expression_id)operands.push(binding_expression_id);
    const row = { expression_id, method_id: method.method_id, op: node.operator || node.nodeType,
      text: node.entry_argument?node.name:sourceText(node).slice(0, 4096), type, width: integer ? Number(integer[2] || '256') : null,
      signed: integer ? integer[1] === 'int' : null,
      arithmetic: integer ? node.kind==='typeConversion'?'explicit_cast_truncation':(context.unchecked || Number(source.compilation.compilerVersion.split('.')[1]) < 8 ? 'wrap' : 'checked') : 'not_applicable',
      rounding: node.operator === '/' ? 'toward_zero' : null, operands, referenced_declaration: node.referencedDeclaration ?? null,call_kind:node.kind||null,
      path: context.path, conditions: [...(context.conditions || [])], step_order: method.step_ids.length,entry_return_summary:context.entry_return_summary||false,
      evidence_ids: [getEvidence(method, node)] };
    if(node.nodeType==='Assignment'||node.nodeType==='UnaryOperation'&&['++','--'].includes(node.operator))
      row.unresolved_evaluation_effect='LOCAL_EXPRESSION_EFFECT_REQUIRES_ORDERED_RETURN_MODEL';
    if(node.nodeType==='YulBuiltin')Object.assign(row,{symbolic_format:'evm_word',operator:node.functionName.name,
      width:256,signed:false,arithmetic:'evm_word_wrap',rounding:['div','sdiv'].includes(node.functionName.name)?'toward_zero_zero_divisor_returns_zero':null});
    if(node.nodeType==='YulWordCast')Object.assign(row,{symbolic_format:'identity',arithmetic:'explicit_cast_truncation',yul_word_assignment:true});
    if(binding_expression_id){row.binding_expression_id=binding_expression_id;row.bound_argument=sourceText(bound)}
    if(node.nodeType==='FunctionCall') {row.argument_expression_ids=(node.arguments||[]).map(n=>expression(method,n,context));row.callee_expression_id=expression(method,node.expression,context);}
    if(node.nodeType==='FunctionCall'&&node.kind!=='typeConversion') {
      const target=nodes.get(node.expression?.referencedDeclaration);
      const {internal,args}=internalArguments(target,node.expression,node.arguments||[]);
      if(internal) {
        try {
          const model=summarizeInternalReturns({target,call:node,context,args,nodes,typeOf,
            build:(n,c)=>expression(method,n,c),synthetic:(n,f,o,c,x)=>synthetic(method,n,f,o,c,x)});
          row.binding_expression_id=model.expression_id;row.internal_return_model=model;
          row.binding_kind='function_return';row.operands.push(row.binding_expression_id);
          for(const missing of model.frontier||[])result.sections.frontier.push({frontier_id:id(method.method_id,'internal-loop-remainder',node.id,missing.ast_id,missing.conditions),method_id:method.method_id,
            ...missing,evidence_ids:[getEvidence(method,node)]});
        } catch(error) { row.unresolved_return_value=String(error.message).startsWith('INTERNAL_RETURN_')?String(error.message):'INTERNAL_FUNCTION_RETURN_REQUIRES_CONTROL_FLOW'; }
      } else if(target?.nodeType==='FunctionDefinition') {
        row.unresolved_return_value='EXTERNAL_FUNCTION_RETURN_NOT_BOUND_TO_EXECUTION';
        const dependency_id=id(expression_id,'external-return'),callsite=location(node);
        const delegate=nodes.get(target.scope)?.contractKind==='library';
        const normalize=d=>typeOf(d).replace(/ (memory|storage|calldata)( ref| pointer)?/g,'').replace(/^contract [^\[\]\s]+/,'address').replace(/^address payable/,'address');
        const signature=target.name+'('+(target.parameters?.parameters||[]).map(normalize).join(',')+')';
        const dependency={dependency_id,method_id:method.method_id,expression_id,source_callsite:callsite,
          target_expression_id:expression(method,node.expression?.expression,context),target_expression:sourceText(node.expression?.expression),
          signature,selector:target.functionSelector?'0x'+target.functionSelector:null,
          arguments:row.argument_expression_ids,return_types:(target.returnParameters?.parameters||[]).map(normalize),
          caller_code_address:method.code_address,caller_storage_address:method.storage_address,
          callee_msg_sender:delegate?'inherited_msg_sender':method.storage_address,callee_msg_value:delegate?'inherited_msg_value':'0',
          callee_storage_context:delegate?method.storage_address:'resolved_target_address',
          call_type:delegate?'DELEGATECALL':['pure','view'].includes(target.stateMutability)&&Number(source.compilation.compilerVersion.split('.')[1])>=5?'STATICCALL':'CALL',
          conditions:[...(context.conditions||[])],path:context.path,state_scope:'call_execution_state',
          status:'requires_target_identity_and_execution',evidence_ids:row.evidence_ids};
        result.sections.external_returns||=[];result.sections.external_returns.push(dependency);row.external_return_dependency_id=dependency_id;
      }
    }
    if (node.nodeType === 'Identifier') {
      const declaration = nodes.get(node.referencedDeclaration);
      const immutable=runtimeValue(declaration);
      if(immutable!==null){row.resolved_value=immutable;row.value_basis='compiler_immutable_reference_and_snapshot_bytes';row.runtime_ranges=runtimeVerification.bindings[declaration.id].ranges}
      else if(declaration?.constant&&declaration.value){row.binding_expression_id=expression(method,declaration.value,context);row.binding_kind='constant_value';row.operands.push(row.binding_expression_id)}
      row.unresolved_local_reference = declaration?.nodeType === 'VariableDeclaration' && !declaration.stateVariable &&
        !method.parameters.some(p => p.source_ast_id === declaration.id) && !binding_expression_id;
    }
    if (node.nodeType === 'Literal') {
      row.raw_literal=String(node.value??node.hexValue??'');row.literal_kind=node.kind;row.subdenomination=node.subdenomination||null;
      if(node.kind==='number')Object.assign(row,numericLiteral(row.raw_literal,row.subdenomination));else row.value=row.raw_literal;
    }
    result.sections.expressions.push(row);
    expressionByID.set(expression_id,row);
    if (++expressionNumber > 50000) throw new Error('EXPRESSION_BUDGET_EXCEEDED');
    return expression_id;
  };
  const stateAccess = (method, node, access, context, valueNode, operation = null) => {
    const base = rootDeclaration(node), variable = nodes.get(base);
    if (!variable?.stateVariable || variable.constant || variable.mutability === 'immutable') return;
    const evidence_id = getEvidence(method, node), slot = layoutByID.get(base);
    const storage_id = id(method.method_id, node.id, access, context.path);
    if (result.sections.storage.some(s => s.storage_id === storage_id)) return;
    const state_id = id(result.snapshot.code_hash, result.snapshot.storage_address, 'state', base);
    const accessText=node.nodeType==='Identifier'?node.name:sourceText(node);
    const baseOnly=accessText!==variable.name||/^(mapping|struct)/.test(typeOf(variable))||typeOf(variable).includes('[');
    const row = { storage_id, state_id, state_ast_id:base, method_id: method.method_id, variable: variable.name,
      code_address: result.snapshot.code_address, storage_address: result.snapshot.storage_address,
      access, slot: slot?.slot ?? null, offset: slot?.offset ?? null, type: typeOf(node), variable_type: typeOf(variable),
      slot_role:baseOnly?'layout_base':'direct_slot',key_expression: accessText, key_expression_id: expression(method, node, context),
      value_expression: valueNode ? sourceText(valueNode) : null, value_expression_id: expression(method, valueNode, context), operation,
      conditions: [...context.conditions], evidence_ids: [evidence_id], layout_status: slot ? slot.layout_basis || 'verified_compiler_layout' : 'layout_unavailable',
      storage_projection: projectStorage(node,context,method) };
    result.sections.storage.push(row);
    method.storage_ids.push(storage_id);
  };
  const addStep = (method, node, kind, text, context, extra = {}) => {
    const step_id = id(method.method_id, node.id, kind, context.path, method.step_ids.length);
    const row = { step_id, method_id: method.method_id, order: method.step_ids.length, kind, text,
      expression_id: expression(method, node, context), conditions: [...context.conditions], path: context.path,
      evidence_ids: [getEvidence(method, node)], ...extra };
    result.sections.steps.push(row); method.step_ids.push(step_id);
    claim(result, method, 'source_behavior', text, row.evidence_ids, `ast-${kind}`);
    return row;
  };
  const guard = (method, node, condition, context, reason) => {
    const guard_id = id(method.method_id, node.id, context.path, 'guard');
    result.sections.permissions.push({ guard_id, method_id: method.method_id,
      condition: sourceText(condition), expression_id: expression(method, condition, context),
      conditions: [...context.conditions], path: context.path, failure: reason, evidence_ids: [getEvidence(method, node)] });
    method.guard_ids.push(guard_id);
    addStep(method, node, 'guard', `检查 ${sourceText(condition)}；不满足时 ${reason}`, context, { guard_id });
    context.conditions.push(sourceText(condition));
  };
  const decodeEncodedTransfer=(encoded,context)=>{
    encoded=resolveArgument(encoded,context);
    if(encoded?.nodeType!=='FunctionCall'||encoded.expression?.expression?.name!=='abi')return null;
    const stringValue=(n,seen=new Set())=>{
      if(!n||seen.has(n.id)||seen.size>16)return null;const next=new Set([...seen,n.id]);
      if(n.nodeType==='Literal'&&n.kind==='string')return n.value;
      if(n.nodeType==='Identifier'){const d=nodes.get(n.referencedDeclaration);return d?.constant?stringValue(d.value,next):null;}
      if(n.nodeType==='FunctionCall'&&n.kind==='typeConversion'&&/^bytes(?: memory)?$/.test(typeOf(n)))return stringValue(n.arguments?.[0],next);
      return null;
    };
    const selectorValue=(n,seen=new Set())=>{
      if(!n||seen.has(n.id)||seen.size>16)return null;const next=new Set([...seen,n.id]);
      if(n.nodeType==='Identifier'){const d=nodes.get(n.referencedDeclaration);return d?.constant?selectorValue(d.value,next):null;}
      if(n.nodeType==='Literal'&&n.kind==='number'){try{const v=BigInt(numericLiteral(String(n.value),n.subdenomination).value);return v>=0n&&v<1n<<32n?'0x'+v.toString(16).padStart(8,'0'):null}catch{return null}}
      if(n.nodeType==='Literal'&&n.kind==='hexString'&&n.hexValue?.length===8)return '0x'+n.hexValue.toLowerCase();
      if(n.nodeType==='FunctionCall'&&n.kind==='typeConversion'&&typeOf(n)==='bytes4')return selectorValue(n.arguments?.[0],next);
      if(n.nodeType==='FunctionCall'&&n.expression?.name==='keccak256'){const value=stringValue(n.arguments?.[0]);return value===null?null:'0x'+Buffer.from(keccak_256(Buffer.from(value))).toString('hex').slice(0,8);}
      return null;
    };
    let selector,args;
    const encoding=encoded.expression.memberName;
    if(encoding==='encodeWithSelector'){selector=selectorValue(encoded.arguments?.[0]);args=encoded.arguments?.slice(1);}
    else if(encoding==='encodeWithSignature'){const signature=stringValue(encoded.arguments?.[0]);if(signature===null)return null;selector='0x'+Buffer.from(keccak_256(Buffer.from(signature))).toString('hex').slice(0,8);args=encoded.arguments.slice(1);}
    else if(encoding==='encodeCall'){
      const fn=nodes.get(encoded.arguments?.[0]?.referencedDeclaration);args=encoded.arguments?.[1]?.components;
      if(fn?.nodeType!=='FunctionDefinition'||!Array.isArray(args))return null;
      const signature=fn.name+'('+(fn.parameters?.parameters||[]).map(typeOf).join(',')+')';selector='0x'+Buffer.from(keccak_256(Buffer.from(signature))).toString('hex').slice(0,8);
    }else return null;
    const signatures={'0xa9059cbb':'transfer(address,uint256)','0x23b872dd':'transferFrom(address,address,uint256)','0x095ea7b3':'approve(address,uint256)'},signature=signatures[selector];
    if(!signature||args?.length!==(selector==='0x23b872dd'?3:2)||args.some(n=>!n))return null;
    if(args.slice(0,-1).some(n=>!/^(?:address(?: payable)?|contract .+)$/.test(typeOf(n))))return null;
    const amount=args.at(-1),literal=amount.nodeType==='Literal'&&amount.kind==='number'?numericLiteral(String(amount.value),amount.subdenomination).value:null;
    if(!/^uint\d+$/.test(typeOf(amount))&&!(literal!==null&&BigInt(literal)>=0n&&BigInt(literal)<1n<<256n))return null;
    return {selector,signature,args,name:signature.split('(')[0],encoding,node:encoded};
  };
  const analyzeCall = (method, node, context) => {
    let callee = node.expression, options = {};
    if (callee?.nodeType === 'FunctionCallOptions') { options = Object.fromEntries((callee.names || []).map((n, i) => [n, callee.options[i]])); callee = callee.expression; }
    const name = callee?.name || callee?.memberName;
    const baseType = typeOf(callee?.expression);
    if (['require', 'assert'].includes(name)) { guard(method, node, node.arguments?.[0], context, name === 'assert' ? '触发 panic' : 'revert'); return; }
    if (name === 'revert') { const step = addStep(method, node, 'revert', `回滚：${sourceText(node)}`, context); method.failure_ids.push(step.step_id); return; }
    const targetNode = nodes.get(callee?.referencedDeclaration);
    const library=nodes.get(targetNode?.scope)?.contractKind==='library';
    if (targetNode?.nodeType === 'EventDefinition') { eventStep(method,node,node,context,targetNode); return; }
    const internal=internalArguments(targetNode,callee,node.arguments||[]);
    if (internal.internal) {
      let called = methodByAST.get(targetNode.id);
      if(!called) {
        called=makeMethod(result.snapshot,'source-library:'+targetNode.id,'',targetNode.name);
        Object.assign(called,{signature:targetNode.name+'('+(targetNode.parameters?.parameters||[]).map(typeOf).join(',')+')',visibility:'internal',entry_kind:'inlined_library_function',
          dispatch_role:'source_definition',code_phase:'runtime_source',source_location:location(targetNode),state_mutability:targetNode.stateMutability,
          parameters:(targetNode.parameters?.parameters||[]).map((p,index)=>({index,name:p.name,source_ast_id:p.id,type:typeOf(p),evidence_ids:[getEvidence(called,p)]})),
          completeness:{complete:false,unresolved:[],basis:'source',scope:'inlined_library_body',execution_paths:'not_exhaustively_verified'}});
        methodByAST.set(targetNode.id,called);result.sections.methods.push(called);
      }
      if (called) {
        const callStep = addStep(method, node, 'internal_call', `调用内部方法 ${called.signature}`, context, { target_method_id: called.method_id });
        result.sections.flows.push({ edge_id: id(callStep.step_id, 'internal'), edge_type: 'calls', from: method.method_id, to: called.method_id, evidence_ids: callStep.evidence_ids });
        if (context.stack.length < 12 && !context.stack.includes(targetNode.id)) {
          if((targetNode.parameters?.parameters||[]).some(p=>p.storageLocation==='storage'))method.completeness.unresolved.push({code:'LIBRARY_STORAGE_REFERENCE_REQUIRES_ALIAS_ANALYSIS',ast_id:node.id});
          const callContext={ ...context, conditions:[...context.conditions],unchecked:false,function_frame:targetNode.id, path: `${context.path}/call:${node.id}`, stack: [...context.stack, targetNode.id],
            parameter_bindings:Object.fromEntries((targetNode.parameters?.parameters||[]).map((p,i)=>[p.id,sourceText(internal.args[i])])),
            parameter_arguments:{...context.parameter_arguments,...Object.fromEntries((targetNode.parameters?.parameters||[]).map((p,i)=>[p.id,internal.args[i]]))},
            parameter_expression_bindings:{...context.parameter_expression_bindings,...Object.fromEntries((targetNode.parameters?.parameters||[]).map((p,i)=>[p.id,expression(method,internal.args[i],context)]))} };
          analyzeFunctionWithModifiers(method,targetNode,callContext);
          let branched=false;for(const body of [targetNode.body,...(targetNode.modifiers||[]).map(m=>nodes.get(m.modifierName?.referencedDeclaration)?.body)])visit(body,n=>{if(['IfStatement','ForStatement','WhileStatement','DoWhileStatement','TryStatement'].includes(n.nodeType))branched=true});
          if(!branched)context.conditions.push(...callContext.conditions.filter(c=>!context.conditions.includes(c)));
        } else method.completeness.unresolved.push({ code: 'RECURSIVE_OR_DEEP_INTERNAL_CALL', ast_id: node.id });
      }
      return;
    }
    if(['selfdestruct','suicide'].includes(name)&&callee?.nodeType==='Identifier'&&/t_function_(selfdestruct|suicide)_/.test(callee.typeDescriptions?.typeIdentifier||'')) {
      const to=sourceText(node.arguments?.[0]),step=addStep(method,node,'selfdestruct',`SELFDESTRUCT：将合约原生币余额交给 ${to}`,context);
      const fund_id=id(method.method_id,node.id,context.path,'selfdestruct');
      result.sections.funds.push({fund_id,method_id:method.method_id,kind:'selfdestruct_balance_transfer',asset:'native',unit:'wei',from_expression:'address(this)',to_expression:to,amount_expression:'address(this).balance',settlement:'static_intent',execution_rule_dependency:'EIP-6780',conditions:[...context.conditions],evidence_ids:step.evidence_ids});method.fund_ids.push(fund_id);return;
    }
    if (node.kind === 'typeConversion') return;
    if (callee?.nodeType !== 'MemberAccess' && node.kind !== 'structConstructorCall') {
      if (callee?.nodeType === 'NewExpression') addExternal('CREATE', null);
      return;
    }
    if (callee?.nodeType !== 'MemberAccess') return;
    if (/\b(abi|type)\b/.test(baseType) || ['encode', 'encodePacked', 'encodeWithSelector', 'encodeWithSignature', 'decode', 'concat'].includes(name)) return;
    const external = library || /\b(contract|address)\b/.test(baseType) || ['call', 'delegatecall', 'staticcall', 'callcode', 'send', 'transfer'].includes(name);
    if (!external) return;
    let kind = name === 'delegatecall' ? 'DELEGATECALL' : name === 'staticcall' || targetNode?.stateMutability === 'view' || targetNode?.stateMutability === 'pure' ? 'STATICCALL' : 'CALL';
    if (name === 'callcode') kind = 'CALLCODE';
    if(library)kind='DELEGATECALL';
    addExternal(kind, callee.expression);
    function addExternal(kind, target) {
      const call_id = id(method.method_id, node.id, context.path, 'external');
      const args = (node.arguments || []).map(n => sourceText(n));
      const libraryName=library?nodes.get(targetNode.scope).name:null;
      const libraryFile=library?fileIDs.get(Number(nodes.get(targetNode.scope).src.split(':')[2])):null;
      const target_expression = library?runtimeVerification.libraries[libraryFile+':'+libraryName]||sourceText(target):sourceText(target);
      const canonicalTypes=(targetNode?.parameters?.parameters||[]).map(p=>typeOf(p).replace(/ (memory|storage|calldata)( ref| pointer)?/g,'').replace(/^contract .+$/,'address').replace(/^address payable$/,'address'));
      const target_signature = targetNode?.nodeType === 'FunctionDefinition' ? `${targetNode.name}(${canonicalTypes.join(',')})` : '';
      const derivedSelector=target_signature&&canonicalTypes.every(t=>/^(?:u?int(?:8|16|24|32|40|48|56|64|72|80|88|96|104|112|120|128|136|144|152|160|168|176|184|192|200|208|216|224|232|240|248|256)|address|bool|string|bytes(?:[1-9]|[12]\d|3[0-2])?)(?:\[\d*\])*$/.test(t))?'0x'+Buffer.from(keccak_256(Buffer.from(target_signature))).toString('hex').slice(0,8):'';
      const call = { call_id, method_id: method.method_id, call_type: kind, target_expression,
        target_expression_id: expression(method, target, context), selector: targetNode?.functionSelector ? '0x' + targetNode.functionSelector : derivedSelector,
        signature: target_signature, input_arguments: args, input_expression: sourceText(node),
        value_expression: options.value ? sourceText(options.value) : null, value_expression_id: expression(method, options.value, context),
        input_expression_ids:(node.arguments||[]).map(n=>expression(method,n,context)),
        failure_propagation: ['call', 'send', 'delegatecall', 'staticcall', 'callcode'].includes(name) ? 'returns_success_flag' : 'reverts_on_failure',
        conditions: [...context.conditions], parameter_bindings: context.parameter_bindings || {}, evidence_ids: [getEvidence(method, node)] };
      result.sections.calls.push(call); method.call_ids.push(call_id);
      addStep(method, node, 'external_call', `${kind} 调用 ${target_expression || '新合约'}：${sourceText(node)}`, context, { call_id });
      let fund;
      const decoded=name==='call'&&kind==='CALL'?decodeEncodedTransfer(node.arguments?.[0],context):null;
      const isNative = /address/.test(baseType) && ['transfer', 'send'].includes(name);
      if ((isNative || options.value) && kind === 'CALL') {
        const amount = isNative ? node.arguments?.[0] : options.value;
        fund = { kind: 'native_payment', asset: 'native', from_expression: 'address(this)', to_expression: target_expression,
          to_expression_id:expression(method,target,context),
          amount_expression: sourceText(amount), amount_expression_id: expression(method, amount, context), unit: 'wei' };
      } else if (targetNode?.nodeType === 'FunctionDefinition' && ['transfer', 'transferFrom', 'approve'].includes(name)) {
        const params = targetNode.parameters?.parameters || [];
        const paramTypes = params.map(typeOf);
        const compatible = name === 'transferFrom' ? paramTypes.length === 3 && paramTypes[0] === 'address' && paramTypes[1] === 'address' && paramTypes[2] === 'uint256' : paramTypes.length === 2 && paramTypes[0] === 'address' && paramTypes[1] === 'uint256';
        if (compatible && /contract/.test(baseType)) {
          const returnsBool=(targetNode.returnParameters?.parameters||[]).some(p=>typeOf(p)==='bool');
          fund = { kind: name === 'transfer' ? 'token_transfer_intent' : name === 'transferFrom' ? 'token_transfer_from_intent' : 'token_approval',
            asset: target_expression, from_expression: name === 'transferFrom' ? args[0] : 'address(this)',
            to_expression: name === 'transferFrom' ? args[1] : args[0], amount_expression: args.at(-1),
            amount_expression_id: expression(method, node.arguments?.at(-1), context), unit: returnsBool?'token_raw':'asset_quantity_or_token_id',
            asset_expression_id:expression(method,target,context),from_expression_id:name==='transferFrom'?expression(method,node.arguments[0],context):null,
            to_expression_id:expression(method,node.arguments[name==='transferFrom'?1:0],context),
            protocol: 'transfer_interface',unit_basis:'caller_declared_interface',asset_standard_verified:false, settlement: 'requires_execution_evidence' };
        }
      } else if(decoded) {
        call.selector=decoded.selector;call.signature=decoded.signature;call.calldata_basis='verified_source_abi_'+decoded.encoding;
        call.input_arguments=decoded.args.map(sourceText);call.input_expression_ids=decoded.args.map(n=>expression(method,n,context));
        call.evidence_ids=unique([...call.evidence_ids,getEvidence(method,decoded.node)]);
        fund={kind:decoded.name==='transferFrom'?'token_transfer_from_intent':decoded.name==='approve'?'token_approval':'token_transfer_intent',
          asset:target_expression,asset_expression_id:expression(method,target,context),from_expression:decoded.name==='transferFrom'?sourceText(decoded.args[0]):'address(this)',
          from_expression_id:decoded.name==='transferFrom'?expression(method,decoded.args[0],context):null,to_expression:sourceText(decoded.args[decoded.name==='transferFrom'?1:0]),
          to_expression_id:expression(method,decoded.args[decoded.name==='transferFrom'?1:0],context),amount_expression:sourceText(decoded.args.at(-1)),amount_expression_id:expression(method,decoded.args.at(-1),context),
          unit:'asset_quantity_or_token_id',unit_basis:'verified_source_encoding_with_unverified_asset_standard',asset_standard_verified:false,settlement:'requires_execution_evidence',protocol:'encoded_transfer_interface'};
      }
      if (fund) {
        const fund_id = id(call_id, 'fund');
        result.sections.funds.push({ fund_id, method_id: method.method_id, call_id, conditions: [...context.conditions],
          parameter_bindings: context.parameter_bindings || {}, evidence_ids: call.evidence_ids, ...fund });
        method.fund_ids.push(fund_id);
      }
    }
  };
  const scanExpression = (method, node, context, access = 'read') => {
    if (!node) return;
    if (node.nodeType === 'Assignment') {
      scanExpression(method, node.leftHandSide, context, node.operator === '=' ? 'write' : 'readwrite');
      stateAccess(method, node.leftHandSide, 'write', context, node.rightHandSide, node.operator);
      scanExpression(method, node.rightHandSide, context);
      const assigned = addStep(method, node, 'assignment', `执行 ${sourceText(node)}`, context);
      if (node.leftHandSide.nodeType === 'Identifier' && !nodes.get(node.leftHandSide.referencedDeclaration)?.stateVariable) {
        result.sections.definitions.push({ definition_id: id(assigned.step_id, 'definition'), method_id: method.method_id,
          declaration_id: node.leftHandSide.referencedDeclaration, name: node.leftHandSide.name,
          value_expression_id: expression(method, node.rightHandSide, context), operation: node.operator,
          order: assigned.order, conditions: [...context.conditions], path: context.path, evidence_ids: assigned.evidence_ids });
      } else if(node.leftHandSide.nodeType==='TupleExpression'&&node.operator==='=') {
        const rhs=expression(method,node.rightHandSide,context);
        for(const [i,p]of (node.leftHandSide.components||[]).entries())if(p?.nodeType==='Identifier'&&!nodes.get(p.referencedDeclaration)?.stateVariable) {
          result.sections.definitions.push({definition_id:id(assigned.step_id,'tuple-definition',i),method_id:method.method_id,declaration_id:p.referencedDeclaration,name:p.name,
            value_expression_id:synthetic(method,node,'tuple_component',[rhs],context,{component_index:i,type:typeOf(p)}),operation:'=',
            order:assigned.order,conditions:[...context.conditions],path:context.path,evidence_ids:assigned.evidence_ids});}
      }
      return;
    }
    if (node.nodeType === 'UnaryOperation' && ['++', '--', 'delete'].includes(node.operator)) {
      stateAccess(method, node.subExpression, 'write', context, node, node.operator);
      if (node.operator !== 'delete') stateAccess(method, node.subExpression, 'read', context);
      addStep(method, node, 'assignment', `执行 ${sourceText(node)}`, context); return;
    }
    if (['Identifier', 'IndexAccess', 'MemberAccess'].includes(node.nodeType)) {
      if (access !== 'write') stateAccess(method, node, 'read', context);
      if (node.nodeType === 'IndexAccess') scanExpression(method, node.indexExpression, context);
    }
    for (const child of children(node)) scanExpression(method, child, context, access);
    if (node.nodeType === 'FunctionCall') analyzeCall(method, node, context);
  };
  const analyzeBlock = (method, node, context) => {
    if (!node) return;
    if (node.nodeType === 'Block' || node.nodeType === 'UncheckedBlock') {
      for (const stmt of node.statements || []) {
        const flow=analyzeBlock(method, stmt, { ...context, unchecked: context.unchecked || node.nodeType === 'UncheckedBlock' });
        if(flow)return flow;
      }
      return;
    }
    if (node.nodeType === 'IfStatement') {
      const condition = sourceText(node.condition);
      scanExpression(method, node.condition, context);
      addStep(method, node.condition, 'branch', `按条件 ${condition} 选择分支`, context);
      const yes=analyzeBlock(method, node.trueBody, { ...context, path: `${context.path}/if:${node.id}:true`, conditions: [...context.conditions, condition] });
      const no=analyzeBlock(method, node.falseBody, { ...context, path: `${context.path}/if:${node.id}:false`, conditions: [...context.conditions, `!(${condition})`] });
      if(yes&&no)return yes===no?yes:'terminated';
      if(yes)context.conditions.push(`!(${condition})`);
      if(no)context.conditions.push(condition);
      return;
    }
    if (['ForStatement', 'WhileStatement', 'DoWhileStatement'].includes(node.nodeType)) {
      const condition = sourceText(node.condition);
      addStep(method, node, 'loop', `循环条件 ${condition || '无显式条件'}`, context);
      scanExpression(method, node.initializationExpression, context);
      scanExpression(method, node.condition, context);
      analyzeBlock(method, node.body, { ...context, path: `${context.path}/loop:${node.id}`, conditions: [...context.conditions, `循环：${condition}`] });
      scanExpression(method, node.loopExpression, context);
      return;
    }
    if (node.nodeType === 'Return') {
      scanExpression(method, node.expression, context);
      const step = addStep(method, node, 'return', `返回 ${sourceText(node.expression) || '已声明的返回变量'}`, context,
        {return_scope:context.function_frame===method.source_location?.ast_id?'entry':'internal',return_frame:context.function_frame});
      method.return_ids.push(step.step_id); return 'return';
    }
    if (node.nodeType === 'EmitStatement') { const callee=node.eventCall?.expression;eventStep(method,node,node.eventCall,context,nodes.get(callee?.referencedDeclaration)); return; }
    if (node.nodeType === 'RevertStatement') { const step = addStep(method, node, 'revert', `回滚 ${sourceText(node.errorCall)}`, context); method.failure_ids.push(step.step_id); return 'revert'; }
    if (node.nodeType === 'InlineAssembly' || node.nodeType === 'TryStatement') {
      addStep(method, node, node.nodeType === 'InlineAssembly' ? 'assembly' : 'try', sourceText(node).slice(0, 2048), context);
      if(node.nodeType==='InlineAssembly')analyzeEncodedAssemblyCall(method,node,context);
      method.completeness.unresolved.push({ code: node.nodeType === 'InlineAssembly' ? 'ASSEMBLY_SEMANTICS_UNRESOLVED' : 'TRY_CATCH_PATHS_UNRESOLVED', ast_id: node.id });
      if (node.nodeType === 'TryStatement') for (const child of children(node)) analyzeBlock(method, child, context);
      return;
    }
    if (node.nodeType === 'VariableDeclarationStatement') {
      scanExpression(method, node.initialValue, context);
      const declared = addStep(method, node, 'local', `计算局部变量：${sourceText(node)}`, context);
      if (node.initialValue && node.declarations?.length === 1 && node.declarations[0]) {
        result.sections.definitions.push({ definition_id: id(declared.step_id, 'definition'), method_id: method.method_id,
          declaration_id: node.declarations[0].id, name: node.declarations[0].name,
          value_expression_id: expression(method, node.initialValue, context), operation: '=',
          order: declared.order, conditions: [...context.conditions], path: context.path, evidence_ids: declared.evidence_ids });
      } else if(node.initialValue&&node.declarations?.length>1) {
        const whole=expression(method,node.initialValue,context);
        for(const [i,p]of node.declarations.entries())if(p)result.sections.definitions.push({definition_id:id(declared.step_id,'tuple-definition',i),method_id:method.method_id,declaration_id:p.id,name:p.name,
          value_expression_id:synthetic(method,node.initialValue,'tuple_component',[whole],context,{component_index:i,type:typeOf(p)}),operation:'=',
          order:declared.order,conditions:[...context.conditions],path:context.path,evidence_ids:declared.evidence_ids});
      }
      return;
    }
    if (node.nodeType === 'ExpressionStatement') { scanExpression(method, node.expression, context);const name=node.expression?.expression?.name;return name==='revert'?'revert':['selfdestruct','suicide'].includes(name)&&/t_function_(selfdestruct|suicide)_/.test(node.expression?.expression?.typeDescriptions?.typeIdentifier||'')?'jump':undefined; }
    if (['Break', 'Continue', 'PlaceholderStatement'].includes(node.nodeType)) { addStep(method, node, 'control', sourceText(node), context); return node.nodeType==='PlaceholderStatement'?undefined:'jump'; }
    for (const child of children(node)) scanExpression(method, child, context);
  };
  function analyzeEncodedAssemblyCall(method,node,context) {
    // Recognize a direct CALL of unmodified abi.encodeCall bytes. Other assembly
    // paths stay unresolved; calldata construction alone never becomes a transfer.
    const first=node.AST?.statements?.[0],call=first?.value;
    if(call?.nodeType!=='YulFunctionCall'||call.functionName?.name!=='call'||call.arguments?.length!==7)return;
    const a=call.arguments,zero=a[2]?.nodeType==='YulLiteral'&&BigInt(a[2].value||'0')===0n;
    if(!zero||a[1]?.nodeType!=='YulIdentifier'||a[3]?.functionName?.name!=='add'||a[4]?.functionName?.name!=='mload')return;
    const ptr=a[3].arguments,len=a[4].arguments;
    if(ptr?.length!==2||len?.length!==1||ptr[0].nodeType!=='YulIdentifier'||len[0].name!==ptr[0].name||ptr[1]?.nodeType!=='YulLiteral'||BigInt(ptr[1].value||'0')!==32n)return;
    const references=Array.isArray(node.externalReferences)?node.externalReferences:Object.entries(node.externalReferences||{}).map(([src,v])=>({src,...v}));
    const external=y=>{const ref=references.find(r=>r.src===y.src);if(!ref||ref.isSlot||ref.isOffset)return null;
      return resolveArgument({nodeType:'Identifier',id:'yul:'+y.src,src:y.src,name:y.name,referencedDeclaration:ref.declaration,typeDescriptions:nodes.get(ref.declaration)?.typeDescriptions},context)};
    const target=external(a[1]),encoded=external(ptr[0]);
    if(!target||encoded?.nodeType!=='FunctionCall'||encoded.expression?.memberName!=='encodeCall'||encoded.expression.expression?.name!=='abi')return;
    const declared=nodes.get(encoded.arguments?.[0]?.referencedDeclaration),args=encoded.arguments?.[1]?.components;
    const types=(declared?.parameters?.parameters||[]).map(typeOf),name=declared?.name;
    const compatible=name==='transferFrom'?types.join(',')==='address,address,uint256':types.join(',')==='address,uint256';
    if(!['transfer','transferFrom','approve'].includes(name)||!compatible||!Array.isArray(args)||args.length!==types.length)return;
    const call_id=id(method.method_id,node.id,context.path,'encoded-assembly-call'),eid=getEvidence(method,node),encodingEvidence=getEvidence(method,encoded);
    result.sections.calls.push({call_id,method_id:method.method_id,call_type:'CALL',target_expression:sourceText(target),target_expression_id:expression(method,target,context),
      selector:'0x'+declared.functionSelector,signature:name+'('+types.join(',')+')',input_arguments:args.map(sourceText),input_expression_ids:args.map(n=>expression(method,n,context)),
      calldata_basis:'typed_abi_encodeCall_bound_to_direct_assembly_call',failure_propagation:'assembly_paths_require_execution',conditions:[...context.conditions],evidence_ids:[eid,encodingEvidence]});
    method.call_ids.push(call_id);
    const fund_id=id(call_id,'fund');
    result.sections.funds.push({fund_id,method_id:method.method_id,call_id,kind:name==='transfer'?'token_transfer_intent':name==='transferFrom'?'token_transfer_from_intent':'token_approval',
      asset:sourceText(target),asset_expression_id:expression(method,target,context),from_expression:name==='transferFrom'?sourceText(args[0]):'address(this)',
      from_expression_id:name==='transferFrom'?expression(method,args[0],context):null,to_expression:sourceText(args[name==='transferFrom'?1:0]),to_expression_id:expression(method,args[name==='transferFrom'?1:0],context),
      amount_expression:sourceText(args.at(-1)),amount_expression_id:expression(method,args.at(-1),context),unit:'token_raw',unit_basis:'caller_declared_interface',asset_standard_verified:false,
      settlement:'requires_execution_evidence',conditions:[...context.conditions],evidence_ids:[eid,encodingEvidence]});method.fund_ids.push(fund_id);
  }
  function eventStep(method,node,call,context,definition) {
    const parameters=definition?.parameters?.parameters || [];
    const signature=definition ? `${definition.name}(${parameters.map(typeOf).join(',')})` : '';
    addStep(method,node,'event',`发出事件 ${sourceText(call)}`,context,{event_name:definition?.name,event_signature:signature,
      event_arguments:(call?.arguments || []).map(sourceText),event_argument_ids:(call?.arguments || []).map(n=>expression(method,n,context)),
      event_indexed:parameters.map(p=>Boolean(p.indexed))});
  }
  function analyzeFunctionWithModifiers(method,fn,context) {
    const modifiers = (fn.modifiers || []).map(m => ({ invocation: m, definition: nodes.get(m.modifierName?.referencedDeclaration) })).filter(m => m.definition);
    // Expand modifier wrappers at each placeholder, preserving post-body modifiers and conditional execution.
    const execute = (index, activeContext = context) => {
      if (index >= modifiers.length) return analyzeBlock(method, fn.body, activeContext);
      const { invocation, definition } = modifiers[index];
      const body = definition.body;
      const expand = (n, ctx) => {
        if (!n) return;
        if (n.nodeType === 'PlaceholderStatement') {
          const flow=execute(index + 1,{...ctx,path:`${ctx.path}/placeholder:${n.id}`});
          if(flow==='revert')return flow;
          if(flow==='terminated')method.completeness.unresolved.push({code:'MODIFIER_TERMINATION_PATHS',ast_id:n.id});
          return;
        }
        if (n.nodeType === 'Block') { for (const stmt of n.statements || []) { const flow=expand(stmt, ctx);if(flow)return flow } return; }
        if (n.nodeType === 'IfStatement') {
          scanExpression(method, n.condition, ctx);
          const cond = sourceText(n.condition);
          const yes=expand(n.trueBody, { ...ctx, path:`${ctx.path}/if:${n.id}:true`,conditions: [...ctx.conditions, cond] });
          const no=expand(n.falseBody, { ...ctx, path:`${ctx.path}/if:${n.id}:false`,conditions: [...ctx.conditions, `!(${cond})`] });
          if(yes&&no)return yes===no?yes:'terminated';
          if(yes)ctx.conditions.push(`!(${cond})`);
          if(no)ctx.conditions.push(cond);
          return;
        }
        return analyzeBlock(method, n, ctx);
      };
      return expand(body, { ...activeContext, path: `${activeContext.path}/modifier:${definition.id}`, parameter_bindings: { ...activeContext.parameter_bindings, ...Object.fromEntries((definition.parameters?.parameters || []).map((p, i) => [p.id, sourceText(invocation.arguments?.[i])])) },parameter_arguments:{...activeContext.parameter_arguments,...Object.fromEntries((definition.parameters?.parameters || []).map((p,i)=>[p.id,invocation.arguments?.[i]]))} });
    };
    return execute(0);
  }
  for (const fn of functions) {
    const method = methodByAST.get(fn.id);
    if (!method) continue;
    const context = { path: 'entry', conditions: [], stack: [fn.id], function_frame:fn.id, unchecked: false,legacy_arithmetic:Number(source.compilation.compilerVersion.split('.')[1])<8 };
    analyzeFunctionWithModifiers(method,fn,context);
    if(['view','pure'].includes(method.state_mutability)&&(fn.returnParameters?.parameters||[]).length) {
      try {
        const args=(fn.parameters?.parameters||[]).map(p=>({...p,nodeType:'Identifier',id:'entry-argument:'+p.id,referencedDeclaration:p.id,entry_argument:true}));
        const call={...fn,id:'entry-return:'+fn.id};
        const model=summarizeInternalReturns({target:fn,call,context:{...context,entry_return_summary:true},args,nodes,typeOf,
          build:(n,c)=>expression(method,n,c),synthetic:(n,f,o,c,x)=>synthetic(method,n,f,o,c,x)});
        const row=result.sections.expressions.find(e=>e.expression_id===model.expression_id);
        row.entry_return_model=model;method.entry_return_expression_id=model.expression_id;
        for(const missing of model.frontier||[])result.sections.frontier.push({frontier_id:id(method.method_id,'loop-remainder',missing.ast_id,missing.conditions),method_id:method.method_id,
          ...missing,evidence_ids:[getEvidence(method,fn)]});
      }catch(error){method.entry_return_model_status='partial';method.entry_return_model_unresolved=[String(error.message)];}
    }
    method.completeness.complete = method.completeness.unresolved.length === 0;
  }
  for (const [signature, value] of Object.entries(compiled.evm?.methodIdentifiers || {})) {
    const selector = '0x' + value;
    if (result.sections.methods.some(m => m.selector === selector)) continue;
    const name = signature.split('(')[0], variable = stateVars.find(v => v.name === name && v.visibility === 'public');
    if (!variable) continue;
    const method = makeMethod(result.snapshot, `selector:${selector}`, selector, name);
    method.signature = signature; method.verified_signature = true; method.visibility = 'external'; method.entry_kind = 'generated_getter'; method.state_mutability = 'view';
    method.completeness = { complete: true, unresolved: [], basis: 'source', scope: 'compiler_generated_getter', execution_paths: 'not_exhaustively_verified' };
    const abi = compiled.abi.find(a => a.type === 'function' && a.name === name);
    const eid = getEvidence(method, variable);
    method.parameters = (abi?.inputs || []).map((p, index) => ({ index, name: p.name || `arg${index}`, type: p.type, unit: null, evidence_ids: [eid] }));
    method.outputs = (abi?.outputs || []).map((p, index) => ({ index, name: p.name, type: p.type, evidence_ids: [eid] }));
    const context = { path: 'getter', conditions: [], unchecked: false };
    if (!variable.constant && variable.mutability !== 'immutable') stateAccess(method, { ...variable, nodeType: 'Identifier', referencedDeclaration: variable.id }, 'read', context);
    const step = addStep(method, variable, 'return', `读取并返回 ${name}${method.parameters.length ? ' 对应键值' : ''}`, context);
    method.return_ids.push(step.step_id); result.sections.methods.push(method);
  }
  for (const [path, value] of Object.entries(sources)) result.sections.artifacts.push({ artifact_id: id('source', path, hash(value.content)),
    kind: 'verified_source', filename: path, media_type: 'text/plain; charset=utf-8', text: value.content, sha256: hash(value.content) });
  result.sections.artifacts.push({ artifact_id: id('layout', result.snapshot.code_hash), kind: 'storage_layout', filename: 'storage-layout.json', media_type: 'application/json', text: JSON.stringify(layout || {}), sha256: hash(JSON.stringify(layout || {})) });
  result.sections.engines.push({ engine: 'solc-ast', version: source.compilation.compilerVersion, adapter_version: 'source-1',
    status: 'completed', duration_ms: Math.round(performance.now() - started), methods: methodByAST.size, source_verification: 'recompiled_and_snapshot_matched' });
  result.sections.coverage.push({ dimension: 'source', complete: true, basis: 'recompiled_runtime_and_verified_snapshot', methods: methodByAST.size });
  if(layout.basis)result.sections.coverage.push({dimension:'storage_layout',basis:layout.basis,complete:layout.unresolved.length===0,unresolved:layout.unresolved});
}

function children(node) {
  return Object.entries(node || {}).filter(([key]) => !['typeDescriptions', 'documentation'].includes(key)).flatMap(([, value]) =>
    Array.isArray(value) ? value.filter(v => v?.nodeType) : value?.nodeType ? [value] : []);
}
function visit(node, fn) { if (!node?.nodeType) return; fn(node); for (const child of children(node)) visit(child, fn); }
function rootDeclaration(node) {
  if (!node) return null;
  if (node.nodeType === 'Identifier') return node.referencedDeclaration;
  if (node.nodeType === 'IndexAccess') return rootDeclaration(node.baseExpression);
  if (node.nodeType === 'MemberAccess') return rootDeclaration(node.expression);
  return null;
}
