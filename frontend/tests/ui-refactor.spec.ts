import { expect, test } from '@playwright/test'

const sampleAddress = '0x9b77e3b4f85c7cea7da451f454895edbd10b80dd'
const control = { state: 'NORMAL', reason: '最近 5 分钟指标正常', since: '2026-09-03T01:00:00Z', automatic_protection: true, kill_switch: false, background_paused: false, window_requests: 1658, window_failures: 48, error_rate: .0001, p95_ms: 3533.2 }
const overview = { window: { from: '2026-09-02T00:00:00Z', to: '2026-09-03T00:00:00Z' }, freshness: '2026-09-03T01:12:49Z', metrics: { logical_calls: 1658, provider_calls: 949, errors: 48, retries: 24, cache_hits: 8, success_rate: .9498, credits: 536000, estimated_cost_usd: .0536, p50_ms: 802, p95_ms: 3533.2, p99_ms: 7231.4 }, trend: [{ time: '2026-09-02T10:00:00Z', requests: 120, errors: 3, cost_usd: .01, p95_ms: 900 }, { time: '2026-09-02T14:00:00Z', requests: 360, errors: 4, cost_usd: .02, p95_ms: 2200 }, { time: '2026-09-02T20:00:00Z', requests: 170, errors: 2, cost_usd: .02, p95_ms: 5400 }], control, scope: { usage: 'internal', account: 'official', credits: 'estimate', official_comparable: false, comparison_reason: '' }, sources: {} }
const jobs = [
  { id: 'job-1', job_no: 'DL-20260902-0050', status: 'RUNNING', priority: 1, source: 'ui', chain: 'bsc', range_type: 'block', from_block: 41123145, to_block: 41153145, datasets: ['token_transfers'], reuse_policy: 'reuse', head_block: 0, error: '', stats: { done_shards: 15, total_shards: 24, downloaded_rows: 78324, api_requests: 2184, api_cost_usd: .0536, elapsed_seconds: 512, subjects: 1, stage: '请求拉取' }, created_at: '2026-09-03T00:42:11Z', updated_at: '2026-09-03T00:50:43Z' },
  { id: 'job-2', job_no: 'DL-20260902-0049', status: 'COMPLETED', priority: 1, source: 'ui', chain: 'eth', range_type: 'block', datasets: ['transactions', 'token_transfers'], reuse_policy: 'reuse', head_block: 0, error: '', stats: { done_shards: 18, total_shards: 18, downloaded_rows: 234567, api_requests: 4321, api_cost_usd: .12, elapsed_seconds: 920, subjects: 2 }, created_at: '2026-09-02T20:42:11Z', updated_at: '2026-09-02T21:01:43Z' },
  { id: 'job-3', job_no: 'DL-20260902-0048', status: 'FAILED', priority: 1, source: 'ui', chain: 'polygon', range_type: 'block', datasets: ['logs'], reuse_policy: 'reuse', head_block: 0, error: 'provider timeout', stats: { done_shards: 3, total_shards: 16, downloaded_rows: 1240, api_requests: 123, api_cost_usd: .008, elapsed_seconds: 230, subjects: 1, failed_stage: '请求拉取' }, created_at: '2026-09-02T18:42:11Z', updated_at: '2026-09-02T18:48:43Z' },
]
const jobDetail = { job: jobs[0], subjects: [{ id: 'subject-1', job_id: 'job-1', subject_type: 'address', subject: sampleAddress, status: 'RUNNING', progress: .63, stats: {} }], datasets: [{ id: 'dataset-1', job_id: 'job-1', subject_id: 'subject-1', dataset: 'token_transfers', status: 'RUNNING', progress: .63, weight: 1, stats: {} }], shards: Array.from({ length: 6 }, (_, index) => ({ id: `shard-${index}`, job_id: 'job-1', subject_id: 'subject-1', dataset_id: 'dataset-1', dataset: 'token_transfers', from_block: 41123145 + index * 5000, to_block: 41128144 + index * 5000, page_token: '', page_no: index + 1, status: index < 4 ? 'COMPLETED' : 'RUNNING', priority: 1, retries: 0, max_retries: 3, downloaded_rows: index < 4 ? 13054 : 0, downloaded_bytes: 1024, raw_object: index < 4 ? `raw/${index}.json` : '', error: '', updated_at: `2026-09-03T00:${45 + index}:43Z` })), progress: { job_id: 'job-1', status: 'RUNNING', progress: .63, downloaded_rows: 78324, downloaded_bytes: 12939428, api_requests: 2184, api_cost_usd: .0536, rows_per_sec: 153, bytes_per_sec: 1490000, coverage_hit_ratio: .678, elapsed_seconds: 512, eta_seconds: 301, total_shards: 24, done_shards: 15, ts: '2026-09-03T00:50:43Z' } }

