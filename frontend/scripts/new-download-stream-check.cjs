const fs = require('node:fs');
const http = require('node:http');
const { chromium } = require('@playwright/test');

// Real browser EventSource, including inherited lastEventId on frames without id.
// All auth/REST data and the upstream stream are local fixtures; no provider calls.
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const now = () => new Date().toISOString();
const base = process.env.NEW_DOWNLOAD_UI_URL || 'http://127.0.0.1:3171';
const output = process.env.NEW_DOWNLOAD_EVIDENCE || '.work/new-flow/stream-baseline.json';
const baseline = process.env.NEW_DOWNLOAD_BASELINE === '1';

function detail(id, completed = false) {
  const status = completed ? 'COMPLETED' : 'RUNNING';
  return {
    job: { id, job_no: 'NEW-' + id, status, chain: 'bsc', datasets: ['transactions'], stats: {}, updated_at: now(), created_at: now(), source: 'user_download', error: '', requested_mode: 'safe', effective_mode: 'safe', range_type: 'block', reuse_policy: 'refresh', head_block: 100 },
    progress: { job_id: id, status, progress: completed ? 1 : 0.99, downloaded_rows: completed ? 10000 : 1000, total_shards: 100, done_shards: completed ? 100 : 99, downloaded_bytes: 1000, api_requests: 1, api_cost_usd: 0.00007, elapsed_seconds: 10, eta_seconds: completed ? 0 : 1, ts: now() },
    layers: { job: { status, updated_at: now(), error_safe: '' }, addresses: [], datasets: [], shards: [] }, subjects: [], datasets: [], shards: [], exports: [], performance: {}, source: {},
  };
}

