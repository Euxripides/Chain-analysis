import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { PointerEvent as ReactPointerEvent } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { ApiError } from "@chain/api-client";
import {
  BackLink,
  Card,
  EmptyState,
  ErrorState,
  EvidenceRail,
  PageHeader,
  SkeletonRows,
  StatusBadge,
  statusToneFor,
  dialogInputValue,
  useDialog,
} from "@chain/ui";
import type { EvidenceItem } from "@chain/ui";
import { formatBytes, formatRawAmount, formatTime, shortValue, useApi, useS5Action } from "../sprint5/common";

/**
 * U-GRAPH-02 / S5-FE-03 / USER-007：Canvas / Fund Flow 工作区（路由 /canvases/:canvasId）。
 *
 * 数据模型（与后端 canvas service 一致）：
 * - GET/PATCH /api/v2/canvases/{id}，revision 通过 If-Match 提交；
 * - node_refs：address:<chain>:<address> | tx:<chain>:<hash> | evidence:<uuid>；
 * - edge_refs：edge:<fromNodeRef>-><toNodeRef>:<amountRaw?>:<asset?>；
 * - 解析失败的 ref 在渲染中忽略，并通过 Dialog 列出坏引用数量；
 * - 所有说明 / 警告 / 错误 / 成功 / 确认只通过 useDialog 弹窗呈现。
 */

type NodeKind = "address" | "tx" | "evidence";
type Lens = "flow" | "time" | "asset" | "evidence";
type RenderMode = "normal" | "aggregate" | "limit";