test.beforeEach(async ({ page }) => {
  await page.route('**/api/v1/cases', (route) => route.fulfill({ json: { cases: [{ id: 'case-1', name: '盘古' }] } }))
  await page.route('**/api/v1/tags', (route) => route.fulfill({ json: { tags: [{ id: 'tag-1', name: '主钱包' }] } }))
  await page.route('**/api/v1/data-explorer/addresses**', (route) => route.fulfill({ json: { addresses: [{
    chain: 'bsc', address: sampleAddress, normalized_address: sampleAddress, display_name: '案件主地址',
    case_names: ['盘古'], tag_names: ['主钱包'], transaction_count: 243, token_transfer_count: 166,
    nft_transfer_count: 0, log_count: 77, total_activity_count: 486, last_data_block: 47000000,
    last_sync_at: '2026-09-03T02:30:00Z', sync_status: 'current',
  }], next_cursor: '' } }))
  await page.route('**/api/v1/data-explorer/query', (route) => route.fulfill({ json: { rows: [{
    tx_hash: '0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    from_address: sampleAddress,
    to_address: '0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
    amount: '12.5',
    token_symbol: 'USDT',
    token_address: '0xcccccccccccccccccccccccccccccccccccccc',
    timestamp: '2026-09-03T02:30:00Z',
  }], next_cursor: '', count: 1 } }))
  await page.route('**/api/v1/download-jobs/**', (route) => {
    const url = route.request().url()
    if (url.endsWith('/events')) return route.fulfill({ status: 200, contentType: 'text/event-stream', body: '' })
    if (url.endsWith('/pause') || url.endsWith('/resume') || url.endsWith('/cancel')) return route.fulfill({ json: { status: 'ok' } })
    return route.fulfill({ json: jobDetail })
  })
  await page.route('**/api/v1/download-jobs?**', (route) => route.fulfill({ json: { jobs } }))
})

test('download workspace is bounded and responsive', async ({ page }, testInfo) => {
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '地址数据下载' })).toBeVisible()
  await expect(page.getByLabel('任务预览')).toBeVisible()
  await expect(page.getByLabel('调查地址')).toBeVisible()
  if (testInfo.project.name === 'mobile') await page.getByRole('button', { name: '打开主导航' }).click()
  await expect(page.getByRole('link', { name: '地址下载', exact: true })).toHaveClass(/active/)
  await expect(page.getByText('数据中心', { exact: true })).toBeVisible()
  await expect(page.getByText('API 控制', { exact: true })).toBeVisible()
  await expect(page.getByText('系统管理', { exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: '数据工作台', exact: true })).toHaveCount(0)
  await expect(page.getByRole('link', { name: '数据浏览', exact: true })).toHaveCount(0)
  await expect(page.getByRole('link', { name: 'Provider 管理', exact: true })).toHaveCount(0)
  await expect(page.getByRole('link', { name: '成本分析', exact: true })).toHaveCount(0)
  await expect(page.getByRole('link', { name: '错误监控', exact: true })).toHaveCount(0)
  expect(await page.locator('.workspace-sidebar .workspace-nav-group').evaluateAll((groups) => groups.map((group) => Array.from(group.querySelectorAll('.workspace-nav-item')).map((item) => item.textContent?.trim() || '')))).toEqual([
    ['总览'],
    ['地址查询', '地址下载', '任务管理'],
    ['API 控制中心'],
    ['设置中心'],
  ])
  if (testInfo.project.name === 'mobile') await page.getByRole('button', { name: '关闭主导航', exact: true }).last().click()
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
  if (testInfo.project.name === 'desktop') {
    const previewBox = await page.getByLabel('任务预览').boundingBox()
    const inputBox = await page.getByLabel('调查地址').boundingBox()
    expect(previewBox?.x).toBeGreaterThan((inputBox?.x || 0) + (inputBox?.width || 0))
    expect(inputBox?.height).toBeGreaterThanOrEqual(120)
    expect(Number.parseFloat(await page.getByLabel('调查地址').evaluate((element) => getComputedStyle(element).fontSize))).toBeGreaterThanOrEqual(12)
    await expect(page.locator('.workspace-sidebar .workspace-nav-item')).toHaveCount(6)
  }
})

