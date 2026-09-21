// dRPC 额度 / 性能档位 / 密钥的后台共享类型与展示辅助（工作流 I）。
//
// 后端契约（只增不改，见 docs/implementation/solana-drpc-integration.md §5 与开发文档 §11/§14）：
//   GET   /api/admin/v1/providers/{key}                          详情新增 runtime_health.channels、quota、
//                                                                performance_profiles[]、secrets[].secret_kind
//   GET   /api/admin/v1/providers/{key}/quota                    （admin:cost:read）
//   GET   /api/admin/v1/providers/{key}/quota-history?range=day  （admin:cost:read）
//   GET   /api/admin/v1/providers/{key}/performance-profiles     （admin:provider:read）
//   PATCH /api/admin/v1/providers/{key}/performance-profiles/{chain}/{operation}/{mode}
//   GET   /api/admin/v1/providers/{key}/runtime-states           （admin:provider:read）
//   PUT   /api/admin/v1/providers/{key}/secrets/{kind}           （admin:provider:update + Step-up）
//
// 本文件只放类型与纯函数：字段含义的额外解释必须通过 useDialog() 弹窗呈现（AGENTS.md / docs/code-standards.md）。
// 数值字段一律容忍后端把 numeric 序列化成字符串（numeric(18,6) 可能以十进制字符串返回）。

import type { StatusTone } from "@chain/ui";
import { fmtDateTime, fmtNumber } from "../lib/adminApi";

// ---------------------------------------------------------------- 数值与通用取值

/** num 把后端可能返回的 number / 十进制字符串安全转成数字；空值与非数字返回 null。 */
export function num(value: unknown): number | null {
  if (value === null || value === undefined || value === "") return null;
  const parsed = typeof value === "number" ? value : Number(value);
  return Number.isFinite(parsed) ? parsed : null;
}

function str(value: unknown): string | undefined {
  if (value === null || value === undefined || value === "") return undefined;
  return typeof value === "string" ? value : String(value);
}

function bool(value: unknown): boolean | null {
  if (value === true || value === "true") return true;
  if (value === false || value === "false") return false;
  return null;
}

function firstValue(raw: Record<string, unknown>, keys: string[]): unknown {
  for (const key of keys) {
    const value = raw[key];
    if (value !== undefined && value !== null && value !== "") return value;
  }
  return undefined;
}

/** pickArray 从列表信封里取出数组：既接受裸数组，也接受 items / snapshots 等具名键。 */
export function pickArray<T>(payload: unknown, keys: string[]): T[] {
  if (Array.isArray(payload)) return payload as T[];
  if (payload && typeof payload === "object") {
    const root = payload as Record<string, unknown>;
    for (const key of keys) {
      const value = root[key];
      if (Array.isArray(value)) return value as T[];
    }
  }
  return [];
}

// ---------------------------------------------------------------- 通道健康

export interface ProviderChannels {
  data_api?: string;
  solana_rpc?: string;
}

/** 通道顺序固定为 Data API → Solana RPC；后端新增通道时追加在末尾。 */
export const CHANNEL_KEYS = ["data_api", "solana_rpc"] as const;
export type ChannelKey = (typeof CHANNEL_KEYS)[number];

export function channelLabel(channel: string): string {
  switch (channel) {
    case "data_api":
      return "Data API";
    case "solana_rpc":
      return "Solana RPC";
    default:
      return channel || "—";
  }
}

export function channelTone(state: string | undefined | null): StatusTone {
  switch ((state ?? "").toUpperCase()) {
    case "NORMAL":
    case "OK":
    case "HEALTHY":
    case "READY":
    case "UP":
    case "CLOSED":
      return "ok";
    case "DEGRADED":
    case "HALF_OPEN":
    case "WARN":
    case "WARNING":
      return "warn";
    case "OPEN":
    case "DOWN":
    case "FAILED":
    case "ERROR":
    case "UNHEALTHY":
      return "danger";
    case "":
      return "neutral";
    default:
      return "info";
  }
}

export function channelStateText(state: string | undefined | null): string {
  return state && state !== "" ? state : "—";
}

// ---------------------------------------------------------------- 额度

