import{SUM_SCALE,polynomialSMT}from'./polynomials.mjs';
import{and,number,obligation}from'./solver.mjs';

const sums=[[0n,12n],[0n,-6n,6n],[0n,2n,-6n,4n],[0n,0n,3n,-6n,3n]];
const gcd=(a,b)=>{a=a<0n?-a:a;b=b<0n?-b:b;while(b){[a,b]=[b,a%b];}return a;};
const lcm=(a,b)=>a/gcd(a,b)*b;
function rational(n,d=1n){if(d<=0n)throw Error('LOOP_COUPLED_INVALID_DENOMINATOR');const g=gcd(n,d);return{n:n/g,d:d/g};}
const multiply=(a,b)=>rational(a.n*b.n,a.d*b.d),plus=(a,b)=>rational(a.n*b.d+b.n*a.d,a.d*b.d);
const term=(degree,factors=[],coefficient=rational(1n))=>({degree,factors:[...factors].sort(),coefficient});
function canonical(rows){
 const map=new Map();for(const row of rows){
  if(row.degree>4||row.degree<0||row.factors.length>16)throw Error('LOOP_COUPLED_DEGREE_OR_FACTOR_BUDGET');
  const key=row.degree+':'+row.factors.join(','),prior=map.get(key);
  if(prior)prior.coefficient=plus(prior.coefficient,row.coefficient);else map.set(key,{...row,factors:[...row.factors]});
 }
 const out=[...map.values()].filter(r=>r.coefficient.n!==0n).sort((a,b)=>a.degree-b.degree||a.factors.join(',').localeCompare(b.factors.join(',')));
 if(out.length>48||out.some(t=>t.coefficient.n.toString(2).length>2048||t.coefficient.d.toString(2).length>2048))throw Error('LOOP_COUPLED_COEFFICIENT_BUDGET');return out;
}
function product(a,b){if(a.length*b.length>512)throw Error('LOOP_COUPLED_PRODUCT_BUDGET');return canonical(a.flatMap(x=>b.map(y=>term(x.degree+y.degree,[...x.factors,...y.factors],multiply(x.coefficient,y.coefficient)))));}
function choose(n,k){let v=1n;for(let i=1;i<=k;i++)v=v*BigInt(n-i+1)/BigInt(i);return v;}
function shift(poly,phase){
 if(!phase)return poly;
 return canonical(poly.flatMap(t=>Array.from({length:t.degree+1},(_,k)=>term(k,t.factors,multiply(t.coefficient,rational(choose(t.degree,k)))))));
}
function sum(poly){return canonical(poly.flatMap(t=>{
 const row=sums[t.degree];if(!row)throw Error('LOOP_COUPLED_INCREMENT_DEGREE_BUDGET');
 return row.map((n,degree)=>term(degree,t.factors,multiply(t.coefficient,rational(n,SUM_SCALE))));
}));}
function encode(poly,leaves){
 let scale=1n;for(const t of poly)scale=lcm(scale,t.coefficient.d);
 const terms=poly.map(t=>({degree:t.degree,coefficient:String(t.coefficient.n*(scale/t.coefficient.d)),factors:t.factors}));
 const keys=new Set(terms.flatMap(t=>t.factors));if(keys.size>16)throw Error('LOOP_COUPLED_LEAF_BUDGET');
 return{degree:Math.max(0,...terms.map(t=>t.degree)),coefficient_scale:String(scale),terms,leaves:[...keys].sort().map(k=>leaves.get(k)||(()=>{throw Error('LOOP_COUPLED_FACTOR_UNBOUND');})())};
}

