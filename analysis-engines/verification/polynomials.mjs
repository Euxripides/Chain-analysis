import {numericLiteral} from '../literals.mjs';
import {and,number,obligation} from './solver.mjs';

export const SUM_SCALE=12n;
const SUMS=Object.freeze([['0','12'],['0','-6','6'],['0','2','-6','4'],['0','0','3','-6','3']].map(row=>Object.freeze(row)));
const width=n=>{const m=/^uint(\d*)$/.exec(n?.typeDescriptions?.typeString||'');return m?Number(m[1]||256):null;};
const mul=xs=>xs.includes('0')?'0':xs.filter(x=>x!=='1').length>1?'(* '+xs.filter(x=>x!=='1').join(' ')+')':xs.find(x=>x!=='1')||'1';
const add=xs=>xs.filter(x=>x!=='0').length>1?'(+ '+xs.filter(x=>x!=='0').join(' ')+')':xs.find(x=>x!=='0')||'0';
const power=(n,k)=>k?mul(Array(k).fill(n)):'1';
const leafKey=n=>n.nodeType==='Identifier'?'declaration:'+n.referencedDeclaration:'environment:'+n.expression.name+'.'+n.memberName;

export function counterPolynomial(node,counter,nodes,{unchecked=false,wordBits=256}={}) {
 const leaves=new Map(),operations=[];let visits=0;
 function parse(n){
  if(!n||++visits>96)throw new Error('LOOP_POLYNOMIAL_EXPRESSION_BUDGET');
  if(n.nodeType==='TupleExpression'&&n.components?.length===1&&n.components[0])return parse(n.components[0]);
  const constant=/^int_const (-?\d+)$/.exec(n.typeDescriptions?.typeString||'');
  if(constant&&['BinaryOperation','UnaryOperation'].includes(n.nodeType))return [{degree:0,coefficient:BigInt(constant[1]),factors:[]}];
  if(n.nodeType==='Literal'&&n.kind==='number'){
   const value=numericLiteral(n.value,n.subdenomination).value;
   if(value===null)throw new Error('LOOP_POLYNOMIAL_LITERAL_UNSUPPORTED');
   return [{degree:0,coefficient:BigInt(value),factors:[]}];
  }
  if(n.nodeType==='Identifier'&&n.referencedDeclaration===counter)return [{degree:1,coefficient:1n,factors:[]}];
  const environment=n.nodeType==='MemberAccess'&&n.expression?.nodeType==='Identifier'&&
   (n.expression.name==='block'&&['number','timestamp','chainid','basefee','gaslimit'].includes(n.memberName)||n.expression.name==='msg'&&n.memberName==='value');
  if(n.nodeType==='Identifier'||environment){
   const bits=width(nodes.get(n.referencedDeclaration)||n);if(!bits)throw new Error('LOOP_POLYNOMIAL_LEAF_NOT_UINT');
   const key=leafKey(n);leaves.set(key,{key,node:n,width:bits});return [{degree:0,coefficient:1n,factors:[key]}];
  }
  const unary=n.nodeType==='UnaryOperation'&&n.operator==='-';
  if(!unary&&(n.nodeType!=='BinaryOperation'||!['+','-','*'].includes(n.operator)))throw new Error('LOOP_INCREMENT_NOT_POLYNOMIAL');
  const bits=width(n);if(!bits)throw new Error('LOOP_POLYNOMIAL_OPERATION_NOT_UINT');
  if(unchecked&&bits<wordBits)throw new Error('LOOP_POLYNOMIAL_NARROW_INTERMEDIATE');
  if(!unchecked&&(unary||n.operator==='-'))throw new Error('LOOP_CHECKED_POLYNOMIAL_NOT_MONOTONE');
  operations.push({ast_id:n.id,operator:n.operator,arity:unary?1:2,width:bits});
  const left=parse(unary?n.subExpression:n.leftExpression);
  if(unary)return left.map(t=>({...t,coefficient:-t.coefficient}));
  const right=parse(n.rightExpression);
  if(n.operator!=='*')return [...left,...right.map(t=>({...t,coefficient:n.operator==='-'?-t.coefficient:t.coefficient}))];
  if(left.length*right.length>32)throw new Error('LOOP_POLYNOMIAL_TERM_BUDGET');
  return left.flatMap(a=>right.map(b=>({degree:a.degree+b.degree,coefficient:a.coefficient*b.coefficient,factors:[...a.factors,...b.factors].sort()})));
 }
 const merged=new Map();
 for(const term of parse(node)){
  if(term.degree>3||term.factors.length>16)throw new Error('LOOP_POLYNOMIAL_DEGREE_OR_FACTOR_BUDGET');
  const key=term.degree+':'+term.factors.join(',');
  const previous=merged.get(key);if(previous)previous.coefficient+=term.coefficient;else merged.set(key,{...term});
 }
 const terms=[...merged.values()].filter(t=>t.coefficient!==0n).map(t=>({...t,coefficient:String(t.coefficient)}));
 if(terms.length>16||leaves.size>16)throw new Error('LOOP_POLYNOMIAL_TERM_OR_LEAF_BUDGET');
 return {schema_version:'counter-polynomial-v1',degree:Math.max(0,...terms.map(t=>t.degree)),terms,leaves:[...leaves.values()],operations,
  scale:String(SUM_SCALE),scaled_power_sums:SUMS,checked_monotone:!unchecked};
}

