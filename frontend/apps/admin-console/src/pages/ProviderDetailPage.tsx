import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { ApiError, newIdempotencyKey, useAuth } from "@chain/api-client";
import {
  Card,
  ChartPanel,
  DataTable,
  EntityLink,
  ErrorState,
  Metric,
  PageHeader,
  SkeletonRows,
  StatusBadge,
  statusToneFor,
  dialogFormValues,
  dialogInputValue,
  type DataTableColumn,
  type StatusTone,
  useDialog,
} from "@chain/ui";
import {
  JsonBlock,
  KeyValueList,
  MaskedSecret,
  NumberInput,
  SelectInput,
  Tabs,
  TextInput,
} from "../components/s6";
import {
  fmtDateTime,
  fmtMillis,
  fmtNumber,
  fmtPercent,
  fmtUSD,
  useAdminCommand,
  useAdminPatch,
  useAdminResource,
} from "../lib/adminApi";
import {
  CHANNEL_KEYS,
  PROFILE_MODES,
  PROFILE_PARAM_FIELDS,
  SECRET_KINDS,
  SECRET_KIND_META,
  channelLabel,
  channelStateText,
  channelTone,
  cuLimitText,
  cuRemainingPercent,
  cuRemainingPercentText,
  cuRemainingText,
  cuUsageText,
  fmtBalance,
  isCachedSnapshot,
  managementSecretsConfigured,
  num,
  profileChannel,
  profileModeTone,
  profilesOf,
  quotaHistoryPoints,
  quotaPayloadOf,
  quotaSourceText,
  quotaSyncedAtText,
  runtimeKeyConfigured,
  runtimeStatesOf,
  runtimeStateTone,
  secretConfigured,
  secretKindLabel,
  toQuota,
  type PerformanceProfile,
  type ProviderQuota,
  type ProviderRuntimeState,
  type ProviderSecretSummary,
  type QuotaHistoryPoint,
  type SecretKind,
} from "./provider.types";

/**
 * A-PROVIDER-02：Provider 详情（路由 /providers/:provider，权限 admin:provider:read）。
 *
 * 真实接口：
 *   GET   /api/admin/v1/providers/{key}
 *   GET   /api/admin/v1/providers/{key}/secrets
 *   PATCH /api/admin/v1/providers/{key}                     （If-Match: revision）
 *   POST  /api/admin/v1/providers/{key}/commands/{connect|disconnect|test|rotate-secret}
 *
 * 提示类内容一律走弹窗；页面正文只有字段标签、业务数据与操作控件。
 */

interface ProviderUsage {
  calls_24h?: number;
  errors_24h?: number;
  error_rate_24h?: number;
  p95_ms?: number;
  credits_24h?: number;
  cost_usd_24h?: number | string;
  window?: string;
  source?: string;
}

interface DeclaredCapabilities {
  name?: string;
  datasets?: string[];
  max_page_size?: number;
  supports_time_range?: boolean;
}

interface RuntimeHealth {
  state?: string;
  last_check?: string;
  error?: string;
  consecutive_failures?: number;
  recent_error_rate?: number;
  current_concurrency?: number;
  declared_capabilities?: DeclaredCapabilities;
  /** dRPC 按通道上报：data_api / solana_rpc，取值 NORMAL/DEGRADED/OPEN/... */
  channels?: { data_api?: string; solana_rpc?: string };
  source?: string;
}

interface ActiveSecret {
  configured?: boolean;
  secret_kind?: string;
  status?: string;
  secret_ref?: string;
  prefix?: string;
  fingerprint?: string;
  rotated_at?: string;
  last_used_at?: string;
  plaintext_available?: boolean;
}

interface ProviderDetail {
  key: string;
  display_name: string;
  kind: string;
  chains: string[];
  datasets: string[];
  capabilities: unknown[];
  endpoint: string;
  status: string;
  concurrency: number;
  rate_limit_per_minute: number;
  priority: number;
  revision: number;
  capability_drift?: unknown;
  last_test_at?: string;
  last_test_result?: Record<string, unknown>;
  runtime_health?: RuntimeHealth;
  usage?: ProviderUsage;
  active_secret?: ActiveSecret;
  /** 后端只增不改的新增字段（dRPC 额度 / 档位 / 分类密钥）。 */
  quota?: ProviderQuota | null;
  performance_profiles?: PerformanceProfile[];
  secrets?: ProviderSecretSummary[];
  management_sync_configured?: boolean;
  updated_at: string;
}

interface SecretRow {
  id: string;
  /** 0075 迁移新增：runtime_api_key / statistics_api_token / keys_api_token。 */
  secret_kind?: string;
  secret_ref: string;
  prefix: string;
  fingerprint: string;
  status: string;
  note: string;
  rotated_at: string;
  last_used_at?: string | null;
  expires_at?: string | null;
  plaintext_available: boolean;
}

interface SecretsEnvelope {
  items: SecretRow[];
}

interface ConfigForm {
  display_name: string;
  endpoint: string;
  concurrency: number;
  rate_limit_per_minute: number;
  priority: number;
  status: string;
}

const STATUS_OPTIONS = [
  { value: "connected", label: "connected" },
  { value: "disconnected", label: "disconnected" },
  { value: "degraded", label: "degraded" },
  { value: "disabled", label: "disabled" },
];

