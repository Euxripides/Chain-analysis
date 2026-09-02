import { expect, test, type Page } from '@playwright/test'
import { mkdirSync } from 'node:fs'
import { readFile } from 'node:fs/promises'
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

test('chain selectors show branded icons and update both workflows', async ({ page }, testInfo) => {
  const errors = captureBrowserErrors(page)
  for (const [route, screenshotName] of [['/', 'download'], ['/data-explorer', 'explorer']] as const) {
    await page.goto(route)
    const selector = page.getByRole('combobox', { name: '链' })
    await expect(selector).toContainText('BSC')
    await expect(selector.locator('.chain-logo')).toBeVisible()
    await selector.click()
    const options = page.getByRole('listbox', { name: '链选项' }).getByRole('option')
    await expect(options).toHaveCount(7)
    await expect(options.locator('.chain-logo')).toHaveCount(7)
    await screenshot(page, `${testInfo.project.name}-${screenshotName}-chain-icons.png`)
    if (route === '/') {
      await page.keyboard.press('End')
      await expect(page.getByRole('option', { name: 'OPTIMISM' })).toBeFocused()
      await page.keyboard.press('Enter')
      await expect(selector).toContainText('OPTIMISM')
    } else {
      await page.getByRole('option', { name: 'POLYGON' }).click()
      await expect(selector).toContainText('POLYGON')
    }
    await expect(selector.locator('.chain-logo')).toBeVisible()
  }
  expect(errors).toEqual([])
})

test('data explorer loads live catalog rows and opens address details', async ({ page }, testInfo) => {
  const errors = captureBrowserErrors(page)
  await page.goto('/data-explorer')
  await expect(page.getByRole('heading', { name: '数据浏览' })).toBeVisible()
  const firstDetails = page.getByRole('link', { name: '详情' }).first()
  await expect(firstDetails).toBeVisible({ timeout: 15_000 })
  await firstDetails.click()
  await expect(page.getByRole('button', { name: '代币转账' })).toBeVisible()
  await expect(page.locator('table.detail-table thead th:not(.select-cell)')).toHaveText([
    '交易哈希', '方法', '时间', '发送方', '接收方', '数量', '代币',
  ])
  await page.getByRole('button', { name: '代币转账' }).click()
  await expect(page.locator('table.detail-table thead th:not(.select-cell)')).toHaveText([
    '交易哈希', '方法', '时间', '发送方', '接收方', '数量', '代币',
  ])
  await expect(page.locator('tbody tr').first()).toBeVisible({ timeout: 15_000 })
  await screenshot(page, `${testInfo.project.name}-address-detail.png`)
  expect(errors).toEqual([])
})

