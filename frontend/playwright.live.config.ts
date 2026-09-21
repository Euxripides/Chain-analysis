import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './tests-live', workers: 1, reporter: 'list', timeout: 30_000,
  use: { baseURL: 'http://192.168.31.152:3000', channel: 'msedge', trace: 'retain-on-failure' },
  projects: [
    { name: 'desktop', use: { viewport: { width: 1440, height: 900 } } },
    { name: 'mobile', use: { viewport: { width: 390, height: 844 }, isMobile: true } },
  ],
})
