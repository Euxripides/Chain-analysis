import{id,hash,unique}from'../model.mjs';
const WORD=1n<<256n;
export class AbiFault extends Error{constructor(code,status='invalid',offset=null){super(code);this.code=code;this.status=status;this.offset=offset===null?null:String(offset);}}
const fail=(code,status='invalid',offset=null)=>{throw new AbiFault(code,status,offset);};
const parts=text=>{const out=[];let nesting=0,start=0;for(let i=0;i<text.length;i++){if('(['.includes(text[i]))nesting++;if(')]'.includes(text[i]))nesting--;if(nesting<0)fail('ABI_TYPE_SYNTAX');if(text[i]===','&&!nesting){out.push(text.slice(start,i));start=i+1;}}if(nesting)fail('ABI_TYPE_SYNTAX');if(text.length)out.push(text.slice(start));return out;};
export function parseAbiType(input,depth=0,budget={nodes:0}){
 if(depth>16||++budget.nodes>4096)fail('ABI_TYPE_DEPTH_OR_NODE_BUDGET','partial');const description=typeof input==='string'?{type:input}:input;
 if(typeof description?.type!=='string'||description.type.length>4096)fail('ABI_TYPE_INVALID');let text=description.type.trim(),dimensions=[],match;
 while((match=/\[(\d*)\]$/.exec(text))){dimensions.push(match[1]);text=text.slice(0,match.index);}
 if(depth+dimensions.length>16)fail('ABI_TYPE_DEPTH_OR_NODE_BUDGET','partial');
 let type;
 if(text==='tuple'||text.startsWith('(')&&text.endsWith(')')){
  const children=text==='tuple'?description.components:parts(text.slice(1,-1));if(!Array.isArray(children)||children.length>256)fail('ABI_TUPLE_COMPONENT_BUDGET','partial');
  const components=children.map(c=>parseAbiType(c,depth+1+dimensions.length,budget)),dynamic=components.some(c=>c.dynamic);
  type={kind:'tuple',components,canonical:'('+components.map(c=>c.canonical).join(',')+')',dynamic,static_size:dynamic?null:components.reduce((n,c)=>n+BigInt(c.static_size),0n).toString()};
 }else if((match=/^(u?int)(\d*)$/.exec(text))){const bits=Number(match[2]||256);if(bits<8||bits>256||bits%8)fail('ABI_INTEGER_WIDTH');type={kind:match[1],bits,canonical:match[1]+bits,dynamic:false,static_size:'32'};}
 else if((match=/^(u?fixed)(?:(\d+)x(\d+))?$/.exec(text))){const bits=Number(match[2]||128),decimals=Number(match[3]||18);if(bits<8||bits>256||bits%8||decimals<1||decimals>80)fail('ABI_FIXED_WIDTH');type={kind:match[1],bits,decimals,canonical:match[1]+bits+'x'+decimals,dynamic:false,static_size:'32'};}
 else if((match=/^bytes(\d+)$/.exec(text))){const length=Number(match[1]);if(length<1||length>32)fail('ABI_BYTES_WIDTH');type={kind:'fixed_bytes',length,canonical:'bytes'+length,dynamic:false,static_size:'32'};}
 else if(['address','bool','function','bytes','string'].includes(text))type={kind:text,canonical:text,dynamic:['bytes','string'].includes(text),static_size:['bytes','string'].includes(text)?null:'32'};
 else fail('ABI_TYPE_UNSUPPORTED','partial');
 for(const dimension of dimensions.reverse()){
  const length=dimension===''?null:BigInt(dimension);if(length!==null&&length>256n)fail('ABI_ARRAY_TYPE_ELEMENT_BUDGET','partial');
  const dynamic=length===null||type.dynamic;type={kind:'array',element:type,length:length===null?null:Number(length),canonical:type.canonical+'['+(length===null?'':length)+']',dynamic,static_size:dynamic?null:(length*BigInt(type.static_size)).toString()};
 }
 if(description.name)type.name=description.name;return type;
}