interface CanvasRecord {
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

interface CanvasState {
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

interface ParsedNode {
  ref: string;
  kind: NodeKind;
  chain?: string;
  id: string;
}

interface ParsedEdge {
  ref: string;
  fromRef: string;
  toRef: string;
  amountRaw?: string;
  asset?: string;
}

interface ParsedCanvas {
  nodes: ParsedNode[];
  edges: ParsedEdge[];
  badNodeRefs: string[];
  badEdgeRefs: string[];
}

interface RenderNode {
  key: string;
  ref?: string;
  kind: NodeKind;
  chain?: string;
  label: string;
  sublabel?: string;
  count: number;
  aggregate: boolean;
}

interface RenderEdge {
  key: string;
  fromKey: string;
  toKey: string;
  amountRaw?: string;
  asset?: string;
  repeated: number;
}

interface EvidenceRow {
  id: string;
  title?: string;
  status?: string;
  nature?: string;
  content_hash?: string;
  version?: number;
  created_at?: string;
}

type EvidenceEntry =
  | { phase: "loading" }
  | { phase: "ok"; data: EvidenceRow }
  | { phase: "unavailable"; status?: number };

interface FlowEdge {
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

interface FlowResult {
  chain?: string;
  address?: string;
  transaction_count?: number;
  unique_counterparties?: number;
  edges?: FlowEdge[];
  algorithm_version?: string;
}

interface FlowEnvelope {
  items?: FlowResult | null;
  source?: { provider?: string; tables?: string[]; data_version?: string; as_of?: string };
  caliber?: Record<string, unknown>;
}

interface SnapshotRow {
  id: string;
  canvas_id?: string;
  canvas_revision?: number;
  name: string;
  created_at?: string;
}

const NODE_KIND_LABEL: Record<NodeKind, string> = { address: "地址", tx: "交易", evidence: "证据" };
const LENSES: Array<{ key: Lens; label: string }> = [
  { key: "flow", label: "资金流" },
  { key: "time", label: "时间" },
  { key: "asset", label: "资产" },
  { key: "evidence", label: "证据" },
];
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const ASSET_PALETTE = ["#175cd3", "#067647", "#b45309", "#7f56d9", "#c11574", "#088ab2"];
const NODE_LIMIT_SOFT = 1000;
const NODE_LIMIT_HARD = 2000;
const SVG_W = 1000;
const SVG_H = 600;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function asTrimmed(value: unknown): string | undefined {
  if (typeof value !== "string") return undefined;
  const text = value.trim();
  return text === "" ? undefined : text;
}

function parseNodeRef(raw: string): ParsedNode | null {
  const ref = raw.trim();
  if (!ref) return null;
  const firstColon = ref.indexOf(":");
  if (firstColon <= 0 || firstColon === ref.length - 1) return null;
  const kind = ref.slice(0, firstColon).toLowerCase();
  const rest = ref.slice(firstColon + 1);
  if (kind === "evidence") {
    const id = rest.trim();
    if (!UUID_RE.test(id)) return null;
    return { ref, kind, id };
  }
  if (kind !== "address" && kind !== "tx") return null;
  const chainEnd = rest.indexOf(":");
  if (chainEnd <= 0 || chainEnd === rest.length - 1) return null;
  const chain = rest.slice(0, chainEnd).trim();
  const id = rest.slice(chainEnd + 1).trim();
  if (!chain || !id || chain.includes(":") || id.includes(":")) return null;
  return { ref, kind, chain, id };
}

function encodeEdgeRef(fromRef: string, toRef: string, amountRaw?: string, asset?: string): string {
  let suffix = "";
  if (amountRaw && asset) suffix = `:${amountRaw}:${asset}`;
  else if (amountRaw) suffix = `:${amountRaw}`;
  else if (asset) suffix = `::${asset}`;
  return `edge:${fromRef}->${toRef}${suffix}`;
}

function parseEdgeRef(raw: string): ParsedEdge | null {
  const ref = raw.trim();
  if (!ref.toLowerCase().startsWith("edge:")) return null;
  const body = ref.slice(5);
  const arrow = body.indexOf("->");
  if (arrow <= 0) return null;
  const fromRef = body.slice(0, arrow).trim();
  const tail = body.slice(arrow + 2);
  const from = parseNodeRef(fromRef);
  if (!from) return null;
  const parts = tail.split(":");
  const kind = parts[0]?.trim().toLowerCase();
  let toRef = "";
  let rest: string[] = [];
  if (kind === "evidence") {
    if (parts.length < 2) return null;
    toRef = `${parts[0]}:${parts[1]}`;
    rest = parts.slice(2);
  } else if (kind === "address" || kind === "tx") {
    if (parts.length < 3) return null;
    toRef = `${parts[0]}:${parts[1]}:${parts[2]}`;
    rest = parts.slice(3);
  } else {
    return null;
  }
  const to = parseNodeRef(toRef);
  if (!to) return null;
  let amountRaw: string | undefined;
  let asset: string | undefined;
  if (rest.length === 1) {
    const value = rest[0].trim();
    if (/^-?\d+$/.test(value)) amountRaw = value;
    else asset = value || undefined;
  } else if (rest.length >= 2) {
    amountRaw = rest[0].trim() || undefined;
    asset = rest.slice(1).join(":").trim() || undefined;
  }
  return { ref, fromRef: from.ref, toRef: to.ref, amountRaw, asset };
}

function parseCanvasRefs(nodeRefs: string[], edgeRefs: string[]): ParsedCanvas {
  const nodes = new Map<string, ParsedNode>();
  const edges: ParsedEdge[] = [];
  const badNodeRefs: string[] = [];
  const badEdgeRefs: string[] = [];
  const seenEdges = new Set<string>();
  for (const raw of nodeRefs) {
    const parsed = parseNodeRef(raw);
    if (parsed) nodes.set(parsed.ref, parsed);
    else badNodeRefs.push(raw);
  }
  for (const raw of edgeRefs) {
    const parsed = parseEdgeRef(raw);
    if (!parsed) {
      badEdgeRefs.push(raw);
      continue;
    }
    if (!seenEdges.has(parsed.ref)) {
      seenEdges.add(parsed.ref);
      edges.push(parsed);
    }
    for (const endpoint of [parsed.fromRef, parsed.toRef]) {
      const node = parseNodeRef(endpoint);
      if (node) nodes.set(node.ref, node);
    }
  }
  return { nodes: [...nodes.values()], edges, badNodeRefs, badEdgeRefs };
}

function normalizeCanvasState(source: Partial<CanvasRecord>): CanvasState {
  const nodeRefs = Array.isArray(source.node_refs)
    ? source.node_refs.filter((ref): ref is string => typeof ref === "string")
    : [];
  const edgeRefs = Array.isArray(source.edge_refs)
    ? source.edge_refs.filter((ref): ref is string => typeof ref === "string")
    : [];
  const groups = Array.isArray(source.groups) ? source.groups.filter(isRecord) : [];
  const annotations = Array.isArray(source.annotations) ? source.annotations.filter(isRecord) : [];
  return {
    name: typeof source.name === "string" ? source.name : "",
    node_refs: nodeRefs,
    edge_refs: edgeRefs,
    groups,
    annotations,
    filters: isRecord(source.filters) ? source.filters : {},
    layout: isRecord(source.layout) ? source.layout : {},
    camera: isRecord(source.camera) ? source.camera : {},
    view_state: isRecord(source.view_state) ? source.view_state : {},
  };
}

function collectEvidenceIds(state: CanvasState | null): string[] {
  if (!state) return [];
  const ids = new Set<string>();
  const add = (value: string) => {
    const matches = value.matchAll(/evidence[:/]([0-9a-fA-F-]{36})/g);
    for (const match of matches) {
      const id = match[1].toLowerCase();
      if (UUID_RE.test(id)) ids.add(id);
    }
  };
  const walk = (value: unknown) => {
    if (typeof value === "string") {
      add(value);
      return;
    }
    if (Array.isArray(value)) {
      value.forEach(walk);
      return;
    }
    if (isRecord(value)) {
      const type = asTrimmed(value.type)?.toLowerCase();
      const id = asTrimmed(value.id);
      if (type === "evidence" && id && UUID_RE.test(id)) ids.add(id.toLowerCase());
      Object.values(value).forEach(walk);
    }
  };
  walk(state.node_refs);
  walk(state.edge_refs);
  walk(state.annotations);
  return [...ids];
}

function nodeRefLabel(node: ParsedNode | RenderNode): string {
  if ("kind" in node && node.kind === "evidence") {
    const id = "id" in node ? node.id : node.label;
    return `证据 ${shortValue(id, 6, 4)}`;
  }
  const id = "id" in node ? node.id : node.label;
  return shortValue(id, 8, 4);
}

function shortDate(value?: string | null): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value.slice(0, 10);
  return date.toISOString().slice(0, 10);
}

function readDiff(value: unknown): Array<{ field: string; submitted: unknown; current: unknown }> {
  if (!Array.isArray(value)) return [];
  return value
    .filter(isRecord)
    .map((entry) => ({
      field: asTrimmed(entry.field) ?? "unknown",
      submitted: entry.submitted,
      current: entry.current,
    }));
}

function diffText(value: unknown): string {
  if (typeof value === "string") return value.length > 240 ? `${value.slice(0, 240)}…` : value;
  try {
    const text = JSON.stringify(value);
    return text.length > 240 ? `${text.slice(0, 240)}…` : text;
  } catch {
    return String(value);
  }
}

function readStringArray(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : [];
}

function useIsMobile(): boolean {
  const [mobile, setMobile] = useState(
    () => typeof window !== "undefined" && window.matchMedia("(max-width: 767px)").matches,
  );
  useEffect(() => {
    if (typeof window === "undefined") return;
    const query = window.matchMedia("(max-width: 767px)");
    const update = () => setMobile(query.matches);
    update();
    query.addEventListener("change", update);
    return () => query.removeEventListener("change", update);
  }, []);
  return mobile;
}

function layoutPositions(nodes: RenderNode[], edges: RenderEdge[]): Map<string, { x: number; y: number }> {
  const raw = new Map<string, { x: number; y: number }>();
  if (nodes.length === 0) return raw;
  if (nodes.length > 80) {
    let index = 0;
    let ring = 0;
    while (index < nodes.length) {
      const capacity = ring === 0 ? 1 : 6 * ring;
      const radius = ring === 0 ? 0 : 80 + ring * 92;
      for (let slot = 0; slot < capacity && index < nodes.length; slot += 1, index += 1) {
        const angle = (2 * Math.PI * slot) / capacity - Math.PI / 2;
        raw.set(nodes[index].key, { x: Math.cos(angle) * radius, y: Math.sin(angle) * radius });
      }
      ring += 1;
    }
  } else {
    const adjacency = new Map<string, string[]>();
    const indegree = new Map<string, number>(nodes.map((node) => [node.key, 0]));
    for (const edge of edges) {
      if (!indegree.has(edge.fromKey) || !indegree.has(edge.toKey)) continue;
      adjacency.set(edge.fromKey, [...(adjacency.get(edge.fromKey) ?? []), edge.toKey]);
      indegree.set(edge.toKey, (indegree.get(edge.toKey) ?? 0) + 1);
    }
    const depth = new Map<string, number>();
    const queue: string[] = [];
    for (const node of nodes) {
      if ((indegree.get(node.key) ?? 0) === 0) {
        depth.set(node.key, 0);
        queue.push(node.key);
      }
    }
    if (queue.length === 0) {
      depth.set(nodes[0].key, 0);
      queue.push(nodes[0].key);
    }
    for (let i = 0; i < queue.length; i += 1) {
      const current = queue[i];
      const currentDepth = depth.get(current) ?? 0;
      for (const next of adjacency.get(current) ?? []) {
        if (depth.has(next)) continue;
        depth.set(next, currentDepth + 1);
        queue.push(next);
      }
    }
    for (const node of nodes) if (!depth.has(node.key)) depth.set(node.key, 0);
    const columns = new Map<number, string[]>();
    for (const node of nodes) {
      const level = depth.get(node.key) ?? 0;
      if (!columns.has(level)) columns.set(level, []);
      columns.get(level)!.push(node.key);
    }
    for (const [level, keys] of columns) {
      keys.forEach((key, index) => {
        raw.set(key, { x: level * 260, y: index * 105 });
      });
    }
  }
  const xs = [...raw.values()].map((point) => point.x);
  const ys = [...raw.values()].map((point) => point.y);
  const minX = Math.min(...xs);
  const maxX = Math.max(...xs);
  const minY = Math.min(...ys);
  const maxY = Math.max(...ys);
  const spanX = maxX - minX || 1;
  const spanY = maxY - minY || 1;
  const pad = 96;
  const scale = Math.min((SVG_W - pad * 2) / spanX, (SVG_H - pad * 2) / spanY);
  const offsetX = (SVG_W - spanX * scale) / 2 - minX * scale;
  const offsetY = (SVG_H - spanY * scale) / 2 - minY * scale;
  const placed = new Map<string, { x: number; y: number }>();
  for (const [key, point] of raw) {
    placed.set(key, { x: point.x * scale + offsetX, y: point.y * scale + offsetY });
  }
  return placed;
}

export default function CanvasPage() {
  const { canvasId = "" } = useParams();
  const navigate = useNavigate();
  const dialog = useDialog();
  const { client, busy: actionBusyHook } = useS5Action();
  const canvasQuery = useApi<CanvasRecord>(canvasId ? `/api/v2/canvases/${encodeURIComponent(canvasId)}` : null);

  const [state, setState] = useState<CanvasState | null>(null);
  const [revision, setRevision] = useState(0);
  const [dirty, setDirty] = useState(false);
  const [selection, setSelection] = useState<
    { kind: "node"; ref: string } | { kind: "aggregate"; key: string } | null
  >(null);
  const [lens, setLens] = useState<Lens>("flow");
  const [search, setSearch] = useState("");
  const [zoom, setZoom] = useState(1);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const [svgOpen, setSvgOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [actionBusy, setActionBusy] = useState(false);
  const [flowBusy, setFlowBusy] = useState(false);
  const [evidenceMap, setEvidenceMap] = useState<Record<string, EvidenceEntry>>({});

  const isMobile = useIsMobile();
  const loadedCanvasRef = useRef<string | null>(null);
  const badRefDialogRef = useRef("");
  const budgetDialogRef = useRef("");
  const inspectorRef = useRef<HTMLDivElement | null>(null);
  const dragRef = useRef<{ x: number; y: number; panX: number; panY: number; moved: boolean } | null>(null);

  const parsed = useMemo<ParsedCanvas>(
    () => parseCanvasRefs(state?.node_refs ?? [], state?.edge_refs ?? []),
    [state],
  );
  const totalNodes = parsed.nodes.length;
  const renderMode: RenderMode =
    totalNodes > NODE_LIMIT_HARD ? "limit" : totalNodes > NODE_LIMIT_SOFT ? "aggregate" : "normal";

  const nodeMeta = useMemo(() => {
    const out: Record<string, { first_seen?: string; last_seen?: string }> = {};
    const raw = state?.view_state?.node_meta;
    if (!isRecord(raw)) return out;
    for (const [key, value] of Object.entries(raw)) {
      if (!isRecord(value)) continue;
      const first = asTrimmed(value.first_seen);
      const last = asTrimmed(value.last_seen);
      if (first || last) out[key] = { first_seen: first, last_seen: last };
    }
    return out;
  }, [state]);

  const nodeSources = useMemo(() => {
    const out: Record<string, string> = {};
    const raw = state?.view_state?.node_sources;
    if (!isRecord(raw)) return out;
    for (const [key, value] of Object.entries(raw)) {
      const source = asTrimmed(value);
      if (source) out[key] = source;
    }
    return out;
  }, [state]);

  const evidenceIds = useMemo(() => collectEvidenceIds(state), [state]);
  const evidenceKey = evidenceIds.join(",");

  const visibleNodes = useMemo(() => {
    let nodes = parsed.nodes;
    if (lens === "evidence") {
      const evidenceRefs = new Set(nodes.filter((node) => node.kind === "evidence").map((node) => node.ref));
      const keep = new Set(evidenceRefs);
      for (const edge of parsed.edges) {
        if (evidenceRefs.has(edge.fromRef)) keep.add(edge.toRef);
        if (evidenceRefs.has(edge.toRef)) keep.add(edge.fromRef);
      }
      nodes = nodes.filter((node) => keep.has(node.ref));
    }
    const query = search.trim().toLowerCase();
    if (!query) return nodes;
    return nodes.filter(
      (node) => node.ref.toLowerCase().includes(query) || node.id.toLowerCase().includes(query),
    );
  }, [parsed, lens, search]);

  const visibleNodeRefs = useMemo(() => new Set(visibleNodes.map((node) => node.ref)), [visibleNodes]);

  const visibleEdges = useMemo(
    () => parsed.edges.filter((edge) => visibleNodeRefs.has(edge.fromRef) && visibleNodeRefs.has(edge.toRef)),
    [parsed.edges, visibleNodeRefs],
  );

  const { renderNodes, renderEdges } = useMemo(() => {
    if (renderMode === "normal") {
      const nodes: RenderNode[] = visibleNodes.map((node) => ({
        key: node.ref,
        ref: node.ref,
        kind: node.kind,
        chain: node.chain,
        label: nodeRefLabel(node),
        sublabel: lens === "time" ? `${shortDate(nodeMeta[node.ref]?.first_seen)} → ${shortDate(nodeMeta[node.ref]?.last_seen)}` : undefined,
        count: 1,
        aggregate: false,
      }));
      const edges: RenderEdge[] = visibleEdges.map((edge) => ({
        key: edge.ref,
        fromKey: edge.fromRef,
        toKey: edge.toRef,
        amountRaw: edge.amountRaw,
        asset: edge.asset,
        repeated: 1,
      }));
      return { renderNodes: nodes, renderEdges: edges };
    }
    const nodeMap = new Map(visibleNodes.map((node) => [node.ref, node]));
    const groupOf = (node: ParsedNode) =>
      renderMode === "limit" ? node.kind : `${node.kind}:${node.chain ?? "unknown"}`;
    const groups = new Map<string, { kind: NodeKind; chain?: string; count: number }>();
    for (const node of visibleNodes) {
      const key = groupOf(node);
      const group = groups.get(key);
      if (group) group.count += 1;
      else groups.set(key, { kind: node.kind, chain: node.chain, count: 1 });
    }
    const nodes: RenderNode[] = [...groups.entries()].map(([key, group]) => ({
      key,
      kind: group.kind,
      chain: group.chain,
      label:
        group.kind === "address" || group.kind === "tx"
          ? `${NODE_KIND_LABEL[group.kind]} · ${group.chain ?? "unknown"}`
          : `${NODE_KIND_LABEL[group.kind]}节点`,
      sublabel: `${group.count} 个节点`,
      count: group.count,
      aggregate: true,
    }));
    const pairs = new Map<string, RenderEdge>();
    for (const edge of visibleEdges) {
      const from = nodeMap.get(edge.fromRef);
      const to = nodeMap.get(edge.toRef);
      if (!from || !to) continue;
      const fromKey = groupOf(from);
      const toKey = groupOf(to);
      if (fromKey === toKey) continue;
      const key = `${fromKey}->${toKey}`;
      const existing = pairs.get(key);
      if (existing) existing.repeated += 1;
      else pairs.set(key, { key, fromKey, toKey, repeated: 1 });
    }
    return { renderNodes: nodes, renderEdges: [...pairs.values()] };
  }, [renderMode, visibleNodes, visibleEdges, lens, nodeMeta]);

  const positions = useMemo(() => layoutPositions(renderNodes, renderEdges), [renderNodes, renderEdges]);

  const stateBytes = useMemo(() => {
    if (!state) return 0;
    try {
      return new TextEncoder().encode(JSON.stringify(state)).length;
    } catch {
      return 0;
    }
  }, [state]);

  const applyServerRecord = useCallback((record: Partial<CanvasRecord>) => {
    setState(normalizeCanvasState(record));
    setRevision(typeof record.revision === "number" ? record.revision : Number(record.revision ?? 0));
    setDirty(false);
    setSelection(null);
  }, []);

  useEffect(() => {
    loadedCanvasRef.current = null;
    badRefDialogRef.current = "";
    budgetDialogRef.current = "";
    setState(null);
    setRevision(0);
    setDirty(false);
    setSelection(null);
    setSearch("");
    setZoom(1);
    setPan({ x: 0, y: 0 });
    setSvgOpen(false);
  }, [canvasId]);

  useEffect(() => {
    const record = canvasQuery.data;
    if (!record || record.id !== canvasId) return;
    if (loadedCanvasRef.current === record.id) return;
    loadedCanvasRef.current = record.id;
    applyServerRecord(record);
  }, [canvasId, canvasQuery.data, applyServerRecord]);

  useEffect(() => {
    if (!state) return;
    if (parsed.badNodeRefs.length === 0 && parsed.badEdgeRefs.length === 0) return;
    const key = `${canvasId}:bad:${parsed.badNodeRefs.length}:${parsed.badEdgeRefs.length}`;
    if (badRefDialogRef.current === key) return;
    badRefDialogRef.current = key;
    dialog.notify(
      "warning",
      "存在无法解析的引用",
      "以下引用不符合 Canvas ref 约定，已在渲染中忽略；本地编辑与保存不受影响。",
      [
        { label: "坏节点引用数量", value: String(parsed.badNodeRefs.length) },
        { label: "坏边引用数量", value: String(parsed.badEdgeRefs.length) },
        ...parsed.badNodeRefs.slice(0, 5).map((ref, index) => ({
          label: `坏节点 ${index + 1}`,
          value: <span className="mono">{ref}</span>,
        })),
        ...parsed.badEdgeRefs.slice(0, 5).map((ref, index) => ({
          label: `坏边 ${index + 1}`,
          value: <span className="mono">{ref}</span>,
        })),
      ],
    );
  }, [canvasId, dialog, parsed, state]);

  useEffect(() => {
    if (!state || renderMode === "normal") return;
    const key = `${canvasId}:${renderMode}:${totalNodes}`;
    if (budgetDialogRef.current === key) return;
    budgetDialogRef.current = key;
    dialog.notify(
      "warning",
      renderMode === "limit" ? "节点数超过 2000：已停止扩展" : "节点数超过 1000：已启用简化渲染",
      renderMode === "limit"
        ? "已超过渲染预算上限，仅展示聚合视图，新增节点操作已停用。"
        : "已超过完整渲染预算，当前按类型与链聚合展示；可继续编辑并保存。",
      [
        { label: "节点总数", value: String(totalNodes) },
        { label: "边总数", value: String(parsed.edges.length) },
        { label: "渲染模式", value: renderMode === "limit" ? "聚合视图（停止扩展）" : "聚合视图（简化渲染）" },
        { label: "渲染节点数", value: String(renderNodes.length) },
        { label: "渲染预算", value: `≤${NODE_LIMIT_SOFT} 完整 / ≤${NODE_LIMIT_HARD} 简化` },
      ],
    );
  }, [canvasId, dialog, parsed.edges.length, renderMode, renderNodes.length, state, totalNodes]);

  useEffect(() => {
    if (!state) return;
    if (selection?.kind === "node" && parsed.nodes.some((node) => node.ref === selection.ref)) return;
    if (selection?.kind === "aggregate" && renderNodes.some((node) => node.key === selection.key)) return;
    if (selection === null || selection.kind === "node") {
      const first = parsed.nodes[0];
      setSelection(first ? { kind: "node", ref: first.ref } : null);
    }
  }, [parsed.nodes, renderNodes, selection, state]);

  useEffect(() => {
    if (!evidenceKey) {
      setEvidenceMap({});
      return;
    }
    const ids = evidenceKey.split(",");
    const controller = new AbortController();
    setEvidenceMap((previous) => {
      const next: Record<string, EvidenceEntry> = {};
      for (const id of ids) next[id] = previous[id]?.phase === "ok" ? previous[id] : { phase: "loading" };
      return next;
    });
    void Promise.all(
      ids.map(async (id) => {
        try {
          const response = await client.get<EvidenceRow>(`/api/v2/evidence/${encodeURIComponent(id)}`, {
            signal: controller.signal,
          });
          return [id, { phase: "ok", data: response.data }] as const;
        } catch (error) {
          if (controller.signal.aborted) return null;
          return [
            id,
            { phase: "unavailable", status: error instanceof ApiError ? error.status : undefined },
          ] as const;
        }
      }),
    ).then((entries) => {
      if (controller.signal.aborted) return;
      const next: Record<string, EvidenceEntry> = {};
      for (const entry of entries) {
        if (!entry) continue;
        next[entry[0]] = entry[1];
      }
      setEvidenceMap(next);
    });
    return () => controller.abort();
  }, [client, evidenceKey]);

  const caseId = canvasQuery.data?.case_id ?? null;
  const selectedAddressNode =
    selection?.kind === "node"
      ? parsed.nodes.find((node) => node.ref === selection.ref && node.kind === "address") ?? null
      : null;
  const canvasName = state?.name ?? canvasQuery.data?.name ?? "画布";

  const inspectorItems: EvidenceItem[] = useMemo(() => {
    const items: EvidenceItem[] = [];
    if (selection?.kind === "aggregate") {
      const node = renderNodes.find((entry) => entry.key === selection.key);
      items.push({ label: "节点", value: <span className="mono">{selection.key}</span> });
      items.push({ label: "类型", value: "聚合组" });
      items.push({ label: "节点数", value: String(node?.count ?? "—") });
      items.push({ label: "ID", value: node?.chain ?? "—" });
      items.push({ label: "所在案件", value: caseId ? <span className="mono">{shortValue(caseId, 8, 6)}</span> : "—" });
      items.push({ label: "数据来源", value: "本地聚合渲染" });
      return items;
    }
    const ref = selection?.kind === "node" ? selection.ref : null;
    const node = ref ? parsed.nodes.find((entry) => entry.ref === ref) ?? null : null;
    items.push({
      label: "节点",
      value: ref ? (
        <span className="mono" title={ref}>
          {shortValue(ref, 18, 10)}
        </span>
      ) : (
        "—"
      ),
    });
    items.push({
      label: "ID",
      value: node?.id ? <span className="mono">{shortValue(node.id, 14, 8)}</span> : "—",
    });
    items.push({
      label: "类型",
      value: node ? NODE_KIND_LABEL[node.kind] : ref?.toLowerCase().startsWith("evidence:") ? "证据" : "—",
    });
    items.push({ label: "链", value: node?.chain ?? "—" });
    items.push({
      label: "所在案件",
      value: caseId ? <span className="mono">{shortValue(caseId, 10, 6)}</span> : "—",
    });
    items.push({ label: "数据来源", value: ref ? nodeSources[ref] ?? "Canvas 引用（来源未标注）" : "—" });
    const meta = ref ? nodeMeta[ref] : undefined;
    if (meta) {
      items.push({
        label: "首见 / 末见",
        value: `${shortDate(meta.first_seen)} → ${shortDate(meta.last_seen)}`,
      });
    }
    if (state) items.push({ label: "本地状态", value: formatBytes(stateBytes) });
    return items;
  }, [caseId, nodeMeta, nodeSources, parsed.nodes, renderNodes, selection, state, stateBytes]);

  const evidenceItems: EvidenceItem[] = useMemo(() => {
    const items: EvidenceItem[] = [];
    evidenceIds.forEach((id, index) => {
      const entry = evidenceMap[id];
      const prefix = `证据 ${index + 1}（${shortValue(id, 6, 4)}）`;
      items.push({ label: `${prefix} · Evidence`, value: <span className="mono">{shortValue(id, 8, 4)}</span> });
      if (!entry || entry.phase === "loading") {
        items.push({ label: `${prefix} · 状态`, value: "读取中" });
        return;
      }
      if (entry.phase === "unavailable") {
        items.push({ label: `${prefix} · 状态`, value: <StatusBadge tone="neutral">不可用</StatusBadge> });
        items.push({ label: `${prefix} · 读取结果`, value: entry.status ? `HTTP ${entry.status}` : "读取失败" });
        return;
      }
      const row = entry.data;
      items.push({ label: `${prefix} · 标题`, value: row.title ?? "—" });
      items.push({
        label: `${prefix} · 状态`,
        value: <StatusBadge tone={statusToneFor(row.status ?? "")}>{row.status ?? "—"}</StatusBadge>,
      });
      items.push({ label: `${prefix} · 性质`, value: row.nature ?? "—" });
      items.push({
        label: `${prefix} · content_hash`,
        value: <span className="mono">{shortValue(row.content_hash ?? "", 12, 8)}</span>,
      });
      items.push({ label: `${prefix} · 版本`, value: `v${row.version ?? "—"}` });
    });
    if (items.length === 0) items.push({ label: "Evidence 引用", value: "—" });
    return items;
  }, [evidenceIds, evidenceMap]);

  const toRequestBody = (source: CanvasState) => ({
    name: source.name,
    node_refs: source.node_refs,
    edge_refs: source.edge_refs,
    groups: source.groups,
    annotations: source.annotations,
    filters: source.filters,
    layout: source.layout,
    camera: source.camera,
    view_state: source.view_state,
  });

  const fail = useCallback(
    (title: string, message: string, status?: number) => {
      dialog.notify("error", title, message, status ? [{ label: "HTTP", value: String(status) }] : undefined);
    },
    [dialog],
  );

  const handleRevisionConflict = useCallback(
    async (error: ApiError) => {
      const details = error.details ?? {};
      const diff = readDiff(details.diff);
      const currentRaw = details.current;
      const current = isRecord(currentRaw) ? (currentRaw as unknown as Partial<CanvasRecord>) : null;
      const answer = await dialog.open({
        kind: "warning",
        title: "画布已被其他客户端更新",
        body: "服务端 revision 已变化，未自动覆盖。请选择加载服务端版本，或保留本地修改后再次比较。",
        details:
          diff.length > 0
            ? diff.map((entry) => ({
                label: entry.field,
                value: (
                  <span>
                    <span style={{ color: "var(--c-cobalt)" }}>提交：</span>
                    <span className="mono">{diffText(entry.submitted)}</span>
                    <br />
                    <span style={{ color: "var(--c-danger)" }}>当前：</span>
                    <span className="mono">{diffText(entry.current)}</span>
                  </span>
                ),
              }))
            : [
                { label: "字段级 diff", value: "服务端未返回 diff" },
                { label: "服务端 revision", value: String(details.current_revision ?? "—") },
                { label: "本地 revision", value: String(revision) },
              ],
        actions: [
          { label: "保留我的修改", result: "local", variant: "default" },
          { label: "加载服务端版本", result: "server", variant: "primary" },
        ],
      });
      if (answer === "server") {
        if (current) {
          applyServerRecord(current);
          dialog.notify("info", "已加载服务端版本", null, [
            { label: "revision", value: `r${current.revision ?? "—"}` },
            { label: "名称", value: current.name ?? "—" },
          ]);
        } else {
          dialog.notify("warning", "服务端未返回完整版本", "details.current 缺失，无法自动加载；本地修改保持不变。");
        }
        return;
      }
      if (answer === "local") {
        dialog.notify(
          "warning",
          "已保留本地修改",
          "未自动提交。请先比较服务端字段差异；再次保存时会重新进行 If-Match 并发校验。",
        );
      }
    },
    [applyServerRecord, dialog, revision],
  );

  const save = useCallback(async () => {
    if (!state || !canvasId) return;
    setSaving(true);
    try {
      const response = await client.patch<CanvasRecord>(
        `/api/v2/canvases/${encodeURIComponent(canvasId)}`,
        toRequestBody(state),
        { ifMatch: String(revision) },
      );
      const nextRevision = typeof response.data?.revision === "number" ? response.data.revision : revision + 1;
      setRevision(nextRevision);
      setDirty(false);
      dialog.notify("success", "画布已保存", null, [
        { label: "revision", value: `r${nextRevision}` },
        { label: "节点 refs", value: String(state.node_refs.length) },
        { label: "边 refs", value: String(state.edge_refs.length) },
      ]);
    } catch (error) {
      if (error instanceof ApiError && error.status === 412) {
        await handleRevisionConflict(error);
      } else if (error instanceof ApiError && (error.status === 422 || error.code === "BUSINESS_CROSS_CASE_EVIDENCE")) {
        const details = error.details ?? {};
        const crossCaseIds = readStringArray(details.cross_case_ids);
        const missingIds = readStringArray(details.missing_ids);
        dialog.notify("error", "跨案件 Evidence 引用被拒绝", error.message, [
          {
            label: "cross_case_ids",
            value: crossCaseIds.length ? crossCaseIds.join("、") : "—",
          },
          { label: "missing_ids", value: missingIds.length ? missingIds.join("、") : "—" },
          { label: "本地编辑", value: "已保留，未清空" },
        ]);
      } else if (error instanceof ApiError) {
        fail("保存失败", error.userMessage, error.status);
      } else {
        fail("保存失败", error instanceof Error ? error.message : "请稍后重试");
      }
    } finally {
      setSaving(false);
    }
  }, [canvasId, client, dialog, fail, handleRevisionConflict, revision, state]);

  const branch = async () => {
    if (!state || !canvasId) return;
    const name = await dialog.prompt({
      title: "创建分支画布",
      input: {
        label: "名称",
        defaultValue: `${state.name || "画布"} · 分支`,
        pattern: /^.{1,120}$/,
        patternHint: "1-120 个字符",
      },
      submitLabel: "创建",
    });
    const trimmed = (name ?? "").trim();
    if (!trimmed) return;
    setActionBusy(true);
    try {
      const response = await client.post<CanvasRecord>(
        `/api/v2/canvases/${encodeURIComponent(canvasId)}/branches`,
        { ...toRequestBody(state), name: trimmed },
      );
      dialog.notify("success", "分支画布已创建", null, [
        { label: "分支 ID", value: <span className="mono">{response.data?.id ?? "—"}</span> },
        { label: "名称", value: response.data?.name ?? trimmed },
        { label: "来源画布", value: <span className="mono">{canvasId}</span> },
      ]);
    } catch (error) {
      if (error instanceof ApiError) fail("分支创建失败", error.userMessage, error.status);
      else fail("分支创建失败", error instanceof Error ? error.message : "请稍后重试");
    } finally {
      setActionBusy(false);
    }
  };

  const openSnapshots = async () => {
    if (!canvasId) return;
    setActionBusy(true);
    try {
      const response = await client.get<{ items: SnapshotRow[] }>(
        `/api/v2/canvases/${encodeURIComponent(canvasId)}/snapshots`,
      );
      const items = response.data?.items ?? [];
      const answer = await dialog.open({
        kind: "info",
        title: "画布快照",
        body: items.length
          ? "快照固化创建时的 revision 与完整状态，不随主线更新。"
          : "当前画布还没有快照。",
        details: items.length
          ? items.slice(0, 20).map((snapshot, index) => ({
              label: `快照 ${index + 1}`,
              value: (
                <span>
                  {snapshot.name} · r{snapshot.canvas_revision ?? "—"} · {formatTime(snapshot.created_at)}
                </span>
              ),
            }))
          : [{ label: "快照", value: "—" }],
        actions: [
          { label: "关闭", result: "", variant: "default" },
          { label: "新建快照", result: "create", variant: "primary" },
        ],
      });
      if (answer !== "create") return;
      const name = await dialog.prompt({
        title: "新建快照",
        input: { label: "名称", defaultValue: `修订 r${revision}`, pattern: /^.{1,120}$/ },
        submitLabel: "保存快照",
      });
      if (!name) return;
      const created = await client.post<SnapshotRow>(
        `/api/v2/canvases/${encodeURIComponent(canvasId)}/snapshots`,
        { name },
      );
      dialog.notify("success", "快照已固化", null, [
        { label: "名称", value: created.data?.name ?? name },
        { label: "画布 revision", value: `r${created.data?.canvas_revision ?? revision}` },
        { label: "快照 ID", value: <span className="mono">{created.data?.id ?? "—"}</span> },
      ]);
    } catch (error) {
      if (error instanceof ApiError) fail("快照操作失败", error.userMessage, error.status);
      else fail("快照操作失败", error instanceof Error ? error.message : "请稍后重试");
    } finally {
      setActionBusy(false);
    }
  };

  const exportCanvas = async () => {
    const size = stateBytes;
    const answer = await dialog.open({
      kind: "info",
      title: "通过导出中心导出",
      body: "当前页面不生成文件；导出由导出中心异步创建任务，完成后从下载票据获取。",
      details: [
        { label: "画布", value: canvasName },
        { label: "revision", value: `r${revision}` },
        { label: "本地状态", value: formatBytes(size) },
      ],
      actions: [
        { label: "关闭", result: "", variant: "default" },
        { label: "前往导出中心", result: "go", variant: "primary" },
      ],
    });
    if (answer === "go") navigate("/exports");
  };

  const addAddressFlow = async (presetChain?: string, presetAddress?: string) => {
    if (!state || !canvasId) return;
    let chain = presetChain;
    let address = presetAddress;
    if (!chain) {
      chain = dialogInputValue(
        await dialog.prompt({
          title: "加入地址资金流（1/2）",
          input: { label: "链", defaultValue: "tron", pattern: /^[a-z0-9_-]{2,32}$/, patternHint: "小写链 slug" },
          submitLabel: "下一步",
        }),
      ) ?? undefined;
    }
    if (!chain) return;
    if (!address) {
      address = dialogInputValue(
        await dialog.prompt({
          title: "加入地址资金流（2/2）",
          input: { label: "地址", pattern: /^\S{10,128}$/, patternHint: "10-128 个非空白字符" },
          submitLabel: "查询本地索引",
        }),
      ) ?? undefined;
    }
    const cleanChain = chain.trim();
    const cleanAddress = (address ?? "").trim();
    if (!cleanChain || !cleanAddress) return;
    const confirmed = await dialog.confirm({
      kind: "info",
      title: "从本地索引加入资金流",
      body: "数据来自本地索引的一跳聚合，不调用 Provider；仅表示索引内的转账关系。",
      details: [
        { label: "链", value: cleanChain },
        { label: "地址", value: <span className="mono">{cleanAddress}</span> },
      ],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "加入", result: "ok", variant: "primary" },
      ],
    });
    if (confirmed !== "ok") return;
    setFlowBusy(true);
    try {
      const response = await client.get<FlowEnvelope>(
        `/api/v2/explorer/addresses/${encodeURIComponent(cleanChain)}/${encodeURIComponent(cleanAddress)}/flow-profile`,
      );
      const flow = response.data?.items;
      const flowEdges = Array.isArray(flow?.edges) ? flow.edges : [];
      if (!flow || flowEdges.length === 0) {
        dialog.notify("info", "本地索引没有可加入的资金流", null, [
          { label: "链", value: cleanChain },
          { label: "地址", value: cleanAddress },
          { label: "交易数", value: String(flow?.transaction_count ?? 0) },
          { label: "对手方", value: String(flow?.unique_counterparties ?? 0) },
        ]);
        return;
      }
      const chainSlug = asTrimmed(flow.chain) ?? cleanChain;
      const centerRef = `address:${chainSlug}:${asTrimmed(flow.address) ?? cleanAddress}`;
      const sourceLabel = `本地索引 · ${(response.data?.source?.tables ?? []).join("、") || "clickhouse:blockchain.token_transfers"} · ${
        asTrimmed(flow.algorithm_version) ?? "algorithm 未标注"
      } · as_of ${asTrimmed(response.data?.source?.as_of) ?? "—"}`;
      const existingNodeRefs = new Set(parsed.nodes.map((node) => node.ref));
      const existingEdgeRefs = new Set(parsed.edges.map((edge) => edge.ref));
      const addedNodeRefs: string[] = [];
      const addedEdgeRefs: string[] = [];
      const addNode = (ref: string) => {
        if (existingNodeRefs.has(ref)) return;
        existingNodeRefs.add(ref);
        addedNodeRefs.push(ref);
      };
      const addEdge = (ref: string) => {
        if (existingEdgeRefs.has(ref)) return;
        const parsedEdge = parseEdgeRef(ref);
        if (!parsedEdge) return;
        existingEdgeRefs.add(ref);
        addedEdgeRefs.push(ref);
        addNode(parsedEdge.fromRef);
        addNode(parsedEdge.toRef);
      };
      addNode(centerRef);
      const metaUpdates: Record<string, { first_seen?: string; last_seen?: string; source: string }> = {};
      const mergeMeta = (ref: string, first?: string, last?: string) => {
        const previous = metaUpdates[ref] ?? { source: sourceLabel };
        const candidates = [previous.first_seen, first].filter((value): value is string => Boolean(value));
        const lastCandidates = [previous.last_seen, last].filter((value): value is string => Boolean(value));
        metaUpdates[ref] = {
          source: sourceLabel,
          first_seen: candidates.length ? candidates.sort()[0] : undefined,
          last_seen: lastCandidates.length ? lastCandidates.sort()[lastCandidates.length - 1] : undefined,
        };
      };
      for (const edge of flowEdges) {
        const counterparty = asTrimmed(edge.counterparty);
        if (!counterparty) continue;
        const counterpartyRef = `address:${chainSlug}:${counterparty}`;
        const incoming = asTrimmed(edge.direction)?.toLowerCase() === "in";
        const fromRef = incoming ? counterpartyRef : centerRef;
        const toRef = incoming ? centerRef : counterpartyRef;
        const amountRaw = asTrimmed(edge.amount);
        const asset = asTrimmed(edge.asset);
        addEdge(encodeEdgeRef(fromRef, toRef, amountRaw, asset));
        const first = asTrimmed(edge.first_seen) ?? asTrimmed(edge.first);
        const last = asTrimmed(edge.last_seen) ?? asTrimmed(edge.last);
        mergeMeta(centerRef, first, last);
        mergeMeta(counterpartyRef, first, last);
      }
      const projectedNodes = new Set([...parsed.nodes.map((node) => node.ref), ...addedNodeRefs]).size;
      if (projectedNodes > NODE_LIMIT_HARD) {
        dialog.notify("warning", "已停止扩展", "本次加入会使节点数超过 2000 上限，本地画布未修改。", [
          { label: "当前节点", value: String(parsed.nodes.length) },
          { label: "预计节点", value: String(projectedNodes) },
          { label: "上限", value: String(NODE_LIMIT_HARD) },
        ]);
        return;
      }
      if (addedNodeRefs.length === 0 && addedEdgeRefs.length === 0) {
        dialog.notify("info", "本地索引资金流已在画布中", null, [
          { label: "链", value: chainSlug },
          { label: "地址", value: centerRef },
        ]);
        return;
      }
      setState((previous) => {
        if (!previous) return previous;
        const previousMeta = isRecord(previous.view_state.node_meta)
          ? { ...(previous.view_state.node_meta as Record<string, Record<string, unknown>>) }
          : {};
        const previousSources = isRecord(previous.view_state.node_sources)
          ? { ...(previous.view_state.node_sources as Record<string, string>) }
          : {};
        for (const [ref, meta] of Object.entries(metaUpdates)) {
          previousSources[ref] = meta.source;
          previousMeta[ref] = {
            ...(isRecord(previousMeta[ref]) ? previousMeta[ref] : {}),
            ...(meta.first_seen ? { first_seen: meta.first_seen } : {}),
            ...(meta.last_seen ? { last_seen: meta.last_seen } : {}),
          };
        }
        return {
          ...previous,
          node_refs: [...previous.node_refs, ...addedNodeRefs],
          edge_refs: [...previous.edge_refs, ...addedEdgeRefs],
          view_state: { ...previous.view_state, node_sources: previousSources, node_meta: previousMeta },
        };
      });
      setDirty(true);
      dialog.notify("success", "资金流已加入", null, [
        { label: "新增节点", value: String(addedNodeRefs.length) },
        { label: "新增边", value: String(addedEdgeRefs.length) },
        { label: "数据来源", value: sourceLabel },
      ]);
    } catch (error) {
      if (error instanceof ApiError) fail("本地资金流查询失败", error.userMessage, error.status);
      else fail("本地资金流查询失败", error instanceof Error ? error.message : "请稍后重试");
    } finally {
      setFlowBusy(false);
    }
  };

