function metadataStart(bytes,compiler,compilerAuxdata=false) {
  if(bytes.length<4)return null;
  const size=bytes.readUInt16BE(bytes.length-2),start=bytes.length-size-2;
  const legacy=Number(compiler.split('.')[1])===4;
  const afterHalt=bytes[start-1]===0xfe||legacy&&bytes[start-1]===0x00;
  if(size<2||size>4096||start<1||!(afterHalt||compilerAuxdata))return null;
  let at=start;
  const length=additional=>{if(additional<24)return additional;if(additional===24)return bytes[at++];if(additional===25){const n=bytes.readUInt16BE(at);at+=2;return n}throw new Error('CBOR_LENGTH_UNSUPPORTED')};
  const read=()=>{const header=bytes[at++],major=header>>5,low=header&31;
    if(major===7&&[20,21].includes(low))return low===21;
    const n=length(low);if(n>4096||at+n>bytes.length-2&&major!==5)throw new Error('CBOR_RANGE_INVALID');
    if(major===2||major===3){const b=bytes.subarray(at,at+n);at+=n;return major===2?b:b.toString('utf8')}
    if(major===5){if(n>8)throw new Error('CBOR_MAP_INVALID');const map=Object.create(null);for(let i=0;i<n;i++){const key=read();if(typeof key!=='string'||Object.hasOwn(map,key))throw new Error('CBOR_KEY_INVALID');map[key]=read()}return map}
    throw new Error('CBOR_VALUE_UNSUPPORTED');
  };
  try {
    const value=read(),keys=Object.keys(value);
    if(at!==bytes.length-2||!keys.length||keys.some(k=>!['ipfs','bzzr0','bzzr1','solc','experimental'].includes(k)))return null;
    if(value.solc){const expected=compiler.split('+')[0].split('.').map(Number);if(!Buffer.isBuffer(value.solc)||value.solc.length!==3||expected.some((v,i)=>v!==value.solc[i]))return null}
    if(!keys.some(k=>['ipfs','bzzr0','bzzr1'].includes(k)))return null;
    if(compilerAuxdata&&!afterHalt&&!value.solc)return null;
    return start;
  }catch{return null}
}

export function verifySolidityRuntime(compiled,source,bytecode,compiler,{isLibrary=false,codeAddress=''}={}) {
  const original=compiled.object,provided=source.runtimeBytecode?.recompiledBytecode?.replace(/^0x/,'').toLowerCase();
  if(typeof original!=='string'||typeof provided!=='string')throw new Error('RECOMPILATION_BYTECODE_UNAVAILABLE');
  if(source.runtimeBytecode?.onchainBytecode?.toLowerCase()!==bytecode.toLowerCase())throw new Error('SOURCE_SNAPSHOT_CODE_MISMATCH');
  if(!['match','exact_match'].includes(source.runtimeMatch)||!['match','exact_match'].includes(source.match))throw new Error('SOURCE_NOT_VERIFIED');
  let recompiled=original.toLowerCase(),reference=provided;
  const links=[];
  for(const [file,libraries] of Object.entries(compiled.linkReferences||{}))for(const [name,ranges] of Object.entries(libraries))for(const range of ranges) {
    if(!Number.isInteger(range.start)||range.start<0||range.length!==20||range.start*2+40>original.length)throw new Error('COMPILER_LINK_REFERENCE_INVALID');
    const start=range.start*2;recompiled=recompiled.slice(0,start)+'0'.repeat(40)+recompiled.slice(start+40);reference=reference.slice(0,start)+'0'.repeat(40)+reference.slice(start+40);
    links.push({...range,file,name});
  }
  if(recompiled!==reference||!/^(?:[0-9a-f]{2})+$/.test(recompiled))throw new Error('RECOMPILATION_BYTECODE_MISMATCH');
  const built=Buffer.from(recompiled,'hex'),actual=Buffer.from(bytecode.slice(2),'hex'),bindings={},libraries={},mask=[];
  const range=(start,length)=>{if(!Number.isInteger(start)||!Number.isInteger(length)||start<0||length<1||start+length>built.length||start+length>actual.length)throw new Error('COMPILER_RUNTIME_REFERENCE_INVALID');
    if(mask.some(r=>start<r.start+r.length&&r.start<start+length))throw new Error('COMPILER_RUNTIME_REFERENCES_OVERLAP');mask.push({start,length});return actual.subarray(start,start+length).toString('hex')};
  for(const [astID,ranges] of Object.entries(compiled.immutableReferences||{})) {
    const values=ranges.map(r=>range(r.start,r.length));if(new Set(values).size!==1)throw new Error('IMMUTABLE_RUNTIME_REFERENCES_CONFLICT');
    bindings[astID]={word_hex:'0x'+values[0],ranges};
  }
  for(const link of links){const value='0x'+range(link.start,link.length),key=link.file+':'+link.name;if(libraries[key]&&libraries[key]!==value)throw new Error('LIBRARY_RUNTIME_REFERENCES_CONFLICT');libraries[key]=value}
  if(isLibrary&&built[0]===0x73&&built.subarray(1,21).every(b=>b===0)&&actual[0]===0x73) {
    const address='0x'+range(1,20);if(address.toLowerCase()!==codeAddress.toLowerCase())throw new Error('LIBRARY_CALL_PROTECTION_ADDRESS_MISMATCH');
  }
  for(const r of mask){built.fill(0,r.start,r.start+r.length);actual.fill(0,r.start,r.start+r.length)}
  let basis='runtime_bytes_and_compiler_references_match';
  const metadata_ranges=[];
  if(!built.equals(actual)) {
    if(source.stdJsonInput?.settings?.metadata?.appendCBOR===false)throw new Error('RECOMPILED_RUNTIME_SNAPSHOT_MISMATCH');
    // Older compiler layouts can append strings/data before CBOR rather than an
    // immediate INVALID. Accept that layout only when the published auxdata is
    // exactly the locally recompiled trailing bytes, with a valid solc CBOR key.
    const ranges=Object.values(source.runtimeBytecode?.cborAuxdata||{});
    const compilerAuxdata=ranges.some(r=>Number.isInteger(r.offset)&&r.offset>0&&typeof r.value==='string'&&/^0x(?:[0-9a-f]{2})+$/i.test(r.value)&&
      r.offset+r.value.slice(2).length/2===built.length&&built.subarray(r.offset).toString('hex')===r.value.slice(2).toLowerCase());
    const a=metadataStart(built,compiler,compilerAuxdata),b=metadataStart(actual,compiler,compilerAuxdata);
    if(source.runtimeMatch!=='match'||a===null||b===null||a!==b||!built.subarray(0,a).equals(actual.subarray(0,b)))throw new Error('RECOMPILED_RUNTIME_SNAPSHOT_MISMATCH');
    if(compilerAuxdata&&!ranges.some(r=>r.offset===a&&built.subarray(a).toString('hex')===r.value?.slice(2).toLowerCase()))throw new Error('COMPILER_AUXDATA_RANGE_MISMATCH');
    metadata_ranges.push({start:a,length:built.length-a,basis:compilerAuxdata?'published_auxdata_checked_against_recompilation_and_valid_cbor':'valid_terminal_cbor_after_halt'});
    basis='runtime_code_match_with_valid_trailing_metadata_difference';
  }
  return {basis,bindings,libraries,permitted_ranges:mask,metadata_ranges,metadata_exact:basis==='runtime_bytes_and_compiler_references_match'};
}