export function powerSumScaledSMT(degree,n){
 const coefficients=SUMS[degree];if(!coefficients)throw new Error('POLYNOMIAL_SUM_DEGREE_UNSUPPORTED');
 return add(coefficients.map((c,k)=>mul([number(c),power(n,k)])));
}
export function polynomialSMT(polynomial,n,factor,{sum=false}={}){
 return add(polynomial.terms.map(t=>mul([number(t.coefficient),...t.factors.map(factor),sum?powerSumScaledSMT(t.degree,n):power(n,t.degree)])));
}
export function exactPowerSum(degree,n){
 if(n<0n)throw new Error('POLYNOMIAL_SUM_NEGATIVE_BOUND');
 const numerator=SUMS[degree]?.reduce((v,c,k)=>v+BigInt(c)*n**BigInt(k),0n);
 if(numerator===undefined||numerator%SUM_SCALE!==0n)throw new Error('POLYNOMIAL_SUM_CERTIFICATE_INVALID');
 return numerator/SUM_SCALE;
}

export function addPolynomialObligations(model,descriptor,recurrence,queries,counterDomain,counterDeclarations,counterInputs){
 const p=recurrence.polynomial,tag=recurrence.declaration,M=1n<<BigInt(recurrence.width),D=String(SUM_SCALE),max=String(M-1n);
 const symbols=new Map(p.leaves.map((leaf,i)=>[leaf.key,'c_'+i]));
 const inputs=[...counterInputs,...p.leaves.map(leaf=>({name:symbols.get(leaf.key),expression:leaf.node.name||leaf.key,type:'uint'+leaf.width}))];
 const declarations=counterDeclarations+'\n'+p.leaves.map(l=>'(declare-const '+symbols.get(l.key)+' Int)').join('\n');
 const domain=and([counterDomain,...p.leaves.flatMap(l=>['(<= 0 '+symbols.get(l.key)+')','(<= '+symbols.get(l.key)+' '+((1n<<BigInt(l.width))-1n)+')'])]);
 const source=n=>polynomialSMT(p,n,key=>symbols.get(key)),sum=n=>polynomialSMT(p,n,key=>symbols.get(key),{sum:true});
 const addQuery=(kind,decl,scope,violation,args=inputs)=>{const item=obligation(model,kind+'_'+tag,decl,scope,violation,args);queries.push(item);return item.property.property_id;};
 const ids=[];
 ids.push(addQuery('polynomial_sum_initialization',declarations,domain,'(distinct '+sum('0')+' 0)'));
 ids.push(addQuery('polynomial_discrete_difference',declarations,domain,'(distinct (- '+sum('(+ i 1)')+' '+sum('i')+') (* '+D+' '+source('i')+'))'));
 const ghostInputs=['a0','a','p','s','q','k','x','y','u','v'].map(name=>({name,kind:'proof_ghost_integer',expression:name}));
 const ghosts=ghostInputs.map(v=>'(declare-const '+v.name+' Int)').join('\n');
 ids.push(addQuery('polynomial_integrality_preservation',ghosts,'(= (mod s '+D+') 0)','(distinct (mod (+ s (* '+D+' p)) '+D+') 0)',ghostInputs));
 const bounds=and(['(<= 0 a0)','(<= a0 '+max+')','(<= 0 a)','(<= a '+max+')']);
 const sign=recurrence.operator,form=s=>'('+sign+' (* '+D+' a0) '+s+')',next='('+sign+' a p)';
 ids.push(addQuery('loop_accumulator_initialization',ghosts,bounds,'(distinct (* '+D+' a0) '+form('0')+')',ghostInputs));
 if(recurrence.unchecked){
  const before='(- '+form('s')+' (* '+D+' '+M+' q))',after='(- '+form('(+ s (* '+D+' p))')+' (* '+D+' '+M+' (+ q k)))';
  ids.push(addQuery('loop_accumulator_preservation',ghosts,and([bounds,'(= (* '+D+' a) '+before+')']),
   '(distinct (* '+D+' (- '+next+' (* '+M+' k))) '+after+')',ghostInputs));
  ids.push(addQuery('loop_word_quotient_reduction',ghosts,'true','(distinct (mod x '+M+') (- x (* '+M+' (div x '+M+'))))',ghostInputs));
  ids.push(addQuery('loop_word_modular_shift',ghosts,'true','(distinct (mod (+ x (* '+M+' q)) '+M+') (mod x '+M+'))',ghostInputs));
  for(const op of new Set(p.operations.filter(op=>['+','-'].includes(op.operator)).map(op=>op.operator+':'+op.arity))){
   const [operator,arity]=op.split(':'),left=arity==='1'?'(- (+ x (* '+M+' u)))':'('+operator+' (+ x (* '+M+' u)) (+ y (* '+M+' v)))';
   const right=arity==='1'?'(+ (- x) (* '+M+' (- u)))':'(+ ('+operator+' x y) (* '+M+' ('+operator+' u v)))';
   ids.push(addQuery('loop_word_additive_projection_'+(operator==='+'?'add':'sub')+'_'+arity,ghosts,'true','(distinct '+left+' '+right+')',ghostInputs));
  }
  if(p.operations.some(op=>op.operator==='*'))ids.push(addQuery('loop_word_product_projection',ghosts,'true',
   '(distinct (* (+ x (* '+M+' u)) (+ y (* '+M+' v))) (+ (* x y) (* '+M+' (+ (* x v) (* y u) (* '+M+' u v)))))',ghostInputs));
  for(const bits of new Set(p.operations.map(op=>op.width)))ids.push(addQuery('loop_word_width_projection_'+bits,ghosts,'true',
   '(distinct (mod (mod x '+(1n<<BigInt(bits))+') '+M+') (mod x '+M+'))',ghostInputs));
  ids.push(addQuery('loop_accumulator_exit',ghosts,and([bounds,'(= (mod s '+D+') 0)','(= (* '+D+' a) '+before+')']),
   '(distinct a (mod ('+sign+' a0 (div s '+D+')) '+M+'))',ghostInputs));
 }else{
  ids.push(addQuery('loop_accumulator_preservation',ghosts,and([bounds,'(= (* '+D+' a) '+form('s')+')','(<= 0 '+next+')','(<= '+next+' '+max+')']),
   '(distinct (* '+D+' '+next+') '+form('(+ s (* '+D+' p))')+')',ghostInputs));
  ids.push(addQuery('loop_accumulator_exit',ghosts,and([bounds,'(= (mod s '+D+') 0)','(= (* '+D+' a) '+form('s')+')']),
   '(distinct a ('+sign+' a0 (div s '+D+')))',ghostInputs));
 }
 model.polynomial_certificates||=[];model.polynomial_certificates.push({schema_version:'polynomial-loop-certificate-v1',accumulator_ast_id:tag,
  counter_ast_id:descriptor.counter,increment_ast_id:recurrence.increment.id,operator:sign,word_bits:recurrence.width,
  terms:p.terms,leaves:p.leaves.map(l=>({key:l.key,ast_id:l.node.id,width:l.width})),operations:p.operations,scaled_power_sums:SUMS,scale:D,
  arithmetic:recurrence.unchecked?'word_ring':'checked_monotone',proof_obligation_ids:ids,
  derivation:['exact_source_polynomial_normalization','scaled_discrete_antiderivative','integrality_induction','accumulator_induction','typed_exit_value',...(recurrence.unchecked?['word_ring_projection']:['monotone_checked_increment_maximum'])]});
 return ids;
}
