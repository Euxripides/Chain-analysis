import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { hash, id, evidence, unique } from '../model.mjs';

export function pythonOperation(payload, timeout = 90000) {
  const python = process.env.CONTRACT_PYTHON || '/opt/contract-python/bin/python';
  const run = spawnSync(python, [fileURLToPath(new URL('./python-engine.py', import.meta.url))], {
    input: JSON.stringify(payload), encoding: 'utf8', timeout, maxBuffer: 16 << 20,
    env: { ...process.env, PYTHONDONTWRITEBYTECODE: '1' },
  });
  if (run.status !== 0) throw new Error((run.error?.code || 'PYTHON_ENGINE_FAILED') + ':' + (run.stderr || '').slice(-800));
  return JSON.parse(run.stdout, (_key, value, context) =>
    typeof value === 'number' && !Number.isSafeInteger(value) && /^-?\d+$/.test(context?.source || '') ? context.source : value);
}

export function analyzeSlither(result, source) {
  const started = performance.now();
  const engine = { engine: 'slither', adapter_version: 'slithir-1', status: 'failed' };
  try {
    const output = pythonOperation({ operation: 'slither', source });
    Object.assign(engine, { version: output.version, source_commit: output.source_commit,
      crytic_compile_version: output.crytic_compile_version, crytic_compile_commit: output.crytic_compile_commit,
      operation_count: output.operation_count });
    const text = JSON.stringify(output), artifact_id = id('slithir', result.snapshot.code_hash);
    result.sections.artifacts.push({ artifact_id, kind: 'slithir', filename: 'slithir.json',
      media_type: 'application/json', text, sha256: hash(text) });
    result.sections.ir ||= [];
    for (const fn of output.functions) {
      const methods = result.sections.methods.filter(m => m.signature === fn.signature && m.dispatch_role !== 'overridden_source_definition'&&
        (!m.source_location||!fn.location||(m.source_location.file===fn.location.file&&m.source_location.start===fn.location.start)));
      for (const method of methods) {
        const references = [];
        for (const node of fn.nodes) {
          const e = evidence(result, method, 'slithir', { artifact_id, ...node.location, node_id: node.node_id }, fn.full_name);
          for (const operation of node.operations) {
            const ir_id = id(method.method_id, 'slithir', node.node_id, operation.index);
            result.sections.ir.push({ ir_id, method_id: method.method_id, engine: 'slither',
              block: node.node_id, successors: node.successors, location: node.location,
              op: operation.op, text: operation.text, reads: operation.reads, lvalue: operation.lvalue,
              evidence_ids: [e], basis: 'recompiled_source_ir' });
            references.push(ir_id);
          }
        }
        method.ir_ids = unique([...(method.ir_ids || []), ...references]);
        method.additional_analysis_bases = unique([...(method.additional_analysis_bases || []), 'slithir']);
      }
    }
    engine.status = 'completed'; engine.artifact_id = artifact_id;
  } catch (error) {
    engine.error_code = 'SLITHER_ANALYSIS_FAILED'; engine.error_message = String(error.message).slice(0, 800);
    result.sections.coverage.push({ dimension: 'slithir', complete: false, reason: engine.error_message });
  }
  engine.duration_ms = Math.round(performance.now() - started);
  result.sections.engines.push(engine);
}
