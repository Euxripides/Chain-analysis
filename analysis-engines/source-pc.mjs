import {hash,id,unique} from './model.mjs';
import {createHash} from 'node:crypto';

export const CALL_OPS=new Map([[0xf1,'CALL'],[0xf2,'CALLCODE'],[0xf4,'DELEGATECALL'],[0xfa,'STATICCALL']]);

export function instructionMap(bytecode,sourceMap) {
  if(!/^0x(?:[0-9a-fA-F]{2})*$/.test(bytecode))throw new Error('PC_INVALID_BYTECODE');
  const bytes=Buffer.from(bytecode.slice(2),'hex'),entries=sourceMap?sourceMap.split(';'):[];
  const out=[];let previous=['0','0','-1','-', '0'],pc=0;
  for(let index=0;index<entries.length;index++) {
    if(pc>=bytes.length)throw new Error('PC_SOURCE_MAP_EXCEEDS_CODE');
    const fields=entries[index].split(':');
    if(fields.length>5)throw new Error('PC_INVALID_SOURCE_MAP');
    previous=previous.map((v,i)=>fields[i]!==undefined&&fields[i]!==''?fields[i]:v);
    const [start,length,file, jump,modifierDepth]=previous;
    if(![start,length,file,modifierDepth].every(v=>/^-?\d+$/.test(v))||!['i','o','-'].includes(jump))throw new Error('PC_INVALID_SOURCE_MAP');
    const op=bytes[pc],size=op>=0x60&&op<=0x7f?op-0x5f+1:1;
    if(pc+size>bytes.length)throw new Error('PC_TRUNCATED_PUSH');
    out.push({pc,instruction_index:index,opcode:op,source_start:Number(start),source_length:Number(length),source_file_id:Number(file),jump,modifier_depth:Number(modifierDepth)});
    pc+=size;
  }
  return out;
}

export function addSourcePCMap(result,{compiled,bytecode,output,compiler,source,location}) {
  const artifact_id=id(result.snapshot.code_hash,result.snapshot.code_address,'runtime-source-pc');
  const fileIDs=new Map(Object.entries(output.sources||{}).map(([file,value])=>[value.id,file]));
  const generated=new Set((compiled.evm.deployedBytecode.generatedSources||[]).map(s=>s.id));
  const sourceMap=compiled.evm.deployedBytecode.sourceMap||'';
  const deps=result.sections.external_returns||[],evidence=new Map(result.sections.evidence.map(e=>[e.evidence_id,e]));
  const rows=[];
  for(const instruction of instructionMap(bytecode,sourceMap)) {
    const call_type=CALL_OPS.get(instruction.opcode);if(!call_type)continue;
    const file=fileIDs.get(instruction.source_file_id),start=instruction.source_start,length=instruction.source_length;
    const usable=Boolean(file)&&start>=0&&length>0&&!generated.has(instruction.source_file_id);
    let candidates=usable?deps.filter(d=>d.call_type===call_type&&d.evidence_ids.some(key=>{
      const loc=evidence.get(key)?.location;
      return loc?.file===file&&start>=loc.start&&start+length<=loc.start+loc.length;
    })):[];
    if(candidates.length){
      const span=d=>d.source_callsite?.length??Math.min(...d.evidence_ids.map(k=>evidence.get(k)?.location?.length??Infinity));
      const smallest=Math.min(...candidates.map(span));candidates=candidates.filter(d=>span(d)===smallest);
    }
    const astIDs=unique(candidates.flatMap(d=>d.evidence_ids.map(key=>evidence.get(key)?.location?.ast_id).filter(x=>x!==undefined)));
    rows.push({pc_map_id:id(artifact_id,instruction.pc),...instruction,call_type,source_file:file||null,
      code_address:result.snapshot.code_address,code_hash:result.snapshot.code_hash,storage_address:result.snapshot.storage_address,
      source_dependency_ids:candidates.map(d=>d.dependency_id),source_ast_ids:astIDs,
      status:!usable?'generated_or_unmapped_code':astIDs.length===1?'unique_source_callsite':'requires_unambiguous_source_range',
      artifact_id,scope:'verified_runtime_instruction',compiler});
  }
  result.sections.source_pc_maps||=[];result.sections.source_pc_maps.push(...rows);
  const text=JSON.stringify({schema_version:'contract-runtime-source-pc-v1',snapshot:result.snapshot,compiler,
    source_map:sourceMap,runtime_bytecode_sha256:createHash('sha256').update(Buffer.from(bytecode.slice(2),'hex')).digest('hex'),source_files:Object.fromEntries(fileIDs),
    generated_source_ids:[...generated],call_instructions:rows});
  result.sections.artifacts.push({artifact_id,kind:'source_pc_map',filename:'runtime-source-pc.json',media_type:'application/json',text,sha256:hash(text)});
  return rows;
}
