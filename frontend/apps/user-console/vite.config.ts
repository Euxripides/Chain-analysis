import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { fileURLToPath, URL } from "node:url";

const root = fileURLToPath(new URL(".", import.meta.url));
const shared = (p: string) => fileURLToPath(new URL(`../../packages/${p}`, import.meta.url));

// 用户层系统（Investigator Console）。
// 灰度期挂载在 /app-v2/，与旧 SPA 并存（§29.3 Feature Flag 发布顺序）。
export default defineConfig({
  root,
  base: "/app-v2/",
  plugins: [react()],
  // 计划 1852：导入解析跑在 Web Worker 里；Worker 内部动态 import("xlsx") 会产生代码分割，
  // 而 Vite 默认的 worker.format="iife" 不支持代码分割构建，因此显式用 ES module worker
  // （与 parse-client.ts 的 new Worker(url, { type: "module" }) 一致；CSP script-src 'self' 允许同源模块）。
  worker: { format: "es" },
  resolve: {
    // 顺序敏感：先匹配具体文件，再匹配包入口（对象形式的 alias 是前缀匹配）。
    alias: [
      { find: "@chain/design-tokens/tokens.css", replacement: shared("design-tokens/tokens.css") },
      { find: "@chain/ui/ui.css", replacement: shared("ui/ui.css") },
      /* Dialog Core / Renderer 解耦后的子路径入口（规范 §21.0）。
         与 @chain/user-ui 同样必须排在包入口之前，否则会被前缀匹配成 index.ts/dialog-core。 */
      { find: "@chain/ui/dialog-core", replacement: shared("ui/dialog-core.tsx") },
      { find: "@chain/ui/dialog-legacy-renderer", replacement: shared("ui/dialog-legacy-renderer.tsx") },
      { find: "@chain/ui", replacement: shared("ui/index.ts") },
      /* @chain/user-ui：用户端专属设计系统（规范 §2.2）。
         具体子路径必须排在包入口之前，否则 @chain/user-ui/styles/*.css 会被
         前缀匹配成 index.ts/styles/*，形成"dev 能跑、production build 找不到包"的半接线状态。 */
      { find: "@chain/user-ui/theme/css-vars.css", replacement: shared("user-ui/theme/css-vars.css") },
      { find: "@chain/user-ui/styles/surface.css", replacement: shared("user-ui/styles/surface.css") },
      { find: "@chain/user-ui/styles/motion.css", replacement: shared("user-ui/styles/motion.css") },
      { find: "@chain/user-ui/styles/progress.css", replacement: shared("user-ui/styles/progress.css") },
      { find: "@chain/user-ui/styles/states.css", replacement: shared("user-ui/styles/states.css") },
      { find: "@chain/user-ui/styles/shell.css", replacement: shared("user-ui/styles/shell.css") },
      { find: "@chain/user-ui/styles/graph-shell.css", replacement: shared("user-ui/styles/graph-shell.css") },
      { find: "@chain/user-ui/styles/legacy-bridge.css", replacement: shared("user-ui/styles/legacy-bridge.css") },
      { find: "@chain/user-ui/styles/adapter.css", replacement: shared("user-ui/styles/adapter.css") },
      /* ProComponents 封装层走子路径：入口可达会让它进初始包（见 user-ui/index.ts 的说明）。 */
      { find: "@chain/user-ui/pro-table", replacement: shared("user-ui/components/pro-table.tsx") },
      { find: "@chain/user-ui", replacement: shared("user-ui/index.ts") },
      /* @chain/graph-engine：独立图谱引擎（规范 §30 / §60 Phase R6）。
         当前尚无页面引用它——按 §2.2 的要求，包一旦存在就必须**完整接线**，
         不能留下"TypeScript 找得到、Vite 找不到"的半接线状态。 */
      { find: "@chain/graph-engine", replacement: shared("graph-engine/index.ts") },
      { find: "@chain/graph-react", replacement: shared("graph-react/index.ts") },
      { find: "@chain/design-tokens", replacement: shared("design-tokens/index.ts") },
      { find: "@chain/api-client", replacement: shared("api-client/index.ts") },
    ],
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    rollupOptions: {
      output: {
        manualChunks(id) {
          /*
           * 前缀匹配必须精确到**包目录**，不能只写 `node_modules/react`：
           * 那个字符串同时是 `react-dom`、`react-slick`、`react-router` 的前缀，
           * 会把与 React 无关的库一并拖进 react-vendor。
           * 实测：ProComponents 依赖 react-slick，于是整个 ProComponents 被并进 react-vendor。
           */
          if (
            id.includes("node_modules/react/") ||
            id.includes("node_modules/react-dom/") ||
            id.includes("node_modules/scheduler/")
          ) {
            return "react-vendor";
          }
          /*
           * Ant Design / ProComponents 单独成 chunk（规范 §56.2 / §63E）。
           * 它们只被已迁移的页面按需引用；放进 common chunk 会让首页初始包直接超预算。
           * motion / echarts / ag-grid / xlsx 沿用各自既有的异步边界，不在这里合并。
           */
          /*
           * Ant Design / ProComponents 单独成 vendor chunk。
           *
           * 实测（pnpm build:user，gzip）：
           *   合并 vendor：react 75.85 + antd 89.22 + motion 40.89 + app 35.14 = 241.1 KB
           *   交给 Rollup 自然分块：react 75.85 + motion 40.89 + app 122.91      = 239.7 KB
           * 两者初始体积几乎相同（因为 @chain/user-ui 的 UserDialogRenderer 是入口静态依赖，
           * Ant Modal 无论如何都会进初始包），因此选择 vendor 拆分：
           * **应用代码 chunk 只有 35 KB**，改一行业务代码不会让用户重新下载 120 KB。
           *
           * 真正能再降一档的做法（留给 R7 bundle 预算专项）是把 UserDialogRenderer
           * 改成 React.lazy + 空闲预热；本轮不做，因为弹窗是业务确认链路，
           * 不能为了几十 KB 让"点确认后弹窗晚 1 个网络往返才出现"。
           */
          /*
           * 这里**刻意不**把 antd / @ant-design 合并成一个手动 chunk。
           *
           * 手动 chunk 是"全局归并"：只要 antd 或 @ant-design 的任意模块被入口引用，
           * 整个 chunk 就会被 index.html 预加载——包括只被懒加载路由用到的
           * @ant-design/pro-components。实测后果非常严重：
           *   antd-vendor 90 KB gzip → 536 KB gzip，初始包直接翻近三倍。
           *
           * 交给 Rollup 按动态 import 边界自然分块后：
           *   入口用到的 ConfigProvider/App 进初始包；
           *   只有调查报告页用到的 ProTable 留在 InvestigationsPage chunk 里。
           * 这正是规范 §56.1「ECharts / Graph / AG Grid 必须 route lazy-load」的同一条原则。
           */
          if (id.includes("node_modules/motion") || id.includes("node_modules/framer-motion")) return "motion-vendor";
          if (id.includes("node_modules/echarts") || id.includes("node_modules/zrender")) return "echarts-vendor";
        },
      },
    },
  },
  server: {
    port: 3001,
    host: "127.0.0.1",
    proxy: {
      "^/api/": { target: "http://localhost:8080", changeOrigin: true },
      "/health": { target: "http://localhost:8080", changeOrigin: true },
    },
  },
});