(async () => {
  const streams = new Map();
  const server = http.createServer((req, res) => {
    res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Access-Control-Allow-Origin': base, 'Access-Control-Allow-Credentials': 'true', 'Cache-Control': 'no-cache' });
    res.write(': ready\n\n');
    const id = new URL(req.url, 'http://localhost').searchParams.get('job');
    streams.set(id, res);
    req.on('close', () => { if (streams.get(id) === res) streams.delete(id); });
  });
  await new Promise(resolve => server.listen(3172, '127.0.0.1', resolve));
  const browser = await chromium.launch({ channel: process.env.E2E_BROWSER_CHANNEL || 'msedge', headless: true });
  const results = [];
  try {
    for (const width of [1440, 390]) {
      const id = 'fresh-' + width;
      const page = await browser.newPage({ viewport: { width, height: width === 390 ? 844 : 900 } });
      const errors = [];
      let reads = 0, finished = false;
      page.on('pageerror', error => errors.push(error.message));
      await page.addInitScript(() => {
        const NativeEventSource = window.EventSource;
        window.__frames = [];
        window.EventSource = class extends NativeEventSource {
          constructor(url) {
            const job = new URL(url, location.href).pathname.split('/').at(-2);
            super('http://127.0.0.1:3172/?job=' + encodeURIComponent(job), { withCredentials: true });
            this.addEventListener('update', event => window.__frames.push({ id: event.lastEventId, payloadId: JSON.parse(event.data).event_id ?? null }));
          }
        };
      });
      await page.route('**/api/**', async route => {
        const path = new URL(route.request().url()).pathname;
        let data = [];
        if (path === '/api/auth/v1/session') data = { user: { id: 'qa', display_name: 'QA', email: 'qa@test.invalid' }, email: 'qa@test.invalid', display_name: 'QA', audience: 'user', session_id: 'qa', organization_id: '11111111-1111-1111-1111-111111111111', workspace_id: '22222222-2222-2222-2222-222222222222', roles: [], permissions: ['job:read', 'job:list:self', 'notification:list', 'download:create', 'export:create'], expires_at: '2099-01-01T00:00:00Z', absolute_expires_at: '2099-01-01T00:00:00Z', must_change_password: false, mfa_satisfied: true };
        else if (path === '/api/v2/jobs/' + id) { reads++; data = detail(id, finished); }
        else if (path.includes('workspaces')) data = { workspaces: [] };
        await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ success: true, data, meta: { request_id: 'qa', server_time: now() } }) });
      });
      await page.goto(base + '/app-v2/jobs/' + id);
      await page.getByText('NEW-' + id, { exact: false }).first().waitFor();
      for (let n = 0; n < 100 && !streams.has(id); n++) await sleep(25);
      const stream = streams.get(id);
      if (!stream) throw Error('Native EventSource did not connect');
      for (let n = 1; n <= 9; n++) {
        const payload = { ...detail(id).progress, downloaded_rows: n * 1000, rows_per_sec: 123, fetched_shards: 99, ingested_shards: 99, validated_shards: 99, fetched_rows: n * 1000, ingested_rows: n * 1000, validated_rows: n * 1000, event_type: 'progress', ...(n === 1 ? { event_id: 700 } : {}) };
        stream.write((n === 1 ? 'id: 700\n' : '') + 'event: update\ndata: ' + JSON.stringify(payload) + '\n\n');
        await sleep(180);
      }
      await sleep(400);
      const rowText = await page.locator('.cs-jobprogress__fact').filter({ hasText: '已下载行数' }).locator('.cs-jobprogress__factvalue').innerText();
      const speedText = await page.locator('.cs-jobprogress__fact').filter({ hasText: '当前速度' }).locator('.cs-jobprogress__factvalue').innerText();
      const frames = await page.evaluate(() => window.__frames);
      const liveRows = Number(rowText.replaceAll(',', ''));
      const readsDuringProgress = reads;
      await page.screenshot({ path: output.replace('.json', '-' + width + '.png'), fullPage: true });
      let stalledEtaUnknown = false;
      stream.write('event: update\ndata: ' + JSON.stringify({ ...detail(id).progress, downloaded_rows: 9000, rows_per_sec: 0, tail_eta_seconds: 3, event_type: 'progress' }) + '\n\n');
      await sleep(250);
      stalledEtaUnknown = (await page.locator('.cs-jobprogress__fact').filter({ hasText: '预计剩余' }).locator('.cs-jobprogress__factvalue').innerText()) === '—';
      const phase = { ...detail(id).progress, event_type: 'progress', downloaded_rows: 10000, rows_per_sec: 0, fetched_shards: 100, fetched_rows: 10000, ingested_rows: 5000 };
      stream.write('event: update\ndata: ' + JSON.stringify(phase) + '\n\n');
      let ingestStage = false, validationStage = false;
      if (!baseline) {
        await page.getByText('正在写入下载数据', { exact: true }).waitFor({ timeout: 2500 });
        ingestStage = true;
      }
      stream.write('event: snapshot\ndata: ' + JSON.stringify({ ...phase, ingested_rows: 10000, ts: now() }) + '\n\n');
      if (!baseline) {
        await page.getByText('正在校验下载数据', { exact: true }).waitFor({ timeout: 2500 });
        validationStage = true;
      }
      finished = true;
      stream.write('id: 701\nevent: update\ndata: ' + JSON.stringify({ job_id: id, status: 'COMPLETED', event_type: 'jobs', event_id: 701, ts: now() }) + '\n\n');
      await page.getByTestId('job-completion-panel').waitFor({ timeout: 5000 });
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth);
      results.push({ width, liveRows, speedText, readsDuringProgress, inheritedIds: frames.slice(1).every(frame => frame.id === '700' && frame.payloadId === null), stalledEtaUnknown, ingestStage, validationStage, complete: true, overflow, errors });
      await page.close();
    }
    const pass = results.every(result => result.liveRows === 9000 && result.speedText === '123 rows/s' && result.readsDuringProgress <= 2 && result.inheritedIds && result.stalledEtaUnknown && result.ingestStage && result.validationStage && result.complete && !result.overflow && !result.errors.length);
    fs.writeFileSync(output, JSON.stringify({ pass, baseline, providerCalls: 0, results }, null, 2));
    console.log(JSON.stringify({ pass, baseline, results }));
    if (!baseline && !pass) process.exitCode = 1;
  } finally {
    await browser.close();
    for (const stream of streams.values()) stream.end();
    await new Promise(resolve => server.close(resolve));
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
