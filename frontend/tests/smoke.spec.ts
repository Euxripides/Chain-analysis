import { expect, test, type Page } from '@playwright/test'
import { mkdirSync } from 'node:fs'
import path from 'node:path'

const screenshotDir = process.env.E2E_SCREENSHOT_DIR

function captureBrowserErrors(page: Page) {
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(`pageerror: ${error.message}`))
  page.on('console', (message) => {
    if (message.type() === 'error') errors.push(`console: ${message.text()}`)
  })
  return errors
}

async function screenshot(page: Page, name: string) {
  if (!screenshotDir) return
  mkdirSync(screenshotDir, { recursive: true })
  await page.screenshot({ path: path.join(screenshotDir, name), fullPage: true })
}

test('download page renders its primary workflow without browser errors', async ({ page }, testInfo) => {
  const errors = captureBrowserErrors(page)
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '地址数据下载' })).toBeVisible()
  await expect(page.getByRole('button', { name: '+ 新建案件' })).toBeVisible()
  await expect(page.getByRole('button', { name: '+ 新建标签' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Coverage 预览' })).toBeDisabled()
  await screenshot(page, `${testInfo.project.name}-download.png`)
  expect(errors).toEqual([])
})

test('data explorer loads live catalog rows and opens address details', async ({ page }, testInfo) => {
  const errors = captureBrowserErrors(page)
  await page.goto('/data-explorer')
  await expect(page.getByRole('heading', { name: '数据浏览' })).toBeVisible()
  const firstDetails = page.getByRole('link', { name: '详情' }).first()
  await expect(firstDetails).toBeVisible({ timeout: 15_000 })
  await firstDetails.click()
  await expect(page.getByRole('button', { name: 'Token Transfers' })).toBeVisible()
  await page.getByRole('button', { name: 'Token Transfers' }).click()
  await expect(page.locator('tbody tr').first()).toBeVisible({ timeout: 15_000 })
  await screenshot(page, `${testInfo.project.name}-address-detail.png`)
  expect(errors).toEqual([])
})

test('viewport does not overflow horizontally', async ({ page }) => {
  await page.goto('/data-explorer')
  await expect(page.getByRole('heading', { name: '数据浏览' })).toBeVisible()
  await expect.poll(() => page.evaluate(() =>
    document.documentElement.scrollWidth <= document.documentElement.clientWidth,
  )).toBe(true)
})