test('desktop density holds from 1366 through 2560', async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== 'desktop')
  for (const width of [1366, 1440, 1920, 2336, 2560]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/')
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
    const previewBox = await page.getByLabel('任务预览').boundingBox()
    const inputBox = await page.getByLabel('调查地址').boundingBox()
    expect(previewBox?.x).toBeGreaterThan((inputBox?.x || 0) + (inputBox?.width || 0))
    const workspaceBox = await page.locator('.form-workspace').boundingBox()
    expect((workspaceBox?.width || 0) / width).toBeGreaterThan(.78)
    const previewRatio = (previewBox?.width || 0) / (workspaceBox?.width || 1)
    expect(previewRatio).toBeGreaterThan(.19)
    expect(previewRatio).toBeLessThan(.3)
  }
})

test('every primary workspace scales with the available viewport', async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== 'desktop')
  test.setTimeout(90_000)
  const browserErrors: string[] = []
  page.on('console', (message) => { if (message.type() === 'error') browserErrors.push(message.text()) })
  page.on('pageerror', (error) => browserErrors.push(error.message))
  await page.route('**/health/ready', (route) => route.fulfill({ json: { status: 'ready', checks: { postgres: 'ok', clickhouse: 'ok', nats: 'ok', minio: 'ok' } } }))
  await page.route('**/api/v1/providers/ankr/status', (route) => route.fulfill({ json: { state: 'NORMAL', provider: 'ankr' } }))
  await page.route('**/api/v1/api-control/**', (route) => {
    const url = route.request().url()
    if (url.includes('/control/states')) return route.fulfill({ json: control })
    if (url.includes('/provider-accounts')) return route.fulfill({ json: { accounts: [] } })
    if (url.includes('/errors')) return route.fulfill({ json: { items: [] } })
    if (url.includes('/overview')) return route.fulfill({ json: overview })
    return route.fulfill({ json: {} })
  })
  await page.route('**/api/v1/coverage/stats', (route) => route.fulfill({ json: { stats: { 'bsc:blocks': 5_550_000_000, 'bsc:rows': 5_951_520_000 } } }))
  await page.route('**/api/v1/tokens/enrichment', (route) => route.fulfill({ json: { token_contracts: 2850, complete: 2847, partial: 3, unknown: 0, conflict: 0, failed: 0, logo_available: 2847, queue_depth: 0 } }))

  const routes: Array<[string, string]> = [
    ['/', '.download-page'],
    ['/system', '.workspace-page'],
    ['/jobs', '.task-workspace'],
    ['/jobs/job-1', '.job-detail-workspace'],
    ['/api-control', '.ac-page'],
    ['/data-explorer', '.workspace-main > .page'],
    ['/browse/transactions', '.workspace-main > .page'],
  ]

  for (const width of [1366, 1920, 2560]) {
    await page.setViewportSize({ width, height: 900 })
    for (const [path, selector] of routes) {
      await page.goto(path)
      const surface = page.locator(selector).first()
      await expect(surface, `${path} at ${page.url()}: ${browserErrors.join(' | ')}`).toBeVisible()
      const ratio = await page.evaluate((target) => {
        const surfaceElement = document.querySelector(target)
        const mainElement = document.querySelector('.workspace-main')
        if (!surfaceElement || !mainElement) return 0
        return surfaceElement.getBoundingClientRect().width / mainElement.getBoundingClientRect().width
      }, selector)
      expect(ratio).toBeGreaterThan(.98)
      await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
    }
  }
})

