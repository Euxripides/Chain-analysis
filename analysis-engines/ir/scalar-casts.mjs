// A fixed bytes value is represented here by its N-byte unsigned integer.
// Solidity pads/truncates its right side when changing bytesN widths.
export function scalarDefaultLiteral(type){
 if(type==='bool')return'false';
 if(/^(?:address(?: payable)?|contract [^\[\]\s]+)$/.test(type||''))return'0x'+'0'.repeat(40);
 const bytes=/^bytes([1-9]|[12]\d|3[0-2])$/.exec(type||'');
 if(bytes)return'0x'+'00'.repeat(Number(bytes[1]));
 if(/^(?:u?int\d*|enum [^\[\]\s]+)$/.test(type||''))return'0';
 throw Error('SCALAR_DEFAULT_TYPE_UNSUPPORTED');
}
export function tupleComponentType(type,index){
 if(!Number.isInteger(index)||index<0||!type?.startsWith('tuple(')||!type.endsWith(')'))return null;
 const body=type.slice(6,-1),parts=[];let depth=0,start=0;
 for(let i=0;i<body.length;i++){
  const c=body[i];if(c==='('||c==='[')depth++;else if(c===')'||c===']')depth--;
  if(depth<0)return null;if(c===','&&depth===0){parts.push(body.slice(start,i).trim());start=i+1;}
 }
 if(depth!==0)return null;parts.push(body.slice(start).trim());return parts[index]||null;
}
export function scalarAssignmentConversion(from,to,info={}){
 const integerType=type=>{const match=/^(u?int)(\d*)$/.exec(type||'');if(!match)return null;const bits=Number(match[2]||256);return bits>=8&&bits<=256&&bits%8===0?{width:bits,signed:match[1]==='int'}:null;},targetInteger=integerType(to),sourceInteger=integerType(from),literal=info.literal_kind==='number'||/^(?:int|rational)_const\b/.test(from||'')||info.symbolic_format==='literal';
 if(targetInteger&&(literal||sourceInteger&&(sourceInteger.width!==targetInteger.width||sourceInteger.signed!==targetInteger.signed)))return{format:'scalar_cast',cast_source_type:from,preserve_source_type:!literal&&Boolean(sourceInteger),conversion_basis:literal?'compiler_declared_integer_literal_type':'compiler_integer_assignment_type',...targetInteger};
 const fixed=/^bytes([1-9]|[12]\d|3[0-2])$/.exec(to||'');
 if(fixed&&(['string','hexString','unicodeString'].includes(info.literal_kind)||/^literal_string\b/.test(from||''))){
  const hex=info.literal_hex_value;
  if(typeof hex!=='string'||!/^(?:[0-9a-fA-F]{2})*$/.test(hex)||hex.length>Number(fixed[1])*2)return{unresolved:'FIXED_BYTES_LITERAL_CONVERSION_UNAVAILABLE'};
  return{format:'literal',value:'0x'+hex.toLowerCase().padEnd(Number(fixed[1])*2,'0'),cast_source_type:from,conversion_basis:'compiler_literal_bytes_right_padding'};
 }
 if(fixedBytesShift(from,to))return{format:'scalar_cast',cast_source_type:from,preserve_source_type:true,conversion_basis:'compiler_fixed_bytes_width'};
 return null;
}
export function fixedBytesShift(from,to){
 const a=/^bytes([1-9]|[12]\d|3[0-2])$/.exec(from||''),b=/^bytes([1-9]|[12]\d|3[0-2])$/.exec(to||'');
 return a&&b?(Number(b[1])-Number(a[1]))*8:0;
}
export function fixedBytesTerm(term,from,to){
 const bits=fixedBytesShift(from,to);return bits>0?'(* '+term+' '+(1n<<BigInt(bits))+')':bits<0?'(div '+term+' '+(1n<<BigInt(-bits))+')':term;
}
export function fixedBytesValue(value,from,to){
 const bits=fixedBytesShift(from,to);return bits>0?value<<BigInt(bits):bits<0?value>>BigInt(-bits):value;
}
