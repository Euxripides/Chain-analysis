import { readFileSync } from 'node:fs';
import { analyze } from './analyze.mjs';
import { stringify } from './model.mjs';
import { replay } from './replay.mjs';
import { capabilities } from './capabilities.mjs';
import { analyzeProgram } from './program.mjs';
import {encodeEngineResult}from'./transport.mjs';
try {
  const raw = readFileSync(0);
  if (raw.byteLength > 24 * 1024 * 1024) throw new Error('ENGINE_INPUT_TOO_LARGE');
  const input=JSON.parse(raw.toString('utf8'),(_key,value,context)=>
    typeof value==='number'&&!Number.isSafeInteger(value)&&/^\d+$/.test(context?.source||'')?context.source:value);
  const result = input.operation === 'program' ? analyzeProgram(input) : input.operation === 'capabilities' ? capabilities(input.snapshot) : input.operation==='replay'?await replay(input):analyze(input);
  try { result.metrics.cgroup_memory_peak_bytes = readFileSync('/sys/fs/cgroup/memory.peak', 'utf8').trim(); } catch {}
  try { result.metrics.cgroup_cpu_stat = readFileSync('/sys/fs/cgroup/cpu.stat', 'utf8').trim(); } catch {}
  process.stdout.write(encodeEngineResult(result));
} catch (error) {
  process.stderr.write(String(error.message).slice(0, 1000) + '\n');
  process.exitCode = 1;
}
