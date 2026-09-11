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
  resolve: {
    // 顺序敏感：先匹配具体文件，再匹配包入口（对象形式的 alias 是前缀匹配）。
    alias: [
      { find: "@chain/design-tokens/tokens.css", replacement: shared("design-tokens/tokens.css") },
      { find: "@chain/ui/ui.css", replacement: shared("ui/ui.css") },
      { find: "@chain/ui", replacement: shared("ui/index.ts") },
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
          if (id.includes("node_modules/react") || id.includes("node_modules/scheduler")) return "react-vendor";
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
