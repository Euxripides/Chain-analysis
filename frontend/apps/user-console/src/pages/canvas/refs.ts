/**
 * node_ref / edge_ref 字符串协议的解析与规范化。
 *
 * 这是**契约代码**：后端的 ref 格式没有变，所以这里的解析规则逐字照搬，
 * 只是搬出了页面组件，让它可以被单独测试（§61：图形逻辑移出 CanvasPage）。
 *
 * 坏引用不会被静默丢弃——解析失败的 ref 会进 badNodeRefs / badEdgeRefs，
 * 由页面用弹窗提示（仓库规范：提示只能走弹窗）。
 */

import { UUID_RE } from "./constants";
import type { CanvasRecord, CanvasState, ParsedCanvas, ParsedEdge, ParsedNode } from "./types";

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function asTrimmed(value: unknown): string | undefined {
  if (typeof value !== "string") return undefined;
  const text = value.trim();
  return text === "" ? undefined : text;
}

export function parseNodeRef(raw: string): ParsedNode | null {
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

export function encodeEdgeRef(fromRef: string, toRef: string, amountRaw?: string, asset?: string): string {
  let suffix = "";
  if (amountRaw && asset) suffix = `:${amountRaw}:${asset}`;
  else if (amountRaw) suffix = `:${amountRaw}`;
  else if (asset) suffix = `::${asset}`;
  return `edge:${fromRef}->${toRef}${suffix}`;
}

export function parseEdgeRef(raw: string): ParsedEdge | null {
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

export function parseCanvasRefs(nodeRefs: string[], edgeRefs: string[]): ParsedCanvas {
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

export function normalizeCanvasState(source: Partial<CanvasRecord>): CanvasState {
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

export function collectEvidenceIds(state: CanvasState | null): string[] {
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