// V1.3 验收（方案 §56）：Method 列可见 / 可筛选 / Unknown 正确显示 / 详情可深度解析。
test('transactions expose a method column, method filter and decoded detail drawer', async ({ page }, testInfo) => {
  const errors = captureBrowserErrors(page)
  await page.goto('/data-explorer/bsc/0x9b77e3b4f85c7cea7da451f454895edbd10b80dd')
  await page.getByRole('button', { name: '交易', exact: true }).click()
  await expect(page.getByText('统计中…')).toHaveCount(0, { timeout: 20_000 })

  // Method 列不能留空（§26）
  const methodCells = page.locator('tbody .method-cell b')
  await expect(methodCells.first()).toBeVisible({ timeout: 20_000 })
  const labels = await methodCells.allTextContents()
  expect(labels.length).toBeGreaterThan(0)
  expect(labels.every((value) => value.trim().length > 0)).toBe(true)

  // Method 筛选（§28）
  const methodFilter = page.getByLabel('Method 筛选')
  await expect(methodFilter).toBeVisible()
  await methodFilter.fill('unknown')
  await expect(page.getByText('统计中…')).toHaveCount(0, { timeout: 20_000 })
  const unknownLabels = await page.locator('tbody .method-cell small.method-unknown-tag').allTextContents()
  expect(unknownLabels.every((value) => value === 'Unknown')).toBe(true)
  await methodFilter.fill('')
  await expect(page.getByText('统计中…')).toHaveCount(0, { timeout: 20_000 })

  // 交易详情抽屉（§42/§43）
  await page.locator('tbody .method-cell').first().click()
  await expect(page.getByRole('dialog', { name: '交易详情' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Transaction Overview' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Input Data' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Decoded Input' })).toBeVisible()
  await screenshot(page, `${testInfo.project.name}-transaction-detail.png`)
  await page.getByRole('button', { name: '关闭' }).click()
  await expect(page.getByRole('dialog', { name: '交易详情' })).toHaveCount(0)
  expect(errors).toEqual([])
})

test('viewport does not overflow horizontally', async ({ page }) => {
  await page.goto('/data-explorer')
  await expect(page.getByRole('heading', { name: '数据浏览' })).toBeVisible()
  await expect.poll(() => page.evaluate(() =>
    document.documentElement.scrollWidth <= document.documentElement.clientWidth,
  )).toBe(true)
})

test('wide address detail uses the workspace and shows a compact normalized amount', async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== 'desktop')
  await page.setViewportSize({ width: 1920, height: 1080 })
  await page.goto('/data-explorer/bsc/0x9b77e3b4f85c7cea7da451f454895edbd10b80dd')
  await expect(page.getByRole('heading', { name: /0x9b77e3b4/ })).toBeVisible()
  const amount = page.locator('.flow-amount b').first()
  await expect(amount).toHaveText(/-16,175\.000597/)
  await expect(page.getByLabel('数据筛选与导出')).toBeVisible()
  await expect(page.getByText('统计中…')).toHaveCount(0, { timeout: 20_000 })
  await expect(page.getByLabel('导出范围')).toHaveValue('filtered')
  const amountAlignment = await page.getByRole('columnheader', { name: '数量' }).evaluate((element) => getComputedStyle(element).textAlign)
  const amountValueAlignment = await page.locator('.flow-amount').first().evaluate((element) => getComputedStyle(element).textAlign)
  expect(amountAlignment).toBe(amountValueAlignment)
  const contentWidth = await page.locator('main.content').evaluate((element) => element.getBoundingClientRect().width)
  expect(contentWidth).toBeGreaterThan(1600)
  await screenshot(page, 'wide-address-detail.png')
})

test('address cases and tags can be edited from the header', async ({ page }, testInfo) => {
  const errors = captureBrowserErrors(page)
  await page.goto('/data-explorer/bsc/0x9b77e3b4f85c7cea7da451f454895edbd10b80dd')
  await expect(page.getByRole('heading', { name: /0x9b77e3b4/ })).toBeVisible()
  await expect(page.getByText(/案件：盘古/)).toBeVisible({ timeout: 15_000 })
  await page.getByRole('button', { name: '修改案件和标签' }).click()
  const dialog = page.getByRole('dialog', { name: '修改案件和标签' })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByLabel('案件名称')).toHaveValue('盘古')
  await expect(dialog.getByLabel('标签 入金地址')).toBeChecked()
  await expect(dialog.getByLabel('新标签名称')).toBeVisible()
  await dialog.getByLabel('新标签名称').fill('入金地址')
  await dialog.getByRole('button', { name: '添加', exact: true }).click()
  await expect(dialog.getByLabel('标签 入金地址')).toBeChecked()
  await screenshot(page, `${testInfo.project.name}-metadata-editor.png`)

  const patchResponse = page.waitForResponse((response) => response.request().method() === 'PATCH' && response.url().includes('/metadata'))
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  expect((await patchResponse).ok()).toBe(true)
  await expect(dialog).toHaveCount(0)
  await page.reload()
  await expect(page.getByText(/案件：盘古/)).toBeVisible({ timeout: 15_000 })
  await expect(page.getByText('入金地址', { exact: true })).toBeVisible()
  expect(errors).toEqual([])
})

test('arbitrary case and tag names are created, linked and restored', async ({ page, request }) => {
  test.skip(process.env.E2E_LIVE_METADATA_MUTATION !== '1', '仅在显式启用时修改真实元数据')
  const address = '0x9b77e3b4f85c7cea7da451f454895edbd10b80dd'
  const suffix = Date.now().toString().slice(-8)
  const caseName = `验收案件-${suffix}`
  const tagName = `验收标签-${suffix}`
  const [casesResponse, tagsResponse, catalogResponse] = await Promise.all([
    request.get('/api/v1/cases'), request.get('/api/v1/tags'),
    request.get(`/api/v1/data-explorer/addresses?chain=bsc&q=${address}&limit=5`),
  ])
  const cases = (await casesResponse.json()).cases as { id: string; name: string }[]
  const tags = (await tagsResponse.json()).tags as { id: string; name: string }[]
  const original = (await catalogResponse.json()).addresses.find((item: any) => item.normalized_address === address)
  const originalCaseIDs = cases.filter((item) => original.case_names.includes(item.name)).map((item) => item.id)
  const originalTagIDs = tags.filter((item) => original.tag_names.includes(item.name)).map((item) => item.id)
  let temporaryCaseID = ''
  let temporaryTagID = ''

  try {
    await page.goto(`/data-explorer/bsc/${address}`)
    await page.getByRole('button', { name: '修改案件和标签' }).click()
    const dialog = page.getByRole('dialog', { name: '修改案件和标签' })
    await dialog.getByLabel('案件名称').fill(caseName)
    await dialog.getByLabel('新标签名称').fill(tagName)
    await dialog.getByRole('button', { name: '添加', exact: true }).click()
    await expect(dialog.getByRole('button', { name: `移除新标签 ${tagName}` })).toBeVisible()
    const caseCreated = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/api/v1/cases'))
    const tagCreated = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/api/v1/tags'))
    const metadataSaved = page.waitForResponse((response) => response.request().method() === 'PATCH' && response.url().endsWith('/metadata'))
    await dialog.getByRole('button', { name: '保存', exact: true }).click()
    temporaryCaseID = (await (await caseCreated).json()).id
    temporaryTagID = (await (await tagCreated).json()).id
    expect((await metadataSaved).ok()).toBe(true)
    await page.reload()
    await expect(page.getByText(new RegExp(`案件：${caseName}`))).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText(tagName, { exact: true })).toBeVisible()
  } finally {
    if (temporaryCaseID || temporaryTagID) {
      await request.patch(`/api/v1/addresses/bsc/${address}/metadata`, { data: {
        set_primary_case: originalCaseIDs.length > 0,
        add_cases: originalCaseIDs,
        remove_cases: temporaryCaseID ? [temporaryCaseID] : [],
        add_tags: originalTagIDs,
        remove_tags: temporaryTagID ? [temporaryTagID] : [],
      } })
    }
    if (temporaryCaseID) await request.delete(`/api/v1/cases/${temporaryCaseID}`)
    if (temporaryTagID) await request.delete(`/api/v1/tags/${temporaryTagID}`)
  }
})

