/**
 * Canvas 工作区的常量与工具栏选项。
 *
 * 注意 LENSES 的顺序就是规范 §30.8 的工具栏顺序（资金流 / 时间 / 资产 / 证据），
 * 与 @chain/graph-engine 的 LENS_OPTIONS 同源；这里保留页面侧的呈现列表，
 * 但"某个透镜有什么效果"只在引擎的 LENS_EFFECTS 里定义一次。
 */

import type { CanvasTool, LayoutMode, Lens, NodeKind } from "./types";

export const NODE_KIND_LABEL: Record<NodeKind, string> = { address: "地址", tx: "交易", evidence: "证据" };
export const LENSES: Array<{ key: Lens; label: string }> = [
  { key: "flow", label: "资金流" },
  { key: "time", label: "时间" },
  { key: "asset", label: "资产" },
  { key: "evidence", label: "证据" },
];
export const TOOL_ITEMS: Array<{ key: CanvasTool; label: string }> = [
  { key: "select", label: "选择" },
  { key: "box", label: "框选" },
  { key: "pan", label: "平移" },
];
export const LAYOUT_ITEMS: Array<{ key: LayoutMode; label: string }> = [
  { key: "auto", label: "自动" },
  { key: "columns", label: "分层" },
  { key: "ring", label: "环形" },
];
export const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
export const ASSET_PALETTE = ["#175cd3", "#067647", "#b45309", "#7f56d9", "#c11574", "#088ab2"];
export const NODE_LIMIT_SOFT = 1000;
export const NODE_LIMIT_HARD = 2000;
export const SVG_W = 1000;
export const SVG_H = 600;
/** 单次聚类展开上限：超过时用 Dialog 说明，不静默渲染上千个元素（蓝图 §12.13）。 */
export const CLUSTER_EXPAND_LIMIT = 200;
/** 复杂标签的渲染预算：超过后只给选中/框选/路径相关对象画文字（蓝图 §12.13 "不能一次渲染全部复杂标签"）。 */
export const NODE_LABEL_BUDGET = 120;
export const EDGE_LABEL_BUDGET = 60;
/** 路径播放的步进间隔；reduced-motion 下不使用自动播放（蓝图 §7.4）。 */
export const PATH_STEP_MS = 1200;
