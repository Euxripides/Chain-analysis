import { createRequire } from 'node:module';
import { hash, id, unique, makeMethod, evidence, claim } from './model.mjs';
import { legacyLayout } from './layout.mjs';
import { numericLiteral } from './literals.mjs';
import { verifySolidityRuntime } from './source-match.mjs';
import { summarizeInternalReturns } from './ir/internal-returns.mjs';
import { addSourcePCMap } from './source-pc.mjs';
import { analyzeLoopInvariants } from './verification/loops.mjs';
import { analyzeStateModels,buildCreationTransition } from './verification/state.mjs';
import { analyzePolicies } from './verification/policies.mjs';
import { executeAssembly } from './verification/assembly.mjs';
import {builtin,builtinMember,internalDispatch} from './ir/source-symbols.mjs';
import { sectionCheckpoint } from './section-checkpoint.mjs';
import {SourceByteMemory,sourceBytePredicateAnd}from'./ir/source-memory.mjs';
import {SourceScalars}from'./ir/source-scalars.mjs';
import {SourceAggregates}from'./ir/source-aggregates.mjs';
import{scalarDefaultLiteral,scalarAssignmentConversion,tupleComponentType}from'./ir/scalar-casts.mjs';
import {parseAbiType,encodeAbi,decodeAbi,AbiFault}from'./ir/abi.mjs';
import {storageTypeRegistry,aggregateStorageType,storageRoot,storageMember,storageIndex,StorageReferences}from'./ir/storage-reference.mjs';
import {collectNamespaces,bindNamespaceAccessors,activateNamespaceLayout,namespaceAccessPath,namespaceCalleeID,namespacePointerType} from './namespace-layout.mjs';
const require = createRequire(import.meta.url);
const {keccak_256}=require('@noble/hashes/sha3.js');
const compilers = new Set(Object.keys(require('./package.json').dependencies).filter(k => k.startsWith('solc-')).map(k => k.slice(5)));
const compilationCache=new Map();let cachedBytes=0;
export const SUBMITTED_TEMPLATE=Symbol('isolated_submitted_compilation');

export function compileSource(input, compilerVersion) {
  if(input?.language!=='Solidity')throw new Error('SOURCE_LANGUAGE_UNSUPPORTED');
  const version = compilerVersion.split('+')[0];
  if (!compilers.has(version)) throw new Error(`COMPILER_UNAVAILABLE:${version}`);
  const solc = require(`solc-${version}`);
  if (!solc.version().startsWith(compilerVersion)) throw new Error('COMPILER_IDENTITY_MISMATCH');
  const settings = structuredClone(input.settings || {});
  settings.outputSelection = { '*': { '*': ['abi', 'evm.bytecode', 'evm.deployedBytecode', 'evm.methodIdentifiers', 'storageLayout'], '': ['ast', 'legacyAST'] } };
  const payload = JSON.stringify({ ...input, settings });
  const cacheKey=hash(compilerVersion+'\n'+payload);if(compilationCache.has(cacheKey))return compilationCache.get(cacheKey).output;
  const compile = solc.compileStandardWrapper || solc.compile;
  const output = JSON.parse(compile.call(solc, payload));
  const errors = (output.errors || []).filter(e => e.severity === 'error');
  if (errors.length) {const error=new Error(`COMPILATION_FAILED:${errors.map(e => e.message).join('; ').slice(0, 500)}`);error.diagnostics=output.errors||[];throw error;}
  const size=Buffer.byteLength(JSON.stringify(output));if(size<=32<<20){while(compilationCache.size>=8||cachedBytes+size>64<<20){const key=compilationCache.keys().next().value;cachedBytes-=compilationCache.get(key).size;compilationCache.delete(key)}compilationCache.set(cacheKey,{output,size});cachedBytes+=size}
  return output;
}

