import {numericLiteral} from '../literals.mjs';
import {lowerYul} from './yul.mjs';
import {evmWord} from './evm-word.mjs';
import {detectLoopRecurrences} from '../verification/loops.mjs';
import {builtin,internalDispatch} from './source-symbols.mjs';
import {fixedBytesShift,fixedBytesValue} from './scalar-casts.mjs';

// Bounded read-only execution. Local scalar writes use immutable expression
// bindings; branch guards and evaluations retain their execution order.
export function summarizeInternalReturns({target, call, context, args, build, synthetic, nodes, typeOf,expressionType=()=>null}) {
  const fail = code => { throw new Error(code); };
  const output = target.returnParameters?.parameters || [];
  const scalar = d => /^(?:u?int\d*|bool|bytes(?:[1-9]|[12]\d|3[0-2])|address(?: payable)?|contract [^\[\]\s]+)$/.test(typeOf(d));
  if (!output.length || output.length > 8 || output.some(d => !scalar(d))) fail('INTERNAL_RETURN_NONSCALAR_OR_TUPLE');
  if (!['pure','view'].includes(target.stateMutability) && !target.constant) fail('INTERNAL_RETURN_STATE_EFFECT_UNSUPPORTED');
  if ((target.parameters?.parameters || []).some(p => p.storageLocation === 'storage')) fail('INTERNAL_RETURN_STORAGE_ALIAS_UNSUPPORTED');
  const stack = context.return_stack || [];
  if (stack.length >= 12 || stack.includes(target.id)) fail('INTERNAL_RETURN_RECURSION_OR_DEPTH');
  let count = 0, serial = 0, loopIterations = 0;
  const loops = new Map(), results = [], frontier = [], memoryEffects=[], effectCache = new WeakMap();
  const expressionChildren = n => Object.entries(n || {}).filter(([k]) => !['typeDescriptions','documentation'].includes(k))
    .flatMap(([,v]) => Array.isArray(v) ? v.filter(x=>x?.nodeType) : v?.nodeType ? [v] : []);
  const fork = (s,suffix) => ({...s,path:s.path+'/'+suffix,parameter_arguments:{...s.parameter_arguments},
    parameter_expression_bindings:{...s.parameter_expression_bindings},compiletime_bindings:{...s.compiletime_bindings},
    range_bindings:{...s.range_bindings},yul_memory:[...(s.yul_memory||[])],
    return_conditions:[...s.return_conditions],execution_steps:[...s.execution_steps]});
  const bounded = rows => {if(rows.length+results.length>32)fail('INTERNAL_RETURN_PATH_BUDGET');return rows;};
  function validate(n) {
    if (!n || ++count > 1024) fail('INTERNAL_RETURN_EXPRESSION_BUDGET');
    if (n.nodeType==='NewExpression' || n.nodeType==='UnaryOperation' && n.operator==='delete') fail('INTERNAL_RETURN_EXPRESSION_SIDE_EFFECT');
    if (n.nodeType === 'FunctionCall' && n.kind !== 'typeConversion') {
      const called = nodes.get(n.expression?.referencedDeclaration);
      if (called?.nodeType === 'FunctionDefinition' && !['pure','view'].includes(called.stateMutability) && !called.constant ||
          ['call','send','transfer','delegatecall','callcode','selfdestruct','suicide'].includes(n.expression?.memberName || n.expression?.name)) fail('INTERNAL_RETURN_EXPRESSION_SIDE_EFFECT');
      if(called?.nodeType!=='FunctionDefinition'&&!['keccak256','sha256','ripemd160','ecrecover','addmod','mulmod'].includes(n.expression?.name)) fail('INTERNAL_RETURN_DYNAMIC_CALL_UNSUPPORTED');
    }
    for (const child of expressionChildren(n)) validate(child);
  }
  function effects(n) {
    if(!n)return {reads:new Set(),writes:new Set()};
    if(effectCache.has(n))return effectCache.get(n);
    const parts=expressionChildren(n).map(effects),reads=new Set(parts.flatMap(p=>[...p.reads])),writes=new Set(parts.flatMap(p=>[...p.writes]));
    if(n.nodeType==='Identifier')reads.add(n.referencedDeclaration);
    if(n.nodeType==='Assignment')writes.add(n.leftHandSide?.referencedDeclaration??'unsupported_target');
    if(n.nodeType==='UnaryOperation'&&['++','--'].includes(n.operator))writes.add(n.subExpression?.referencedDeclaration??'unsupported_target');
    if(n.nodeType==='YulMemoryLoad')reads.add('yul_memory');
    if(n.nodeType==='YulMemoryStore')writes.add('yul_memory');
    const found={reads,writes};effectCache.set(n,found);return found;
  }
  // Ordinary sibling order is unspecified. Source-order traversal is valid
  // only when the siblings cannot observe each other's local writes.
  function independent(list) {
    const es=list.map(effects);
    for(let i=0;i<es.length;i++)for(let j=i+1;j<es.length;j++)
      if([...es[i].writes].some(k=>es[j].reads.has(k)||es[j].writes.has(k)) || [...es[j].writes].some(k=>es[i].reads.has(k)))
        fail('INTERNAL_RETURN_EVALUATION_ORDER_UNSPECIFIED');
  }
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
    if(n.nodeType==='YulBuiltin') {
      const args=n.arguments.map(a=>constant(a,s,next));
      return args.some(a=>a===null)?null:evmWord(n.functionName.name,args);
    }
    if(n.nodeType==='YulWordCast'){const v=constant(n.expression,s,next);return v===null?null:integerValue(v,n,{...s,unchecked:true});}
    if(n.nodeType==='Identifier') {
      if(Object.hasOwn(s.compiletime_bindings||{},n.referencedDeclaration))return s.compiletime_bindings[n.referencedDeclaration];
      if(Object.hasOwn(s.parameter_expression_bindings||{},n.referencedDeclaration))return null;
      const d=nodes.get(n.referencedDeclaration),bound=s.parameter_arguments?.[n.referencedDeclaration];
      return d?.constant&&d.value?constant(d.value,s,next):bound&&bound.id!==n.id?constant(bound,s,next):null;
    }
    if(n.nodeType==='TupleExpression'&&n.components?.length===1)return constant(n.components[0],s,next);
    if(n.nodeType==='Conditional'){const c=constant(n.condition,s,next);return c===null?null:constant(c!==0n?n.trueExpression:n.falseExpression,s,next);}
    if(n.nodeType==='UnaryOperation') {const a=constant(n.subExpression,s,next);if(a===null)return null;return n.operator==='!'?(a===0n?1n:0n):n.operator==='-'?integerValue(-a,n,s):n.operator==='+'?a:null;}
    if(n.nodeType==='BinaryOperation') {
      const a=constant(n.leftExpression,s,next);if(a===null)return null;
      if(n.operator==='&&'&&a===0n)return 0n;if(n.operator==='||'&&a!==0n)return 1n;
      const b=constant(n.rightExpression,s,next);if(b===null)return null;
      switch(n.operator){case '+':return integerValue(a+b,n,s);case '-':return integerValue(a-b,n,s);case '*':return integerValue(a*b,n,s);case '/':return b===0n?null:integerValue(a/b,n,s);case '%':return b===0n?null:a%b;case '<':return a<b?1n:0n;case '<=':return a<=b?1n:0n;case '>':return a>b?1n:0n;case '>=':return a>=b?1n:0n;case '==':return a===b?1n:0n;case '!=':return a!==b?1n:0n;case '&&':return b!==0n?1n:0n;case '||':return b!==0n?1n:0n;default:return null;}
    }
    return null;
  }
  function interval(n,s) {
    const v=constant(n,s);if(v!==null)return [v,v];
    if(n?.nodeType!=='Identifier')return null;
    const key=n.referencedDeclaration,known=s.range_bindings?.[key];if(known)return known;
    const d=nodes.get(key)||n,t=/^(u?int)(\d*)$/.exec(typeOf(d));if(!t)return null;
    const bits=BigInt(t[2]||256),mod=1n<<bits;return t[1]==='int'?[-(mod>>1n),(mod>>1n)-1n]:[0n,mod-1n];
  }
  function truthKnown(n,s) {
    const v=constant(n,s);if(v!==null)return v;
    if(n?.nodeType!=='BinaryOperation')return null;
    const a=interval(n.leftExpression,s),b=interval(n.rightExpression,s);if(!a||!b)return null;
    switch(n.operator){
      case '<':return a[1]<b[0]?1n:a[0]>=b[1]?0n:null;
      case '<=':return a[1]<=b[0]?1n:a[0]>b[1]?0n:null;
      case '>':return a[0]>b[1]?1n:a[1]<=b[0]?0n:null;
      case '>=':return a[0]>=b[1]?1n:a[1]<b[0]?0n:null;
      case '==':return a[1]<b[0]||b[1]<a[0]?0n:null;
      case '!=':return a[1]<b[0]||b[1]<a[0]?1n:null;
      default:return null;
    }
  }
  function assume(n,expected,s) {
    if(n?.nodeType==='UnaryOperation'&&n.operator==='!')return assume(n.subExpression,!expected,s);
    if(n?.nodeType==='BinaryOperation'&&((n.operator==='&&'&&expected)||(n.operator==='||'&&!expected))) {
      return assume(n.leftExpression,expected,s)&&assume(n.rightExpression,expected,s);
    }
    if(n?.nodeType!=='BinaryOperation')return true;
    let variable=n.leftExpression,fixed=constant(n.rightExpression,s),op=n.operator;
    if(fixed===null){variable=n.rightExpression;fixed=constant(n.leftExpression,s);op=({'<':'>','<=':'>=','>':'<','>=':'<=','==':'==','!=':'!='})[op];}
    if(!expected)op=({'<':'>=','<=':'>','>':'<=','>=':'<','==':'!=','!=':'=='})[op];
    if(variable?.nodeType!=='Identifier'||fixed===null||!op)return true;
    const range=interval(variable,s);if(!range)return true;let [lo,hi]=range;
    if(op==='<')hi=hi<fixed-1n?hi:fixed-1n;
    if(op==='<=')hi=hi<fixed?hi:fixed;
    if(op==='>')lo=lo>fixed+1n?lo:fixed+1n;
    if(op==='>=')lo=lo>fixed?lo:fixed;
    if(op==='=='){lo=lo>fixed?lo:fixed;hi=hi<fixed?hi:fixed;}
    if(op==='!='&&lo===hi&&lo===fixed)return false;
    s.range_bindings[variable.referencedDeclaration]=[lo,hi];return lo<=hi;
  }
  const initial = {...context,path:context.path+'/return:'+call.id,unchecked:false,return_stack:[...stack,target.id],
    parameter_arguments:{...context.parameter_arguments},parameter_expression_bindings:{...context.parameter_expression_bindings},
    compiletime_bindings:{},range_bindings:{},return_conditions:[],execution_steps:[]};
  for(const [i,p]of (target.parameters?.parameters||[]).entries()) {
    initial.parameter_arguments[p.id]=args[i];initial.parameter_expression_bindings[p.id]=castBinding(p,build(args[i],context),initial);
    const known=constant(args[i],context);if(known!==null)initial.compiletime_bindings[p.id]=fixedBytesValue(known,typeOf(args[i]),typeOf(p));
  }
  function defaultValue(d,s) {
    const type=typeOf(d);if(!scalar(d))fail('INTERNAL_RETURN_LOCAL_TYPE_UNSUPPORTED');
    return synthetic(d,'literal',[],s,{value:type==='bool'?'false':/^(address|contract )/.test(type)?'0x'+'0'.repeat(40):/^bytes/.test(type)?'0x'+'00'.repeat(Number(type.slice(5))):'0',type});
  }
  for(const d of output)if(d.name){initial.parameter_expression_bindings[d.id]=defaultValue(d,initial);initial.compiletime_bindings[d.id]=0n;}
  function local(n,s) {
    const d=nodes.get(n?.referencedDeclaration);
    if(n?.nodeType!=='Identifier'||!d||d.stateVariable||!scalar(d)||!Object.hasOwn(s.parameter_expression_bindings,d.id))fail('INTERNAL_RETURN_ASSIGNMENT_TARGET_UNSUPPORTED');
    return d;
  }
  function bind(d,key,known,s,range=null) {
    const from=expressionType(key);key=castBinding(d,key,s);if(known!==null)known=fixedBytesValue(known,from,typeOf(d));
    s.parameter_expression_bindings[d.id]=key;
    delete s.range_bindings[d.id];
    if(known===null)delete s.compiletime_bindings[d.id];else s.compiletime_bindings[d.id]=integerValue(known,d,s);
    if(known===null&&range){const full=interval({nodeType:'Identifier',referencedDeclaration:d.id},s);if(full&&range[0]>=full[0]&&range[1]<=full[1])s.range_bindings[d.id]=range;}
  }
  function castBinding(d,key,s){
    const from=expressionType(key),to=typeOf(d);if(!fixedBytesShift(from,to))return key;
    return synthetic(d,'scalar_cast',[key],s,{type:to,width:Number(to.slice(5))*8,signed:false,arithmetic:'wrap',cast_source_type:from});
  }
  function guard(s,key,expected,failure) {
    const condition={expression_id:key,expected,...(failure?{failure}:{})};
    s.return_conditions.push(condition);s.execution_steps.push({kind:'guard',...condition});return s;
  }
  function choose(row,n,label) {
    if(row.known!==null)return [{...row,expected:row.known!==0n}];
    return [true,false].map(expected=>({...row,s:guard(fork(row.s,label+':'+n.id+':'+expected),row.key,expected),expected}))
      .filter(branch=>assume(n.condition||n,branch.expected,branch.s));
  }
  function evaluate(n,s) {
    if(++count>1024)fail('INTERNAL_RETURN_EXPRESSION_BUDGET');
    if(n.nodeType==='YulMemoryStore'||n.nodeType==='YulMemoryLoad') {
      return evaluateList(n.arguments,s).map(row=>{
        const width=n.memoryWidth||32,offset=row.keys[0],offsetKnown=row.known[0],
          canonical=n.arguments[0].nodeType==='Identifier'?row.s.parameter_expression_bindings[n.arguments[0].referencedDeclaration]||offset:offset;
        const writes=row.s.yul_memory||[];
        if(n.nodeType==='YulMemoryStore') {
          if(writes.length>=64)fail('INTERNAL_RETURN_YUL_MEMORY_WRITE_BUDGET');
          const bounds=offsetKnown===null?interval(n.arguments[0],row.s):[offsetKnown,offsetKnown];
          if(!bounds||!(bounds[0]>=128n||bounds[1]+BigInt(width)<=64n))frontier.push({kind:'assembly_compiler_memory_alias',ast_id:n.id,
            conditions:[...row.s.return_conditions],reason:'INTERNAL_RETURN_YUL_RESERVED_MEMORY_REGION_MAY_BE_CLOBBERED'});
          row.s.yul_memory=[...writes,{offset,value:row.keys[1],width,canonical,known:offsetKnown?.toString()??null}];
          memoryEffects.push({kind:width===32?'mstore':'mstore8',ast_id:n.id,path:row.s.path,offset_expression_id:offset,value_expression_id:row.keys[1],conditions:[...row.s.return_conditions],scope:'one_inline_assembly_block'});
          const key=synthetic(n,'yul_memory_access',[offset,row.keys[1]],row.s,{memory_width:width,type:'uint256',width:256,signed:false,arithmetic:'wrap'});
          row.s.execution_steps.push({kind:'evaluate',expression_id:key});return {s:row.s,key,known:0n};
        }
        const covered=new Set();for(const w of writes) {
          if(w.width===32&&w.canonical===canonical)for(let i=0;i<32;i++)covered.add(i);
          if(offsetKnown!==null&&w.known!==null)for(let i=0;i<32;i++)if(offsetKnown+BigInt(i)>=BigInt(w.known)&&offsetKnown+BigInt(i)<BigInt(w.known)+BigInt(w.width))covered.add(i);
        }
        const operands=[offset,...writes.flatMap(w=>[w.offset,w.value])],memory_writes=writes.map((w,i)=>({offset_operand:1+i*2,value_operand:2+i*2,width:w.width,
          offset_known:w.known,offset_equals_read:w.canonical===canonical}));
        const key=synthetic(n,'yul_memory_read',operands,row.s,{memory_writes,type:'uint256',width:256,signed:false,arithmetic:'wrap',
          initial_memory:'symbolic_solidity_assembly_entry_memory',read_offset_known:offsetKnown?.toString()??null,all_read_bytes_written:covered.size===32,
          ...(covered.size===32?{}:{unresolved_return_value:'INTERNAL_RETURN_YUL_MEMORY_ENTRY_BYTES_UNKNOWN'})});
        memoryEffects.push({kind:'mload',ast_id:n.id,path:row.s.path,offset_expression_id:offset,result_expression_id:key,
          all_read_bytes_written:covered.size===32,conditions:[...row.s.return_conditions],scope:'one_inline_assembly_block'});
        return {s:row.s,key,known:null};
      });
    }
    if(!effects(n).writes.size&&!effects(n).reads.has('yul_memory'))return [{s,key:build(n,{...s,path:s.path+'/expr:'+(serial++)}),known:truthKnown(n,s),range:interval(n,s)}];
    if(n.nodeType==='YulBuiltin')return evaluateList(n.arguments,s).map(row=>({s:row.s,
      key:synthetic(n,'evm_word',row.keys,row.s,{operator:n.functionName.name,type:'uint256',width:256,signed:false,arithmetic:'wrap'}),known:null}));
    if(n.nodeType==='YulWordCast')return evaluate(n.expression,s).map(row=>({s:row.s,key:synthetic(n,'identity',[row.key],row.s,{type:typeOf(n),yul_word_assignment:true,arithmetic:'wrap'}),known:null}));
    if(n.nodeType==='Assignment') {
      if(n.leftHandSide?.nodeType==='TupleExpression')fail('INTERNAL_RETURN_NESTED_TUPLE_ASSIGNMENT_UNSUPPORTED');
      const d=local(n.leftHandSide,s),old=s.parameter_expression_bindings[d.id],oldKnown=s.compiletime_bindings[d.id]??null,oldRange=interval(n.leftHandSide,s);
      if(n.operator!=='='&&effects(n.rightHandSide).writes.has(d.id))fail('INTERNAL_RETURN_EVALUATION_ORDER_UNSPECIFIED');
      return bounded(evaluate(n.rightHandSide,s).map(row=>{
        const key=n.operator==='='?castBinding(d,row.key,row.s):synthetic(n,'binary',[old,row.key],row.s,{operator:n.operator.slice(0,-1),type:typeOf(d)});
        let known=n.operator==='='?row.known:null;
        if(known!==null)known=fixedBytesValue(known,expressionType(row.key),typeOf(d));
        if(n.operator!=='='&&oldKnown!==null&&row.known!==null){const a=oldKnown,b=row.known;known=n.operator==='+='?a+b:n.operator==='-='?a-b:n.operator==='*='?a*b:n.operator==='/='&&b!==0n?a/b:n.operator==='%='&&b!==0n?a%b:null;}
        const assigned=synthetic(n,'identity',[key],row.s,{type:typeOf(d),local_assignment:true});
        const range=n.operator==='='?row.range:oldRange&&row.known!==null&&['+=','-='].includes(n.operator)?oldRange.map(v=>n.operator==='+='?v+row.known:v-row.known):null;
        bind(d,assigned,known,row.s,range);row.s.execution_steps.push({kind:'evaluate',expression_id:assigned});
        return {s:row.s,key:assigned,known:row.s.compiletime_bindings[d.id]??null};
      }));
    }
    if(n.nodeType==='UnaryOperation'&&['++','--'].includes(n.operator)) {
      const d=local(n.subExpression,s),old=s.parameter_expression_bindings[d.id],oldKnown=s.compiletime_bindings[d.id]??null,oldRange=interval(n.subExpression,s);
      const one=synthetic(n,'literal',[],s,{value:'1',type:typeOf(d)}),key=synthetic(n,'binary',[old,one],s,{operator:n.operator==='++'?'+':'-',type:typeOf(d)});
      bind(d,key,oldKnown===null?null:oldKnown+(n.operator==='++'?1n:-1n),s,oldRange?.map(v=>v+(n.operator==='++'?1n:-1n)));s.execution_steps.push({kind:'evaluate',expression_id:key});
      return [{s,key:n.prefix?key:old,known:n.prefix?s.compiletime_bindings[d.id]??null:oldKnown}];
    }
    if(n.nodeType==='BinaryOperation'&&['&&','||'].includes(n.operator)) {
      return bounded(evaluate(n.leftExpression,s).flatMap(left=>choose(left,n,'short-circuit').flatMap(branch=>{
        if(branch.expected===(n.operator==='&&'))return evaluate(n.rightExpression,branch.s);
        return [{s:branch.s,key:synthetic(n,'literal',[],branch.s,{value:branch.expected?'true':'false',type:'bool'}),known:branch.expected?1n:0n}];
      })));
    }
    if(n.nodeType==='Conditional')return bounded(evaluate(n.condition,s).flatMap(row=>choose(row,n,'conditional').flatMap(branch=>evaluate(branch.expected?n.trueExpression:n.falseExpression,branch.s).map(r=>({...r,key:castBinding(n,r.key,r.s),known:r.known===null?null:fixedBytesValue(r.known,expressionType(r.key),typeOf(n))})))));
    if(n.nodeType==='TupleExpression') {
      if(n.components?.length===1)return evaluate(n.components[0],s);
      return evaluateList(n.components,s).map(row=>({s:row.s,key:synthetic(n,'tuple',row.keys,row.s,{type:typeOf(n)}),known:null}));
    }
    if(n.nodeType==='BinaryOperation')return evaluateList([n.leftExpression,n.rightExpression],s).map(row=>({s:row.s,key:synthetic(n,'binary',row.keys,row.s,{operator:n.operator,common_type:n.commonType?.typeString||null}),known:null}));
    if(n.nodeType==='UnaryOperation')return evaluate(n.subExpression,s).map(row=>({s:row.s,key:synthetic(n,'unary',[row.key],row.s,{operator:n.operator}),known:null}));
    if(n.nodeType==='FunctionCall') {
      independent([n.expression,...n.arguments]);
      if(effects(n.expression).writes.size)fail('INTERNAL_RETURN_CALLEE_SIDE_EFFECT_UNSUPPORTED');
      return evaluateList(n.arguments,s).map(row=>{
        const c=fork(row.s,'arguments:'+(serial++)),arguments_=n.arguments.map((a,i)=>{
          const ref='return-argument:'+(serial++);c.parameter_expression_bindings[ref]=row.keys[i];
          return {nodeType:'Identifier',id:ref,referencedDeclaration:ref,name:'bound_argument',src:a.src,typeDescriptions:a.typeDescriptions};
        });
        return {s:row.s,key:build({...n,arguments:arguments_},c),known:null};
      });
    }
    fail('INTERNAL_RETURN_EXPRESSION_EFFECT_UNSUPPORTED:'+n.nodeType);
  }
  function evaluateList(list,s) {
    independent(list);let pending=[{s,keys:[],known:[],ranges:[]}];
    for(const n of list)pending=bounded(pending.flatMap(p=>evaluate(n,p.s).map(r=>({s:r.s,keys:[...p.keys,r.key],known:[...p.known,r.known],ranges:[...p.ranges,r.range]}))));
    return pending;
  }
  function value(n,s) {
    validate(n);return bounded(evaluate(n,s).map(row=>{row.s.execution_steps.push({kind:'evaluate',expression_id:row.key});return row;}));
  }
  function tupleValues(n,length,s) {
    if(length===1)return value(n,s).map(r=>({s:r.s,keys:[r.key],known:[r.known],ranges:[r.range]}));
    if(n?.nodeType==='TupleExpression'&&n.components?.length===length&&n.components.every(Boolean)) {
      validate(n);return evaluateList(n.components,s).map(r=>{for(const key of r.keys)r.s.execution_steps.push({kind:'evaluate',expression_id:key});return r;});
    }
    const componentTypes=/^tuple\((.*)\)$/.exec(typeOf(n))?.[1].split(',').map(t=>t.trim())||[];
    return value(n,s).map(r=>({s:r.s,keys:Array.from({length},(_,i)=>synthetic(n,'tuple_component',[r.key],r.s,{component_index:i,type:componentTypes[i]||'unresolved'})),known:Array(length).fill(null)}));
  }
  function returned(keys,s) {
    if(keys.some(v=>!v))fail('INTERNAL_RETURN_VALUE_UNAVAILABLE');
    keys=keys.map((key,i)=>castBinding(output[i],key,s));
    results.push({expression_id:keys[0],expression_ids:keys,conditions:s.return_conditions,execution_steps:s.execution_steps});bounded([]);
  }
  function execute(n,s) {
    if(++count>1024||results.length>32)fail('INTERNAL_RETURN_PATH_BUDGET');
    if(!n||s.control)return [s];
    if(['Block','UncheckedBlock'].includes(n.nodeType)) {
      const outer=s.unchecked;let pending=[{...s,unchecked:s.unchecked||n.nodeType==='UncheckedBlock'}];
      for(const stmt of n.statements||[]){pending=bounded(pending.flatMap(p=>execute(stmt,p)));if(!pending.length)break;}
      return pending.map(p=>({...p,unchecked:outer}));
    }
    if(n.nodeType==='IfStatement')return bounded(value(n.condition,s).flatMap(row=>choose(row,n,'if').flatMap(branch=>execute(branch.expected?n.trueBody:n.falseBody,branch.s))));
    if(n.nodeType==='VariableDeclarationStatement') {
      const ds=n.declarations||[];
      if(!ds.length||ds.some(d=>d&&(d.storageLocation==='storage'||!scalar(d))))fail('INTERNAL_RETURN_LOCAL_TYPE_UNSUPPORTED');
      const rows=n.initialValue?tupleValues(n.initialValue,ds.length,s):[{s,keys:ds.map(d=>d?defaultValue(d,s):null),known:ds.map(()=>0n)}];
      return rows.map(row=>{for(const [i,d]of ds.entries())if(d)bind(d,row.keys[i],row.known[i],row.s,row.ranges?.[i]);return row.s;});
    }
    if(n.nodeType==='Return') {
      const rows=n.expression?tupleValues(n.expression,output.length,s):[{s,keys:output.map(d=>s.parameter_expression_bindings[d.id])}];
      for(const row of rows)returned(row.keys,row.s);return [];
    }
    if(n.nodeType==='InlineAssembly') {
      // Solidity can use scratch and ABI memory before any assembly block.
      // A fresh symbolic entry prevents invented zero bytes or propagation
      // across high-level compiler memory effects between separate blocks.
      const states=execute(lowerYul(n,{nodes,typeOf}),{...s,yul_memory:[]});
      return states.map(row=>({...row,yul_memory:[]}));
    }
    if(['ForStatement','WhileStatement','DoWhileStatement'].includes(n.nodeType)) {
      const proved=context.loop_summaries?.get(n.id);
      if(proved?.complete&&['ForStatement','WhileStatement'].includes(n.nodeType)) {
        let descriptor;try{descriptor=detectLoopRecurrences(n,nodes,{unchecked:s.unchecked,
          bindings:new Map(Object.entries(s.compiletime_bindings).map(([key,value])=>[/^\d+$/.test(key)?Number(key):key,value]))});}catch{}
        if(descriptor&&descriptor.recurrences.every(r=>r.total_increment||r.polynomial_summary)) {
          let rows=n.initializationExpression?execute(n.initializationExpression,s):[s];
          rows=rows.flatMap(p=>value(descriptor.bound,p).map(bound=>{
            const state=bound.s;
            if(descriptor.coupled){
              const entry=fork(state,'coupled-entry'),makeValue=(r,countKey,context,checks=[])=>{
                const poly=r.coupled_state_polynomial,operands=[countKey],indices=new Map();
                for(const leaf of poly.leaves){
                  const key=leaf.entry_declaration?entry.parameter_expression_bindings[leaf.entry_declaration]:build(leaf.node,entry);
                  if(!key)fail('INTERNAL_RETURN_COUPLED_ENTRY_UNBOUND');indices.set(leaf.key,operands.length);operands.push(key);
                }
                const checkIndices=checks.map(key=>{const i=operands.length;operands.push(key);return i;}),before=entry.parameter_expression_bindings[r.declaration];
                const beforeIndex=operands.length;operands.push(before);
                return synthetic(nodes.get(r.declaration),'coupled_polynomial_value',operands,context,{type:'uint'+r.width,width:r.width,signed:false,arithmetic:'checked',
                  polynomial_terms:poly.terms.map(t=>({...t,factor_operands:t.factors.map(f=>indices.get(f))})),coefficient_scale:poly.coefficient_scale,
                  last_iteration_check_operands:checkIndices,entry_value_operand:beforeIndex,empty_loop_is_entry:checks.length>0,
                  loop_model_id:proved.model_id,proof_obligation_ids:proved.obligations,scope:'all_successful_loop_iterations'});
              };
              const one=synthetic(n,'literal',[],entry,{value:'1',type:'uint'+descriptor.counter_width}),lastCount=synthetic(n,'binary',[bound.key,one],entry,{operator:'-',type:'uint'+descriptor.counter_width}),last=fork(entry,'coupled-last-iteration');
              bind(nodes.get(descriptor.counter),lastCount,null,last);
              for(const r of descriptor.recurrences)bind(nodes.get(r.declaration),makeValue(r,lastCount,last),null,last);
              const firstStep=last.execution_steps.length;let final=execute(n.body,last);if(n.loopExpression)final=final.flatMap(row=>execute(n.loopExpression,row));
              if(final.length!==1||final[0].control||final[0].execution_steps.slice(firstStep).some(s=>s.kind!=='evaluate'))fail('INTERNAL_RETURN_COUPLED_LAST_ITERATION_CONTROL');
              const checks=final[0].execution_steps.slice(firstStep).map(s=>s.expression_id);if(!checks.length)fail('INTERNAL_RETURN_COUPLED_LAST_ITERATION_EMPTY');
              for(const r of descriptor.recurrences){const key=makeValue(r,bound.key,state,checks);state.execution_steps.push({kind:'evaluate',expression_id:key});bind(nodes.get(r.declaration),key,null,state);}
              bind(nodes.get(descriptor.counter),bound.key,bound.known,state);return state;
            }
            for(const r of descriptor.recurrences) {
              const d=nodes.get(r.declaration),before=state.parameter_expression_bindings[r.declaration];
              if(!before)fail('INTERNAL_RETURN_INDUCTION_INITIAL_VALUE');
              let key;
              const common={operator:r.operator,type:typeOf(d),width:r.width,signed:false,arithmetic:r.unchecked?'wrap':'checked',
                loop_model_id:proved.model_id,proof_obligation_ids:proved.obligations,scope:'all_successful_loop_iterations'};
              if(r.polynomial_summary){
                const operands=[before,bound.key],factorIndices=new Map();
                for(const leaf of r.polynomial.leaves){factorIndices.set(leaf.key,operands.length);operands.push(build(leaf.node,state));}
                const one=synthetic(n,'literal',[],state,{value:'1',type:'uint'+descriptor.counter_width});
                const last=synthetic(n,'binary',[bound.key,one],state,{operator:'-',type:'uint'+descriptor.counter_width});
                const lastState=fork(state,'last-polynomial-increment');lastState.unchecked=r.unchecked;
                lastState.parameter_expression_bindings[descriptor.counter]=last;
                delete lastState.compiletime_bindings[descriptor.counter];delete lastState.range_bindings[descriptor.counter];
                const incrementOperand=operands.length;operands.push(build(r.increment,lastState));
                key=synthetic(d,'inductive_polynomial_loop',operands,state,{...common,summation_scale:r.polynomial.scale,
                  summation_terms:r.polynomial.terms.map(t=>({...t,factor_operands:t.factors.map(f=>factorIndices.get(f))})),
                  last_increment_operand:incrementOperand,checked_increment_monotone:r.polynomial.checked_monotone,
                  polynomial_certificate:proved.polynomial_certificates.find(c=>c.accumulator_ast_id===r.declaration)});
              }else{
                const increments=value(r.increment,state);if(increments.length!==1)fail('INTERNAL_RETURN_INDUCTION_INCREMENT_BRANCH');
                key=synthetic(d,'inductive_affine_loop',[before,bound.key,increments[0].key],state,common);
              }
              state.execution_steps.push({kind:'evaluate',expression_id:key});bind(d,key,null,state);
            }
            bind(nodes.get(descriptor.counter),bound.key,bound.known,state);
            return state;
          }));
          loops.set(n.id,{ast_id:n.id,iterations:null,bound_basis:descriptor.recurrences.some(r=>r.polynomial_summary)?'proved_inductive_polynomial_summary':'proved_inductive_affine_summary',dynamic_bound:true,inductive:true,
            model_id:proved.model_id,proof_obligation_ids:proved.obligations,complete:true,remaining_paths:0});
          return rows;
        }
      }
      let pending=n.initializationExpression?execute(n.initializationExpression,s):[s],finished=[],iteration=0,dynamic=false,truncated=false;
      do {
        const active=[];
        for(const p of pending) {
          const tests=n.nodeType==='DoWhileStatement'&&iteration===0?[{s:p,known:1n}]:n.condition?value(n.condition,p):[{s:p,known:1n}];
          for(const t of tests){
            if(t.known===null)dynamic=true;
            for(const branch of choose(t,n.condition||n,'loop-test')){
              // Range reasoning can discharge a repeated comparison, but the
              // symbolic exit condition remains part of the saved domain.
              if(t.known!==null&&t.key)guard(branch.s,t.key,branch.expected);
              if(!branch.expected)finished.push(branch.s);else active.push(branch.s);
            }
          }
        }
        if(!active.length)break;
        if(iteration>=16||++loopIterations>64){
          if(!dynamic)fail('INTERNAL_RETURN_LOOP_ITERATION_BUDGET');
          truncated=true;
          frontier.push(...active.map(p=>({kind:'dynamic_loop_remainder',ast_id:n.id,iterations:iteration,
            conditions:p.return_conditions,execution_steps:p.execution_steps,reason:'INTERNAL_RETURN_CONTROL_FLOW_UNSUPPORTED:LOOP_DYNAMIC_BOUND'})));
          break;
        }
        pending=[];
        for(const p of active)for(const q of execute(n.body,fork(p,'loop:'+n.id+':'+iteration))) {
          if(q.control==='break'){delete q.control;finished.push(q);continue;}
          if(q.control==='continue')delete q.control;
          pending.push(...(n.loopExpression?execute(n.loopExpression,q):[q]));
        }
        iteration++;bounded([...pending,...finished]);
      }while(pending.length);
      const prior=loops.get(n.id),hasDynamic=dynamic||prior?.dynamic_bound,remainingPaths=activeFrontier(n.id);
      loops.set(n.id,{ast_id:n.id,iterations:Math.max(prior?.iterations||0,iteration),
        bound_basis:hasDynamic?'symbolic_exit_conditions_and_integer_ranges':'compiletime_values_and_local_counter',
        dynamic_bound:hasDynamic,complete:remainingPaths===0&&prior?.complete!==false&&!truncated,remaining_paths:remainingPaths});return finished;
    }
    if(['Break','Continue'].includes(n.nodeType)){s.control=n.nodeType.toLowerCase();return [s];}
    if(n.nodeType==='RevertStatement')return [];
    if(n.nodeType==='ExpressionStatement') {
      const e=n.expression,name=e?.expression?.name;
      if(e?.nodeType==='Assignment'&&e.leftHandSide?.nodeType==='TupleExpression') {
        if(e.operator!=='=')fail('INTERNAL_RETURN_TUPLE_COMPOUND_ASSIGNMENT');
        const lhs=e.leftHandSide.components,ds=lhs.map(p=>p?local(p,s):null);
        if(new Set(ds.filter(Boolean).map(d=>d.id)).size!==ds.filter(Boolean).length)fail('INTERNAL_RETURN_DUPLICATE_TUPLE_TARGET');
        return tupleValues(e.rightHandSide,lhs.length,s).map(row=>{for(const [i,d]of ds.entries())if(d)bind(d,row.keys[i],row.known[i],row.s);return row.s;});
      }
      if(e?.nodeType==='FunctionCall'&&['require','assert'].some(n=>builtin(e.expression,n,nodes))) {
        independent(e.arguments);
        if(e.arguments.slice(1).some(a=>a.nodeType!=='Literal'))fail('INTERNAL_RETURN_REQUIRE_MESSAGE_EFFECT_UNSUPPORTED');
        return value(e.arguments[0],s).flatMap(row=>row.known===0n||!assume(e.arguments[0],true,row.s)?[]:[guard(row.s,row.key,true,name==='assert'?'panic':'revert')]);
      }
      if(e?.nodeType==='FunctionCall'&&builtin(e.expression,'revert',nodes))return [];
      const called=e?.nodeType==='FunctionCall'?nodes.get(e.expression?.referencedDeclaration):null;
      if(internalDispatch(called,e?.expression,nodes)&&['pure','view'].includes(called.stateMutability)&&!called.returnParameters?.parameters?.length&&!called.body.statements?.length&&!called.modifiers?.length&&called.parameters?.parameters?.length===e.arguments.length){
        for(const argument of e.arguments)validate(argument);
        return evaluateList(e.arguments,s).map(row=>{for(const key of row.keys)row.s.execution_steps.push({kind:'evaluate',expression_id:key});return row.s;});
      }
      if(e?.nodeType)return value(e,s).map(row=>row.s);
    }
    fail('INTERNAL_RETURN_CONTROL_FLOW_UNSUPPORTED:'+n.nodeType);
  }
  function activeFrontier(astID){return frontier.filter(f=>f.ast_id===astID).length;}
  let starts=[initial];
  for(const invocation of target.modifiers||[]) {
    const modifier=nodes.get(invocation.modifierName?.referencedDeclaration),statements=modifier?.body?.statements;
    if(!statements||statements.filter(n=>n.nodeType==='PlaceholderStatement').length!==1||statements.at(-1)?.nodeType!=='PlaceholderStatement')fail('INTERNAL_RETURN_MODIFIER_UNSUPPORTED');
    const parameters=modifier.parameters?.parameters||[];
    independent(invocation.arguments||[]);
    starts=starts.flatMap(p=>{
      let pending=[{s:p,keys:[],known:[]}];
      for(const a of invocation.arguments||[])pending=bounded(pending.flatMap(row=>value(a,row.s).map(r=>({s:r.s,keys:[...row.keys,r.key],known:[...row.known,r.known]}))));
      return pending.map(row=>{for(const [i,d]of parameters.entries()){bind(d,row.keys[i],row.known[i],row.s);row.s.parameter_arguments[d.id]=invocation.arguments[i];}return row.s;});
    });
    for(const stmt of statements.slice(0,-1)){starts=bounded(starts.flatMap(p=>execute(stmt,p)));if(results.length)fail('INTERNAL_RETURN_MODIFIER_UNSUPPORTED');}
  }
  const remaining=starts.flatMap(p=>execute(target.body,p));
  for(const s of remaining) {
    if(output.some(d=>!d.name||!s.parameter_expression_bindings[d.id]))fail('INTERNAL_RETURN_FALLTHROUGH_UNSUPPORTED');
    returned(output.map(d=>s.parameter_expression_bindings[d.id]),s);
  }
  if(!results.length)fail('INTERNAL_RETURN_NO_SUCCESSFUL_PATH');
  const unresolved_model_reasons=[...new Set(frontier.map(f=>f.reason))];
  const output_expression_ids=output.map((d,i)=>{const paths=results.map(r=>({...r,expression_id:r.expression_ids[i]}));return synthetic(call,'return_cases',paths.map(r=>r.expression_id),initial,{paths,type:typeOf(d),unresolved_model_reasons});});
  return {expression_id:output.length===1?output_expression_ids[0]:synthetic(call,'tuple',output_expression_ids,initial,{type:'tuple('+output.map(typeOf).join(',')+')'}),
    output_expression_ids,paths:results,loops:[...loops.values()],frontier,memory_effects:memoryEffects,unresolved:unresolved_model_reasons,scope:output.length===1?'bounded_scalar_readonly_return_paths':'bounded_scalar_tuple_readonly_return_paths',ordered_evaluations_preserved:true,all_execution_paths_verified:false};
}
