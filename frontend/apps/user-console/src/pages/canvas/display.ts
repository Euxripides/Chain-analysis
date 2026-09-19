/** 展示层的文本格式化（标签、日期、省略、diff 渲染）。 */

import { shortValue } from "../../sprint5/common";
import { asTrimmed, isRecord } from "./refs";
import type { ParsedNode, RenderNode } from "./types";

export function nodeRefLabel(node: ParsedNode | RenderNode): string {
  if ("kind" in node && node.kind === "evidence") {
    const id = "id" in node ? node.id : node.label;
    return `证据 ${shortValue(id, 6, 4)}`;
  }
  const id = "id" in node ? node.id : node.label;
  return shortValue(id, 8, 4);
}

export function shortDate(value?: string | null): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value.slice(0, 10);
  return date.toISOString().slice(0, 10);
}

export function readDiff(value: unknown): Array<{ field: string; submitted: unknown; current: unknown }> {
  if (!Array.isArray(value)) return [];
  return value
    .filter(isRecord)
    .map((entry) => ({
      field: asTrimmed(entry.field) ?? "unknown",
      submitted: entry.submitted,
      current: entry.current,
    }));
}

export function diffText(value: unknown): string {
  if (typeof value === "string") return value.length > 240 ? `${value.slice(0, 240)}…` : value;
  try {
    const text = JSON.stringify(value);
    return text.length > 240 ? `${text.slice(0, 240)}…` : text;
  } catch {
    return String(value);
  }
}

export function readStringArray(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : [];
}

/** 长标签的视觉省略：完整值始终保留在 <title> 与 Inspector（蓝图 §2.5）。 */
export function ellipsize(value: string, max = 22): string {
  if (value.length <= max) return value;
  return `${value.slice(0, max - 1)}…`;
}