// Dependency edges use compiler declaration IDs. A source read before another
// update uses its entry-of-iteration value; a later read uses that update's
// result. Cycles require a different invariant family and are not summarized.
export function deriveCoupledRecurrences(recurrences){
 if(recurrences.length>4)throw Error('LOOP_COUPLED_ACCUMULATOR_BUDGET');
 const byID=new Map(recurrences.map((r,i)=>[r.declaration,{...r,source_index:i}])),changed=new Set(byID.keys());
 if(byID.size!==recurrences.length)throw Error('LOOP_COUPLED_REPEATED_TARGET');
 const leaves=new Map(),dependencies=new Map(),summaries=new Map(),resolved=new Map(),visiting=new Set(),order=[];
 for(const r of byID.values()){
  if(!r.polynomial)throw Error('LOOP_COUPLED_POLYNOMIAL_REQUIRED');
  if(r.operator!=='+'||r.unchecked!==recurrences[0].unchecked||r.width!==recurrences[0].width)throw Error('LOOP_COUPLED_ARITHMETIC_OR_WIDTH_UNSUPPORTED');
  const deps=new Set();for(const leaf of r.polynomial.leaves){
   const key=leaf.node.referencedDeclaration;if(changed.has(key))deps.add(key);else leaves.set(leaf.key,leaf);
  }dependencies.set(r.declaration,deps);
  const key='entry:'+r.declaration;leaves.set(key,{key,node:{nodeType:'Identifier',name:r.name+'@entry',referencedDeclaration:r.declaration,typeDescriptions:{typeString:'uint'+r.width}},width:r.width,entry_declaration:r.declaration});
 }
 function derive(id){
  if(resolved.has(id))return;
  if(visiting.has(id))throw Error('LOOP_COUPLED_CYCLIC_RECURRENCE');visiting.add(id);
  const r=byID.get(id);for(const dep of dependencies.get(id))derive(dep);
  const refs=new Map(r.polynomial.leaves.map(l=>[l.key,l]));let increment=[];
  for(const source of r.polynomial.terms){
   let expanded=[term(source.degree,[],rational(BigInt(source.coefficient)))];
   for(const factor of source.factors){
    const leaf=refs.get(factor),dep=leaf?.node.referencedDeclaration;
    const replacement=changed.has(dep)?shift(summaries.get(dep),byID.get(dep).source_index<r.source_index?1:0):[term(0,[factor])];
    expanded=product(expanded,replacement);
   }increment=canonical([...increment,...expanded]);
  }
  if(increment.some(t=>t.degree>3))throw Error('LOOP_COUPLED_INCREMENT_DEGREE_BUDGET');
  const summary=canonical([term(0,['entry:'+id]),...sum(increment)]),encoded=encode(increment,leaves),state=encode(summary,leaves);
  const deps=[...dependencies.get(id)].map(declaration=>({declaration,source_index:byID.get(declaration).source_index,read_phase:byID.get(declaration).source_index<r.source_index?'after_update':'before_update'}));
  const polynomial={...r.polynomial,...encoded,scale:String(SUM_SCALE*BigInt(encoded.coefficient_scale)),
   coupled:true,coupled_dependencies:deps,source_polynomial:r.polynomial};
  resolved.set(id,{...r,polynomial,polynomial_summary:true,coupled:true,coupled_dependencies:deps,coupled_state_polynomial:state});
  summaries.set(id,summary);order.push(id);visiting.delete(id);
 }
 for(const r of byID.values())derive(r.declaration);
 return {schema_version:'coupled-polynomial-recurrence-v1',derivation_order:order,
  recurrences:recurrences.map(r=>resolved.get(r.declaration)),entry_state_scope:'before_first_iteration',max_accumulators:4,max_increment_degree:3};
}

export function evaluateCoupledPolynomial(polynomial,n,factor){
 const numerator=polynomial.terms.reduce((value,t)=>value+BigInt(t.coefficient)*n**BigInt(t.degree)*t.factors.reduce((v,k)=>v*BigInt(factor(k)),1n),0n),d=BigInt(polynomial.coefficient_scale);
 if(numerator%d!==0n)throw Error('LOOP_COUPLED_NONINTEGRAL_VALUE');return numerator/d;
}

export function coupledPolynomialSMT(polynomial,n,factor){
 const numerator=polynomialSMT(polynomial,n,factor);
 return polynomial.coefficient_scale==='1'?numerator:'(div '+numerator+' '+polynomial.coefficient_scale+')';
}

