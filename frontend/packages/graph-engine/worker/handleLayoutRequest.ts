import { buildCsr, type LayoutGraph } from "../model/indices";
import { runLayout } from "../layout";
import type { LayoutRequest, LayoutResponse } from "./protocol";

/**
 * 布局请求处理器（用户端重构规范 V4 §30.5）
 *
 * 刻意做成**与 worker 作用域无关的纯函数**：
 *   - `worker/graph.worker.ts` 只是把它接到 `self.onmessage`；
 *   - 验收脚本可以直接调用它，不需要真的起一个 Worker。
 *
 * 这样"布局逻辑"与"消息传输"是两个可以分别验证的东西——
 * 前者是算法正确性，后者是通道正确性，混在一起时失败信息毫无指向性。
 */
export function handleLayoutRequest(request: LayoutRequest): LayoutResponse {
  const started = Date.now();
  try {
    const { outOffset, outEdges, inOffset } = buildCsr(request.nodeCount, request.edgeCount, request.edgeSource, request.edgeTarget);
    const graph: LayoutGraph = {
      nodeCount: request.nodeCount,
      edgeCount: request.edgeCount,
      positions: request.positions,
      kinds: request.kinds,
      edgeSource: request.edgeSource,
      edgeTarget: request.edgeTarget,
      outOffset,
      outEdges,
      inOffset,
    };
    const resolvedMode = runLayout(request.mode, graph, request.options, request.times);
    return {
      type: "layout:done",
      requestId: request.requestId,
      resolvedMode,
      positions: request.positions,
      durationMs: Date.now() - started,
    };
  } catch (error) {
    return {
      type: "layout:error",
      requestId: request.requestId,
      message: error instanceof Error ? error.message : String(error),
    };
  }
}

/** worker 入口：把 self 上的消息接到处理器上。 */
export function attachLayoutWorker(scope: {
  addEventListener: (type: "message", listener: (event: { data: LayoutRequest }) => void) => void;
  postMessage: (message: LayoutResponse, transfer?: Transferable[]) => void;
}): void {
  scope.addEventListener("message", (event) => {
    const response = handleLayoutRequest(event.data);
    scope.postMessage(response, response.type === "layout:done" ? [response.positions.buffer] : undefined);
  });
}
