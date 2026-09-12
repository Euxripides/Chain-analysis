import { useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { useAuth } from "@chain/api-client";
import {
  Card,
  DataTable,
  Metric,
  PageHeader,
  StatusBadge,
  statusToneFor,
  type StatusTone,
  useDialog,
} from "@chain/ui";
import {
  fmtDateTime,
  fmtMillis,
  fmtNumber,
  fmtPercent,
  fmtUSD,
  useAdminCommand,
  useAdminList,
} from "../lib/adminApi";

/**
 * A-PROVIDER-01：Provider 账户总览（路由 /providers，权限 admin:provider:read）。
 *
 * 数据来源：GET /api/admin/v1/providers（provider_descriptors + provider_usage + provider_secrets 摘要）。
 * 「测试连接」走 POST /providers/{key}/commands/test（Provider.Health 探针）。
 * 所有提示只通过弹窗呈现；页面正文只有业务数据、字段标签与操作控件。
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

interface RuntimeHealth {
  state?: string;
  last_check?: string;
  error?: string;
  consecutive_failures?: number;
  recent_error_rate?: number;
  current_concurrency?: number;
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

interface ProviderRow {
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
  last_test_at?: string;
  last_test_result?: Record<string, unknown>;
  runtime_health?: RuntimeHealth;
  usage?: ProviderUsage;
  active_secret?: ActiveSecret;
  updated_at: string;
}

function providerTone(status: string): StatusTone {
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
      return statusToneFor(status);
  }
}

/** 金额统一按后端的十进制字符串口径展示：先转成微美元整数再格式化，避免浮点累加误差。 */
function sumUSD(values: Array<number | string | undefined>): string {
  const micro = values.reduce<number>((total, value) => {
    const num = typeof value === "number" ? value : Number(value ?? 0);
    if (!Number.isFinite(num)) return total;
    return total + Math.round(num * 1e6);
  }, 0);
  return (micro / 1e6).toFixed(6);
}

function text(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—";
  return String(value);
}

export default function ProvidersPage() {
  const navigate = useNavigate();
  const dialog = useDialog();
  const { can } = useAuth();
  const list = useAdminList<ProviderRow>("/api/admin/v1/providers", {}, 50);
  const command = useAdminCommand();

  const items = list.items;

  const summary = useMemo(() => {
    const connected = items.filter((row) => row.status === "connected").length;
    const calls = items.reduce((total, row) => total + Number(row.usage?.calls_24h ?? 0), 0);
    const errors = items.reduce((total, row) => total + Number(row.usage?.errors_24h ?? 0), 0);
    const cost = sumUSD(items.map((row) => row.usage?.cost_usd_24h));
    const errorsWithSecret = items.filter((row) => row.active_secret?.configured !== true).length;
    return { connected, calls, errors, cost, errorsWithSecret };
  }, [items]);

  const requirePermission = (permission: string): boolean => {
    if (can(permission)) return true;
    dialog.notify("warning", "缺少控制权限", null, [{ label: "所需权限", value: permission }]);
    return false;
  };

  const showScope = () =>
    dialog.notify("info", "Provider 列表口径", null, [
      { label: "数据来源", value: "postgres:provider_descriptors / provider_usage / provider_secrets" },
      { label: "调用与错误率", value: "provider_usage 最近 24 小时；错误率 = 非 success 调用 / 调用总数" },
      { label: "P95", value: "provider_usage.duration_ms 的 95 分位（percentile_disc）" },
      { label: "成本", value: "provider_usage.estimated_cost_usd 累加，按十进制字符串展示" },
      { label: "并发 / 优先级", value: "provider_descriptors 的声明值，非运行时实测值" },
      { label: "运行时健康", value: "当前进程 provider.Health()，仅对已加载实现的 Provider 返回" },
      { label: "密钥", value: "只保存引用名、前 6 位与 SHA-256 指纹，明文永不回显" },
      { label: "读取权限", value: "admin:provider:read" },
    ]);

  const runTest = async (row: ProviderRow) => {
    if (!requirePermission("admin:provider:read")) return;
    await command.run({
      path: `/api/admin/v1/providers/${encodeURIComponent(row.key)}/commands/test`,
      requireReason: false,
      confirmTitle: `测试连接 · ${row.display_name || row.key}`,
      confirmBody: "将对该 Provider 执行一次 Provider.Health 探针，并写入一条 provider_usage 记录。",
      confirmLabel: "执行测试",
      successTitle: "连接测试完成",
      successDetails: (data) => [
        { label: "Provider", value: <span className="mono">{text(data.provider ?? row.key)}</span> },
        { label: "探测方法", value: text(data.method) },
        {
          label: "结果",
          value: <StatusBadge tone={data.ok === true ? "ok" : "danger"}>{data.ok === true ? "ok" : "failed"}</StatusBadge>,
        },
        { label: "延迟", value: fmtMillis(data.latency_ms) },
        { label: "运行状态", value: text(data.state) },
        { label: "错误类", value: text(data.error_class) },
        { label: "错误", value: text(data.error) },
        { label: "近端错误率", value: typeof data.recent_error_rate === "number" ? fmtPercent(data.recent_error_rate, 2) : "—" },
        { label: "Trace", value: <span className="mono">{text(data.trace_id)}</span> },
        { label: "检查时间", value: fmtDateTime(data.checked_at) },
      ],
      onDone: () => list.reload(),
    });
  };

  const openDetail = (row: ProviderRow) => {
    navigate(`/providers/${encodeURIComponent(row.key)}`);
  };

  return (
    <>
      <PageHeader
        title="Provider 账户"
        subtitle={
          <>
            数据源 <span className="mono">provider_descriptors · provider_usage</span> · 24h 窗口
          </>
        }
        actions={
          <>
            <button type="button" className="btn btn--sm" onClick={list.reload}>
              刷新
            </button>
            <button type="button" className="btn btn--sm" disabled={!list.hasPrevious} onClick={list.goPrevious}>
              上一页
            </button>
            <button type="button" className="btn btn--sm" disabled={!list.nextCursor} onClick={list.goNext}>
              下一页
            </button>
            <button type="button" className="btn btn--sm" onClick={showScope}>
              口径
            </button>
          </>
        }
      />

      <div className="metric-grid">
        <Metric label="Provider" value={fmtNumber(items.length)} hint="provider_descriptors" />
        <Metric label="已连接" value={fmtNumber(summary.connected)} />
        <Metric label="24h 调用" value={fmtNumber(summary.calls)} />
        <Metric label="24h 错误" value={fmtNumber(summary.errors)} tone={summary.errors > 0 ? "warn" : "ok"} />
        <Metric label="24h 成本" value={fmtUSD(summary.cost)} hint="provider_usage.estimated_cost_usd" />
        <Metric
          label="未配置密钥"
          value={fmtNumber(summary.errorsWithSecret)}
          tone={summary.errorsWithSecret > 0 ? "warn" : "neutral"}
        />
      </div>

      <Card title="Provider 列表" flush>
        <DataTable<ProviderRow>
          rows={items}
          rowKey={(row) => row.key}
          loading={list.loading}
          error={list.error}
          onRetry={list.reload}
          columns={[
            {
              key: "provider",
              header: "Provider",
              render: (row) => (
                <span>
                  <strong>{row.display_name || row.key}</strong>
                  <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>
                    {row.key}
                  </div>
                </span>
              ),
            },
            {
              key: "kind",
              header: "类型",
              width: "110px",
              render: (row) => <StatusBadge tone="info">{row.kind}</StatusBadge>,
            },
            {
              key: "status",
              header: "状态",
              width: "130px",
              render: (row) => <StatusBadge tone={providerTone(row.status)}>{row.status}</StatusBadge>,
            },
            {
              key: "calls",
              header: "24h 调用",
              width: "110px",
              align: "right",
              render: (row) => <span className="num">{fmtNumber(row.usage?.calls_24h ?? 0)}</span>,
            },
            {
              key: "error_rate",
              header: "错误率",
              width: "100px",
              align: "right",
              render: (row) => (
                <span className="num" style={{ color: Number(row.usage?.error_rate_24h ?? 0) > 0 ? "var(--c-danger)" : undefined }}>
                  {fmtPercent(row.usage?.error_rate_24h ?? 0, 2)}
                </span>
              ),
            },
            {
              key: "p95",
              header: "P95",
              width: "100px",
              align: "right",
              render: (row) => <span className="num">{fmtMillis(row.usage?.p95_ms)}</span>,
            },
            {
              key: "cost",
              header: "24h 成本",
              width: "130px",
              align: "right",
              render: (row) => <span className="num">{fmtUSD(row.usage?.cost_usd_24h)}</span>,
            },
            {
              key: "concurrency",
              header: "并发",
              width: "80px",
              align: "right",
              render: (row) => (
                <span className="num">
                  {fmtNumber(row.runtime_health?.current_concurrency ?? row.concurrency)}
                </span>
              ),
            },
            {
              key: "priority",
              header: "优先级",
              width: "90px",
              align: "right",
              render: (row) => <span className="num">{fmtNumber(row.priority)}</span>,
            },
            {
              key: "updated",
              header: "更新时间",
              width: "170px",
              render: (row) => <span className="num">{fmtDateTime(row.updated_at)}</span>,
            },
            {
              key: "actions",
              header: "操作",
              width: "170px",
              align: "right",
              render: (row) => (
                <span className="toolbar" style={{ justifyContent: "flex-end" }}>
                  <button type="button" className="btn btn--sm" onClick={() => openDetail(row)}>
                    详情
                  </button>
                  <button
                    type="button"
                    className="btn btn--sm"
                    disabled={!can("admin:provider:read") || command.busy}
                    onClick={() => void runTest(row)}
                  >
                    测试连接
                  </button>
                </span>
              ),
            },
          ]}
        />
      </Card>
    </>
  );
}
