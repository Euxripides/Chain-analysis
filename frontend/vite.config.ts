import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

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
    host: '0.0.0.0',
    proxy: {
      // 只代理真正的 API 前缀；否则前端路由 /api-control 会被误送到后端并返回 404。
      '^/api/': { target: 'http://localhost:8080', changeOrigin: true },
      '/health': { target: 'http://localhost:8080', changeOrigin: true },
    },
  },
})