function text(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

function listText(value: unknown): string {
  if (Array.isArray(value)) return value.length > 0 ? value.map((item) => text(item)).join("、") : "无";
  return text(value);
}

function providerTone(status: string | undefined): StatusTone {
  switch (status) {
    case "connected":
      return "ok";
    case "degraded":
      return "warn";
    case "disabled":
      return "danger";
    case "disconnected":
      return "neutral";
    default:
      return statusToneFor(status ?? "");
  }
}

function healthTone(state: string | undefined): StatusTone {
  switch ((state ?? "").toLowerCase()) {
    case "healthy":
    case "ok":
    case "ready":
      return "ok";
    case "degraded":
    case "warning":
      return "warn";
    case "down":
    case "failed":
    case "unhealthy":
    case "error":
      return "danger";
    default:
      return "neutral";
  }
}

function apiErrorDetails(error: unknown): Array<{ label: string; value: ReactNode }> {
  if (!(error instanceof ApiError)) return [];
  const details: Array<{ label: string; value: ReactNode }> = [];
  if (error.traceId) details.push({ label: "Trace", value: <span className="mono">{error.traceId}</span> });
  if (error.code) details.push({ label: "错误码", value: error.code });
  details.push({ label: "HTTP", value: String(error.status) });
  Object.entries(error.details ?? {}).forEach(([key, value]) => {
    if (value === null || value === undefined) return;
    details.push({ label: key, value: text(value) });
  });
  return details;
}

function impactDetails(impact: unknown): Array<{ label: string; value: ReactNode }> {
  const data = (impact ?? {}) as Record<string, unknown>;
  return [
    { label: "受影响链", value: listText(data.affected_chains) },
    { label: "受影响 Dataset", value: listText(data.affected_datasets) },
    { label: "运行中任务", value: text(data.affected_running_jobs) },
    { label: "等待中任务", value: text(data.affected_waiting_jobs) },
    { label: "无 fallback 的策略", value: listText(data.policies_without_fallback) },
  ];
}

// ---- dRPC 额度 / 档位 / 密钥：展示辅助 ----

interface SecretKindRow {
  kind: SecretKind;
  summary: ProviderSecretSummary | null;
  configured: boolean;
}

/** 新增 Tab 的数据按需加载：加载失败只通过弹窗提示，页面正文不放提示区。 */
function useResourceErrorNotice(error: string | null, title: string): void {
  const dialog = useDialog();
  const notified = useRef<string | null>(null);
  useEffect(() => {
    if (!error) {
      notified.current = null;
      return;
    }
    if (notified.current === error) return;
    notified.current = error;
    dialog.notify("error", title, error);
  }, [dialog, error, title]);
}

/** quotaKeyValueItems 把额度快照字段翻译成页面字段标签；字段含义的解释放在「口径」弹窗里。 */
function quotaKeyValueItems(quota: ProviderQuota): Array<{ label: string; value: ReactNode; mono?: boolean }> {
  return [
    { label: "账户层级 account_tier", value: quota.account_tier ?? "—" },
    { label: "账户余额 balance", value: fmtBalance(quota.balance, quota.balance_unit) },
    {
      label: "balance 原始字符串",
      value: quota.balance === null || quota.balance === undefined || quota.balance === "" ? "—" : String(quota.balance),
      mono: true,
    },
    { label: "余额单位 balance_unit", value: quota.balance_unit ?? "—", mono: true },
    {
      label: "当日已用 CU cu_spent_today",
      value:
        quota.cu_spent_today === null || quota.cu_spent_today === undefined
          ? "—"
          : fmtNumber(num(quota.cu_spent_today)),
    },
    { label: "日上限 cu_day_limit", value: cuLimitText(quota) },
    { label: "剩余 CU cu_remaining_today", value: cuRemainingText(quota) },
    { label: "剩余百分比", value: cuRemainingPercentText(quota) },
    {
      label: "限流 rate_limit",
      value: quota.rate_limit === null || quota.rate_limit === undefined ? "—" : fmtNumber(num(quota.rate_limit)),
    },
    { label: "Key ID key_id", value: quota.key_id ?? "—", mono: true },
    { label: "Key 名称 key_name", value: quota.key_name ?? "—" },
    { label: "Key 启用 key_enabled", value: quota.key_enabled === true ? "是" : quota.key_enabled === false ? "否" : "—" },
    { label: "同步时间 synced_at", value: quotaSyncedAtText(quota) },
    { label: "同步状态", value: quotaSourceText(quota) },
    { label: "来源 source", value: quota.source ?? "—", mono: true },
  ];
}

/** channelProbeDetails 渲染 probe-channels 的逐通道结果；对象与纯状态字符串都兼容。 */
function channelProbeDetails(data: Record<string, unknown>): Array<{ label: string; value: ReactNode }> {
  const channels = data.channels && typeof data.channels === "object" ? (data.channels as Record<string, unknown>) : {};
  const keys = Object.keys(channels).length > 0 ? Object.keys(channels) : [...CHANNEL_KEYS];
  return keys.map((key) => {
    const raw = channels[key];
    if (raw && typeof raw === "object" && !Array.isArray(raw)) {
      const item = raw as Record<string, unknown>;
      const state = typeof item.state === "string" ? item.state : typeof item.status === "string" ? item.status : "";
      return {
        label: channelLabel(key),
        value: (
          <span className="toolbar" style={{ justifyContent: "flex-start", gap: "var(--sp-2)" }}>
            <StatusBadge tone={item.ok === true ? "ok" : item.ok === false ? "danger" : channelTone(state)}>
              {item.ok === true ? "ok" : item.ok === false ? "failed" : channelStateText(state)}
            </StatusBadge>
            <span className="num">{fmtMillis(item.latency_ms)}</span>
            {item.error ? <span className="mono">{text(item.error)}</span> : null}
          </span>
        ),
      };
    }
    const state = typeof raw === "string" ? raw : "";
    return {
      label: channelLabel(key),
      value: <StatusBadge tone={channelTone(state)}>{channelStateText(state)}</StatusBadge>,
    };
  });
}

/** QuotaTrendChart：不引入图表依赖，用等高柱表示每次快照的当日已用 CU。 */
function QuotaTrendChart({ points }: { points: QuotaHistoryPoint[] }) {
  const limit = points.reduce<number>((max, point) => Math.max(max, num(point.quota.cu_day_limit) ?? 0), 0);
  const maxSpent = points.reduce<number>((max, point) => Math.max(max, num(point.quota.cu_spent_today) ?? 0), 0);
  // 按窗口内最大已用值缩放：日上限通常远大于已用值，用上限做分母会让趋势压成一条线。
  const scale = Math.max(maxSpent, 1);
  return (
    <div
      role="img"
      aria-label="当日 CU 已用趋势"
      style={{ display: "flex", alignItems: "flex-end", gap: "var(--sp-1)", height: 150, padding: "0 var(--sp-1)" }}
    >
      {points.map((point, index) => {
        const spent = num(point.quota.cu_spent_today) ?? 0;
        const height = Math.max(2, Math.round((spent / scale) * 100));
        const overLimit = limit > 0 && spent >= limit;
        return (
          <div
            key={(point.at || String(index)) + "-" + String(index)}
            style={{
              flex: "1 1 0",
              minWidth: 4,
              height: "100%",
              display: "flex",
              flexDirection: "column",
              justifyContent: "flex-end",
            }}
            title={fmtDateTime(point.at) + " · " + fmtNumber(spent) + " CU"}
          >
            <div
              style={{
                height: height + "%",
                background: overLimit ? "var(--c-danger)" : "var(--c-cobalt)",
                borderRadius: "2px 2px 0 0",
              }}
            />
          </div>
        );
      })}
    </div>
  );
}

/** profileSaveDetails 把 PATCH 结果拼成成功弹窗明细。 */
function profileSaveDetails(
  data: Record<string, unknown>,
  profile: PerformanceProfile,
  changes: Array<{ label: string; value: ReactNode }>,
): Array<{ label: string; value: ReactNode }> {
  return [
    { label: "档位", value: channelLabel(profileChannel(profile)) + " / " + String(profile.mode ?? "").toUpperCase() },
    ...changes,
    { label: "原 revision", value: String(profile.revision ?? "—") },
    { label: "新 revision", value: text(data.revision) },
    { label: "验证来源", value: text(data.verification_source) },
    { label: "验证时间", value: fmtDateTime(data.verified_at) },
  ];
}

export default function ProviderDetailPage() {
  const params = useParams<{ provider?: string }>();
  const navigate = useNavigate();
  const dialog = useDialog();
  const { client, can } = useAuth();

  const providerKey = params.provider ?? "";
  const basePath = providerKey ? `/api/admin/v1/providers/${encodeURIComponent(providerKey)}` : null;

  const resource = useAdminResource<ProviderDetail>(basePath);
  const secretsResource = useAdminResource<SecretsEnvelope>(basePath ? `${basePath}/secrets` : null);
  const command = useAdminCommand();
  const patch = useAdminPatch();

  /** 新增 Tab 的数据按需加载：只有第一次打开对应 Tab 才请求接口（Tabs 的 onChange 上报）。 */
  const [visitedTabs, setVisitedTabs] = useState<string[]>(["health"]);
  const markVisited = (key: string) => {
    setVisitedTabs((current) => (current.includes(key) ? current : [...current, key]));
  };
  const quotaPath = visitedTabs.includes("quota") && basePath ? basePath + "/quota" : null;
  const quotaHistoryPath = visitedTabs.includes("quota") && basePath ? basePath + "/quota-history?range=day" : null;
  const profilesPath = visitedTabs.includes("profiles") && basePath ? basePath + "/performance-profiles" : null;
  const runtimeStatesPath = visitedTabs.includes("profiles") && basePath ? basePath + "/runtime-states" : null;

  const quotaResource = useAdminResource<Record<string, unknown>>(quotaPath);
  const quotaHistoryResource = useAdminResource<Record<string, unknown>>(quotaHistoryPath);
  const profilesResource = useAdminResource<Record<string, unknown>>(profilesPath);
  const runtimeStatesResource = useAdminResource<Record<string, unknown>>(runtimeStatesPath);

  const provider = resource.data;
  const secretRows = secretsResource.data?.items ?? [];

  const quota = useMemo<ProviderQuota | null>(() => {
    const raw = quotaResource.data;
    if (raw) {
      const normalized = toQuota(quotaPayloadOf(raw));
      if (
        normalized.synced_at ||
        normalized.balance !== null ||
        normalized.cu_spent_today !== null ||
        normalized.account_tier
      ) {
        return normalized;
      }
    }
    return provider?.quota ?? null;
  }, [quotaResource.data, provider]);

  const historyPoints = useMemo(() => quotaHistoryPoints(quotaHistoryResource.data), [quotaHistoryResource.data]);

  const detailProfiles = provider?.performance_profiles;
  const profiles = useMemo(() => {
    const fromApi = profilesOf(profilesResource.data);
    const rows = fromApi.length > 0 ? fromApi : (detailProfiles ?? []);
    const modeRank = (mode: string): number => {
      const index = PROFILE_MODES.findIndex((item) => item === mode.toLowerCase());
      return index === -1 ? PROFILE_MODES.length : index;
    };
    return [...rows].sort((left, right) => {
      const byChannel = profileChannel(left).localeCompare(profileChannel(right));
      if (byChannel !== 0) return byChannel;
      const byMode = modeRank(left.mode ?? "") - modeRank(right.mode ?? "");
      if (byMode !== 0) return byMode;
      return (left.dataset ?? "").localeCompare(right.dataset ?? "");
    });
  }, [profilesResource.data, detailProfiles]);

  const runtimeStates = useMemo(() => runtimeStatesOf(runtimeStatesResource.data), [runtimeStatesResource.data]);

  const secretKindRows = useMemo<SecretKindRow[]>(() => {
    const detailSecrets = provider?.secrets ?? [];
    const active = provider?.active_secret;
    return SECRET_KINDS.map((kind) => {
      const fromDetail = detailSecrets.find((secret) => secret.secret_kind === kind) ?? null;
      const fromList = secretRows.find((secret) => secret.secret_kind === kind) ?? null;
      let summary: ProviderSecretSummary | null = fromDetail ?? fromList;
      if (!summary && kind === "runtime_api_key" && active?.configured === true) {
        summary = { ...active, secret_kind: kind, status: active.status ?? "active" };
      }
      return { kind, summary, configured: secretConfigured(summary) };
    });
  }, [provider, secretRows]);

  useResourceErrorNotice(quotaResource.error, "额度读取失败");
  useResourceErrorNotice(quotaHistoryResource.error, "额度趋势读取失败");
  useResourceErrorNotice(profilesResource.error, "性能档位读取失败");
  useResourceErrorNotice(runtimeStatesResource.error, "运行时档位读取失败");

  const [form, setForm] = useState<ConfigForm | null>(null);
  useEffect(() => {
    if (!provider) return;
    setForm({
      display_name: provider.display_name ?? "",
      endpoint: provider.endpoint ?? "",
      concurrency: Number(provider.concurrency ?? 0),
      rate_limit_per_minute: Number(provider.rate_limit_per_minute ?? 0),
      priority: Number(provider.priority ?? 0),
      status: provider.status ?? "connected",
    });
  }, [provider]);

  const usage = provider?.usage ?? {};
  const health = provider?.runtime_health;
  const declared = health?.declared_capabilities;
  const lastTest = provider?.last_test_result ?? {};
  const activeSecret = provider?.active_secret ?? {};

  const requirePermission = (permission: string): boolean => {
    if (can(permission)) return true;
    dialog.notify("warning", "缺少控制权限", null, [{ label: "所需权限", value: permission }]);
    return false;
  };

  /** prompt 的空值与取消需要区分：空值代表「不修改」，取消代表放弃整个流程。 */
  const askText = async (request: Parameters<typeof dialog.prompt>[0]) => {
    const raw = await dialog.prompt(request);
    if (raw === null) return { cancelled: true, value: "" };
    const value = raw.startsWith("input:") ? raw.slice("input:".length).trim() : raw.trim();
    return { cancelled: false, value };
  };

  const showScope = () => {
    if (!provider) return;
    dialog.notify("info", `${provider.display_name || provider.key} 的数据口径`, null, [
      { label: "Provider 描述符", value: "postgres:provider_descriptors" },
      { label: "用量窗口", value: `${text(usage.window)} · ${text(usage.source)}` },
      { label: "错误率", value: "provider_usage 最近 24 小时非 success 调用占比" },
      { label: "P95", value: "provider_usage.duration_ms 的 95 分位" },
      { label: "成本", value: "estimated_cost_usd 累加，按十进制字符串展示" },
      { label: "运行时健康", value: "provider.Health()（仅当前进程已加载该 Provider 时有值）" },
      { label: "密钥规则", value: "只登记引用名 + 前 6 位 + SHA-256 指纹；明文永不回显、永不经前端传入" },
      { label: "配置版本", value: `revision ${fmtNumber(provider.revision)}，PATCH 需携带 If-Match` },
      { label: "命令权限", value: "commands/* 需要 admin:provider:read；配置 PATCH 需要 admin:provider:update" },
      { label: "运行与额度", value: "GET /quota 与 /quota-history?range=day（admin:cost:read）；sync-quota 60 秒内返回上次快照" },
      { label: "账户余额", value: "balance 原样保存为 numeric(38,18)；界面裁掉末尾无意义的 0 后展示，原始字符串在额度明细里单独给出" },
      { label: "货币符号", value: "只有 balance_unit 明确为货币（usd/usdt/usdc 等）时才使用货币符号，provider_unit 等未知单位只追加单位文案" },
      { label: "当日 CU", value: "cu_day_limit 为空或 0 时显示「未设置日上限」，不计算剩余量" },
      { label: "管理同步", value: "Statistics / Keys Token 缺失时标记「管理同步未配置」，仅凭 provider_descriptors.status 不能证明 dRPC 可用" },
      { label: "性能档位", value: "provider_performance_profiles；PATCH 需 If-Match + 原因，TURBO 或提高硬上限需 Step-up" },
      { label: "密钥分类", value: "runtime_api_key / statistics_api_token / keys_api_token 各自独立轮换，明文永不回显" },
    ]);
  };

  const runTest = async () => {
    if (!provider) return;
    if (!requirePermission("admin:provider:read")) return;
    await command.run({
      path: `/api/admin/v1/providers/${encodeURIComponent(provider.key)}/commands/test`,
      requireReason: false,
      confirmTitle: `测试连接 · ${provider.display_name || provider.key}`,
      confirmBody: "执行一次 Provider.Health 探针，结果会写入 last_test_result 并记录一条 provider_usage。",
      confirmLabel: "执行测试",
      successTitle: "连接测试完成",
      successDetails: (data) => [
        { label: "探测方法", value: text(data.method) },
        {
          label: "结果",
          value: <StatusBadge tone={data.ok === true ? "ok" : "danger"}>{data.ok === true ? "ok" : "failed"}</StatusBadge>,
        },
        { label: "延迟", value: fmtMillis(data.latency_ms) },
        { label: "运行状态", value: text(data.state) },
        { label: "错误类", value: text(data.error_class) },
        { label: "错误", value: text(data.error) },
        { label: "当前并发", value: text(data.current_concurrency) },
        { label: "Trace", value: <span className="mono">{text(data.trace_id)}</span> },
        { label: "检查时间", value: fmtDateTime(data.checked_at) },
      ],
      onDone: () => resource.reload(),
    });
  };

  const connect = async () => {
    if (!provider) return;
    if (!requirePermission("admin:provider:read")) return;
    await command.run({
      path: `/api/admin/v1/providers/${encodeURIComponent(provider.key)}/commands/connect`,
      confirmTitle: `连接 Provider · ${provider.display_name || provider.key}`,
      confirmBody: "连接后路由策略可以重新把新请求路由到该 Provider。",
      confirmLabel: "连接",
      successTitle: "Provider 已连接",
      successDetails: (data) => [
        { label: "状态", value: <StatusBadge tone={providerTone(String(data.status ?? ""))}>{text(data.status)}</StatusBadge> },
        { label: "revision", value: text(data.revision) },
        { label: "更新时间", value: fmtDateTime(data.updated_at) },
      ],
      onDone: () => resource.reload(),
    });
  };

  /**
   * 断开：先按普通命令提交；服务端返回 403 STEP_UP_REQUIRED（存在运行中任务）时，
   * 用 useAdminCommand 的 requireStepUp 重新提交，避免无条件索要密码。
   */
  const disconnect = async () => {
    if (!provider) return;
    if (!requirePermission("admin:provider:read")) return;
    const path = `/api/admin/v1/providers/${encodeURIComponent(provider.key)}/commands/disconnect`;
    const confirmed = await dialog.confirm({
      kind: "warning",
      title: `断开 Provider · ${provider.display_name || provider.key}`,
      body: "断开后新请求不再路由到该 Provider；若存在运行中任务，服务端会要求二次身份确认。",
      details: [
        { label: "当前状态", value: <StatusBadge tone={providerTone(provider.status)}>{provider.status}</StatusBadge> },
        { label: "声明链", value: listText(provider.chains) },
        { label: "revision", value: text(provider.revision) },
      ],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "继续", result: "ok", variant: "danger" },
      ],
    });
    if (confirmed !== "ok") return;

    const reason = await askText({
      kind: "warning",
      title: "填写断开原因",
      body: "原因会写入审计日志，无法为空。",
      input: { label: "原因", placeholder: "例如：上游持续 5xx，按预案摘除", pattern: /^.{1,500}$/, patternHint: "1-500 个字符" },
      submitLabel: "提交",
    });
    if (reason.cancelled || reason.value === "") return;

    try {
      const response = await client.post<Record<string, unknown>>(
        path,
        { reason: reason.value },
        { idempotencyKey: newIdempotencyKey() },
      );
      const data = response.data ?? {};
      dialog.notify("success", "Provider 已断开", null, impactDetails(data.impact));
      resource.reload();
    } catch (error) {
      if (error instanceof ApiError && error.code === "STEP_UP_REQUIRED") {
        await command.run({
          path,
          body: { reason: reason.value },
          requireReason: false,
          requireStepUp: true,
          variant: "danger",
          confirmTitle: "二次身份确认后断开",
          confirmBody: "该 Provider 上仍有运行中任务，需要重新确认当前账号身份。",
          confirmLabel: "确认身份并断开",
          successTitle: "Provider 已断开",
          successDetails: (data) => impactDetails(data.impact),
          onDone: () => resource.reload(),
        });
        return;
      }
      dialog.notify(
        "error",
        "断开失败",
        error instanceof ApiError ? error.userMessage : "请稍后重试",
        apiErrorDetails(error),
      );
    }
  };

  const rotateSecret = async () => {
    if (!provider) return;
    if (!requirePermission("admin:provider:read")) return;
    const ref = await askText({
      kind: "warning",
      title: "轮换 Provider 密钥 · 第 1 步",
      body: "只接受密钥引用名（例如 KMS / 环境变量引用）。服务端只登记引用名、前 6 位与 SHA-256 指纹，明文既不接收也不回显。",
      input: { label: "secret_ref（引用名，不接受明文）", placeholder: "env:ANKR_API_KEY", pattern: /^.{3,200}$/, patternHint: "3-200 个字符" },
      submitLabel: "下一步",
    });
    if (ref.cancelled || ref.value === "") return;
    const prefix = await askText({
      kind: "warning",
      title: "轮换 Provider 密钥 · 第 2 步",
      body: "前缀只用于界面识别，最多 6 位；留空表示不记录前缀。",
      input: { label: "secret_prefix（可选）", pattern: /^.{0,6}$/, patternHint: "最多 6 个字符" },
      submitLabel: "继续",
    });
    if (prefix.cancelled) return;
    await command.run({
      path: `/api/admin/v1/providers/${encodeURIComponent(provider.key)}/commands/rotate-secret`,
      body: { secret_ref: ref.value, secret_prefix: prefix.value },
      variant: "danger",
      requireStepUp: true,
      confirmTitle: `确认轮换密钥 · ${provider.display_name || provider.key}`,
      confirmBody: "当前 active 密钥会被置为 retiring，新引用立即生效；旧密钥需在密钥库侧自行撤销。",
      confirmLabel: "轮换",
      successTitle: "密钥已轮换",
      successDetails: (data) => [
        { label: "Provider", value: text(data.provider) },
        { label: "新密钥记录", value: <span className="mono">{text(data.rotated_secret_id)}</span> },
        { label: "指纹", value: <span className="mono">{text(data.fingerprint)}</span> },
        { label: "前缀", value: <span className="mono">{text(data.prefix)}</span> },
        { label: "明文回显", value: data.plaintext_returned === true ? "是" : "否（永不回显）" },
        { label: "密钥条数", value: Array.isArray(data.secrets) ? String(data.secrets.length) : "—" },
      ],
      onDone: () => {
        resource.reload();
        secretsResource.reload();
      },
    });
  };

  /** askStepUp 用 dialog.prompt 收集当前密码；取消返回 null。 */
  const askStepUp = async (title: string, body: string): Promise<string | null> => {
    const raw = await dialog.prompt({
      kind: "warning",
      title,
      body,
      input: {
        label: "当前账号密码",
        placeholder: "请输入当前账号密码",
        pattern: /^.{1,200}$/,
        patternHint: "请输入当前账号密码",
      },
      submitLabel: "确认身份并提交",
    });
    return dialogInputValue(raw);
  };

  /** 测试连接：分别探测 Data API 与 Solana JSON-RPC 两个通道。 */
  const probeChannels = async () => {
    if (!provider) return;
    if (!requirePermission("admin:provider:read")) return;
    await command.run({
      path: "/api/admin/v1/providers/" + encodeURIComponent(provider.key) + "/commands/probe-channels",
      requireReason: false,
      confirmTitle: "测试连接 · " + (provider.display_name || provider.key),
      confirmBody: "分别执行 Data API 低成本能力检查与 Solana JSON-RPC getHealth，结果按通道展示。",
      confirmLabel: "执行测试",
      successTitle: "通道测试完成",
      successDetails: (data) => [
        { label: "Provider", value: <span className="mono">{text(data.provider ?? provider.key)}</span> },
        {
          label: "总体结果",
          value:
            data.ok === true ? (
              <StatusBadge tone="ok">ok</StatusBadge>
            ) : data.ok === false ? (
              <StatusBadge tone="danger">failed</StatusBadge>
            ) : (
              "—"
            ),
        },
        ...channelProbeDetails(data),
        { label: "检查时间", value: fmtDateTime(data.checked_at) },
        { label: "Trace", value: <span className="mono">{text(data.trace_id)}</span> },
      ],
      onDone: () => {
        resource.reload();
        quotaResource.reload();
      },
    });
  };

  /** 同步额度：Statistics API + Keys API；60 秒内重复调用由服务端返回上次快照。 */
  const syncQuota = async () => {
    if (!provider) return;
    if (!requirePermission("admin:provider:update")) return;
    await command.run({
      path: "/api/admin/v1/providers/" + encodeURIComponent(provider.key) + "/commands/sync-quota",
      requireReason: false,
      confirmTitle: "同步额度 · " + (provider.display_name || provider.key),
      confirmBody: "调用 Statistics API 与 Keys API 写入一条额度快照；60 秒内重复调用返回最近一次快照。",
      confirmLabel: "同步额度",
      successTitle: "额度同步已完成",
      successDetails: (data) => {
        const snapshot = toQuota(quotaPayloadOf(data));
        return [
          {
            label: "结果",
            value: isCachedSnapshot(data) ? (
              <StatusBadge tone="info">已返回最近一次快照</StatusBadge>
            ) : (
              <StatusBadge tone="ok">已写入新快照</StatusBadge>
            ),
          },
          { label: "账户层级", value: snapshot.account_tier ?? "—" },
          { label: "账户余额", value: fmtBalance(snapshot.balance, snapshot.balance_unit) },
          { label: "当日 CU（已用/上限）", value: cuUsageText(snapshot) },
          { label: "剩余 CU", value: cuRemainingText(snapshot) },
          { label: "剩余百分比", value: cuRemainingPercentText(snapshot) },
          { label: "同步时间", value: quotaSyncedAtText(snapshot) },
          { label: "同步状态", value: quotaSourceText(snapshot) },
          { label: "来源", value: snapshot.source ?? "—" },
          { label: "Trace", value: <span className="mono">{text(data.trace_id)}</span> },
        ];
      },
      onDone: () => {
        quotaResource.reload();
        quotaHistoryResource.reload();
        resource.reload();
      },
    });
  };

  /** 编辑档位：If-Match + 修改原因；TURBO 或提高硬上限先做 Step-up，403 时再补一次。 */
  const editProfile = async (profile: PerformanceProfile) => {
    if (!provider) return;
    if (!requirePermission("admin:performance:update")) return;
    const channel = profileChannel(profile);
    const mode = String(profile.mode ?? "").toLowerCase();
    const current: Record<string, number> = {};
    for (const field of PROFILE_PARAM_FIELDS) current[field.name] = num(profile[field.name]) ?? 0;
    const resolved = await dialog.open({
      kind: "confirm",
      title: "编辑档位 · " + channelLabel(channel) + " / " + mode.toUpperCase(),
      body: "提交会携带该档位当前 revision；需要填写修改原因，TURBO 或提高硬上限需要二次身份确认。",
      details: [
        { label: "链 chain", value: <span className="mono">{profile.chain}</span> },
        { label: "数据集 dataset", value: <span className="mono">{profile.dataset}</span> },
        { label: "操作类 operation_class", value: <span className="mono">{profile.operation_class}</span> },
        { label: "revision", value: String(profile.revision ?? "—") },
        { label: "验证来源", value: profile.verification_source ?? "—" },
        { label: "验证时间", value: fmtDateTime(profile.verified_at) },
      ],
      fields: PROFILE_PARAM_FIELDS.map((field) => ({
        name: field.name,
        label: field.label,
        defaultValue: String(current[field.name]),
        pattern: field.integer ? /^[0-9]+$/ : /^[0-9]+([.][0-9]{1,6})?$/,
        patternHint: field.integer ? "非负整数" : "非负数字，最多 6 位小数",
      })),
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "下一步", result: "ok", variant: "primary" },
      ],
    });
    const values = dialogFormValues(resolved);
    if (!values) return;
    const body: Record<string, number> = {};
    const changes: Array<{ label: string; value: ReactNode }> = [];
    for (const field of PROFILE_PARAM_FIELDS) {
      const next = num(values[field.name]);
      if (next === null || next < 0) {
        dialog.notify("warning", "参数不合法", null, [{ label: field.label, value: values[field.name] }]);
        return;
      }
      if (next !== current[field.name]) {
        body[field.name] = next;
        changes.push({ label: field.label, value: String(current[field.name]) + " → " + String(next) });
      }
    }
    if (changes.length === 0) {
      dialog.notify("warning", "没有需要保存的变更", null, [
        { label: "档位", value: channelLabel(channel) + " / " + mode.toUpperCase() },
        { label: "revision", value: String(profile.revision ?? "—") },
      ]);
      return;
    }
    const confirmed = await dialog.confirm({
      kind: "warning",
      title: "确认保存档位 · " + channelLabel(channel) + " / " + mode.toUpperCase(),
      body: "保存后新建 Shard 按新参数执行，已固化档位的旧 Shard 不受影响。",
      details: changes,
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "保存", result: "ok", variant: "primary" },
      ],
    });
    if (confirmed !== "ok") return;
    const reason = dialogInputValue(
      await dialog.prompt({
        kind: "warning",
        title: "填写修改原因",
        body: "原因会写入审计日志与档位版本，无法为空。",
        input: {
          label: "原因",
          placeholder: "例如：付费层级压测后提高 Data API RPM",
          pattern: /^.{1,500}$/,
          patternHint: "1-500 个字符",
        },
        submitLabel: "继续",
      }),
    );
    if (!reason) return;
    const payload: Record<string, unknown> = { ...body, reason };
    const raisingHardCeiling = body.hard_ceiling !== undefined && body.hard_ceiling > current.hard_ceiling;
    const needsStepUp = mode === "turbo" || raisingHardCeiling;
    if (needsStepUp) {
      const password = await askStepUp(
        "二次身份确认 · 高风险档位",
        "该档位为 " + mode.toUpperCase() + (raisingHardCeiling ? " 且提高硬上限" : "") + "，需要重新确认当前账号身份。",
      );
      if (password === null) return;
      payload.step_up_password = password;
    }
    const path =
      "/api/admin/v1/providers/" +
      encodeURIComponent(provider.key) +
      "/performance-profiles/" +
      encodeURIComponent(profile.chain) +
      "/" +
      encodeURIComponent(profile.operation_class) +
      "/" +
      encodeURIComponent(profile.mode);
    const submit = (requestBody: Record<string, unknown>) =>
      client.patch<Record<string, unknown>>(path, requestBody, {
        ifMatch: String(profile.revision ?? ""),
        idempotencyKey: newIdempotencyKey(),
      });
    try {
      const response = await submit(payload);
      dialog.notify("success", "档位已保存", null, profileSaveDetails(response.data ?? {}, profile, changes));
      profilesResource.reload();
      resource.reload();
    } catch (error) {
      if (error instanceof ApiError && error.code === "STEP_UP_REQUIRED") {
        const retryPassword = await askStepUp("二次身份确认", "服务端要求重新确认当前账号身份后重试。");
        if (retryPassword === null) return;
        try {
          const retry = await submit({ ...payload, step_up_password: retryPassword });
          dialog.notify("success", "档位已保存", null, profileSaveDetails(retry.data ?? {}, profile, changes));
          profilesResource.reload();
          resource.reload();
        } catch (retryError) {
          dialog.notify(
            "error",
            "保存档位失败",
            retryError instanceof ApiError ? retryError.userMessage : "请稍后重试",
            apiErrorDetails(retryError),
          );
        }
        return;
      }
      dialog.notify("error", "保存档位失败", error instanceof ApiError ? error.userMessage : "请稍后重试", apiErrorDetails(error));
    }
  };

  /**
   * 设置 / 轮换某一类密钥：Dialog 内二选一（粘贴明文 / Secret Ref），提交前 Step-up。
   * 明文只存在于本次请求体，既不进 React state 也不回显。
   */
  const editSecret = async (kind: SecretKind) => {
    if (!provider) return;
    if (!requirePermission("admin:provider:update")) return;
    const meta = SECRET_KIND_META[kind];
    const existing = secretKindRows.find((row) => row.kind === kind)?.summary ?? null;
    for (;;) {
      const resolved = await dialog.open({
        kind: "confirm",
        title: "设置 / 轮换密钥 · " + meta.label,
        body: "方式 A（推荐）：直接粘贴密钥值，明文只在本次请求体中出现一次；方式 B：填写受控引用名。两种方式二选一。",
        details: [
          { label: "secret_kind", value: <span className="mono">{kind}</span> },
          { label: "用途", value: meta.purpose },
          { label: "推荐引用", value: <span className="mono">{meta.recommendedRef}</span> },
          { label: "当前引用名（掩码）", value: existing?.secret_ref || "未配置" },
          { label: "当前状态", value: existing?.status || "未配置" },
        ],
        fields: [
          {
            name: "value",
            label: "方式 A：粘贴密钥值（推荐）",
            type: "password",
            required: false,
            placeholder: "明文只提交一次，页面不会回显",
            autoComplete: "off",
          },
          {
            name: "secret_ref",
            label: "方式 B：Secret Ref（env://NAME）",
            required: false,
            placeholder: meta.recommendedRef,
            pattern: /^(env:[/]{2}[A-Za-z0-9_.-]{1,120})?$/,
            patternHint: "env://NAME 或留空",
          },
          {
            name: "ack",
            label: "我已确认：服务端写入受管密钥文件（0600），页面不会回显明文",
            type: "checkbox",
          },
        ],
        actions: [
          { label: "取消", result: "", variant: "default" },
          { label: "继续提交", result: "ok", variant: "primary" },
        ],
      });
      const values = dialogFormValues(resolved);
      if (!values) return;
      const secretRef = (values.secret_ref ?? "").trim();
      const value = values.value ?? "";
      if (value !== "" && secretRef !== "") {
        dialog.notify("warning", "两种方式只能选一种", null, [
          { label: "方式 A", value: "已填写密钥值" },
          { label: "方式 B", value: secretRef },
        ]);
        continue;
      }
      if (value === "" && secretRef === "") {
        dialog.notify("warning", "请选择一种提交方式", null, [
          { label: "方式 A", value: "粘贴密钥值" },
          { label: "方式 B", value: "填写 Secret Ref（env://NAME）" },
        ]);
        continue;
      }
      const password = await askStepUp(
        "二次身份确认 · " + meta.label,
        "写入或轮换密钥需要重新确认当前账号身份。密码只用于本次校验，不会写入日志。",
      );
      if (password === null) return;
      const requestBody: Record<string, unknown> = secretRef !== "" ? { secret_ref: secretRef } : { value };
      requestBody.step_up_password = password;
      const path = "/api/admin/v1/providers/" + encodeURIComponent(provider.key) + "/secrets/" + encodeURIComponent(kind);
      const submit = (body: Record<string, unknown>) =>
        client.request<Record<string, unknown>>(path, {
          method: "PUT",
          body,
          idempotencyKey: newIdempotencyKey(),
        });
      const successDetails = (data: Record<string, unknown>): Array<{ label: string; value: ReactNode }> => [
        { label: "密钥类型", value: secretKindLabel(typeof data.secret_kind === "string" ? data.secret_kind : kind) },
        { label: "引用名（掩码）", value: <span className="mono">{text(data.ref ?? data.secret_ref)}</span> },
        { label: "前缀", value: <span className="mono">{text(data.prefix)}</span> },
        { label: "指纹", value: <span className="mono">{text(data.fingerprint)}</span> },
        {
          label: "状态",
          value: (
            <StatusBadge tone={data.configured === true ? "ok" : "neutral"}>
              {data.configured === true ? "已配置" : "未配置"}
            </StatusBadge>
          ),
        },
        { label: "明文回显", value: "否（永不回显）" },
      ];
      try {
        const response = await submit(requestBody);
        dialog.notify("success", "已保存，页面不会回显明文", null, successDetails(response.data ?? {}));
        resource.reload();
        secretsResource.reload();
        return;
      } catch (error) {
        if (error instanceof ApiError && error.code === "STEP_UP_REQUIRED") {
          const retryPassword = await askStepUp("二次身份确认", "服务端要求重新确认当前账号身份后重试。");
          if (retryPassword === null) return;
          try {
            const retry = await submit({ ...requestBody, step_up_password: retryPassword });
            dialog.notify("success", "已保存，页面不会回显明文", null, successDetails(retry.data ?? {}));
            resource.reload();
            secretsResource.reload();
          } catch (retryError) {
            dialog.notify(
              "error",
              "保存密钥失败",
              retryError instanceof ApiError ? retryError.userMessage : "请稍后重试",
              apiErrorDetails(retryError),
            );
          }
          return;
        }
        dialog.notify("error", "保存密钥失败", error instanceof ApiError ? error.userMessage : "请稍后重试", apiErrorDetails(error));
        return;
      }
    }
  };

  const currentForm = form;
  const changedFields = useMemo(() => {
    if (!provider || !currentForm) return {} as Record<string, unknown>;
    const patchBody: Record<string, unknown> = {};
    if (currentForm.display_name !== (provider.display_name ?? "")) patchBody.display_name = currentForm.display_name;
    if (currentForm.endpoint !== (provider.endpoint ?? "")) patchBody.endpoint = currentForm.endpoint;
    if (Number(currentForm.concurrency) !== Number(provider.concurrency ?? 0)) patchBody.concurrency = Number(currentForm.concurrency);
    if (Number(currentForm.rate_limit_per_minute) !== Number(provider.rate_limit_per_minute ?? 0))
      patchBody.rate_limit_per_minute = Number(currentForm.rate_limit_per_minute);
    if (Number(currentForm.priority) !== Number(provider.priority ?? 0)) patchBody.priority = Number(currentForm.priority);
    if (currentForm.status !== (provider.status ?? "")) patchBody.status = currentForm.status;
    return patchBody;
  }, [provider, currentForm]);

  const saveConfig = async () => {
    if (!provider || !currentForm) return;
    if (!requirePermission("admin:provider:update")) return;
    if (Object.keys(changedFields).length === 0) {
      dialog.notify("warning", "没有需要保存的变更", null, [{ label: "revision", value: text(provider.revision) }]);
      return;
    }
    await patch.run({
      path: `/api/admin/v1/providers/${encodeURIComponent(provider.key)}`,
      ifMatch: provider.revision,
      body: changedFields,
      confirmTitle: `保存 Provider 配置 · ${provider.display_name || provider.key}`,
      confirmBody: "配置会立即影响路由选择与限流判定；并发与限流为声明值，运行时生效需随后端刷新。",
      details: Object.entries(changedFields).map(([key, value]) => ({ label: key, value: text(value) })),
      successTitle: "Provider 配置已保存",
      successDetails: [
        { label: "Provider", value: <span className="mono">{provider.key}</span> },
        { label: "提交字段", value: Object.keys(changedFields).join("、") },
        { label: "原 revision", value: text(provider.revision) },
      ],
      onDone: () => resource.reload(),
    });
  };

  if (resource.loading && !provider) {
    return (
      <>
        <PageHeader title="Provider 详情" subtitle={<span className="admin-detail-id mono">{providerKey}</span>} />
        <Card title="正在加载">
          <SkeletonRows rows={6} columns={4} />
        </Card>
      </>
    );
  }

  if (!provider) {
    return (
      <>
        <PageHeader title="Provider 详情" subtitle={<span className="admin-detail-id mono">{providerKey}</span>} />
        <Card>
          <ErrorState title="Provider 数据加载失败" message={resource.error ?? "对象不存在或无权查看"} onRetry={resource.reload} />
        </Card>
      </>
    );
  }

  const capabilities = provider.capabilities ?? [];
  const channels = health?.channels;
  const remainingPercent = cuRemainingPercent(quota);
  const quotaTone: StatusTone =
    remainingPercent === null ? "neutral" : remainingPercent <= 0.05 ? "danger" : remainingPercent <= 0.2 ? "warn" : "ok";
  const numberText = (value: number | string | null | undefined): string => {
    const parsed = num(value);
    return parsed === null ? "—" : fmtNumber(parsed);
  };
  const runtimeKeyReady = runtimeKeyConfigured(provider);
  const managementReady = managementSecretsConfigured(provider);
  const profileChannels = Array.from(new Set(profiles.map((profile) => profileChannel(profile))));

  const profileColumns: Array<DataTableColumn<PerformanceProfile>> = [
    {
      key: "mode",
      header: "模式",
      width: "100px",
      render: (row) => <StatusBadge tone={profileModeTone(row.mode)}>{String(row.mode ?? "").toUpperCase()}</StatusBadge>,
    },
    { key: "dataset", header: "数据集", width: "150px", render: (row) => <span className="mono">{row.dataset}</span> },
    {
      key: "operation",
      header: "操作类",
      width: "150px",
      render: (row) => <span className="mono">{row.operation_class}</span>,
    },
    { key: "shard", header: "Shard 并发", width: "110px", align: "right", render: (row) => <span className="num">{numberText(row.shard_concurrency)}</span> },
    { key: "request", header: "请求并发", width: "110px", align: "right", render: (row) => <span className="num">{numberText(row.request_concurrency)}</span> },
    { key: "rpm", header: "RPM", width: "100px", align: "right", render: (row) => <span className="num">{numberText(row.requests_per_minute)}</span> },
    { key: "rps", header: "RPS", width: "100px", align: "right", render: (row) => <span className="num">{numberText(row.requests_per_second)}</span> },
    { key: "cu", header: "CU/分钟", width: "120px", align: "right", render: (row) => <span className="num">{numberText(row.cu_per_minute)}</span> },
    {
      key: "page",
      header: "分页 / 批次",
      width: "130px",
      align: "right",
      render: (row) => <span className="num">{numberText(row.page_size) + " / " + numberText(row.batch_size)}</span>,
    },
    { key: "weight", header: "请求权重", width: "110px", align: "right", render: (row) => <span className="num">{numberText(row.request_weight)}</span> },
    { key: "timeout", header: "超时（秒）", width: "110px", align: "right", render: (row) => <span className="num">{numberText(row.request_timeout_seconds)}</span> },
    { key: "ceiling", header: "硬上限", width: "100px", align: "right", render: (row) => <span className="num">{numberText(row.hard_ceiling)}</span> },
    { key: "revision", header: "revision", width: "100px", align: "right", render: (row) => <span className="num">{String(row.revision ?? "—")}</span> },
    {
      key: "verified",
      header: "验证",
      width: "200px",
      render: (row) => (
        <span>
          <span className="mono">{row.verification_source ?? "—"}</span>
          <div className="num" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>
            {fmtDateTime(row.verified_at)}
          </div>
        </span>
      ),
    },
    {
      key: "actions",
      header: "操作",
      width: "110px",
      align: "right",
      render: (row) => (
        <button
          type="button"
          className="btn btn--sm"
          disabled={!can("admin:performance:update") || patch.busy}
          onClick={() => void editProfile(row)}
        >
          编辑
        </button>
      ),
    },
  ];

  const runtimeStateColumns: Array<DataTableColumn<ProviderRuntimeState>> = [
    { key: "chain", header: "链", width: "100px", render: (row) => row.chain || "—" },
    { key: "operation", header: "操作类", width: "160px", render: (row) => <span className="mono">{row.operation_class || "—"}</span> },
    { key: "requested", header: "请求档", width: "100px", render: (row) => <StatusBadge tone="info">{String(row.requested_mode ?? "").toUpperCase() || "—"}</StatusBadge> },
    {
      key: "effective",
      header: "生效档",
      width: "120px",
      render: (row) => (
        <StatusBadge tone={runtimeStateTone(row)}>{String(row.effective_mode ?? "").toUpperCase() || "—"}</StatusBadge>
      ),
    },
    {
      key: "concurrency",
      header: "目标 / 活动并发",
      width: "150px",
      align: "right",
      render: (row) => <span className="num">{numberText(row.target_concurrency) + " / " + numberText(row.active_concurrency)}</span>,
    },
    {
      key: "rate",
      header: "RPM / RPS",
      width: "130px",
      align: "right",
      render: (row) => <span className="num">{numberText(row.requests_per_minute) + " / " + numberText(row.requests_per_second)}</span>,
    },
    { key: "cu", header: "CU/分钟", width: "120px", align: "right", render: (row) => <span className="num">{numberText(row.cu_per_minute)}</span> },
    { key: "reason", header: "降级原因", width: "220px", render: (row) => <span className="mono">{row.degrade_reason || "—"}</span> },
    { key: "updated", header: "更新时间", width: "170px", render: (row) => <span className="num">{fmtDateTime(row.updated_at)}</span> },
  ];

  const healthTab = (
    <>
      <Card title="运行时健康">
        <KeyValueList
          items={[
            {
              label: "状态",
              value: health?.state ? <StatusBadge tone={healthTone(health.state)}>{health.state}</StatusBadge> : "—",
            },
            { label: "最近检查", value: fmtDateTime(health?.last_check) },
            { label: "当前并发", value: fmtNumber(health?.current_concurrency ?? 0) },
            { label: "连续失败", value: fmtNumber(health?.consecutive_failures ?? 0) },
            { label: "近端错误率", value: fmtPercent(health?.recent_error_rate ?? 0, 2) },
            { label: "错误", value: text(health?.error) },
            { label: "来源", value: text(health?.source), mono: true },
            { label: "声明名称", value: text(declared?.name) },
            { label: "声明最大分页", value: text(declared?.max_page_size) },
            {
              label: "声明支持时间范围",
              value: declared?.supports_time_range === true ? "是" : declared?.supports_time_range === false ? "否" : "—",
            },
            { label: "声明数据集", value: listText(declared?.datasets) },
          ]}
        />
      </Card>

      <Card title="最近一次连接测试">
        <KeyValueList
          columns={3}
          items={[
            { label: "测试时间", value: fmtDateTime(provider.last_test_at) },
            { label: "方法", value: text(lastTest.method) },
            {
              label: "结果",
              value:
                lastTest.ok === true ? (
                  <StatusBadge tone="ok">ok</StatusBadge>
                ) : lastTest.ok === false ? (
                  <StatusBadge tone="danger">failed</StatusBadge>
                ) : (
                  "—"
                ),
            },
            { label: "延迟", value: fmtMillis(lastTest.latency_ms) },
            { label: "错误类", value: text(lastTest.error_class) },
            { label: "错误", value: text(lastTest.error) },
            { label: "运行状态", value: text(lastTest.state) },
            { label: "当前并发", value: text(lastTest.current_concurrency) },
            { label: "Trace", value: text(lastTest.trace_id), mono: true },
          ]}
        />
        <div style={{ marginTop: "var(--sp-4)" }}>
          <JsonBlock value={provider.last_test_result} label="last_test_result" />
        </div>
      </Card>
    </>
  );

  const secretTab = (
    <>
      <Card title="当前生效密钥（摘要）">
        <MaskedSecret
          prefix={activeSecret.prefix}
          fingerprint={activeSecret.fingerprint}
          secretRef={activeSecret.secret_ref}
          rotatedAt={fmtDateTime(activeSecret.rotated_at)}
          lastUsedAt={fmtDateTime(activeSecret.last_used_at)}
        />
        <div className="toolbar" style={{ marginTop: "var(--sp-4)" }}>
          <button
            type="button"
            className="btn btn--sm btn--danger"
            disabled={!can("admin:provider:read") || command.busy}
            onClick={() => void rotateSecret()}
          >
            轮换密钥
          </button>
          <button
            type="button"
            className="btn btn--sm"
            disabled={!can("admin:provider:read") || command.busy}
            onClick={() => void runTest()}
          >
            连接测试
          </button>
          <button type="button" className="btn btn--sm" onClick={secretsResource.reload}>
            刷新密钥列表
          </button>
        </div>
      </Card>

      <Card title={`密钥历史（${secretRows.length}）`} flush>
        <DataTable<SecretRow>
          rows={secretRows}
          rowKey={(row) => row.id}
          loading={secretsResource.loading}
          error={secretsResource.error}
          onRetry={secretsResource.reload}
          columns={[
            { key: "ref", header: "引用名（摘要）", render: (row) => <span className="mono">{row.secret_ref}</span> },
            { key: "prefix", header: "前缀", width: "120px", render: (row) => <span className="mono">{row.prefix || "—"}</span> },
            { key: "fingerprint", header: "SHA-256 指纹", width: "200px", render: (row) => <span className="mono">{row.fingerprint}</span> },
            {
              key: "status",
              header: "状态",
              width: "120px",
              render: (row) => <StatusBadge tone={row.status === "active" ? "ok" : statusToneFor(row.status)}>{row.status}</StatusBadge>,
            },
            { key: "note", header: "备注", width: "160px", render: (row) => row.note || "—" },
            { key: "rotated", header: "轮换时间", width: "170px", render: (row) => <span className="num">{fmtDateTime(row.rotated_at)}</span> },
            { key: "used", header: "最后使用", width: "170px", render: (row) => <span className="num">{fmtDateTime(row.last_used_at)}</span> },
            { key: "expires", header: "过期时间", width: "170px", render: (row) => <span className="num">{fmtDateTime(row.expires_at)}</span> },
            { key: "plaintext", header: "明文", width: "100px", render: () => "永不回显" },
          ]}
        />
      </Card>
    </>
  );

  const quotaTab = (
    <>
      <div className="metric-grid">
        <Metric label="账户层级" value={text(quota?.account_tier)} hint="account_tier" />
        <Metric
          label="账户余额"
          value={quota ? fmtBalance(quota.balance, quota.balance_unit) : "—"}
          hint={quota?.balance_unit || "balance_unit"}
        />
        <Metric label="当日已用 CU" value={quota ? numberText(quota.cu_spent_today) : "—"} hint="cu_spent_today" />
        <Metric label="日上限" value={quota ? cuLimitText(quota) : "—"} hint="cu_day_limit" />
        <Metric label="剩余 CU" value={quota ? cuRemainingText(quota) : "—"} hint="cu_remaining_today" tone={quotaTone} />
        <Metric label="剩余百分比" value={quota ? cuRemainingPercentText(quota) : "—"} hint="remaining / limit" tone={quotaTone} />
        <Metric
          label="当前请求并发"
          value={fmtNumber(health?.current_concurrency ?? 0)}
          hint="runtime_health.current_concurrency"
        />
      </div>

      <Card title="通道健康">
        <KeyValueList
          columns={3}
          items={[
            {
              label: "Data API 通道",
              value: <StatusBadge tone={channelTone(channels?.data_api)}>{channelStateText(channels?.data_api)}</StatusBadge>,
            },
            {
              label: "Solana RPC 通道",
              value: <StatusBadge tone={channelTone(channels?.solana_rpc)}>{channelStateText(channels?.solana_rpc)}</StatusBadge>,
            },
            {
              label: "运行状态 runtime_health.state",
              value: health?.state ? <StatusBadge tone={healthTone(health.state)}>{health.state}</StatusBadge> : "—",
            },
            { label: "最近检查 last_check", value: fmtDateTime(health?.last_check) },
            { label: "当前请求并发", value: fmtNumber(health?.current_concurrency ?? 0) },
            { label: "运行时来源", value: text(health?.source), mono: true },
          ]}
        />
      </Card>

      <Card
        title="额度与 Key 状态"
        actions={
          <span className="toolbar">
            <button
              type="button"
              className="btn btn--sm"
              disabled={!can("admin:provider:read") || command.busy}
              onClick={() => void probeChannels()}
            >
              测试连接
            </button>
            <button
              type="button"
              className="btn btn--sm btn--primary"
              disabled={!can("admin:provider:update") || command.busy}
              onClick={() => void syncQuota()}
            >
              同步额度
            </button>
            <button
              type="button"
              className="btn btn--sm"
              onClick={() => {
                quotaResource.reload();
                quotaHistoryResource.reload();
                resource.reload();
              }}
            >
              刷新
            </button>
          </span>
        }
      >
        <KeyValueList columns={3} items={quota ? quotaKeyValueItems(quota) : [{ label: "额度快照", value: "—" }]} />
        <div style={{ marginTop: "var(--sp-4)" }}>
          <KeyValueList
            columns={3}
            items={[
              {
                label: "运行时 API Key",
                value: (
                  <StatusBadge tone={runtimeKeyReady ? "ok" : "warn"}>{runtimeKeyReady ? "已配置" : "未配置"}</StatusBadge>
                ),
              },
              {
                label: "管理同步 Token",
                value:
                  managementReady === true ? (
                    <StatusBadge tone="ok">已配置</StatusBadge>
                  ) : managementReady === false ? (
                    <StatusBadge tone="warn">管理同步未配置</StatusBadge>
                  ) : (
                    "—"
                  ),
              },
              {
                label: "Key 状态",
                value:
                  quota?.key_enabled === true ? (
                    <StatusBadge tone="ok">启用</StatusBadge>
                  ) : quota?.key_enabled === false ? (
                    <StatusBadge tone="danger">停用</StatusBadge>
                  ) : (
                    "—"
                  ),
              },
            ]}
          />
        </div>
      </Card>

      <ChartPanel
        title="额度趋势"
        subtitle={historyPoints.length > 0 ? String(historyPoints.length) + " 个快照" : "quota-history?range=day"}
        dataTime={historyPoints.length > 0 ? fmtDateTime(historyPoints[historyPoints.length - 1]?.at) : "—"}
        empty={historyPoints.length === 0}
        emptyBody="该 Provider 暂无额度快照"
        onExplain={{
          title: "额度趋势口径",
          body: "柱高按窗口内最大当日已用 CU 等比缩放；某次快照的当日已用达到日上限时该柱标红。日上限与精确数值见数据表。",
          details: [
            { label: "数据来源", value: "GET /api/admin/v1/providers/{key}/quota-history?range=day" },
            { label: "时间轴", value: "按 synced_at 升序，从左到右" },
            { label: "缺失值", value: "快照缺少 cu_spent_today 时按 0 处理" },
          ],
        }}
        tableView={
          <DataTable<QuotaHistoryPoint>
            rows={[...historyPoints].reverse()}
            rowKey={(row, index) => (row.at || String(index)) + "-" + String(index)}
            columns={[
              {
                key: "at",
                header: "同步时间",
                width: "180px",
                render: (row) => <span className="num">{fmtDateTime(row.at)}</span>,
              },
              { key: "tier", header: "层级", width: "110px", render: (row) => row.quota.account_tier ?? "—" },
              {
                key: "balance",
                header: "余额",
                width: "170px",
                align: "right",
                render: (row) => <span className="num">{fmtBalance(row.quota.balance, row.quota.balance_unit)}</span>,
              },
              {
                key: "spent",
                header: "当日已用 CU",
                width: "150px",
                align: "right",
                render: (row) => <span className="num">{numberText(row.quota.cu_spent_today)}</span>,
              },
              {
                key: "limit",
                header: "日上限",
                width: "150px",
                align: "right",
                render: (row) => <span className="num">{cuLimitText(row.quota)}</span>,
              },
              {
                key: "remaining",
                header: "剩余 CU",
                width: "150px",
                align: "right",
                render: (row) => <span className="num">{cuRemainingText(row.quota)}</span>,
              },
              {
                key: "rate",
                header: "限流",
                width: "110px",
                align: "right",
                render: (row) => <span className="num">{numberText(row.quota.rate_limit)}</span>,
              },
            ]}
          />
        }
      >
        <QuotaTrendChart points={historyPoints} />
      </ChartPanel>
    </>
  );

  const profilesTab = (
    <>
      {profileChannels.map((channel) => (
        <Card key={channel} title={"性能档位 · " + channelLabel(channel)} flush>
          <DataTable<PerformanceProfile>
            rows={profiles.filter((profile) => profileChannel(profile) === channel)}
            rowKey={(row) => [row.chain, row.dataset, row.operation_class, row.mode].join("/")}
            loading={profilesResource.loading}
            onRetry={profilesResource.reload}
            columns={profileColumns}
          />
        </Card>
      ))}
      {profileChannels.length === 0 ? (
        <Card title="性能档位" flush>
          <DataTable<PerformanceProfile>
            rows={[]}
            rowKey={(row) => [row.chain, row.operation_class, row.mode].join("/")}
            loading={profilesResource.loading}
            onRetry={profilesResource.reload}
            columns={profileColumns}
          />
        </Card>
      ) : null}
      <Card title={"运行时档位（" + runtimeStates.length + "）"} flush>
        <DataTable<ProviderRuntimeState>
          rows={runtimeStates}
          rowKey={(row, index) => [row.chain, row.operation_class, String(index)].join("/")}
          loading={runtimeStatesResource.loading}
          onRetry={runtimeStatesResource.reload}
          columns={runtimeStateColumns}
        />
      </Card>
    </>
  );

  const keysTab = (
    <Card title="密钥类型" flush>
      <DataTable<SecretKindRow>
        rows={secretKindRows}
        rowKey={(row) => row.kind}
        columns={[
          {
            key: "kind",
            header: "密钥类型",
            width: "220px",
            render: (row) => (
              <span>
                <strong>{secretKindLabel(row.kind)}</strong>
                <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>
                  {row.kind}
                </div>
              </span>
            ),
          },
          {
            key: "ref",
            header: "引用名（掩码）",
            render: (row) => <span className="mono">{row.summary?.secret_ref || "未配置"}</span>,
          },
          {
            key: "prefix",
            header: "前缀",
            width: "140px",
            render: (row) => <span className="mono">{row.summary?.prefix ? row.summary.prefix + "••••••" : "—"}</span>,
          },
          {
            key: "fingerprint",
            header: "SHA-256 指纹",
            width: "200px",
            render: (row) => <span className="mono">{row.summary?.fingerprint ?? "—"}</span>,
          },
          {
            key: "status",
            header: "状态",
            width: "120px",
            render: (row) => (
              <StatusBadge tone={row.configured ? "ok" : "warn"}>
                {row.configured ? row.summary?.status ?? "active" : "未配置"}
              </StatusBadge>
            ),
          },
          {
            key: "rotated",
            header: "轮换时间",
            width: "170px",
            render: (row) => <span className="num">{fmtDateTime(row.summary?.rotated_at)}</span>,
          },
          {
            key: "used",
            header: "最后使用",
            width: "170px",
            render: (row) => <span className="num">{fmtDateTime(row.summary?.last_used_at)}</span>,
          },
          {
            key: "actions",
            header: "操作",
            width: "170px",
            align: "right",
            render: (row) => (
              <span className="toolbar" style={{ justifyContent: "flex-end" }}>
                <button
                  type="button"
                  className="btn btn--sm"
                  style={{ whiteSpace: "nowrap" }}
                  disabled={!can("admin:provider:update")}
                  onClick={() => void editSecret(row.kind)}
                >
                  {row.configured ? "轮换" : "设置"}
                </button>
              </span>
            ),
          },
        ]}
      />
    </Card>
  );

  const capabilitiesTab = (
    <>
      <Card title={`能力声明（${capabilities.length}）`}>
        <div className="toolbar">
          {capabilities.map((capability, index) => {
            const label =
              typeof capability === "string"
                ? capability
                : capability && typeof capability === "object" && "name" in (capability as Record<string, unknown>)
                  ? text((capability as Record<string, unknown>).name)
                  : JSON.stringify(capability);
            return (
              <StatusBadge key={index} tone="info">
                {label}
              </StatusBadge>
            );
          })}
        </div>
      </Card>

      <Card title="能力明细">
        <JsonBlock value={provider.capabilities} label="capabilities" />
        <div style={{ marginTop: "var(--sp-3)" }}>
          <JsonBlock value={provider.capability_drift} label="capability_drift" />
        </div>
        <div style={{ marginTop: "var(--sp-3)" }}>
          <JsonBlock value={declared} label="declared_capabilities" />
        </div>
      </Card>

      <Card title="覆盖范围">
        <KeyValueList
          items={[
            { label: "链", value: listText(provider.chains) },
            { label: "数据集", value: listText(provider.datasets) },
            { label: "类型", value: provider.kind },
            { label: "端点", value: provider.endpoint, mono: true },
          ]}
        />
      </Card>
    </>
  );

  const requestsTab = (
    <>
      <div className="metric-grid">
        <Metric label="24h 调用" value={fmtNumber(usage.calls_24h ?? 0)} />
        <Metric label="24h 错误" value={fmtNumber(usage.errors_24h ?? 0)} tone={(usage.errors_24h ?? 0) > 0 ? "warn" : "ok"} />
        <Metric label="P95 延迟" value={fmtMillis(usage.p95_ms)} />
        <Metric label="24h 错误率" value={fmtPercent(usage.error_rate_24h ?? 0, 2)} />
      </div>
      <Card title="请求用量">
        <KeyValueList
          items={[
            { label: "窗口", value: text(usage.window) },
            { label: "来源", value: text(usage.source), mono: true },
            { label: "Provider", value: <EntityLink value={provider.key} copyable /> },
            { label: "端点", value: provider.endpoint, mono: true },
            { label: "声明并发", value: fmtNumber(provider.concurrency) },
            { label: "声明限流", value: `${fmtNumber(provider.rate_limit_per_minute)} / 分钟` },
            { label: "运行时当前并发", value: fmtNumber(health?.current_concurrency ?? 0) },
            { label: "运行时最近检查", value: fmtDateTime(health?.last_check) },
          ]}
        />
      </Card>
    </>
  );

  const errorsTab = (
    <>
      <div className="metric-grid">
        <Metric label="24h 错误数" value={fmtNumber(usage.errors_24h ?? 0)} tone={(usage.errors_24h ?? 0) > 0 ? "danger" : "ok"} />
        <Metric label="24h 错误率" value={fmtPercent(usage.error_rate_24h ?? 0, 2)} tone={(usage.error_rate_24h ?? 0) > 0 ? "warn" : "ok"} />
        <Metric label="近端错误率" value={fmtPercent(health?.recent_error_rate ?? 0, 2)} />
        <Metric label="连续失败" value={fmtNumber(health?.consecutive_failures ?? 0)} tone={(health?.consecutive_failures ?? 0) > 0 ? "warn" : "ok"} />
      </div>
      <Card title="错误来源">
        <KeyValueList
          items={[
            { label: "运行时错误", value: text(health?.error) },
            { label: "运行时状态", value: health?.state ? <StatusBadge tone={healthTone(health.state)}>{health.state}</StatusBadge> : "—" },
            { label: "最近测试错误类", value: text(lastTest.error_class) },
            { label: "最近测试错误", value: text(lastTest.error) },
            { label: "最近测试结果", value: lastTest.ok === true ? "ok" : lastTest.ok === false ? "failed" : "—" },
            { label: "最近测试 Trace", value: text(lastTest.trace_id), mono: true },
            { label: "描述符状态", value: <StatusBadge tone={providerTone(provider.status)}>{provider.status}</StatusBadge> },
            { label: "更新时间", value: fmtDateTime(provider.updated_at) },
          ]}
        />
        <div style={{ marginTop: "var(--sp-4)" }}>
          <JsonBlock value={provider.last_test_result} label="last_test_result" />
        </div>
      </Card>
    </>
  );

  const costTab = (
    <>
      <div className="metric-grid">
        <Metric label="24h 成本" value={fmtUSD(usage.cost_usd_24h)} hint="estimated_cost_usd" />
        <Metric label="24h Credits" value={fmtNumber(usage.credits_24h ?? 0)} />
        <Metric label="24h 调用" value={fmtNumber(usage.calls_24h ?? 0)} />
        <Metric label="单次均价" value={unitCost(usage)} hint="cost_usd_24h / calls_24h" />
      </div>
      <Card title="成本明细">
        <KeyValueList
          items={[
            { label: "窗口", value: text(usage.window) },
            { label: "来源", value: text(usage.source), mono: true },
            { label: "金额口径", value: "decimal-string" },
            { label: "Credits", value: fmtNumber(usage.credits_24h ?? 0) },
            { label: "原始成本字段", value: text(usage.cost_usd_24h), mono: true },
          ]}
        />
      </Card>
    </>
  );

  const configTab = currentForm ? (
    <>
      <Card
        title={`配置 · revision ${fmtNumber(provider.revision)}`}
        actions={
          <button
            type="button"
            className="btn btn--sm btn--primary"
            disabled={!can("admin:provider:update") || patch.busy}
            onClick={() => void saveConfig()}
          >
            保存配置
          </button>
        }
      >
        <div style={{ display: "flex", gap: "var(--sp-4)", flexWrap: "wrap" }}>
          <TextInput
            id="provider-display-name"
            label="显示名称 display_name"
            value={currentForm.display_name}
            onChange={(value) => setForm({ ...currentForm, display_name: value })}
          />
          <TextInput
            id="provider-endpoint"
            label="端点 endpoint"
            value={currentForm.endpoint}
            onChange={(value) => setForm({ ...currentForm, endpoint: value })}
          />
        </div>
        <div style={{ display: "flex", gap: "var(--sp-4)", flexWrap: "wrap", marginTop: "var(--sp-4)" }}>
          <NumberInput
            id="provider-concurrency"
            label="并发 concurrency"
            min={1}
            max={100000}
            value={currentForm.concurrency}
            onChange={(value) => setForm({ ...currentForm, concurrency: value })}
          />
          <NumberInput
            id="provider-rate-limit"
            label="每分钟限流 rate_limit_per_minute"
            min={1}
            max={10000000}
            value={currentForm.rate_limit_per_minute}
            onChange={(value) => setForm({ ...currentForm, rate_limit_per_minute: value })}
          />
          <NumberInput
            id="provider-priority"
            label="优先级 priority"
            min={0}
            max={10000}
            value={currentForm.priority}
            onChange={(value) => setForm({ ...currentForm, priority: value })}
          />
          <SelectInput
            id="provider-status"
            label="状态 status"
            value={currentForm.status}
            onChange={(value) => setForm({ ...currentForm, status: value })}
            options={STATUS_OPTIONS}
          />
        </div>
        <div style={{ marginTop: "var(--sp-4)" }}>
          <KeyValueList
            columns={3}
            items={[
              { label: "If-Match 版本", value: fmtNumber(provider.revision) },
              { label: "待提交字段", value: Object.keys(changedFields).length > 0 ? Object.keys(changedFields).join("、") : "无" },
              { label: "更新时间", value: fmtDateTime(provider.updated_at) },
            ]}
          />
        </div>
      </Card>

      <Card title="连接控制">
        <div className="toolbar">
          <button
            type="button"
            className="btn btn--sm"
            disabled={!can("admin:provider:read") || command.busy}
            onClick={() => void connect()}
          >
            连接
          </button>
          <button
            type="button"
            className="btn btn--sm btn--danger"
            disabled={!can("admin:provider:read") || command.busy}
            onClick={() => void disconnect()}
          >
            断开
          </button>
          <button
            type="button"
            className="btn btn--sm"
            disabled={!can("admin:provider:read") || command.busy}
            onClick={() => void runTest()}
          >
            连接测试
          </button>
        </div>
      </Card>
    </>
  ) : (
    <Card title="配置">
      <SkeletonRows rows={3} columns={3} />
    </Card>
  );

  return (
    <>
      <PageHeader
        title={provider.display_name || provider.key}
        subtitle={
          <span className="toolbar admin-detail-id">
            <EntityLink value={provider.key} copyable />
            <StatusBadge tone="info">{provider.kind}</StatusBadge>
            <StatusBadge tone={providerTone(provider.status)}>{provider.status}</StatusBadge>
            <span className="num">revision {fmtNumber(provider.revision)}</span>
          </span>
        }
        actions={
          <>
            <button type="button" className="btn btn--sm" onClick={() => navigate("/providers")}>
              返回列表
            </button>
            <button type="button" className="btn btn--sm" onClick={resource.reload}>
              刷新
            </button>
            <button
              type="button"
              className="btn btn--sm"
              disabled={!can("admin:provider:read") || command.busy}
              onClick={() => void runTest()}
            >
              连接测试
            </button>
            <button type="button" className="btn btn--sm" onClick={showScope}>
              口径
            </button>
          </>
        }
      />

      <Tabs
        ariaLabel="Provider 详情分区"
        onChange={markVisited}
        items={[
          { key: "health", label: "健康", content: healthTab },
          { key: "quota", label: "运行与额度", content: quotaTab },
          { key: "profiles", label: "性能档位", content: profilesTab },
          { key: "keys", label: "密钥", content: keysTab },
          { key: "secret", label: "账户与密钥", content: secretTab },
          { key: "capabilities", label: "能力", content: capabilitiesTab },
          { key: "requests", label: "请求", content: requestsTab },
          { key: "errors", label: "错误", content: errorsTab },
          { key: "cost", label: "成本", content: costTab },
          { key: "config", label: "配置", content: configTab },
        ]}
      />
    </>
  );
}

/** 单次均价：只在调用量大于 0 时展示，避免用 0 调用做出无意义除法。 */
function unitCost(usage: ProviderUsage): string {
  const calls = Number(usage.calls_24h ?? 0);
  const cost = typeof usage.cost_usd_24h === "number" ? usage.cost_usd_24h : Number(usage.cost_usd_24h ?? 0);
  if (!Number.isFinite(calls) || calls <= 0 || !Number.isFinite(cost)) return "—";
  return fmtUSD((cost / calls).toFixed(6));
}