function inputCells(input){
 if(typeof input==='string'){if(!/^0x(?:[\da-f]{2})*$/i.test(input))fail('ABI_HEX_INVALID');input=Buffer.from(input.slice(2),'hex');}
 if(!Number.isSafeInteger(input?.length)||input.length>65536)fail('ABI_INPUT_BYTE_BUDGET','partial');
 return Array.from(input,b=>typeof b==='number'?{kind:'constant',value:b}:b).map(b=>{
  if(!b||!['constant','word_byte','source_byte','unknown'].includes(b.kind)||b.kind==='constant'&&(!Number.isInteger(b.value)||b.value<0||b.value>255)||
   b.kind==='word_byte'&&(!Number.isInteger(b.byte_index)||b.byte_index<0||b.byte_index>31||typeof b.expression!=='string'||!b.expression)||
   b.kind==='source_byte'&&(typeof b.source_id!=='string'||typeof b.source_offset!=='string')||b.kind==='unknown'&&typeof b.reason!=='string')fail('ABI_BYTE_INVALID');return b;});
}
function wordExpression(bytes){
 if(bytes.every(b=>b.kind==='constant'))return{constant:bytes.reduce((n,b)=>(n<<8n)|BigInt(b.value),0n)};
 if(bytes.some(b=>b.kind==='unknown'))return{unresolved:unique(bytes.filter(b=>b.kind==='unknown').map(b=>b.reason))};
 if(bytes.every((b,i)=>b.kind==='word_byte'&&b.byte_index===i&&b.expression===bytes[0].expression))return{expression:bytes[0].expression};
 return{expression:'concat_bytes('+bytes.map(b=>b.kind==='constant'?b.value:b.kind==='word_byte'?`byte(${b.byte_index}, ${b.expression})`:`${b.source_id}[${b.source_offset}]`).join(', ')+')'};
}

