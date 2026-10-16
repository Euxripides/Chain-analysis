import test from'node:test';import assert from'node:assert/strict';
import{deriveCoupledRecurrences,evaluateCoupledPolynomial}from'../verification/coupled-polynomials.mjs';
const leaf=id=>({key:'declaration:'+id,node:{nodeType:'Identifier',referencedDeclaration:id,name:String(id),typeDescriptions:{typeString:'uint256'}},width:256});
function recurrence(id,factors,coefficient='1',degree=0){
 const refs=[...new Set(factors)].map(leaf);return{declaration:id,name:'a'+id,width:256,operator:'+',unchecked:false,
  polynomial:{degree,scale:'12',terms:[{degree,coefficient,factors:refs.length?factors.map(id=>'declaration:'+id):[]}],leaves:refs,operations:[],checked_monotone:true}};
}
function values(model,n,entry,constant){return model.recurrences.map(r=>evaluateCoupledPolynomial(r.coupled_state_polynomial,n,key=>key.startsWith('entry:')?entry[Number(key.slice(6))]:constant[Number(key.slice(12))]));}
test('coupled polynomial derivation preserves before/after update order and nonzero entry values',()=>{
 for(const forward of[false,true]){
  const input=[recurrence(1,[99]),recurrence(2,[1])];if(!forward)input.reverse();const model=deriveCoupledRecurrences(input),actual={1:7n,2:11n},entry={...actual};
  for(let n=0n;n<256n;n++){
   assert.deepEqual(values(model,n,entry,{99:3n}),input.map(r=>actual[r.declaration]));
   if(forward){actual[1]+=3n;actual[2]+=actual[1];}else{actual[2]+=actual[1];actual[1]+=3n;}
  }
  assert.equal(model.recurrences.find(r=>r.declaration===2).coupled_dependencies[0].read_phase,forward?'after_update':'before_update');
 }
});
test('three and four level coupled closed forms agree with independent iteration at every prefix',()=>{
 for(const order of[[1,2,3,4],[4,2,1,3]]){
  const byID=new Map([recurrence(1,[99]),recurrence(2,[1]),recurrence(3,[2]),recurrence(4,[3])].map(r=>[r.declaration,r]));
  const input=order.map(id=>byID.get(id)),model=deriveCoupledRecurrences(input),entry={1:2n,2:3n,3:5n,4:7n},actual={...entry};
  for(let n=0n;n<128n;n++){
   assert.deepEqual(values(model,n,entry,{99:11n}),order.map(id=>actual[id]));
   for(const id of order)actual[id]+=id===1?11n:actual[id-1];
  }
  assert.ok(model.recurrences.some(r=>BigInt(r.coupled_state_polynomial.coefficient_scale)>1n));
 }
});
test('nonlinear dependency products retain exact integer polynomials and word-ring congruence',()=>{
 const input=[recurrence(1,[99]),recurrence(2,[1,1])];for(const r of input){r.unchecked=true;r.polynomial.checked_monotone=false;}const model=deriveCoupledRecurrences(input),M=1n<<256n;
 const initial={1:M-3n,2:9n},actual={...initial};for(let n=0n;n<128n;n++){
  const closed=values(model,n,initial,{99:5n});assert.deepEqual(closed.map(v=>(v%M+M)%M),[actual[1],actual[2]]);
  actual[1]=(actual[1]+5n)%M;actual[2]=(actual[2]+actual[1]*actual[1])%M;
 }
});
test('feedback, incompatible widths, excess degree and unsupported arithmetic stay explicit',()=>{
 for(const mode of['cycle','self','width','arithmetic','degree','missing']){
  const input=[recurrence(1,[99]),recurrence(2,[1])];
  if(mode==='cycle')input[0]=recurrence(1,[2]);if(mode==='self')input[0]=recurrence(1,[1]);if(mode==='width')input[1].width=128;
  if(mode==='arithmetic')input[1].unchecked=true;if(mode==='degree')input[1]=recurrence(2,[1,1,1,1]);if(mode==='missing')delete input[1].polynomial;
  assert.throws(()=>deriveCoupledRecurrences(input),/LOOP_COUPLED_/,mode);
 }
});
