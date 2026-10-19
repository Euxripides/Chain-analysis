import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {createRequire} from 'node:module';
import {analyze} from '../analyze.mjs';
import {hash} from '../model.mjs';
import {normalizeSubmittedInput} from '../input.mjs';
import {compactConclusionDependencies}from'../conclusion-dependencies.mjs';
import {gunzipSync}from'node:zlib';
const require=createRequire(import.meta.url),compiler=require('solc-0.8.30').version().split('.Emscripten')[0];
function input(material){
 const raw=JSON.stringify(material);
 return {submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:material.kind,input_sha256:hash(raw),engine_digest:'sha256:'+'a'.repeat(64),
  declared_environment:material.environment,state_granularity:'symbolic_state',evm_rules:material.environment?.evm_rules||'shanghai'},engines:['whatsabi','sevm','solc-ast']};
}
function source(text,extra={}){return {kind:'source',source:{language:'Solidity',files:{'Example.sol':text},compiler_version:compiler,settings:{evmVersion:'shanghai'},target:'Example.sol:Example'},...extra};}
const simple='pragma solidity ^0.8.24; contract Example {uint public total; function set(uint n) external {require(n<100);total=n;} function plus(uint n)external pure returns(uint){return n+1;}}';
test('submitted source compiles in the common pipeline without a fabricated deployment',()=>{
 const request=input(source(simple)),r=analyze(request);
 assert.ok(r.sections.methods.some(m=>m.signature==='set(uint256)'));
 assert.ok(r.sections.permissions.some(p=>p.condition.includes('n<100')));
 assert.ok(r.sections.state_transitions.some(s=>s.complete&&s.paths.some(p=>p.writes.length)));
 assert.equal(r.snapshot.input_sha256,request.snapshot.input_sha256);assert.equal(r.snapshot.identity_kind,'code_only');
 for(const field of ['chain','chain_id','block_number','block_hash','entry_address','storage_address','code_address','block_time'])assert.equal(Object.hasOwn(r.snapshot,field),false);
 assert.equal(r.snapshot.code_sha256,createHash('sha256').update(Buffer.from(r.sections.artifacts.find(a=>a.kind==='bytecode').text.slice(2),'hex')).digest('hex'));
 assert.ok(r.sections.conclusions.length);assert.ok(r.sections.conclusions.every(c=>c.code_identity.input_sha256===r.snapshot.input_sha256&&c.state_context.observed===false));
 const evidence=new Set(r.sections.evidence.map(e=>e.evidence_id));assert.ok(r.sections.conclusions.every(c=>c.evidence_ids.every(x=>evidence.has(x))));
});
test('raw Unicode source and full compiler errors survive both failure and runtime fallback',()=>{
 const invalid=source('// 中文 Ω\npragma solidity ^0.8.24; contract Example { invalid syntax }');
 const failed=analyze(input(invalid));assert.equal(failed.sections.methods.length,0);
 const raw=failed.sections.artifacts.find(a=>a.kind==='submitted_input');assert.equal(JSON.parse(raw.text).source.files['Example.sol'],invalid.source.files['Example.sol']);
 const diagnostic=JSON.parse(failed.sections.artifacts.find(a=>a.kind==='compilation_diagnostics').text);assert.equal(diagnostic.status,'failed');assert.ok(diagnostic.diagnostics.some(d=>d.severity==='error'&&d.sourceLocation));
 const good=normalizeSubmittedInput(input(source(simple)));
 const fallback=analyze(input({...invalid,bytecode:good.input.bytecode,code_phase:'runtime'}));
 assert.ok(fallback.sections.methods.length);assert.ok(fallback.sections.coverage.some(c=>c.dimension==='source'&&!c.complete));
 assert.equal(fallback.sections.contracts[0].source_status,'bytecode_only');
});
test('unbound immutable template cannot turn placeholder zero into an observed recipient',()=>{
 const r=analyze(input(source('pragma solidity ^0.8.24; contract Example {address payable public immutable treasury;constructor(address payable t){treasury=t;}function pay()external payable{treasury.transfer(msg.value);}}')));
 assert.ok(r.snapshot.code_identity.unbound_immutable_ranges.length);
 assert.equal(r.sections.engines.find(e=>e.engine==='sevm').status,'skipped');
 assert.ok(!r.sections.expressions.some(e=>e.value_basis==='compiler_immutable_reference_and_snapshot_bytes'));
 assert.ok(!r.sections.funds.some(f=>f.to_expression==='0x'+'0'.repeat(40)));
 assert.ok(r.sections.coverage.some(c=>c.reason==='deployment_bindings_unavailable'));
});
test('execution address and chain identity remain symbolic or explicit declared assumptions',()=>{
 const text='pragma solidity ^0.8.24;contract Example {uint public n; function update()external{require(msg.sender!=address(this));n=block.chainid;}function assemblyAddress()external view returns(address a){assembly{a:=address()}}}';
 const symbolic=analyze(input(source(text))),known=analyze(input(source(text,{environment:{chain_id:'11155111',execution_address:'0x'+'9'.repeat(40),evm_rules:'shanghai'}})));
 const a=symbolic.sections.state_transitions.find(t=>t.paths?.some(p=>p.writes.length));assert.ok(a?.complete,JSON.stringify(a));
 assert.ok(a.input_variables.some(v=>v.source==='symbolic chain identity'||v.name==='chain_id'||JSON.stringify(v).includes('symbolic chain identity')));
 assert.equal(known.snapshot.chain_id,undefined);assert.equal(known.snapshot.storage_address,undefined);
 assert.ok(known.sections.state_transitions.some(t=>t.assumptions.includes('declared_chain_id:11155111')));
 assert.ok(known.sections.state_transitions.some(t=>t.assumptions.includes('declared_execution_address:0x'+'9'.repeat(40))));
});
test('creation bytes preserve their phase and cannot become runtime methods',()=>{
 const code='0x600160005360016000f3',r=analyze(input({kind:'bytecode',bytecode:code,code_phase:'creation'}));
 assert.equal(r.snapshot.code_phase,'creation');assert.equal(r.sections.methods.length,0);
 assert.ok(r.sections.frontier.some(f=>f.kind==='creation_runtime'));assert.equal(r.sections.artifacts.find(a=>a.kind==='bytecode').text,code);
});
test('submitted source binds valid metadata differences while rejecting changed executable bytes',()=>{
 const normalized=normalizeSubmittedInput(input(source(simple))),actual=Buffer.from(normalized.input.bytecode.slice(2),'hex');
 actual[actual.length-20]^=1;
 const bound=input(source(simple));bound.snapshot.identity_kind='observed_deployment';bound.snapshot.storage_address='0x'+'8'.repeat(40);bound.bytecode='0x'+actual.toString('hex');
 const result=analyze(bound);assert.equal(result.sections.engines.find(e=>e.engine==='solc-ast').status,'completed');
 assert.equal(result.sections.source_verification[0].metadata_exact,false);assert.equal(result.sections.source_verification[0].basis,'runtime_code_match_with_valid_trailing_metadata_difference');
 actual[0]^=1;bound.bytecode='0x'+actual.toString('hex');const wrong=analyze(bound);
 assert.equal(wrong.sections.engines.find(e=>e.engine==='solc-ast').status,'failed');assert.ok(wrong.sections.conflicts.some(c=>c.kind==='source_mismatch'));
});
test('state dependencies resolve nested ledger/storage references to physical state identities',()=>{
 const r=analyze(input(source('pragma solidity ^0.8.24;contract Example {uint public x;mapping(address=>uint)public balanceOf;event Transfer(address indexed from,address indexed to,uint amount);function set(uint n)external{x=n;}function get()external view returns(uint){return x;}function transfer(address to,uint n)external{balanceOf[msg.sender]-=n;balanceOf[to]+=n;emit Transfer(msg.sender,to,n);}}')));
 const storage=new Map(r.sections.storage.map(s=>[s.storage_id,s]));
 const stateMethods=r.sections.methods.filter(m=>m.storage_ids?.length);assert.ok(stateMethods.length>0);
 for(const method of stateMethods){
  assert.ok(method.analysis_dependencies.storage_ids.length);
  for(const ref of method.analysis_dependencies.storage_ids)assert.ok(method.analysis_dependencies.state_ids.includes(storage.get(ref).state_id));
 }
 const ledger=r.sections.funds.find(f=>f.ledger_effects?.length);assert.ok(ledger);
 for(const effect of ledger.ledger_effects){assert.ok(ledger.analysis_dependencies.storage_ids.includes(effect.storage_id));assert.ok(ledger.analysis_dependencies.state_ids.includes(storage.get(effect.storage_id).state_id));}
});
test('submitted input digest, observed identity contamination and runtime binding conflicts fail closed',()=>{
 const request=input(source(simple));request.submitted_input_json+=' ';assert.throws(()=>analyze(request),/DIGEST_MISMATCH/);
 const contaminated=input({kind:'bytecode',bytecode:'0x00',code_phase:'runtime'});contaminated.snapshot.block_number=0;assert.throws(()=>analyze(contaminated),/OBSERVED_DEPLOYMENT/);
 const bound=input({kind:'bytecode',bytecode:'0x00',code_phase:'runtime'});bound.snapshot.identity_kind='observed_deployment';bound.bytecode='0x6000';assert.throws(()=>analyze(bound),/SNAPSHOT_MISMATCH/);
});
test('a conditional scalar fee is represented by its two captured source values',()=>{
 const r=analyze(input(source('pragma solidity ^0.8.24;contract Example {function pay(address payable to,bool fee)external payable{uint n=msg.value;if(fee)n=n/2;to.transfer(n);}}')));
 const fund=r.sections.funds.find(f=>f.kind==='native_payment');assert.ok(fund);
 const derivation=r.sections.dataflow.find(d=>d.dataflow_id===fund.amount_derivation_id);assert.deepEqual(derivation.unresolved,[]);
 const evaluate=new Function('fee','msg','return '+fund.expanded_amount_expression.replace(/\b\d+\b/g,'$&n'));
 for(const fee of[false,true])for(const value of[0n,1n,7n,9007199254740993n])assert.equal(evaluate(fee,{value}),fee?value/2n:value);
 assert.ok(r.sections.source_scalar_phis.some(p=>fund.analysis_dependencies.expression_ids.includes(p.value_expression_id)));
});

