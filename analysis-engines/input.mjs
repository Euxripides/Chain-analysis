import { createRequire } from 'node:module';
import { createHash } from 'node:crypto';
import { compileSource, SUBMITTED_TEMPLATE } from './source.mjs';
import { emptyResult, hash, id } from './model.mjs';
import { disassemble } from './bytecode.mjs';
const require=createRequire(import.meta.url),{keccak_256}=require('@noble/hashes/sha3.js');
const validCode=code=>/^0x(?:[0-9a-f]{2}){1,49152}$/i.test(code||'');
const codeHash=code=>'0x'+Buffer.from(keccak_256(Buffer.from(code.slice(2),'hex'))).toString('hex');
const artifact=(kind,filename,text)=>({artifact_id:id(kind,hash(text)),kind,filename,media_type:'application/json',text,sha256:hash(text)});

// Called only inside the same network-disabled processor as analysis/solc.
export function normalizeSubmittedInput(original) {
  if(!original.submitted_input_json)return {input:original,artifacts:[],gaps:[]};
  const raw=original.submitted_input_json,snapshot={...original.snapshot};
  if(Buffer.byteLength(raw)>2<<20||hash(raw)!==snapshot.input_sha256)throw Error('SUBMITTED_INPUT_DIGEST_MISMATCH');
  const submitted=JSON.parse(raw),artifacts=[artifact('submitted_input','submitted-input.json',raw)],gaps=[];
  if(!['source','bytecode'].includes(submitted.kind)||submitted.kind!==snapshot.input_kind)throw Error('SUBMITTED_INPUT_KIND_MISMATCH');
  const offline=snapshot.identity_kind==='code_only';
  if(offline){
    for(const key of ['chain','chain_id','block_number','block_hash','block_time','entry_address','code_address','storage_address','fetched_at','proxy'])if(Object.hasOwn(snapshot,key))throw Error('CODE_ONLY_OBSERVED_DEPLOYMENT_FORBIDDEN');
    snapshot.state_granularity='symbolic_state';
  }
  if(JSON.stringify(snapshot.declared_environment??null)!==JSON.stringify(submitted.environment??null))throw Error('DECLARED_ENVIRONMENT_IDENTITY_MISMATCH');
  const input={...original,snapshot};delete input.source;
  if(submitted.kind==='bytecode') {
    if(!validCode(submitted.bytecode)||!['runtime','creation'].includes(submitted.code_phase))throw Error('SUBMITTED_BYTECODE_INVALID');
    if(!offline&&(submitted.code_phase!=='runtime'||submitted.bytecode.toLowerCase()!==input.bytecode?.toLowerCase()))throw Error('SUBMITTED_RUNTIME_SNAPSHOT_MISMATCH');
    input.bytecode=offline?submitted.bytecode.toLowerCase():input.bytecode;
    snapshot.code_phase=submitted.code_phase;
    snapshot.code_identity={basis:offline?'submitted_code_bytes':'submitted_bytes_match_observed_runtime',input_sha256:snapshot.input_sha256};
  } else {
    const source=submitted.source;
    if(source?.language!=='Solidity'||!source.files||Array.isArray(source.files)||!source.compiler_version||!source.target)throw Error('SUBMITTED_SOURCE_INVALID');
    const paths=Object.keys(source.files);
    if(!paths.length||paths.length>256||paths.some(p=>{const logical=p.replace(/^(?:\.\/)+/,'');return!logical||p.length>256||/(?:^|\/)\.\.?($|\/)|[\\:\x00\r\n\t]|^\/|\/\/$/.test(logical)||typeof source.files[p]!=='string'}))throw Error('SUBMITTED_SOURCE_PATH_INVALID');
    const standard={language:'Solidity',sources:Object.fromEntries(paths.map(p=>[p,{content:source.files[p]}])),settings:source.settings||{}};
    let compiled,output;
    try {
      output=compileSource(standard,source.compiler_version);
      const split=source.target.lastIndexOf(':'),file=source.target.slice(0,split),name=source.target.slice(split+1);
      compiled=output.contracts?.[file]?.[name];
      if(!compiled?.evm?.deployedBytecode?.object)throw Error('COMPILED_RUNTIME_UNAVAILABLE');
    } catch(error) {
      const diagnostics={status:'failed',compiler:source.compiler_version,target:source.target,error_code:String(error.message).split(':')[0],message:String(error.message),diagnostics:error.diagnostics||[]};
      artifacts.push(artifact('compilation_diagnostics','compilation-diagnostics.json',JSON.stringify(diagnostics)));
      gaps.push({dimension:'source',complete:false,reason:diagnostics.error_code,artifact_id:artifacts.at(-1).artifact_id});
      if(offline&&!validCode(submitted.bytecode))return {input,artifacts,gaps,terminal:'compilation_failed'};
      if(offline){input.bytecode=submitted.bytecode.toLowerCase();snapshot.code_phase=submitted.code_phase;}
      else if(submitted.bytecode&&submitted.bytecode.toLowerCase()!==input.bytecode?.toLowerCase())throw Error('SUBMITTED_FALLBACK_SNAPSHOT_MISMATCH');
      snapshot.code_identity={basis:offline?'submitted_fallback_bytes':'observed_runtime_after_source_compilation_failure',source_compilation:'failed'};
    }
    if(compiled) {
      const runtime=compiled.evm.deployedBytecode,template=runtime.object.toLowerCase(),links=[];
      let masked=template;
      for(const [file,libraries]of Object.entries(runtime.linkReferences||{}))for(const [name,ranges]of Object.entries(libraries))for(const r of ranges){
        if(!Number.isInteger(r.start)||r.start<0||r.length!==20||r.start*2+40>masked.length)throw Error('COMPILER_LINK_REFERENCE_INVALID');
        masked=masked.slice(0,r.start*2)+'0'.repeat(40)+masked.slice((r.start+20)*2);links.push({...r,file,name});
      }
      if(!validCode('0x'+masked))throw Error('COMPILED_RUNTIME_INVALID_OR_TOO_LARGE');
      const immutables=Object.entries(runtime.immutableReferences||{}).flatMap(([ast_id,ranges])=>ranges.map(r=>({...r,ast_id})));
      if(offline)input.bytecode='0x'+masked;
      snapshot.code_phase='runtime';
      snapshot.code_identity={basis:offline?'compiled_runtime_template':'submitted_source_compared_to_observed_runtime',compiler:source.compiler_version,target:source.target,
        compiler_settings_sha256:hash(JSON.stringify(standard.settings)),unbound_immutable_ranges:offline?immutables:[],unbound_library_ranges:offline?links:[],observed_deployment:!offline};
      if(offline&&(immutables.length||links.length))gaps.push({dimension:'runtime_bindings',complete:false,reason:'deployment_bindings_unavailable',immutable_ranges:immutables,library_ranges:links});
      const diagnostics={status:'completed',compiler:source.compiler_version,target:source.target,diagnostics:output.errors||[]};
      artifacts.push(artifact('compilation_diagnostics','compilation-diagnostics.json',JSON.stringify(diagnostics)));
      input.source={stdJsonInput:standard,compilation:{compilerVersion:source.compiler_version,fullyQualifiedName:source.target},
        origin:'submitted_source',inputVerification:offline?{basis:'compiled_runtime_template',bindings:{},libraries:{},permitted_ranges:[],metadata_ranges:[],observed_deployment:false}:null,
        runtimeBytecode:{recompiledBytecode:'0x'+template,onchainBytecode:input.bytecode},match:'match',runtimeMatch:'match'};
      if(offline)input.source[SUBMITTED_TEMPLATE]=true;
    }
  }
  if(input.bytecode){
    snapshot.code_hash=codeHash(input.bytecode);snapshot.code_sha256=createHash('sha256').update(Buffer.from(input.bytecode.slice(2),'hex')).digest('hex');
  }
  const normalized={input_sha256:snapshot.input_sha256,identity:snapshot,compiler:input.source?.compilation||null,source_status:input.source?'compiled':'unavailable_or_failed'};
  artifacts.push(artifact('input_identity','input-identity.json',JSON.stringify(normalized)));
  return {input,artifacts,gaps,terminal:snapshot.code_phase==='creation'?'creation':null};
}

