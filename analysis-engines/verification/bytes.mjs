import {createRequire} from 'node:module';
import {asInt,WORD,MAX_WORD,scalarType} from './terms.mjs';
import {and} from './solver.mjs';
const require=createRequire(import.meta.url),{keccak_256}=require('@noble/hashes/sha3.js');
const {secp256k1}=require('@noble/curves/secp256k1.js');
export const SECP256K1_ORDER=0xfffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141n;
const bytes32=scalarType({type:'bytes32'});
const power=n=>1n<<BigInt(n*8),constant=s=>/^-?\d+$/.test(s)?BigInt(s):null;
export const bytesType=node=>/^(bytes|string)(?: (memory|calldata|storage)(?: ref| pointer)?)?$/.test(node?.typeDescriptions?.typeString||node?.type||'');
export function localType(node){return bytesType(node)?{sort:'Bytes',type:(node.typeDescriptions?.typeString||node.type).startsWith('string')?'string':'bytes'}:scalarType(node);}
export function literalBytes(node){
 const raw=node.hexValue!==undefined?Buffer.from(node.hexValue,'hex'):Buffer.from(node.value||'');
 return byteSequence([{term:raw.length?BigInt('0x'+raw.toString('hex')).toString():'0',length:raw.length}],{kind:'literal',hex:raw.toString('hex')});
}
export function byteSequence(segments,provenance={}){
 const parts=segments.filter(p=>p.length);let length=0;
 for(const p of parts){if(!Number.isSafeInteger(p.length)||p.length<0)throw new Error('STATE_BYTE_LENGTH_UNSUPPORTED');length+=p.length;}
 if(length>2048||parts.length>96)throw new Error('STATE_BYTE_ENCODING_BUDGET');
 return {sort:'Bytes',type:'bytes',length,segments:parts,provenance};
}
export function concatBytes(values,provenance={}){return byteSequence(values.flatMap(v=>{if(v.sort!=='Bytes')throw new Error('STATE_BYTES_VALUE_REQUIRED');return v.segments;}),provenance);}
export function bytesInteger(value){
 let remaining=value.length;const parts=value.segments.map(p=>{remaining-=p.length;return remaining?'(* '+p.term+' '+power(remaining)+')':p.term;});
 return parts.length===0?'0':parts.length===1?parts[0]:'(+ '+parts.join(' ')+')';
}
export function concreteBytes(value){
 if(value.sort!=='Bytes')return null;
 const raw=[];for(const p of value.segments){const n=constant(p.term);if(n===null||n<0n||n>=power(p.length))return null;raw.push(n.toString(16).padStart(p.length*2,'0'));}return Buffer.from(raw.join(''),'hex');
}
function byteWords(value,terms){
 const words=[];let index=0,consumed=0;
 for(let at=0;at<value.length;at+=32){
  let remaining=32;const pieces=[];
  while(remaining){
   const segment=value.segments[index],take=Math.min(remaining,segment.length-consumed),trailing=segment.length-consumed-take,n=constant(segment.term);
   let term=consumed===0&&take===segment.length?segment.term:n!==null?((n/power(trailing))%power(take)).toString():
    '(mod '+(trailing?'(div '+segment.term+' '+power(trailing)+')':segment.term)+' '+power(take)+')';
   remaining-=take;if(remaining)term='(* '+term+' '+power(remaining)+')';pieces.push(term);consumed+=take;
   if(consumed===segment.length){index++;consumed=0;}
  }
  words.push(pieces.length===1?pieces[0]:terms.define('(+ '+pieces.join(' ')+')'));
 }
 return words;
}
export function coerceBytes(value,type,terms){
 if(type.sort==='Bytes'){if(value.sort!=='Bytes')throw new Error('STATE_BYTES_CONVERSION_UNSUPPORTED');return {...value,...type};}
 if(value.sort!=='Bytes')return null;
 const match=/^bytes(\d+)$/.exec(type.type);if(!match||value.length>Number(match[1]))throw new Error('STATE_BYTES_LITERAL_CONVERSION_UNSUPPORTED');
 const raw=concreteBytes(value);if(!raw)throw new Error('STATE_BYTES_TO_FIXED_CONVERSION_UNSUPPORTED');
 return {...type,term:(BigInt('0x'+(raw.toString('hex')||'0'))*power(Number(match[1])-value.length)).toString()};
}
function scalarBytes(v,packed){
 const fixed=/^bytes(\d+)$/.exec(v.type),length=packed?(fixed?Number(fixed[1]):v.sort==='Bool'?1:(v.bits||256)/8):32;
 const n=v.sort==='Bool'&&['true','false'].includes(v.term)?v.term==='true'?1n:0n:constant(v.term),shift=fixed&&!packed?power(32-Number(fixed[1])):1n;
 const term=n!==null?(((n%power(length)+power(length))%power(length))*shift).toString():
  fixed&&!packed?'(* '+asInt(v)+' '+shift+')':v.signed?'(mod '+asInt(v)+' '+power(length)+')':asInt(v);
 return byteSequence([{term,length}],{kind:'scalar',scalar_type:v.type,value:asInt(v)});
}
// Dynamic values are supported only when their exact finite byte contents are
// already represented. Unknown calldata, arrays and structs remain gaps.
export function encodeABI(values,{packed=false}={}){
 const provenance={kind:packed?'abi.encodePacked':'abi.encode',values:values.map(v=>v.sort==='Bytes'?{type:v.type,bytes:v}:{type:v.type,term:asInt(v),hash_preimage:v.hash_preimage})};
 if(packed)return concatBytes(values.map(v=>v.sort==='Bytes'?v:scalarBytes(v,true)),provenance);
 let offset=values.length*32;const heads=[],tails=[];
 for(const value of values){
  if(value.sort!=='Bytes'){heads.push(scalarBytes(value,false));continue;}
  heads.push(byteSequence([{term:String(offset),length:32}]));
  const padding=(32-value.length%32)%32,tail=concatBytes([byteSequence([{term:String(value.length),length:32}]),value,byteSequence([{term:'0',length:padding}])]);
  tails.push(tail);offset+=tail.length;
 }
 return concatBytes([...heads,...tails],provenance);
}
export function hashBytes(value,terms){
 if(value.sort!=='Bytes')throw new Error('STATE_HASH_BYTE_PREIMAGE_UNAVAILABLE');
 const raw=concreteBytes(value);let term;
 if(raw)term=BigInt('0x'+Buffer.from(keccak_256(raw)).toString('hex')).toString();
 else if(value.length%32===0){
  // Use the same byte words as assembly keccak256. No collision or
  // injectivity assumption is introduced for general data hashing.
  term=terms.digestWords(byteWords(value,terms));
 }else{
  const name='keccak_bytes_'+value.length;if(!terms.declarations.has(name))terms.declarations.set(name,{function:true,args:['Int'],result:'Int'});
  term=terms.define('('+name+' '+bytesInteger(value)+')');terms.constraints.add(and(['(<= 0 '+term+')','(<= '+term+' '+MAX_WORD+')']));
 }
 return {...bytes32,term,hash_preimage:value};
}
export function concreteRecovery(hash,v,r,s){
 const h=BigInt(hash),vv=BigInt(v),rr=BigInt(r),ss=BigInt(s);
 if(h<0n||h>=WORD||![27n,28n].includes(vv)||rr<=0n||rr>=SECP256K1_ORDER||ss<=0n||ss>=SECP256K1_ORDER)return 0n;
 try{
  const signature=Buffer.from(Number(vv-27n).toString(16).padStart(2,'0')+rr.toString(16).padStart(64,'0')+ss.toString(16).padStart(64,'0'),'hex');
  const pub=secp256k1.Point.fromBytes(secp256k1.recoverPublicKey(signature,Buffer.from(h.toString(16).padStart(64,'0'),'hex'),{prehash:false})).toBytes(false);
  return BigInt('0x'+Buffer.from(keccak_256(pub.subarray(1))).subarray(12).toString('hex'));
 }catch{return 0n;}
}
export function recoverSigner(args,terms){
 if(args.length!==4||args.some(a=>a.sort==='Bytes'))throw new Error('STATE_ECRECOVER_ARGUMENT_UNSUPPORTED');
 const values=args.map(asInt),numbers=values.map(constant),address=scalarType({type:'address'});let term;
 if(numbers.every(n=>n!==null))term=concreteRecovery(...numbers).toString();
 else{
  const name='evm_ecrecover';if(!terms.declarations.has(name))terms.declarations.set(name,{function:true,args:['Int','Int','Int','Int'],result:'Int'});
  const [h,v,r,s]=values,valid=and(['(or (= '+v+' 27) (= '+v+' 28))','(< 0 '+r+')','(< '+r+' '+SECP256K1_ORDER+')','(< 0 '+s+')','(< '+s+' '+SECP256K1_ORDER+')']);
  term=terms.define('(ite '+valid+' ('+name+' '+[h,v,r,s].join(' ')+') 0)');
  terms.constraints.add(and(['(<= 0 '+term+')','(<= '+term+' '+address.max+')']));
 }
 return {...address,term,recovery:{digest:values[0],v:values[1],r:values[2],s:values[3],hash_preimage:args[0].hash_preimage,semantics:'exact_concrete_or_conservative_symbolic_evm_precompile'}};
}
