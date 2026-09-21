import { chromium } from '@playwright/test'
import { mkdirSync } from 'node:fs'

const baseURL = process.env.QA_BASE_URL || 'http://127.0.0.1:8088'
const outputDir = process.env.QA_OUTPUT_DIR || 'test-results-nginx'
mkdirSync(outputDir, { recursive: true })

const resolverArgs = process.env.QA_RESOLVE_IP
  ? [`--host-resolver-rules=MAP ${new URL(baseURL).hostname} ${process.env.QA_RESOLVE_IP}`]
  : []
const browser = await chromium.launch({ headless: true, executablePath: '/snap/bin/chromium', args: resolverArgs })
const results = []

for (const profile of [
  { name: 'desktop', viewport: { width: 1440, height: 900 }, isMobile: false },
  { name: 'mobile', viewport: { width: 390, height: 844 }, isMobile: true },
]) {
  const context = await browser.newContext({ viewport: profile.viewport, isMobile: profile.isMobile })
  const page = await context.newPage()
  const errors = []
  const gatewayErrors = []
  page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()) })
  page.on('pageerror', (error) => errors.push(error.message))
  page.on('response', (response) => {
    if ([502, 504].includes(response.status())) gatewayErrors.push(`${response.status()} ${response.url()}`)
  })

  const pages = [
    ['download', '/', '.download-page'],
    ['jobs', '/jobs', '.task-workspace'],
    ['job-detail', '/jobs/f48e96df-a067-4f0a-a1a8-e01203560325', '.job-detail-workspace'],
    ['explorer', '/data-explorer', '.workbench-toolbar'],
    ['tron-investigation', '/data-explorer/tron/TFFAMQLZybALaLb4uxHA9RBE7pxhUAjF3U', '.detail-tools'],
    ['bsc-investigation', '/data-explorer/bsc/0x6d7d33ab0d492acc7122e0918df54e2dcf35fb28', '.detail-tools'],
    ['system', '/system', '.status-workspace'],
  ]

  for (const [name, path, selector] of pages) {
    const response = await page.goto(baseURL + path, { waitUntil: 'domcontentloaded', timeout: 30_000 })
    if (!response || response.status() !== 200) throw new Error(`${profile.name} ${path} status=${response?.status()}`)
    await page.locator(selector).first().waitFor({ state: 'visible', timeout: 15_000 })
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)
    results.push({ profile: profile.name, page: name, status: response.status(), overflow })
  }

  await page.goto(baseURL + '/', { waitUntil: 'domcontentloaded' })
  await page.getByLabel('导入地址文件').setInputFiles({
    name: 'nginx-import-smoke.csv',
    mimeType: 'text/csv',
    buffer: Buffer.from('address,chain,case,tags\n0x6d7d33ab0d492acc7122e0918df54e2dcf35fb28,bsc,代理验收,导入|BSC\n0x9b77e3b4f85c7cea7da451f454895edbd10b80dd,bsc,代理验收,导入|BSC\n'),
  })
  await page.getByText('2 个有效', { exact: false }).waitFor()
  const metadataParsed = await page.evaluate(() => document.body.textContent?.includes('2 个案件/标签映射、2 个链映射') || false)
  if (!metadataParsed) throw new Error(`${profile.name} CSV metadata/chain mapping was not parsed`)
  results.push({ profile: profile.name, import: 'csv', parsedAddresses: 2, parsedChains: 2, metadataParsed })

  const ethStatus = await page.evaluate(async () => (await fetch('/api/v1/data-explorer/addresses?chain=eth&limit=3')).status)
  if (ethStatus !== 200) throw new Error(`${profile.name} ETH API status=${ethStatus}`)
  results.push({ profile: profile.name, ethApiStatus: ethStatus })

  const chain = page.getByRole('combobox', { name: '链' })
  await chain.click()
  await page.getByRole('option', { name: 'TRON' }).click()
  await page.getByRole('textbox', { name: '调查地址' }).fill('TJmmqjb1DK9TTZbQXzRQ2AuA94z4gKAPFh')
  await page.getByText('1 个有效').waitFor()
  await page.screenshot({ path: `${outputDir}/${profile.name}-download-tron.png`, fullPage: true })

  if (profile.isMobile) {
    const nav = page.getByRole('button', { name: '打开主导航' })
    await nav.click()
    await page.getByRole('link', { name: '任务管理', exact: true }).waitFor()
    await page.screenshot({ path: `${outputDir}/${profile.name}-navigation.png`, fullPage: true })
  }

  results.push({ profile: profile.name, consoleErrors: errors, gatewayErrors })
  if (errors.length || gatewayErrors.length) throw new Error(JSON.stringify({ profile: profile.name, errors, gatewayErrors }))
  await context.close()
}

console.log(JSON.stringify(results, null, 2))
await browser.close()
