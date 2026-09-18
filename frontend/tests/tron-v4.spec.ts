import { expect, test } from '@playwright/test'
import { mkdirSync } from 'node:fs'
import path from 'node:path'

test.beforeEach(async ({ page }) => {
  await page.route('**/api/v1/cases', (route) => route.fulfill({ json: { cases: [] } }))
  await page.route('**/api/v1/tags', (route) => route.fulfill({ json: { tags: [] } }))
  await page.route('**/api/v1/providers/ankr/production-profile', (route) => route.fulfill({ json: {
    version: 'test', default_mode: 'fast', hard_ceiling: 8, reject_boundary: 9,
    memory_budget_bytes: 1, absolute_rss_limit_bytes: 1, page_size_default: 200,
    request_timeout_seconds: 30, modes: { safe: { target_concurrency: 1 }, fast: { target_concurrency: 4 }, turbo: { target_concurrency: 8 } },
  } }))
})

test('TRON token transfers expose full, time and advanced block modes', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()) })
  await page.goto('/')
  await page.getByRole('combobox', { name: '链' }).click()
  await page.getByRole('option', { name: 'TRON' }).click()

  const full = page.getByRole('radio', { name: '全量' })
  await expect(full).toBeEnabled()
  await expect(full).toBeChecked()
  await expect(page.getByRole('checkbox', { name: '交易' })).toBeChecked()
  await expect(page.getByRole('checkbox', { name: '代币转账' })).toBeChecked()
  await expect(page.getByRole('checkbox', { name: '内部转账' })).not.toBeChecked()
  await expect(page.getByText('系统自动选择', { exact: true })).toBeVisible()

  await page.getByRole('button', { name: '查看 TRON 数据范围说明' }).click()
  const dialog = page.getByRole('alertdialog', { name: 'TRON 数据范围' })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByText(/主数据源持续不可用时会评估备用扫描成本/)).toBeVisible()
  await expect(dialog.getByRole('button', { name: '关闭' })).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()

  await page.getByRole('radio', { name: '区块范围' }).check()
  await expect(page.getByLabel('起始区块')).toBeVisible()
  await expect(page.getByLabel('结束区块')).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
  if (process.env.E2E_SCREENSHOT_DIR) {
    mkdirSync(process.env.E2E_SCREENSHOT_DIR, { recursive: true })
    await page.screenshot({ path: path.join(process.env.E2E_SCREENSHOT_DIR, `tron-v4-${test.info().project.name}.png`), fullPage: true })
  }
  expect(errors).toEqual([])
})
