import {number,and} from './solver.mjs';
export const WORD=1n<<256n,MAX_WORD=WORD-1n,ARRAY='(Array Int Int)';
export function scalarType(node) {
 const text=node?.typeDescriptions?.typeString||node?.name||node?.type||'';
 if(text==='bool')return {sort:'Bool',type:'bool'};
 const integer=/^(u?int)(\d*)$/.exec(text),bytes=/^bytes(\d+)$/.exec(text);
 const bits=integer?Number(integer[2]||256):/^(address|contract )/.test(text)?160:/^enum\s/.test(text)?8:bytes?Number(bytes[1])*8:null;
 if(!bits)throw new Error('STATE_SCALAR_TYPE_UNSUPPORTED:'+text);
 const signed=integer?.[1]==='int',mod=1n<<BigInt(bits);
 return {sort:'Int',type:text,bits,signed,min:signed?-(mod>>1n):0n,max:signed?(mod>>1n)-1n:mod-1n,mod};
}
export const asBool=v=>v.sort==='Bool'?v.term:'(not (= '+v.term+' 0))';
export const asInt=v=>v.sort==='Bool'?'(ite '+v.term+' 1 0)':v.term;
export const mod=(term,width=WORD)=>'(mod '+term+' '+width+')';

export class Terms {
 constructor(){this.declarations=new Map();this.definitions=[];this.cache=new Map();this.constraints=new Set();this.inputs=[];this.hashes=[];this.reserved=new Set();this.serial=0;}
 variable(name,type,description=name){
  const sort=typeof type==='string'?type:type.sort;
  if(this.declarations.has(name)&&this.declarations.get(name)!==sort)throw new Error('STATE_SYMBOL_TYPE_CONFLICT');
  if(!this.declarations.has(name)){
   this.declarations.set(name,sort);if(sort==='Int'&&type.min!==undefined)this.constraints.add(and(['(<= '+number(type.min)+' '+name+')','(<= '+name+' '+number(type.max)+')']));
   if(sort==='Int'||sort==='Bool')this.inputs.push({name,expression:description,type:type.type||sort});
  }return {term:name,...(typeof type==='string'?{sort:type}:type)};
 }
 define(term,sort='Int') {
  if(++this.serial>12000)throw new Error('STATE_TERM_BUDGET');
  const key=sort+':'+term;if(this.cache.has(key))return this.cache.get(key);
  const name='t_'+this.serial;this.definitions.push('(define-fun '+name+' () '+sort+' '+term+')');this.cache.set(key,name);return name;
 }
 value(term,type){return {...type,term:this.define(term,type.sort)};}
 observe(term,description,metadata={}) {
  if(!this.inputs.some(v=>v.name===term))this.inputs.push({name:term,expression:description,type:'uint256',...metadata});
 }
 normalize(term,type){
  if(type.sort==='Bool')return term;
  const value=mod(term,type.mod);return type.signed?'(ite (>= '+value+' '+(type.mod>>1n)+') (- '+value+' '+type.mod+') '+value+')':value;
 }
 reserveDerivedField(slot){
  if(this.reserved.has(slot))return;this.reserved.add(slot);
  for(const prior of this.hashes)this.constraints.add('(distinct '+prior.term+' '+slot+')');
 }
 digestWords(parts) {
  const arity=parts.length,name='keccak_'+arity;
  if(!this.declarations.has(name))this.declarations.set(name,{function:true,args:parts.map(()=>'Int'),result:'Int'});
  const term=this.define('('+name+' '+parts.join(' ')+')');this.constraints.add('(and (<= 0 '+term+') (<= '+term+' '+MAX_WORD+'))');
  return term;
 }
 hash(parts,staticSlots=[]) {
  const known=this.hashes.find(h=>h.parts.length===parts.length&&h.parts.every((p,i)=>p===parts[i]));if(known)return known.term;
  const term=this.digestWords(parts);
  for(const prior of this.hashes) {
   const distinct=prior.parts.length===parts.length?'(or '+parts.map((p,i)=>'(distinct '+p+' '+prior.parts[i]+')').join(' ')+')':'true';
   this.constraints.add('(=> '+distinct+' (distinct '+term+' '+prior.term+'))');
  }
  for(const slot of staticSlots)this.constraints.add('(distinct '+term+' '+slot+')');
  for(const slot of this.reserved)this.constraints.add('(distinct '+term+' '+slot+')');
  this.hashes.push({parts,term});return term;
 }
 smt(){return [...this.declarations].map(([name,s])=>typeof s==='object'?'(declare-fun '+name+' ('+s.args.join(' ')+') '+s.result+')':'(declare-const '+name+' '+s+')').join('\n')+'\n'+this.definitions.join('\n');}
 domain(extra=[]){return and([...this.constraints,...extra]);}
}
