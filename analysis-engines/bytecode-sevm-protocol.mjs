import { hash, makeMethod, RULE_VERSION } from './model.mjs';
import { createHash } from 'node:crypto';
import { gzipSync, gunzipSync } from 'node:zlib';

export const SEVM_PROTOCOL = 'sevm-isolation-v1';
export const SEVM_OUTPUT_LIMIT = 32 << 20;
export const SEVM_EXPANDED_LIMIT = 128 << 20;
const sha = bytes => createHash('sha256').update(bytes).digest('hex');
const allowedSections = ['methods','steps','expressions','storage','calls','permissions','funds','flows','evidence','coverage','engines','artifacts'];
const appendFields = ['evidence_ids','step_ids','guard_ids','storage_ids','call_ids','fund_ids','return_ids','failure_ids'];
const replaceFields = ['entry_pc','signature_candidates','parameters','completeness'];
const primary = {methods:'method_id',steps:'step_id',expressions:'expression_id',storage:'storage_id',calls:'call_id',
  permissions:'guard_id',funds:'fund_id',flows:'edge_id',evidence:'evidence_id',artifacts:'artifact_id'};
const plain = value => value && typeof value === 'object' && !Array.isArray(value);
const requireValid = (valid, reason) => { if (!valid) throw new Error('SEVM_PROTOCOL_INVALID:' + reason); };

export function encodeSevmResponse(response, compressionThreshold = SEVM_OUTPUT_LIMIT) {
  const raw = Buffer.from(JSON.stringify(response));
  if (raw.length > SEVM_EXPANDED_LIMIT) throw new Error('SEVM_EXPANDED_OUTPUT_LIMIT');
  if (raw.length <= compressionThreshold) return raw;
  const compressed = gzipSync(raw,{level:6});
  const wire = Buffer.from(JSON.stringify({encoding:'sevm-child-gzip-v1',protocol:SEVM_PROTOCOL,
    byte_size:compressed.length,sha256:sha(compressed),expanded_bytes:raw.length,expanded_sha256:sha(raw),content_base64:compressed.toString('base64')}));
  if (wire.length > SEVM_OUTPUT_LIMIT) throw new Error('SEVM_OUTPUT_LIMIT');
  return wire;
}

export function decodeSevmResponse(response) {
  const wireBytes = Buffer.byteLength(JSON.stringify(response));
  requireValid(wireBytes <= SEVM_OUTPUT_LIMIT,'wire_limit');
  if (!response?.encoding) return {response,transport:{encoding:'json',wire_bytes:wireBytes,expanded_bytes:wireBytes}};
  requireValid(response.encoding === 'sevm-child-gzip-v1' && response.protocol === SEVM_PROTOCOL,'transport_version');
  requireValid(Number.isSafeInteger(response.byte_size) && response.byte_size > 0 && response.byte_size <= SEVM_OUTPUT_LIMIT &&
    Number.isSafeInteger(response.expanded_bytes) && response.expanded_bytes > 0 && response.expanded_bytes <= SEVM_EXPANDED_LIMIT,'expanded_limit');
  requireValid(typeof response.content_base64 === 'string','compressed_content');
  const compressed=Buffer.from(response.content_base64,'base64');
  requireValid(compressed.length === response.byte_size && sha(compressed) === response.sha256,'compressed_integrity');
  const raw=gunzipSync(compressed,{maxOutputLength:SEVM_EXPANDED_LIMIT});
  requireValid(raw.length === response.expanded_bytes && sha(raw) === response.expanded_sha256,'expanded_integrity');
  const decoded=JSON.parse(raw.toString('utf8'));
  requireValid(!decoded?.encoding,'nested_transport');
  return {response:decoded,transport:{encoding:response.encoding,wire_bytes:wireBytes,expanded_bytes:raw.length,
    compressed_sha256:response.sha256,expanded_sha256:response.expanded_sha256}};
}

export function sevmRequest(result, bytecode, signatures, selectors) {
  const methods = result.sections.methods.filter(m => m.completeness?.basis === 'source' &&
    m.entry_kind !== 'constructor' && !m.code_phase?.startsWith('creation') && typeof m.entry === 'string').map(m => ({
    ...makeMethod(result.snapshot, m.entry, m.selector, m.name),
    entry_kind:m.entry_kind,dispatch_role:m.dispatch_role,
    completeness:structuredClone(m.completeness),parameters:structuredClone(m.parameters),
  }));
  const payload = JSON.parse(JSON.stringify({version:SEVM_PROTOCOL,rule_version:RULE_VERSION,
    snapshot:result.snapshot,bytecode,signatures,selectors,source_methods:methods}));
  return {payload,sha256:hash(payload)};
}

export function validateSevmRequest(request) {
  const p = request?.payload;
  requireValid(plain(p) && request.sha256 === hash(p), 'request_digest');
  requireValid(p.version === SEVM_PROTOCOL && p.rule_version === RULE_VERSION, 'request_version');
  requireValid(plain(p.snapshot) && /^0x(?:[0-9a-fA-F]{2}){1,49152}$/.test(p.bytecode), 'request_identity');
  requireValid(Array.isArray(p.source_methods) && p.source_methods.length <= 20000 && Array.isArray(p.signatures) && Array.isArray(p.selectors), 'request_arrays');
  const seen = new Set();
  for (const m of p.source_methods) {
    requireValid(plain(m) && m.method_id === makeMethod(p.snapshot,m.entry,m.selector,m.name).method_id && !seen.has(m.method_id), 'seed_method');
    requireValid(m.completeness?.basis === 'source' && Array.isArray(m.parameters), 'seed_source');
    seen.add(m.method_id);
  }
  return p;
}

