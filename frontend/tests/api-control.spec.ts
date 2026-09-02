import { expect, test, type Page } from '@playwright/test'
import { mkdirSync } from 'node:fs'
import path from 'node:path'

const screenshotDir = process.env.E2E_SCREENSHOT_DIR
const compact = new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 2 })
const exact = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 })

async function screenshot(page: Page, name: string) {
  if (!screenshotDir) return
  mkdirSync(screenshotDir, { recursive: true })
  await page.screenshot({ path: path.join(screenshotDir, name), fullPage: true })
}

test.describe('V1.6 API 控制中心', () => {
  test.beforeEach(async ({ page }) => {
    const errors: string[] = []
    page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()) })
    await page.goto('/api-control')
    await expect(page.getByRole('heading', { name: 'API 控制中心' })).toBeVisible()
    await expect(page.getByText('自动管控状态')).toBeVisible()
    await page.waitForTimeout(500)
    expect(errors, '页面不应产生 console error').toEqual([])
  })

  test('总览数据与筛选器可用', async ({ page }) => {
    await expect(page.getByText('本地记录的逻辑调用')).toBeVisible()
    await expect(page.getByText('官网总账与本地审计是两套口径，不能直接对比')).toHaveCount(0)
    await expect(page.locator('.ac-scope-notice')).toHaveCount(0)
    await expect(page.getByRole('img', { name: /API 请求、错误和 P95/ })).toBeVisible()
    await page.getByLabel('时间范围').selectOption('7d')
    await expect(page.getByText('请求趋势')).toBeVisible()
  })

  test('Provider 展示官方余额且立即同步后与接口一致', async ({ page, request }, testInfo) => {
    const response = await request.get('/api/v1/api-control/provider-accounts')
    expect(response.ok()).toBe(true)
    const account = (await response.json()).accounts[0]
    expect(account.connected).toBe(true)
    expect(account.official_available).toBe(true)
    const tab = page.getByRole('button', { name: 'Provider', exact: true })
    await tab.scrollIntoViewIfNeeded(); await tab.click()
    await expect(page.getByText('Management MCP 官方账户')).toBeVisible()
    await expect(page.getByText('账户剩余积分')).toBeVisible()
    await expect(page.getByText(exact.format(account.remaining_credits), { exact: true })).toBeVisible()
    await expect(page.getByText('GREEN', { exact: true })).toBeVisible()

    const syncResponse = page.waitForResponse((r) => r.request().method() === 'POST' && r.url().includes('/provider-accounts/ankr/sync'))
    await page.getByRole('button', { name: '立即同步' }).click()
    const synced = await syncResponse
    expect(synced.ok()).toBe(true)
    const syncedAccount = (await synced.json()).account
    await expect(page.getByRole('button', { name: '立即同步' })).toBeEnabled()
    await expect(page.getByText(exact.format(syncedAccount.remaining_credits), { exact: true })).toBeVisible()
    await screenshot(page, `${testInfo.project.name}-api-provider.png`)
  })

  test('请求表格可打开详情抽屉', async ({ page }) => {
    const tab = page.getByRole('button', { name: '请求', exact: true })
    await tab.scrollIntoViewIfNeeded(); await tab.click()
    await expect(page.getByRole('heading', { name: '请求审计' })).toBeVisible()
    const row = page.locator('.ag-row').first()
    await expect(row).toBeVisible(); await row.click()
    await expect(page.getByText('请求详情')).toBeVisible()
    await expect(page.getByRole('heading', { name: '尝试时间线' })).toBeVisible()
    await page.getByRole('button', { name: '关闭' }).click()
  })

  test('聚合行下钻会把条件带入请求查询', async ({ page }) => {
    const tab = page.getByRole('button', { name: '操作', exact: true })
    await tab.scrollIntoViewIfNeeded(); await tab.click()
    await expect(page.getByRole('heading', { name: '操作表现' })).toBeVisible()
    const request = page.waitForRequest((r) => r.url().includes('/api-control/requests?') && r.url().includes('operation='))
    await page.locator('.ac-responsive-table tbody tr').first().click()
    expect((await request).url()).toContain('operation=')
    await expect(page.getByRole('heading', { name: '请求审计' })).toBeVisible()
  })

  test('管控页展示人工覆盖表单且不内嵌风险提示', async ({ page }, testInfo) => {
    const tab = page.getByRole('button', { name: '管控', exact: true })
    await tab.scrollIntoViewIfNeeded(); await tab.click()
    await expect(page.getByText('人工覆盖会暂时绕过自动决策。')).toHaveCount(0)
    await expect(page.locator('.ac-warning')).toHaveCount(0)
    await expect(page.getByText('有效期（分钟）')).toBeVisible()
    await expect(page.getByText('全局停机开关')).toBeVisible()
    const mode = page.getByLabel('覆盖模式')
    await expect(mode.locator('option')).toHaveText(['观察', '降级', '隔离', '保护', '恢复中', '正常'])
    await mode.selectOption('NORMAL')
    await expect(mode).toHaveValue('NORMAL')
    await expect(mode).toHaveText(/正常/)
    await screenshot(page, `${testInfo.project.name}-chinese-control-mode.png`)
    await expect(page.getByRole('button', { name: '确认人工覆盖' })).toBeDisabled()
    await expect(page.getByRole('button', { name: '启用停机' })).toBeDisabled()
  })

  test('全局筛选同步到聚合请求，各标签页均能加载', async ({ page }, testInfo) => {
    const filtered = page.waitForResponse((r) => r.url().includes('/api-control/overview?') && r.url().includes('range=7d') && r.url().includes('provider=ankr') && r.url().includes('chain=bsc'))
    await page.getByLabel('时间范围').selectOption('7d')
    await page.getByLabel('Provider').selectOption('ankr')
    await page.getByLabel('链').selectOption('bsc')
    expect((await filtered).ok()).toBe(true)

    const tabs = [
      ['Provider', 'Provider 表现'], ['操作', '操作表现'], ['成本', '成本归因'], ['错误', '错误分类'],
      ['管控', '实时控制面'], ['请求', '请求审计'], ['预算', '预算使用'], ['告警', '告警中心'],
    ] as const
    for (const [tab, heading] of tabs) {
      const button = page.getByRole('button', { name: tab, exact: true })
      await button.scrollIntoViewIfNeeded(); await button.click()
      await expect(page.getByRole('heading', { name: heading })).toBeVisible()
    }
    await screenshot(page, `${testInfo.project.name}-api-alerts.png`)
  })
})

