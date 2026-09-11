import { expect, test } from '@playwright/test'

const liveJobID = process.env.E2E_LIVE_JOB_ID

test('live task exposes every address progress card', async ({ page }) => {
  test.skip(!liveJobID, 'Set E2E_LIVE_JOB_ID for production verification')
  const browserErrors: string[] = []
  page.on('console', (message) => { if (message.type() === 'error') browserErrors.push(message.text()) })
  page.on('pageerror', (error) => browserErrors.push(error.message))

  await page.goto(`/jobs/${liveJobID}`)
  await expect(page.getByRole('heading', { name: 'DL-20260911-0001' })).toBeVisible()
  await page.getByRole('button', { name: '地址进度', exact: true }).click()
  await expect(page.getByRole('heading', { name: '地址下载进度' })).toBeVisible()
  await expect(page.locator('.address-progress-card')).toHaveCount(2)
  await expect(page.locator('.address-progress-card').first().getByText('100.0%', { exact: true }).first()).toBeVisible()
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
  expect(browserErrors).toEqual([])
})