test('data explorer renders a dense keyboard-accessible workbench', async ({ page }, testInfo) => {
  const browserErrors: string[] = []
  page.on('console', (message) => { if (message.type() === 'error') browserErrors.push(message.text()) })
  page.on('pageerror', (error) => browserErrors.push(error.message))
  await page.goto('/data-explorer')
  await expect(page.getByRole('heading', { name: '数据浏览' })).toBeVisible()
  await expect(page.getByText('案件主地址')).toBeVisible()
  await expect(page.getByRole('link', { name: sampleAddress, exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: `复制地址 ${sampleAddress}` })).toBeVisible()
  const explorer = page.getByRole('link', { name: '在 Explorer 查看地址' })
  await expect(explorer).toHaveAttribute('href', new RegExp(`/bsc/address/${sampleAddress}$`))
  expect(await page.getByRole('columnheader', { name: '地址' }).evaluate((element) => getComputedStyle(element).position)).toBe('sticky')
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
  if (testInfo.project.name === 'desktop') await page.screenshot({ path: 'test-results/ui-refactor/data-explorer-1440.png', fullPage: true })
  await page.getByRole('row', { name: new RegExp(`打开地址 ${sampleAddress}`) }).press('Enter')
  await expect(page).toHaveURL(new RegExp(`/data-explorer/bsc/${sampleAddress}$`))
  expect(browserErrors).toEqual([])
})

