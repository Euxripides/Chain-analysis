/**
 * 渲染预算与降级（用户端重构规范 V4 §30.15 / §63C）
 *
 * 规范原文："若数据超过预算：**自动进入 aggregate/limit 模式，而不是浏览器卡死**。"
 * 这句话的重点是"自动"和"不卡死"——所以这里给出的是**可判定的档位**，
 * 而不是一组写在文档里、需要人肉判断的 FPS 目标。
 *
 * 阈值取自 §30.15 的固定夹具（G10K / G50K / G100K）与它们的边数配比，
 * 并在其之上留出"聚合"与"限额"两级：
 *   full      —— 全量绘制（配合 LOD 后 10k/30k 可稳定 60fps）
 *   aggregate —— 进入聚合模式：LOD 降采样 + 聚类（G4 的后续部分）
 *   limit     —— 超出可交互规模：**只保留窗口内的数据**（§63D 的 L3 原则），
 *                并在 UI 上明确告知，而不是假装一切正常
 */

export type RenderMode = "full" | "aggregate" | "limit";

export interface BudgetLimits {
  /** 超过这个节点数进入聚合模式。 */
  aggregateNodes: number;
  aggregateEdges: number;
  /** 超过这个规模进入限额模式。 */
  limitNodes: number;
  limitEdges: number;
}

export const DEFAULT_BUDGET_LIMITS: BudgetLimits = {
  aggregateNodes: 20_000,
  aggregateEdges: 60_000,
  limitNodes: 150_000,
  limitEdges: 450_000,
};

export interface BudgetDecision {
  mode: RenderMode;
  /** 给人看的判定理由（页面要把它放进弹窗，而不是页面正文——仓库规范）。 */
  reason: string;
  nodeCount: number;
  edgeCount: number;
}

export function decideBudget(
  nodeCount: number,
  edgeCount: number,
  limits: BudgetLimits = DEFAULT_BUDGET_LIMITS,
): BudgetDecision {
  if (nodeCount > limits.limitNodes || edgeCount > limits.limitEdges) {
    return {
      mode: "limit",
      reason: `规模超出可交互上限（${nodeCount} 节点 / ${edgeCount} 边）：只保留视口窗口内的数据`,
      nodeCount,
      edgeCount,
    };
  }
  if (nodeCount > limits.aggregateNodes || edgeCount > limits.aggregateEdges) {
    return {
      mode: "aggregate",
      reason: `规模较大（${nodeCount} 节点 / ${edgeCount} 边）：已启用聚合与细节层次`,
      nodeCount,
      edgeCount,
    };
  }
  return { mode: "full", reason: "规模在预算内", nodeCount, edgeCount };
}