export interface ProviderQuota {
  account_tier?: string;
  balance?: string | number | null;
  balance_unit?: string;
  cu_spent_today?: number | string | null;
  cu_day_limit?: number | string | null;
  cu_remaining_today?: number | string | null;
  rate_limit?: number | string | null;
  synced_at?: string | null;
  stale?: boolean | null;
  source?: string;
  /** 管理同步的 Key 摘要（后端可选补充字段；缺失时界面按「—」展示，不猜测）。 */
  key_id?: string;
  key_name?: string;
  key_enabled?: boolean | null;
  sync_status?: string;
  /** 由后端显式告知管理 Token 是否齐备；缺失时前端回退到 secrets[].secret_kind 判断。 */
  management_configured?: boolean;
}

/** toQuota 把详情 quota 或额度快照行归一到同一展示口径。 */
export function toQuota(raw: Record<string, unknown>): ProviderQuota {
  return {
    account_tier: str(firstValue(raw, ["account_tier", "tier"])),
    balance: (firstValue(raw, ["account_balance", "balance"]) ?? null) as string | number | null,
    balance_unit: str(firstValue(raw, ["account_balance_unit", "balance_unit"])),
    cu_spent_today: (firstValue(raw, ["cu_spent_today", "cu_spent", "cu_day"]) ?? null) as number | string | null,
    cu_day_limit: (firstValue(raw, ["cu_day_limit", "day_limit"]) ?? null) as number | string | null,
    cu_remaining_today: (firstValue(raw, ["cu_remaining_today", "cu_remaining"]) ?? null) as number | string | null,
    rate_limit: (firstValue(raw, ["rate_limit"]) ?? null) as number | string | null,
    synced_at: str(firstValue(raw, ["synced_at", "recorded_at", "created_at", "snapshot_at"])) ?? null,
    stale: bool(firstValue(raw, ["stale"])),
    source: str(firstValue(raw, ["source", "data_source"])),
    key_id: str(firstValue(raw, ["key_id", "api_key_id"])),
    key_name: str(firstValue(raw, ["key_name", "api_key_name"])),
    key_enabled: bool(firstValue(raw, ["key_enabled", "api_key_enabled"])),
    sync_status: str(firstValue(raw, ["sync_status", "status"])),
    management_configured: bool(firstValue(raw, ["management_configured", "management_sync_configured"])) ?? undefined,
  };
}

/** hasDayLimit 只在日上限为正时成立：为空或 0 都表示「未设置日上限」。 */
export function hasDayLimit(quota: ProviderQuota | null | undefined): boolean {
  const limit = num(quota?.cu_day_limit);
  return limit !== null && limit > 0;
}

export function cuLimitText(quota: ProviderQuota | null | undefined): string {
  return hasDayLimit(quota) ? fmtNumber(num(quota?.cu_day_limit)) : "未设置日上限";
}

/** 当日 CU（已用/上限）：上限缺失时不得显示成「已用/0」。 */
export function cuUsageText(quota: ProviderQuota | null | undefined): string {
  const spent = num(quota?.cu_spent_today);
  const spentText = spent === null ? "—" : fmtNumber(spent);
  return hasDayLimit(quota) ? spentText + " / " + fmtNumber(num(quota?.cu_day_limit)) : spentText + " / 未设置日上限";
}

export function cuRemainingValue(quota: ProviderQuota | null | undefined): number | null {
  if (!hasDayLimit(quota)) return null;
  const limit = num(quota?.cu_day_limit);
  if (limit === null) return null;
  const remaining = num(quota?.cu_remaining_today);
  if (remaining !== null) return remaining;
  const spent = num(quota?.cu_spent_today);
  return spent === null ? null : limit - spent;
}

export function cuRemainingText(quota: ProviderQuota | null | undefined): string {
  if (!hasDayLimit(quota)) return "未设置日上限";
  const remaining = cuRemainingValue(quota);
  return remaining === null ? "—" : fmtNumber(remaining);
}

/** 剩余 CU 百分比：只在设置了正的日上限时计算；否则返回 null（界面显示「未设置日上限」）。 */
export function cuRemainingPercent(quota: ProviderQuota | null | undefined): number | null {
  if (!hasDayLimit(quota)) return null;
  const limit = num(quota?.cu_day_limit);
  const remaining = cuRemainingValue(quota);
  if (limit === null || limit <= 0 || remaining === null) return null;
  return Math.max(0, Math.min(1, remaining / limit));
}