// Type descriptions are supplied evidence or candidates. Decoding a selector
// never verifies the target implementation or that an asset transfer settled.
// Canonical layout is separate: Solidity decoders need not enforce strict ABI.
export function decodeAbi(input,descriptions,{baseOffset=0,identity,strictCanonical=false,nodeBudget=4096,arrayBudget=256,depthBudget=16}={}){
 if(!Array.isArray(descriptions)||descriptions.length>256)fail('ABI_TOP_LEVEL_TYPE_BUDGET','partial');
 const cells=inputCells(input),typeBudget={nodes:0},types=descriptions.map(t=>parseAbiType(t,0,typeBudget)),limit=BigInt(cells.length),nodes=[],issues=[],canonicalIssues=[];
 if(!Number.isSafeInteger(baseOffset)||baseOffset<0||BigInt(baseOffset)>limit)fail('ABI_BASE_OFFSET');
 for(const[n,max]of[[nodeBudget,4096],[arrayBudget,256],[depthBudget,16]])if(!Number.isSafeInteger(n)||n<1||n>max)fail('ABI_INVALID_BUDGET');
 const context=identity||hash([descriptions,cells]),canonical=(code,path,offset,details={})=>canonicalIssues.push({code,path,offset:String(offset),...details});
 let readEnd=BigInt(baseOffset);
 const read=(at,length)=>{if(at<0n||length<0n||at+length>limit)fail('ABI_RANGE_OUT_OF_BOUNDS','invalid',at);if(at+length>readEnd)readEnd=at+length;return cells.slice(Number(at),Number(at+length));};
 const known=(at,code)=>{const value=wordExpression(read(at,32n));if(value.constant===undefined)fail(code,'partial',at);return value.constant;};
 let count=0;
 function safe(type,at,path,depth,headOffset=at){
  const node={abi_value_id:id(context,path,type.canonical,String(at)),path,type:type.canonical,name:type.name||'',head_offset:String(headOffset),data_offset:String(at),status:'decoded',unresolved:[],children:[],value:null};nodes.push(node);
  try{if(++count>nodeBudget||depth>depthBudget)fail('ABI_VALUE_NODE_OR_DEPTH_BUDGET','partial',at);body(node,type,at,path,depth);}
  catch(error){if(!(error instanceof AbiFault))throw error;node.status=error.status;node.unresolved.push(error.code);node.canonical_end=null;issues.push({code:error.code,status:error.status,path,offset:error.offset});}
  return node;
 }
 function sequence(elements,base,path,depth){
  const headSize=elements.reduce((n,t)=>n+(t.dynamic?32n:BigInt(t.static_size)),0n);read(base,headSize);
  let cursor=base,tail=base+headSize,maxEnd=base+headSize;const children=[];
  for(let i=0;i<elements.length;i++){
   if(count>=nodeBudget){const code='ABI_VALUE_NODE_OR_DEPTH_BUDGET',frontier={abi_value_id:id(context,path,i,'frontier'),path:path+'['+i+':]',type:'unparsed_sequence',head_offset:String(cursor),data_offset:null,status:'partial',unresolved:[code],remaining_elements:elements.length-i,children:[],value:null,canonical_end:null};
    children.push(frontier);nodes.push(frontier);issues.push({code,status:'partial',path:frontier.path,offset:String(cursor)});break;}
   const type=elements[i],childPath=path+'['+i+']';let child;
   if(type.dynamic){
    try{const relative=known(cursor,'ABI_DYNAMIC_OFFSET_SYMBOLIC'),target=base+relative;if(target>=WORD)fail('ABI_DYNAMIC_OFFSET_OVERFLOW','invalid',cursor);
     if(relative%32n)canonical('ABI_OFFSET_UNALIGNED',childPath,cursor);if(relative<headSize)canonical('ABI_OFFSET_INSIDE_HEAD',childPath,cursor);
     if(tail!==null&&target!==tail)canonical('ABI_TAIL_GAP_OR_OVERLAP',childPath,cursor,{expected:String(tail),actual:String(target)});
     child=safe(type,target,childPath,depth+1,cursor);
    }catch(error){if(!(error instanceof AbiFault))throw error;child={abi_value_id:id(context,childPath,type.canonical,String(cursor),'pointer'),path:childPath,type:type.canonical,head_offset:String(cursor),data_offset:null,status:error.status,unresolved:[error.code],value:null,children:[],canonical_end:null};nodes.push(child);issues.push({code:error.code,status:error.status,path:childPath,offset:error.offset});}
    tail=child.canonical_end===null?null:BigInt(child.canonical_end);cursor+=32n;
   }else{child=safe(type,cursor,childPath,depth+1);cursor+=BigInt(type.static_size);}
   children.push(child);if(child.canonical_end!==null&&BigInt(child.canonical_end)>maxEnd)maxEnd=BigInt(child.canonical_end);
  }
  return{children,end:maxEnd};
 }
 function body(node,type,at,path,depth){
  if(type.kind==='tuple'){
   const result=sequence(type.components,at,path,depth);node.children=result.children;node.value=result.children.map(c=>c.value);node.canonical_end=String(result.end);return;
  }
  if(type.kind==='array'){
   const length=type.length===null?known(at,'ABI_ARRAY_LENGTH_SYMBOLIC'):BigInt(type.length);if(length>BigInt(arrayBudget))fail('ABI_ARRAY_VALUE_ELEMENT_BUDGET','partial',at);
   const base=at+(type.length===null?32n:0n),result=sequence(Array.from({length:Number(length)},()=>type.element),base,path,depth);
   node.length=String(length);node.length_offset=type.length===null?String(at):null;node.children=result.children;node.value=result.children.map(c=>c.value);node.canonical_end=String(result.end);return;
  }
  if(['bytes','string'].includes(type.kind)){
   const length=known(at,'ABI_BYTES_LENGTH_SYMBOLIC'),start=at+32n,end=start+length,padded=start+((length+31n)/32n)*32n,bytes=read(start,length);
   node.length=String(length);node.payload_offset=String(start);node.canonical_end=String(padded);
   if(padded>limit)canonical('ABI_DYNAMIC_PADDING_MISSING',path,end);
   else{const padding=read(end,padded-end);if(padding.some(b=>b.kind==='constant'&&b.value!==0))canonical('ABI_DYNAMIC_PADDING_NONZERO',path,end);
    else if(padding.some(b=>b.kind!=='constant'))canonical('ABI_DYNAMIC_PADDING_SYMBOLIC',path,end,{certainty:'unknown'});}
   if(bytes.every(b=>b.kind==='constant')){const raw=Buffer.from(bytes.map(b=>b.value));node.value='0x'+raw.toString('hex');
    if(type.kind==='string'){try{node.utf8=new TextDecoder('utf-8',{fatal:true}).decode(raw);node.utf8_status='valid';}catch{node.utf8=null;node.utf8_status='invalid_bytes_preserved';}}
   }else{node.status='symbolic';node.symbolic_bytes=structuredClone(bytes);node.unresolved=unique(bytes.filter(b=>b.kind==='unknown').map(b=>b.reason));}
   return;
  }
  const bytes=read(at,32n),word=wordExpression(bytes);node.byte_length='32';node.canonical_end=String(at+32n);
  if(word.constant===undefined){node.status='symbolic';node.value_expression=word.expression||null;node.unresolved=word.unresolved||[];
   if(!['uint','int'].includes(type.kind)||type.bits!==256){node.unresolved.push('ABI_SYMBOLIC_TYPE_CONSTRAINT_UNVERIFIED');node.required_type=type.canonical;}return;}
  const n=word.constant;
  if(['uint','ufixed','address','bool'].includes(type.kind)){
   const bits=type.kind==='address'?160:type.kind==='bool'?1:type.bits;if(n>=1n<<BigInt(bits))fail('ABI_UNSIGNED_HIGH_BITS_NONZERO','invalid',at);
   node.value=type.kind==='address'?'0x'+n.toString(16).padStart(40,'0'):type.kind==='bool'?n===1n:n.toString();
  }else if(['int','fixed'].includes(type.kind)){
   const signed=n>=WORD/2n?n-WORD:n,high=1n<<BigInt(type.bits-1);if(signed< -high||signed>=high)fail('ABI_SIGN_EXTENSION_INVALID','invalid',at);node.value=signed.toString();
  }else if(['fixed_bytes','function'].includes(type.kind)){
   const length=type.kind==='function'?24:type.length;if(bytes.slice(length).some(b=>b.value!==0))fail('ABI_FIXED_BYTES_PADDING_NONZERO','invalid',at);node.value='0x'+Buffer.from(bytes.slice(0,length).map(b=>b.value)).toString('hex');
  }
  if(['fixed','ufixed'].includes(type.kind))node.scale={coefficient:node.value,decimals:type.decimals,denominator:(10n**BigInt(type.decimals)).toString()};
 }
 let root;
 try{root=sequence(types,BigInt(baseOffset),'arguments',0);}catch(error){if(!(error instanceof AbiFault))throw error;issues.push({code:error.code,status:error.status,path:'arguments',offset:error.offset});root={children:[],end:BigInt(baseOffset)};}
 if(!issues.length&&root.end<limit)canonical('ABI_TRAILING_BYTES','arguments',root.end,{unused_bytes:String(limit-root.end)});
 const uncertainCanonical=canonicalIssues.filter(c=>c.certainty==='unknown'),noncanonical=canonicalIssues.some(c=>c.certainty!=='unknown');
 const unresolved=unique([...issues.map(i=>i.code),...nodes.flatMap(n=>n.unresolved),...(strictCanonical?uncertainCanonical.map(c=>c.code):[])]),invalid=issues.some(i=>i.status==='invalid'),partial=issues.some(i=>i.status==='partial')||unresolved.length>0;
 return{status:invalid||strictCanonical&&noncanonical?'invalid':partial?'partial':nodes.some(n=>n.status==='symbolic')?'symbolic':'decoded',
  values:root.children,nodes,unresolved,issues,canonical:{status:noncanonical?'noncanonical':issues.length||uncertainCanonical.length||unresolved.includes('ABI_SYMBOLIC_TYPE_CONSTRAINT_UNVERIFIED')?'not_verified':'canonical',issues:canonicalIssues},
  consumed_bytes:String(readEnd-BigInt(baseOffset)),canonical_encoded_bytes:String(root.end-BigInt(baseOffset)),validation_scope:'ABI_representation_only_not_target_implementation_or_EVM_decoder_failure_proof'};
}

