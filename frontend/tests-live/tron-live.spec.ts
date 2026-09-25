import { expect, test } from '@playwright/test'

const address = 'TJmmqjb1DK9TTZbQXzRQ2AuA94z4gKAPFh'
const flowAddress = 'TQguVRm3tDmZG7AeZ47Mk6qi6GTF1ZDqkZ'

test('TRON download and explorer UI work without lowercasing or overflow', async ({ page }, testInfo) => {
  const issues: string[] = []
  page.on('console', (message) => { if (message.type() === 'error') issues.push(message.text()) })
  page.on('pageerror', (error) => issues.push(error.message))

  await page.goto('/')
  await page.getByRole('combobox', { name: '链' }).click()
  await page.getByRole('option', { name: 'TRON' }).click()
  await page.getByRole('textbox', { name: '调查地址' }).fill(address)
  await expect(page.getByText('1 个有效')).toBeVisible()
	await expect(page.getByText('Provider：Ankr TRON Node API')).toBeAttached()
	await expect(page.getByRole('radio', { name: '全量' })).toBeDisabled()
	await expect(page.getByRole('radio', { name: '区块范围' })).toBeEnabled()
	await expect(page.getByRole('radio', { name: '时间范围' })).toBeChecked()
	const fromValue = await page.getByLabel('开始时间').inputValue()
	const toValue = await page.getByLabel('结束时间').inputValue()
	const defaultLookbackMs = new Date(toValue).getTime() - new Date(fromValue).getTime()
	expect(defaultLookbackMs).toBeGreaterThanOrEqual(4 * 60 * 1000)
	expect(defaultLookbackMs).toBeLessThanOrEqual(6 * 60 * 1000)
	await page.getByRole('button', { name: '查看 TRON 数据范围说明' }).click()
	await expect(page.getByRole('alertdialog', { name: 'TRON 数据范围' })).toBeVisible()
	await page.getByRole('button', { name: '我知道了' }).click()
	await expect(page.getByRole('alertdialog', { name: 'TRON 数据范围' })).toHaveCount(0)
  await expect(page.getByText('交易', { exact: true }).first()).toBeVisible()
  await expect(page.getByText('代币转账')).toBeVisible()
  await expect(page.getByText('内部转账')).toBeVisible()
  await expect(page.getByText('NFT 转账')).toHaveCount(0)

  await page.goto('/data-explorer/tron/' + address)
  await expect(page.getByRole('heading', { name: `TRON · ${address}` })).toBeVisible()
  await page.getByRole('button', { name: '代币转账', exact: true }).click()
  await expect(page.getByText('USDT', { exact: true }).first()).toBeVisible()
  await expect(page.locator('td[title="完整数量：1"]').first()).toBeVisible()
  await expect(page.locator('a[href*="tronscan.org/#/transaction/"]').first()).toBeVisible()
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
  await page.screenshot({ path: `test-results-live/tron-${testInfo.project.name}.png`, fullPage: true })
  expect(issues).toEqual([])
})

test('TRON activity flow renders complete transaction relationships', async ({ page }) => {
  await page.goto('/data-explorer/tron/' + flowAddress)
  await expect(page.getByRole('heading', { name: `TRON · ${flowAddress}` })).toBeVisible()
  await page.getByRole('button', { name: '交易', exact: true }).click()
  await expect(page.locator('.flow-table tbody tr').first()).toBeVisible()
  await expect(page.locator('.flow-direction').first()).toContainText(/In|Out|Self/, { timeout: 15_000 })
  await expect(page.locator('.flow-address a[title^="T"]').first()).toBeVisible()
  await expect(page.locator('a[href*="tronscan.org/#/transaction/"]').first()).toBeVisible()
})