export function sevmResponse(request, result, metrics) {
  const seeds = new Map(request.payload.source_methods.map(m => [m.method_id,m]));
  const updates = [];
  for (const m of result.sections.methods) if (seeds.has(m.method_id)) {
    const seed = seeds.get(m.method_id), changes = {};
    for (const key of [...appendFields,...replaceFields]) if (JSON.stringify(m[key]) !== JSON.stringify(seed[key])) changes[key] = m[key];
    if (Object.keys(changes).length) updates.push({method_id:m.method_id,changes});
  }
  const sections = Object.fromEntries(allowedSections.map(key => [key,(result.sections[key] || []).filter(row => key !== 'methods' || !seeds.has(row.method_id))]));
  const payload = JSON.parse(JSON.stringify({version:SEVM_PROTOCOL,rule_version:RULE_VERSION,request_sha256:request.sha256,
    snapshot:result.snapshot,sections,method_updates:updates,metrics}));
  return {payload,sha256:hash(payload)};
}

// Validate every record and reference before changing the parent's saved model.
export function mergeSevmResponse(result, request, response) {
  const p = response?.payload;
  requireValid(plain(p) && response.sha256 === hash(p), 'response_digest');
  requireValid(p.version === SEVM_PROTOCOL && p.rule_version === RULE_VERSION && p.request_sha256 === request.sha256, 'response_binding');
  requireValid(hash(p.snapshot) === hash(result.snapshot), 'snapshot_identity');
  requireValid(plain(p.sections) && Object.keys(p.sections).every(k => allowedSections.includes(k)) && Array.isArray(p.method_updates), 'sections');
  const known = new Map();
  for (const [section,key] of Object.entries(primary)) {
    const ids = new Set((result.sections[section] || []).map(row => row[key]));
    const incoming = new Set();
    requireValid(Array.isArray(p.sections[section]) && p.sections[section].length <= 200000, 'section_' + section);
    for (const row of p.sections[section]) {
      requireValid(plain(row) && typeof row[key] === 'string' && /^[a-f0-9]{40}$/.test(row[key]), 'row_identity_' + section);
      requireValid(!ids.has(row[key]) && !incoming.has(row[key]), 'duplicate_' + section);
      if (section === 'methods') requireValid(row.method_id === makeMethod(result.snapshot,row.entry,row.selector,row.name).method_id, 'new_method_identity');
      incoming.add(row[key]);
    }
    known.set(key, new Set([...ids,...incoming]));
  }
  for (const key of ['engines','coverage']) requireValid(Array.isArray(p.sections[key]) && p.sections[key].length < 1000, key);
  for (const a of p.sections.artifacts) requireValid(typeof a.text === 'string' && a.sha256 === hash(a.text), 'artifact_digest');
  const parents = new Map(result.sections.methods.map(m => [m.method_id,m]));
  const updated = new Set();
  for (const u of p.method_updates) {
    const parent = parents.get(u.method_id);
    requireValid(parent && !updated.has(u.method_id) && plain(u.changes), 'method_update_identity');
    requireValid(Object.keys(u.changes).every(k => [...appendFields,...replaceFields].includes(k)), 'method_update_fields');
    for (const key of appendFields) if (key in u.changes) requireValid(Array.isArray(u.changes[key]), 'method_append_field');
    if (u.changes.parameters) requireValid(Array.isArray(u.changes.parameters) && u.changes.parameters.length >= parent.parameters.length && parent.parameters.every((v,i) => hash(v) === hash(u.changes.parameters[i])), 'source_parameters_changed');
    if (u.changes.completeness) requireValid(u.changes.completeness.basis === 'mixed_source_and_bytecode' &&
      parent.completeness.unresolved.every(v => u.changes.completeness.unresolved.some(w => hash(v) === hash(w))), 'source_completeness_changed');
    updated.add(u.method_id);
  }
  const refs = {evidence_id:'evidence_id',expression_id:'expression_id',storage_id:'storage_id',call_id:'call_id',fund_id:'fund_id',
    step_id:'step_id',guard_id:'guard_id',artifact_id:'artifact_id',method_id:'method_id',target_method_id:'method_id'};
  const walk = (value,key = '') => {
    if (Array.isArray(value)) { for (const item of value) walk(item,key.replace(/_ids$/,'_id')); return; }
    if (plain(value)) { for (const [name,item] of Object.entries(value)) if (name !== 'raw_node') walk(item,name); return; }
    if (typeof value !== 'string' || !value) return;
    const ref = key === 'operands' ? 'expression_id' : Object.entries(refs).find(([suffix]) => key === suffix || key.endsWith('_' + suffix))?.[1];
    if (ref) requireValid(known.get(ref)?.has(value), 'dangling_' + key);
  };
  walk(p.sections);
  for (const u of p.method_updates) walk(u.changes);
  for (const u of p.method_updates) {
    const parent = parents.get(u.method_id);
    for (const [key,value] of Object.entries(u.changes)) {
      if (appendFields.includes(key)) parent[key] = [...new Set([...(parent[key] || []),...value])];
      else parent[key] = value;
    }
  }
  for (const [key,rows] of Object.entries(p.sections)) { result.sections[key] ||= []; result.sections[key].push(...rows); }
  return p.metrics;
}
