import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { analyze } from '../analyze.mjs';
import { analyzeSource } from '../source.mjs';
import { analyzeBytecode } from '../bytecode.mjs';
import { emptyResult, hash, id, makeMethod } from '../model.mjs';
import { attachConclusionDependencies } from '../conclusion-dependencies.mjs';
import { normalizeSubmittedInput } from '../input.mjs';
import { sevmRequest, sevmResponse, mergeSevmResponse, encodeSevmResponse, decodeSevmResponse } from '../bytecode-sevm-protocol.mjs';

const require = createRequire(import.meta.url), compiler = require('solc-0.8.30').version().split('.Emscripten')[0];
function fixture(text) {
  const material = {kind:'source',source:{language:'Solidity',files:{'Recovery.sol':text},compiler_version:compiler,
    settings:{optimizer:{enabled:true,runs:200},evmVersion:'shanghai'},target:'Recovery.sol:Recovery'}};
  const raw = JSON.stringify(material);
  return {submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256:hash(raw),
    engine_digest:'sha256:'+'a'.repeat(64),state_granularity:'symbolic_state',evm_rules:'shanghai'},engines:['whatsabi','sevm','solc-ast']};
}
const short = 'pragma solidity ^0.8.24; contract Recovery {uint public total; function first(uint n) external payable {require(n<100);total=n;payable(msg.sender).transfer(msg.value/2);} function last(uint n)external{total=n+3;} }';
const large = 'pragma solidity ^0.8.24; contract Recovery {uint public total;function first(uint n)external{require(n<100);total=n;}function heavy(uint n)external{total=n;'+
  'n=n+1;'.repeat(60)+'total=n;}function last(uint n)external{total=n+3;}}';
const faultPath = name => fileURLToPath(new URL('./fixtures/sevm-'+name+'.mjs',import.meta.url));

function assertReferences(result) {
  const sets = {evidence_ids:new Set(result.sections.evidence.map(r=>r.evidence_id)),step_ids:new Set(result.sections.steps.map(r=>r.step_id)),
    guard_ids:new Set(result.sections.permissions.map(r=>r.guard_id)),storage_ids:new Set(result.sections.storage.map(r=>r.storage_id)),
    call_ids:new Set(result.sections.calls.map(r=>r.call_id)),fund_ids:new Set(result.sections.funds.map(r=>r.fund_id))};
  for (const m of result.sections.methods) for (const [key,known] of Object.entries(sets)) for (const ref of m[key] || []) assert.ok(known.has(ref),key+':'+ref);
  const expressions = new Set(result.sections.expressions.map(r=>r.expression_id));
  for(const row of result.sections.expressions) for(const ref of [...(row.operands || []),row.binding_expression_id].filter(Boolean)) assert.ok(expressions.has(ref),ref);
  const artifacts = new Set(result.sections.artifacts.map(r=>r.artifact_id));
  for (const row of result.sections.evidence) if(row.location?.artifact_id) assert.ok(artifacts.has(row.location.artifact_id),row.location.artifact_id);
}

test('a failed source method rolls back all its body records and preserves earlier and later verified methods',() => {
  const normalized = normalizeSubmittedInput(fixture(large)).input, result = emptyResult(normalized.snapshot);
  analyzeSource(result,normalized.source,normalized.bytecode,{methodExpressions:80});
  const first = result.sections.methods.find(m=>m.name==='first'), last = result.sections.methods.find(m=>m.name==='last');
  const failed = result.sections.methods.find(m=>m.name==='heavy');
  assert.equal(first.source_checkpoint,'method_body_saved');assert.equal(last.source_checkpoint,'method_body_saved');
  assert.equal(failed.source_checkpoint,'compiled_entry_only');
  assert.ok(failed.completeness.unresolved.some(g=>g.code==='SOURCE_METHOD_EXPRESSION_BUDGET_EXCEEDED'));
  for(const key of ['steps','expressions','storage','calls','funds','permissions','definitions','claims'])assert.ok(!(result.sections[key] || []).some(r=>r.method_id===failed.method_id),key);
  assert.ok(result.sections.storage.some(r=>r.method_id===first.method_id&&r.access==='write'));
  assert.ok(result.sections.storage.some(r=>r.method_id===last.method_id&&r.access==='write'));
  assert.equal(result.sections.engines.find(e=>e.engine==='solc-ast').status,'partial');
  assert.equal(result.sections.engines.find(e=>e.engine==='source-state-verification').status,'skipped');
  assert.ok(result.sections.artifacts.some(a=>a.kind==='submitted_source'&&a.text===JSON.parse(fixture(large).submitted_input_json).source.files['Recovery.sol']));
  assertReferences(result);
});