export function cuRemainingPercentText(quota: ProviderQuota | null | undefined): string {
  const percent = cuRemainingPercent(quota);
  return percent === null ? "未设置日上限" : (percent * 100).toFixed(1) + "%";
}

/** 列表列专用：没有日上限时只说一次「未设置日上限」，避免剩余量与百分比重复同一句话。 */
export function cuRemainingSummary(quota: ProviderQuota | null | undefined): string {
  if (!hasDayLimit(quota)) return "未设置日上限";
  const remaining = cuRemainingText(quota);
  const percent = cuRemainingPercentText(quota);
  return percent === "未设置日上限" ? remaining : remaining + " · " + percent;
}

const CURRENCY_PREFIX: Record<string, string> = { usd: "$", cny: "¥", rmb: "¥", eur: "€", gbp: "£", jpy: "¥" };
const CURRENCY_SUFFIX: Record<string, string> = { usdt: "USDT", usdc: "USDC", dai: "DAI" };

/** balanceUnitLabel：balance_unit 不是具体货币时只展示数值 + 单位文案，不硬编码 $。 */
export function balanceUnitLabel(unit: string | null | undefined): string {
  const raw = (unit ?? "").trim();
  if (raw === "") return "单位未提供";
  const key = raw.toLowerCase();
  if (key === "provider_unit") return "Provider 单位";
  if (key === "cu" || key === "credit" || key === "credits") return "CU";
  return raw;
}

/**
 * fmtDecimal 只裁掉 numeric(38,18) 字符串末尾无意义的 0（数值不变），
 * 避免 18 位小数把指标卡撑成多行；原始字符串仍在额度明细里单独展示。
 */
export function fmtDecimal(value: string | number | null | undefined): string {
  if (value === null || value === undefined || value === "") return "—";
  const raw = typeof value === "string" ? value : String(value);
  if (!raw.includes(".")) return raw;
  const trimmed = raw.replace(/0+$/, "").replace(/[.]$/, "");
  return trimmed === "" || trimmed === "-" ? "0" : trimmed;
}

export function fmtBalance(balance: string | number | null | undefined, unit: string | null | undefined): string {
  if (balance === null || balance === undefined || balance === "") return "—";
  const raw = fmtDecimal(balance);
  const key = (unit ?? "").trim().toLowerCase();
  const prefix = CURRENCY_PREFIX[key];
  if (prefix) return prefix + raw;
  const suffix = CURRENCY_SUFFIX[key];
  if (suffix) return raw + " " + suffix;
  return raw + " " + balanceUnitLabel(unit);
}

// ---------------------------------------------------------------- 额度趋势

export interface QuotaHistoryPoint {
  /** 快照时间（synced_at）；后端缺失时为空串，按返回顺序展示。 */
  at: string;
  quota: ProviderQuota;
}

export function quotaHistoryPoints(payload: unknown): QuotaHistoryPoint[] {
  const rows = pickArray<Record<string, unknown>>(payload, ["items", "snapshots", "history", "quota_history", "points"]);
  return rows
    .map((raw) => {
      const quota = toQuota(raw);
      return { at: quota.synced_at ?? "", quota };
    })
    .sort((left, right) => left.at.localeCompare(right.at));
}

/** sync-quota 在 60 秒内会返回上次快照；后端字段名以契约实现为准，这里容忍多个等价标志。 */
export function isCachedSnapshot(data: Record<string, unknown>): boolean {
  const flags = ["cached", "from_cache", "snapshot_cached", "reused", "reused_snapshot", "returned_cached", "deduplicated"];
  if (flags.some((key) => data[key] === true)) return true;
  const status = typeof data.sync_status === "string" ? data.sync_status.toLowerCase() : "";
  return status === "cached" || status === "reused" || status === "deduplicated";
}

/** quotaPayloadOf：同步命令的响应可能把快照嵌在 snapshot / quota 下。 */
export function quotaPayloadOf(data: Record<string, unknown>): Record<string, unknown> {
  const nested = data.snapshot ?? data.quota ?? data.current_snapshot;
  if (nested && typeof nested === "object" && !Array.isArray(nested)) return nested as Record<string, unknown>;
  return data;
}