export function addCoupledRefinementObligations(model,descriptor,queries,counterDomain,counterDeclarations,counterInputs){
 const byID=new Map(descriptor.recurrences.map(r=>[r.declaration,r])),leaves=new Map();
 for(const r of byID.values()){
  for(const l of [...r.polynomial.leaves,...r.coupled_state_polynomial.leaves])leaves.set(l.key,l);
  for(const l of r.polynomial.source_polynomial.leaves)if(!byID.has(l.node.referencedDeclaration))leaves.set(l.key,l);
 }
 if(leaves.size>32)throw Error('LOOP_COUPLED_REFINEMENT_LEAF_BUDGET');
 const symbols=new Map([...leaves.keys()].sort().map((key,i)=>[key,'coupled_input_'+i])),declarations=counterDeclarations+'\n'+[...symbols.values()].map(s=>'(declare-const '+s+' Int)').join('\n'),
  inputs=[...counterInputs,...[...symbols].map(([key,name])=>({name,expression:leaves.get(key).node.name||key,type:'uint'+leaves.get(key).width}))],
  domain=and([counterDomain,'(< i n)',...[...symbols].flatMap(([key,name])=>['(<= 0 '+name+')','(<= '+name+' '+((1n<<BigInt(leaves.get(key).width))-1n)+')'])]);
 const certificates=[];
 for(const r of byID.values()){
  const stateNames=new Map(r.coupled_dependencies.map(d=>[d.declaration,'coupled_read_'+d.declaration]));
  const decl=declarations+'\n'+[...stateNames.values()].map(s=>'(declare-const '+s+' Int)').join('\n'),conditions=[domain];
  const wordMod=1n<<BigInt(r.width);
  for(const d of r.coupled_dependencies){
   const p=byID.get(d.declaration).coupled_state_polynomial,name=stateNames.get(d.declaration),at=d.read_phase==='after_update'?'(+ i 1)':'i',
    expression=polynomialSMT(p,at,key=>symbols.get(key));
   conditions.push(r.unchecked?'(= '+name+' (mod '+expression+' '+wordMod+'))':' (= (* '+p.coefficient_scale+' '+name+') '+expression+')');
   conditions.push('(<= 0 '+name+')','(<= '+name+' '+(wordMod-1n)+')');
  }
  const original=r.polynomial.source_polynomial,source=polynomialSMT(original,'i',key=>{
   const leaf=original.leaves.find(l=>l.key===key),dep=leaf.node.referencedDeclaration;
   return stateNames.has(dep)?stateNames.get(dep):symbols.get(key);
  }),numerator=polynomialSMT(r.polynomial,'i',key=>symbols.get(key)),tag=r.declaration;
  const readInputs=[...inputs,...[...stateNames].map(([id,name])=>({name,expression:'iteration read of '+byID.get(id).name,type:'uint'+r.width}))];
  const violation=r.unchecked?'(distinct (mod '+numerator+' (* '+r.polynomial.coefficient_scale+' '+wordMod+')) (mod (* '+r.polynomial.coefficient_scale+' '+source+') (* '+r.polynomial.coefficient_scale+' '+wordMod+')))'
   :'(distinct '+numerator+' (* '+r.polynomial.coefficient_scale+' '+source+'))';
  const q=obligation(model,'coupled_source_increment_refinement_'+tag,decl,and(conditions),violation,readInputs);queries.push(q);
  const d=r.polynomial.coefficient_scale,g=obligation(model,'coupled_source_integrality_'+tag,'(declare-const x Int)\n(declare-const p Int)',
   '(= x (* '+d+' p))','(distinct (mod x '+d+') 0)',[{name:'x',kind:'proof_ghost_integer'},{name:'p',kind:'proof_ghost_integer'}]);queries.push(g);
  certificates.push({schema_version:'coupled-source-refinement-v1',accumulator_ast_id:tag,increment_ast_id:r.increment?.id,
   dependencies:r.coupled_dependencies,coefficient_scale:r.polynomial.coefficient_scale,state_polynomial:r.coupled_state_polynomial,
   proof_obligation_ids:[q.property.property_id,g.property.property_id],derivation_order:descriptor.coupled.derivation_order,
   arithmetic:r.unchecked?'unchecked_word_ring':'checked_monotone',scope:'source_reads_at_their_actual_update_position'});
 }
 model.coupled_certificates=certificates;
}
