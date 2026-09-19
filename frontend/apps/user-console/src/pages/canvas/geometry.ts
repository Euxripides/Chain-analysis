/**
 * 画布几何：客户端坐标 → viewBox 坐标，以及三种布局的坐标计算。
 *
 * 与渲染技术无关——换任何渲染器（内联 SVG 或 WebGL）都要用同一套坐标规则，
 * 所以它单独成模块，而不是埋在页面里（§61）。
 */

import { SVG_H, SVG_W } from "./constants";
import type { LayoutMode, RenderEdge, RenderNode } from "./types";

export function clientToViewBox(svg: SVGSVGElement, clientX: number, clientY: number): { x: number; y: number } {
  const rect = svg.getBoundingClientRect();
  const scale = Math.min(rect.width / SVG_W, rect.height / SVG_H) || 1;
  const offsetX = (rect.width - SVG_W * scale) / 2;
  const offsetY = (rect.height - SVG_H * scale) / 2;
  return { x: (clientX - rect.left - offsetX) / scale, y: (clientY - rect.top - offsetY) / scale };
}

export function layoutPositions(
  nodes: RenderNode[],
  edges: RenderEdge[],
  mode: LayoutMode = "auto",
): Map<string, { x: number; y: number }> {
  const raw = new Map<string, { x: number; y: number }>();
  if (nodes.length === 0) return raw;
  const useRing = mode === "ring" || (mode === "auto" && nodes.length > 80);
  if (useRing) {
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