test('global source exhaustion retains every compiler entry and rejects semantic publication for unprocessed methods',() => {
  const normalized = normalizeSubmittedInput(fixture(large)).input, result = emptyResult(normalized.snapshot);
  analyzeSource(result,normalized.source,normalized.bytecode,{totalExpressions:8});
  for (const name of ['first','heavy','last','total']) assert.ok(result.sections.methods.some(m=>m.name===name),name);
  assert.ok(result.sections.methods.some(m=>m.completeness.unresolved.some(g=>g.code==='SOURCE_TOTAL_EXPRESSION_BUDGET_EXCEEDED')));
  assertReferences(result);
  const timed = emptyResult(normalized.snapshot);
  analyzeSource(timed,normalized.source,normalized.bytecode,{methodTimeoutMs:0});
  assert.ok(timed.sections.methods.every(m=>m.completeness.unresolved.some(g=>g.code==='SOURCE_METHOD_TIME_BUDGET_EXCEEDED')));
  assertReferences(timed);
});

test('actual isolated SEVM retains bytecode conditions, state and payment effects with request-bound references',() => {
  const normalized = normalizeSubmittedInput(fixture(short)).input;
  delete normalized.source;
  const result = analyze(normalized), engine = result.sections.engines.find(e=>e.engine==='sevm');
  assert.ok(['completed','partial'].includes(engine.status),JSON.stringify(engine));
  assert.equal(engine.isolation.kind,'node_child_process');assert.ok(engine.isolation.peak_rss_bytes>0);
  assert.ok(result.sections.storage.some(s=>s.access==='write'));
  assert.ok(result.sections.funds.some(f=>f.kind==='native_payment'));
  assert.ok(result.sections.permissions.length);
  assertReferences(result);
});

for (const [name,code,extra] of [
  ['timeout','SEVM_CHILD_TIMEOUT',{timeoutMs:100}],
  ['oom','SEVM_CHILD_OOM',{heapMiB:16,timeoutMs:10000}],
  ['exit','SEVM_CHILD_EXIT',{}],['corrupt','SEVM_CHILD_INVALID_OUTPUT',{}],
  ['output-limit','SEVM_CHILD_OUTPUT_LIMIT',{maxBuffer:256}],
]) test('actual child '+name+' preserves valid source payments and checksum-bound artifacts',() => {
  const normal = analyze(fixture(short)), failed = analyze(fixture(short),{sevm:{workerPath:faultPath(name),...extra}});
  const engine = failed.sections.engines.find(e=>e.engine==='sevm');
  assert.equal(engine.error_code,code,JSON.stringify(engine));
  for(const section of ['methods','steps','expressions','storage','calls','funds','permissions']) {
    const sourceRows = r => r.sections[section].filter(row=>section!=='methods'||row.completeness.basis==='source');
    assert.deepEqual(sourceRows(failed),sourceRows(normal),section);
  }
  assert.ok(failed.sections.artifacts.some(a=>a.kind==='disassembly'&&a.sha256===hash(a.text)));
  assert.ok(failed.sections.artifacts.some(a=>a.kind==='submitted_source'&&a.sha256===hash(a.text)));
  assertReferences(failed);
});