  const focusNode = (ref: string, key?: string) => {
    if (key) setSelection({ kind: "aggregate", key });
    else setSelection({ kind: "node", ref });
    setSvgOpen(true);
    const position = positions.get(key ?? ref);
    if (position) {
      setZoom(1);
      setPan({ x: SVG_W / 2 - position.x, y: SVG_H / 2 - position.y });
    }
    window.requestAnimationFrame(() => inspectorRef.current?.scrollIntoView({ block: "nearest" }));
  };

  const onCanvasPointerDown = (event: ReactPointerEvent<SVGSVGElement>) => {
    if (event.button !== 0) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    dragRef.current = { x: event.clientX, y: event.clientY, panX: pan.x, panY: pan.y, moved: false };
  };

  const onCanvasPointerMove = (event: ReactPointerEvent<SVGSVGElement>) => {
    const drag = dragRef.current;
    if (!drag) return;
    const dx = event.clientX - drag.x;
    const dy = event.clientY - drag.y;
    if (Math.abs(dx) + Math.abs(dy) > 4) drag.moved = true;
    if (drag.moved) setPan({ x: drag.panX + dx, y: drag.panY + dy });
  };

  const onCanvasPointerUp = () => {
    dragRef.current = null;
  };

  const resetView = () => {
    setZoom(1);
    setPan({ x: 0, y: 0 });
  };

