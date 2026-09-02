import { expect, test, type Page } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { mkdirSync } from 'node:fs'
import { readFile } from 'node:fs/promises'
import path from 'node:path'

const live = process.env.E2E_LIVE_DOWNLOAD === '1'
const screenshotDir = process.env.E2E_SCREENSHOT_DIR
const artifactDir = process.env.E2E_ARTIFACT_DIR
const exportJobId = process.env.E2E_JOB_EXPORT_ID
const address = '0x9b77e3b4f85c7cea7da451f454895edbd10b80dd'
const testBlock = 98000718
const testTxHash = '0x2684ba9654b372f99b349ddf839afda409f4a306182d5fdde2e97af2e4d41617'

function captureBrowserErrors(page: Page) {
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(`pageerror: ${error.message}`))
  page.on('console', (message) => { if (message.type() === 'error') errors.push(`console: ${message.text()}`) })
  return errors
}

async function screenshot(page: Page, name: string) {
  if (!screenshotDir) return
  mkdirSync(screenshotDir, { recursive: true })
  await page.screenshot({ path: path.join(screenshotDir, name), fullPage: true })
}

test('live address download completes and persists a real provider row', async ({ page, request }, testInfo) => {
  test.skip(!live || testInfo.project.name !== 'desktop', '仅在显式启用时创建真实下载任务')
  const errors = captureBrowserErrors(page)
  await page.goto('/')
  await page.locator('textarea').fill(address)
  await page.getByLabel('区块范围').check()
  await page.getByPlaceholder('起始区块').fill(String(testBlock))
  await page.getByPlaceholder('结束区块').fill(String(testBlock))
  await page.getByLabel('交易', { exact: true }).uncheck()
  await page.getByLabel('NFT转账', { exact: true }).uncheck()
  await page.getByLabel('日志', { exact: true }).uncheck()

  const previewResponse = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/datasets/coverage/preview'))
  await page.getByRole('button', { name: 'Coverage 预览' }).click()
  const preview = await previewResponse
  expect(preview.ok()).toBe(true)
  const previewBody = await preview.json()
  expect(previewBody.items).toHaveLength(1)
  await expect(page.getByText('本地 Coverage 预览')).toBeVisible()

  const ensureResponse = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/datasets/ensure'))
  await page.getByRole('button', { name: '创建下载任务' }).click()
  const ensure = await ensureResponse
  expect(ensure.status()).toBe(202)
  const created = await ensure.json()
  expect(created.job_id).toBeTruthy()
  console.log(`LIVE_JOB_ID=${created.job_id}`)
  await expect(page).toHaveURL(new RegExp(`/jobs/${created.job_id}$`))

  await expect.poll(async () => {
    const response = await request.get(`/api/v1/download-jobs/${created.job_id}`)
    return (await response.json()).job.status
  }, { timeout: 120_000, intervals: [500, 1000, 2000] }).toBe('COMPLETED')

  const detailResponse = await request.get(`/api/v1/download-jobs/${created.job_id}`)
  const detail = await detailResponse.json()
  expect(detail.job.range_type).toBe('block')
  expect(detail.job.from_block).toBe(testBlock)
  expect(detail.job.to_block).toBe(testBlock)
  expect(detail.job.datasets).toEqual(['token_transfers'])
  expect(detail.progress.api_requests).toBeGreaterThan(0)
  expect(detail.progress.downloaded_rows).toBe(1)
  expect(detail.datasets[0].stats.downloaded_rows).toBe(1)
  expect(detail.shards.every((shard: any) => shard.status === 'COMPLETED' && shard.raw_object)).toBe(true)

  const rowsResponse = await request.get(`/api/v1/datasets/token_transfers?chain=bsc&subject=${address}&limit=300&offset=0`)
  const rows = (await rowsResponse.json()).rows
  expect(rows.some((row: any) => row.block_number === testBlock && row.tx_hash === testTxHash)).toBe(true)
  await page.reload()
  await expect(page.getByText('已完成', { exact: true }).first()).toBeVisible()
  await screenshot(page, 'live-address-download-completed.png')
  expect(errors).toEqual([])
})

