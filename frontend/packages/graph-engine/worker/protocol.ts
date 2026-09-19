import type { LayoutMode, LayoutOptions } from "../layout";

/**
 * 布局 Worker 的消息协议（用户端重构规范 V4 §30.2 / §30.5）
 *
 * 载荷全部是 **TypedArray + 数字**：字符串字典池**不传**——布局只关心几何与拓扑，
 * 把几万个地址字符串结构化克隆到 worker 再传回来，正是 §30.5 要避免的浪费。
 *
 * 数组用 transfer 移交（零拷贝）：发送方交出所有权，worker 用完再交还。
 * 因此调用方**不能**在 postMessage 之后继续读写那些数组。
 */

export interface LayoutRequest {
  type: "layout";
  requestId: number;
  mode: LayoutMode;
  options: LayoutOptions;
  nodeCount: number;
  edgeCount: number;
  positions: Float32Array;
  kinds: Uint8Array;
  edgeSource: Uint32Array;
  edgeTarget: Uint32Array;
  /** 时间轴布局需要；没有就传 null（**不伪造时间**，§64 #1）。 */
  times: Float64Array | null;
}

export interface LayoutDone {
  type: "layout:done";
  requestId: number;
  /** 实际采用的模式（auto 会解析成具体算法，调用方需要知道结果）。 */
  resolvedMode: LayoutMode;
  positions: Float32Array;
  durationMs: number;
}

export interface LayoutFailed {
  type: "layout:error";
  requestId: number;
  message: string;
}

export type LayoutResponse = LayoutDone | LayoutFailed;

/** 把请求体里需要 transfer 的 buffer 列出来（调用方 postMessage 的第二个参数）。 */
export function layoutTransferables(request: LayoutRequest): Transferable[] {
  const list: Transferable[] = [request.positions.buffer, request.kinds.buffer, request.edgeSource.buffer, request.edgeTarget.buffer];
  if (request.times) list.push(request.times.buffer);
  return list;
}
