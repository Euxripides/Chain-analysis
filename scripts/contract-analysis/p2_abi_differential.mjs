import assert from'node:assert/strict';import{readFileSync,writeFileSync,mkdirSync}from'node:fs';import{createHash}from'node:crypto';import{decodeAbi,encodeAbi}from'../../analysis-engines/ir/abi.mjs';
const root=new URL('../../',import.meta.url),folder=readFileSync(new URL('acceptance/contract-analysis-memory/abi-reference-current.txt',root),'utf8').trim(),reference=JSON.parse(readFileSync(folder+'/fixtures.json'));
const sha=b=>createHash('sha256').update(b).digest('hex'),records=[];
const output=folder+'/differential-runs/'+new Date().toISOString().replace(/\D/g,'')+'Z';mkdirSync(output,{recursive:true});
function value(node){
 if(Array.isArray(node.value))return node.children.map(value);
 if(node.type==='string')return node.utf8;
 if(node.scale){let n=BigInt(node.scale.coefficient),sign=n<0n?'-':'';if(n<0n)n=-n;const decimals=node.scale.decimals,digits=n.toString().padStart(decimals+1,'0'),fraction=digits.slice(-decimals).replace(/0+$/,'');return sign+digits.slice(0,-decimals)+(fraction?'.'+fraction:'');}
 return node.value;
}
try{
 for(const fixture of reference.cases){
  const result=decodeAbi(fixture.encoded,fixture.types,{strictCanonical:true});assert.equal(result.status,'decoded',fixture.name+':'+JSON.stringify(result.issues));assert.equal(result.canonical.status,'canonical',fixture.name);
  const actual=result.values.map(value),expected=fixture.expected.map((v,i)=>fixture.types[i].includes('fixed')?String(v).replace(/(\.\d*?)0+$/,'$1').replace(/\.$/,''):v);assert.deepEqual(actual,expected,fixture.name);
  const encoded=encodeAbi(fixture.types,fixture.expected);assert.equal(encoded.status,'encoded',fixture.name+':'+JSON.stringify(encoded.issues));assert.equal(encoded.encoded_hex,fixture.encoded,fixture.name+' independent encoding');
  const truncated='0x'+fixture.encoded.slice(2,-2),negative=decodeAbi(truncated,fixture.types,{strictCanonical:true});assert.equal(negative.status,'invalid',fixture.name+' truncated canonical encoding');
  records.push({name:fixture.name,encoded_sha256:fixture.sha256,independent_encoding_matches:true,status:'PASS',nodes:result.nodes.length,truncation_status:negative.status});
 }
 const result={status:'PASS',reference:'eth-abi '+reference.version,cases:records.length,canonical_positive_and_truncated_negative:records.length*2,
  fixture_sha256:sha(readFileSync(folder+'/fixtures.json')),abi_module_sha256:sha(readFileSync(new URL('analysis-engines/ir/abi.mjs',root))),scope:'independent concrete ABI decoding; target semantics, source bindings and MCP remain separate',records};
 writeFileSync(output+'/result.json',JSON.stringify(result,null,2));writeFileSync(folder+'/differential-current.txt',output+'\n');console.log('ABI_INDEPENDENT_DIFFERENTIAL_PASS',records.length,output);
}catch(error){writeFileSync(output+'/failure.json',JSON.stringify({message:error.message,stack:error.stack,completed:records.length},null,2));throw error;}
