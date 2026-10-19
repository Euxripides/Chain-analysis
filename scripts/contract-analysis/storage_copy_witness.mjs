import assert from 'node:assert/strict';

// Acceptance helper. A point-specific read requires an observed instruction;
// absence, ambiguity and unsupported layouts fail rather than inventing state.
export function createStorageCopyWitness({compiled, contract, trace, keccak256, reads=[]}) {
  const layout=contract.storageLayout,code=Buffer.from(contract.evm.deployedBytecode.object,'hex'),nodes=new Map();
  function visit(node){if(!node?.nodeType)return;nodes.set(node.id,node);for(const[key,value]of Object.entries(node))if(!['documentation','typeDescriptions'].includes(key)){if(Array.isArray(value))value.forEach(visit);else if(value?.nodeType)visit(value);}}
  for(const source of Object.values(compiled.sources))visit(source.ast);
  const pcs=[];for(let pc=0;pc<code.length;){pcs.push(pc);const op=code[pc];pc+=1+(op>=0x60&&op<=0x7f?op-0x5f:0);}
  const locations=new Map();let previous=[-1,0,-1,'',0];
  for(const[index,text]of contract.evm.deployedBytecode.sourceMap.split(';').entries()){const parts=text.split(':');previous=previous.map((v,j)=>parts[j]!==undefined&&parts[j]!==''?(j===3?parts[j]:Number(parts[j])):v);locations.set(pcs[index],[...previous]);}
  const word=value=>Buffer.from(BigInt.asUintN(256,BigInt(value)).toString(16).padStart(64,'0'),'hex');
  return (expression,keys)=>{
    assert.equal(expression.symbolic_format,'source_storage_snapshot');
    const projection=expression.storage_projection,root=layout.storage.find(r=>r.astId===projection.root_state_ast_id);assert.ok(root,'COPY_ROOT_LAYOUT_UNAVAILABLE');
    let slot=BigInt(root.slot),offset=root.offset||0,typeId=root.type;
    for(const part of projection.path){const type=layout.types[typeId];
      if(part.kind==='mapping'){
        assert.equal(type.encoding,'mapping');const label=layout.types[type.key].label;assert.equal(part.key_type,label);
        let key=part.key_literal!==null&&part.key_literal!==undefined?BigInt(part.key_literal):keys.get(part.key_expression_id);assert.notEqual(key,undefined,'COPY_KEY_UNBOUND');key=BigInt(key);
        const fixedBytes=/^bytes(\d+)$/.exec(label);if(fixedBytes)key<<=BigInt(256-Number(fixedBytes[1])*8);
        slot=BigInt('0x'+Buffer.from(keccak256(Buffer.concat([word(key),word(slot)]))).toString('hex'));offset=0;typeId=type.value;
      }else{
        assert.equal(part.kind,'member','COPY_PATH_UNSUPPORTED');const field=type.members?.find(f=>f.astId===part.field_ast_id);assert.ok(field,'COPY_FIELD_LAYOUT_UNAVAILABLE');
        slot=BigInt.asUintN(256,slot+BigInt(field.slot));offset=field.offset;typeId=field.type;
      }
    }
    const field=nodes.get(expression.source_storage_field_declaration_id),scalar=expression.source_storage_scalar;
    assert.ok(field);assert.equal(field.typeDescriptions.typeString,expression.type);assert.equal(projection.path.at(-1).field_ast_id,field.id);assert.equal(projection.offset,offset);
    const bytes=Number(layout.types[typeId].numberOfBytes);assert.equal(bytes*8,scalar.bits);assert.ok(bytes>=1&&bytes<=32&&offset+bytes<=32);
    const origin=expression.source_storage_copy_source,node=nodes.get(origin.ast_id);assert.ok(node);assert.equal(node.src.split(':').slice(0,2).join(':'),origin.start+':'+origin.length);
    const witnesses=[];
    for(const[step,log]of trace.structLogs.entries()){
      if(log.depth!==1||log.op!=='SLOAD'||BigInt('0x'+log.stack.at(-1).replace(/^0x/,''))!==slot)continue;
      const source=locations.get(log.pc);if(!source||source[2]!==compiled.sources[origin.file]?.id||source[0]>origin.start||source[0]+source[1]<origin.start+origin.length)continue;
      const next=trace.structLogs[step+1];assert.equal(next.depth,1);witnesses.push({step,pc:log.pc,source_length:source[1],word:BigInt('0x'+next.stack.at(-1).replace(/^0x/,''))});
    }
    assert.ok(witnesses.length,'COPY_SLOAD_WITNESS_UNAVAILABLE:'+field.name);const shortest=Math.min(...witnesses.map(w=>w.source_length)),selected=witnesses.filter(w=>w.source_length===shortest);
    assert.equal(new Set(selected.map(w=>w.word.toString())).size,1,'COPY_POINT_AMBIGUOUS');
    const raw=selected[0].word>>BigInt(offset*8)&((1n<<BigInt(bytes*8))-1n),value=scalar.kind==='bool'?raw!==0n:scalar.signed?BigInt.asIntN(bytes*8,raw):raw;
    reads.push({expression_id:expression.expression_id,copy_point_id:expression.source_storage_copy_point_id,field_ast_id:field.id,slot:slot.toString(),byte_offset:offset,value:typeof value==='bigint'?value.toString():value,basis:'actual_SLOAD_and_compiler_source_map',witnesses:selected.map(({step,pc})=>({step,pc}))});return value;
  };
}
