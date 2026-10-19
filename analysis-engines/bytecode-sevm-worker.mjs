import { readFileSync } from 'node:fs';
import { emptyResult } from './model.mjs';
import { analyzeSevm } from './bytecode-sevm.mjs';
import { validateSevmRequest, sevmResponse, encodeSevmResponse } from './bytecode-sevm-protocol.mjs';

try {
  const raw = readFileSync(0);
  if (raw.length > 16 << 20) throw new Error('SEVM_INPUT_LIMIT');
  const request = JSON.parse(raw.toString('utf8')), input = validateSevmRequest(request);
  const result = emptyResult(input.snapshot);
  result.sections.methods = structuredClone(input.source_methods);
  analyzeSevm(result,input.bytecode,input.signatures,input.selectors);
  const usage = process.resourceUsage();
  const output = encodeSevmResponse(sevmResponse(request,result,{peak_rss_bytes:usage.maxRSS * 1024,
    cpu_user_us:usage.userCPUTime,cpu_system_us:usage.systemCPUTime}));
  process.stdout.write(output);
} catch (error) {
  process.stderr.write(String(error.message).slice(0,1000) + '\n');
  process.exitCode = 1;
}