test('filters update full-result statistics and selected rows can be exported', async ({ page }, testInfo) => {
  await page.goto('/data-explorer/bsc/0x9b77e3b4f85c7cea7da451f454895edbd10b80dd')
  await page.getByRole('button', { name: '代币转账' }).click()
  await expect(page.getByText('统计中…')).toHaveCount(0, { timeout: 20_000 })
  await page.getByRole('button', { name: '代币筛选' }).click()
  const tokenDialog = page.getByRole('dialog', { name: '选择代币' })
  await expect(tokenDialog.getByLabel('搜索代币')).toBeVisible()
  const tokenCheckbox = tokenDialog.getByLabel(/U5DТ 0xefc391/i).first()
  await expect(tokenCheckbox).toBeVisible({ timeout: 20_000 })
  await tokenCheckbox.check()
  await tokenDialog.getByRole('button', { name: '确认' }).click()
  await page.getByRole('button', { name: '代币筛选' }).click()
  await expect(tokenDialog.getByText('历史搜索')).toBeVisible()
  await screenshot(page, `${testInfo.project.name}-token-filter-open.png`)
  await tokenDialog.getByRole('button', { name: '确认' }).click()
  await page.getByLabel('最小数量').fill('50000')
  await expect(page.getByText('统计中…')).toHaveCount(0, { timeout: 20_000 })
  await expect(page.locator('tbody .token-cell-title b').first()).toHaveText('U5DТ')
  expect((await page.locator('tbody .token-cell').evaluateAll((nodes) => nodes.map((node) => node.getAttribute('data-contract')))).every((value) => value === '0xefc391a6e4a1c0775d845c4ced7bfcaa653e6a6e')).toBe(true)
  const amounts = await page.locator('tbody .flow-amount b').allTextContents()
  expect(amounts.length).toBeGreaterThan(0)
  expect(amounts.every((value) => Math.abs(Number(value.replaceAll(',', ''))) >= 50000)).toBe(true)

  await page.getByLabel('勾选第 1 行').check()
  await expect(page.getByText('已选 1 条')).toBeVisible()
  await page.getByLabel('导出范围').selectOption('selected')
  await page.getByLabel('导出格式').selectOption('json')
  const downloadPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: '导出', exact: true }).click()
  const download = await downloadPromise
  expect(download.suggestedFilename()).toMatch(/已勾选\.json$/)
  const jsonPath = await download.path()
  expect(jsonPath).not.toBeNull()
  expect(JSON.parse(await readFile(jsonPath!, 'utf8'))).toHaveLength(1)

  await page.getByLabel('导出格式').selectOption('xlsx')
  const xlsxPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: '导出', exact: true }).click()
  const xlsx = await xlsxPromise
  expect(xlsx.suggestedFilename()).toMatch(/已勾选\.xlsx$/)
  const xlsxPath = await xlsx.path()
  expect(xlsxPath).not.toBeNull()
  expect((await readFile(xlsxPath!)).subarray(0, 2).toString()).toBe('PK')

  await page.getByLabel('导出范围').selectOption('filtered')
  await page.getByLabel('导出格式').selectOption('csv')
  const csvPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: '导出', exact: true }).click()
  const csv = await csvPromise
  expect(csv.suggestedFilename()).toMatch(/筛选结果\.csv$/)
  const csvPath = await csv.path()
  expect(csvPath).not.toBeNull()
  expect((await readFile(csvPath!, 'utf8')).split(/\r?\n/).length).toBeGreaterThan(1)

  await page.getByLabel('导出范围').selectOption('all')
  await page.getByLabel('导出格式').selectOption('json')
  const allPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: '导出', exact: true }).click()
  const allDownload = await allPromise
  expect(allDownload.suggestedFilename()).toMatch(/全部\.json$/)
  const allPath = await allDownload.path()
  expect(allPath).not.toBeNull()
  expect(JSON.parse(await readFile(allPath!, 'utf8')).length).toBeGreaterThan(1)
})

