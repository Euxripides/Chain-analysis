import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { ApiError, newIdempotencyKey, useAuth } from "@chain/api-client";
import {
  Card,
  DataTable,
  EntityLink,
  ErrorState,
  Metric,
  PageHeader,
  SkeletonRows,
  StatusBadge,
  statusToneFor,
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
  source?: string;
}

interface ActiveSecret {
  configured?: boolean;
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
  updated_at: string;
}

interface SecretRow {
  id: string;
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

  const provider = resource.data;
  const secretRows = secretsResource.data?.items ?? [];

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
        <PageHeader title="Provider 详情" subtitle={<span className="mono">{providerKey}</span>} />
        <Card title="正在加载">
          <SkeletonRows rows={6} columns={4} />
        </Card>
      </>
    );
  }

  if (!provider) {
    return (
      <>
        <PageHeader title="Provider 详情" subtitle={<span className="mono">{providerKey}</span>} />
        <Card>
          <ErrorState title="Provider 数据加载失败" message={resource.error ?? "对象不存在或无权查看"} onRetry={resource.reload} />
        </Card>
      </>
    );
  }

  const capabilities = provider.capabilities ?? [];

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
          <span className="toolbar">
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
        items={[
          { key: "health", label: "健康", content: healthTab },
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
