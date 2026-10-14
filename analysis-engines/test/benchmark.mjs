import { mkdirSync, writeFileSync } from 'node:fs';
import { analyze } from '../analyze.mjs';
import { compileFixture } from './fixtures.mjs';
import { stringify } from '../model.mjs';
const rows = [];
mkdirSync('../acceptance/contract-analysis/engines', { recursive: true });
for (const name of ['AnalystVault', 'NativeLedger', 'Misleading', 'DelegateFixture']) for (const optimizer of [false, true]) for (const channel of ['source', 'hidden_source']) {
  const input = compileFixture(name, optimizer); if (channel === 'hidden_source') delete input.source;
  const result = analyze(input), key = `${name}-${optimizer ? 'optimized' : 'plain'}-${channel}`;
  writeFileSync(`../acceptance/contract-analysis/engines/${key}.json`, stringify(result));
  rows.push({ sample: key, code_hash: input.snapshot.code_hash, selectors: result.sections.methods.filter(m => m.selector).length,
    parsed: result.sections.methods.filter(m => m.step_ids.length).length, unresolved: result.sections.methods.filter(m => m.completeness.unresolved.length).length,
    metrics: result.metrics, engines: result.sections.engines });
}
writeFileSync('../acceptance/contract-analysis/engine-benchmark.json', stringify(rows));
process.stdout.write(stringify(rows.map(r => ({ sample: r.sample, selectors: r.selectors, parsed: r.parsed, unresolved: r.unresolved, ms: r.metrics.duration_ms }))));
