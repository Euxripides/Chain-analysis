# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: zz-acceptance-temp.spec.ts >> UI-001 viewport 768x1024 no horizontal overflow
- Location: tests\zz-acceptance-temp.spec.ts:6:3

# Error details

```
Error: /data-explorer must not overflow

expect(received).toBe(expected) // Object.is equality

Expected: false
Received: true
```

# Page snapshot

```yaml
- generic [ref=f1e3]:
  - complementary [ref=f1e4]:
    - generic [ref=f1e5]:
      - generic [ref=f1e6]: ◈
      - generic [ref=f1e7]:
        - generic [ref=f1e8]: 链上分析平台
        - generic [ref=f1e9]: Coverage-aware V1
    - navigation [ref=f1e10]:
      - button "数据下载 ▾" [ref=f1e11] [cursor=pointer]:
        - generic [ref=f1e17]: 数据下载
        - generic [ref=f1e18]: ▾
      - link "地址下载" [ref=f1e19] [cursor=pointer]:
        - /url: /
      - link "任务中心" [ref=f1e26] [cursor=pointer]:
        - /url: /jobs
      - link "状态总览" [ref=f1e32] [cursor=pointer]:
        - /url: /system
      - link "API 控制中心" [ref=f1e37] [cursor=pointer]:
        - /url: /api-control
      - button "数据浏览 ▾" [ref=f1e42] [cursor=pointer]:
        - generic [ref=f1e46]: 数据浏览
        - generic [ref=f1e47]: ▾
      - link "数据浏览" [ref=f1e48] [cursor=pointer]:
        - /url: /data-explorer
    - generic [ref=f1e53]: 数据下载 · Ankr Provider
  - main [ref=f1e54]:
    - generic [ref=f1e55]:
      - heading "数据浏览" [level=1] [ref=f1e56]
      - paragraph [ref=f1e57]: 浏览、筛选、更新和导出本地链上数据
      - generic [ref=f1e59]:
        - combobox "链" [ref=f1e61] [cursor=pointer]:
          - generic [ref=f1e65]: BSC
          - generic [ref=f1e66]: ⌄
        - textbox "搜索地址 / 前缀 / 后缀 / 名称 / 案件 / 标签" [ref=f1e67]
        - combobox [ref=f1e68]:
          - option "全部案件" [selected]
          - option "盘古"
        - combobox [ref=f1e69]:
          - option "全部标签" [selected]
          - option "入金地址"
        - combobox [ref=f1e70]:
          - option "标签 ANY" [selected]
          - option "标签 ALL"
      - generic [ref=f1e71]:
        - generic [ref=f1e72]: 已显示 1 个地址 · 已选 0
        - generic [ref=f1e73]:
          - button "更新" [disabled] [ref=f1e74]
          - button "查看组合数据" [disabled] [ref=f1e75]
          - button "导出 CSV 压缩包" [disabled] [ref=f1e76]
          - generic "导出交易的完整解码参数，会显著增加文件体积（§47）" [ref=f1e77]:
            - checkbox "Include Decoded Input" [ref=f1e78]
            - text: Include Decoded Input
      - table [ref=f1e80]:
        - rowgroup [ref=f1e81]:
          - row [ref=f1e82]:
            - columnheader [ref=f1e83]
            - columnheader "地址" [ref=f1e84]
            - columnheader "案件" [ref=f1e85]
            - columnheader "标签" [ref=f1e86]
            - columnheader "交易" [ref=f1e87]
            - columnheader "转账" [ref=f1e88]
            - columnheader "NFT" [ref=f1e89]
            - columnheader "Logs" [ref=f1e90]
            - columnheader "总量" [ref=f1e91]
            - columnheader "状态" [ref=f1e92]
            - columnheader "最后同步" [ref=f1e93]
            - columnheader [ref=f1e94]
        - rowgroup [ref=f1e95]:
          - row [ref=f1e96]:
            - cell [ref=f1e97]:
              - checkbox [ref=f1e98]
            - cell [ref=f1e99]:
              - link "0x9b77e3b4…" [ref=f1e100] [cursor=pointer]:
                - /url: /data-explorer/bsc/0x9b77e3b4f85c7cea7da451f454895edbd10b80dd
            - cell "盘古" [ref=f1e101]
            - cell "入金地址" [ref=f1e102]
            - cell "4" [ref=f1e104]
            - cell "242" [ref=f1e105]
            - cell "0" [ref=f1e106]
            - cell "0" [ref=f1e107]
            - cell "246" [ref=f1e108]
            - cell "current" [ref=f1e109]
            - cell "9/2/2026, 10:04:35 PM" [ref=f1e111]
            - cell [ref=f1e112]:
              - link "详情" [ref=f1e113] [cursor=pointer]:
                - /url: /data-explorer/bsc/0x9b77e3b4f85c7cea7da451f454895edbd10b80dd
```

