/**
 * 新建下载的类型、常量与纯函数（用户端重构规范 V4 §45.1）
 *
 * 从 1305 行的 DownloadNewPage.tsx **原样抽出**。这一层里没有任何 JSX、没有组件状态：
 * 接口直接对应 /api/v2/downloads/{preview,validate-addresses} 的响应，
 * 纯函数只依赖入参（groupAddressesByChain 的注释解释了为什么必须是纯的）。
 *
 * 搬运本身不改变任何字段名与口径；STEP_ITEMS 仍是五个业务步骤顺序的单一来源。
 */
import { formatTime } from "../../sprint5/common";
import type { ImportedAddress } from "../../import/parse-core";

export interface ChainMeta {
  slug: string;
  name: string;
  type: string;
  enabled: boolean;
  supports_trace: boolean;
  /**
   * Explorer 读能力：full = 有链上 RPC 读路径；local_index = 只有本地索引（Solana）。
   * 已不再用于"隐藏整条链"：Solana 的活动/对手方/资金流都读本地索引，数据来源会在
   * 地址详情的口径弹窗里如实标注（source.provider = local_index）。
   */
  explorer_mode?: "full" | "local_index";
}

export interface DatasetMeta {
  id: string;
  label: string;
  status: string;
}

export type RangeType = "full" | "block" | "time";
export type ReusePolicy = "prefer_local" | "refresh";
export type PerformanceMode = "safe" | "fast" | "turbo";

export interface RangeBody {
  type: RangeType;
  from_block?: number;
  to_block?: number;
  from?: string;
  to?: string;
}

export interface DownloadBody {
  chain: string;
  addresses: string[];
  datasets: string[];
  range: RangeBody;
  reuse_policy: ReusePolicy;
  performance_mode: PerformanceMode;
  /** §23 per-address 案件/标签覆盖；键为服务端 normalized 地址。 */
  subject_meta?: Record<string, { case_name: string; tags: string[] }>;
}

export interface PreviewCoverageItem {
  subject: string;
  dataset: string;
  covered_blocks: number;
  total_blocks: number;
  ratio: number;
  gaps: Array<{ from_block: number; to_block: number }>;
}

export interface PreviewCandidate {
  job_id: string;
  job_no: string;
  status: string;
  coverage: number;
  updated_at: string;
}

export interface PreviewReuse {
  policy: string;
  will_reuse: boolean;
  candidate: PreviewCandidate | null;
  coverage_hit: boolean;
  refresh: boolean;
}

export interface DownloadEstimate {
  estimated_missing_blocks: number | null;
  estimated_rows: number | null;
  estimated_cost_usd: string | null;
  estimated_time_seconds: number | null;
  basis: string;
}

export interface DownloadPreviewData {
  request: {
    chain: string;
    addresses: string[];
    datasets: string[];
    range: RangeBody;
    reuse_policy: ReusePolicy;
    performance_mode: PerformanceMode;
  };
  coverage: { chain: string; datasets: string[]; items: PreviewCoverageItem[]; overall_ratio: number };
  missing_blocks: number;
  provider_work_required: boolean;
  reuse: PreviewReuse;
  estimate: DownloadEstimate;
  risks: string[];
  source: Record<string, unknown>;
}

export interface DownloadCreateData {
  status: "ready" | "reused" | "planned";
  created: boolean;
  reused: boolean;
  provider_work_required: boolean;
  coverage: number;
  job_id: string | null;
  job_no?: string;
}

export function parseAddressInput(raw: string) {
  const tokens = raw
    .split(/[\s,;，；|]+/)
    .map((value) => value.trim())
    .filter(Boolean);
  const seen = new Set<string>();
  const list: string[] = [];
  let duplicates = 0;
  for (const token of tokens) {
    const localKey = token.toLowerCase();
    if (seen.has(localKey)) {
      duplicates += 1;
      continue;
    }
    seen.add(localKey);
    list.push(token);
  }
  return { tokens: tokens.length, list, duplicates };
}