export function analyzeSource(result, source, bytecode, options = {}) {
  const started = performance.now();
  const sourceDeadline = Math.min(started + (options.timeoutMs ?? 90000), options.deadline ?? Infinity);
  const totalExpressionLimit = Math.min(options.totalExpressions ?? 50000, 50000);
  const methodExpressionLimit = Math.min(options.methodExpressions ?? 8000, totalExpressionLimit);
  const failures = [];
  const sourceMemoryBudget={objects:0,cells:0,versions:0,maxObjects:1024,maxCells:262144,maxVersions:4096};
  const storageReferenceBudget={count:0,max:4096};
  const scalarBudget={versions:0,maxVersions:4096,maxPerMethod:512};
  const aggregateBudget={versions:0,records:0,maxVersions:2048,maxPerMethod:256,maxRecords:8192,maxFields:32,maxDepth:4};
  const output = compileSource(source.stdJsonInput, source.compilation.compilerVersion);
  const fq = source.compilation.fullyQualifiedName;
  const split = fq.lastIndexOf(':');
  const file = fq.slice(0, split), contractName = fq.slice(split + 1);
  const compiled = output.contracts?.[file]?.[contractName];
  if (!compiled) throw new Error('COMPILED_CONTRACT_NOT_FOUND');
  const runtimeVerification=source[SUBMITTED_TEMPLATE]===true&&result.snapshot.identity_kind==='code_only'?source.inputVerification:verifySolidityRuntime(compiled.evm?.deployedBytecode||{},source,bytecode,source.compilation.compilerVersion,
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
  const enumValues=new Map();for(const definition of nodes.values())if(definition.nodeType==='EnumDefinition')for(const[index,member]of(definition.members||[]).entries())enumValues.set(member.id,{definition,index,count:definition.members.length,member});
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
  const baseLayout = compiled.storageLayout?.storage ? compiled.storageLayout : legacyLayout(orderedContracts,typeOf,nodes);
  const namespaces=collectNamespaces({nodes,orderedContracts,layout:baseLayout}),namespaceAccessors=bindNamespaceAccessors({functions,nodes,namespaces});
  const namespaceRegistry=activateNamespaceLayout(baseLayout,namespaces,namespaceAccessors),layout=namespaceRegistry.layout;
  const storageTypes=storageTypeRegistry(layout,nodes);
  // Publish these only after compiler and runtime identity have been verified,
  // and before any method can exhaust its analysis budget.
  for (const [path, value] of Object.entries(sources)) result.sections.artifacts.push({ artifact_id: id('source', path, hash(value.content)),
    kind: source.origin==='submitted_source'?'submitted_source':'verified_source', filename: path, media_type: 'text/plain; charset=utf-8', text: value.content, sha256: hash(value.content) });
  const layoutArtifact = { artifact_id: id('layout', result.snapshot.code_hash), kind: 'storage_layout', filename: 'storage-layout.json', media_type: 'application/json', text: JSON.stringify(layout || {}), sha256: hash(JSON.stringify(layout || {})) };
  result.sections.artifacts.push(layoutArtifact);
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
    const internal=internalDispatch(target,callee,nodes);
    const receiver=library&&callee?.nodeType==='MemberAccess'&&callee.expression?.referencedDeclaration!==parent.id;
    return {internal,args:receiver?[callee.expression,...args]:args};
  };
  const namespacePath=(node,context={})=>namespaceAccessPath(node,namespaceRegistry,{
    aliases:new Map([...Object.entries(context.parameter_arguments||{}).map(([key,value])=>[Number(key),value]),...(context.namespace_aliases||[])]),
    resolveFunction:n=>namespaceCalleeID(n,nodes,functions)
  });
  const projectStorageCases = (node,context={},method,seen=new Set()) => {
    if(!node||seen.has(node))return[];seen=new Set([...seen,node]);
    if(node.nodeType==='Identifier'&&context.storage_references?.bindings.has(node.referencedDeclaration)){
      const value=context.storage_references.get(node.referencedDeclaration);
      return context.storage_references.resolve(value).map(({case:c,record})=>({projection:record.projection,conditions:c.conditions,
        storage_reference_ids:[record.storage_reference_id],unresolved:unique([...(value.unresolved||[]),...(record.unresolved||[])])}));
    }
    if(node.nodeType==='FunctionCall'){
      const returned=context.storage_references?.shared.callReturns.get(expression(method,node,context))?.components?.[0];
      if(returned)return context.storage_references.resolve(returned).map(({case:c,record})=>({projection:record.projection,conditions:c.conditions,
        storage_reference_ids:[record.storage_reference_id],unresolved:unique([...(returned.unresolved||[]),...(record.unresolved||[])])}));
    }
    if(node.nodeType==='Conditional'){
      const value=context.storage_references?.shared.expressionBindings.get(expression(method,node,context));
      if(value)return context.storage_references.resolve(value).map(({case:c,record})=>({projection:record.projection,conditions:c.conditions,
        storage_reference_ids:[record.storage_reference_id],unresolved:unique([...(value.unresolved||[]),...(record.unresolved||[])])}));
    }
    const ns=namespacePath(node,context);
    if(ns){let projection={root_state_ast_id:ns.root,base_slot:ns.base_slot,type_id:ns.type_id,offset:ns.offset,scalar_type:ns.scalar_type,
      namespace_id:ns.namespace_id,namespace_struct_ast_id:ns.namespace_struct_ast_id,namespace_root:ns.namespace_root,fields:ns.fields||[],static_slot:ns.static_slot,path:ns.path.map(part=>{
        if(part.kind!=='mapping')return part;const key=resolveArgument(part.key_node,context);
        return{kind:'mapping',key_type:part.key_type,key_ast_id:key?.id,key_referenced_declaration:key?.referencedDeclaration??null,
          key_text:sourceText(key),key_expression_id:expression(method,key,context),key_literal:key?.nodeType==='Literal'&&key.kind==='number'?numericLiteral(String(key.value),key.subdenomination).value:null};
      }),bounds:[],slot_expression:{op:'constant',value:ns.base_slot},offset_expression:{op:'constant',value:String(ns.offset||0)}};
      // Reconstruct the physical path from its verified pointer root, retaining
      // declaration and captured key identities rather than hashing text.
      let physical={...projection,type_id:namespaceRegistry.namespaces.get(ns.namespace_struct_ast_id)?.type_id||ns.type_id,
        root_state_ast_id:ns.namespace_struct_ast_id,namespace_root:true,path:[],static_slot:ns.base_slot,offset:0,offset_expression:{op:'constant',value:'0'}};
      for(const part of projection.path){
        if(part.kind==='member')physical=storageMember(physical,layout.types[physical.type_id]?.members?.find(f=>f.astId===part.field_ast_id),layout.types);
        else if(part.kind==='mapping')physical=storageIndex(physical,part,layout.types);
        if(!physical)break;
      }
      if(physical)projection={...projection,...physical};
      return[{projection,conditions:[],storage_reference_ids:[],unresolved:projection.unresolved||[]}];
    }
    if(node?.nodeType==='Identifier') {
      const projection=storageRoot(layoutByID.get(node.referencedDeclaration),layout.types);
      return projection?[{projection,conditions:[],storage_reference_ids:[],unresolved:[]}]:[];
    }
    const cases=projectStorageCases(node.baseExpression||node.expression,context,method,seen),out=[];
    for(const item of cases){
      const parent=item.projection,type=layout.types[parent.type_id];let projection=null;
      if(node.nodeType==='IndexAccess'){
        const key=resolveArgument(node.indexExpression,context);
        projection=storageIndex(parent,{key_ast_id:key?.id,key_referenced_declaration:key?.referencedDeclaration??null,key_text:sourceText(key),key_expression_id:expression(method,key,context),
          key_literal:key?.nodeType==='Literal'&&key.kind==='number'?numericLiteral(String(key.value),key.subdenomination).value:null},layout.types);
      }else if(node.nodeType==='MemberAccess'&&type?.members){
        projection=storageMember(parent,type.members.find(m=>m.astId===node.referencedDeclaration),layout.types);
      }
      if(projection)out.push({...item,projection,unresolved:unique([...item.unresolved,...(projection.unresolved||[])])});
    }
    return out;
  };
  const projectStorage=(node,context={},method)=>{const cases=projectStorageCases(node,context,method);return cases.length===1?cases[0].projection:null;};
  const pointerType=declaration=>namespacePointerType(declaration,namespaceRegistry)||storageTypes.pointerType(declaration);
  const resolveStorageReference=(method,node,context,component=null)=>{
    const tracker=context.storage_references;if(!tracker||!node)return{cases:[],unresolved:['STORAGE_REFERENCE_VALUE_UNAVAILABLE']};
    if(node.nodeType==='TupleExpression'&&component!==null)return resolveStorageReference(method,node.components?.[component],context);
    if(node.nodeType==='Identifier'&&tracker.bindings.has(node.referencedDeclaration))return tracker.get(node.referencedDeclaration);
    if(node.nodeType==='Conditional'&&tracker.shared.expressionBindings.has(expression(method,node,context)))return copyStorageBinding(tracker.shared.expressionBindings.get(expression(method,node,context)));
    if(node.nodeType==='FunctionCall'){
      const value=tracker.shared.callReturns.get(expression(method,node,context))?.components?.[component??0];if(value)return value;
    }
    const cases=projectStorageCases(node,context,method),values=cases.map(item=>tracker.capture(item.projection,{expression_id:expression(method,node,context),
      context:{path:context.path,order:method.step_ids.length,conditions:unique([...context.conditions,...item.conditions])},evidence_ids:[getEvidence(method,node)]}));
    return{cases:values.flatMap(v=>v.cases),unresolved:unique([...values.flatMap(v=>v.unresolved),...cases.flatMap(v=>v.unresolved),...(!cases.length?['STORAGE_REFERENCE_ORIGIN_UNAVAILABLE']:[])])};
  };
  const copyStorageBinding=value=>structuredClone(value);
  const bindStorageReference=(method,context,declaration,value,component=null)=>{
    if(!pointerType(declaration)||!context.storage_references)return;
    context.storage_references.bind(declaration.id,resolveStorageReference(method,value,context,component));
  };
  const collectStorageReturn=(method,fn,node,context)=>{
    if(!context.storage_references)return;
    for(const[component,p]of(fn?.returnParameters?.parameters||[]).entries())if(pointerType(p)){
      const value=node?resolveStorageReference(method,node,context,(fn.returnParameters.parameters.length>1)?component:null):context.storage_references.get(p.id);
      context.storage_references.returns.push({component,value,conditions:[...context.conditions]});
    }
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
  if(layout.namespace_storage){
    const metadata=layout.namespace_storage;
    for(const row of metadata.accessors){
      const method=methodByAST.get(row.function_ast_id);
      row.evidence_ids=method?unique([row.function_ast_id,row.namespace_struct_ast_id,row.assembly_ast_id,row.constant_ast_id]
        .filter(key=>key!==null&&key!==undefined&&nodes.has(key)).map(key=>getEvidence(method,nodes.get(key)))):[];
    }
    for(const row of metadata.namespaces)row.evidence_ids=unique(metadata.accessors.filter(a=>a.namespace_struct_ast_id===row.struct_ast_id).flatMap(a=>a.evidence_ids));
    metadata.unresolved=unique([...metadata.namespaces.flatMap(n=>[...n.unresolved,...(!n.pointer_assignment_verified?['NAMESPACE_POINTER_IDENTITY_UNVERIFIED:'+n.struct_ast_id]:[])]),
      ...metadata.accessors.flatMap(a=>a.unresolved.map(code=>code+':'+a.function_ast_id))]);
    metadata.complete=metadata.namespaces.every(n=>n.complete)&&metadata.accessors.every(a=>a.complete);
  }
  let expressionNumber = 0;
  let syntheticNumber = 0;
  let methodExpressionStart = 0, methodDeadline = Infinity;
  const expressionByID=new Map();
  const checkMethodTime = () => {
    if (performance.now() >= sourceDeadline) throw new Error('SOURCE_TOTAL_TIME_BUDGET_EXCEEDED');
    if (performance.now() >= methodDeadline) throw new Error('SOURCE_METHOD_TIME_BUDGET_EXCEEDED');
  };
  let enhancedReturnDepth=0;
  const requestedEnhancedReturnLimit=options.enhancedReturnExpressions??Math.floor(totalExpressionLimit/8);
  if(!Number.isSafeInteger(requestedEnhancedReturnLimit)||requestedEnhancedReturnLimit<0)throw new Error('SOURCE_ADDITIONAL_RETURN_BUDGET_INVALID');
  const enhancedReturnBudget={expressions:0,max:Math.min(requestedEnhancedReturnLimit,Math.floor(totalExpressionLimit/8))};
  const enhancedReturnTypes=new Map(),enhancedReturnFrontiers=new Set();
  const needsEnhancedReturn=target=>{
    if(enhancedReturnTypes.has(target.id))return enhancedReturnTypes.get(target.id);
    let needed=false;visit(target,node=>{const type=typeOf(node),identifier=node.typeDescriptions?.typeIdentifier||'';
      if(/\benum\s/.test(type)||/\bstruct\s.*\bmemory\b/.test(type)||/^t_struct\$_.*_memory(?:_ptr)?$/.test(identifier))needed=true;
    });enhancedReturnTypes.set(target.id,needed);return needed;
  };
  const boundedInternalReturns=(method,request)=>{
    const enhanced=enhancedReturnDepth>0||needsEnhancedReturn(request.target);
    try{
      if(enhanced&&enhancedReturnBudget.expressions>=enhancedReturnBudget.max)throw new Error('INTERNAL_RETURN_ADDITIONAL_EXPRESSION_BUDGET');
      if(enhanced)enhancedReturnDepth++;
      try{return summarizeInternalReturns(request);}finally{if(enhanced)enhancedReturnDepth--;}
    }catch(error){
      if(error.message==='INTERNAL_RETURN_ADDITIONAL_EXPRESSION_BUDGET'){
        const key=id(method.method_id,request.target.id,request.call.id,request.context.path,'enhanced-return-budget');
        if(!enhancedReturnFrontiers.has(key)){enhancedReturnFrontiers.add(key);result.sections.frontier.push({frontier_id:key,kind:'source_internal_return',method_id:method.method_id,code:error.message,target_declaration_id:request.target.id,source_callsite:location(request.call),path:request.context.path,conditions:[...(request.context.conditions||[])],budget:enhancedReturnBudget.max,consumed:enhancedReturnBudget.expressions,evidence_ids:[getEvidence(method,request.call)]});}
      }throw error;
    }
  };
  const consumeExpression = () => {
    checkMethodTime();
    if(enhancedReturnDepth){if(enhancedReturnBudget.expressions>=enhancedReturnBudget.max)throw new Error('INTERNAL_RETURN_ADDITIONAL_EXPRESSION_BUDGET');enhancedReturnBudget.expressions++;}
    if (++expressionNumber > totalExpressionLimit) throw new Error('SOURCE_TOTAL_EXPRESSION_BUDGET_EXCEEDED');
    if (expressionNumber - methodExpressionStart > methodExpressionLimit) throw new Error('SOURCE_METHOD_EXPRESSION_BUDGET_EXCEEDED');
  };
  const runMethod = (method, node, analyzeBody) => {
    const checkpoint = sectionCheckpoint(result, [method]);
    const expressionStart = result.sections.expressions.length, syntheticStart = syntheticNumber;
    const methodKeys = new Set(methodByAST.keys());
    methodExpressionStart = expressionNumber;
    methodDeadline = performance.now() + (options.methodTimeoutMs ?? 15000);
    try {
      checkMethodTime();
      if (expressionNumber >= totalExpressionLimit) throw new Error('SOURCE_TOTAL_EXPRESSION_BUDGET_EXCEEDED');
      analyzeBody();
      method.source_checkpoint = 'method_body_saved';
    } catch (error) {
      for (const row of result.sections.expressions.slice(expressionStart)) expressionByID.delete(row.expression_id);
      checkpoint.rollback();
      syntheticNumber = syntheticStart;
      for (const key of methodByAST.keys()) if (!methodKeys.has(key)) methodByAST.delete(key);
      evidenceCache.clear();
      const reason = /^SOURCE_(?:METHOD|TOTAL)_.*BUDGET_EXCEEDED$/.test(error.message) ? error.message : 'SOURCE_METHOD_ANALYSIS_FAILED';
      const failure = { code: reason, method_id: method.method_id, location: location(node),
        detail: String(error.message).slice(0, 500), scope: 'source_method_body',
        preserved: 'verified_compiler_entry_and_preceding_complete_methods' };
      method.completeness.complete = false;
      method.completeness.unresolved.push(failure);
      method.source_checkpoint = 'compiled_entry_only';
      failures.push(failure);
      result.sections.frontier.push({ frontier_id: id(method.method_id, reason), kind: 'source_method', ...failure,
        evidence_ids: method.evidence_ids });
    } finally {
      methodDeadline = Infinity;
    }
  };
  const synthetic = (method,node,format,operands,context,extra={}) => {
    const expression_id=id(method.method_id,node.id,context.path,'return-model',syntheticNumber++);
    const type=extra.type||typeOf(node),integer=/^(u?int)(\d*)$/.exec(type);
    result.sections.expressions.push({expression_id,method_id:method.method_id,op:'InternalReturnModel',symbolic_format:format,
      text:sourceText(node),operands,path:context.path,conditions:[...(context.conditions||[])],
      width:integer?Number(integer[2]||256):null,signed:integer?integer[1]==='int':null,type,
      arithmetic:context.unchecked||Number(source.compilation.compilerVersion.split('.')[1])<8?'wrap':'checked',
      evidence_ids:[getEvidence(method,node)],...extra});
    expressionByID.set(expression_id,result.sections.expressions.at(-1));
    consumeExpression();
    return expression_id;
  };
  const scalarDescriptor=declaration=>{
    if(declaration?.nodeType!=='VariableDeclaration'||declaration.stateVariable)return null;
    const type=typeOf(declaration);
    if(type.includes('[')||/\b(?:memory|storage|calldata)\b/.test(type))return null;
    return /^(?:u?int\d*|bool|address(?: payable)?|bytes(?:[1-9]|[12]\d|3[0-2]))$/.test(type)||/^(?:enum|contract)\s/.test(type)?{id:declaration.id,type}:null;
  };
  const scalarContext=(context,method)=>({path:context.path,conditions:context.conditions,order:method.step_ids.length});
  const scalarEffectContext=(method,context,node)=>({...scalarContext(context,method),evidence_ids:[getEvidence(method,node)]});
  const scalarZero=(method,declaration,context,node=declaration)=>synthetic(method,node,'literal',[],context,{type:typeOf(declaration),value:scalarDefaultLiteral(typeOf(declaration))});
  const aggregateType=node=>{
    if(!node)return null;const identifier=node.typeDescriptions?.typeIdentifier||'',memory=/^t_struct\$_.*_\$(\d+)_(memory|storage)(?:_ptr)?$/.exec(identifier),field=node.nodeType==='VariableDeclaration'&&nodes.get(node.scope)?.nodeType==='StructDefinition';
    if(!memory||memory[2]!=='memory'&&!field)return null;const definition=nodes.get(Number(memory[1]));
    if(definition?.nodeType!=='StructDefinition')return null;
    return{kind:'struct',declaration_id:definition.id,type:typeOf(node)};
  };
  const aggregateDescriptor=declaration=>declaration?.nodeType==='VariableDeclaration'&&!declaration.stateVariable&&aggregateType(declaration)?{id:declaration.id,aggregate_type:aggregateType(declaration)}:null;
  const aggregateContext=(method,node,context)=>({...scalarEffectContext(method,context,node),ast_id:node.id});
  const constructorHasEffects=node=>{let unsafe=false;visit(node,n=>{
    if(n.nodeType==='Assignment'||n.nodeType==='NewExpression'||n.nodeType==='UnaryOperation'&&['++','--','delete'].includes(n.operator)||n.nodeType==='FunctionCall'&&!['typeConversion','structConstructorCall'].includes(n.kind))unsafe=true;
  });return unsafe;};
  const constructorMayFail=(node,context)=>{let fallible=false;visit(node,n=>{
    if(n.nodeType==='IndexAccess'||n.nodeType==='BinaryOperation'&&(['/', '%'].includes(n.operator)||!context.unchecked&&Number(source.compilation.compilerVersion.split('.')[1])>=8&&['+','-','*','**'].includes(n.operator))||n.nodeType==='UnaryOperation'&&n.operator==='-'&&!context.unchecked&&Number(source.compilation.compilerVersion.split('.')[1])>=8||n.nodeType==='FunctionCall'&&n.kind==='typeConversion'&&/^enum\s/.test(typeOf(n)))fallible=true;
  });return fallible;};
  const aggregateExpressionReference=(key,seen=new Set())=>{
    if(!key||seen.has(key)||seen.size>=64)return null;seen=new Set([...seen,key]);const row=expressionByID.get(key);if(!row)return null;
    if(row.source_aggregate_reference)return structuredClone(row.source_aggregate_reference);
    if(row.binding_expression_id)return aggregateExpressionReference(row.binding_expression_id,seen);
    if(row.symbolic_format==='identity'&&row.operands?.length===1)return aggregateExpressionReference(row.operands[0],seen);
    if(row.symbolic_format==='tuple_component'){
      let whole=expressionByID.get(row.operands?.[0]);const walked=new Set();while(whole?.binding_expression_id&&!walked.has(whole.expression_id)){walked.add(whole.expression_id);whole=expressionByID.get(whole.binding_expression_id);}
      const component=whole?.tuple_component_expression_ids?.[row.component_index]||(whole?.symbolic_format==='tuple'?whole.operands?.[row.component_index]:null);return aggregateExpressionReference(component,seen);
    }
    return null;
  };
  const storageCopyScalar=member=>{
    const type=typeOf(member),integer=/^(u?int)(\d*)$/.exec(type),bytes=/^bytes([1-9]|[12]\d|3[0-2])$/.exec(type);
    if(integer)return{type,bits:Number(integer[2]||256),signed:integer[1]==='int',kind:'integer'};
    if(bytes)return{type,bits:Number(bytes[1])*8,signed:false,kind:'fixed_bytes'};
    if(type==='bool')return{type,bits:8,signed:false,kind:'bool'};
    if(/^enum\s/.test(type))return{type,bits:8,signed:false,kind:'enum'};
    if(/^address(?: payable)?$/.test(type)||/^contract\s/.test(type))return{type,bits:160,signed:false,kind:'address'};
    return null;
  };
  const storageCopyKeyKnown=(key,seen=new Set())=>{
    if(!key||seen.has(key)||seen.size>=64)return false;seen=new Set([...seen,key]);const row=expressionByID.get(key);if(!row)return false;
    if(row.unresolved_evaluation_effect||row.unresolved_return_value||row.source_scalar_binding_unavailable||row.source_scalar_unresolved?.length||row.unresolved_model_reasons?.length)return false;
    if(/^enum\s/.test(row.type||''))return false;
    if(row.op==='Identifier'&&nodes.get(row.referenced_declaration)?.stateVariable&&!nodes.get(row.referenced_declaration)?.constant&&nodes.get(row.referenced_declaration)?.mutability!=='immutable')return false;
    if(['/','%'].includes(row.operator||row.op)||row.arithmetic==='checked'&&['+','-','*','**'].includes(row.operator||row.op))return false;
    if(row.op==='FunctionCall'&&row.call_kind!=='typeConversion')return false;
    return unique([...(row.operands||[]),...(row.binding_expression_id?[row.binding_expression_id]:[])]).every(k=>storageCopyKeyKnown(k,seen));
  };
  const copyStorageAggregate=(method,node,context,type)=>{
    const match=/^t_struct\$_.*_\$(\d+)_storage(?:_ptr)?$/.exec(node?.typeDescriptions?.typeIdentifier||'');
    if(!type||!match)return null;
    const model=context.source_aggregates,ctx=aggregateContext(method,node,context),fail=reason=>model.unknown(type,reason,ctx);
    if(Number(match[1])!==type.declaration_id)return fail('SOURCE_AGGREGATE_STORAGE_COPY_TYPE_IDENTITY_UNAVAILABLE');
    if(context.entry_return_summary||context.return_stack?.length)return fail('SOURCE_AGGREGATE_STORAGE_COPY_RETURN_SUMMARY_UNMODELED');
    if(!context.source_memory?.allocatorKnown||context.source_memory.allocatorInvalidated||model.heap.opaqueMemoryEffects?.length)return fail('SOURCE_AGGREGATE_STORAGE_COPY_ALLOCATOR_UNVERIFIED');
    if(constructorHasEffects(node))return fail('SOURCE_AGGREGATE_STORAGE_COPY_RHS_EFFECT_UNMODELED');
    const cases=projectStorageCases(node,context,method);
    if(cases.length!==1||cases[0].unresolved?.length||cases[0].conditions.some(c=>!(context.conditions||[]).includes(c)))return fail('SOURCE_AGGREGATE_STORAGE_COPY_PROJECTION_UNAVAILABLE');
    const base=cases[0],projection=base.projection;
    if(projection.bounds?.length)return fail('SOURCE_AGGREGATE_STORAGE_COPY_ARRAY_BOUNDS_UNMODELED');
    const keys=[];let slotNodes=0,valid=true;
    const slots=(term,depth=0)=>{if(!term||++slotNodes>128||depth>32){valid=false;return;}if(term.op==='expression'){if(!storageCopyKeyKnown(term.expression_id))valid=false;keys.push(term.expression_id);}else if(term.op!=='constant'&&!['keccak_words','add','mul','div','mod'].includes(term.op))valid=false;for(const child of term.args||[])slots(child,depth+1);};
    slots(projection.slot_expression);if(!valid)return fail('SOURCE_AGGREGATE_STORAGE_COPY_KEY_OR_SLOT_UNMODELED');
    const literalBytesOf=row=>{
      if(!row)return null;
      if(row.literal_hex_value&&/^[0-9a-fA-F]*$/.test(row.literal_hex_value))return Buffer.from(row.literal_hex_value,'hex');
      if(row.literal_kind==='hexString'&&typeof row.raw_literal==='string')return Buffer.from(row.raw_literal.replace(/^0x/,'').replace(/[^0-9a-fA-F]/g,''),'hex');
      if(row.literal_kind==='string'&&typeof row.raw_literal==='string')return Buffer.from(row.raw_literal,'utf8');
      return null;
    };
    const copyProjectionMatches=(left,right)=>{
      if(!left||!right||left.root_state_ast_id!==right.root_state_ast_id)return false;
      const a=left.path||[],b=right.path||[];if(a.length!==b.length)return false;
      return a.every((step,index)=>{const other=b[index];if(step.kind!==other.kind)return false;
        if(step.kind==='mapping')return Boolean(step.key_expression_id)&&step.key_expression_id===other.key_expression_id;
        return step.field_ast_id===other.field_ast_id&&step.index===other.index;});
    };
    const dominatingStorageWrite=(fieldProjection,method,context,options={})=>{
      const order=method.step_ids.length,conditions=context.conditions||[];
      const rows=(result.sections.storage||[]).filter(row=>row.method_id===method.method_id&&row.access==='write'&&row.storage_projection&&copyProjectionMatches(row.storage_projection,fieldProjection)&&(row.order??Infinity)<order&&(row.conditions||[]).every(condition=>conditions.includes(condition)));
      if(!rows.length)return null;
      const latest=rows.at(-1);
      if(latest.operation!=='='||!latest.value_expression_id)return null;
      const value=expressionByID.get(latest.value_expression_id);
      if(!value||(options.requireType!==false&&value.type!==fieldProjection.scalar_type)||value.unresolved_model_reasons?.length||value.unresolved_evaluation_effect||value.unresolved_return_value||value.source_scalar_binding_unavailable)return null;
      const writeLocation=latest.source_location,copyLocation=location(node);
      if(!writeLocation||!copyLocation||writeLocation.file!==copyLocation.file||!(writeLocation.start<copyLocation.start))return null;
      const intervening=(result.sections.external_returns||[]).some(row=>row.method_id===method.method_id&&row.source_callsite&&row.source_callsite.file===writeLocation.file&&row.source_callsite.start>writeLocation.start&&row.source_callsite.start<copyLocation.start);
      if(intervening)return null;
      return {...latest,value};
    };
    const origin=expression(method,node,context),point=id(method.method_id,origin,method.step_ids.length,context.path,'storage-to-memory-copy',model.shared.serial),keyIDs=unique(keys);
    const read=(copyType,at,depth,stack=[])=>{
      checkMethodTime();if(depth>model.budget.maxDepth||stack.includes(copyType.declaration_id))return model.unknown(copyType,'SOURCE_AGGREGATE_STORAGE_COPY_NESTING_BUDGET',ctx);
      const layoutType=layout.types[at.type_id],definition=nodes.get(copyType.declaration_id);
      if(!layoutType?.members||layoutType.struct_ast_id!==copyType.declaration_id||definition?.nodeType!=='StructDefinition'||definition.members.length>model.budget.maxFields||layoutType.members.length!==definition.members.length||definition.members.some((m,i)=>m.id!==layoutType.members[i].astId))return model.unknown(copyType,'SOURCE_AGGREGATE_STORAGE_COPY_LAYOUT_UNAVAILABLE',ctx);
      const fields=[],fieldGaps=[];
      for(const member of definition.members){
        const field=layoutType.members.find(f=>f.astId===member.id),child=storageMember(at,field,layout.types),nested=aggregateType(member);
        if(!child)return model.unknown(copyType,'SOURCE_AGGREGATE_STORAGE_COPY_FIELD_LAYOUT_UNAVAILABLE',ctx);
        if(nested){const reference=read(nested,child,depth+1,[...stack,copyType.declaration_id]);fields.push({declaration_id:member.id,type:typeOf(member),reference,evaluation_may_fail:false});continue;}
        const memberType=typeOf(member).replace(/ (memory|storage|calldata)( ref| pointer)?/g,'');
        if(['string','bytes'].includes(memberType)){
          const prior=dominatingStorageWrite(child,method,context,{requireType:false}),literal=prior?literalBytesOf(prior.value):null;
          if(literal&&literal.length<=256&&context.source_memory){
            const captured=context.source_memory.capture({originExpressionId:prior.value.expression_id,encoding:'raw_bytes',rawCells:Array.from(literal,value=>({kind:'constant',value})),
              context:{path:context.path,order:method.step_ids.length,conditions:context.conditions},evidence_ids:unique([getEvidence(method,node),getEvidence(method,member),...(prior.evidence_ids||[])])});
            fields.push({declaration_id:member.id,type:typeOf(member),bytes_value:captured,bytes_length:literal.length,bytes_hex:literal.toString('hex'),bytes_source:'dominating_literal_storage_write',evaluation_may_fail:false});
            continue;
          }
          const gap=literal?'SOURCE_AGGREGATE_STORAGE_COPY_FIELD_CONTENT_TOO_LONG':'SOURCE_AGGREGATE_STORAGE_COPY_FIELD_CONTENT_NOT_PROVEN';
          fieldGaps.push({declaration_id:member.id,type:typeOf(member),reason:gap});
          fields.push({declaration_id:member.id,type:typeOf(member),value_expression_id:null,field_gap:gap+':'+member.name,evaluation_may_fail:false});
          continue;
        }
        const shape=storageCopyScalar(member),storageType=layout.types[child.type_id],bytes=Number(storageType?.numberOfBytes);
        if(!shape||storageType?.encoding!=='inplace'||storageType?.base||storageType?.members||!Number.isSafeInteger(bytes)||bytes<1||bytes>32||bytes*8!==shape.bits||!Number.isInteger(child.offset)||child.offset<0||child.offset+bytes>32){
          const gap='SOURCE_AGGREGATE_STORAGE_COPY_FIELD_TYPE_UNSUPPORTED:'+member.name;
          fieldGaps.push({declaration_id:member.id,type:typeOf(member),reason:gap});
          fields.push({declaration_id:member.id,type:typeOf(member),value_expression_id:null,field_gap:gap,evaluation_may_fail:false});
          continue;
        }
        const prior=dominatingStorageWrite(child,method,context),priorValue=prior?.value?.expression_id;
        const value=priorValue?synthetic(method,node,'identity',[priorValue],context,{type:shape.type,source_storage_scalar:shape,source_storage_copy_point_id:point,source_copy_expression_id:origin,source_storage_copy_source:location(node),source_storage_field_declaration_id:member.id,source_storage_write_order:prior.order,source_storage_write_expression_id:priorValue,storage_projection:structuredClone(child),storage_reference_ids:[...base.storage_reference_ids],source_order:method.step_ids.length,source_frame_path:context.path,storage_value_bound:true,storage_value_basis:'dominating_scalar_storage_write_before_copy',state_binding:'dominating_write_within_same_source_frame',validation_scope:'compiler_storage_location_and_source_copy_point_not_historical_state',all_execution_paths_verified:false,evidence_ids:unique([getEvidence(method,node),getEvidence(method,member),...(prior.evidence_ids||[])])})
          :synthetic(method,node,'source_storage_snapshot',keyIDs,context,{op:'SourceStorageReadAtCopy',type:shape.type,width:shape.kind==='integer'?shape.bits:null,signed:shape.signed,arithmetic:'storage_cleanup',source_storage_scalar:shape,source_storage_copy_point_id:point,source_copy_expression_id:origin,source_storage_copy_source:location(node),source_storage_field_declaration_id:member.id,storage_projection:structuredClone(child),storage_reference_ids:[...base.storage_reference_ids],source_order:method.step_ids.length,source_frame_path:context.path,storage_value_bound:false,validation_scope:'compiler_storage_location_and_source_copy_point_not_historical_state',all_execution_paths_verified:false,evidence_ids:unique([getEvidence(method,node),getEvidence(method,member)])});
        fields.push({declaration_id:member.id,type:typeOf(member),value_expression_id:value,evaluation_may_fail:false});
      }
      const reference=model.allocate(copyType,fields,{context:ctx,depth});
      if(reference.kind==='aggregate_reference')model.version(reference.object_id).source_storage_copy={copy_point_id:point,source_expression_id:origin,source_location:location(node),source_order:method.step_ids.length,storage_projection:structuredClone(at),captured_key_expression_ids:keyIDs,storage_value_bound:false,all_fields_copied_to_fresh_objects:true,field_gaps:fieldGaps};
      return reference;
    };
    return read(type,projection,1);
  };

  const resolveAggregate=(method,node,context,component=null,copyType=null)=>{
    const model=context.source_aggregates,type=aggregateType(node)||copyType;
    if(!model)return null;
    const evaluationGap=model.shared.globalEvaluationGap||model.shared.evaluationGaps.get(expressionIdentity(method,node,context));
    if(evaluationGap)return model.unknown(type,evaluationGap,aggregateContext(method,node,context));
    if(component!==null){if(node?.nodeType==='TupleExpression')return resolveAggregate(method,node.components?.[component],context,null,copyType);const key=projectTupleValue(method,node,expression(method,node,context),component,context,'unresolved');return aggregateExpressionReference(key)||model.unknown(type,'SOURCE_AGGREGATE_TUPLE_COMPONENT_UNAVAILABLE',aggregateContext(method,node,context));}
    const storageCopy=copyStorageAggregate(method,node,context,copyType);if(storageCopy)return storageCopy;
    if(node?.nodeType==='Identifier')return model.get(node.referencedDeclaration)||model.unknown(type,'SOURCE_AGGREGATE_REFERENCE_UNAVAILABLE',aggregateContext(method,node,context));
    if(node?.nodeType==='TupleExpression'&&node.components?.length===1)return resolveAggregate(method,node.components[0],context,null,copyType);
    const row=expressionByID.get(expression(method,node,context));
    return aggregateExpressionReference(row?.expression_id)||model.unknown(type,'SOURCE_AGGREGATE_EXPRESSION_UNSUPPORTED',aggregateContext(method,node,context));
  };
  const defaultAggregate=(method,declaration,context,type=aggregateType(declaration),stack=[])=>{
    const model=context.source_aggregates,ctx=aggregateContext(method,declaration,context);
    if(!['0.4.26','0.5.8','0.6.12','0.7.6','0.8.30'].includes(source.compilation.compilerVersion.split('+')[0]))return model.unknown(type,'SOURCE_AGGREGATE_DEFAULT_INITIALIZATION_COMPILER_UNVERIFIED',ctx);
    if(!context.source_memory?.allocatorKnown||context.source_memory.allocatorInvalidated)return model.unknown(type,'SOURCE_AGGREGATE_DEFAULT_ALLOCATOR_UNVERIFIED',ctx);
    if(model.heap.opaqueMemoryEffects?.length)return model.unknown(type,'SOURCE_AGGREGATE_DEFAULT_INITIALIZATION_AFTER_MEMORY_EFFECT',ctx);
    if(!type||stack.includes(type.declaration_id)||stack.length>=4)return model.unknown(type,'SOURCE_AGGREGATE_DEFAULT_NESTING_UNAVAILABLE',ctx);
    const definition=nodes.get(type.declaration_id);if(definition?.nodeType!=='StructDefinition'||definition.members.length>32)return model.unknown(type,'SOURCE_AGGREGATE_DEFAULT_TYPE_UNAVAILABLE',ctx);
    const fields=[];for(const member of definition.members){
      const nested=aggregateType(member);if(nested){fields.push({declaration_id:member.id,type:typeOf(member),reference:defaultAggregate(method,member,context,nested,[...stack,type.declaration_id]),evaluation_may_fail:false});continue;}
      if(!scalarDescriptor(member))return model.unknown(type,'SOURCE_AGGREGATE_DEFAULT_FIELD_TYPE_UNSUPPORTED',ctx);
      fields.push({declaration_id:member.id,type:typeOf(member),value_expression_id:scalarZero(method,member,context),evaluation_may_fail:false});
    }
    const reference=model.allocate(type,fields,{context:ctx,depth:stack.length+1});if(reference.kind==='aggregate_reference'){const row=model.version(reference.object_id);row.implicit_initialization={basis:'compiler_default_local_memory_struct',declaration_id:declaration.id,scope:'typed_values_and_fresh_object_identity_without_concrete_memory_offsets'};}return reference;
  };
  const bindAggregate=(method,context,declaration,value,kind='assignment',component=null)=>{
    const descriptor=aggregateDescriptor(declaration);if(!descriptor||!context.source_aggregates)return null;
    const ref=value?resolveAggregate(method,value,context,component,descriptor.aggregate_type):kind==='declaration'&&component===null?defaultAggregate(method,declaration,context):context.source_aggregates.unknown(descriptor.aggregate_type,'SOURCE_AGGREGATE_UNINITIALIZED_MEMORY_REFERENCE',aggregateContext(method,declaration,context));
    return context.source_aggregates.bind(descriptor,ref,{context:aggregateContext(method,declaration,context),kind});
  };
  const readonlyScalarFieldCalls=new Map();
  const fieldWriteHasEffects=(method,node)=>{
    let unsafe=false;visit(node,n=>{
      if(n.nodeType==='Assignment'||n.nodeType==='NewExpression'||n.nodeType==='UnaryOperation'&&['++','--','delete'].includes(n.operator)){unsafe=true;return;}
      if(n.nodeType!=='FunctionCall'||['typeConversion','structConstructorCall'].includes(n.kind))return;
      const callee=n.expression?.nodeType==='FunctionCallOptions'?n.expression.expression:n.expression,target=nodes.get(callee?.referencedDeclaration);
      if(target?.nodeType!=='FunctionDefinition'||!internalArguments(target,callee,n.arguments||[]).internal){unsafe=true;return;}
      if(!readonlyScalarFieldCalls.has(target.id))readonlyScalarFieldCalls.set(target.id,aggregateReturnAPI(method).readonly(target));
      if(!readonlyScalarFieldCalls.get(target.id)||!(target.returnParameters?.parameters||[]).every(scalarDescriptor))unsafe=true;
    });return unsafe;
  };
  const writeAggregateField=(method,node,context)=>{
    const lhs=node.leftHandSide,model=context.source_aggregates,reference=resolveAggregate(method,lhs.expression,context),member=nodes.get(lhs.referencedDeclaration),ctx=aggregateContext(method,node,context);
    const fail=reason=>model.invalidate(reference,reason,{context:ctx});
    const plainTarget=n=>n?.nodeType==='Identifier'||n?.nodeType==='MemberAccess'&&plainTarget(n.expression);
    if(node.operator!=='='||!plainTarget(lhs.expression)||fieldWriteHasEffects(method,node.rightHandSide)){model.invalidateAll('SOURCE_AGGREGATE_FIELD_WRITE_EVALUATION_UNMODELED',ctx);return;}
    if(!scalarDescriptor(member)||nodes.get(member.scope)?.nodeType!=='StructDefinition'){fail('SOURCE_AGGREGATE_FIELD_WRITE_TYPE_UNSUPPORTED');return;}
    let value=expression(method,node.rightHandSide,context);const info=expressionByID.get(value),conversion=info&&scalarAssignmentConversion(info.type,typeOf(member),info);
    if(conversion?.unresolved){fail('SOURCE_AGGREGATE_'+conversion.unresolved);return;}
    if(conversion){const{format,...details}=conversion;value=synthetic(method,node.rightHandSide,format,[value],context,{type:typeOf(member),implicit_source_conversion:true,...details});}
    let called=false;visit(node.rightHandSide,n=>{if(n.nodeType==='FunctionCall'&&!['typeConversion','structConstructorCall'].includes(n.kind))called=true;});
    model.writeScalar(reference,{declaration_id:member.id,type:typeOf(member)},value,{context:ctx,evaluationMayFail:constructorMayFail(node.rightHandSide,context)||called});
  };

  const writeAggregateTupleFields=(method,node,context)=>{
    const model=context.source_aggregates,rhs=node.rightHandSide,ctx=aggregateContext(method,node,context),targets=node.leftHandSide.components||[];
    if(node.operator!=='='||!['TupleExpression','FunctionCall'].includes(rhs?.nodeType)||fieldWriteHasEffects(method,rhs)||model.heap.opaqueMemoryEffects?.length)return false;
    const plain=n=>n?.nodeType==='Identifier'||n?.nodeType==='MemberAccess'&&plain(n.expression),prepared=[],seen=new Set(),whole=expression(method,rhs,context);
    if(rhs.nodeType==='FunctionCall'&&(!expressionByID.get(whole)?.internal_return_model||expressionByID.get(whole)?.unresolved_return_value))return false;
    for(const[index,target]of targets.entries()){
      if(!target)continue;
      if(target.nodeType==='Identifier'&&scalarDescriptor(nodes.get(target.referencedDeclaration))&&!nodes.get(target.referencedDeclaration)?.stateVariable){const key='scalar:'+target.referencedDeclaration;if(seen.has(key))return false;seen.add(key);continue;}
      if(target.nodeType!=='MemberAccess'||!aggregateType(target.expression)||!plain(target.expression))return false;
      const member=nodes.get(target.referencedDeclaration),reference=resolveAggregate(method,target.expression,context),previous=reference?.kind==='aggregate_reference'?model.version(reference.object_id):null,state=reference?.kind==='aggregate_reference'?model.heap.current.get(reference.object_id):null;
      if(!scalarDescriptor(member)||nodes.get(member.scope)?.nodeType!=='StructDefinition'||!previous?.fields||state?.unresolved?.length)return false;
      const key=reference.object_id+':'+member.id;if(seen.has(key))return false;seen.add(key);
      const field=previous.fields.find(f=>f.declaration_id===member.id);if(!field||field.reference||field.type!==typeOf(member))return false;
      let value=projectTupleValue(method,rhs,whole,index,context,typeOf(member));const info=expressionByID.get(value),conversion=info&&scalarAssignmentConversion(info.type,typeOf(member),info);
      if(!info||conversion?.unresolved)return false;
      if(conversion){const{format,...details}=conversion;value=synthetic(method,rhs,format,[value],context,{type:typeOf(member),implicit_source_conversion:true,tuple_component:index,...details});}
      prepared.push({reference,field:{declaration_id:member.id,type:typeOf(member)},value,index});
    }
    if(!prepared.length)return false;
    for(const item of prepared){
      const write=model.writeScalar(item.reference,item.field,item.value,{context:ctx,evaluationMayFail:constructorMayFail(rhs,context)||rhs.nodeType==='FunctionCall',evaluationExpressionIds:rhs.nodeType==='FunctionCall'?[whole]:[],evaluationGroup:rhs.nodeType==='FunctionCall'?whole:null});
      if(write.status!=='source_aggregate_field_written'){model.invalidateAll('SOURCE_AGGREGATE_TUPLE_FIELD_WRITE_UNMODELED',ctx);model.unknown(null,'SOURCE_AGGREGATE_TUPLE_FIELD_WRITE_UNMODELED',ctx);return true;}
      model.shared.versions.get(write.version_expression_id).source_aggregate_tuple_snapshot={assignment_ast_id:node.id,component:item.index,rhs_kind:rhs.nodeType,rhs_expression_id:whole,all_values_captured_before_writes:true,overlapping_targets:false};
    }
    return true;
  };
  const initializeAggregates=(method,fn,context,args=null,caller=null)=>{
    if(!context.source_aggregates)return;
    for(const[i,p]of(fn.parameters?.parameters||[]).entries())if(aggregateDescriptor(p)){
      const reference=args?resolveAggregate(method,args[i],caller,null,aggregateType(p)):context.source_aggregates.unknown(aggregateType(p),'SOURCE_AGGREGATE_ENTRY_MEMORY_VALUE_UNBOUND',aggregateContext(method,p,context));
      context.source_aggregates.bind(aggregateDescriptor(p),reference,{context:aggregateContext(method,p,context),kind:args?'parameter_binding':'entry_argument'});
    }
    for(const p of fn.returnParameters?.parameters||[])if(p.name&&aggregateDescriptor(p))bindAggregate(method,context,p,null,'named_return_unbound');
  };
  const projectionValue=(method,node,context,projection)=>{
    if(projection.kind==='source_aggregate_field_value')return projection;
    if(projection.kind==='conditional_aggregate_value'){
      const yes=projectionValue(method,node,context,projection.yes),no=projectionValue(method,node,context,projection.no),extra={type:projection.type,source_aggregate_field_join:true};
      return{value_expression_id:yes.value_expression_id&&no.value_expression_id?synthetic(method,node,'conditional',[projection.condition_expression_id,yes.value_expression_id,no.value_expression_id],context,extra):null,
        unresolved:unique([...(yes.unresolved||[]),...(no.unresolved||[])]),aggregate_version_ids:projection.aggregate_version_ids,
        aggregate_return_expression_ids:unique([...(projection.aggregate_return_expression_ids||[]),...(yes.aggregate_return_expression_ids||[]),...(no.aggregate_return_expression_ids||[])]),
        aggregate_evaluation_expression_ids:unique([...(yes.aggregate_evaluation_expression_ids||[]),...(no.aggregate_evaluation_expression_ids||[])])};
    }
    return{value_expression_id:null,unresolved:projection.unresolved||['SOURCE_AGGREGATE_FIELD_VALUE_UNAVAILABLE'],aggregate_version_ids:projection.aggregate_version_ids||[]};
  };
  const attachAggregate=(method,node,context,row)=>{
    const model=context.source_aggregates;if(!model)return;
    const evaluationGap=model.shared.globalEvaluationGap||model.shared.evaluationGaps.get(row.expression_id);
    if(evaluationGap)row.unresolved_model_reasons=unique([...(row.unresolved_model_reasons||[]),evaluationGap]);
    const type=aggregateType(node),capture=reference=>{reference=structuredClone(reference);if(evaluationGap)reference.unresolved=unique([...(reference.unresolved||[]),evaluationGap]);row.source_aggregate_reference=reference;row.aggregate_type_declaration_id=type?.declaration_id;row.binding_expression_id=null;row.unresolved_local_reference=false;row.unresolved_model_reasons=unique([...(row.unresolved_model_reasons||[]).filter(c=>c!=='SOURCE_AGGREGATE_REFERENCE_VALUE_REQUIRES_MEMORY_MODEL'),...(reference.unresolved||[])]);};
    if(node.nodeType==='FunctionCall'&&node.kind==='structConstructorCall'&&type){
      const definition=nodes.get(type.declaration_id),members=definition.members||[],args=node.arguments||[],names=node.names||[],ctx=aggregateContext(method,node,context);
      if(!context.source_memory?.allocatorKnown||context.source_memory.allocatorInvalidated){capture(model.unknown(type,'SOURCE_AGGREGATE_ALLOCATOR_UNVERIFIED',ctx));return;}
      if(args.length!==members.length||names.length&&names.length!==args.length||new Set(names).size!==names.length){capture(model.unknown(type,'SOURCE_AGGREGATE_CONSTRUCTOR_FIELD_IDENTITY_UNAVAILABLE',ctx));return;}
      if(args.some(constructorHasEffects)){capture(model.unknown(type,'SOURCE_AGGREGATE_CONSTRUCTOR_ARGUMENT_EFFECT_UNMODELED',ctx));return;}
      const fields=[];
      for(const[i,member]of members.entries()){
        const index=names.length?names.indexOf(member.name):i,arg=args[index];if(!arg){capture(model.unknown(type,'SOURCE_AGGREGATE_CONSTRUCTOR_FIELD_IDENTITY_UNAVAILABLE',ctx));return;}
        const nested=aggregateType(member);if(nested){fields.push({declaration_id:member.id,type:typeOf(member),reference:resolveAggregate(method,arg,context),evaluation_may_fail:constructorMayFail(arg,context)});continue;}
        if(!scalarDescriptor(member)){capture(model.unknown(type,'SOURCE_AGGREGATE_FIELD_TYPE_UNSUPPORTED',ctx));return;}
        let value=expression(method,arg,context);const info=expressionByID.get(value),conversion=info&&scalarAssignmentConversion(info.type,typeOf(member),info);
        if(conversion?.unresolved){capture(model.unknown(type,'SOURCE_AGGREGATE_'+conversion.unresolved,ctx));return;}
        if(conversion){const{format,...details}=conversion;value=synthetic(method,arg,format,[value],context,{type:typeOf(member),implicit_source_conversion:true,...details});}
        fields.push({declaration_id:member.id,type:typeOf(member),value_expression_id:value,evaluation_may_fail:constructorMayFail(arg,context)});
      }
      capture(model.allocate(type,fields,{context:ctx}));return;
    }
    if(node.nodeType==='FunctionCall'&&type&&row.internal_return_model){const ref=aggregateExpressionReference(row.binding_expression_id);if(ref){capture(ref);row.binding_kind='bounded_readonly_struct_return';return;}}
    if(node.nodeType==='Identifier'&&type){capture(model.get(node.referencedDeclaration)||model.unknown(type,'SOURCE_AGGREGATE_REFERENCE_UNAVAILABLE',aggregateContext(method,node,context)));return;}
    if(node.nodeType==='Conditional'&&type){capture({kind:'conditional_aggregate_reference',type,condition_expression_id:expression(method,node.condition,context),yes:resolveAggregate(method,node.trueExpression,context),no:resolveAggregate(method,node.falseExpression,context)});return;}
    if(node.nodeType==='TupleExpression'&&node.components?.length===1&&type){capture(resolveAggregate(method,node.components[0],context));return;}
    if(node.nodeType!=='MemberAccess'||!aggregateType(node.expression))return;
    const member=nodes.get(node.referencedDeclaration);if(member?.nodeType!=='VariableDeclaration'||nodes.get(member.scope)?.nodeType!=='StructDefinition')return;
    const projection=model.project(resolveAggregate(method,node.expression,context),{declaration_id:member.id,type:typeOf(member)},{context:aggregateContext(method,node,context)});
    if(projection.kind==='nested_aggregate_reference'){capture(projection.reference);return;}
    if(type){
      const reference=p=>p.kind==='nested_aggregate_reference'?p.reference:p.kind==='conditional_aggregate_value'?{kind:'conditional_aggregate_reference',type,condition_expression_id:p.condition_expression_id,yes:reference(p.yes),no:reference(p.no),aggregate_return_expression_ids:p.aggregate_return_expression_ids||[]}:model.unknown(type,p.unresolved?.[0]||'SOURCE_AGGREGATE_NESTED_VALUE_UNAVAILABLE',aggregateContext(method,node,context));
      capture(reference(projection));return;
    }
    const value=projectionValue(method,node,context,projection);row.binding_expression_id=value.value_expression_id;row.binding_kind='compiler_memory_struct_field';row.source_aggregate_binding_unavailable=!value.value_expression_id;
    row.source_aggregate_field_declaration_id=member.id;row.source_aggregate_version_ids=value.aggregate_version_ids||[];row.aggregate_version_expression_ids=value.aggregate_version_ids||[];row.aggregate_evaluation_expression_ids=value.aggregate_evaluation_expression_ids||[];row.aggregate_return_expression_ids=value.aggregate_return_expression_ids||[];
    row.unresolved_model_reasons=unique([...(row.unresolved_model_reasons||[]).filter(c=>c!=='SOURCE_AGGREGATE_REFERENCE_VALUE_REQUIRES_MEMORY_MODEL'),...(value.unresolved||[])]);
  };
  const aggregateReturnAPI=method=>({
    isAggregate:node=>Boolean(aggregateType(node)),
    readonly:target=>{let safe=true,count=0;const checked=new Set(),check=(fn,stack=[])=>{
      if(!fn?.body||stack.includes(fn.id)||stack.length>=12||fn.nodeType!=='ModifierDefinition'&&!['pure','view'].includes(fn.stateMutability)&&!fn.constant){safe=false;return;}if(checked.has(fn.id))return;checked.add(fn.id);
      visit(fn.body,n=>{if(!safe||++count>8192){safe=false;return;}
        if(['InlineAssembly','NewExpression','TryStatement'].includes(n.nodeType)){safe=false;return;}
        const lhs=n.nodeType==='Assignment'?n.leftHandSide:n.nodeType==='UnaryOperation'&&['++','--','delete'].includes(n.operator)?n.subExpression:null;
        if(lhs){const targets=lhs.nodeType==='TupleExpression'?lhs.components:[lhs];if(targets.some(t=>t&&(t.nodeType!=='Identifier'||nodes.get(t.referencedDeclaration)?.stateVariable||n.operator==='delete'&&aggregateType(t))))safe=false;}
        if(n.nodeType==='FunctionCall'&&!['typeConversion','structConstructorCall'].includes(n.kind)){
          const callee=n.expression?.nodeType==='FunctionCallOptions'?n.expression.expression:n.expression,called=nodes.get(callee?.referencedDeclaration),internal=internalArguments(called,callee,n.arguments||[]).internal;
          if(internal)check(called,[...stack,fn.id]);else if(/^t_function_internal_/.test(callee?.typeDescriptions?.typeIdentifier||''))safe=false;
        }
      });
      for(const invocation of fn.modifiers||[])check(nodes.get(invocation.modifierName?.referencedDeclaration),[...stack,fn.id]);
    };check(target);return safe;},
    bind:(declaration,key,context)=>context.source_aggregates.bind(aggregateDescriptor(declaration),aggregateExpressionReference(key)||context.source_aggregates.unknown(aggregateType(declaration),'SOURCE_AGGREGATE_RETURN_VALUE_UNAVAILABLE',aggregateContext(method,declaration,context)),{context:aggregateContext(method,declaration,context),kind:'readonly_summary_binding'}),
    unknown:(declaration,context)=>synthetic(method,declaration,'source_aggregate_reference',[],context,{type:typeOf(declaration),source_aggregate_reference:context.source_aggregates.unknown(aggregateType(declaration),'SOURCE_AGGREGATE_UNINITIALIZED_MEMORY_REFERENCE',aggregateContext(method,declaration,context))}),
    wrap:(declaration,key,context)=>{
      let ref=aggregateExpressionReference(key);if(ref?.type?.declaration_id!==aggregateType(declaration)?.declaration_id)ref=context.source_aggregates.unknown(aggregateType(declaration),'SOURCE_AGGREGATE_RETURN_TYPE_OR_VALUE_UNAVAILABLE',aggregateContext(method,declaration,context));
      return synthetic(method,declaration,'source_aggregate_reference',[],context,{type:typeOf(declaration),source_aggregate_reference:ref});
    },
    adopt:(caller,results)=>{for(const{model}of results)for(const[key,value]of model.heap.current){const before=caller.source_aggregates.heap.current.get(key);if(before&&JSON.stringify(before)!==JSON.stringify(value))throw Error('INTERNAL_RETURN_AGGREGATE_HEAP_EFFECT_UNSUPPORTED');caller.source_aggregates.heap.current.set(key,structuredClone(value));}},
    paths:(call,declaration,paths,context,unresolved)=>{
      const references=paths.map(p=>aggregateExpressionReference(p.expression_id)||context.source_aggregates.unknown(aggregateType(declaration),'SOURCE_AGGREGATE_RETURN_VALUE_UNAVAILABLE',aggregateContext(method,call,context)));
      let reference=references.at(-1);
      for(let i=paths.length-2;i>=0;i--){const conditions=paths[i].conditions.map(c=>c.expected?c.expression_id:synthetic(method,call,'unary',[c.expression_id],context,{type:'bool',operator:'!'}));if(!conditions.length){reference=references[i];continue;}let condition=conditions[0];for(const next of conditions.slice(1))condition=synthetic(method,call,'binary',[condition,next],context,{type:'bool',operator:'&&'});reference={kind:'conditional_aggregate_reference',type:aggregateType(declaration),condition_expression_id:condition,yes:references[i],no:reference};}
      const key=synthetic(method,call,'source_aggregate_reference',[],context,{type:typeOf(declaration),source_aggregate_reference:reference,aggregate_return_paths:paths,unresolved_model_reasons:unresolved});
      reference.aggregate_return_expression_ids=unique([...(reference.aggregate_return_expression_ids||[]),key]);if(reference.aggregate_return_expression_ids.length>128)reference=context.source_aggregates.unknown(aggregateType(declaration),'SOURCE_AGGREGATE_RETURN_DEPENDENCY_BUDGET',aggregateContext(method,call,context));expressionByID.get(key).source_aggregate_reference=structuredClone(reference);
      return key;
    }
  });
  const aggregateReadonlyChecks=new Map(),aggregateGlobalEffects=new Map();
  const aggregateGlobalMemoryEffect=(target,stack=[])=>{
    if(!target?.body||stack.includes(target.id)||stack.length>=12)return true;if(aggregateGlobalEffects.has(target.id))return aggregateGlobalEffects.get(target.id);let unsafe=false,count=0;
    for(const body of[target.body,...(target.modifiers||[]).map(m=>nodes.get(m.modifierName?.referencedDeclaration)?.body)])visit(body,n=>{
      if(unsafe||++count>8192){unsafe=true;return;}if(n.nodeType==='InlineAssembly'){unsafe=true;return;}
      if(n.nodeType==='FunctionCall'&&!['typeConversion','structConstructorCall'].includes(n.kind)){
        const callee=n.expression?.nodeType==='FunctionCallOptions'?n.expression.expression:n.expression,called=nodes.get(callee?.referencedDeclaration),internal=internalArguments(called,callee,n.arguments||[]).internal;
        if(internal&&aggregateGlobalMemoryEffect(called,[...stack,target.id])||called?.nodeType!=='FunctionDefinition'&&/^t_function_internal_/.test(callee?.typeDescriptions?.typeIdentifier||''))unsafe=true;
      }
    });aggregateGlobalEffects.set(target.id,unsafe);return unsafe;
  };
  const aggregateSiblingEffects=(method,node,context)=>{
    const model=context.source_aggregates;if(!model)return null;
    if(!model.references.size&&!model.heap.current.size)return null;
    const list=node.nodeType==='TupleExpression'?(node.components||[]).filter(Boolean):node.nodeType==='FunctionCall'?[node.expression,...(node.arguments||[])].filter(Boolean):node.nodeType==='BinaryOperation'&&!['&&','||'].includes(node.operator)?[node.leftExpression,node.rightExpression]:[];
    if(list.length<2)return null;
    const objects=(reference,out=new Set(),seen=new Set())=>{
      if(!reference||reference.kind==='unresolved_aggregate_reference'){out.add('*');return out;}
      if(reference.kind==='conditional_aggregate_reference'){objects(reference.yes,out,seen);objects(reference.no,out,seen);return out;}
      if(seen.has(reference.object_id))return out;seen.add(reference.object_id);out.add(reference.object_id);
      for(const field of model.version(reference.object_id)?.fields||[])if(field.reference)objects(field.reference,out,seen);return out;
    };
    const referencedObjects=(part,out)=>visit(part,n=>{if(n.nodeType==='Identifier'&&aggregateDescriptor(nodes.get(n.referencedDeclaration)))for(const key of objects(model.get(n.referencedDeclaration)))out.add(key);});
    const effects=list.map(part=>{const reads=new Set(),writes=new Set(),readObjects=new Set(),writeObjects=new Set();visit(part,n=>{
      if(n.nodeType==='Identifier'&&aggregateDescriptor(nodes.get(n.referencedDeclaration))){reads.add(n.referencedDeclaration);for(const key of objects(model.get(n.referencedDeclaration)))readObjects.add(key);}
      const lhs=n.nodeType==='Assignment'?n.leftHandSide:n.nodeType==='UnaryOperation'&&['++','--','delete'].includes(n.operator)?n.subExpression:null;
      if(lhs)for(const target of lhs.nodeType==='TupleExpression'?lhs.components:[lhs])if(target){
        if(target.nodeType==='Identifier'&&aggregateDescriptor(nodes.get(target.referencedDeclaration))){writes.add(target.referencedDeclaration);if(n.operator==='delete')referencedObjects(target,writeObjects);}
        else if(target.nodeType==='MemberAccess'&&aggregateType(target.expression))referencedObjects(target.expression,writeObjects);
      }
      if(n.nodeType==='FunctionCall'&&!['typeConversion','structConstructorCall'].includes(n.kind)){
        const callee=n.expression?.nodeType==='FunctionCallOptions'?n.expression.expression:n.expression,called=nodes.get(callee?.referencedDeclaration),call=internalArguments(called,callee,n.arguments||[]);
        if(call.internal||/^t_function_internal_/.test(callee?.typeDescriptions?.typeIdentifier||'')){
          if(aggregateGlobalMemoryEffect(called)){writeObjects.add('*');return;}
          const arguments_=call.args||n.arguments||[];let hasReference=false;for(const arg of arguments_)visit(arg,p=>{if(p.nodeType==='Identifier'&&aggregateDescriptor(nodes.get(p.referencedDeclaration)))hasReference=true;});if(!hasReference)return;
          let readonly=false;if(called?.nodeType==='FunctionDefinition'){if(!aggregateReadonlyChecks.has(called.id))aggregateReadonlyChecks.set(called.id,aggregateReturnAPI(method).readonly(called));readonly=aggregateReadonlyChecks.get(called.id);}
          if(!readonly)for(const arg of arguments_)referencedObjects(arg,writeObjects);
        }
      }
    });return{reads,writes,readObjects,writeObjects};});
    const intersects=(a,b,wildcard=false)=>a.size&&b.size&&(wildcard&&(a.has('*')||b.has('*'))||[...a].some(k=>b.has(k)));
    let unsafe=false;for(let i=0;i<effects.length;i++)for(let j=i+1;j<effects.length;j++){const a=effects[i],b=effects[j];if(intersects(a.writes,new Set([...b.reads,...b.writes]))||intersects(b.writes,a.reads)||intersects(a.writeObjects,new Set([...b.readObjects,...b.writeObjects]),true)||intersects(b.writeObjects,a.readObjects,true))unsafe=true;}
    if(!unsafe)return null;
    const reason='SOURCE_AGGREGATE_EVALUATION_ORDER_UNSPECIFIED',ctx=aggregateContext(method,node,context);model.unknown(null,reason,ctx);
    visit(node,n=>{const key=expressionIdentity(method,n,context);if(model.shared.evaluationGaps.size>=4096){model.shared.globalEvaluationGap='SOURCE_AGGREGATE_EVALUATION_GAP_BUDGET';model.unknown(null,model.shared.globalEvaluationGap,ctx);return;}model.shared.evaluationGaps.set(key,reason);const old=expressionByID.get(key);if(old){old.unresolved_model_reasons=unique([...(old.unresolved_model_reasons||[]),reason]);if(old.source_aggregate_reference)old.source_aggregate_reference.unresolved=unique([...(old.source_aggregate_reference.unresolved||[]),reason]);}});
    return{reason,writes:unique(effects.flatMap(e=>[...e.writes])),objects:unique(effects.flatMap(e=>[...e.writeObjects]))};
  };
  const finishAggregateSiblings=(method,node,context,effects)=>{
    if(!effects)return;const model=context.source_aggregates,ctx=aggregateContext(method,node,context);
    for(const key of effects.writes){const declaration=nodes.get(key);model.bind(aggregateDescriptor(declaration),model.unknown(aggregateType(declaration),effects.reason,ctx),{context:ctx,kind:'unspecified_sibling_write'});}
    if(effects.objects.includes('*'))model.invalidateAll(effects.reason,ctx);else for(const key of effects.objects)model.invalidate({kind:'aggregate_reference',object_id:key,type:model.shared.types.get(key)},effects.reason,{context:ctx});
  };
  const tupleValueType=(whole,index)=>{
    const seen=new Set();let row=expressionByID.get(whole);
    while(row&&!seen.has(row.expression_id)){
      seen.add(row.expression_id);
      const key=row.abi_component_expression_ids?.[index]||row.tuple_component_expression_ids?.[index]||(row.symbolic_format==='tuple'?row.operands?.[index]:null),
        type=row.return_component_types?.[index]||expressionByID.get(key)?.type||tupleComponentType(row.type,index);
      if(type)return type;row=expressionByID.get(row.binding_expression_id);
    }
    return null;
  };
  const tupleProjectionCache=new Map();
  const projectTupleValue=(method,node,whole,index,context,fallback)=>{
    const type=tupleValueType(whole,index),key=id(method.method_id,node.id,whole,index,context.path,context.unchecked||false,type||fallback),cached=tupleProjectionCache.get(key);
    if(cached&&expressionByID.has(cached))return cached;
    const value=synthetic(method,node,'tuple_component',[whole],context,{component_index:index,type:type||fallback,
      ...(!type?{unresolved_model_reasons:['SOURCE_TUPLE_COMPONENT_TYPE_UNAVAILABLE']}:{} )});
    if(tupleProjectionCache.size<50000)tupleProjectionCache.set(key,value);return value;
  };
  const bindScalar=(method,context,declaration,value,{component=null,definition_id=null,kind='assignment',unresolved=[],valueExpression}={})=>{
    const descriptor=scalarDescriptor(declaration);if(!descriptor||!context.source_scalars)return null;
    let expression_id=valueExpression===undefined?value?expression(method,value,context):null:valueExpression;
    if(component!==null&&expression_id)expression_id=projectTupleValue(method,value||declaration,expression_id,component,context,descriptor.type);
    const info=expressionByID.get(expression_id),conversion=info&&scalarAssignmentConversion(info.type,descriptor.type,info);
    if(conversion?.unresolved){unresolved=[...unresolved,'SOURCE_SCALAR_'+conversion.unresolved];expression_id=null;}
    else if(conversion){const{format,...details}=conversion;expression_id=synthetic(method,value||declaration,format,[expression_id],context,
      {type:descriptor.type,width:Number(descriptor.type.slice(5))*8,signed:false,arithmetic:'wrap',implicit_source_conversion:true,...details});}
    return context.source_scalars.capture(descriptor,expression_id,{context:scalarContext(context,method),definition_id,kind,unresolved,evidence_ids:unique([getEvidence(method,value||declaration),getEvidence(method,declaration)])});
  };
  const initializeScalars=(method,fn,context,args=null,caller=null)=>{
    if(!context.source_scalars)return;
    for(const[i,p]of(fn.parameters?.parameters||[]).entries())if(scalarDescriptor(p)){
      const value=args?expression(method,args[i],caller):synthetic(method,p,'entry_scalar',[],context,{type:typeOf(p),value:p.name||'arg'+i,entry_argument:true,referenced_declaration:p.id});
      bindScalar(method,context,p,null,{valueExpression:value,kind:args?'parameter_binding':'entry_argument'});
    }
    // An unnamed return declaration cannot be read or written by the body or
    // a modifier. Its expression is already retained by the return model.
    for(const p of fn.returnParameters?.parameters||[])if(p.name&&scalarDescriptor(p))bindScalar(method,context,p,null,{valueExpression:scalarZero(method,p,context),kind:'named_return_default'});
  };
  const mergeScalars=(method,node,context,yes,no,conditionExpression,yesTerminated=false,noTerminated=false)=>{
    context.source_scalars?.merge(yes.source_scalars,no.source_scalars,{
      conditionExpression,yesTerminated,noTerminated,context:{...scalarContext(context,method),ast_id:node.id},declarations:key=>scalarDescriptor(nodes.get(key)),
      createPhi:(declaration,operands)=>synthetic(method,node.condition||node,'conditional',operands,context,{type:declaration.type,source_scalar_join:true})});
    context.source_aggregates?.merge(yes.source_aggregates,no.source_aggregates,{conditionExpression,yesTerminated,noTerminated,context:{...scalarContext(context,method),ast_id:node.id},createPhi:(field,operands)=>synthetic(method,node.condition||node,'conditional',operands,context,{type:field.type,source_aggregate_field_join:true})});
  };
  const scalarWrites=node=>{const found=new Set();visit(node,n=>{
    const lhs=n.nodeType==='Assignment'?n.leftHandSide:n.nodeType==='UnaryOperation'&&['++','--','delete'].includes(n.operator)?n.subExpression:null;
    if(lhs?.nodeType==='Identifier'&&scalarDescriptor(nodes.get(lhs.referencedDeclaration)))found.add(lhs.referencedDeclaration);
    if(lhs?.nodeType==='TupleExpression')for(const p of lhs.components||[])if(p?.nodeType==='Identifier'&&scalarDescriptor(nodes.get(p.referencedDeclaration)))found.add(p.referencedDeclaration);
  });return[...found].map(key=>scalarDescriptor(nodes.get(key)));};
  const expressionIdentity=(method,node,context)=>id(method.method_id,node.id,node.src,context.path,context.unchecked||false);
  const expression = (method, node, context = {}) => {
    if (!node) return null;
    checkMethodTime();
    const type = typeOf(node), integer = /\b(u?int)(\d*)\b/.exec(type);
    const expression_id = expressionIdentity(method,node,context);
    if (expressionByID.has(expression_id)) return expression_id;
    const capturedOperands=context.source_scalars?.shared.expressionOperands.get(expression_id);
    const operands = capturedOperands?[...capturedOperands.operands]:children(node).filter(n => !['TypeName', 'VariableDeclaration'].some(t => n.nodeType?.endsWith(t)))
      .map(n => expression(method, n, context)).filter(Boolean);
    const bound=node.nodeType==='Identifier'?context.parameter_arguments?.[node.referencedDeclaration]:null;
    const scalar=node.nodeType==='Identifier'&&!context.entry_return_summary&&!context.return_stack?.length&&scalarDescriptor(nodes.get(node.referencedDeclaration))?context.source_scalars?.get(node.referencedDeclaration):null;
    const binding_expression_id=scalar?scalar.value_expression_id:node.nodeType==='Identifier'&&context.parameter_expression_bindings?.[node.referencedDeclaration]||
      (bound&&bound.id!==node.id?expression(method,bound,context):null);
    if(binding_expression_id)operands.push(binding_expression_id);
    const row = { expression_id, method_id: method.method_id, op: node.operator || node.nodeType,
      text: node.entry_argument?node.name:sourceText(node).slice(0, 4096), type, width: integer ? Number(integer[2] || '256') : null,
      signed: integer ? integer[1] === 'int' : null,
      arithmetic: integer ? node.kind==='typeConversion'?'explicit_cast_truncation':(context.unchecked || Number(source.compilation.compilerVersion.split('.')[1]) < 8 ? 'wrap' : 'checked') : 'not_applicable',
      rounding: node.operator === '/' ? 'toward_zero' : null, operands,common_type:node.commonType?.typeString||null, referenced_declaration: node.referencedDeclaration ?? null,call_kind:node.kind||null,
      builtin_global:builtinMember(node,nodes),
      path: context.path, conditions: [...(context.conditions || [])], step_order: method.step_ids.length,entry_argument:node.entry_argument===true,entry_return_summary:context.entry_return_summary||false,
      evidence_ids: [getEvidence(method, node)] };
    if(node.nodeType==='Assignment'||node.nodeType==='UnaryOperation'&&['++','--'].includes(node.operator)){
      row.unresolved_evaluation_effect='LOCAL_EXPRESSION_EFFECT_REQUIRES_ORDERED_RETURN_MODEL';
      row.write_target_expression_id=expression(method,node.nodeType==='Assignment'?node.leftHandSide:node.subExpression,context);
      row.write_target_role=node.nodeType==='Assignment'&&node.operator==='='?'write_only':'read_modify_write';
      if(node.nodeType==='Assignment')row.write_value_expression_id=expression(method,node.rightHandSide,context);
    }
    if(node.nodeType==='YulBuiltin')Object.assign(row,{symbolic_format:'evm_word',operator:node.functionName.name,
      width:256,signed:false,arithmetic:'evm_word_wrap',rounding:['div','sdiv'].includes(node.functionName.name)?'toward_zero_zero_divisor_returns_zero':null});
    if(node.nodeType==='YulWordCast')Object.assign(row,{symbolic_format:'identity',arithmetic:'explicit_cast_truncation',yul_word_assignment:true});
    if(['MemberAccess','IndexAccess'].includes(node.nodeType)){
      const base=node.nodeType==='IndexAccess'?node.baseExpression:node.expression,root=nodes.get(rootDeclaration(base)),type=typeOf(base),
        aggregate=type.includes('[')||/^struct\s/.test(type),localReference=root?.nodeType==='VariableDeclaration'&&!root.stateVariable&&!pointerType(root),
        returnedReference=/\b(?:memory|calldata)\b/.test(type)||/_(?:memory|calldata)_/.test(base?.typeDescriptions?.typeIdentifier||'');
      if(aggregate&&(localReference||!root&&returnedReference))row.unresolved_model_reasons=['SOURCE_AGGREGATE_REFERENCE_VALUE_REQUIRES_MEMORY_MODEL'];
    }
    if(binding_expression_id){row.binding_expression_id=binding_expression_id;row.bound_argument=sourceText(bound)}
    if(scalar){row.source_scalar_version_id=scalar.source_scalar_version_id;row.binding_kind='source_scalar_version';
      row.source_scalar_binding_unavailable=!scalar.value_expression_id;row.source_scalar_unresolved=[...scalar.unresolved];}
    if(node.nodeType==='FunctionCall') {row.argument_expression_ids=(node.arguments||[]).map(n=>expression(method,n,context));row.callee_expression_id=expression(method,node.expression,context);}
    if(node.nodeType==='TupleExpression'&&!node.isInlineArray){
      if(node.components?.length===1&&node.components[0]&&typeOf(node.components[0])===type){row.binding_expression_id=expression(method,node.components[0],context);row.binding_kind='source_grouping';}
      else row.tuple_component_expression_ids=(node.components||[]).map(n=>n?expression(method,n,context):null);
    }
    if(node.nodeType==='Conditional')Object.assign(row,capturedOperands?{condition_expression_id:capturedOperands.operands[0],true_expression_id:capturedOperands.operands[1],false_expression_id:capturedOperands.operands[2]}:
      {condition_expression_id:expression(method,node.condition,context),true_expression_id:expression(method,node.trueExpression,context),false_expression_id:expression(method,node.falseExpression,context)});
    if(node.nodeType==='FunctionCall'&&node.kind!=='typeConversion') {
      const target=nodes.get(node.expression?.referencedDeclaration);
      if(target?.nodeType==='FunctionDefinition')row.return_component_types=(target.returnParameters?.parameters||[]).map(typeOf);
      const {internal,args}=internalArguments(target,node.expression,node.arguments||[]);
      if(internal) {
        try {
          const model=boundedInternalReturns(method,{target,call:node,context,args,nodes,typeOf,
            build:(n,c)=>expression(method,n,c),expressionType:key=>expressionByID.get(key)?.type,expressionInfo:key=>expressionByID.get(key),synthetic:(n,f,o,c,x)=>synthetic(method,n,f,o,c,x),aggregates:context.source_aggregates?aggregateReturnAPI(method):null});
          row.binding_expression_id=model.expression_id;row.internal_return_model=model;
          row.binding_kind='function_return';row.operands.push(row.binding_expression_id);
          for(const missing of model.frontier||[])result.sections.frontier.push({frontier_id:id(method.method_id,'internal-loop-remainder',node.id,missing.ast_id,missing.conditions),method_id:method.method_id,
            ...missing,evidence_ids:[getEvidence(method,node)]});
        } catch(error) { row.unresolved_return_value=String(error.message).startsWith('INTERNAL_RETURN_')?String(error.message):'INTERNAL_FUNCTION_RETURN_REQUIRES_CONTROL_FLOW'; }
      } else if(/^t_function_(?:internal|external)_/.test(node.expression?.typeDescriptions?.typeIdentifier||'')&&target?.nodeType!=='FunctionDefinition'){
        row.unresolved_return_value='SOURCE_DYNAMIC_FUNCTION_RETURN_UNBOUND';
      } else if(target?.nodeType==='FunctionDefinition') {
        row.unresolved_return_value='EXTERNAL_FUNCTION_RETURN_NOT_BOUND_TO_EXECUTION';
        const dependency_id=id(expression_id,'external-return'),callsite=location(node);
        const delegate=nodes.get(target.scope)?.contractKind==='library';
        const normalize=d=>typeOf(d).replace(/ (memory|storage|calldata)( ref| pointer)?/g,'').replace(/^contract [^\[\]\s]+/,'address').replace(/^address payable/,'address');
        const signature=target.name+'('+(target.parameters?.parameters||[]).map(normalize).join(',')+')';
        const dependency={dependency_id,method_id:method.method_id,expression_id,source_callsite:callsite,
          representation_scope:context.entry_return_summary?'entry_return_summary':context.return_stack?.length?'internal_return_summary':'source_statement',
          target_expression_id:expression(method,node.expression?.expression,context),target_expression:sourceText(node.expression?.expression),
          signature,selector:target.functionSelector?'0x'+target.functionSelector:null,
          arguments:row.argument_expression_ids,return_types:(target.returnParameters?.parameters||[]).map(normalize),
          caller_code_address:method.code_address,caller_storage_address:method.storage_address,
          callee_msg_sender:delegate?'inherited_msg_sender':method.storage_address,callee_msg_value:delegate?'inherited_msg_value':'0',
          callee_storage_context:delegate?method.storage_address:'resolved_target_address',
          call_type:delegate?'DELEGATECALL':['pure','view'].includes(target.stateMutability)&&Number(source.compilation.compilerVersion.split('.')[1])>=5?'STATICCALL':'CALL',
          conditions:[...(context.conditions||[])],path:context.path,state_scope:'call_execution_state',
          status:'requires_target_identity_and_execution',evidence_ids:row.evidence_ids};
        dependency.source_callsite_id=id(method.method_id,callsite.file,callsite.start,callsite.length,dependency.call_type,signature);
        result.sections.external_returns||=[];result.sections.external_returns.push(dependency);row.external_return_dependency_id=dependency_id;
      }
    }
    if(node.nodeType==='FunctionCall'&&!row.external_return_dependency_id) {
      const callee=node.expression?.nodeType==='FunctionCallOptions'?node.expression.expression:node.expression;
      const nativeCall=['send','transfer'].includes(callee?.memberName)&&/^address(?: payable)?$/.test(typeOf(callee.expression));
      const callType={call:'CALL',staticcall:'STATICCALL',delegatecall:'DELEGATECALL',callcode:'CALLCODE'}[callee?.memberName]||(nativeCall?'CALL':null);
      if(callType){
        const callsite=location(node),dependency_id=id(expression_id,'external-return');
        const dependency={dependency_id,method_id:method.method_id,expression_id,source_callsite:callsite,
          source_callsite_id:id(method.method_id,callsite.file,callsite.start,callsite.length,callType,'raw-call'),
          representation_scope:context.entry_return_summary?'entry_return_summary':'source_statement',
          call_type:callType,signature:nativeCall?'native_'+callee.memberName:'raw_evm_call',selector:null,
          return_schema:nativeCall?'native_call_success_and_evm_return_bytes':'evm_return_bytes',return_types:nativeCall?(callee.memberName==='send'?['bool']:[]):['bool','bytes'],
          target_expression:sourceText(callee.expression),target_expression_id:expression(method,callee.expression,context),arguments:row.argument_expression_ids,
          caller_code_address:method.code_address,caller_storage_address:method.storage_address,state_scope:'call_execution_state',
          status:'requires_target_identity_and_execution',conditions:[...(context.conditions||[])],evidence_ids:row.evidence_ids};
        result.sections.external_returns||=[];result.sections.external_returns.push(dependency);row.external_return_dependency_id=dependency_id;
      }
    }
    if (node.nodeType === 'Identifier') {
      const declaration = nodes.get(node.referencedDeclaration);
      const immutable=runtimeValue(declaration);
      if(immutable!==null){row.resolved_value=immutable;row.value_basis='compiler_immutable_reference_and_snapshot_bytes';row.runtime_ranges=runtimeVerification.bindings[declaration.id].ranges}
      else if(declaration?.constant&&declaration.value){row.binding_expression_id=expression(method,declaration.value,context);row.binding_kind='constant_value';row.operands.push(row.binding_expression_id)}
      row.unresolved_local_reference = declaration?.nodeType === 'VariableDeclaration' && !declaration.stateVariable &&
        !method.parameters.some(p => p.source_ast_id === declaration.id) && !binding_expression_id&&!scalar;
    }
    if (node.nodeType === 'Literal') {
      row.raw_literal=String(node.value??node.hexValue??'');row.literal_kind=node.kind;row.subdenomination=node.subdenomination||null;
      if(['string','hexString','unicodeString'].includes(node.kind)&&typeof node.hexValue==='string'&&node.hexValue.length<=64)row.literal_hex_value=node.hexValue;
      if(node.kind==='number')Object.assign(row,numericLiteral(row.raw_literal,row.subdenomination));else row.value=row.raw_literal;
    }
    const enumDefinition=node.nodeType==='MemberAccess'?nodes.get(node.expression?.referencedDeclaration):null,
      legacyEnumMember=enumDefinition?.nodeType==='EnumDefinition'&&/^type\(enum\s/.test(typeOf(node.expression))?(enumDefinition.members||[]).find(m=>m.name===node.memberName):null,
      enumValue=enumValues.get(node.referencedDeclaration)||enumValues.get(legacyEnumMember?.id);
    if(enumValue&&/^enum\s/.test(type))Object.assign(row,{symbolic_format:'literal',literal_kind:'enum_member',value:String(enumValue.index),enum_type_declaration_id:enumValue.definition.id,enum_member_declaration_id:enumValue.member.id,enum_member_index:enumValue.index,enum_member_count:enumValue.count,enum_value_basis:'compiler_enum_definition_member_order',evidence_ids:unique([...row.evidence_ids,getEvidence(method,enumValue.member),getEvidence(method,enumValue.definition)])});
    attachAggregate(method,node,context,row);
    result.sections.expressions.push(row);
    expressionByID.set(expression_id,row);
    consumeExpression();
    return expression_id;
  };
  const stateAccess = (method, node, access, context, valueNode, operation = null) => {
    const declaration=nodes.get(node?.referencedDeclaration);
    if(node?.nodeType==='Identifier'&&declaration&&!declaration.stateVariable&&pointerType(declaration))return;
    const projections=projectStorageCases(node,context,method),fallback=nodes.get(rootDeclaration(node)),storageIDs=[],cases=[];
    if(!projections.length&&!fallback?.stateVariable){
      if(nodes.get(rootDeclaration(node))?.storageLocation==='storage'){
        const storage_access_binding_id=id(method.method_id,node.id,access,context.path,'storage-access'),value=context.storage_references?.get(rootDeclaration(node));
        if(!(result.sections.storage_access_bindings||[]).some(r=>r.storage_access_binding_id===storage_access_binding_id)){
          result.sections.storage_access_bindings||=[];result.sections.storage_access_bindings.push({storage_access_binding_id,method_id:method.method_id,expression_id:expression(method,node,context),
            access,operation,cases:[],status:'partial',unresolved:value?.unresolved||['STORAGE_REFERENCE_ORIGIN_UNAVAILABLE'],evidence_ids:[getEvidence(method,node)]});
          method.completeness.unresolved.push({code:'STORAGE_REFERENCE_ACCESS_UNRESOLVED',ast_id:node.id,storage_access_binding_id});
        }
      }return;
    }
    const items=projections.length?projections:[{projection:null,conditions:[],storage_reference_ids:[],unresolved:[]}];
    const bindingID=id(method.method_id,node.id,access,context.path,'storage-access');
    for(const[itemIndex,item]of items.entries()){
    const projection=item.projection,base=projection?.root_state_ast_id??rootDeclaration(node),variable=nodes.get(base),namespaceField=projection?.namespace_id&&!projection.namespace_root?projection:null;
    if((!variable?.stateVariable&&!namespaceField)||variable.constant||variable.mutability==='immutable')continue;
    const evidence_id=getEvidence(method,node),slot=layoutByID.get(base),storage_id=items.length===1?id(method.method_id,node.id,access,context.path):id(method.method_id,node.id,access,context.path,'reference-case',itemIndex);
    if(result.sections.storage.some(s=>s.storage_id===storage_id))continue;
    const state_id=id(result.snapshot.code_hash,result.snapshot.storage_address,'state',base),accessText=node.nodeType==='Identifier'?node.name:sourceText(node),
      baseOnly=namespaceField?namespaceField.static_slot===null||layout.types?.[namespaceField.type_id]?.encoding!=='inplace'||Boolean(layout.types?.[namespaceField.type_id]?.members||layout.types?.[namespaceField.type_id]?.base):
      accessText!==variable.name||/^(mapping|struct)/.test(typeOf(variable))||typeOf(variable).includes('[');
    const row = { storage_id, state_id, state_ast_id:base, method_id: method.method_id, variable: variable.name,
      code_address: result.snapshot.code_address, storage_address: result.snapshot.storage_address,
      access, slot: namespaceField?.static_slot??slot?.slot??null, offset: namespaceField?.offset??slot?.offset??null, type: typeOf(node), variable_type: typeOf(variable),
      slot_role:baseOnly?'layout_base':'direct_slot',key_expression: accessText, key_expression_id: expression(method, node, context),
      value_expression: valueNode ? sourceText(valueNode) : null, value_expression_id: expression(method, valueNode, context), operation,
      conditions: unique([...context.conditions,...item.conditions]), evidence_ids: [evidence_id], layout_status: slot ? slot.layout_basis || 'verified_compiler_layout' : 'layout_unavailable',
      storage_projection: projection,storage_reference_ids:item.storage_reference_ids,storage_access_binding_id:bindingID,projection_unresolved:item.unresolved,order:method.step_ids.length,source_location:location(node),...(namespaceField?{namespace_id:namespaceField.namespace_id,
        namespace_struct_ast_id:namespaceField.namespace_struct_ast_id,namespace_root_slot:namespaceField.base_slot}:{} ) };
    result.sections.storage.push(row);
    method.storage_ids.push(storage_id);
    storageIDs.push(storage_id);cases.push({storage_id,storage_reference_ids:item.storage_reference_ids,conditions:row.conditions,projection});
    }
    if(cases.length){
      const unresolved=unique(items.flatMap(v=>v.unresolved));result.sections.storage_access_bindings||=[];
      if(unresolved.length)method.completeness.unresolved.push({code:'STORAGE_REFERENCE_DEPENDENCY_UNRESOLVED',ast_id:node.id,storage_access_binding_id:bindingID,reasons:unresolved});
      result.sections.storage_access_bindings.push({storage_access_binding_id:bindingID,method_id:method.method_id,expression_id:expression(method,node,context),
        access,operation,storage_ids:storageIDs,cases,status:unresolved.length?'partial':'source_locations_bound',unresolved,path:context.path,
        order:method.step_ids.length,evidence_ids:[getEvidence(method,node)]});
      const e=expressionByID.get(expression(method,node,context));if(e)e.storage_access_binding_ids=unique([...(e.storage_access_binding_ids||[]),bindingID]);
    }
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
  const decodeEncodedTransfer=(encoded,context,{raw=false,describeOnly=false}={})=>{
    if(!raw&&context.source_memory){const binding=resolveSourceBytes(encoded,context);if(binding){
      const captured=context.source_memory.conditionalSingle(binding);if(!captured)return null;
      return transferProfile({selector:captured.record.selector,args:captured.argumentNodes,encoding:captured.record.encoding,node:encoded,
        argument_expression_ids:captured.record.argument_expression_ids,source_memory_version_id:captured.record.source_memory_version_id,
        condition_groups:captured.cases.map(c=>c.conditions),assumptions:captured.assumptions||[]});
    }return null;}
    encoded=resolveArgument(encoded,context);
    if(encoded?.nodeType!=='FunctionCall'||!builtin(encoded.expression?.expression,'abi',nodes))return null;
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
      if(n.nodeType==='MemberAccess'&&n.memberName==='selector'){
        const fn=nodes.get(n.expression?.referencedDeclaration);if(fn?.nodeType!=='FunctionDefinition')return null;
        if(fn.functionSelector)return'0x'+fn.functionSelector;
        const signature=fn.name+'('+(fn.parameters?.parameters||[]).map(p=>typeOf(p).replace(/ (memory|storage|calldata)( ref| pointer)?/g,'').replace(/^contract .+$/,'address').replace(/^address payable$/,'address')).join(',')+')';
        return'0x'+Buffer.from(keccak_256(Buffer.from(signature))).toString('hex').slice(0,8);
      }
      if(n.nodeType==='FunctionCall'&&n.kind==='typeConversion'&&typeOf(n)==='bytes4')return selectorValue(n.arguments?.[0],next);
      if(n.nodeType==='FunctionCall'&&builtin(n.expression,'keccak256',nodes)){const value=stringValue(n.arguments?.[0]);return value===null?null:'0x'+Buffer.from(keccak_256(Buffer.from(value))).toString('hex').slice(0,8);}
      return null;
    };
    let selector,args,parameterTypes;
    const encoding=encoded.expression.memberName;
    if(encoding==='encodeWithSelector'){selector=selectorValue(encoded.arguments?.[0]);args=encoded.arguments?.slice(1);}
    else if(encoding==='encodeWithSignature'){const signature=stringValue(encoded.arguments?.[0]);if(signature===null&&!describeOnly)return null;selector=signature===null?null:'0x'+Buffer.from(keccak_256(Buffer.from(signature))).toString('hex').slice(0,8);args=encoded.arguments.slice(1);}
    else if(encoding==='encodeCall'){
      const fn=nodes.get(encoded.arguments?.[0]?.referencedDeclaration);args=encoded.arguments?.[1]?.components;
      if(fn?.nodeType!=='FunctionDefinition'||!Array.isArray(args))return null;
      const signature=fn.name+'('+(fn.parameters?.parameters||[]).map(sourceAbiType).join(',')+')';selector='0x'+Buffer.from(keccak_256(Buffer.from(signature))).toString('hex').slice(0,8);
      parameterTypes=(fn.parameters?.parameters||[]).map(sourceAbiType);
    }else if(encoding==='encode'){selector=null;args=encoded.arguments||[];}else return null;
    const description={selector,args,encoding,node:encoded,parameterTypes};if(describeOnly)return description;
    return transferProfile(description);
  };
  function transferProfile(description){
    const{selector,args}=description;
    const signatures={'0xa9059cbb':'transfer(address,uint256)','0x23b872dd':'transferFrom(address,address,uint256)','0x095ea7b3':'approve(address,uint256)'},signature=signatures[selector];
    if(!signature||args?.length!==(selector==='0x23b872dd'?3:2)||args.some(n=>!n))return null;
    if(args.slice(0,-1).some(n=>!/^(?:address(?: payable)?|contract .+)$/.test(typeOf(n))))return null;
    const amount=args.at(-1),literal=amount.nodeType==='Literal'&&amount.kind==='number'?numericLiteral(String(amount.value),amount.subdenomination).value:null;
    if(!/^uint\d+$/.test(typeOf(amount))&&!(literal!==null&&BigInt(literal)>=0n&&BigInt(literal)<1n<<256n))return null;
    return {...description,selector,signature,args,name:signature.split('(')[0]};
  }
  function sourceAbiType(node,seen=new Set()){
    if(!(seen instanceof Set))seen=new Set();
    if(node?.nodeType==='ElementaryTypeName')return node.name==='uint'?'uint256':node.name==='int'?'int256':node.name;
    if(node?.nodeType==='ArrayTypeName')return sourceAbiType(node.baseType,seen)+'['+(node.length?.value||'')+']';
    let type=typeOf(node).replace(/^type\((.*)\)$/,'$1').replace(/ (memory|storage|calldata)( ref| pointer)?/g,'').replace(/^contract [^\[\]\s]+/,'address').replace(/^address payable/,'address');
    if(/^(?:int_const|rational_const)/.test(type))return type.includes('-')?'int256':'uint256';
    if(node?.nodeType==='Literal'&&node.kind==='string')return'string';
    if(node?.nodeType==='Literal'&&node.kind==='hexString')return'bytes';
    if(type.startsWith('tuple('))type=type.slice(5);
    const identifier=node?.typeDescriptions?.typeIdentifier||node?.typeName?.typeDescriptions?.typeIdentifier||'',declared=nodes.get(node?.typeName?.referencedDeclaration??node?.referencedDeclaration),
      embedded=/t_(?:struct|enum|userDefinedValueType)\$_.*?_\$(\d+)/.exec(identifier),definition=embedded?nodes.get(Number(embedded[1])):declared;
    const suffix=(/(?:\[\d*\])+$/.exec(type)||[''])[0];
    if(definition?.nodeType==='StructDefinition'&&!seen.has(definition.id))return'('+definition.members.map(p=>sourceAbiType(p,new Set([...seen,definition.id]))).join(',')+')'+suffix;
    if(definition?.nodeType==='EnumDefinition')return'uint8'+suffix;
    if(definition?.nodeType==='UserDefinedValueTypeDefinition'&&!seen.has(definition.id))return sourceAbiType(definition.underlyingType,new Set([...seen,definition.id]))+suffix;
    if(/^function \(/.test(type)&&/ external/.test(type))return'function';return type;
  }
  const sourceBytesType=node=>/^(?:bytes|string)(?: (?:memory|calldata)(?: ref| pointer)?)?$/.test(typeOf(node));
  const unknownBytes=reason=>({cases:[],unresolved:[reason]});
  function sourceAbiValue(method,node,type,context){
    if(node?.nodeType==='Literal'){
      if(node.kind==='bool')return node.value==='true';
      if(node.kind==='number'){const n=BigInt(numericLiteral(String(node.value),node.subdenomination).value),fixed=/^bytes(\d+)$/.exec(type);
        return type==='address'?'0x'+BigInt.asUintN(160,n).toString(16).padStart(40,'0'):fixed?'0x'+BigInt.asUintN(Number(fixed[1])*8,n).toString(16).padStart(Number(fixed[1])*2,'0'):n;}
      if(node.kind==='string'||node.kind==='hexString'){const bytes=Buffer.from(node.hexValue||Buffer.from(node.value||'').toString('hex'),'hex');return type==='string'&&node.kind==='string'?node.value:'0x'+bytes.toString('hex');}
    }
    if(node?.nodeType==='FunctionCall'&&node.kind==='typeConversion'){
      const original=node.arguments?.[0];
      if(original?.nodeType==='Literal'&&original.kind==='number'){
        const n=BigInt(numericLiteral(String(original.value),original.subdenomination).value),integer=/^(u?int)(\d+)$/.exec(type),fixed=/^bytes(\d+)$/.exec(type);
        if(type==='address')return'0x'+BigInt.asUintN(160,n).toString(16).padStart(40,'0');
        if(integer)return(integer[1]==='int'?BigInt.asIntN:BigInt.asUintN)(Number(integer[2]),n);
        if(fixed)return'0x'+BigInt.asUintN(Number(fixed[1])*8,n).toString(16).padStart(Number(fixed[1])*2,'0');
      }
      if(type==='bytes'&&original?.nodeType==='Literal'&&['string','hexString'].includes(original.kind))return'0x'+(original.hexValue||Buffer.from(original.value||'').toString('hex'));
    }
    if(node?.nodeType==='TupleExpression'&&node.components?.every(Boolean)){
      try{const parsed=parseAbiType(type);return node.components.map((n,i)=>sourceAbiValue(method,n,parsed.kind==='array'?parsed.element.canonical:parsed.components[i].canonical,context));}catch{}
    }
    if(type==='bytes'||type==='string'){
      const value=resolveSourceBytes(node,context),read=value&&context.source_memory.read(value);
      if(read?.status==='exact'&&read.cases.length===1)return{cells:read.cases[0].bytes,length:read.cases[0].bytes.length};
    }
    const expression_id=expression(method,node,context),row=expressionByID.get(expression_id);
    return{expression:sourceText(node),expression_id,evidence_ids:row?.evidence_ids||[],type};
  }
  function resolveSourceBytes(node,context,seen=new Set()){
    const memory=context.source_memory;if(!memory||!node)return null;
    const effectGap=memory.shared.globalEvaluationGap||memory.shared.evaluationGaps?.get(expressionIdentity(memory.method,node,context));if(effectGap)return unknownBytes(effectGap);
    const key=node.id+':'+context.path;if(seen.has(key)||seen.size>32)return unknownBytes('SOURCE_BYTES_BINDING_CYCLE_OR_BUDGET');const next=new Set([...seen,key]);
    if(node.nodeType==='Identifier'){
      if(memory.bindings.has(node.referencedDeclaration))return memory.get(node.referencedDeclaration);
      const actual=context.parameter_arguments?.[node.referencedDeclaration];if(actual&&actual.id!==node.id)return resolveSourceBytes(actual,context,next);
      return sourceBytesType(node)?unknownBytes('SOURCE_BYTES_ARGUMENT_UNBOUND'):null;
    }
    if(node.nodeType==='FunctionCall'&&node.kind==='typeConversion'&&sourceBytesType(node))return resolveSourceBytes(node.arguments?.[0],context,next);
    if(node.nodeType==='MemberAccess'&&context.source_aggregates){
      const reference=node.expression?.nodeType==='Identifier'?context.source_aggregates.get(node.expression.referencedDeclaration):null;
      if(reference){
        const projected=context.source_aggregates.project(reference,{declaration_id:node.referencedDeclaration,type:typeOf(node)},{context:aggregateContext(memory.method,node,context)});
        if(projected?.kind==='source_bytes_value')return projected.binding;
        return unknownBytes(projected?.unresolved?.[0]||'SOURCE_BYTES_AGGREGATE_FIELD_UNAVAILABLE');
      }
    }
    const expression_id=expression(memory.method,node,context);
    if(node.nodeType==='FunctionCall'){
      const returned=memory.shared.callReturns.get(expression_id);if(returned)return returned.components?.[0]||unknownBytes('SOURCE_BYTES_RETURN_COMPONENT_UNAVAILABLE');
      if(builtin(node.expression?.expression,'abi',nodes)){
        if(memory.shared.expressionBindings.has(expression_id))return memory.shared.expressionBindings.get(expression_id);
        const description=decodeEncodedTransfer(node,context,{raw:true,describeOnly:true});if(!description)return unknownBytes('SOURCE_ABI_ENCODING_UNSUPPORTED');
        const method=memory.method,args=description.args,types=description.parameterTypes||args.map(sourceAbiType),argumentExpressionIds=args.map(n=>expression(method,n,context));
        const selectorNode=node.arguments?.[0],selectorExpression=description.encoding==='encodeWithSelector'&&selectorNode?{expression:sourceText(selectorNode),expression_id:expression(method,selectorNode,context),evidence_ids:[getEvidence(method,selectorNode)]}:
          description.encoding==='encodeWithSignature'&&selectorNode?{expression:'first4_keccak('+sourceText(selectorNode)+')',expression_id:expression(method,selectorNode,context),evidence_ids:[getEvidence(method,selectorNode)]}:null;
        const value=memory.capture({originExpressionId:expression_id,encoding:description.encoding,selector:description.selector,selectorPresent:description.encoding!=='encode',selectorExpression,
          types,values:args.map((n,i)=>sourceAbiValue(method,n,types[i],context)),argumentExpressionIds,argumentTexts:args.map(sourceText),argumentNodes:args,
          context:{path:context.path,order:expressionByID.get(expression_id)?.step_order,conditions:context.conditions},evidence_ids:[getEvidence(method,node)]});
        memory.shared.expressionBindings.set(expression_id,value);return value;
      }
      return sourceBytesType(node)?unknownBytes('SOURCE_BYTES_FUNCTION_RETURN_UNBOUND'):null;
    }
    if(node.nodeType==='Literal'&&['string','hexString'].includes(node.kind)){
      if(memory.shared.expressionBindings.has(expression_id))return memory.shared.expressionBindings.get(expression_id);
      const raw=Buffer.from(node.hexValue||Buffer.from(node.value||'').toString('hex'),'hex'),value=memory.capture({originExpressionId:expression_id,encoding:'raw_bytes',rawCells:Array.from(raw,value=>({kind:'constant',value})),
        context:{path:context.path,order:expressionByID.get(expression_id)?.step_order,conditions:context.conditions},evidence_ids:[getEvidence(memory.method,node)]});
      memory.shared.expressionBindings.set(expression_id,value);return value;
    }
    if(node.nodeType==='Conditional')return memory.shared.expressionBindings.get(expression_id)||unknownBytes('SOURCE_BYTES_CONDITIONAL_VALUE_REQUIRES_BRANCH_BINDING');
    return sourceBytesType(node)?unknownBytes('SOURCE_BYTES_VALUE_UNSUPPORTED'):null;
  }
  function sourceBytesComponent(method,node,component,context){
    if(node?.nodeType==='TupleExpression')return resolveSourceBytes(node.components?.[component],context)||unknownBytes('SOURCE_BYTES_TUPLE_COMPONENT_UNAVAILABLE');
    const returned=node&&context.source_memory?.shared.callReturns.get(expression(method,node,context));
    return returned?.components?.[component]||unknownBytes('SOURCE_BYTES_TUPLE_COMPONENT_UNAVAILABLE');
  }
  function bindSourceBytes(method,context,declaration,value,component=null){
    if(!context.source_memory||!declaration||declaration.stateVariable||!sourceBytesType(declaration))return;
    context.source_memory.bind(declaration.id,component===null?resolveSourceBytes(value,context):sourceBytesComponent(method,value,component,context));
  }
  function collectSourceReturn(method,fn,node,context){
    if(!context.source_memory)return;
    const parameters=fn?.returnParameters?.parameters||[];
    for(const [i,p]of parameters.entries())if(sourceBytesType(p)){
      const value=node?parameters.length===1?resolveSourceBytes(node,context):sourceBytesComponent(method,node,i,context):context.source_memory.get(p.id);
      context.source_memory.addReturn(value||unknownBytes('SOURCE_BYTES_RETURN_UNAVAILABLE'),context.conditions,i);
    }
    context.source_memory.recordExit(context.conditions);
  }

  function legacyLowLevelBytes(method,node,context,call){
    const compiler=source.compilation.compilerVersion.split('+')[0];
    if(Number(compiler.split('.')[1])>=5)return resolveSourceBytes(node.arguments?.[0],context);
    if(compiler!=='0.4.26')return unknownBytes('SOURCE_LEGACY_CALL_COMPILER_ENCODING_UNVERIFIED');
    const memory=context.source_memory;if(!memory)return null;
    let cases=[{cells:[],conditions:[],sourceVersions:[],predicate:null}];
    const args=node.arguments||[];
    for(const[index,arg]of args.entries()){
      const type=sourceAbiType(arg);let parts;
      if(['bytes','string'].includes(type)){
        const value=resolveSourceBytes(arg,context),read=memory.read(value);
        if(read.unresolved.length||!read.cases.length)return unknownBytes(read.unresolved[0]||'SOURCE_LEGACY_CALL_BYTES_UNAVAILABLE');
        parts=read.cases.map(c=>({cells:[...c.bytes,...Array.from({length:(32-c.bytes.length%32)%32},()=>({kind:'constant',value:0}))],conditions:c.conditions,sourceVersions:[c.source_memory_version_id],predicate:c.predicate||null}));
      }else if(/^(?:u?int\d*|address|bool|bytes(?:[1-9]|[12]\d|3[0-2]))$/.test(type)){
        let encoded;try{encoded=encodeAbi([type],[sourceAbiValue(method,arg,type,context)],{identity:id(method.method_id,node.id,index,'legacy-call')});}catch(error){if(!(error instanceof AbiFault))throw error;return unknownBytes(error.code);}
        if(!encoded.cells||encoded.unresolved.length)return unknownBytes(encoded.unresolved[0]||'SOURCE_LEGACY_CALL_ARGUMENT_UNAVAILABLE');
        parts=[{cells:index===0&&type==='bytes4'?encoded.cells.slice(0,4):encoded.cells,conditions:[],sourceVersions:[]}];
      }else return unknownBytes('SOURCE_LEGACY_CALL_ARGUMENT_TYPE_UNMODELED');
      const next=[];for(const a of cases)for(const b of parts){
        const predicate=sourceBytePredicateAnd(a.predicate,b.predicate),checked=memory._bounded({cases:[{predicate}],unresolved:[]});
        if(checked.unresolved.length)return unknownBytes(checked.unresolved[0]);if(!checked.cases.length)continue;
        if(next.length>=16)return unknownBytes('SOURCE_LEGACY_CALL_CASE_BUDGET');if(a.cells.length+b.cells.length>65536)return unknownBytes('SOURCE_MEMORY_OBJECT_OR_CELL_BUDGET');
        next.push({cells:[...a.cells,...b.cells],conditions:unique([...a.conditions,...b.conditions]),sourceVersions:unique([...a.sourceVersions,...b.sourceVersions]),predicate});
      }cases=next;
    }
    call.input_encoding='solidity_0_4_26_legacy_low_level_arguments';const values=cases.map((c,index)=>{
      const value=memory.capture({originExpressionId:expression(method,node,context),encoding:call.input_encoding,rawCells:c.cells,argumentExpressionIds:args.map(a=>expression(method,a,context)),argumentTexts:args.map(sourceText),argumentNodes:args,
        context:{path:context.path+':legacy-arguments:'+index,conditions:unique([...context.conditions,...c.conditions]),order:method.step_ids.length-1},evidence_ids:[getEvidence(method,node)]});
      for(const v of value.cases||[]){const record=memory.entry(v.source_memory_version_id)?.record;if(record)record.legacy_input_source_version_ids=c.sourceVersions;}
      return{...value,cases:value.cases.map(v=>({...v,predicate:sourceBytePredicateAnd(v.predicate,c.predicate),conditions:unique([...(v.conditions||[]),...c.conditions])}))};
    });
    return{cases:values.flatMap(v=>v.cases),unresolved:unique(values.flatMap(v=>v.unresolved))};
  }
  function sourceCallBinding(method,node,context,call,value){
    const memory=context.source_memory;if(!memory||!value)return null;
    value=memory.current(value);const read=memory.read(value),call_input_binding_id=id(call.call_id,'source-bytes-input'),versions=unique((value.cases||[]).map(c=>c.source_memory_version_id).filter(Boolean)),
      entries=versions.map(v=>memory.entry(v)).filter(Boolean),lengths=unique(entries.map(e=>e.record.byte_length)),eid=getEvidence(method,node);
    const row={call_input_binding_id,method_id:method.method_id,call_id:call.call_id,code_hash:method.code_hash,representation_scope:'source_object',
      address_space:'compiler_object_relative',input_encoding:call.input_encoding||'source_bytes_payload',source_memory_version_ids:versions,source_memory_phi_id:value.source_memory_phi_id||null,
      status:read.status,cases:read.cases,byte_length:lengths.length===1?lengths[0]:null,unresolved:read.unresolved,
      target_expression_id:call.target_expression_id,conditions:[...context.conditions],step_order:method.step_ids.length,
      settlement:'call_encoding_only_execution_success_and_target_semantics_unverified',all_execution_paths_verified:false,
      evidence_ids:unique([eid,...entries.flatMap(e=>e.record.evidence_ids)])};
    result.sections.call_input_bindings||=[];result.sections.call_input_bindings.push(row);call.call_input_binding_id=call_input_binding_id;
    call.source_memory_version_ids=versions;call.memory_input_status=read.status;
    result.sections.abi_values||=[];
    for(const [i,c]of read.cases.entries()){
      const entry=memory.entry(c.source_memory_version_id);if(!entry?.record.valid_encoding)continue;
      const r=entry.record,prefix=r.encoding==='encode'?0:4;
      try{const decoded=decodeAbi(c.bytes.slice(prefix),r.argument_types,{identity:id(call_input_binding_id,i,'source-types')});
        for(const item of decoded.nodes)result.sections.abi_values.push({...item,method_id:method.method_id,call_id:call.call_id,call_input_binding_id,
          source_memory_version_id:r.source_memory_version_id,encoding_prefix_bytes:prefix,type_basis:'source_encoder_types',...(c.predicate?{predicate:c.predicate}:{}),conditions:unique([...context.conditions,...c.conditions]),evidence_ids:row.evidence_ids});
      }catch(error){if(!(error instanceof AbiFault))throw error;row.unresolved=unique([...row.unresolved,error.code]);row.abi_decode_status='partial';}
    }
    return row;
  }
  function analyzeSourceAbiDecode(method,node,context){
    const memory=context.source_memory;if(!memory)return;
    const typeNodes=node.arguments?.[1]?.components||[],types=typeNodes.map(n=>sourceAbiType(n?.typeName||n)),input=resolveSourceBytes(node.arguments?.[0],context),read=memory.read(input),
      expression_id=expression(method,node,context),abi_decoding_id=id(expression_id,'source-abi-decode'),evidence_ids=[getEvidence(method,node)],cases=[],valueIDs=[],unresolved=[...read.unresolved];
    result.sections.abi_decodings||=[];result.sections.abi_values||=[];
    for(const [i,c]of read.cases.entries()){
      try{const decoded=decodeAbi(c.bytes,types,{identity:id(abi_decoding_id,i)});cases.push({source_memory_version_id:c.source_memory_version_id,conditions:c.conditions,...(c.predicate?{predicate:c.predicate}:{}),
        status:decoded.status,value_ids:decoded.values.map(v=>v.abi_value_id),canonical:decoded.canonical,unresolved:decoded.unresolved});
        unresolved.push(...decoded.unresolved);valueIDs.push(...decoded.nodes.map(v=>v.abi_value_id));
        for(const value of decoded.nodes)result.sections.abi_values.push({...value,abi_decoding_id,method_id:method.method_id,source_memory_version_id:c.source_memory_version_id,
          type_basis:'source_abi_decode_types',...(c.predicate?{predicate:c.predicate}:{}),conditions:unique([...context.conditions,...c.conditions]),evidence_ids});
      }catch(error){if(!(error instanceof AbiFault))throw error;unresolved.push(error.code);}
    }
    const captured=memory.single(input),sameTypes=captured?.record.encoding==='encode'&&hash(captured.record.argument_types)===hash(types)&&read.status==='exact';
    const row={abi_decoding_id,method_id:method.method_id,expression_id,input_expression_id:expression(method,node.arguments?.[0],context),types,cases,
      source_memory_version_ids:unique((input?.cases||[]).map(c=>c.source_memory_version_id).filter(Boolean)),abi_value_ids:valueIDs,
      status:!cases.length||unresolved.length?'partial':cases.some(c=>c.status==='invalid')?'invalid_representation':read.status==='conditional'?'conditional':'represented_source_bytes_decoded',
      unresolved:unique([...unresolved,...(!cases.length?['SOURCE_ABI_INPUT_BYTES_UNAVAILABLE']:[])]),
      source_argument_equivalence:sameTypes?{encoding_version_id:captured.record.source_memory_version_id,argument_expression_ids:captured.record.argument_expression_ids}:null,
      conditions:[...context.conditions],evidence_ids,validation_scope:'source_byte_representation_not_external_return_execution_or_all_paths'};
    result.sections.abi_decodings.push(row);const e=expressionByID.get(expression_id);e.abi_decoding_id=abi_decoding_id;
    if(sameTypes){e.abi_component_expression_ids=[...captured.record.argument_expression_ids];if(types.length===1){e.binding_expression_id=e.abi_component_expression_ids[0];e.binding_kind='source_abi_same_type_decode';}}
    else e.unresolved_model_reasons=unique([...(e.unresolved_model_reasons||[]),'SOURCE_ABI_DECODE_VALUE_REQUIRES_BYTE_AND_TYPE_BINDING']);
  }
  const analyzeCall = (method, node, context) => {
    let callee = node.expression, options = {};
    if (callee?.nodeType === 'FunctionCallOptions') { options = Object.fromEntries((callee.names || []).map((n, i) => [n, callee.options[i]])); callee = callee.expression; }
    const name = callee?.name || callee?.memberName;
    const baseType = typeOf(callee?.expression);
    if(name==='decode'&&builtin(callee?.expression,'abi',nodes)){analyzeSourceAbiDecode(method,node,context);return;}
    if (['require', 'assert'].some(n=>builtin(callee,n,nodes))) { guard(method, node, node.arguments?.[0], context, name === 'assert' ? '触发 panic' : 'revert'); return; }
    if (builtin(callee,'revert',nodes)) { const step = addStep(method, node, 'revert', `回滚：${sourceText(node)}`, context); method.failure_ids.push(step.step_id); return; }
    if(/^t_function_array(?:push|pop)_/.test(callee?.typeDescriptions?.typeIdentifier||'')){
      const step=addStep(method,node,'storage_array_mutation',sourceText(node),context),reason='SOURCE_ARRAY_MUTATION_REQUIRES_LENGTH_AND_ELEMENT_VERSION';
      result.sections.frontier.push({frontier_id:id(step.step_id,'array-mutation-frontier'),method_id:method.method_id,reason,operation:name,
        target_expression_id:expression(method,callee.expression,context),conditions:[...context.conditions],evidence_ids:step.evidence_ids});
      method.completeness.unresolved.push({code:reason,ast_id:node.id});context.storage_references?.invalidate(reason);return;
    }
    const targetNode = nodes.get(namespaceCalleeID(node,nodes,functions)??callee?.referencedDeclaration);
    const library=nodes.get(targetNode?.scope)?.contractKind==='library';
    const rawAddressCall=callee?.nodeType==='MemberAccess'&&/^address(?: payable)?$/.test(baseType)&&targetNode?.nodeType!=='FunctionDefinition'&&['call','delegatecall','staticcall','callcode'].includes(name);
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
          if((targetNode.parameters?.parameters||[]).some((p,i)=>p.storageLocation==='storage'&&(!pointerType(p)||!projectStorageCases(internal.args[i],context,method).length)))method.completeness.unresolved.push({code:'LIBRARY_STORAGE_REFERENCE_REQUIRES_ALIAS_ANALYSIS',ast_id:node.id});
          const byteArgumentValues=new Map((targetNode.parameters?.parameters||[]).map((p,i)=>[p.id,sourceBytesType(p)?resolveSourceBytes(internal.args[i],context):null]));
          const callContext={ ...context, conditions:[...context.conditions],unchecked:false,function_frame:targetNode.id, path: `${context.path}/call:${node.id}`, stack: [...context.stack, targetNode.id],
            source_memory:context.source_memory?.fork({newFunction:true}),
            storage_references:context.storage_references?.fork({newFunction:true}),
            source_scalars:context.source_scalars?.fork({newFunction:true,frameId:context.path+'/call:'+node.id}),
            source_aggregates:context.source_aggregates?.fork({newFunction:true,frameId:context.path+'/call:'+node.id}),
            namespace_aliases:new Map(context.namespace_aliases||[]),
            parameter_bindings:Object.fromEntries((targetNode.parameters?.parameters||[]).map((p,i)=>[p.id,sourceText(internal.args[i])])),
            parameter_arguments:{...context.parameter_arguments,...Object.fromEntries((targetNode.parameters?.parameters||[]).map((p,i)=>[p.id,internal.args[i]]))},
            parameter_expression_bindings:{...context.parameter_expression_bindings,...Object.fromEntries((targetNode.parameters?.parameters||[]).map((p,i)=>[p.id,expression(method,internal.args[i],context)]))} };
          initializeScalars(method,targetNode,callContext,internal.args,context);
          initializeAggregates(method,targetNode,callContext,internal.args,context);
          for(const [i,p]of (targetNode.parameters?.parameters||[]).entries())if(sourceBytesType(p)&&callContext.source_memory)
            callContext.source_memory.bind(p.id,byteArgumentValues.get(p.id));
          for(const[i,p]of(targetNode.parameters?.parameters||[]).entries())if(pointerType(p)&&callContext.storage_references)
            callContext.storage_references.bind(p.id,resolveStorageReference(method,internal.args[i],context));
          const flow=analyzeFunctionWithModifiers(method,targetNode,callContext);
          if(callContext.source_memory){
            if(!flow)collectSourceReturn(method,targetNode,null,callContext);
            const allReturningPathsCovered=!targetNode.modifiers?.length&&(!flow||['return','revert','terminated'].includes(flow));
            const components=(targetNode.returnParameters?.parameters||[]).map((p,i)=>sourceBytesType(p)?callContext.source_memory.returned({component:i,allReturningPathsCovered}):null);
            context.source_memory.propagateCall(callContext.source_memory,{allReturningPathsCovered});
            context.source_memory.shared.callReturns.set(expression(method,node,context),{components,all_returning_paths_covered:allReturningPathsCovered});
            if(callContext.storage_references){
              if(!flow)collectStorageReturn(method,targetNode,null,callContext);
              const components=(targetNode.returnParameters?.parameters||[]).map((p,i)=>pointerType(p)?callContext.storage_references.returned(i,{allReturningPathsCovered}):null);
              context.storage_references.shared.callReturns.set(expression(method,node,context),{components,all_returning_paths_covered:allReturningPathsCovered});
            }
          }
          let branched=false;for(const body of [targetNode.body,...(targetNode.modifiers||[]).map(m=>nodes.get(m.modifierName?.referencedDeclaration)?.body)])visit(body,n=>{if(['IfStatement','ForStatement','WhileStatement','DoWhileStatement','TryStatement'].includes(n.nodeType))branched=true});
          if(!branched)context.conditions.push(...callContext.conditions.filter(c=>!context.conditions.includes(c)));
        } else {method.completeness.unresolved.push({ code: 'RECURSIVE_OR_DEEP_INTERNAL_CALL', ast_id: node.id });
          context.source_memory?.invalidateAll('SOURCE_BYTES_UNEXPANDED_INTERNAL_CALL',{ast_id:node.id,path:context.path});
          context.source_aggregates?.invalidateAll('SOURCE_AGGREGATE_UNEXPANDED_INTERNAL_CALL',aggregateContext(method,node,context));}
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
      else if(/^t_function_internal_/.test(callee?.typeDescriptions?.typeIdentifier||'')||/^function\b/.test(typeOf(callee))&&!/\bexternal\b/.test(typeOf(callee))){context.source_memory?.invalidateAll('SOURCE_BYTES_DYNAMIC_INTERNAL_CALL',{ast_id:node.id,path:context.path});context.source_aggregates?.invalidateAll('SOURCE_AGGREGATE_DYNAMIC_INTERNAL_CALL',aggregateContext(method,node,context));}
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
        failure_propagation: rawAddressCall||name==='send'&&/^address(?: payable)?$/.test(baseType)&&targetNode?.nodeType!=='FunctionDefinition' ? 'returns_success_flag' : 'reverts_on_failure',
        conditions: [...context.conditions], parameter_bindings: context.parameter_bindings || {}, evidence_ids: [getEvidence(method, node)] };
      result.sections.calls.push(call); method.call_ids.push(call_id);
      addStep(method, node, 'external_call', `${kind} 调用 ${target_expression || '新合约'}：${sourceText(node)}`, context, { call_id });
      if(rawAddressCall)sourceCallBinding(method,node,context,call,legacyLowLevelBytes(method,node,context,call));
      else if(targetNode?.nodeType==='FunctionDefinition'&&context.source_memory){
        const argumentNodes=node.arguments||[],types=(targetNode.parameters?.parameters||[]).map(p=>sourceAbiType(p));
        const value=context.source_memory.capture({originExpressionId:expression(method,node,context),encoding:'compiler_external_call',selector:call.selector,selectorPresent:true,
          types,values:argumentNodes.map((n,i)=>sourceAbiValue(method,n,types[i],context)),argumentExpressionIds:call.input_expression_ids,argumentTexts:args,argumentNodes,
          context:{path:context.path,conditions:context.conditions,order:method.step_ids.length-1},evidence_ids:call.evidence_ids});
        sourceCallBinding(method,node,context,call,value);
      }
      let fund;const fundCandidates=[];
      const decoded=rawAddressCall&&name==='call'&&kind==='CALL'?decodeEncodedTransfer(node.arguments?.[0],context):null;
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
      }
      if(decoded) {
        const nativeFund=fund;
        call.selector=decoded.selector;call.signature=decoded.signature;call.calldata_basis='verified_source_abi_'+decoded.encoding;
        const argumentIDs=decoded.argument_expression_ids||decoded.args.map(n=>expression(method,n,context));
        call.input_arguments=decoded.args.map(sourceText);call.input_expression_ids=argumentIDs;
        call.source_memory_version_id=decoded.source_memory_version_id||null;call.encoding_condition_groups=decoded.condition_groups||[];call.assumptions=decoded.assumptions||[];
        call.evidence_ids=unique([...call.evidence_ids,getEvidence(method,decoded.node)]);
        fund={kind:decoded.name==='transferFrom'?'token_transfer_from_intent':decoded.name==='approve'?'token_approval':'token_transfer_intent',
          asset:target_expression,asset_expression_id:expression(method,target,context),from_expression:decoded.name==='transferFrom'?sourceText(decoded.args[0]):'address(this)',
          from_expression_id:decoded.name==='transferFrom'?argumentIDs[0]:null,to_expression:sourceText(decoded.args[decoded.name==='transferFrom'?1:0]),
          to_expression_id:argumentIDs[decoded.name==='transferFrom'?1:0],amount_expression:sourceText(decoded.args.at(-1)),amount_expression_id:argumentIDs.at(-1),
          source_memory_version_id:decoded.source_memory_version_id||null,call_input_binding_id:call.call_input_binding_id||null,encoding_condition_groups:decoded.condition_groups||[],assumptions:decoded.assumptions||[],
          unit:'asset_quantity_or_token_id',unit_basis:'verified_source_encoding_with_unverified_asset_standard',asset_standard_verified:false,settlement:'requires_execution_evidence',protocol:'encoded_transfer_interface'};
        if(nativeFund)fundCandidates.push({fund:nativeFund,suffix:'fund'});
        fundCandidates.push({fund,suffix:nativeFund?'encoded-fund':'fund'});fund=null;
      }
      if(fund)fundCandidates.push({fund,suffix:'fund'});
      for(const {fund,suffix}of fundCandidates){
        if(call.call_input_binding_id){fund.call_input_binding_id=call.call_input_binding_id;fund.source_memory_version_ids=call.source_memory_version_ids;}
        const fund_id = id(call_id, suffix);
        result.sections.funds.push({ fund_id, method_id: method.method_id, call_id, conditions: [...context.conditions],
          parameter_bindings: context.parameter_bindings || {}, evidence_ids: call.evidence_ids, ...fund });
        method.fund_ids.push(fund_id);
      }
    }
  };
  const bindNamespaceAlias=(method,context,declaration,value)=>{
    if(!namespacePointerType(declaration,namespaceRegistry))return;
    const path=namespacePath(value,context);
    // Dynamic keys are evaluated when a pointer is assigned. The formal state
    // model captures that value; source projections do not reconstruct it later.
    context.namespace_aliases?.set(declaration.id,path&&!path.keys.length?value:null);
    if(path?.keys.length)method.completeness.unresolved.push({code:'NAMESPACE_POINTER_KEY_CAPTURE_REQUIRES_STATE_MODEL',ast_id:declaration.id});
  };


  const tupleStoreOrderKnown=()=>!source.stdJsonInput.settings?.viaIR&&['0.4.26','0.5.8','0.6.12','0.7.6','0.8.30'].includes(source.compilation.compilerVersion.split('+')[0]);
  const unwrapAssignmentGroup=target=>{
    while(target?.nodeType==='TupleExpression'&&target.components?.length===1&&target.components[0]&&!typeOf(target).startsWith('tuple('))target=target.components[0];
    if(target?.nodeType==='TupleExpression')return{...target,components:target.components.map(unwrapAssignmentGroup)};return target;
  };
  const tupleCommitEntries=targets=>{const entries=[...targets.entries()];return tupleStoreOrderKnown()?entries.reverse():entries;};
  const writeByteTupleElements=(method,node,context)=>{
    const memory=context.source_memory,rhs=node.rightHandSide,targets=node.leftHandSide.components||[];
    if(!memory||node.operator!=='='||rhs?.nodeType!=='TupleExpression'||fieldWriteHasEffects(method,rhs))return false;
    const prepared=[],seen=new Set();
    for(const[index,target]of targets.entries()){
      if(!target)continue;
      if(target.nodeType==='Identifier'&&scalarDescriptor(nodes.get(target.referencedDeclaration))&&!nodes.get(target.referencedDeclaration)?.stateVariable)continue;
      if(target.nodeType!=='IndexAccess'||target.baseExpression?.nodeType!=='Identifier'||!sourceBytesType(target.baseExpression)||!target.indexExpression||target.indexExpression.nodeType!=='Literal'||target.indexExpression.kind!=='number')return false;
      const value=memory.current(resolveSourceBytes(target.baseExpression,context)),objects=unique(value.cases.map(c=>c.source_memory_object_id).filter(Boolean));
      if(value.unresolved.length||objects.length!==1||!value.cases.length)return false;
      let at,byte;try{at=BigInt(numericLiteral(String(target.indexExpression.value),target.indexExpression.subdenomination).value);byte=sourceAbiValue(method,rhs.components?.[index],'bytes1',context);}catch{return false;}
      if(!/^0x[\da-f]{2}$/i.test(byte)||value.cases.some(c=>{const r=memory.entry(c.source_memory_version_id)?.record;return!r?.allocator_known||r.byte_length===null||at<0n||at>=BigInt(r.byte_length);}))return false;
      const key=objects[0]+':'+at;if(seen.has(key))return false;seen.add(key);prepared.push({value,index:at,byteValue:BigInt(byte),component:index});
    }
    if(!prepared.length)return false;
    for(const item of prepared)memory.invalidate(item.value,'SOURCE_BYTES_TUPLE_INDEX_WRITE',{index:item.index,byteValue:item.byteValue,context:{path:context.path,ast_id:node.id,tuple_component:item.component,all_values_captured_before_writes:true},evidence_ids:[getEvidence(method,node)]});
    return true;
  };
  const finishTupleBindingSafety=(method,node,context,{byteElementsWritten=false}={})=>{
    const lhs=node.leftHandSide,counts=new Map(),wrapped=new Set(),ctx=aggregateContext(method,node,context),singleGroup=(lhs.components||[]).length===1&&!typeOf(lhs).startsWith('tuple(');
    const collect=(target,grouped=false)=>{
      if(!target)return;
      if(target.nodeType==='TupleExpression'){for(const child of target.components||[])collect(child,true);return;}
      if(target.nodeType!=='Identifier')return;
      const declaration=nodes.get(target.referencedDeclaration);if(declaration?.nodeType!=='VariableDeclaration'||declaration.stateVariable)return;
      counts.set(declaration.id,(counts.get(declaration.id)||0)+1);if(grouped)wrapped.add(declaration.id);
    };
    for(const target of lhs.components||[])collect(target,singleGroup);
    for(const[key,count]of counts){
      if(count<2&&!wrapped.has(key)||count>=2&&tupleStoreOrderKnown()&&!wrapped.has(key))continue;
      const declaration=nodes.get(key),reason=count>1?'SOURCE_TUPLE_OVERLAPPING_LOCAL_BINDINGS':'SOURCE_TUPLE_WRAPPED_LVALUE_UNMODELED';
      bindScalar(method,context,declaration,null,{kind:'unresolved_write',unresolved:[reason]});
      const aggregate=aggregateDescriptor(declaration);if(aggregate&&context.source_aggregates)context.source_aggregates.bind(aggregate,context.source_aggregates.unknown(aggregate.aggregate_type,reason,ctx),{context:ctx,kind:'unresolved_tuple_binding'});
      if(sourceBytesType(declaration))context.source_memory?.bind(key,unknownBytes(reason));
      if(pointerType(declaration))context.storage_references?.bind(key,{cases:[],unresolved:[reason]});
      if(context.namespace_aliases?.has(key))context.namespace_aliases.set(key,null);
      method.completeness.unresolved.push({code:reason,ast_id:node.id,declaration_id:key});
      result.sections.frontier.push({frontier_id:id(method.method_id,node.id,key,context.path,reason),method_id:method.method_id,reason,status:'partial',path:context.path,evidence_ids:ctx.evidence_ids||[]});
    }
    let byteWrite=false;visit(lhs,target=>{if(target.nodeType==='IndexAccess'&&sourceBytesType(target.baseExpression)&&/\bmemory\b/.test(typeOf(target.baseExpression)))byteWrite=true;});
    if(byteWrite&&!byteElementsWritten&&context.source_memory){
      const reason='SOURCE_BYTES_TUPLE_INDEX_WRITE_UNMODELED',memory=context.source_memory,cases=[...memory.bindings.values()].flatMap(value=>value?.cases||[]);
      memory.invalidate({cases,unresolved:[]},reason,{context:{path:context.path,ast_id:node.id},evidence_ids:ctx.evidence_ids||[]});
      method.completeness.unresolved.push({code:reason,ast_id:node.id});
      result.sections.frontier.push({frontier_id:id(method.method_id,node.id,context.path,reason),method_id:method.method_id,reason,status:'partial',path:context.path,evidence_ids:ctx.evidence_ids||[]});
    }
  };


  const byteReferenceReadOnly=node=>{
    if(node?.nodeType==='Identifier'&&sourceBytesType(node))return true;
    if(node?.nodeType!=='FunctionCall'||node.kind==='typeConversion'||node.arguments?.length!==1||node.arguments[0]?.nodeType!=='Identifier'||!sourceBytesType(node.arguments[0]))return false;
    const callee=node.expression?.nodeType==='FunctionCallOptions'?node.expression.expression:node.expression,target=nodes.get(callee?.referencedDeclaration),internal=internalArguments(target,callee,node.arguments);
    if(!internal.internal||target?.modifiers?.length||target?.parameters?.parameters?.length!==1||target?.returnParameters?.parameters?.length!==1||!sourceBytesType(target.returnParameters.parameters[0]))return false;
    const body=target.body?.statements;if(body?.length!==1||body[0].nodeType!=='Return')return false;
    return body[0].expression?.nodeType==='Identifier'&&body[0].expression.referencedDeclaration===target.parameters.parameters[0].id;
  };

  // Stack/environment-only Yul cannot overwrite an already captured bytes object.
  // Unknown instructions, memory accesses, non-scalar external references and
  // terminating operations keep the existing conservative effect frontier.
  const byteStackAssemblyOps=new Set('add sub mul div sdiv mod smod addmod mulmod exp signextend lt gt slt sgt eq iszero and or xor not byte shl shr sar address balance origin caller callvalue calldataload calldatasize codesize gasprice extcodesize extcodehash returndatasize blockhash coinbase timestamp number prevrandao difficulty gaslimit chainid selfbalance basefee blobhash blobbasefee gas pc pop sload tload'.split(' '));
  const byteStackAssemblyNodes=new Set(['YulBlock','YulAssignment','YulIdentifier','YulLiteral','YulVariableDeclaration','YulTypedName','YulExpressionStatement','YulIf','YulSwitch','YulCase','YulForLoop','YulBreak','YulContinue','YulFunctionCall']);
  const byteStackOnlyAssembly=node=>{
    if(!node.AST)return false;
    for(const reference of Object.values(node.externalReferences||{})){
      const declaration=nodes.get(reference.declaration),type=declaration&&sourceAbiType(declaration);
      if(!declaration||!/^(?:u?int\d*|address|bool|bytes(?:[1-9]|[12]\d|3[0-2]))$/.test(type||''))return false;
    }
    let safe=true,count=0;visit(node.AST,item=>{
      if(!safe)return;if(++count>8192||!byteStackAssemblyNodes.has(item.nodeType)){safe=false;return;}
      if(item.nodeType==='YulFunctionCall'&&!byteStackAssemblyOps.has(item.functionName?.name))safe=false;
    });return safe;
  };
  const byteGlobalEffects=new Map();
  const byteGlobalMemoryEffect=(target,stack=[])=>{
    if(!aggregateGlobalMemoryEffect(target,stack))return false;
    if(!target?.body||target.virtual||target.baseFunctions?.length||stack.includes(target.id)||stack.length>=12)return true;
    if(byteGlobalEffects.has(target.id))return byteGlobalEffects.get(target.id);
    const bodies=[target.body,...(target.modifiers||[]).map(m=>nodes.get(m.modifierName?.referencedDeclaration)?.body)];if(bodies.some(body=>!body))return true;
    let unsafe=false,count=0;for(const body of bodies)visit(body,n=>{
      if(unsafe||++count>8192){unsafe=true;return;}if(n.nodeType==='InlineAssembly'){if(!byteStackOnlyAssembly(n))unsafe=true;return;}
      if(n.nodeType==='FunctionCall'&&!['typeConversion','structConstructorCall'].includes(n.kind)){
        const callee=n.expression?.nodeType==='FunctionCallOptions'?n.expression.expression:n.expression,called=nodes.get(callee?.referencedDeclaration),internal=internalArguments(called,callee,n.arguments||[]).internal;
        if(internal&&byteGlobalMemoryEffect(called,[...stack,target.id])||called?.nodeType!=='FunctionDefinition'&&/^t_function_internal_/.test(callee?.typeDescriptions?.typeIdentifier||''))unsafe=true;
      }
    });byteGlobalEffects.set(target.id,unsafe);return unsafe;
  };
  const byteEffectMemo=new Map(),byteReadonlyCalls=new Map();
  const byteEffectSummary=(method,node,depth=0)=>{
    if(!node)return{read:false,write:false};
    if(byteEffectMemo.has(node.id))return byteEffectMemo.get(node.id);
    if(depth>64||byteEffectMemo.size>=50000)return{read:true,write:true,referenceWrite:true,budget:true};
    const flags={read:sourceBytesType(node)||node.nodeType==='Literal'&&['string','hexString'].includes(node.kind),write:false,referenceWrite:false,budget:false};
    const lhs=node.nodeType==='Assignment'?node.leftHandSide:node.nodeType==='UnaryOperation'&&['++','--','delete'].includes(node.operator)?node.subExpression:null;
    if(lhs)visit(lhs,target=>{if(sourceBytesType(target)||target.nodeType==='IndexAccess'&&sourceBytesType(target.baseExpression))flags.write=true;if(target.nodeType==='Identifier'&&sourceBytesType(target)&&lhs.nodeType!=='IndexAccess')flags.referenceWrite=true;});
    if(node.nodeType==='FunctionCall'&&!['typeConversion','structConstructorCall'].includes(node.kind)){
      const callee=node.expression?.nodeType==='FunctionCallOptions'?node.expression.expression:node.expression,target=nodes.get(callee?.referencedDeclaration),call=internalArguments(target,callee,node.arguments||[]);
      if(call.internal){
        if(byteGlobalMemoryEffect(target)){flags.write=true;flags.referenceWrite=true;}
        const hasBytes=(call.args||node.arguments||[]).some(arg=>sourceBytesType(arg));
        if(hasBytes){if(!byteReadonlyCalls.has(target?.id))byteReadonlyCalls.set(target?.id,aggregateReturnAPI(method).readonly(target));if(!byteReadonlyCalls.get(target?.id))flags.write=true;}
      }else if(/^t_function_internal_/.test(callee?.typeDescriptions?.typeIdentifier||''))flags.write=true;
    }
    for(const child of children(node)){const nested=byteEffectSummary(method,child,depth+1);flags.read||=nested.read;flags.write||=nested.write;flags.referenceWrite||=nested.referenceWrite;flags.budget||=nested.budget;}
    byteEffectMemo.set(node.id,flags);return flags;
  };
  const byteSiblingEffects=(method,node,context)=>{
    const memory=context.source_memory;if(!memory)return null;
    const list=node.nodeType==='TupleExpression'?(node.components||[]).filter(Boolean):node.nodeType==='FunctionCall'?[node.expression,...(node.arguments||[])].filter(Boolean):node.nodeType==='BinaryOperation'&&!['&&','||'].includes(node.operator)?[node.leftExpression,node.rightExpression]:[];
    if(list.length<2)return null;const summaries=list.map(child=>byteEffectSummary(method,child)),referenceOnly=list.map(byteReferenceReadOnly);let unsafe=false;
    for(let i=0;i<summaries.length;i++)for(let j=i+1;j<summaries.length;j++)if(summaries[i].write&&summaries[j].read&&(!referenceOnly[j]||summaries[i].referenceWrite)||summaries[j].write&&summaries[i].read&&(!referenceOnly[i]||summaries[j].referenceWrite))unsafe=true;
    if(!unsafe)return null;
    const reason=summaries.some(row=>row.budget)?'SOURCE_BYTES_EFFECT_SUMMARY_BUDGET':'SOURCE_BYTES_EVALUATION_ORDER_UNSPECIFIED';
    memory.shared.evaluationGaps||=new Map();const pending=[node];let count=0;
    while(pending.length&&count++<4096&&memory.shared.evaluationGaps.size<4096){const current=pending.pop();memory.shared.evaluationGaps.set(expressionIdentity(method,current,context),reason);pending.push(...children(current));}
    if(pending.length)memory.shared.globalEvaluationGap='SOURCE_BYTES_EVALUATION_GAP_BUDGET';
    method.completeness.unresolved.push({code:reason,ast_id:node.id});
    result.sections.frontier.push({frontier_id:id(method.method_id,node.id,context.path,reason),method_id:method.method_id,reason,status:'partial',path:context.path,evidence_ids:[getEvidence(method,node)]});
    return reason;
  };
  const finishByteSiblings=(method,node,context,reason)=>{
    if(!reason)return;const memory=context.source_memory,cases=[...memory.bindings.values()].flatMap(value=>value?.cases||[]);
    memory.invalidate({cases,unresolved:[]},reason,{context:{path:context.path,ast_id:node.id},evidence_ids:[getEvidence(method,node)]});
  };

  const sourceBytePredicate=(key,expected,context)=>{
    const seen=new Set();let current=key,wanted=expected;
    while(current&&!seen.has(current)&&seen.size<64){seen.add(current);const row=expressionByID.get(current);if(!row)break;
      if(row.binding_expression_id){current=row.binding_expression_id;continue;}
      if(row.symbolic_format==='unary'&&row.operator==='!'&&row.operands?.length===1){wanted=!wanted;current=row.operands[0];continue;}
      if(row.symbolic_format==='identity'&&row.operands?.length===1){current=row.operands[0];continue;}
      if(row.symbolic_format==='literal'&&['true','false',true,false].includes(row.value)){const value=row.value===true||row.value==='true';return{op:'constant',value:value===wanted};}
      break;
    }
    return{op:'atom',expression_id:current||key,expected:wanted};
  };
  const assumeByteBranch=(memory,key,expected,context)=>memory?.assume(sourceBytePredicate(key,expected,context));
  const scanExpression = (method, node, context, access = 'read') => {
    if (!node) return;
    checkMethodTime();
    const byteEffects=byteSiblingEffects(method,node,context);
    const aggregateEffects=aggregateSiblingEffects(method,node,context);
    if(node.nodeType==='Conditional'&&context.source_memory){
      scanExpression(method,node.condition,context);const condition=sourceText(node.condition),conditionExpression=expression(method,node.condition,context),yes={...context,source_scalars:context.source_scalars?.fork(),source_aggregates:context.source_aggregates?.fork(),source_memory:context.source_memory.fork(),storage_references:context.storage_references?.fork(),namespace_aliases:new Map(context.namespace_aliases||[]),
        path:context.path+'/ternary:'+node.id+':true',conditions:[...context.conditions,condition]},no={...context,source_scalars:context.source_scalars?.fork(),source_aggregates:context.source_aggregates?.fork(),source_memory:context.source_memory.fork(),storage_references:context.storage_references?.fork(),namespace_aliases:new Map(context.namespace_aliases||[]),
        path:context.path+'/ternary:'+node.id+':false',conditions:[...context.conditions,'!('+condition+')']};
      assumeByteBranch(yes.source_memory,conditionExpression,true,yes);assumeByteBranch(no.source_memory,conditionExpression,false,no);
      scanExpression(method,node.trueExpression,yes,access);scanExpression(method,node.falseExpression,no,access);
      context.source_scalars?.shared.expressionOperands.set(expressionIdentity(method,node,context),{operands:[conditionExpression,expression(method,node.trueExpression,yes),expression(method,node.falseExpression,no)]});
      mergeScalars(method,node,context,yes,no,conditionExpression);
      if(sourceBytesType(node)){const a=resolveSourceBytes(node.trueExpression,yes)||unknownBytes('SOURCE_BYTES_CONDITIONAL_BRANCH_UNAVAILABLE'),b=resolveSourceBytes(node.falseExpression,no)||unknownBytes('SOURCE_BYTES_CONDITIONAL_BRANCH_UNAVAILABLE');
        context.source_memory.shared.expressionBindings.set(expression(method,node,context),{cases:[...a.cases.map(c=>({...c,conditions:unique([...yes.conditions,...c.conditions])})),
          ...b.cases.map(c=>({...c,conditions:unique([...no.conditions,...c.conditions])}))],unresolved:unique([...a.unresolved,...b.unresolved])});}
      if(context.storage_references&&aggregateStorageType(storageTypes.forNode(node))){
        const a=resolveStorageReference(method,node.trueExpression,yes),b=resolveStorageReference(method,node.falseExpression,no);
        context.storage_references.shared.expressionBindings.set(expression(method,node,context),{cases:[...a.cases,...b.cases],unresolved:unique([...a.unresolved,...b.unresolved])});
      }
      context.source_memory.merge(yes.source_memory,no.source_memory,{yesConditions:yes.conditions,noConditions:no.conditions,context:{path:context.path,ast_id:node.id}});
      context.storage_references?.merge(yes.storage_references,no.storage_references,{yesConditions:yes.conditions,noConditions:no.conditions,context:{path:context.path,ast_id:node.id}});
      for(const key of context.namespace_aliases?.keys()||[]){const a=yes.namespace_aliases.get(key),b=no.namespace_aliases.get(key);context.namespace_aliases.set(key,a?.id===b?.id?a:null);}return;
    }
    if(node.nodeType==='BinaryOperation'&&['&&','||'].includes(node.operator)&&context.source_memory){
      scanExpression(method,node.leftExpression,context);const leftExpression=expression(method,node.leftExpression,context),condition=node.operator==='&&'?sourceText(node.leftExpression):'!('+sourceText(node.leftExpression)+')',
        conditionExpression=node.operator==='&&'?leftExpression:synthetic(method,node.leftExpression,'unary',[leftExpression],context,{operator:'!',type:'bool'}),
        evaluated={...context,source_scalars:context.source_scalars?.fork(),source_aggregates:context.source_aggregates?.fork(),source_memory:context.source_memory.fork(),storage_references:context.storage_references?.fork(),namespace_aliases:new Map(context.namespace_aliases||[]),path:context.path+'/short-circuit:'+node.id,conditions:[...context.conditions,condition]},
        skipped=context.source_memory.fork(),skippedScalars={source_scalars:context.source_scalars?.fork(),source_aggregates:context.source_aggregates?.fork()};assumeByteBranch(evaluated.source_memory,conditionExpression,true,evaluated);assumeByteBranch(skipped,conditionExpression,false,context);scanExpression(method,node.rightExpression,evaluated);
      context.source_scalars?.shared.expressionOperands.set(expressionIdentity(method,node,context),{operands:[leftExpression,expression(method,node.rightExpression,evaluated)]});
      mergeScalars(method,node,context,evaluated,skippedScalars,conditionExpression);
      context.source_memory.merge(evaluated.source_memory,skipped,{yesConditions:evaluated.conditions,noConditions:[...context.conditions,'!('+condition+')'],context:{path:context.path,ast_id:node.id}});
      context.storage_references?.merge(evaluated.storage_references,context.storage_references.fork(),{yesConditions:evaluated.conditions,noConditions:[...context.conditions,'!('+condition+')'],context:{path:context.path,ast_id:node.id}});
      for(const key of context.namespace_aliases?.keys()||[])if(evaluated.namespace_aliases.get(key)?.id!==context.namespace_aliases.get(key)?.id)context.namespace_aliases.set(key,null);return;
    }
    if (node.nodeType === 'Assignment') {
      node={...node,leftHandSide:unwrapAssignmentGroup(node.leftHandSide),rightHandSide:unwrapAssignmentGroup(node.rightHandSide)};
      scanExpression(method, node.leftHandSide, context, node.operator === '=' ? 'write' : 'readwrite');
      stateAccess(method, node.leftHandSide, 'write', context, node.rightHandSide, node.operator);
      scanExpression(method, node.rightHandSide, context);
      const assigned = addStep(method, node, 'assignment', `执行 ${sourceText(node)}`, context);
      const tupleComponents=node.leftHandSide.nodeType==='TupleExpression'?(node.leftHandSide.components||[]):[],
        tupleByteSnapshots=tupleComponents.map((target,index)=>{const declaration=target?.nodeType==='Identifier'?nodes.get(target.referencedDeclaration):null;return declaration&&!declaration.stateVariable&&sourceBytesType(declaration)&&context.source_memory?{declaration,value:structuredClone(sourceBytesComponent(method,node.rightHandSide,index,context))}:null;}).filter(Boolean),
        tupleStorageSnapshots=tupleComponents.map((target,index)=>{const declaration=target?.nodeType==='Identifier'?nodes.get(target.referencedDeclaration):null;return declaration&&pointerType(declaration)&&context.storage_references?{declaration,value:structuredClone(resolveStorageReference(method,node.rightHandSide,context,index))}:null;}).filter(Boolean);

      const aggregateTupleBindings=node.leftHandSide.nodeType==='TupleExpression'?(node.leftHandSide.components||[]).map((p,i)=>p?.nodeType==='Identifier'&&aggregateDescriptor(nodes.get(p.referencedDeclaration))?{declaration:nodes.get(p.referencedDeclaration),reference:resolveAggregate(method,node.rightHandSide,context,i,aggregateType(nodes.get(p.referencedDeclaration)))}:null).filter(Boolean):[];
      const tupleFieldsWritten=node.leftHandSide.nodeType==='TupleExpression'&&writeAggregateTupleFields(method,node,context),byteElementsWritten=node.leftHandSide.nodeType==='TupleExpression'&&writeByteTupleElements(method,node,context);
      if(node.leftHandSide.nodeType==='Identifier'&&aggregateDescriptor(nodes.get(node.leftHandSide.referencedDeclaration)))bindAggregate(method,context,nodes.get(node.leftHandSide.referencedDeclaration),node.operator==='='?node.rightHandSide:null);
      if(node.leftHandSide.nodeType==='MemberAccess'&&aggregateType(node.leftHandSide.expression)&&context.source_aggregates)writeAggregateField(method,node,context);
      if (node.leftHandSide.nodeType === 'Identifier' && !nodes.get(node.leftHandSide.referencedDeclaration)?.stateVariable) {
        result.sections.definitions.push({ definition_id: id(assigned.step_id, 'definition'), method_id: method.method_id,
          declaration_id: node.leftHandSide.referencedDeclaration, name: node.leftHandSide.name,
          value_expression_id: expression(method, node.rightHandSide, context), operation: node.operator,
          order: assigned.order, conditions: [...context.conditions], path: context.path, evidence_ids: assigned.evidence_ids });
        const scalar=bindScalar(method,context,nodes.get(node.leftHandSide.referencedDeclaration),node.operator==='='?node.rightHandSide:null,{
          definition_id:id(assigned.step_id,'definition'),unresolved:node.operator==='='?[]:['SOURCE_SCALAR_COMPOUND_ASSIGNMENT_UNMODELED']});
        if(scalar)result.sections.definitions.at(-1).source_scalar_version_id=scalar;
        bindNamespaceAlias(method,context,nodes.get(node.leftHandSide.referencedDeclaration),node.operator==='='?node.rightHandSide:null);
        bindSourceBytes(method,context,nodes.get(node.leftHandSide.referencedDeclaration),node.operator==='='?node.rightHandSide:null);
        bindStorageReference(method,context,nodes.get(node.leftHandSide.referencedDeclaration),node.operator==='='?node.rightHandSide:null);
      } else if(node.leftHandSide.nodeType==='TupleExpression'&&node.operator==='=') {
        const rhs=expression(method,node.rightHandSide,context);
        for(const [i,p]of (node.leftHandSide.components||[]).entries())if(p?.nodeType==='Identifier'&&!nodes.get(p.referencedDeclaration)?.stateVariable) {
          result.sections.definitions.push({definition_id:id(assigned.step_id,'tuple-definition',i),method_id:method.method_id,declaration_id:p.referencedDeclaration,name:p.name,
            value_expression_id:projectTupleValue(method,node.rightHandSide,rhs,i,context,typeOf(p)),operation:'=',
            order:assigned.order,tuple_component:i,tuple_store_order:tupleStoreOrderKnown()?'right_to_left_known_compiler_lowering':'unverified',conditions:[...context.conditions],path:context.path,evidence_ids:assigned.evidence_ids});}
        for(const binding of (tupleStoreOrderKnown()?[...tupleByteSnapshots].reverse():tupleByteSnapshots))context.source_memory.bind(binding.declaration.id,binding.value);
        for(const binding of (tupleStoreOrderKnown()?[...tupleStorageSnapshots].reverse():tupleStorageSnapshots))context.storage_references.bind(binding.declaration.id,binding.value);
        for(const[i,p]of tupleCommitEntries(node.leftHandSide.components||[]))if(p?.nodeType==='Identifier'){
          const scalar=bindScalar(method,context,nodes.get(p.referencedDeclaration),node.rightHandSide,{component:i,definition_id:id(assigned.step_id,'tuple-definition',i)});
          if(scalar)result.sections.definitions.find(d=>d.definition_id===id(assigned.step_id,'tuple-definition',i)).source_scalar_version_id=scalar;
        }
      }
      for(const binding of (tupleStoreOrderKnown()?[...aggregateTupleBindings].reverse():aggregateTupleBindings))context.source_aggregates.bind(aggregateDescriptor(binding.declaration),binding.reference,{context:aggregateContext(method,node,context),kind:'tuple_assignment'});
      if(node.leftHandSide.nodeType==='TupleExpression')finishTupleBindingSafety(method,node,context,{byteElementsWritten});
      if(node.leftHandSide.nodeType==='TupleExpression'&&context.source_aggregates){
        let memoryFieldWrite=false;visit(node.leftHandSide,target=>{if(target.nodeType==='MemberAccess'&&aggregateType(target.expression))memoryFieldWrite=true;});
        if(memoryFieldWrite&&!tupleFieldsWritten){const reason='SOURCE_AGGREGATE_TUPLE_FIELD_WRITE_UNMODELED',ctx=aggregateContext(method,node,context);context.source_aggregates.invalidateAll(reason,ctx);context.source_aggregates.unknown(null,reason,ctx);}
      }
      if(node.leftHandSide.nodeType==='IndexAccess'&&sourceBytesType(node.leftHandSide.baseExpression)&&context.source_memory){
        const value=resolveSourceBytes(node.leftHandSide.baseExpression,context),index=node.leftHandSide.indexExpression;
        let knownIndex=null,byteValue=null;try{if(index?.nodeType==='Literal'&&index.kind==='number')knownIndex=BigInt(numericLiteral(String(index.value),index.subdenomination).value);
          const rhs=sourceAbiValue(method,node.rightHandSide,'bytes1',context);if(node.operator==='='&&typeof rhs==='string'&&/^0x[\da-f]{2}$/i.test(rhs))byteValue=BigInt(rhs);}catch{}
        context.source_memory.invalidate(value,'SOURCE_BYTES_INDEX_WRITE',{index:knownIndex,byteValue,context:{path:context.path,ast_id:node.id},evidence_ids:assigned.evidence_ids});
      }
      return;
    }
    if (node.nodeType === 'UnaryOperation' && ['++', '--', 'delete'].includes(node.operator)) {
      scanExpression(method,node.subExpression,context,'write');
      if(node.subExpression.nodeType==='MemberAccess'&&aggregateType(node.subExpression.expression))context.source_aggregates?.invalidate(resolveAggregate(method,node.subExpression.expression,context),'SOURCE_AGGREGATE_FIELD_WRITE_UNMODELED',{context:aggregateContext(method,node,context)});
      if(node.subExpression.nodeType==='Identifier'&&aggregateDescriptor(nodes.get(node.subExpression.referencedDeclaration))){context.source_aggregates?.invalidate(resolveAggregate(method,node.subExpression,context),'SOURCE_AGGREGATE_OBJECT_WRITE_UNMODELED',{context:aggregateContext(method,node,context)});bindAggregate(method,context,nodes.get(node.subExpression.referencedDeclaration),null,'unresolved_write');}
      stateAccess(method, node.subExpression, 'write', context, node, node.operator);
      if (node.operator !== 'delete') stateAccess(method, node.subExpression, 'read', context);
      const step=addStep(method, node, 'assignment', `执行 ${sourceText(node)}`, context);
      if(node.subExpression.nodeType==='Identifier'){
        const declaration=nodes.get(node.subExpression.referencedDeclaration);
        if(scalarDescriptor(declaration))bindScalar(method,context,declaration,null,{kind:node.operator==='delete'?'delete':'unresolved_write',
          valueExpression:node.operator==='delete'?scalarZero(method,declaration,context,node):null,
          unresolved:node.operator==='delete'?[]:['SOURCE_SCALAR_INCREMENT_REQUIRES_ORDERED_VALUE_MODEL']});
      }
      if(node.operator==='delete'&&context.source_memory){
        if(node.subExpression.nodeType==='Identifier')bindSourceBytes(method,context,nodes.get(node.subExpression.referencedDeclaration),null);
        else if(node.subExpression.nodeType==='IndexAccess'&&sourceBytesType(node.subExpression.baseExpression))context.source_memory.invalidate(resolveSourceBytes(node.subExpression.baseExpression,context),
          'SOURCE_BYTES_INDEX_DELETE',{byteValue:0n,context:{ast_id:node.id,path:context.path},evidence_ids:step.evidence_ids});
      }return;
    }
    if (['Identifier', 'IndexAccess', 'MemberAccess'].includes(node.nodeType)) {
      if (access !== 'write') stateAccess(method, node, 'read', context);
    }
    for (const child of children(node)) scanExpression(method,child,context,node.nodeType==='IndexAccess'&&child===node.indexExpression?'read':access);
    if(node.nodeType==='MemberAccess'&&node.expression?.nodeType==='FunctionCall'&&access!=='write')stateAccess(method,node,'read',context);
    if (node.nodeType === 'FunctionCall') {
      finishByteSiblings(method,node,context,byteEffects);
      const callee=node.expression?.nodeType==='FunctionCallOptions'?node.expression.expression:node.expression;
      if(['call','delegatecall','staticcall','callcode'].includes(callee?.memberName)||
        ['send','transfer'].includes(callee?.memberName)&&/^address(?: payable)?$/.test(typeOf(callee.expression)))expression(method,node,context);
      analyzeCall(method, node, context);
    }
    if(node.nodeType!=='FunctionCall')finishByteSiblings(method,node,context,byteEffects);
    finishAggregateSiblings(method,node,context,aggregateEffects);
  };
  const analyzeBlock = (method, node, context) => {
    if (!node) return;
    checkMethodTime();
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
      const conditionExpression=expression(method,node.condition,context);
      const yesContext={...context,source_scalars:context.source_scalars?.fork(),source_aggregates:context.source_aggregates?.fork(),source_memory:context.source_memory?.fork(),storage_references:context.storage_references?.fork(),namespace_aliases:new Map(context.namespace_aliases||[]),path:`${context.path}/if:${node.id}:true`,conditions:[...context.conditions,condition]},
        noContext={...context,source_scalars:context.source_scalars?.fork(),source_aggregates:context.source_aggregates?.fork(),source_memory:context.source_memory?.fork(),storage_references:context.storage_references?.fork(),namespace_aliases:new Map(context.namespace_aliases||[]),path:`${context.path}/if:${node.id}:false`,conditions:[...context.conditions,`!(${condition})`]};
      assumeByteBranch(yesContext.source_memory,conditionExpression,true,yesContext);assumeByteBranch(noContext.source_memory,conditionExpression,false,noContext);
      const yes=analyzeBlock(method,node.trueBody,yesContext),no=analyzeBlock(method,node.falseBody,noContext);
      mergeScalars(method,node,context,yesContext,noContext,conditionExpression,Boolean(yes),Boolean(no));
      context.source_memory?.merge(yesContext.source_memory,noContext.source_memory,{yesConditions:yesContext.conditions,noConditions:noContext.conditions,
        yesTerminated:Boolean(yes),noTerminated:Boolean(no),context:{ast_id:node.id,path:context.path}});
      context.storage_references?.merge(yesContext.storage_references,noContext.storage_references,{yesConditions:yesContext.conditions,noConditions:noContext.conditions,
        yesTerminated:Boolean(yes),noTerminated:Boolean(no),context:{ast_id:node.id,path:context.path}});
      for(const key of context.namespace_aliases?.keys()||[]){const a=yesContext.namespace_aliases.get(key),b=noContext.namespace_aliases.get(key);
        context.namespace_aliases.set(key,yes&&!no?b:no&&!yes?a:a?.id===b?.id?a:null);}
      if(yes&&no)return yes===no?yes:'terminated';
      if(yes)context.conditions.push(`!(${condition})`);
      if(no)context.conditions.push(condition);
      return;
    }
    if (['ForStatement', 'WhileStatement', 'DoWhileStatement'].includes(node.nodeType)) {
      const condition = sourceText(node.condition);
      addStep(method, node, 'loop', `循环条件 ${condition || '无显式条件'}`, context);
      // This view represents an arbitrary iteration, not an unrolling of its
      // first execution. An initializer must not replace every later counter
      // read with its initial constant. Loop summaries have their own model.
      scanExpression(method,node.initializationExpression,context);
      if(node.initializationExpression?.nodeType==='VariableDeclarationStatement')for(const p of node.initializationExpression.declarations||[])if(p)
        bindSourceBytes(method,context,p,node.initializationExpression.initialValue);
      if(node.initializationExpression?.nodeType==='VariableDeclarationStatement')for(const p of node.initializationExpression.declarations||[])if(p)
        bindStorageReference(method,context,p,node.initializationExpression.initialValue);
      if(node.initializationExpression?.nodeType==='VariableDeclarationStatement')for(const p of node.initializationExpression.declarations||[])if(p&&scalarDescriptor(p))
        bindScalar(method,context,p,node.initializationExpression.initialValue,{kind:'loop_initialization',valueExpression:node.initializationExpression.initialValue?undefined:scalarZero(method,p,context)});
      const scalarLoopWrites=unique([...scalarWrites(node.body),...scalarWrites(node.loopExpression),...scalarWrites(node.condition)].map(d=>d.id)).map(key=>scalarDescriptor(nodes.get(key)));
      context.source_aggregates?.invalidateAll('SOURCE_AGGREGATE_LOOP_REQUIRES_INVARIANT',aggregateContext(method,node,context));
      context.source_scalars?.invalidate(scalarLoopWrites,'SOURCE_SCALAR_LOOP_REQUIRES_INVARIANT',scalarEffectContext(method,context,node));
      scanExpression(method, node.condition, context);
      const loopContext={...context,source_scalars:context.source_scalars?.fork(),source_aggregates:context.source_aggregates?.fork(),source_memory:context.source_memory?.fork(),storage_references:context.storage_references?.fork(),namespace_aliases:new Map(context.namespace_aliases||[]),path:`${context.path}/loop:${node.id}`,conditions:[...context.conditions,`循环：${condition}`]};
      analyzeBlock(method,node.body,loopContext);
      scanExpression(method,node.loopExpression,loopContext);
      context.source_aggregates?.invalidateAll('SOURCE_AGGREGATE_LOOP_REQUIRES_INVARIANT',aggregateContext(method,node,context));
      context.source_scalars?.invalidate(scalarLoopWrites,'SOURCE_SCALAR_LOOP_REQUIRES_INVARIANT',scalarEffectContext(method,context,node));
      for(const[key,value]of context.namespace_aliases||[])if(loopContext.namespace_aliases.get(key)?.id!==value?.id)context.namespace_aliases.set(key,null);
      if(context.storage_references)for(const[key,value]of context.storage_references.bindings)if(hash(value)!==hash(loopContext.storage_references.get(key)))
        context.storage_references.bind(key,{cases:[...value.cases,...loopContext.storage_references.get(key).cases].slice(0,16),
          unresolved:unique([...value.unresolved,...loopContext.storage_references.get(key).unresolved,'STORAGE_REFERENCE_LOOP_REQUIRES_INVARIANT'])});
      if(context.source_memory){
        for(const [key,value]of context.source_memory.bindings)if(hash(value)!==hash(loopContext.source_memory.get(key)))context.source_memory.bind(key,{
          cases:[...value.cases,...loopContext.source_memory.get(key).cases],unresolved:unique([...value.unresolved,...loopContext.source_memory.get(key).unresolved,'SOURCE_BYTES_LOOP_REQUIRES_INVARIANT'])});
        context.source_memory.allocatorKnown&&=loopContext.source_memory.allocatorKnown;context.source_memory.allocatorInvalidated||=loopContext.source_memory.allocatorInvalidated;
      }
      return;
    }
    if (node.nodeType === 'Return') {
      scanExpression(method, node.expression, context);
      const parameters=nodes.get(context.function_frame)?.returnParameters?.parameters||[];
      if(node.expression)for(const[i,p]of parameters.entries())if(p.name)bindScalar(method,context,p,node.expression,{component:parameters.length>1?i:null,kind:'return_statement_assignment'});
      const step = addStep(method, node, 'return', `返回 ${sourceText(node.expression) || '已声明的返回变量'}`, context,
        {return_scope:context.function_frame===method.source_location?.ast_id?'entry':'internal',return_frame:context.function_frame});
      collectSourceReturn(method,nodes.get(context.function_frame),node.expression,context);
      collectStorageReturn(method,nodes.get(context.function_frame),node.expression,context);
      method.return_ids.push(step.step_id); return 'return';
    }
    if (node.nodeType === 'EmitStatement') { const callee=node.eventCall?.expression;eventStep(method,node,node.eventCall,context,nodes.get(callee?.referencedDeclaration)); return; }
    if (node.nodeType === 'RevertStatement') { const step = addStep(method, node, 'revert', `回滚 ${sourceText(node.errorCall)}`, context); method.failure_ids.push(step.step_id); return 'revert'; }
    if (node.nodeType === 'InlineAssembly' || node.nodeType === 'TryStatement') {
      const pointer=namespaceRegistry.accessors.get(context.function_frame);
      if(node.nodeType==='InlineAssembly'&&pointer?.assembly_ast_id===node.id){
        addStep(method,node,'storage_pointer_assignment',sourceText(node),context,{namespace_id:pointer.namespace_id,root_slot:pointer.root_slot,root_assignment_verified:true});return;
      }
      addStep(method, node, node.nodeType === 'InlineAssembly' ? 'assembly' : 'try', sourceText(node).slice(0, 2048), context);
      if(node.nodeType==='InlineAssembly'){analyzeAssemblyReturnCallsites(method,node,context);analyzeEncodedAssemblyCall(method,node,context);
        context.source_aggregates?.invalidateAll('SOURCE_AGGREGATE_ASSEMBLY_MEMORY_UNMODELED',aggregateContext(method,node,context));
        if(!sourceAssemblyPreservesMemory(node))context.source_memory?.invalidateAll('SOURCE_ASSEMBLY_MEMORY_UNRESOLVED',{ast_id:node.id,path:context.path});
        const refs=Array.isArray(node.externalReferences)?node.externalReferences:Object.values(node.externalReferences||{});
        if(refs.some(r=>r.isSlot||r.suffix==='slot'))context.storage_references?.invalidate('STORAGE_REFERENCE_ASSEMBLY_POINTER_EFFECT_UNRESOLVED');
        const detailed=Array.isArray(node.externalReferences)?node.externalReferences:Object.entries(node.externalReferences||{}).map(([src,r])=>({src,...r})),writes=new Set();
        if(node.AST)visit(node.AST,n=>{if(n.nodeType==='YulAssignment')for(const lhs of n.variableNames||[]){const ref=detailed.find(r=>r.src===lhs.src);if(ref&&!ref.isSlot&&!ref.isOffset)writes.add(ref.declaration);}});
        else for(const ref of detailed)if(!ref.isSlot&&!ref.isOffset)writes.add(ref.declaration);
        context.source_scalars?.invalidate([...writes].map(key=>scalarDescriptor(nodes.get(key))).filter(Boolean),'SOURCE_SCALAR_ASSEMBLY_WRITE_UNMODELED',scalarEffectContext(method,context,node));}
      method.completeness.unresolved.push({ code: node.nodeType === 'InlineAssembly' ? 'ASSEMBLY_SEMANTICS_UNRESOLVED' : 'TRY_CATCH_PATHS_UNRESOLVED', ast_id: node.id });
      if (node.nodeType === 'TryStatement') {
        context.source_aggregates?.invalidateAll('SOURCE_AGGREGATE_TRY_CATCH_REQUIRES_JOIN',aggregateContext(method,node,context));
        for (const child of children(node)) analyzeBlock(method,child,{...context,source_scalars:context.source_scalars?.fork(),source_aggregates:context.source_aggregates?.fork(),storage_references:context.storage_references?.fork(),namespace_aliases:new Map(context.namespace_aliases||[])});
        context.source_scalars?.invalidate(scalarWrites(node),'SOURCE_SCALAR_TRY_CATCH_REQUIRES_JOIN',scalarEffectContext(method,context,node));
        for(const key of context.namespace_aliases?.keys()||[])context.namespace_aliases.set(key,null);
        context.source_memory?.invalidateAll('SOURCE_BYTES_TRY_CATCH_EFFECTS_UNRESOLVED',{ast_id:node.id,path:context.path});
        context.storage_references?.invalidate('STORAGE_REFERENCE_TRY_CATCH_EFFECTS_UNRESOLVED');
      }
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
          value_expression_id:projectTupleValue(method,node.initialValue,whole,i,context,typeOf(p)),operation:'=',
          order:declared.order,conditions:[...context.conditions],path:context.path,evidence_ids:declared.evidence_ids});
      }
      if(node.declarations?.length===1&&node.declarations[0])bindNamespaceAlias(method,context,node.declarations[0],node.initialValue);
      for(const [i,p]of (node.declarations||[]).entries())if(p)bindSourceBytes(method,context,p,node.initialValue,node.declarations.length===1?null:i);
      for(const[i,p]of(node.declarations||[]).entries())if(p)bindAggregate(method,context,p,node.initialValue,'declaration',node.declarations.length===1?null:i);
      for(const[i,p]of(node.declarations||[]).entries())if(p)bindStorageReference(method,context,p,node.initialValue,node.declarations.length===1?null:i);
      for(const[i,p]of(node.declarations||[]).entries())if(p&&scalarDescriptor(p)){
        const definition_id=node.declarations.length===1?id(declared.step_id,'definition'):id(declared.step_id,'tuple-definition',i);
        const scalar=bindScalar(method,context,p,node.initialValue,{component:node.initialValue&&node.declarations.length>1?i:null,
          valueExpression:node.initialValue?undefined:scalarZero(method,p,context),definition_id:node.initialValue?definition_id:null,kind:node.initialValue?'declaration':'uninitialized_declaration'});
        const definition=result.sections.definitions.find(d=>d.definition_id===definition_id);if(definition)definition.source_scalar_version_id=scalar;
      }
      return;
    }
    if (node.nodeType === 'ExpressionStatement') { scanExpression(method, node.expression, context);const name=node.expression?.expression?.name;return name==='revert'?'revert':['selfdestruct','suicide'].includes(name)&&/t_function_(selfdestruct|suicide)_/.test(node.expression?.expression?.typeDescriptions?.typeIdentifier||'')?'jump':undefined; }
    if (['Break', 'Continue', 'PlaceholderStatement'].includes(node.nodeType)) { addStep(method, node, 'control', sourceText(node), context); return node.nodeType==='PlaceholderStatement'?undefined:'jump'; }
    for (const child of children(node)) scanExpression(method, child, context);
  };
  function analyzeAssemblyReturnCallsites(method,assembly,context) {
    function scan(y){
      const type={call:'CALL',staticcall:'STATICCALL',delegatecall:'DELEGATECALL',callcode:'CALLCODE'}[y.functionName?.name];
      if(y.nodeType==='YulFunctionCall'&&type){
        const node={...y,id:'yul-call:'+assembly.id+':'+y.src},loc=location(node),eid=getEvidence(method,node),dependency_id=id(method.method_id,node.id,context.path,'external-return');
        result.sections.external_returns||=[];result.sections.external_returns.push({dependency_id,method_id:method.method_id,source_callsite:loc,
          source_callsite_id:id(method.method_id,loc.file,loc.start,loc.length,type,'raw-call'),representation_scope:'source_statement',
          expression_id:null,call_type:type,signature:'raw_yul_call',selector:null,return_schema:'evm_return_bytes',return_types:['success_word','returndata'],
          target_expression:sourceText(y.arguments?.[1]),caller_code_address:method.code_address,caller_storage_address:method.storage_address,
          conditions:[...(context.conditions||[])],state_scope:'call_execution_state',status:'requires_target_identity_and_execution',evidence_ids:[eid]});
      }
      for(const child of children(y))scan(child);
    }
    if(assembly.AST)scan(assembly.AST);
  }
  function analyzeEncodedAssemblyCall(method,node,context) {
    // Only the first CALL can use the incoming source-object version without
    // interpreting earlier Yul effects. Its output writes happen after input use.
    const first=node.AST?.statements?.[0],call=first?.value||first?.expression;
    if(call?.nodeType!=='YulFunctionCall'||call.functionName?.name!=='call'||call.arguments?.length!==7)return;
    const a=call.arguments,zero=a[2]?.nodeType==='YulLiteral'&&BigInt(a[2].value||'0')===0n;
    if(!zero||a[1]?.nodeType!=='YulIdentifier'||a[3]?.functionName?.name!=='add'||a[4]?.functionName?.name!=='mload')return;
    const ptr=a[3].arguments,len=a[4].arguments;
    if(ptr?.length!==2||len?.length!==1||ptr[0].nodeType!=='YulIdentifier'||len[0].name!==ptr[0].name||ptr[1]?.nodeType!=='YulLiteral'||BigInt(ptr[1].value||'0')!==32n)return;
    const references=Array.isArray(node.externalReferences)?node.externalReferences:Object.entries(node.externalReferences||{}).map(([src,v])=>({src,...v}));
    const external=y=>{const ref=references.find(r=>r.src===y.src);if(!ref||ref.isSlot||ref.isOffset)return null;
      return{nodeType:'Identifier',id:'yul:'+y.src,src:y.src,name:y.name,referencedDeclaration:ref.declaration,typeDescriptions:nodes.get(ref.declaration)?.typeDescriptions}};
    const target=external(a[1]),encoded=external(ptr[0]);if(!target||!encoded)return;
    const decoded=decodeEncodedTransfer(encoded,context),args=decoded?.args||[],name=decoded?.name,
      call_id=id(method.method_id,node.id,context.path,'encoded-assembly-call'),eid=getEvidence(method,node),targetID=expression(method,target,context),
      argumentIDs=decoded?.argument_expression_ids||args.map(n=>expression(method,n,context));
    const row={call_id,method_id:method.method_id,call_type:'CALL',target_expression:sourceText(resolveArgument(target,context)),target_expression_id:targetID,
      selector:decoded?.selector||null,signature:decoded?.signature||'',input_arguments:args.map(sourceText),input_expression_ids:argumentIDs,
      calldata_basis:'source_bytes_object_bound_to_first_assembly_call',source_memory_version_id:decoded?.source_memory_version_id||null,
      encoding_condition_groups:decoded?.condition_groups||[],assumptions:decoded?.assumptions||[],
      failure_propagation:'assembly_paths_require_execution',conditions:[...context.conditions],evidence_ids:unique([eid,...(decoded?.source_memory_version_id?context.source_memory.entry(decoded.source_memory_version_id).record.evidence_ids:[])])};
    result.sections.calls.push(row);
    method.call_ids.push(call_id);
    sourceCallBinding(method,node,context,row,resolveSourceBytes(encoded,context));
    if(!decoded)return;
    const fund_id=id(call_id,'fund');
    result.sections.funds.push({fund_id,method_id:method.method_id,call_id,kind:name==='transfer'?'token_transfer_intent':name==='transferFrom'?'token_transfer_from_intent':'token_approval',
      asset:row.target_expression,asset_expression_id:targetID,from_expression:name==='transferFrom'?sourceText(args[0]):'address(this)',
      from_expression_id:name==='transferFrom'?argumentIDs[0]:null,to_expression:sourceText(args[name==='transferFrom'?1:0]),to_expression_id:argumentIDs[name==='transferFrom'?1:0],
      amount_expression:sourceText(args.at(-1)),amount_expression_id:argumentIDs.at(-1),unit:decoded.encoding==='encodeCall'?'token_raw':'asset_quantity_or_token_id',unit_basis:'caller_declared_interface',asset_standard_verified:false,
      source_memory_version_id:decoded.source_memory_version_id,call_input_binding_id:row.call_input_binding_id,encoding_condition_groups:decoded.condition_groups||[],assumptions:decoded.assumptions||[],
      settlement:'requires_execution_evidence',conditions:[...context.conditions],evidence_ids:row.evidence_ids});method.fund_ids.push(fund_id);
  }
  function sourceAssemblyPreservesMemory(node){
    if(!node.AST)return false;
    const refs=Array.isArray(node.externalReferences)?node.externalReferences:Object.entries(node.externalReferences||{}).map(([src,v])=>({src,...v})),functions=new Set();
    visit(node.AST,n=>{if(n.nodeType==='YulFunctionDefinition')functions.add(n.name)});
    const pure=new Set(('stop add sub mul div sdiv mod smod exp not lt gt slt sgt eq iszero and or xor byte shl shr sar addmod mulmod signextend keccak256 pop mload sload sstore tload tstore pc msize gas address balance selfbalance caller callvalue calldataload calldatasize codesize extcodesize extcodehash returndatasize create create2 return revert invalid selfdestruct log0 log1 log2 log3 log4 chainid basefee blobbasefee blobhash origin gasprice blockhash coinbase timestamp number prevrandao difficulty gaslimit').split(' '));
    const literal=n=>{try{return n?.nodeType==='YulLiteral'?BigInt(n.value):null}catch{return null}},scratch=(offset,length)=>{const a=literal(offset),b=typeof length==='bigint'?length:literal(length);return b===0n||a!==null&&b!==null&&a>=0n&&b>=0n&&a+b<=64n};
    let preserved=true;
    visit(node.AST,n=>{
      if(n.nodeType==='YulAssignment')for(const v of n.variableNames||[]){const ref=refs.find(r=>r.src===v.src);if(ref&&!ref.isSlot&&!ref.isOffset&&sourceBytesType(nodes.get(ref.declaration)))preserved=false;}
      if(n.nodeType!=='YulFunctionCall')return;const name=n.functionName?.name,a=n.arguments||[];
      if(name==='mstore'||name==='mstore8'){if(!scratch(a[0],name==='mstore'?32n:1n))preserved=false;}
      else if(['mcopy','calldatacopy','returndatacopy','codecopy'].includes(name)){if(!scratch(a[0],a[2]))preserved=false;}
      else if(name==='extcodecopy'){if(!scratch(a[1],a[3]))preserved=false;}
      else if(['call','callcode','delegatecall','staticcall'].includes(name)){if(!scratch(a.at(-2),a.at(-1)))preserved=false;}
      else if(!pure.has(name)&&!functions.has(name))preserved=false;
    });return preserved;
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
          // A second placeholder may re-enter the function body. Return slots
          // across early/implicit returns require their own pending-return join.
          ctx.source_scalars?.invalidate((fn.returnParameters?.parameters||[]).map(scalarDescriptor).filter(Boolean),
            'SOURCE_SCALAR_PENDING_RETURN_BEFORE_MODIFIER_CONTINUATION',scalarEffectContext(method,ctx,n));
          if(flow==='terminated')method.completeness.unresolved.push({code:'MODIFIER_TERMINATION_PATHS',ast_id:n.id});
          return;
        }
        if (n.nodeType === 'Block') { for (const stmt of n.statements || []) { const flow=expand(stmt, ctx);if(flow)return flow } return; }
        if (n.nodeType === 'IfStatement') {
          scanExpression(method, n.condition, ctx);
          const cond = sourceText(n.condition),conditionExpression=expression(method,n.condition,ctx);
          const yesContext={...ctx,source_scalars:ctx.source_scalars?.fork(),source_aggregates:ctx.source_aggregates?.fork(),source_memory:ctx.source_memory?.fork(),storage_references:ctx.storage_references?.fork(),namespace_aliases:new Map(ctx.namespace_aliases||[]),path:`${ctx.path}/if:${n.id}:true`,conditions:[...ctx.conditions,cond]},
            noContext={...ctx,source_scalars:ctx.source_scalars?.fork(),source_aggregates:ctx.source_aggregates?.fork(),source_memory:ctx.source_memory?.fork(),storage_references:ctx.storage_references?.fork(),namespace_aliases:new Map(ctx.namespace_aliases||[]),path:`${ctx.path}/if:${n.id}:false`,conditions:[...ctx.conditions,`!(${cond})`]};
          assumeByteBranch(yesContext.source_memory,conditionExpression,true,yesContext);assumeByteBranch(noContext.source_memory,conditionExpression,false,noContext);
          const yes=expand(n.trueBody,yesContext),no=expand(n.falseBody,noContext);
          mergeScalars(method,n,ctx,yesContext,noContext,conditionExpression,Boolean(yes),Boolean(no));
          ctx.source_memory?.merge(yesContext.source_memory,noContext.source_memory,{yesConditions:yesContext.conditions,noConditions:noContext.conditions,
            yesTerminated:Boolean(yes),noTerminated:Boolean(no),context:{path:ctx.path,ast_id:n.id}});
          ctx.storage_references?.merge(yesContext.storage_references,noContext.storage_references,{yesConditions:yesContext.conditions,noConditions:noContext.conditions,
            yesTerminated:Boolean(yes),noTerminated:Boolean(no),context:{path:ctx.path,ast_id:n.id}});
          for(const key of ctx.namespace_aliases?.keys()||[]){const a=yesContext.namespace_aliases.get(key),b=noContext.namespace_aliases.get(key);ctx.namespace_aliases.set(key,yes&&!no?b:no&&!yes?a:a?.id===b?.id?a:null);}
          if(yes&&no)return yes===no?yes:'terminated';
          if(yes)ctx.conditions.push(`!(${cond})`);
          if(no)ctx.conditions.push(cond);
          return;
        }
        return analyzeBlock(method, n, ctx);
      };
      const scalarParent=activeContext.source_scalars,parentKeys=[...(scalarParent?.bindings.keys()||[])],parameterIDs=new Set((definition.parameters?.parameters||[]).map(p=>p.id)),aggregateParent=activeContext.source_aggregates,aggregateKeys=[...(aggregateParent?.references.keys()||[])];
      const modifierContext={...activeContext,source_scalars:scalarParent?.fork({frameId:activeContext.path+'/modifier:'+definition.id+':'+index}),source_aggregates:aggregateParent?.fork({frameId:activeContext.path+'/modifier:'+definition.id+':'+index,shareHeap:true}),path:`${activeContext.path}/modifier:${definition.id}`,parameter_bindings:{...activeContext.parameter_bindings,...Object.fromEntries((definition.parameters?.parameters||[]).map((p,i)=>[p.id,sourceText(invocation.arguments?.[i])]))},
        parameter_arguments:{...activeContext.parameter_arguments,...Object.fromEntries((definition.parameters?.parameters||[]).map((p,i)=>[p.id,invocation.arguments?.[i]]))}};
      for(const[i,p]of(definition.parameters?.parameters||[]).entries())bindStorageReference(method,modifierContext,p,invocation.arguments?.[i]);
      for(const[i,p]of(definition.parameters?.parameters||[]).entries())bindScalar(method,modifierContext,p,null,{valueExpression:expression(method,invocation.arguments?.[i],activeContext),kind:'modifier_parameter'});
      for(const[i,p]of(definition.parameters?.parameters||[]).entries())if(aggregateDescriptor(p))modifierContext.source_aggregates.bind(aggregateDescriptor(p),resolveAggregate(method,invocation.arguments?.[i],activeContext),{context:aggregateContext(method,p,modifierContext),kind:'modifier_parameter'});
      const flow=expand(body,modifierContext);
      if(scalarParent)for(const key of parentKeys)if(!parameterIDs.has(key)){
        const value=modifierContext.source_scalars.bindings.get(key);if(value)scalarParent.bindings.set(key,value);else scalarParent.bindings.delete(key);
      }
      if(aggregateParent)for(const key of aggregateKeys)if(!parameterIDs.has(key)){const value=modifierContext.source_aggregates.references.get(key);if(value)aggregateParent.references.set(key,value);else aggregateParent.references.delete(key);}
      return flow;
    };
    return execute(0);
  }
  const loopSummaries=analyzeLoopInvariants(result,{functions,nodes,methodByAST,location,getEvidence,legacyArithmetic:Number(source.compilation.compilerVersion.split('.')[1])<8});
  for (const fn of functions) {
    const method = methodByAST.get(fn.id);
    if (!method) continue;
    runMethod(method, fn, () => {
    const context = { path: 'entry', conditions: [], stack: [fn.id], function_frame:fn.id, unchecked: false,namespace_aliases:new Map(),loop_summaries:loopSummaries,legacy_arithmetic:Number(source.compilation.compilerVersion.split('.')[1])<8,
      source_memory:new SourceByteMemory(result,method,{budget:sourceMemoryBudget,allocatorKnown:method.dispatch_role==='external_dispatch'||['fallback','receive'].includes(method.entry_kind)}),
      storage_references:new StorageReferences(result,method,{budget:storageReferenceBudget}),source_scalars:new SourceScalars(result,method,{budget:scalarBudget}),source_aggregates:new SourceAggregates(result,method,{budget:aggregateBudget,onExpression:row=>{expressionByID.set(row.expression_id,row);consumeExpression();}}) };
    initializeScalars(method,fn,context);
    initializeAggregates(method,fn,context);
    analyzeFunctionWithModifiers(method,fn,context);
    if(['view','pure'].includes(method.state_mutability)&&(fn.returnParameters?.parameters||[]).length) {
      try {
        const args=(fn.parameters?.parameters||[]).map(p=>({...p,nodeType:'Identifier',id:'entry-argument:'+p.id,referencedDeclaration:p.id,entry_argument:true}));
        const call={...fn,id:'entry-return:'+fn.id};
        const model=boundedInternalReturns(method,{target:fn,call,context:{...context,entry_return_summary:true},args,nodes,typeOf,
          build:(n,c)=>expression(method,n,c),expressionType:key=>expressionByID.get(key)?.type,expressionInfo:key=>expressionByID.get(key),synthetic:(n,f,o,c,x)=>synthetic(method,n,f,o,c,x)});
        const row=result.sections.expressions.find(e=>e.expression_id===model.expression_id);
        row.entry_return_model=model;method.entry_return_expression_id=model.expression_id;
        for(const missing of model.frontier||[])result.sections.frontier.push({frontier_id:id(method.method_id,'loop-remainder',missing.ast_id,missing.conditions),method_id:method.method_id,
          ...missing,evidence_ids:[getEvidence(method,fn)]});
      }catch(error){if(/^SOURCE_(?:METHOD|TOTAL)_.*BUDGET_EXCEEDED$/.test(error.message))throw error;method.entry_return_model_status='partial';method.entry_return_model_unresolved=[String(error.message)];}
    }
    method.completeness.complete = method.completeness.unresolved.length === 0;
    method.source_memory_version_ids=(result.sections.source_memory_versions||[]).filter(r=>r.method_id===method.method_id).map(r=>r.source_memory_version_id);
    method.source_memory_phi_ids=(result.sections.source_memory_phis||[]).filter(r=>r.method_id===method.method_id).map(r=>r.source_memory_phi_id);
    method.call_input_binding_ids=(result.sections.call_input_bindings||[]).filter(r=>r.method_id===method.method_id).map(r=>r.call_input_binding_id);
    method.storage_reference_ids=(result.sections.storage_references||[]).filter(r=>r.method_id===method.method_id).map(r=>r.storage_reference_id);
    method.storage_reference_phi_ids=(result.sections.storage_reference_phis||[]).filter(r=>r.method_id===method.method_id).map(r=>r.storage_reference_phi_id);
    method.source_scalar_version_ids=(result.sections.source_scalar_versions||[]).filter(r=>r.method_id===method.method_id).map(r=>r.source_scalar_version_id);
    method.source_scalar_phi_ids=(result.sections.source_scalar_phis||[]).filter(r=>r.method_id===method.method_id).map(r=>r.source_scalar_phi_id);
    method.source_aggregate_version_expression_ids=result.sections.expressions.filter(r=>r.method_id===method.method_id&&r.symbolic_format==='source_aggregate_version').map(r=>r.expression_id);
    method.source_aggregate_field_expression_ids=result.sections.expressions.filter(r=>r.method_id===method.method_id&&r.source_aggregate_field_declaration_id!==undefined).map(r=>r.expression_id);
    method.source_aggregate_reference_binding_ids=result.sections.dataflow.filter(r=>r.method_id===method.method_id&&r.kind==='source_aggregate_reference_binding').map(r=>r.dataflow_id);
    });
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
    method.source_location = location(variable);
    result.sections.methods.push(method);
    runMethod(method, variable, () => {
    const context = { path: 'getter', conditions: [], unchecked: false };
    if (!variable.constant && variable.mutability !== 'immutable') stateAccess(method, { ...variable, nodeType: 'Identifier', referencedDeclaration: variable.id }, 'read', context);
    const step = addStep(method, variable, 'return', `读取并返回 ${name}${method.parameters.length ? ' 对应键值' : ''}`, context);
    method.return_ids.push(step.step_id);
    });
  }
  const verificationContext={functions,nodes,methodByAST,location,getEvidence,layout,storage_types:storageTypes,namespace_registry:namespaceRegistry,stateVars,mainContract,sourceText,loopSummaries,runtimeValue,chainID:result.snapshot.chain_id||result.snapshot.declared_environment?.chain_id,executionAddress:result.snapshot.storage_address||result.snapshot.declared_environment?.execution_address,environmentBasis:result.snapshot.identity_kind==='code_only'?'declared':'observed',compilerVersion:source.compilation.compilerVersion,viaIR:source.stdJsonInput.settings?.viaIR===true,assembly:executeAssembly,evmRules:result.snapshot.evm_rules,legacyArithmetic:Number(source.compilation.compilerVersion.split('.')[1])<8};
  if (!failures.length) {
  verificationContext.creationTransition=buildCreationTransition(result,verificationContext);
  const creation=verificationContext.creationTransition.model;
  if(creation){
    const constructor=functions.find(f=>f.scope===mainContract.id&&(f.kind==='constructor'||f.isConstructor));
    creation.creation_identity={compiler:source.compilation.compilerVersion,via_ir:source.stdJsonInput.settings?.viaIR===true,
      initcode:/^(?:[0-9a-fA-F]{2})+$/.test(compiled.evm?.bytecode?.object||'')?'0x'+compiled.evm.bytecode.object:null,
      constructor_parameters:(constructor?.parameters?.parameters||[]).map(p=>({source_ast_id:p.id,name:p.name,type:typeOf(p)})),
      scope:'recompiled_initcode_and_static_constructor_arguments',historical_execution_required:true};
  }
  analyzePolicies(result,verificationContext,analyzeStateModels(result,verificationContext));
  } else {
    result.sections.engines.push({engine:'source-state-verification',status:'skipped',reason:'source_method_models_incomplete',
      affected_method_ids:failures.map(f=>f.method_id)});
    result.sections.coverage.push({dimension:'source_state_models',complete:false,reason:'source_method_models_incomplete',
      affected_method_ids:failures.map(f=>f.method_id)});
  }
  addSourcePCMap(result,{compiled,bytecode,output,compiler:source.compilation.compilerVersion});
  // Namespace evidence is populated after declaration discovery. Preserve the
  // completed layout, rather than the earlier unpopulated metadata snapshot.
  layoutArtifact.text = JSON.stringify(layout || {});
  layoutArtifact.sha256 = hash(layoutArtifact.text);
  result.sections.engines.push({ engine: 'solc-ast', version: source.compilation.compilerVersion, adapter_version: 'source-1',
    status: failures.length?'partial':'completed', duration_ms: Math.round(performance.now() - started), methods: methodByAST.size,
    failed_method_ids:failures.map(f=>f.method_id), budget:{total_expressions:totalExpressionLimit,method_expressions:methodExpressionLimit,consumed_expressions:expressionNumber,additional_return_expression_limit:enhancedReturnBudget.max,additional_return_expressions:enhancedReturnBudget.expressions},
    source_verification: source[SUBMITTED_TEMPLATE]?'compiled_template_without_observed_deployment':'recompiled_and_snapshot_matched' });
  result.sections.coverage.push({ dimension: 'source', complete: !failures.length, basis: source[SUBMITTED_TEMPLATE]?'submitted_source_and_compiler_AST':'recompiled_runtime_and_verified_snapshot', methods: methodByAST.size,
    unresolved:failures,compiled_entries_preserved:true });
  if(layout.basis)result.sections.coverage.push({dimension:'storage_layout',basis:layout.basis,complete:layout.unresolved.length===0,unresolved:layout.unresolved});
  if(layout.namespace_storage)result.sections.coverage.push({dimension:'namespaced_storage',basis:layout.namespace_storage.scope,complete:layout.namespace_storage.complete,
    namespaces:layout.namespace_storage.namespaces.length,accessors:layout.namespace_storage.accessors.length,unresolved:layout.namespace_storage.unresolved,
    execution_operations_scope:'see_individual_state_transitions',historical_initialization_verified:false});
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
