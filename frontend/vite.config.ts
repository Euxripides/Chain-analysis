import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// LEGACY-CONSOLE-01: 这个入口只服务老控制台，而老控制台自身没有登录页、只会在
// "同一个 host" 上借用新系统 /admin/login 的会话；局域网入口没有那个登录页，于是
// 2026-09-13（S7-SEC-LEGACY-V1 给 /api/v1 读路由加上强制认证）之后它看不到数据。
// 这里在**开发服务器侧**为"没有会话 Cookie"的请求注入一个只服务老线的凭据：
//   * 凭据来自服务端环境变量（LEGACY_CONSOLE_TOKEN），不进入前端产物、浏览器拿不到；
//   * 先删除客户端自带的同名头，客户端无法伪造；
//   * 已经带会话的请求不注入——真实会话永远优先。
// 后端只按 SHA-256 常量时间比对，凭据不符即视为未认证（401）。
// 用 globalThis 读取环境变量：根 tsconfig 不含 Node 类型，直接写 process 会 TS2580；
// 而为一个环境变量引入 @types/node 会新增依赖（发布冻结期间不做）。
const LEGACY_CONSOLE_TOKEN =
  (globalThis as { process?: { env?: Record<string, string | undefined> } }).process?.env?.LEGACY_CONSOLE_TOKEN ?? ''

function injectLegacyConsoleHeader(proxyReq: any, req: any) {
  proxyReq.removeHeader('X-Legacy-Console-Token')
  if (!LEGACY_CONSOLE_TOKEN) return
  const cookie = req?.headers?.cookie ?? ''
  if (cookie.includes('cs_user_session') || cookie.includes('cs_admin_session')) return
  proxyReq.setHeader('X-Legacy-Console-Token', LEGACY_CONSOLE_TOKEN)
}

export default defineConfig({
  plugins: [react()],
  build: {
    rollupOptions: {
      output: {
        // Keep the investigation shell small. Heavy graph/table/chart/export
        // engines are cached independently and loaded only by their routes.
        manualChunks(id) {
          if (id.includes('node_modules/echarts')) return 'charts'
          if (id.includes('node_modules/ag-grid')) return 'data-grid'
          if (id.includes('node_modules/xlsx')) return 'xlsx'
          if (id.includes('node_modules/react') || id.includes('node_modules/@tanstack')) return 'react-vendor'
        },
      },
    },
  },
  server: {
    port: 3000,
    // 决策 D3（2026-09-18）：老控制台**只留内网回环**。此前绑 0.0.0.0，局域网任何一台机器
    // 打开 http://<本机IP>:3000 就能无会话读到业务数据（该入口的 vite 代理对任意 ^/api/
    // 无 Cookie 请求注入服务凭据，且不校验 Referer）。内网排障请走 SSH 隧道访问本端口。
    host: '127.0.0.1',
    proxy: {
      // 只代理真正的 API 前缀；否则前端路由 /api-control 会被误送到后端并返回 404。
      '^/api/': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        configure: (proxy: any) => {
          proxy.on('proxyReq', (proxyReq: any, req: any) => injectLegacyConsoleHeader(proxyReq, req))
        },
      },
      '/health': { target: 'http://localhost:8080', changeOrigin: true },
    },
  },
})