const wordCells=n=>Array.from(Buffer.from(((n%WORD+WORD)%WORD).toString(16).padStart(64,'0'),'hex'),value=>({kind:'constant',value}));
const integer=value=>{if(typeof value==='number'&&!Number.isSafeInteger(value))fail('ABI_UNSAFE_NUMBER');
 if(typeof value==='bigint'||typeof value==='number'&&Number.isSafeInteger(value)||typeof value==='string'&&/^(?:-?\d+|0x[\da-f]+)$/i.test(value))return BigInt(value);fail('ABI_INTEGER_VALUE_INVALID');};
function concreteData(value){if(typeof value==='string'){if(!/^0x(?:[\da-f]{2})*$/i.test(value))fail('ABI_BYTES_VALUE_INVALID');return inputCells(value);}return inputCells(value);}

export function encodeAbi(descriptions,values,{identity,byteBudget=65536,nodeBudget=4096}={}){
 if(!Array.isArray(descriptions)||!Array.isArray(values)||descriptions.length!==values.length||descriptions.length>256)fail('ABI_ENCODING_ARGUMENTS_INVALID');
 if(!Number.isSafeInteger(byteBudget)||byteBudget<1||byteBudget>65536||!Number.isSafeInteger(nodeBudget)||nodeBudget<1||nodeBudget>4096)fail('ABI_INVALID_BUDGET');
 const parseBudget={nodes:0},types=descriptions.map(t=>parseAbiType(t,0,parseBudget)),nodes=[],issues=[],context=identity||hash(descriptions);let count=0,allocatedBytes=0;
 const reserve=n=>{allocatedBytes+=n;if(allocatedBytes>byteBudget)fail('ABI_ENCODING_BYTE_BUDGET','partial');};
 const unresolvedBytes=(n,reason)=>Array.from({length:Number(n)},()=>({kind:'unknown',reason}));
 function scalar(type,value){
  reserve(32);
  if(value&&typeof value==='object'&&!Array.isArray(value)&&typeof value.expression==='string'){
   let expression=value.expression,operator='word';
   if(['uint','address','ufixed'].includes(type.kind)){const bits=type.kind==='address'?160:type.bits;if(bits<256){expression=`abi_uint${bits}(${expression})`;operator='zero_extend_typed_low_bits';}}
   else if(type.kind==='int'||type.kind==='fixed'){expression=`abi_int${type.bits}_word(${expression})`;operator='sign_extend_typed_low_bits';}
   else if(type.kind==='bool'){expression=`abi_bool_word(${expression})`;operator='boolean_cleanup';}
   else if(type.kind==='fixed_bytes'||type.kind==='function'){expression=`abi_left_align_${type.kind==='function'?24:type.length}(${expression})`;operator='left_align_typed_bytes';}
   else fail('ABI_SYMBOLIC_SCALAR_TYPE_UNSUPPORTED','partial');
   return Array.from({length:32},(_,byte_index)=>({kind:'word_byte',expression,byte_index,source_expression_id:value.expression_id||null,source_expression:value.expression,
    abi_type:type.canonical,word_operator:operator,evidence_ids:value.evidence_ids||[]}));
  }
  if(['fixed_bytes','function'].includes(type.kind)){const bytes=concreteData(value),length=type.kind==='function'?24:type.length;if(bytes.length!==length)fail('ABI_FIXED_BYTES_VALUE_LENGTH');return[...bytes,...wordCells(0n).slice(length)];}
  if(type.kind==='address'){if(typeof value!=='string'||!/^0x[\da-f]{40}$/i.test(value))fail('ABI_ADDRESS_VALUE_INVALID');return wordCells(BigInt(value));}
  if(type.kind==='bool'){if(typeof value!=='boolean')fail('ABI_BOOL_VALUE_INVALID');return wordCells(value?1n:0n);}
  let n;
  if(['fixed','ufixed'].includes(type.kind)){
   const match=/^(-?)(\d+)(?:\.(\d+))?$/.exec(String(value));if(!match||(match[3]?.length||0)>type.decimals)fail('ABI_FIXED_DECIMAL_VALUE_INVALID');
   n=(BigInt(match[2])*10n**BigInt(type.decimals)+BigInt((match[3]||'').padEnd(type.decimals,'0')||'0'))*(match[1]?-1n:1n);
  }else n=integer(value);
  const signed=['int','fixed'].includes(type.kind),bound=1n<<BigInt(type.bits-(signed?1:0));if(n<(signed?-bound:0n)||n>=bound)fail('ABI_INTEGER_VALUE_RANGE');return wordCells(n);
 }
 function safe(type,value,path,depth){
  const node={abi_encoding_id:id(context,path,type.canonical),path,type:type.canonical,status:'encoded',unresolved:[],children:[],byte_length:null};nodes.push(node);
  try{if(++count>nodeBudget||depth>16)fail('ABI_ENCODING_NODE_OR_DEPTH_BUDGET','partial');
   let encoded;
   if(type.kind==='tuple'){if(!Array.isArray(value)||value.length!==type.components.length)fail('ABI_TUPLE_VALUE_ARITY');encoded=sequence(type.components,value,path,depth);node.children=encoded.children;}
   else if(type.kind==='array'){
    if(!Array.isArray(value)||value.length>256)fail('ABI_ARRAY_VALUE_ELEMENT_BUDGET','partial');if(type.length!==null&&value.length!==type.length)fail('ABI_ARRAY_VALUE_LENGTH');
    encoded=sequence(Array.from({length:value.length},()=>type.element),value,path,depth);node.children=encoded.children;
    if(type.length===null){reserve(32);encoded={...encoded,cells:encoded.cells?[...wordCells(BigInt(value.length)),...encoded.cells]:null,head_cells:[...wordCells(BigInt(value.length)),...encoded.head_cells],segments:encoded.segments.map(s=>({...s,offset:s.offset===null?null:String(BigInt(s.offset)+32n)}))};}
   }else if(['bytes','string'].includes(type.kind)){
    if(value&&typeof value==='object'&&!Array.isArray(value)&&value.expression&&!value.cells)fail('ABI_DYNAMIC_VALUE_LENGTH_UNKNOWN','partial');
    const data=type.kind==='string'&&typeof value==='string'?inputCells(Buffer.from(value,'utf8')):value?.cells?inputCells(value.cells):concreteData(value);
    if(value?.cells&&value.length!==undefined&&integer(value.length)!==BigInt(data.length))fail('ABI_DYNAMIC_BYTES_LENGTH_MISMATCH');
    const padding=(32-data.length%32)%32;reserve(32+data.length+padding);encoded={cells:[...wordCells(BigInt(data.length)),...data,...wordCells(0n).slice(0,padding)],children:[],head_cells:wordCells(BigInt(data.length)),segments:[]};
   }else encoded={cells:scalar(type,value),children:[],head_cells:[],segments:[]};
   if(encoded.cells&&encoded.cells.length>byteBudget)fail('ABI_ENCODING_BYTE_BUDGET','partial');
   node.byte_length=encoded.cells?String(encoded.cells.length):null;node.status=encoded.cells?'encoded':'partial';node.unresolved=unique(encoded.cells?.filter(b=>b.kind==='unknown').map(b=>b.reason)||[]);
   if(node.unresolved.length)node.status='partial';return{...encoded,node};
  }catch(error){if(!(error instanceof AbiFault))throw error;node.status=error.status;node.unresolved.push(error.code);issues.push({code:error.code,status:error.status,path});return{cells:null,head_cells:[],segments:[],node};}
 }
 function sequence(elements,values,path,depth){
  const headSize=elements.reduce((n,t)=>n+(t.dynamic?32n:BigInt(t.static_size)),0n);if(headSize>BigInt(byteBudget))fail('ABI_ENCODING_HEAD_BYTE_BUDGET','partial');
  const children=[];for(let i=0;i<elements.length;i++){
   if(count>=nodeBudget){issues.push({code:'ABI_ENCODING_NODE_OR_DEPTH_BUDGET',status:'partial',path,remaining_elements:elements.length-i});break;}
   children.push(safe(elements[i],values[i],path+'['+i+']',depth+1));}
  const heads=[],tails=[],segments=[];let tail=headSize,cursor=0n,complete=children.length===elements.length;
  for(let i=0;i<elements.length;i++){
   const type=elements[i],child=children[i],length=type.dynamic?32n:BigInt(type.static_size);let bytes;
   if(type.dynamic){reserve(32);bytes=tail===null?Array.from({length:32},(_,byte_index)=>({kind:'word_byte',expression:'abi_tail_offset('+path+','+i+')',byte_index,abi_type:'uint256',evidence_ids:[]})):wordCells(tail);
    if(child?.cells){tails.push(child.cells);segments.push({offset:tail===null?null:String(tail),argument:i,cells:child.cells});if(tail!==null)tail+=BigInt(child.cells.length);}
    else{tail=null;complete=false;}
   }else{bytes=child?.cells||unresolvedBytes(length,'ABI_STATIC_VALUE_UNAVAILABLE');if(!child?.cells)complete=false;}
   heads.push(bytes);segments.push({offset:String(cursor),argument:i,cells:bytes,part:'head'});cursor+=length;
  }
  const head_cells=heads.flat(),cells=complete?[...head_cells,...tails.flat()]:null;
  if(cells&&cells.length>byteBudget)fail('ABI_ENCODING_BYTE_BUDGET','partial');
  return{cells,head_cells,segments,children:children.map(c=>c.node)};
 }
 let encoded;
 try{encoded=sequence(types,values,'arguments',0);}catch(error){if(!(error instanceof AbiFault))throw error;issues.push({code:error.code,status:error.status,path:'arguments'});encoded={cells:null,head_cells:[],segments:[],children:[]};}
 const unresolved=unique([...issues.map(i=>i.code),...nodes.flatMap(n=>n.unresolved)]),invalid=issues.some(i=>i.status==='invalid');
 return{status:invalid?'invalid':!encoded.cells||unresolved.length?'partial':encoded.cells.some(b=>b.kind!=='constant')?'symbolic':'encoded',cells:encoded.cells,
  head_cells:encoded.head_cells,segments:encoded.segments,arguments:encoded.children,nodes,issues,unresolved,byte_length:encoded.cells?String(encoded.cells.length):null,
  encoded_hex:encoded.cells?.every(b=>b.kind==='constant')?'0x'+Buffer.from(encoded.cells.map(b=>b.value)).toString('hex'):null};
}