test('token metadata differentiates lookalike contracts and exposes resolved tooltip fields', async ({ page }, testInfo) => {
  const errors = captureBrowserErrors(page)
  await page.goto('/data-explorer/bsc/0x9b77e3b4f85c7cea7da451f454895edbd10b80dd')
  await page.getByRole('button', { name: '代币转账' }).click()
  await expect(page.getByText('统计中…')).toHaveCount(0, { timeout: 20_000 })

  const firstToken = page.locator('.token-cell').first()
  await expect(firstToken).toBeVisible({ timeout: 20_000 })
  await expect(firstToken).toHaveAttribute('data-contract', '0xefc391a6e4a1c0775d845c4ced7bfcaa653e6a6e')
  await expect(firstToken.locator('.token-name')).toHaveText('U5DТ')
  await expect(firstToken.locator('.metadata-status')).toHaveCount(0)
  await expect(firstToken).not.toContainText('解析')
  await firstToken.hover()
  const tooltip = page.getByRole('tooltip')
  await expect(tooltip).toContainText('U5DТ')
  await expect(tooltip).toContainText('0xefc391a6e4a1c0775d845c4ced7bfcaa653e6a6e')
  await expect(tooltip).toContainText('rpc')
  await expect(tooltip).toContainText('high')

  const contracts = await page.locator('.token-cell').evaluateAll((nodes) => nodes.map((node) => node.getAttribute('data-contract')))
  expect(new Set(contracts).size).toBeGreaterThan(1)
  expect((await page.locator('.token-name').allTextContents()).every((value) => !/^0x[0-9a-f]{40}$/i.test(value))).toBe(true)
  await screenshot(page, `${testInfo.project.name}-token-metadata.png`)
  expect(errors).toEqual([])
})

test('official BSC USDT uses the trusted logo and verified registry status', async ({ page }, testInfo) => {
  const errors = captureBrowserErrors(page)
  const official = '0x55d398326f99059ff775485246999027b3197955'
  await page.goto('/data-explorer/bsc/0x9b77e3b4f85c7cea7da451f454895edbd10b80dd')
  await page.getByRole('button', { name: '代币转账' }).click()
  await expect(page.getByText('统计中…')).toHaveCount(0, { timeout: 20_000 })
  await page.getByRole('button', { name: '代币筛选' }).click()
  const dialog = page.getByRole('dialog', { name: '选择代币' })
  await dialog.getByLabel('搜索代币').fill(official)
  const option = dialog.getByLabel(new RegExp(`USDT ${official}`, 'i'))
  await expect(option).toBeVisible({ timeout: 20_000 })
  await option.check()
  await dialog.getByRole('button', { name: '确认' }).click()
  await expect(page.getByText('统计中…')).toHaveCount(0, { timeout: 20_000 })

  const token = page.locator('.token-cell').first()
  await expect(token).toHaveAttribute('data-contract', official)
  await expect(token.locator('.token-name')).toHaveText('Tether USD')
  await expect(token.locator('.metadata-status')).toHaveCount(0)
  const logo = token.locator('img.flow-token-icon')
  await expect(logo).toBeVisible()
  await expect.poll(() => logo.evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth > 0)).toBe(true)
  await token.hover()
  await expect(page.getByRole('tooltip')).toContainText('verified_registry')
  await expect(page.getByRole('tooltip')).toContainText('可信注册表已认证')
  await screenshot(page, `${testInfo.project.name}-official-usdt.png`)
  expect(errors).toEqual([])
})

test('contract parties use an OKLink-style contract icon', async ({ page }, testInfo) => {
  const errors = captureBrowserErrors(page)
  await page.goto('/data-explorer/bsc/0x9b77e3b4f85c7cea7da451f454895edbd10b80dd')
  await page.getByRole('button', { name: '交易', exact: true }).click()
  await expect(page.getByText('统计中…')).toHaveCount(0, { timeout: 20_000 })
  const icon = page.getByLabel('合约地址').first()
  await expect(icon).toBeVisible({ timeout: 20_000 })
  await expect(icon.locator('svg')).toBeVisible()
  await screenshot(page, `${testInfo.project.name}-contract-party-icon.png`)
  expect(errors).toEqual([])
})