test('an unresolved conditional update reaches the fund and its mechanism edge',()=>{
 const r=analyze(input(source('pragma solidity ^0.8.24;contract Example {function pay(address payable to,bool fee)external payable{uint n=msg.value;if(fee)n++;to.transfer(n);}}')));
 const fund=r.sections.funds.find(f=>f.kind==='native_payment');assert.ok(fund);
 const derivation=r.sections.dataflow.find(d=>d.dataflow_id===fund.amount_derivation_id);assert.ok(derivation.unresolved.length);
 assert.equal(fund.analysis_dependencies.support,'pending');assert.ok(fund.analysis_dependencies.dataflow_ids.includes(derivation.dataflow_id));
 const edge=r.sections.flows.find(e=>e.fund_id===fund.fund_id);assert.ok(edge);assert.equal(edge.analysis_dependencies.support,'pending');
 const affected=r.sections.analysis_gaps.filter(g=>g.detail.dataflow_id===derivation.dataflow_id).flatMap(g=>g.affected_conclusion_ids);
 assert.ok(affected.includes(fund.analysis_dependencies.conclusion_id));assert.ok(affected.includes(edge.analysis_dependencies.conclusion_id));
});
test('each guard and mechanism edge keeps its own stable conclusion identity',()=>{
 const r=analyze(input(source('pragma solidity ^0.8.24;contract Example {uint public x;function set(uint n)external{require(n>0);require(n<100);x=n;}function get()external view returns(uint){return x;}}')));
 for(const section of ['permissions','flows']){
  const rows=r.sections[section];assert.ok(rows.length>1);
  assert.equal(new Set(rows.map(row=>row.analysis_dependencies.conclusion_id)).size,rows.length);
  for(const row of rows)assert.ok(r.sections.conclusions.some(c=>c.section===section&&c.conclusion_id===row.analysis_dependencies.conclusion_id));
 }
});
test('large dependency transport preserves every conclusion and gap byte-for-byte',()=>{
 const r=analyze(input(source(simple))),before=JSON.parse(JSON.stringify({schema_version:'conclusion-dependencies-v1',snapshot:r.snapshot,conclusions:r.sections.conclusions,analysis_gaps:r.sections.analysis_gaps}));
 compactConclusionDependencies(r,1);const index=r.sections.dependency_artifacts[0],artifact=r.sections.artifacts.find(a=>a.artifact_id===index.artifact_id),bytes=Buffer.from(artifact.content_base64,'base64'),raw=gunzipSync(bytes);
 assert.equal(createHash('sha256').update(bytes).digest('hex'),artifact.sha256);assert.equal(createHash('sha256').update(raw).digest('hex'),index.uncompressed_sha256);assert.equal(raw.length,index.uncompressed_bytes);
 const full=JSON.parse(raw);assert.deepEqual(full,before);
 for(const section of ['methods','permissions','funds','flows'])for(const row of r.sections[section])if(row.analysis_dependencies){const d=row.analysis_dependencies;assert.equal(d.dependency_artifact_id,artifact.artifact_id);assert.equal(full.conclusions[d.dependency_record].conclusion_id,d.conclusion_id);}
});
test('relative compiler source aliases retain their original identity without allowing traversal',()=>{
 const material=source(simple);material.source.files={'./Example.sol':simple};material.source.target='./Example.sol:Example';const r=analyze(input(material));
 assert.ok(r.sections.methods.some(m=>m.signature==='set(uint256)'));assert.ok(r.sections.artifacts.some(a=>a.kind==='submitted_source'&&a.filename==='./Example.sol'));
 material.source.files={'./../Example.sol':simple};material.source.target='./../Example.sol:Example';assert.throws(()=>analyze(input(material)),/SOURCE_PATH_INVALID/);
});