test('redesigned dashboard and api views use an asymmetric workspace hierarchy', async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== 'desktop')
  const browserErrors: string[] = []
  page.on('console', (message) => { if (message.type() === 'error') browserErrors.push(message.text()) })
  page.on('pageerror', (error) => browserErrors.push(error.message))
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.route('**/health/ready', (route) => route.fulfill({ json: { status: 'ready', checks: { postgres: 'ok', clickhouse: 'ok', nats: 'ok', minio: 'ok' } } }))
  await page.route('**/api/v1/providers/ankr/status', (route) => route.fulfill({ json: { state: 'NORMAL', provider: 'ankr' } }))
  await page.route('**/api/v1/api-control/**', (route) => {
    const url = route.request().url()
    if (url.includes('/control/states')) return route.fulfill({ json: control })
    if (url.includes('/provider-accounts')) return route.fulfill({ json: { accounts: [{ provider: 'ankr', status: 'AVAILABLE', official_available: true, plan: 'GREEN', remaining_credits: 1_920_942, balance_usd: 19.20942, synced_at: '2026-09-03T01:12:49Z' }] } })
    if (url.includes('/errors')) return route.fulfill({ json: { items: [{ error_class: 'upstream', requests: 28, logical_calls: 28, errors: 28, avg_ms: 1500, p95_ms: 2024.75, credits: 0, estimated_cost_usd: 0, cost_source: 'internal' }] } })
    if (url.includes('/overview')) return route.fulfill({ json: overview })
    return route.fulfill({ json: {} })
  })
  await page.route('**/api/v1/coverage/stats', (route) => route.fulfill({ json: { stats: { 'bsc:blocks': 5_550_000_000, 'bsc:rows': 5_951_520_000 } } }))
  await page.route('**/api/v1/tokens/enrichment', (route) => route.fulfill({ json: { token_contracts: 2850, complete: 2847, partial: 3, unknown: 0, conflict: 0, failed: 0, logo_available: 2847, queue_depth: 0 } }))
  await page.goto('/system')
  await expect(page.getByRole('heading', { name: '状态总览' })).toBeVisible()
  await expect(page.locator('.health-ribbon')).toBeVisible()
  await expect(page.locator('.status-health-grid')).toHaveCount(0)
  await expect(page.locator('.usage-stage .ac-chart canvas')).toBeVisible()
  await page.screenshot({ path: 'test-results/ui-refactor/v3-system-1440.png' })
  await page.goto('/api-control')
  await expect(page.getByRole('heading', { name: 'API 控制中心' })).toBeVisible()
  await expect(page.locator('.ac-primary-metric')).toBeVisible()
  await expect(page.locator('.ac-kpis')).toHaveCount(0)
  await expect(page.locator('.ac-trend-stage .ac-chart canvas')).toBeVisible()
  await page.screenshot({ path: 'test-results/ui-refactor/v3-api-1440.png' })
  await page.getByRole('button', { name: '管控', exact: true }).click()
  await expect(page).toHaveURL(/tab=control/)
  await expect(page.getByRole('button', { name: '管控', exact: true })).toHaveClass(/active/)
  await page.getByPlaceholder('填写启用或解除原因…').fill('计划内维护窗口')
  await page.getByRole('button', { name: '启用停机' }).click()
  await expect(page.getByRole('dialog', { name: '启用全局停机？' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog', { name: '启用全局停机？' })).toHaveCount(0)
  expect(browserErrors).toEqual([])
})

test('task operations table opens preview drawer and contextual batch actions', async ({ page }, testInfo) => {
  const browserErrors: string[] = []
  page.on('console', (message) => { if (message.type() === 'error') browserErrors.push(message.text()) })
  page.on('pageerror', (error) => browserErrors.push(error.message))
  await page.goto('/jobs')
  await expect(page.getByRole('heading', { name: '任务中心' })).toBeVisible()
  await expect(page.getByText('DL-20260902-0050')).toBeVisible()
  await page.getByRole('row', { name: /DL-20260902-0050/ }).getByText('请求拉取').click()
  await expect(page.getByRole('complementary', { name: '任务快速预览' })).toBeVisible()
  await page.getByRole('button', { name: '关闭', exact: true }).click()
  await page.getByRole('checkbox', { name: '选择任务 DL-20260902-0050' }).check()
  await expect(page.getByText('已选择 1 项')).toBeVisible()
  expect(await page.locator('.task-table-scroll').evaluate((element) => getComputedStyle(element).overflowX)).toBe('auto')
  expect(await page.evaluate(() => getComputedStyle(document.body).overflowX)).toBe('hidden')
  if (testInfo.project.name === 'desktop') await page.screenshot({ path: 'test-results/ui-refactor/v3-jobs-1440.png' })
  if (testInfo.project.name === 'mobile') {
    await page.screenshot({ path: 'test-results/ui-refactor/v3-jobs-mobile.png' })
    await page.getByRole('button', { name: '打开主导航' }).click()
    await expect(page.getByRole('complementary', { name: '主导航' })).toHaveClass(/mobile-open/)
    await page.getByRole('complementary', { name: '主导航' }).getByRole('button', { name: '关闭主导航' }).click()
  }
  await page.getByRole('button', { name: '批量取消' }).click()
  await expect(page.getByRole('dialog', { name: '取消 1 个任务' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog', { name: '取消 1 个任务' })).toHaveCount(0)
  expect(browserErrors).toEqual([])
})

test('task detail is pipeline-led and tabs expose shards and logs', async ({ page }, testInfo) => {
  const browserErrors: string[] = []
  page.on('console', (message) => { if (message.type() === 'error') browserErrors.push(message.text()) })
  page.on('pageerror', (error) => browserErrors.push(error.message))
  await page.goto('/jobs/job-1')
  await expect(page.getByRole('heading', { name: 'DL-20260902-0050' })).toBeVisible()
  await expect(page.getByRole('heading', { name: '执行 Pipeline' })).toBeVisible()
  await page.getByRole('button', { name: 'Shards', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Shards' })).toBeVisible()
  await page.getByRole('button', { name: '概览', exact: true }).click()
  if (testInfo.project.name === 'desktop') await page.screenshot({ path: 'test-results/ui-refactor/v3-job-detail-1440.png' })
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
  expect(browserErrors).toEqual([])
})

test('all copy controls work on non-secure HTTP and report the copied value', async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      value: (command: string) => {
        if (command !== 'copy') return false
        const active = document.activeElement
        ;(window as Window & { __copiedText?: string }).__copiedText = active instanceof HTMLTextAreaElement ? active.value : ''
        return true
      },
    })
  })

  async function expectCopy(button: ReturnType<typeof page.getByRole>, expected: string) {
    await button.click()
    await expect(page.getByRole('status')).toHaveText('已复制')
    await expect(page.getByRole('alertdialog')).toHaveCount(0)
    await expect.poll(() => page.evaluate(() => (window as Window & { __copiedText?: string }).__copiedText)).toBe(expected)
    await expect(page.getByRole('status')).toHaveCount(0, { timeout: 2500 })
  }

  await page.goto('/data-explorer')
  await expectCopy(page.getByRole('button', { name: `复制地址 ${sampleAddress}` }), sampleAddress)

  await page.goto(`/data-explorer/bsc/${sampleAddress}`)
  await expectCopy(page.getByRole('button', { name: '复制发送方', exact: true }), sampleAddress)
  await expectCopy(page.getByRole('button', { name: '复制接收方', exact: true }), '0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb')

  await page.goto('/jobs')
  const menu = page.locator('details.row-menu').first()
  await menu.locator('summary').click()
  await expectCopy(menu.getByRole('button', { name: '复制任务 ID', exact: true }), 'job-1')

  await page.goto('/jobs/job-1')
  await expectCopy(page.getByRole('button', { name: '复制地址', exact: true }), sampleAddress)
})
