/**
 * Canvas 工作区的数据模型（路由 /canvases/:canvasId）
 *
 * 这些类型直接对应后端 canvas service 的线格式（§61 / U-GRAPH-02）：
 * node_refs / edge_refs 是**字符串协议**，解析规则见 ./refs.ts。
 * 它们从 CanvasPage.tsx 整体移出，是为了让页面只保留编排逻辑（§61）。
 */

export type NodeKind = "address" | "tx" | "evidence";
export type Lens = "flow" | "time" | "asset" | "evidence";
export type RenderMode = "normal" | "aggregate" | "limit";
export type CanvasTool = "select" | "box" | "pan";
export type LayoutMode = "auto" | "columns" | "ring";

export interface CanvasRecord {
  id: string;
  name: string;
  revision: number;
  node_refs?: string[] | null;
  edge_refs?: string[] | null;
  groups?: Array<Record<string, unknown>> | null;
  annotations?: Array<Record<string, unknown>> | null;
  filters?: Record<string, unknown> | null;
  layout?: Record<string, unknown> | null;
  camera?: Record<string, unknown> | null;
  view_state?: Record<string, unknown> | null;
  case_id?: string | null;
  investigation_id?: string | null;
  parent_canvas_id?: string | null;
  updated_at?: string;
}

export interface CanvasState {
  name: string;
  node_refs: string[];
  edge_refs: string[];
  groups: Array<Record<string, unknown>>;
  annotations: Array<Record<string, unknown>>;
  filters: Record<string, unknown>;
  layout: Record<string, unknown>;
  camera: Record<string, unknown>;
  view_state: Record<string, unknown>;
}

export interface ParsedNode {
  ref: string;
  kind: NodeKind;
  chain?: string;
  id: string;
}

export interface ParsedEdge {
  ref: string;
  fromRef: string;
  toRef: string;
  amountRaw?: string;
  asset?: string;
}

export interface ParsedCanvas {
  nodes: ParsedNode[];
  edges: ParsedEdge[];
  badNodeRefs: string[];
  badEdgeRefs: string[];
}

export interface RenderNode {
  key: string;
  ref?: string;
  kind: NodeKind;
  chain?: string;
  label: string;
  sublabel?: string;
  count: number;
  aggregate: boolean;
  /** 由聚类展开产生的节点：进入时播放一次 spring 位移（蓝图 §7.2）。 */
  entering?: boolean;
  /** 聚合节点的原始 key，用于展开/收起。 */
  clusterKey?: string;
}

export interface RenderEdge {
  key: string;
  fromKey: string;
  toKey: string;
  amountRaw?: string;
  asset?: string;
  repeated: number;
}

export interface ClusterInfo {
  key: string;
  kind: NodeKind;
  chain?: string;
  count: number;
  label: string;
}

export interface EvidenceRow {
  id: string;
  title?: string;
  status?: string;
  nature?: string;
  content_hash?: string;
  version?: number;
  created_at?: string;
}

export type EvidenceEntry =
  | { phase: "loading" }
  | { phase: "ok"; data: EvidenceRow }
  | { phase: "unavailable"; status?: number };

export interface FlowEdge {
  direction?: string;
  counterparty?: string;
  asset?: string;
  amount?: string;
  txs?: number;
  transactions?: number;
  first?: string;
  last?: string;
  first_seen?: string;
  last_seen?: string;
}

export interface FlowResult {
  chain?: string;
  address?: string;
  transaction_count?: number;
  unique_counterparties?: number;
  edges?: FlowEdge[];
  algorithm_version?: string;
}

export interface FlowEnvelope {
  items?: FlowResult | null;
  source?: { provider?: string; tables?: string[]; data_version?: string; as_of?: string };
  caliber?: Record<string, unknown>;
}

export interface SnapshotRow {
  id: string;
  canvas_id?: string;
  canvas_revision?: number;
  name: string;
  created_at?: string;
}

export type Selection =
  | { kind: "node"; ref: string }
  | { kind: "edge"; ref: string }
  | { kind: "aggregate"; key: string }
  | null;
