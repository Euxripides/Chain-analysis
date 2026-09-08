import { expect, test } from '@playwright/test'

const address = '0x9b77e3b4f85c7cea7da451f454895edbd10b80dd'
const profile = {
  version: 'v1.4.2', default_mode: 'fast', hard_ceiling: 48, reject_boundary: 64,
  memory_budget_bytes: 6442450944, absolute_rss_limit_bytes: 12884901888,
  page_size_default: 10000, request_timeout_seconds: 180,
  modes: { safe: { target_concurrency: 24 }, fast: { target_concurrency: 32 }, turbo: { target_concurrency: 48 } },
}
const runtime = { requested_mode: 'turbo', effective_mode: 'fast', target_concurrency: 32, active_concurrency: 29, hard_ceiling: 48, mode_reason: 'provider_degraded' }
const job = {
  id: 'job-mode-1', job_no: 'DL-20260906-0001', status: 'RUNNING', priority: 100,
  source: 'user_download', chain: 'bsc', range_type: 'full', datasets: ['transactions'],
  reuse_policy: 'prefer_local', head_block: 0, error: '', requested_mode: 'turbo',
  effective_mode: 'fast', mode_reason: 'provider_degraded', stats: { subjects: 1, total_shards: 4, done_shards: 1 },
  created_at: '2026-09-06T04:00:00Z', updated_at: '2026-09-06T04:01:00Z',
}
const detail = {
  job, performance: runtime,
  subjects: [{ id: 'subject-1', job_id: job.id, subject_type: 'address', subject: address, status: 'RUNNING', progress: .25, stats: {} }],
  datasets: [{ id: 'dataset-1', job_id: job.id, subject_id: 'subject-1', dataset: 'transactions', status: 'RUNNING', progress: .25, weight: 1, stats: {} }],
  shards: [], progress: { job_id: job.id, status: 'RUNNING', progress: .25, downloaded_rows: 100, downloaded_bytes: 1000, api_requests: 2, api_cost_usd: 0, total_shards: 4, done_shards: 1, ts: '2026-09-06T04:01:00Z' },
}

test.beforeEach(async ({ page }) => {
  await page.route('**/api/v1/providers/ankr/production-profile', route => route.fulfill({ json: profile }))
  await page.route('**/api/v1/providers/ankr/fetch-runtime', route => route.fulfill({ json: runtime }))
  await page.route('**/api/v1/providers/ankr/performance-mode', route => route.fulfill({ json: { ...runtime, requested_mode: 'turbo', effective_mode: 'turbo', target_concurrency: 48 } }))
  await page.route('**/api/v1/cases', route => route.fulfill({ json: { cases: [] } }))
  await page.route('**/api/v1/tags', route => route.fulfill({ json: { tags: [] } }))
  await page.route('**/api/v1/download-jobs?**', route => route.fulfill({ json: { jobs: [job] } }))
  await page.route('**/api/v1/download-jobs/job-mode-1/events', route => route.fulfill({ status: 200, contentType: 'text/event-stream', body: '' }))
  await page.route('**/api/v1/download-jobs/job-mode-1/performance-mode', route => route.fulfill({ json: { job_id: job.id, requested_mode: 'safe', effective_mode: 'fast', target_concurrency: 32 } }))
  await page.route('**/api/v1/download-jobs/job-mode-1', route => route.fulfill({ json: detail }))
  await page.route('**/api/v1/api-control/provider-accounts', route => route.fulfill({ json: { accounts: [] } }))
  await page.route('**/api/v1/api-control/control/states', route => route.fulfill({ json: { state: 'NORMAL', reason: '', automatic_protection: true } }))
  await page.route('**/api/v1/api-control/providers**', route => route.fulfill({ json: { items: [] } }))
  await page.route('**/api/v1/count-oracle/providers', route => route.fulfill({ json: { capabilities: [], profiles: [], registries: [] } }))
})

test('download selector uses profile values and submits a mode', async ({ page }, testInfo) => {
  let submitted: Record<string, unknown> | undefined
  await page.route('**/api/v1/datasets/ensure', async route => {
    submitted = await route.request().postDataJSON()
    await route.fulfill({ status: 202, json: { job_id: job.id, coverage: 0, status: 'planned' } })
  })
  await page.goto('/')
  const selector = page.getByRole('radiogroup', { name: '运行模式' })
  await expect(selector).toBeVisible()
  await expect(selector.getByText('全局目标 24')).toBeVisible()
  await expect(selector.getByText('全局目标 32')).toBeVisible()
  await expect(selector.getByText('全局目标 48')).toBeVisible()
  await expect(page.getByRole('radio', { name: /FAST/ })).toBeChecked()
  await page.getByRole('radio', { name: /TURBO/ }).check()
  await selector.scrollIntoViewIfNeeded()
  await page.screenshot({ path: `test-results/performance-mode-selector-${testInfo.project.name}.png` })
  await page.getByLabel('调查地址').fill(address)
  await page.getByRole('button', { name: '创建下载任务' }).click()
  await expect.poll(() => submitted?.performance_mode).toBe('turbo')
  await expect(page).toHaveURL(/\/jobs\/job-mode-1$/)
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
})

test('task and provider views show effective mode and keep switches in place', async ({ page }, testInfo) => {
  await page.goto('/jobs')
  await expect(page.getByRole('columnheader', { name: '模式' })).toBeVisible()
  await page.getByRole('button', { name: /FAST.*自动保护/ }).first().click()
  await expect(page.getByRole('alertdialog', { name: '任务操作' })).toBeVisible()
  await page.keyboard.press('Escape')

  await page.goto('/jobs/job-mode-1')
  await expect(page.getByText('请求模式')).toBeVisible()
  await expect(page.getByText('全局目标并发')).toBeVisible()
  await page.locator('.context-rail .performance-mode-actions').getByRole('button', { name: 'SAFE' }).click()
  await expect(page).toHaveURL(/\/jobs\/job-mode-1$/)

  await page.goto('/api-control?tab=providers')
  await expect(page.getByRole('heading', { name: 'Ankr Fetch Capacity' })).toBeVisible()
  await expect(page.getByText('Production Ceiling 48', { exact: false })).toBeVisible()
  await expect(page.getByText('Production Ceiling 600')).toHaveCount(0)
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
  await page.screenshot({ path: `test-results/performance-modes-${testInfo.project.name}.png`, fullPage: true })
})
