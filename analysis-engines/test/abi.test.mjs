import test from'node:test';import assert from'node:assert/strict';import{decodeAbi,encodeAbi,parseAbiType,AbiFault}from'../ir/abi.mjs';import{ByteMemory}from'../ir/memory.mjs';
const WORD=1n<<256n,word=value=>Buffer.from(BigInt(value).toString(16).padStart(64,'0'),'hex'),raw=hex=>Buffer.from(hex.padEnd(64,'0'),'hex'),join=(...items)=>Buffer.concat(items),value=result=>result.values.map(v=>v.value);

test('ABI integers, addresses, function values and fixed coefficients retain every bit and exact unit',()=>{
 const address='1234567890123456789012345678901234567890',fn=address+'abcdef01';
 const result=decodeAbi(join(word(WORD-1n),word(WORD-2n),word('0x'+address),word(1),raw('616263'),raw(fn),word(1250000000000000000n)),['uint256','int8','address','bool','bytes3','function','fixed128x18']);
 assert.equal(result.status,'decoded');assert.deepEqual(value(result),[(WORD-1n).toString(),'-2','0x'+address,true,'0x616263','0x'+fn,'1250000000000000000']);
 assert.deepEqual(result.values[6].scale,{coefficient:'1250000000000000000',decimals:18,denominator:'1000000000000000000'});
 for(const[type,n]of[['uint8',256],['int8',128],['bool',2],['address',1n<<200n]])assert.equal(decodeAbi(word(n),[type]).status,'invalid',type);
});

test('nested dynamic array offsets are relative to the tuple after each array length',()=>{
 const result=decodeAbi(join(...[32,2,64,160,2,1,2,1,3].map(word)),['uint256[][]']);
 assert.equal(result.status,'decoded');assert.deepEqual(value(result),[[['1','2'],['3']]]);assert.equal(result.canonical.status,'canonical');
 assert.equal(result.values[0].children[0].head_offset,'64');assert.equal(result.values[0].children[0].data_offset,'128');
 assert.equal(result.values[0].children[1].data_offset,'224');
});

test('strings use byte lengths while tuple and fixed-array static widths occupy full heads',()=>{
 const strings=decodeAbi(join(word(32),word(2),word(64),word(128),word(3),raw('e4b8ad'),word(2),raw('6262')),['string[]']);
 assert.equal(strings.status,'decoded');assert.equal(strings.values[0].children[0].utf8,'中');assert.equal(strings.values[0].children[0].length,'3');
 const types=[{type:'tuple',components:[{type:'uint8'},{type:'bytes3[2]'}]},'bytes'];
 const result=decodeAbi(join(word(7),raw('616263'),raw('646566'),word(128),word(1),raw('7a')),types);
 assert.equal(result.status,'decoded');assert.deepEqual(value(result),[['7',['0x616263','0x646566']],'0x7a']);assert.equal(result.values[1].head_offset,'96');assert.equal(result.values[1].data_offset,'128');
});

test('dynamic tuples, fixed arrays of dynamic elements, empty values and invalid UTF8 preserve their topology',()=>{
 const type={type:'tuple',components:[{type:'uint32'},{type:'bytes'}]},tuple=decodeAbi(join(word(32),word(7),word(64),word(3),raw('aabbcc')),[type]);
 assert.equal(tuple.status,'decoded');assert.deepEqual(value(tuple),[['7','0xaabbcc']]);
 const fixed=decodeAbi(join(word(32),word(64),word(96),word(0),word(1),raw('ff')),['bytes[2]']);
 assert.equal(fixed.status,'decoded');assert.equal(fixed.canonical.status,'canonical');assert.deepEqual(value(fixed),[['0x','0xff']]);
 const zero=decodeAbi('0x',[{type:'tuple',components:[]}]);assert.equal(zero.status,'decoded');assert.deepEqual(value(zero),[[]]);
 const badUTF8=decodeAbi(join(word(32),word(1),raw('ff')),['string']);assert.equal(badUTF8.status,'decoded');assert.equal(badUTF8.values[0].value,'0xff');assert.equal(badUTF8.values[0].utf8,null);
});

test('nonminimal, overlapping and unaligned tails are canonical issues rather than automatic decoder failure claims',()=>{
 const gap=join(word(64),word(0),word(1),raw('aa'));assert.equal(decodeAbi(gap,['bytes']).status,'decoded');assert.equal(decodeAbi(gap,['bytes']).canonical.status,'noncanonical');assert.equal(decodeAbi(gap,['bytes'],{strictCanonical:true}).status,'invalid');
 const overlap=join(word(64),word(64),word(3),raw('aabbcc')),shared=decodeAbi(overlap,['bytes','bytes']);assert.equal(shared.status,'decoded');assert.deepEqual(value(shared),['0xaabbcc','0xaabbcc']);assert.equal(shared.canonical.status,'noncanonical');
 const unaligned=join(word(33),Buffer.from([0]),word(1),Buffer.from([0xaa]));assert.equal(decodeAbi(unaligned,['bytes']).values[0].value,'0xaa');assert.equal(decodeAbi(unaligned,['bytes']).canonical.status,'noncanonical');
 const inside=decodeAbi(word(0),['bytes']);assert.equal(inside.status,'decoded');assert.equal(inside.values[0].value,'0x');assert.equal(inside.canonical.status,'noncanonical');
});

