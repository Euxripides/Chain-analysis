/**
 * 新建下载的类型、常量与纯函数（《新建数据下载 Flowline 界面重构实施规范》§5 / §19.1）
 *
 * 这一层没有任何 JSX、没有组件状态：接口直接对应
 * /api/v2/downloads/{preview,validate-addresses} 的响应，纯函数只依赖入参
 * （groupAddressesByChain 的注释解释了为什么必须是纯的）。
 *
 * 两条业务决策在这一层被固定下来：
 *   1. 复用策略没有用户入口，唯一取值是 DEFAULT_REUSE_POLICY（= prefer_local），
 *      但协议字段 reuse_policy 仍然原样保留在 DownloadBody / request / reuse 上；
 *   2. 可见流程是五步（对象与地址 / 链与范围 / Dataset / Coverage 预览 / 创建任务），
 *      STEP_ITEMS 是这五步的唯一真源，序号由轨道节点渲染，标签里不带 ①②③。
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
  /**
   * available = 该链上有可用来源，可勾选；unsupported = 该链上没有来源，整格禁用。
   * 服务端未返回时按不可选处理（fail-closed），与重构前 `status === "available"` 的判定一致。
   */
  status?: "available" | "unsupported";
  /** 该 Dataset 在所选链上的实际来源与 API 通道（unsupported 时服务端不返回）。 */
  provider?: string;
  api_channel?: string;
  /** 范围支持：全量 / 时间范围 / 区块范围（Solana 下区块即 Slot）。 */
  supports_full?: boolean;
  supports_time_range?: boolean;
  supports_block_range?: boolean;
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
  /**
   * 总 CU 估算。规范把 `estimated_cu` 列为预览响应字段，实现侧可能放在 estimate 里，
   * 两处都读（previewEstimatedCu）；推不出来时服务端返回 null，前端不补 0。
   */
  estimated_cu?: number | string | null;
  basis: string;
}

/**
 * 预览响应里的 Provider 计划（开发文档 §6 / §15.2）：**一行 = 一个 Provider × 一个 API 通道**。
 *
 * `estimated_pages` 与 `estimated_rpc_calls` 是两个不同口径，不能合并：
 * 一个 Data API 页最多 100 条交易，它既不是 100 次请求，也不是 RPC 方法调用数。
 */
export interface ProviderPlanItem {
  provider: string;
  api_channel: string;
  /** Data API 分页页数（1 页 = 1 次 HTTP 请求，页内最多 100 条交易）。 */
  estimated_pages?: number | string | null;
  /** JSON-RPC 详情调用数（Batch=1 时 1 次请求 = 1 个方法调用）。 */
  estimated_rpc_calls?: number | string | null;
  estimated_cu?: number | string | null;
  estimated_cost_usd?: string | null;
}

/**
 * 任务详情里每个 Provider/通道的运行时档位（开发文档 §6.2 / §10.2）。
 *
 * 兼容字段：`used_requests` 的单位随通道变化——Data API 是页数，JSON-RPC 是方法调用数，
 * 因此展示时按 api_channel 给不同字段标签，不把一页 100 条交易显示成 100 次请求。
 */