/* ---- §18.10 导入：CSV/XLSX 解析（浏览器） + 服务端再次验证 ---- */

export interface ServerAddressVerdict {
  index: number;
  row: number;
  input: string;
  chain: string;
  code: string;
  message: string;
}

export interface ServerFieldRejection {
  row: number;
  index: number;
  field: string;
  code: string;
  count: number;
  limit: number;
  message: string;
}

export interface ServerMetaVerdict {
  name: string;
  kind: string;
  status: "resolved" | "missing" | "will_create" | "denied";
  case_id?: string | null;
  tag_id?: string | null;
  required_permission?: string;
}

export interface ServerValidateResult {
  accepted: Array<{ index: number; row: number; input: string; chain: string; normalized: string; case_name: string; tags: string[] }>;
  rejected: ServerAddressVerdict[];
  field_rejections: ServerFieldRejection[];
  scope: { organization_id: string; workspace_id: string };
  grants: { "download:create": boolean; "case:create": boolean; "case:update": boolean; create_missing_meta_requested: boolean };
  summary: {
    submitted: number; accepted: number; rejected: number; blank: number; invalid: number;
    duplicate: number; cap_rejected: number; tags_over_limit: number; field_rejections: number;
    meta_denied: number; max_addresses: number; max_tags: number;
    chains: Record<string, number>; distinct_chains: number;
  };
  meta: { cases: ServerMetaVerdict[]; tags: ServerMetaVerdict[] };
  source: string;
}
/**
 * 按链分组：契约 1043 要求"每个地址可单独选择链"，而服务端一次请求只接受同一条链。
 *
 * **纯函数**：只依赖入参。若改成读组件 state，首次"预览"触发导入后 setState 尚未生效，
 * 就会用旧集合提交（服务端过滤/normalize 之后更是如此）。
 */
export function groupAddressesByChain(rows: ImportedAddress[], defaultChain: string): Array<{ chain: string; items: ImportedAddress[] }> {
  const groups = new Map<string, ImportedAddress[]>();
  for (const item of rows) {
    const key = item.chain || defaultChain;
    if (!key) continue;
    const list = groups.get(key) ?? [];
    list.push(item);
    groups.set(key, list);
  }
  return [...groups.entries()].map(([chain, items]) => ({ chain, items }));
}

/**
 * 以"预览指纹 + 链 + 地址集合"派生稳定幂等键：同一批地址重试时复用同一个 key，
 * 避免第一次部分成功后重试又创建一遍。
 */
export async function stableIdempotencyKey(scope: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(scope));
  const hex = [...new Uint8Array(digest)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-5${hex.slice(13, 16)}-a${hex.slice(17, 20)}-${hex.slice(20, 32)}`;
}


export function toIso(localValue: string): string {
  if (!localValue) return "";
  const date = new Date(localValue);
  if (Number.isNaN(date.getTime())) return "";
  return date.toISOString();
}

export function percent(value: number | null | undefined, digits = 1): string {
  if (value === null || value === undefined || Number.isNaN(value)) return "—";
  return `${(value * 100).toFixed(digits)}%`;
}

export function rangeText(body: { range: RangeBody }): string {
  const range = body.range;
  if (range.type === "full") return "全量范围";
  if (range.type === "block") return `区块 ${range.from_block ?? "—"} → ${range.to_block ?? "—"}`;
  return `时间 ${range.from ? formatTime(range.from) : "—"} → ${range.to ? formatTime(range.to) : "—"}`;
}

/** 五个业务步骤：顺序即真实填写顺序（蓝图 §12.17），跳转只允许回到已完成步骤。 */
export const STEP_ITEMS = [
  { key: "objects", label: "① 对象与地址" },
  { key: "range", label: "② 链与范围" },
  { key: "datasets", label: "③ Dataset" },
  { key: "reuse", label: "④ 复用策略" },
  { key: "preview", label: "⑤ 预览与创建" },
];