test('live data explorer export downloads a valid ZIP with manifest and CSV rows', async ({ page }, testInfo) => {
  test.skip(!live || testInfo.project.name !== 'desktop', '仅在显式启用时创建真实导出')
  const errors = captureBrowserErrors(page)
  await page.goto('/data-explorer')
  await page.getByPlaceholder('搜索地址 / 前缀 / 后缀 / 名称 / 案件 / 标签').fill(address)
  const row = page.locator('tbody tr').filter({ hasText: address.slice(0, 10) }).first()
  await expect(row).toBeVisible({ timeout: 15_000 })
  await expect(row.locator('td').nth(5)).toHaveText('242')
  await expect(row.locator('td').nth(8)).toHaveText('246')
  await row.locator('input[type="checkbox"]').check()
  await expect(page.getByText('已选 1')).toBeVisible()

  const downloadPromise = page.waitForEvent('download', { timeout: 45_000 })
  const exportResponsePromise = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/exports'))
  await page.getByRole('button', { name: '导出 CSV 压缩包' }).click()
  const exportResponse = await exportResponsePromise
  expect(exportResponse.status()).toBe(202)
  const createdExport = await exportResponse.json()
  console.log(`LIVE_EXPORT_ID=${createdExport.id}`)
  const download = await downloadPromise
  expect(download.suggestedFilename()).toMatch(/^export-[a-f0-9]{8}\.zip$/)
  let filePath = await download.path()
  expect(filePath).not.toBeNull()
  if (artifactDir) {
    mkdirSync(artifactDir, { recursive: true })
    filePath = path.join(artifactDir, `data-explorer-${createdExport.id}.zip`)
    await download.saveAs(filePath)
    console.log(`LIVE_EXPORT_FILE=${filePath}`)
  }
  const bytes = await readFile(filePath!)
  expect(bytes.subarray(0, 2).toString()).toBe('PK')
  const entries = execFileSync('tar', ['-tf', filePath!], { encoding: 'utf8' })
  expect(entries).toContain('manifest.json')
  expect(entries).toContain(`addresses/${address}/transactions.csv`)
  expect(entries).toContain(`addresses/${address}/token_transfers.csv`)
  const csv = execFileSync('tar', ['-xOf', filePath!, `addresses/${address}/token_transfers.csv`], { encoding: 'utf8', maxBuffer: 8 * 1024 * 1024 })
  expect(csv).toContain(address)
  expect(csv).toContain(testTxHash)
  await screenshot(page, 'live-data-explorer-export.png')
  expect(errors).toEqual([])
})

test('live job detail downloads scoped CSV and XLSX files', async ({ page }, testInfo) => {
  test.skip(!live || testInfo.project.name !== 'desktop' || !exportJobId, '需要显式指定真实已完成任务 E2E_JOB_EXPORT_ID')
  test.setTimeout(120_000)
  const errors = captureBrowserErrors(page)
  await page.goto(`/jobs/${exportJobId}`)
  await expect(page.getByText('已完成', { exact: true }).first()).toBeVisible({ timeout: 15_000 })

  for (const format of ['CSV', 'XLSX'] as const) {
    const exportResponsePromise = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/exports'))
    const downloadPromise = page.waitForEvent('download', { timeout: 45_000 })
    await page.getByRole('button', { name: `${format} · logs` }).click()
    const exportResponse = await exportResponsePromise
    expect(exportResponse.status()).toBe(202)
    const createdExport = await exportResponse.json()
    console.log(`LIVE_JOB_${format}_EXPORT_ID=${createdExport.id}`)
    const download = await downloadPromise
    expect(download.suggestedFilename()).toMatch(new RegExp(`^export-[a-f0-9]{8}\\.${format.toLowerCase()}$`))
    let filePath = await download.path()
    expect(filePath).not.toBeNull()
    if (artifactDir) {
      mkdirSync(artifactDir, { recursive: true })
      filePath = path.join(artifactDir, `job-${createdExport.id}.${format.toLowerCase()}`)
      await download.saveAs(filePath)
      console.log(`LIVE_JOB_${format}_EXPORT_FILE=${filePath}`)
    }
    const bytes = await readFile(filePath!)
    if (format === 'CSV') {
      const text = bytes.toString('utf8')
      expect(text.split(/\r?\n/).filter(Boolean)).toHaveLength(29)
      expect(text).toContain('115394565')
    } else {
      expect(bytes.subarray(0, 2).toString()).toBe('PK')
    }
  }

  await screenshot(page, 'live-job-detail-exports.png')
  expect(errors).toEqual([])
})