export function quotaSourceText(quota: ProviderQuota | null | undefined): string {
  if (!quota) return "—";
  if (quota.synced_at) return quota.stale === true ? "同步已过期" : "同步正常";
  return quota.sync_status ? quota.sync_status : "—";
}

// ---------------------------------------------------------------- 密钥

export type SecretKind = "runtime_api_key" | "statistics_api_token" | "keys_api_token";

export const SECRET_KINDS: SecretKind[] = ["runtime_api_key", "statistics_api_token", "keys_api_token"];

export const SECRET_KIND_META: Record<SecretKind, { label: string; purpose: string; recommendedRef: string }> = {
  runtime_api_key: {
    label: "运行时 API Key",
    purpose: "Data API 与 JSON-RPC 请求使用",
    recommendedRef: "env://DRPC_API_KEY",
  },
  statistics_api_token: {
    label: "统计 API Token",
    purpose: "账户余额与聚合用量同步使用",
    recommendedRef: "env://DRPC_STATISTICS_API_TOKEN",
  },
  keys_api_token: {
    label: "Keys API Token",
    purpose: "Key 状态、当日 CU 与限制同步使用",
    recommendedRef: "env://DRPC_KEYS_API_TOKEN",
  },
};

export function isSecretKind(value: unknown): value is SecretKind {
  return typeof value === "string" && (SECRET_KINDS as string[]).includes(value);
}

export function secretKindLabel(kind: string | undefined | null): string {
  return isSecretKind(kind) ? SECRET_KIND_META[kind].label : kind && kind !== "" ? kind : "—";
}

export interface ProviderSecretSummary {
  id?: string;
  secret_kind?: string;
  configured?: boolean;
  secret_ref?: string;
  prefix?: string;
  fingerprint?: string;
  status?: string;
  rotated_at?: string | null;
  last_used_at?: string | null;
  expires_at?: string | null;
  note?: string;
  plaintext_available?: boolean;
}

/** 判断某个 secret_kind 当前是否已配置（后端可能只给 configured，也可能只给 status）。 */
export function secretConfigured(secret: ProviderSecretSummary | null | undefined): boolean {
  if (!secret) return false;
  if (secret.configured === true) return true;
  if (secret.configured === false) return false;
  return secret.status === "active";
}

// ---------------------------------------------------------------- Provider 行派生口径

export interface ProviderSecretCarrier {
  secrets?: ProviderSecretSummary[];
  active_secret?: ProviderSecretSummary;
  management_sync_configured?: boolean;
  quota?: ProviderQuota | null;
}

/** 列表汇总的「未配置密钥」按 runtime_api_key 判断（开发文档 §14.1）。 */
export function runtimeKeyConfigured(row: ProviderSecretCarrier): boolean {
  const secrets = row.secrets;
  if (Array.isArray(secrets)) {
    const runtime = secrets.find((secret) => secret.secret_kind === "runtime_api_key");
    if (runtime) return secretConfigured(runtime);
  }
  return secretConfigured(row.active_secret);
}

/** 管理同步 Token（statistics_api_token / keys_api_token）是否齐备；无法判断时返回 null。 */
export function managementSecretsConfigured(row: ProviderSecretCarrier): boolean | null {
  if (typeof row.management_sync_configured === "boolean") return row.management_sync_configured;
  if (typeof row.quota?.management_configured === "boolean") return row.quota.management_configured;
  const secrets = row.secrets;
  if (!Array.isArray(secrets)) return null;
  const required = ["statistics_api_token", "keys_api_token"];
  const management = required.map((kind) => secrets.find((secret) => secret.secret_kind === kind));
  // 两类管理 Token 缺任何一类都算「管理同步未配置」：统计缺了没有余额/用量，Keys 缺了没有 Key 状态与当日 CU。
  if (management.some((secret) => secret === undefined)) return false;
  return management.every((secret) => secretConfigured(secret));
}

export function quotaSyncedAtText(quota: ProviderQuota | null | undefined): string {
  return quota?.synced_at ? fmtDateTime(quota.synced_at) : "—";
}

