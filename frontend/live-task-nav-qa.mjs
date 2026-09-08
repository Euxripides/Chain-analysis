import { chromium } from '@playwright/test'

const baseURL = 'http://192.168.31.152:3000'
const outputDir = 'C:/Users/Euripides/.codex/visualizations/2026/09/03/blockchain-workspace-v3'
const browser = await chromium.launch({ headless: true })
const page = await browser.newPage({ viewport: { width: 2336, height: 1217 } })
const errors = []
page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()) })
page.on('pageerror', (error) => errors.push(error.message))

await page.goto(`${baseURL}/`, { waitUntil: 'domcontentloaded' })
await page.locator('.workspace-sidebar').waitFor()
const groups = await page.locator('.workspace-sidebar .workspace-nav-group').evaluateAll((nodes) => nodes.map((node) => ({
  label: node.querySelector('.workspace-nav-label')?.textContent?.trim() || '总览',
  items: Array.from(node.querySelectorAll('.workspace-nav-item')).map((item) => item.textContent?.trim() || ''),
})))
const dataGroup = groups.find((group) => group.label === '数据中心')
const systemGroup = groups.find((group) => group.label === '系统管理')
if (JSON.stringify(dataGroup?.items) !== JSON.stringify(['地址查询', '地址下载', '数据浏览', '任务管理'])) throw new Error(`数据中心分区错误: ${JSON.stringify(dataGroup)}`)
if (JSON.stringify(systemGroup?.items) !== JSON.stringify(['设置中心'])) throw new Error(`系统管理分区错误: ${JSON.stringify(systemGroup)}`)
await page.getByRole('link', { name: '任务管理', exact: true }).click()
await page.waitForURL('**/jobs')
await page.locator('.task-workspace').waitFor()
await page.getByRole('link', { name: '设置中心', exact: true }).click()
await page.waitForFunction(() => location.pathname === '/system' && location.search.includes('view=settings'))
await page.screenshot({ path: `${outputDir}/live-task-under-data-2336.png`, fullPage: true })

await page.setViewportSize({ width: 390, height: 844 })
await page.goto(`${baseURL}/`, { waitUntil: 'domcontentloaded' })
await page.getByRole('button', { name: '打开主导航' }).click()
await page.waitForTimeout(300)
const mobileGroups = await page.locator('.workspace-sidebar.mobile-open .workspace-nav-group').evaluateAll((nodes) => nodes.map((node) => ({
  label: node.querySelector('.workspace-nav-label')?.textContent?.trim() || '总览',
  items: Array.from(node.querySelectorAll('.workspace-nav-item')).map((item) => item.textContent?.trim() || ''),
})))
const mobileOverflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)
await page.screenshot({ path: `${outputDir}/live-task-under-data-mobile.png` })

console.log(JSON.stringify({ groups, mobileGroups, mobileOverflow, errors }, null, 2))
await browser.close()
