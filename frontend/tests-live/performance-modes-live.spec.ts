import { expect, test } from '@playwright/test'

const jobId = 'd28f52dc-22eb-46e3-8061-c6414f833730'

test('live performance-mode UI is usable without overflow or browser errors', async ({ page }, testInfo) => {
  const issues: string[] = []
  page.on('console', (message) => { if (message.type() === 'error') issues.push(message.text()) })
  page.on('pageerror', (error) => issues.push(error.message))

  await page.goto('/')
  const selector = page.getByRole('radiogroup', { name: '运行模式' })
  await expect(selector).toBeVisible()
  await expect(selector.getByRole('radio', { name: /SAFE.*24/ })).toBeVisible()
  await expect(selector.getByRole('radio', { name: /FAST.*32/ })).toBeVisible()
  await expect(selector.getByRole('radio', { name: /TURBO.*48/ })).toBeVisible()
  await expect(page.getByRole('radio', { name: /FAST/ })).toBeChecked()
  await expect.poll(() => page.evaluate(() => {
    const root = document.getElementById('root')
    return document.body.scrollWidth <= document.body.clientWidth && Boolean(root && root.scrollWidth <= root.clientWidth)
  })).toBe(true)

  await page.goto('/jobs')
  await expect(page.getByRole('columnheader', { name: '模式' })).toBeVisible()
  await expect(page.locator('.task-id').first()).toBeVisible()

  await page.goto('/jobs/' + jobId)
  await expect(page.getByText('FAST', { exact: true }).first()).toBeVisible()
  await expect(page.getByText('Safe Target', { exact: true })).toBeVisible()
  await expect(page.getByText('Hard Ceiling', { exact: true })).toBeVisible()

  await page.goto('/api-control?tab=providers')
  await expect(page.getByRole('heading', { name: 'Ankr Fetch Capacity' })).toBeVisible()
  await expect(page.getByText('Production Ceiling 48', { exact: false })).toBeVisible()
  await expect(page.getByText('Production Ceiling 600')).toHaveCount(0)
  await expect.poll(() => page.evaluate(() => {
    const root = document.getElementById('root')
    return document.body.scrollWidth <= document.body.clientWidth && Boolean(root && root.scrollWidth <= root.clientWidth)
  })).toBe(true)
  await page.screenshot({ path: `test-results-live/performance-modes-${testInfo.project.name}.png`, fullPage: true })
  expect(issues).toEqual([])
})