test('状态总览与 API 控制中心使用同一份 24h 指标和官方余额', async ({ page, request }, testInfo) => {
  const [overviewResponse, accountResponse] = await Promise.all([
    request.get('/api/v1/api-control/overview?range=24h'),
    request.get('/api/v1/api-control/provider-accounts'),
  ])
  expect(overviewResponse.ok()).toBe(true)
  expect(accountResponse.ok()).toBe(true)
  const overview = await overviewResponse.json()
  const account = (await accountResponse.json()).accounts[0]
  await page.goto('/system')
  await expect(page.getByRole('heading', { name: '状态总览' })).toBeVisible()
  await expect(page.getByText('System')).toBeVisible()
  await expect(page.getByText('Auto Control')).toBeVisible()
  await expect(page.getByText('Ankr 官网账户余额')).toBeVisible()
  await expect(page.getByText(`${compact.format(account.remaining_credits)} Credits`, { exact: true })).toBeVisible()
  await expect(page.getByText(`${compact.format(overview.metrics.logical_calls)} Calls`, { exact: true })).toBeVisible()
  await expect(page.getByText(new RegExp(`本地捕获 ${compact.format(overview.metrics.provider_calls)} 次外部请求`))).toBeVisible()
  await expect(page.getByText('Recent Errors / Warnings')).toBeVisible()
  await expect(page.locator('.status-overview table')).toHaveCount(0)
  await screenshot(page, `${testInfo.project.name}-system-overview.png`)

  await page.getByText('查看 Provider 详情').click()
  await expect(page).toHaveURL(/\/api-control\?tab=providers/)
  await expect(page.getByText(exact.format(account.remaining_credits), { exact: true })).toBeVisible()
})

test('状态总览和 API 控制中心不产生页面级横向溢出', async ({ page }) => {
  for (const route of ['/system', '/api-control']) {
    await page.goto(route)
    await expect.poll(() => page.evaluate(() =>
      document.documentElement.scrollWidth <= document.documentElement.clientWidth,
    )).toBe(true)
  }
})