  const edgeColor = (edge: RenderEdge): string => {
    if (lens === "evidence") return "#b45309";
    if (lens === "asset" && edge.asset) {
      let hash = 0;
      for (const char of edge.asset) hash = (hash * 31 + char.charCodeAt(0)) % 9973;
      return ASSET_PALETTE[hash % ASSET_PALETTE.length];
    }
    if (lens === "time") return "#475467";
    return "#175cd3";
  };

  const edgeLabel = (edge: RenderEdge): string => {
    if (edge.repeated > 1) return `${edge.repeated} 条边`;
    if (edge.amountRaw) return `${formatRawAmount(edge.amountRaw)}${edge.asset ? ` ${edge.asset}` : ""}`;
    return edge.asset ?? "";
  };

  const mutedNodes = lens === "evidence";
  const canvasBusy = saving || actionBusy || flowBusy || actionBusyHook;

  if (!canvasQuery.error && (canvasQuery.loading || (!state && Boolean(canvasQuery.data)))) {
    return (
      <>
        <BackLink to="/graph" label="返回图谱列表" />
        <SkeletonRows rows={6} columns={3} />
      </>
    );
  }

  if (!canvasId) {
    return (
      <>
        <BackLink to="/graph" label="返回图谱列表" />
        <Card>
          <EmptyState title="缺少画布 ID" />
        </Card>
      </>
    );
  }