export interface ProviderRuntimeState {
  provider: string;
  api_channel: string;
  operation_class: string;
  requested_mode: string;
  effective_mode: string;
  shard_concurrency?: number | null;
  request_concurrency?: number | null;
  requests_per_minute?: number | null;
  cu_per_minute?: number | null;
  used_requests?: number | null;
  used_cu?: number | null;
  estimated_cost_usd?: string | null;
  /** 降档原因码（例如 cu_cap_unknown_tier / management_stale）。 */
  reason?: string | null;
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
  /** Provider 计划（规范 §6）：旧后端不返回时为 undefined，界面据此隐藏该区块。 */
  provider_plan?: ProviderPlanItem[];
  /** 总 CU 估算（与 estimate.estimated_cu 同义，见 previewEstimatedCu）。 */
  estimated_cu?: number | string | null;
  /** 总成本估算（与 estimate.estimated_cost_usd 同义，见 previewEstimatedCost）。 */
  estimated_cost_usd?: string | null;
  /**
   * 服务端在**预览阶段**解析出的目标模式。低于 request.performance_mode 即表示被钳制；
   * 后端未返回时为 undefined，创建确认弹窗因此不显示"预计实际模式"。
   */
  effective_mode?: string;
  /** 钳制原因码。mode_reason 与本仓库任务字段同名，effective_reason 与开发文档同名，两者都接受。 */
  mode_reason?: string;
  effective_reason?: string;
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

/* ---- 范围能力（开发文档 §5.2）：任务级范围类型按所选 Dataset 的能力开放 ---- */

export interface RangeCapabilities {
  full: boolean;
  time: boolean;
  block: boolean;
}

/**
 * 任务级范围能力 = 参与本次下载的 Dataset 的**交集**。
 *
 * 为什么取交集：范围是任务级字段，只要有一个 Dataset 不支持该范围，整单就不能用它。
 * 两类情况按"不施加约束"处理（返回 null），以保持既有行为：
 *   - balances 是余额快照，本身没有范围概念（服务端在下载层特殊处理），不参与交集；
 *   - 所选 Dataset 一个都没有时不做事前限制，避免"还没选就全禁用"。
 * 服务端未返回能力字段（旧后端）时按"未知不等于不支持"处理，只有显式 false 才禁用。
 */
export function datasetRangeCapabilities(items: DatasetMeta[], selected: string[]): RangeCapabilities | null {
  const picked = items.filter((item) => selected.includes(item.id));
  const pool = (picked.length > 0 ? picked : items.filter((item) => item.status === "available")).filter(
    (item) => item.id !== "balances",
  );
  if (pool.length === 0) return null;
  const supports = (value: boolean | undefined) => value !== false;
  return {
    full: pool.every((item) => supports(item.supports_full)),
    time: pool.every((item) => supports(item.supports_time_range)),
    block: pool.every((item) => supports(item.supports_block_range)),
  };
}

/* ---- Provider 计划与性能模式钳制（开发文档 §13.1 / §15.2） ---- */

/** 总 CU 估算：规范列为预览顶层字段，实现侧也可能放在 estimate 里，两处都读。 */
export function previewEstimatedCu(data: DownloadPreviewData | null | undefined): number | string | null {
  if (!data) return null;
  const value = data.estimated_cu ?? data.estimate.estimated_cu;
  return value === undefined || value === null || value === "" ? null : value;
}

/** 总成本估算（USD）：与 previewEstimatedCu 同理，顶层与 estimate 两处都读。 */
export function previewEstimatedCost(data: DownloadPreviewData | null | undefined): string | null {
  if (!data) return null;
  const value = data.estimated_cost_usd ?? data.estimate.estimated_cost_usd;
  return typeof value === "string" && value.trim() !== "" ? value : null;
}

/** 性能模式档位：safe < fast < turbo。未知模式返回 -1，绝不假定它"更低"。 */
export function performanceModeRank(mode: string | null | undefined): number {
  const value = String(mode ?? "").trim().toLowerCase();
  if (value === "safe") return 0;
  if (value === "fast") return 1;
  if (value === "turbo") return 2;
  return -1;
}

/**
 * 低档位原因码 → 中文可读文案（开发文档 §6.2 / §6.4）。
 *
 * 只映射服务端真实会给出的原因码；**不认识的原因码原样返回**，不编造解释。
 */
const MODE_REASON_TEXT: Record<string, string> = {
  cu_cap_unknown_tier: "账户层级未知，按 CU 上限钳制",
  cu_cap_free_tier: "免费层级，按 CU 上限钳制",
  cu_cap: "按 CU 上限钳制",
  management_stale: "Provider 管理数据超过 15 分钟未同步，TURBO 暂不可用",
  management_safe_only: "Provider 管理数据超过 60 分钟未同步，最多 SAFE",
  quota_warn: "当日 CU 用量达到告警线，最多 FAST",
  quota_safe_only: "当日 CU 用量达到 95%，最多 SAFE",
  quota_stop: "当日 CU 用量已达上限，暂停新的 Provider 请求",
  quota_exhausted: "Provider 额度已用尽，暂停新的 Provider 请求",
  rate_limited: "Provider 返回 429，当前通道并发被减半",
  error_rate: "连续错误率超过阈值，自动降档",
  machine_backpressure: "本机背压（内存 / Spool / ClickHouse）触发降档",
  machine_backpressure_allowed_mode: "本机背压（内存 / Spool / ClickHouse）触发降档",
};

export function modeReasonText(code: string | null | undefined): string {
  const key = String(code ?? "").trim();
  if (key === "") return "";
  return MODE_REASON_TEXT[key] ?? key;
}

export interface PreviewModeClamp {
  requestedMode: string;
  /** 服务端给出的目标模式；未返回时为空串（界面按 — 显示）。 */
  effectiveMode: string;
  /** 原始原因码；没有时为空串。 */
  reasonCode: string;
  /** 中文可读原因；没有时为空串。 */
  reasonText: string;
}

/**
 * 预览里的模式钳制（开发文档 §6.4）。
 *
 * 两个触发条件（任一成立即返回，创建确认弹窗据此显示"请求模式 / 预计实际模式 / 原因"）：
 *   1. 服务端返回的 effective_mode 低于请求模式；
 *   2. 服务端给出了钳制原因码（此时即使没回 effective_mode，也说明已被降档）。
 * 两者都没有时返回 null —— 不显示任何"预计实际模式"，避免前端自己猜档位。
 */
export function previewModeClamp(
  data: DownloadPreviewData | null | undefined,
  requested: PerformanceMode,
): PreviewModeClamp | null {
  if (!data) return null;
  const requestedMode = String(data.request?.performance_mode ?? requested ?? "").trim() || requested;
  const effectiveMode = typeof data.effective_mode === "string" ? data.effective_mode.trim() : "";
  const rawReason = [data.mode_reason, data.effective_reason].find(
    (value): value is string => typeof value === "string" && value.trim() !== "",
  );
  const reasonCode = rawReason ? rawReason.trim() : "";
  if (effectiveMode === "" && reasonCode === "") return null;
  const lowered =
    effectiveMode !== "" && performanceModeRank(effectiveMode) >= 0 &&
    performanceModeRank(effectiveMode) < performanceModeRank(requestedMode);
  if (!lowered && reasonCode === "") return null;
  return {
    requestedMode,
    effectiveMode,
    reasonCode,
    reasonText: reasonCode === "" ? "" : modeReasonText(reasonCode),
  };
}

/** EVM 地址：0x + 40 位十六进制。只有这种形状才做大小写归一。 */
const EVM_ADDRESS = /^0x[0-9a-fA-F]{40}$/;

/**
 * 去重键。
 *
 * **Solana / TRON 是 Base58，大小写敏感**（`AbC` 与 `abc` 是两个不同地址），
 * 因此绝不能对整串 toLowerCase —— 那会把两个真实地址当重复丢掉（开发文档 §2.4）。
 * 只有 EVM 的 0x+40hex 才做大小写归一（EIP-55 校验和只是展示形式，链上地址不区分大小写）。
 * 其余一律原样保留，最终以服务端 /validate-addresses 的判定为准。
 */
function addressDedupeKey(token: string): string {
  return EVM_ADDRESS.test(token) ? token.toLowerCase() : token;
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
    const localKey = addressDedupeKey(token);
    if (seen.has(localKey)) {
      duplicates += 1;
      continue;
    }
    seen.add(localKey);
    /* 提交原文（只 trim）：Solana 地址的大小写是地址本身的一部分。 */
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

/**
 * 复用策略**固定为 prefer_local**（Flowline 规范 §5.2）。
 *
 * 规范把「复用策略」从页面彻底移除：用户不决定缓存复用，也不能切到 refresh。
 * 但服务端契约不变 —— 预览与创建负载里仍然显式带 `reuse_policy: "prefer_local"`，
 * 与重构前的默认行为逐字一致。**不存在**任何 UI、URL 参数或"高级设置"入口：
 * 页面正文、弹窗、DOM 与可访问名称里都没有对应控件（§5.5 禁止 CSS 假隐藏）。
 */
export const DEFAULT_REUSE_POLICY: ReusePolicy = "prefer_local";

export type DownloadStepKey = "objects" | "range" | "datasets" | "preview" | "create";

export type DownloadStepState = "done" | "current" | "todo";

export interface DownloadStepItem {
  key: DownloadStepKey;
  label: string;
  /** 窄屏（<640px）轨道标签：只保留业务名词，不省略关键信息。 */
  shortLabel: string;
}

/**
 * 五个可见步骤：顺序即真实填写顺序（Flowline 规范 §6.1）。
 *
 * 原「④ 复用策略」整步删除（策略不可见，见 DEFAULT_REUSE_POLICY），
 * 「创建任务」成为独立的第 ⑤ 步 —— 它对应右侧任务预览与创建入口
 * （id="download-step-create"），不是一个额外的表单区。
 */
export const STEP_ITEMS = [
  { key: "objects", label: "对象与地址", shortLabel: "对象" },
  { key: "range", label: "链与范围", shortLabel: "范围" },
  { key: "datasets", label: "Dataset", shortLabel: "Dataset" },
  { key: "preview", label: "Coverage 预览", shortLabel: "预览" },
  { key: "create", label: "创建任务", shortLabel: "创建" },
] satisfies ReadonlyArray<DownloadStepItem>;

export function downloadStepIndex(key: string): number {
  return STEP_ITEMS.findIndex((item) => item.key === key);
}

/** 步骤状态：完成 / 当前 / 未到达。轨道节点与移动端折叠卡共用同一判定。 */
export function downloadStepState(key: string, current: string): DownloadStepState {
  const index = downloadStepIndex(key);
  const currentIndex = downloadStepIndex(current);
  if (index < 0 || currentIndex < 0) return "todo";
  if (index < currentIndex) return "done";
  if (index === currentIndex) return "current";
  return "todo";
}

