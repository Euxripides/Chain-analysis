/**
 * 布局 Worker 入口（用户端重构规范 V4 §30.2 `worker/graph.worker.ts`）
 *
 * 这里只做接线：真正的算法在 `handleLayoutRequest` 里，与 worker 作用域无关，
 * 因此可以脱离 Worker 单独验证（见 scripts/graph-engine-check.mjs）。
 *
 * Vite 侧用法（必须写在应用代码里，构建工具才能识别 worker 边界）：
 *   new Worker(new URL("@chain/graph-engine/worker/graph.worker.ts", import.meta.url), { type: "module" })
 * 与既有地址导入 Worker 一样依赖 **ES module worker**（vite.config 的 worker.format="es"）。
 */
import { attachLayoutWorker } from "./handleLayoutRequest";
import type { LayoutRequest } from "./protocol";

declare const self: {
  addEventListener: (type: "message", listener: (event: { data: LayoutRequest }) => void) => void;
  postMessage: (message: unknown, transfer?: Transferable[]) => void;
};

attachLayoutWorker(self as unknown as Parameters<typeof attachLayoutWorker>[0]);