test('truncated payloads and overflow retain the other valid fields without reading zero beyond the input',()=>{
 const result=decodeAbi(join(word(64),word(7),word(10),Buffer.from('aabbcc','hex')),['bytes','uint256']);assert.equal(result.status,'invalid');assert.equal(result.values[1].value,'7');assert.ok(result.unresolved.includes('ABI_RANGE_OUT_OF_BOUNDS'));
 const overflow=decodeAbi(join(Buffer.from('aabbccdd','hex'),word(WORD-1n)),['bytes'],{baseOffset:4});assert.equal(overflow.status,'invalid');assert.ok(overflow.unresolved.includes('ABI_DYNAMIC_OFFSET_OVERFLOW'));
 assert.equal(decodeAbi(Buffer.alloc(31),['uint256']).status,'invalid');
});

test('symbolic words retain their memory expressions and unknown offsets do not invent dynamic values',()=>{
 const memory=new ByteMemory({frameId:'abi-frame'}).mstore(0,{expression:'quantity'}),symbolic=decodeAbi(memory.read(0,32).cases[0].bytes,['uint256']);
 assert.equal(symbolic.status,'symbolic');assert.equal(symbolic.values[0].value_expression,'quantity');assert.equal(symbolic.values[0].value,null);
 const offset=new ByteMemory({frameId:'abi-frame'}).mstore(0,{expression:'dynamic_offset'}).mstore(32,42n),result=decodeAbi(offset.read(0,64).cases[0].bytes,['bytes','uint256']);
 assert.equal(result.status,'partial');assert.ok(result.unresolved.includes('ABI_DYNAMIC_OFFSET_SYMBOLIC'));assert.equal(result.values[1].value,'42');
 assert.equal(decodeAbi(memory.read(0,32).cases[0].bytes,['address']).status,'partial');
});

test('symbolic padding stays unverified and explicit budgets preserve a visible unparsed suffix',()=>{
 const bytes=Array.from(join(word(32),word(1),raw('aa')),b=>({kind:'constant',value:b}));bytes.at(-1).kind='unknown';bytes.at(-1).reason='opaque_padding';
 const result=decodeAbi(bytes,['bytes'],{strictCanonical:true});assert.equal(result.status,'partial');assert.equal(result.canonical.status,'not_verified');
 const data=join(word(32),word(50),...Array.from({length:50},(_,i)=>word(i))),limited=decodeAbi(data,['uint256[]'],{nodeBudget:8});
 assert.equal(limited.status,'partial');assert.ok(limited.nodes.some(n=>n.remaining_elements>0));assert.ok(limited.values[0].children.some(n=>n.value==='0'));
 assert.throws(()=>parseAbiType('uint256'+'[]'.repeat(17)),e=>e instanceof AbiFault&&e.status==='partial');
});

test('ABI encoding builds nested heads and tails with typed symbolic word provenance',()=>{
 const result=encodeAbi(['uint256[][]'],[[[1n,2n],[3n]]]);assert.equal(result.status,'encoded');assert.equal(result.encoded_hex,'0x'+join(...[32,2,64,160,2,1,2,1,3].map(word)).toString('hex'));
 const symbolic=encodeAbi(['address','uint256'],[{expression:'recipient',expression_id:'recipient-value',evidence_ids:['source-receiver']},{expression:'amount',expression_id:'amount-value'}]);
 assert.equal(symbolic.status,'symbolic');assert.equal(symbolic.byte_length,'64');assert.equal(symbolic.cells[0].expression,'abi_uint160(recipient)');assert.equal(symbolic.cells[32].expression,'amount');assert.equal(symbolic.cells[32].source_expression_id,'amount-value');
});

test('unknown dynamic encoding lengths preserve available heads and never publish a truncated complete encoding',()=>{
 const result=encodeAbi(['uint256','bytes'],[42n,{expression:'payload'}]);assert.equal(result.status,'partial');assert.equal(result.byte_length,null);assert.equal(result.cells,null);assert.ok(result.unresolved.includes('ABI_DYNAMIC_VALUE_LENGTH_UNKNOWN'));
 assert.equal(Buffer.from(result.head_cells.slice(0,32).map(b=>b.value)).toString('hex'),word(42).toString('hex'));
 assert.equal(encodeAbi(['uint8'],[256]).status,'invalid');assert.equal(encodeAbi(['bytes3'],['0x0102']).status,'invalid');assert.equal(encodeAbi(['uint256[]'],[[1n,2n,3n]],{byteBudget:64}).status,'partial');
 assert.equal(encodeAbi(['bytes'],[{cells:[1],length:2}]).status,'invalid');
});
