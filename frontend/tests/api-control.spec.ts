import { expect, test } from '@playwright/test'

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
    await expect(page.getByText('逻辑调用')).toBeVisible()
    await expect(page.getByRole('img', { name: /API 请求、错误和 P95/ })).toBeVisible()
    await page.getByLabel('时间范围').selectOption('7d')
    await expect(page.getByText('请求趋势')).toBeVisible()
  })

  test('Provider 官方数据缺失时不伪造余额', async ({ page }) => {
    const tab = page.getByRole('button', { name: 'Provider', exact: true })
    await tab.scrollIntoViewIfNeeded(); await tab.click()
    await expect(page.getByText('官方账户数据未接入')).toBeVisible()
    await expect(page.getByText('不会使用内部成本估算冒充官方余额。')).toBeVisible()
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

  test('管控页明确展示人工覆盖 TTL 和风险提示', async ({ page }) => {
    const tab = page.getByRole('button', { name: '管控', exact: true })
    await tab.scrollIntoViewIfNeeded(); await tab.click()
    await expect(page.getByText('人工覆盖会暂时绕过自动决策。')).toBeVisible()
    await expect(page.getByText('有效期（分钟）')).toBeVisible()
    await expect(page.getByText('全局停机开关')).toBeVisible()
  })
})

test('状态总览使用可下钻健康卡且不再展示大表', async ({ page }) => {
  await page.goto('/system')
  await expect(page.getByRole('heading', { name: '状态总览' })).toBeVisible()
  await expect(page.getByText('System')).toBeVisible()
  await expect(page.getByText('Auto Control')).toBeVisible()
  await expect(page.getByText('Ankr Account')).toBeVisible()
  await expect(page.getByText('Recent Errors / Warnings')).toBeVisible()
  await expect(page.locator('.status-overview table')).toHaveCount(0)
})
