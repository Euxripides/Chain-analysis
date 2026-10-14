import { execFileSync } from 'node:child_process';
import { mkdirSync, writeFileSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { id, hash } from './model.mjs';
export function heimdall(result, bytecode) {
  const engine = { engine: 'heimdall', version: null, release: '0.9.3', digest: 'sha256:8d58c97f56d584d5d0737d0d0895fc8c68b7b8b2b680f2a7842245789f0b4868', adapter_version: 'heimdall-1', status: 'not_installed' };
  const binary = process.env.HEIMDALL_BINARY;
  if (!binary) { result.sections.engines.push(engine); return; }
  const started = performance.now();
  const dir = '/tmp/contract-heimdall'; mkdirSync(dir, { recursive: true }); writeFileSync(dir + '/runtime.hex', bytecode);
  try {
    engine.version=execFileSync(binary,['--version'],{timeout:10000,encoding:'utf8',maxBuffer:4096}).trim();
    execFileSync(binary, ['decompile', dir + '/runtime.hex', '--default', '--skip-resolving', '--include-sol', '--timeout', '2500', '--output', dir + '/output', '--color', 'never', '-q'], {
      timeout: 300000, maxBuffer: 1 << 20, cwd: dir, env: { PATH: '/usr/bin:/bin', HOME: '/tmp', RUST_LOG: 'error' },
    });
    let count = 0;
    const read = path => { for (const entry of readdirSync(path, { withFileTypes: true })) {
      const child = path + '/' + entry.name;
      if (entry.isDirectory()) read(child);
      else if (entry.isFile()) {
        if (statSync(child).size > 16 << 20) throw new Error('HEIMDALL_ARTIFACT_TOO_LARGE');
        const text = readFileSync(child, 'utf8');
        result.sections.artifacts.push({ artifact_id: id('heimdall', result.snapshot.code_hash, child.slice(dir.length)),
          kind: 'heimdall_output', filename: child.slice((dir + '/output/').length), media_type: child.endsWith('.json') ? 'application/json' : 'text/plain; charset=utf-8', text, sha256: hash(text) }); count++;
      }
    } };
    read(dir + '/output'); engine.status = count ? 'completed' : 'partial'; engine.artifact_count = count;
  } catch (error) { engine.status = 'failed'; engine.error_code = error.signal || error.code || 'HEIMDALL_FAILED'; engine.error_message = String(error.message).slice(0, 500); }
  engine.duration_ms = Math.round(performance.now() - started); result.sections.engines.push(engine);
}