test('source budget gaps reach recovered bytecode payments and their concrete conclusion identities',() => {
  const normalized = normalizeSubmittedInput(fixture(short)).input, result = emptyResult(normalized.snapshot);
  analyzeSource(result,normalized.source,normalized.bytecode,{methodExpressions:1});
  analyzeBytecode(result,normalized.bytecode);
  attachConclusionDependencies(result);
  assert.ok(result.sections.funds.length,'Actual symbolic CALL must be recovered');
  for(const fund of result.sections.funds) {
    assert.equal(fund.analysis_dependencies.support,'pending');
    assert.ok(result.sections.analysis_gaps.some(g=>g.reason==='METHOD_ANALYSIS_INCOMPLETE'&&g.affected_conclusion_ids.includes(fund.analysis_dependencies.conclusion_id)));
  }
  assertReferences(result);
});

test('invalid identity, duplicate records and dangling references fail before any parent mutation',() => {
  const normalized = normalizeSubmittedInput(fixture(short)).input;
  for(const fault of ['identity','dangling','duplicate']) {
    const parent = emptyResult(normalized.snapshot), request = sevmRequest(parent,normalized.bytecode,[],[]), child = emptyResult(normalized.snapshot);
    const method = makeMethod(child.snapshot,'dispatch');child.sections.methods.push(method);
    const response = sevmResponse(request,child,{});
    if(fault==='identity') response.payload.snapshot={...response.payload.snapshot,input_sha256:'f'.repeat(64)};
    if(fault==='dangling')response.payload.sections.steps.push({step_id:id('bad-step'),method_id:method.method_id,evidence_ids:[id('missing-evidence')]});
    if(fault==='duplicate')response.payload.sections.methods.push(structuredClone(method));
    response.sha256=hash(response.payload);
    const original = JSON.stringify(parent);
    assert.throws(()=>mergeSevmResponse(parent,request,response),/SEVM_PROTOCOL_INVALID/);
    assert.equal(JSON.stringify(parent),original);
  }
});

test('large child transport preserves every byte and remains bound to the original request',() => {
  const input=normalizeSubmittedInput(fixture(short)).input,parent=emptyResult(input.snapshot);
  const request=sevmRequest(parent,input.bytecode,[],[]),child=emptyResult(input.snapshot),text='完整 IR '+ 'abcdef'.repeat(20000);
  child.sections.artifacts.push({artifact_id:id('large-ir'),kind:'symbolic_ir',text,sha256:hash(text)});
  const original=sevmResponse(request,child,{}),wire=encodeSevmResponse(original,128),envelope=JSON.parse(wire);
  assert.equal(envelope.encoding,'sevm-child-gzip-v1');
  const decoded=decodeSevmResponse(envelope);assert.deepEqual(decoded.response,original);
  mergeSevmResponse(parent,request,decoded.response);assert.equal(parent.sections.artifacts[0].text,text);
  assert.equal(decoded.transport.expanded_bytes,Buffer.byteLength(JSON.stringify(original)));
});

test('compressed child length, SHA, CRC and expansion bounds reject corrupt or truncated output',() => {
  const original={payload:{body:'x'.repeat(20000)},sha256:'test'},valid=JSON.parse(encodeSevmResponse(original,128));
  for(const fault of ['length','compressed_sha','expanded_sha','expanded_limit','crc']) {
    const broken=structuredClone(valid);
    if(fault==='length')broken.byte_size++;
    if(fault==='compressed_sha')broken.sha256='0'.repeat(64);
    if(fault==='expanded_sha')broken.expanded_sha256='0'.repeat(64);
    if(fault==='expanded_limit')broken.expanded_bytes=(128<<20)+1;
    if(fault==='crc'){const bytes=Buffer.from(broken.content_base64,'base64');bytes[bytes.length-5]^=1;broken.content_base64=bytes.toString('base64');broken.sha256=createHash('sha256').update(bytes).digest('hex');}
    assert.throws(()=>decodeSevmResponse(broken),fault);
  }
});