  if (canvasQuery.error && !state) {
    return (
      <>
        <BackLink to="/graph" label="返回图谱列表" />
        <Card>
          <ErrorState title="画布加载失败" message={canvasQuery.error} onRetry={canvasQuery.reload} />
        </Card>
      </>
    );
  }

  return (
    <>
      <BackLink to="/graph" label="返回图谱列表" />
      <PageHeader
        title={canvasName}
        subtitle={`revision r${revision} · 案件 ${caseId ? shortValue(caseId, 8, 6) : "—"} · 来源 /api/v2/canvases/${canvasId}`}
        actions={
          <>
            {dirty ? <StatusBadge tone="warn">有未保存修改</StatusBadge> : <StatusBadge tone="ok">已同步</StatusBadge>}
            <button
              type="button"
              className="btn btn--primary btn--sm"
              onClick={() => void save()}
              disabled={!state || canvasBusy}
            >
              保存
            </button>
            <button type="button" className="btn btn--sm" onClick={() => void branch()} disabled={!state || canvasBusy}>
              分支
            </button>
            <button
              type="button"
              className="btn btn--sm"
              onClick={() => void openSnapshots()}
              disabled={!state || canvasBusy}
            >
              快照
            </button>
            <button type="button" className="btn btn--sm" onClick={() => void exportCanvas()} disabled={!state}>
              导出
            </button>
          </>
        }
      />

      <div className="s5-canvas-shell">
        <div className="s5-canvas-side s5-canvas-left">
          <Card title="Lens">
            <div className="s5-stack">
              {LENSES.map((item) => (
                <button
                  key={item.key}
                  type="button"
                  className={`btn btn--sm${lens === item.key ? " btn--primary" : ""}`}
                  aria-pressed={lens === item.key}
                  onClick={() => setLens(item.key)}
                >
                  {item.label}
                </button>
              ))}
              <span className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>
                节点 {totalNodes} · 边 {parsed.edges.length}
              </span>
              {renderMode === "normal" ? (
                <StatusBadge tone="ok">完整渲染</StatusBadge>
              ) : (
                <StatusBadge tone="warn">{renderMode === "limit" ? "聚合（停止扩展）" : "聚合渲染"}</StatusBadge>
              )}
              {selectedAddressNode ? (
                <button
                  type="button"
                  className="btn btn--sm"
                  disabled={flowBusy || renderMode === "limit"}
                  onClick={() => void addAddressFlow(selectedAddressNode.chain, selectedAddressNode.id)}
                >
                  加入选中地址资金流
                </button>
              ) : null}
            </div>
          </Card>
        </div>

        <div className="s5-canvas-side">
          <Card title="图谱" flush>
            <div className="toolbar" style={{ padding: "var(--sp-3) var(--sp-4)" }}>
              <input
                className="input"
                style={{ flex: "1 1 180px", minWidth: 140, maxWidth: 260 }}
                aria-label="搜索节点"
                placeholder="搜索节点 ref / 地址"
                value={search}
                onChange={(event) => setSearch(event.target.value)}
              />
              <button type="button" className="btn btn--sm" onClick={() => setSearch("")} disabled={!search}>
                清除
              </button>
              <button
                type="button"
                className="btn btn--sm"
                title="放大"
                onClick={() => setZoom((value) => Math.min(3, value * 1.25))}
              >
                +
              </button>
              <button
                type="button"
                className="btn btn--sm"
                title="缩小"
                onClick={() => setZoom((value) => Math.max(0.3, value / 1.25))}
              >
                -
              </button>
              <button type="button" className="btn btn--sm" title="重置缩放与平移" onClick={resetView}>
                重置
              </button>
              <span className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>
                {Math.round(zoom * 100)}%
              </span>
              {isMobile ? (
                <button type="button" className="btn btn--sm" onClick={() => setSvgOpen((value) => !value)}>
                  {svgOpen ? "收起图谱" : "展开图谱"}
                </button>
              ) : null}
              <button
                type="button"
                className="btn btn--sm"
                onClick={() => void addAddressFlow()}
                disabled={!state || flowBusy || renderMode === "limit"}
              >
                加入地址资金流
              </button>
            </div>

            {renderNodes.length === 0 ? (
              <EmptyState
                title={search || lens === "evidence" ? "当前筛选没有可渲染节点" : "画布还没有可渲染节点"}
                action={
                  <button
                    type="button"
                    className="btn btn--sm"
                    onClick={() => void addAddressFlow()}
                    disabled={!state || flowBusy || renderMode === "limit"}
                  >
                    加入地址资金流
                  </button>
                }
              />
            ) : !isMobile || svgOpen ? (
              <div style={{ padding: "0 var(--sp-2) var(--sp-2)" }}>
                <svg
                  className="s5-canvas-svg"
                  viewBox={`0 0 ${SVG_W} ${SVG_H}`}
                  preserveAspectRatio="xMidYMid meet"
                  role="img"
                  aria-label="Canvas SVG 图谱"
                  onPointerDown={onCanvasPointerDown}
                  onPointerMove={onCanvasPointerMove}
                  onPointerUp={onCanvasPointerUp}
                  onPointerCancel={onCanvasPointerUp}
                >
                  <defs>
                    <marker id="canvas-arrow" viewBox="0 0 10 10" refX="18" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
                      <path d="M 0 0 L 10 5 L 0 10 z" fill="#667085" />
                    </marker>
                  </defs>
                  <rect x="0" y="0" width={SVG_W} height={SVG_H} fill="transparent" />
                  <g transform={`translate(${pan.x} ${pan.y}) scale(${zoom})`}>
                    {renderEdges.map((edge) => {
                      const from = positions.get(edge.fromKey);
                      const to = positions.get(edge.toKey);
                      if (!from || !to) return null;
                      const label = edgeLabel(edge);
                      return (
                        <g key={edge.key}>
                          <line
                            x1={from.x}
                            y1={from.y}
                            x2={to.x}
                            y2={to.y}
                            stroke={edgeColor(edge)}
                            strokeWidth={edge.repeated > 1 ? Math.min(5, 1.5 + edge.repeated / 4) : 1.6}
                            strokeOpacity={0.82}
                            strokeDasharray={lens === "evidence" ? "7 5" : undefined}
                            markerEnd="url(#canvas-arrow)"
                          />
                          {label ? (
                            <text
                              x={(from.x + to.x) / 2}
                              y={(from.y + to.y) / 2 - 5}
                              textAnchor="middle"
                              fontSize={10}
                              fill="var(--c-slate)"
                              style={{ pointerEvents: "none" }}
                            >
                              {label}
                            </text>
                          ) : null}
                        </g>
                      );
                    })}
                    {renderNodes.map((node) => {
                      const position = positions.get(node.key);
                      if (!position) return null;
                      const selected =
                        selection?.kind === "node"
                          ? selection.ref === node.ref
                          : selection?.kind === "aggregate" && selection.key === node.key;
                      const fill = node.aggregate
                        ? "var(--c-surface-sunken)"
                        : node.kind === "address"
                          ? "var(--c-cobalt-soft)"
                          : node.kind === "evidence"
                            ? "var(--c-amber-soft)"
                            : "var(--c-neutral-soft)";
                      const stroke = node.aggregate
                        ? "var(--c-neutral)"
                        : node.kind === "address"
                          ? "var(--c-cobalt)"
                          : node.kind === "evidence"
                            ? "var(--c-amber-text)"
                            : "var(--c-neutral)";
                      const radius = node.aggregate ? Math.min(34, 22 + node.count / 40) : node.kind === "address" ? 22 : 18;
                      return (
                        <g
                          key={node.key}
                          className={`s5-canvas-node${selected ? " is-selected" : ""}`}
                          transform={`translate(${position.x} ${position.y})`}
                          role="button"
                          tabIndex={0}
                          aria-label={`节点 ${node.label}`}
                          data-node-ref={node.ref ?? node.key}
                          style={mutedNodes && node.kind !== "evidence" ? { opacity: 0.45 } : undefined}
                          onPointerDown={(event) => event.stopPropagation()}
                          onClick={(event) => {
                            event.stopPropagation();
                            if (node.ref) setSelection({ kind: "node", ref: node.ref });
                            else setSelection({ kind: "aggregate", key: node.key });
                          }}
                          onKeyDown={(event) => {
                            if (event.key !== "Enter" && event.key !== " ") return;
                            event.preventDefault();
                            if (node.ref) setSelection({ kind: "node", ref: node.ref });
                            else setSelection({ kind: "aggregate", key: node.key });
                          }}
                        >
                          {node.kind === "evidence" && !node.aggregate ? (
                            <polygon
                              points="0,-20 20,0 0,20 -20,0"
                              fill={fill}
                              stroke={stroke}
                              strokeWidth={selected ? 3 : 1.5}
                            />
                          ) : node.kind === "tx" && !node.aggregate ? (
                            <rect
                              x="-26"
                              y="-16"
                              width="52"
                              height="32"
                              rx="7"
                              fill={fill}
                              stroke={stroke}
                              strokeWidth={selected ? 3 : 1.5}
                            />
                          ) : (
                            <circle r={radius} fill={fill} stroke={stroke} strokeWidth={selected ? 3 : 1.5} />
                          )}
                          <text textAnchor="middle" y={4} fontSize={10} fill="var(--c-graphite)">
                            {node.label}
                          </text>
                          {node.sublabel ? (
                            <text textAnchor="middle" y={node.kind === "tx" && !node.aggregate ? 30 : 36} fontSize={9} fill="var(--c-slate)">
                              {node.sublabel}
                            </text>
                          ) : null}
                          {node.aggregate ? (
                            <text textAnchor="middle" y={radius + 14} fontSize={10} fill="var(--c-cobalt)" fontWeight={600}>
                              ×{node.count}
                            </text>
                          ) : null}
                        </g>
                      );
                    })}
                  </g>
                </svg>
              </div>
            ) : null}
          </Card>
        </div>

        <div
          className="s5-canvas-side s5-stack"
          ref={inspectorRef}
          style={{ display: "grid", gap: "var(--sp-3)", alignContent: "start" }}
        >
          <EvidenceRail title="Inspector · 对象属性" items={inspectorItems} />
          <EvidenceRail title="Evidence Rail" items={evidenceItems} />
        </div>
      </div>

      {isMobile ? (
        <Card title="列表视图" flush>
          <div className="s5-node-list">
            {parsed.nodes.map((node) => (
              <button
                key={node.ref}
                type="button"
                className="s5-node-list__item"
                onClick={() => focusNode(node.ref)}
              >
                <span className="s5-object-head">
                  <strong>{nodeRefLabel(node)}</strong>
                  <StatusBadge tone={node.kind === "evidence" ? "warn" : node.kind === "address" ? "info" : "neutral"}>
                    {NODE_KIND_LABEL[node.kind]}
                  </StatusBadge>
                  {node.chain ? <span className="mono">{node.chain}</span> : null}
                </span>
                <span className="s5-mono" style={{ display: "block", fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>
                  {node.ref}
                </span>
              </button>
            ))}
            {parsed.edges.map((edge) => (
              <button
                key={edge.ref}
                type="button"
                className="s5-node-list__item"
                onClick={() => focusNode(edge.fromRef)}
              >
                <span className="s5-object-head">
                  <strong>
                    {shortValue(edge.fromRef, 12, 6)} → {shortValue(edge.toRef, 12, 6)}
                  </strong>
                </span>
                <span className="s5-mono" style={{ display: "block", fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>
                  {edge.amountRaw ? `${formatRawAmount(edge.amountRaw)}${edge.asset ? ` ${edge.asset}` : ""}` : edge.asset ?? "edge"}
                </span>
              </button>
            ))}
            {evidenceIds.map((id) => {
              const entry = evidenceMap[id];
              return (
                <button
                  key={`evidence-${id}`}
                  type="button"
                  className="s5-node-list__item"
                  onClick={() => focusNode(`evidence:${id}`)}
                >
                  <span className="s5-object-head">
                    <strong>证据 {shortValue(id, 8, 4)}</strong>
                    {entry?.phase === "ok" ? (
                      <StatusBadge tone={statusToneFor(entry.data.status ?? "")}>{entry.data.status ?? "—"}</StatusBadge>
                    ) : (
                      <StatusBadge tone="neutral">{entry?.phase === "unavailable" ? "不可用" : "读取中"}</StatusBadge>
                    )}
                  </span>
                  {entry?.phase === "ok" ? <span>{entry.data.title ?? "—"}</span> : null}
                </button>
              );
            })}
            {parsed.nodes.length === 0 && parsed.edges.length === 0 && evidenceIds.length === 0 ? (
              <EmptyState title="画布还没有节点或边" />
            ) : null}
          </div>
        </Card>
      ) : null}
    </>
  );
}