# Test source

```ts
  1  | import { expect, test } from '@playwright/test'
  2  |
  3  | // 验收临时规格：UI-001 视口矩阵、UI-003 键盘、UI-004 离线/慢接口（跑完删除）
  4  |
  5  | for (const [w, h] of [[768, 1024], [1366, 768]] as const) {
  6  |   test('UI-001 viewport ' + w + 'x' + h + ' no horizontal overflow', async ({ page }) => {
  7  |     await page.setViewportSize({ width: w, height: h })
  8  |     for (const route of ['/', '/data-explorer', '/api-control', '/system']) {
  9  |       await page.goto(route)
  10 |       await page.waitForTimeout(2500)
  11 |       const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 1)
> 12 |       expect(overflow, route + ' must not overflow').toBe(false)
     |                                                      ^ Error: /data-explorer must not overflow
  13 |     }
  14 |   })
  15 | }
  16 |
  17 | test('UI-003 keyboard: Escape closes dialog and returns focus', async ({ page }) => {
  18 |   await page.goto('/data-explorer/bsc/0x9b77e3b4f85c7cea7da451f454895edbd10b80dd')
  19 |   await page.getByRole('button', { name: '修改案件和标签' }).click()
  20 |   const dialog = page.getByRole('dialog', { name: '修改案件和标签' })
  21 |   await expect(dialog).toBeVisible()
  22 |   await page.keyboard.press('Escape')
  23 |   await expect(dialog).toHaveCount(0)
  24 |   const focused = await page.evaluate(() => document.activeElement?.getAttribute('aria-label') ?? document.activeElement?.textContent?.slice(0, 30) ?? document.activeElement?.tagName)
  25 |   console.log('FOCUS_AFTER_ESC=' + JSON.stringify(focused))
  26 |   // token filter dialog focus trap
  27 |   await page.getByRole('button', { name: '代币筛选' }).click()
  28 |   await expect(page.getByRole('dialog', { name: '选择代币' })).toBeVisible()
  29 |   await page.keyboard.press('Escape')
  30 |   await expect(page.getByRole('dialog', { name: '选择代币' })).toHaveCount(0)
  31 | })
  32 |
  33 | test('UI-004 offline shows error dialog and recovers', async ({ page }) => {
  34 |   await page.goto('/data-explorer')
  35 |   await page.waitForTimeout(2000)
  36 |   await page.context().setOffline(true)
  37 |   // trigger a refetch by searching
  38 |   await page.getByPlaceholder(/搜索地址/).fill('0x9b77e3')
  39 |   await page.waitForTimeout(3000)
  40 |   const dialog = page.getByRole('dialog')
  41 |   const dialogCount = await dialog.count()
  42 |   console.log('OFFLINE_DIALOGS=' + dialogCount)
  43 |   await page.context().setOffline(false)
  44 |   await page.waitForTimeout(2500)
  45 |   const rows = await page.locator('tbody tr').count()
  46 |   console.log('RECOVERED_ROWS=' + rows)
  47 |   expect(rows).toBeGreaterThan(0)
  48 | })
  49 |
  50 | test('UI-004 slow API keeps loading state and completes', async ({ page }) => {
  51 |   await page.route('**/api/v1/**', async (route) => {
  52 |     await new Promise((resolve) => setTimeout(resolve, 4000))
  53 |     await route.continue()
  54 |   })
  55 |   await page.goto('/system')
  56 |   await page.waitForTimeout(6000)
  57 |   const dialogCount = await page.getByRole('dialog').count()
  58 |   const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 1)
  59 |   console.log('SLOW_DIALOGS=' + dialogCount + ' OVERFLOW=' + overflow)
  60 |   expect(overflow).toBe(false)
  61 | })
  62 |
```