export function inputOnlyResult(normalized) {
  const {input,artifacts,gaps,terminal}=normalized,result=emptyResult(input.snapshot);
  result.sections.artifacts.push(...artifacts);result.sections.coverage.push(...gaps);
  result.sections.contracts.push({...input.snapshot,analysis_status:terminal==='creation'?'creation_disassembly_only':'compilation_failed'});
  if(terminal==='creation'){
    const text=disassemble(input.bytecode).map(o=>`${o.pc} ${o.mnemonic}${o.data?' '+o.data:''}`).join('\n');
    result.sections.artifacts.push({...artifact('disassembly','creation-disassembly.txt',text),media_type:'text/plain'});
    result.sections.artifacts.push({...artifact('bytecode','creation-bytecode.hex',input.bytecode),media_type:'text/plain'});
    result.sections.frontier.push({kind:'creation_runtime',reason:'constructor_execution_and_returned_runtime_not_established',code_hash:input.snapshot.code_hash});
    result.sections.coverage.push({dimension:'creation',complete:false,reason:'sequential_disassembly_only_no_reachability_or_runtime_claim'});
  } else result.sections.engines.push({engine:'solc-ast',status:'failed',error_code:'COMPILATION_FAILED',diagnostics_artifact_id:artifacts.find(a=>a.kind==='compilation_diagnostics')?.artifact_id});
  result.metrics={provider_http:0,analysis_status:terminal};return result;
}