// ---------------------------------------------------------------- 性能档位

export interface PerformanceProfile {
  provider_key?: string;
  chain: string;
  dataset: string;
  operation_class: string;
  mode: string;
  shard_concurrency?: number | string;
  request_concurrency?: number | string;
  requests_per_minute?: number | string;
  requests_per_second?: number | string;
  cu_per_minute?: number | string;
  page_size?: number | string;
  batch_size?: number | string;
  request_weight?: number | string;
  request_timeout_seconds?: number | string;
  hard_ceiling?: number | string;
  revision?: number | string;
  verification_source?: string;
  verified_at?: string | null;
  /** 后端若在档位行直接给出通道，则优先使用；否则由 operation_class 推导。 */
  api_channel?: string;
}

export type ProfileParamName =
  | "shard_concurrency"
  | "request_concurrency"
  | "requests_per_minute"
  | "requests_per_second"
  | "cu_per_minute"
  | "page_size"
  | "batch_size"
  | "request_weight"
  | "request_timeout_seconds"
  | "hard_ceiling";

/** 可编辑参数清单：integer=false 的字段在库里是 numeric(18,6)，允许小数。 */
export const PROFILE_PARAM_FIELDS: Array<{ name: ProfileParamName; label: string; integer: boolean }> = [
  { name: "shard_concurrency", label: "Shard 并发 shard_concurrency", integer: true },
  { name: "request_concurrency", label: "请求并发 request_concurrency", integer: true },
  { name: "requests_per_minute", label: "每分钟请求 requests_per_minute", integer: false },
  { name: "requests_per_second", label: "每秒请求 requests_per_second", integer: false },
  { name: "cu_per_minute", label: "每分钟 CU cu_per_minute", integer: true },
  { name: "page_size", label: "分页 page_size", integer: true },
  { name: "batch_size", label: "批次 batch_size", integer: true },
  { name: "request_weight", label: "全局权重 request_weight", integer: true },
  { name: "request_timeout_seconds", label: "超时（秒）request_timeout_seconds", integer: true },
  { name: "hard_ceiling", label: "硬上限 hard_ceiling", integer: true },
];

export const PROFILE_MODES = ["safe", "fast", "turbo"] as const;

/** 档位行的通道归属：档位表本身没有 api_channel 列，按 operation_class 推导（开发文档 §10.1）。 */
export function profileChannel(profile: PerformanceProfile): string {
  if (profile.api_channel) return profile.api_channel;
  const operation = (profile.operation_class ?? "").toLowerCase();
  if (operation === "solana_history" || operation.endsWith("_history") || operation === "data_api") return "data_api";
  if (operation.startsWith("solana_") || operation === "rpc" || operation === "json_rpc") return "solana_rpc";
  return operation || "default";
}

export function profileModeTone(mode: string): StatusTone {
  switch ((mode ?? "").toLowerCase()) {
    case "safe":
      return "ok";
    case "fast":
      return "info";
    case "turbo":
      return "warn";
    default:
      return "neutral";
  }
}

export function profilesOf(payload: unknown): PerformanceProfile[] {
  return pickArray<PerformanceProfile>(payload, ["items", "profiles", "performance_profiles"]);
}

export function runtimeStatesOf(payload: unknown): ProviderRuntimeState[] {
  return pickArray<ProviderRuntimeState>(payload, ["items", "states", "runtime_states"]);
}

export interface ProviderRuntimeState {
  provider_key?: string;
  chain?: string;
  operation_class?: string;
  requested_mode?: string;
  effective_mode?: string;
  target_concurrency?: number | string;
  active_concurrency?: number | string;
  requests_per_minute?: number | string;
  requests_per_second?: number | string;
  cu_per_minute?: number | string;
  degrade_reason?: string;
  updated_at?: string;
}

/** 运行时降级原因为空表示未降级；界面用它区分「正常」与「实际档位被压低」。 */
export function runtimeStateTone(state: ProviderRuntimeState): StatusTone {
  if ((state.degrade_reason ?? "") !== "") return "warn";
  if ((state.effective_mode ?? "") !== "" && (state.requested_mode ?? "") !== "" && state.effective_mode !== state.requested_mode) {
    return "warn";
  }
  return "ok";
}
