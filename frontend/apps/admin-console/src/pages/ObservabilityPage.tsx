// S6-OBS-01 §25 可观测性（路由 /observability · 权限 admin:metrics:read）。
//
// dashboard（overview）/ SLO / alerts / metrics 直读。每个数字都带来源与窗口，不合并不同窗口的口径。
// 约束（AGENTS.md / docs/code-standards.md / AD-009）：正文只放业务数据、字段标签与操作控件，
// 说明 / 警告 / 成功 / 错误 / 风险 / 确认一律通过 useDialog() 弹窗。

import { useMemo, useState, type ReactNode } from "react";
import { ApiError, useAuth } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  ErrorState,
  EvidenceRail,
  Metric,
  NoPermissionState,
  PageHeader,
  StatusBadge,
  SkeletonRows,
  useDialog,
} from "@chain/ui";
import { KeyValueList } from "../components/s6";
import {
  fmtBytes,
  fmtDateTime,
  fmtMillis,
  fmtNumber,
  fmtPercent,
  fmtUSD,
  useAdminCommand,
  useAdminResource,
} from "../lib/adminApi";

const PERM_METRICS = "admin:metrics:read";
const PERM_DASHBOARD = "admin:dashboard:read";

type DialogApi = ReturnType<typeof useDialog>;

const WINDOW_OPTIONS = [
  { value: "15", label: "最近 15 分钟" },
  { value: "60", label: "最近 60 分钟" },
  { value: "360", label: "最近 6 小时" },
  { value: "1440", label: "最近 24 小时" },
  { value: "10080", label: "最近 7 天" },
];

function apiErrorDetails(error: unknown): Array<{ label: string; value: ReactNode }> {
  if (!(error instanceof ApiError)) return [];
  const details: Array<{ label: string; value: ReactNode }> = [];
  if (error.traceId) details.push({ label: "Trace", value: error.traceId });
  if (error.code) details.push({ label: "错误码", value: error.code });
  Object.entries(error.details ?? {}).forEach(([key, value]) => {
    if (value === null || value === undefined) return;
    details.push({ label: key, value: typeof value === "object" ? JSON.stringify(value) : String(value) });
  });
  return details;
}

function notifyCommandError(dialog: DialogApi, error: unknown, fallbackTitle: string): void {
  dialog.notify("error", fallbackTitle, error instanceof ApiError ? error.userMessage : "请稍后重试", apiErrorDetails(error));
}

function num(value: unknown): number {
  const parsed = typeof value === "number" ? value : Number(value ?? 0);
  return Number.isFinite(parsed) ? parsed : 0;
}

function text(value: unknown): string {
  return typeof value === "string" && value !== "" ? value : "—";
}

function yesNo(value: unknown): string {
  return value === true ? "是" : "否";
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" ? (value as Record<string, unknown>) : {};
}

interface ProbeHealth {
  checked_at?: string;
  checks?: Record<string, string>;
  healthy?: boolean;
  open_alerts?: number;
}

interface RouteP95 {
  route_template?: string;
  p95_seconds?: number;
}

interface ApiSummary {
  requests_total?: number;
  error_rate?: number;
  errors_5xx?: number;
  errors_4xx?: number;
  status_classes?: Record<string, number>;
  rate_limited?: number;
  auth_failures?: number;
  p95_by_route?: RouteP95[];
  inflight?: number;
  window_start?: string;
  window?: string;
  resets_on_restart?: boolean;
  source?: string;
  status?: string;
  reason?: string;
}

interface ThroughputBucket {
  calls?: number;
  errors?: number;
  rows?: number;
  bytes?: number;
  jobs?: number;
  avg_seconds?: number;
  audit_events?: number;
  source?: string;
  completed_jobs_source?: string;
}

interface AlertSummary {
  P0?: number;
  P1?: number;
  P2?: number;
  P3?: number;
  open_total?: number;
  source?: string;
  query_status?: string;
}

interface HighRiskRow {
  action?: string;
  actor_email?: string;
  resource_type?: string;
  resource_id?: string;
  risk_level?: string;
  occurred_at?: string;
  trace_id?: string;
}

interface ObservabilityOverview {
  as_of?: string;
  window_minutes?: number;
  window_start?: string;
  health?: ProbeHealth;
  api?: ApiSummary;
  throughput?: Record<string, ThroughputBucket>;
  queue?: Record<string, unknown>;
  alerts?: AlertSummary;
  cost?: { cost_24h?: string; amount_type?: string; source?: string };
  recent_high_risk?: HighRiskRow[];
  data_source?: Record<string, string>;
}

interface SloRow {
  key?: string;
  name?: string;
  target?: string;
  target_value?: number;
  comparator?: string;
  window?: string;
  current_value?: number;
  current_label?: string;
  unit?: string;
  status?: string;
  alert_priority?: string;
  samples?: number;
  source?: string;
  query_status?: string;
}

interface SloResponse {
  as_of?: string;
  items?: SloRow[];
  breached?: number;
  no_data?: number;
  note?: string;
}

interface AlertRule {
  priority?: string;
  trigger?: string;
  slo_keys?: string[];
}

interface AlertItem {
  id?: string;
  severity?: string;
  priority?: string;
  title?: string;
  message?: string;
  status?: string;
  slo_key?: string;
  occurrence_count?: number;
  first_seen_at?: string;
  last_seen_at?: string;
  source?: string;
}

interface AlertsResponse {
  as_of?: string;
  breaches?: string[];
  items?: AlertItem[];
  rules?: AlertRule[];
  note?: string;
}

interface MetricSample {
  labels?: Record<string, string>;
  value?: number;
}

interface MetricEntry {
  name?: string;
  type?: string;
  help?: string;
  samples?: MetricSample[];
}

interface MetricsResponse {
  items?: MetricEntry[];
  filter?: string;
  window_start?: string;
}

interface ThroughputRow {
  key: string;
  label: string;
  value: string;
  source: string;
}

const PROBE_LABELS: Record<string, string> = {
  postgres: "PostgreSQL",
  clickhouse: "ClickHouse",
  nats: "NATS",
  minio: "MinIO",
};

const PRIORITY_TONES: Record<string, "danger" | "warn" | "info" | "neutral"> = {
  P0: "danger",
  P1: "warn",
  P2: "info",
  P3: "neutral",
};

function priorityMetricTone(level: string, count: number): "ok" | "warn" | "danger" | "neutral" {
  if (count <= 0) return "ok";
  if (level === "P0") return "danger";
  if (level === "P1") return "warn";
  if (level === "P2") return "warn";
  return "neutral";
}

function checkTone(value: string | undefined): "ok" | "warn" | "danger" | "neutral" {
  if (value === "ok") return "ok";
  if (value === "degraded") return "warn";
  if (!value) return "neutral";
  return "danger";
}

function sloTone(status: string | undefined): "ok" | "danger" | "neutral" {
  if (status === "ok") return "ok";
  if (status === "breach") return "danger";
  return "neutral";
}

function labelPairs(labels: Record<string, string> | undefined): string {
  const entries = Object.entries(labels ?? {});
  if (entries.length === 0) return "—";
  return entries.map(([key, value]) => key + "=" + value).join(", ");
}

export default function ObservabilityPage() {
  const { client, can } = useAuth();
  const dialog = useDialog();
  const command = useAdminCommand();
  const canDashboard = can(PERM_DASHBOARD);
  const [windowMinutes, setWindowMinutes] = useState("60");
  const [metricName, setMetricName] = useState("");

  const overviewPath = canDashboard
    ? "/api/admin/v1/observability/overview?window_minutes=" + windowMinutes
    : null;
  const overview = useAdminResource<ObservabilityOverview>(overviewPath);
  const slo = useAdminResource<SloResponse>("/api/admin/v1/observability/slo");
  const alerts = useAdminResource<AlertsResponse>("/api/admin/v1/observability/alerts");
  const metrics = useAdminResource<MetricsResponse>(
    "/api/admin/v1/observability/metrics?name=" + encodeURIComponent(metricName),
  );

  const data = overview.data;
  const api = data?.api ?? {};
  const alertsSummary = data?.alerts ?? {};
  const throughput = data?.throughput ?? {};
  const queue = data?.queue ?? {};
  const health = data?.health ?? {};
  const checks = health.checks ?? {};
  const sloItems = slo.data?.items ?? [];
  const alertRules = alerts.data?.rules ?? [];
  const alertItems = alerts.data?.items ?? [];

  const throughputRows = useMemo<ThroughputRow[]>(() => {
    const provider = throughput.provider_fetch ?? {};
    const pipeline = throughput.pipeline ?? {};
    const wall = throughput.job_wall_clock ?? {};
    const exportBucket = throughput.export ?? {};
    const downloads = throughput.download_events ?? {};
    return [
      {
        key: "provider_fetch",
        label: "Provider 拉取",
        value:
          "调用 " + fmtNumber(provider.calls) + " · 失败 " + fmtNumber(provider.errors) + " · 行 " +
          fmtNumber(provider.rows) + " · " + fmtBytes(provider.bytes),
        source: text(provider.source),
      },
      {
        key: "pipeline",
        label: "Pipeline 入库",
        value: "行数 " + fmtNumber(pipeline.rows),
        source: text(pipeline.source),
      },
      {
        key: "job_wall_clock",
        label: "任务端到端",
        value: "平均 " + fmtNumber(wall.avg_seconds) + " 秒",
        source: text(wall.completed_jobs_source ?? wall.source),
      },
      {
        key: "export",
        label: "导出任务",
        value: "任务 " + fmtNumber(exportBucket.jobs),
        source: text(exportBucket.source),
      },
      {
        key: "download_events",
        label: "下载事件",
        value: "审计事件 " + fmtNumber(downloads.audit_events),
        source: text(downloads.source),
      },
    ];
  }, [throughput]);

  const showCaliber = () => {
    dialog.notify("info", "可观测性口径", null, [
      { label: "总览接口", value: "GET /api/admin/v1/observability/overview" },
      { label: "as_of", value: fmtDateTime(data?.as_of) },
      { label: "window_minutes", value: fmtNumber(data?.window_minutes ?? Number(windowMinutes)) },
      { label: "window_start", value: fmtDateTime(data?.window_start ?? api.window_start) },
      { label: "API 窗口", value: text(api.window) },
      { label: "进程窗口重启归零", value: yesNo(api.resets_on_restart) },
      { label: "API 来源", value: text(api.source ?? data?.data_source?.api) },
      { label: "健康来源", value: text(data?.data_source?.health) },
      { label: "告警来源", value: text(data?.data_source?.alerts ?? alertsSummary.source) },
      { label: "审计来源", value: text(data?.data_source?.audit) },
      { label: "SLO 说明", value: text(slo.data?.note) },
      { label: "告警说明", value: text(alerts.data?.note) },
      { label: "成本口径", value: text(data?.cost?.amount_type) + " · " + text(data?.cost?.source) },
    ]);
  };

  const evaluateAlerts = async () => {
    if (!can(PERM_METRICS)) {
      dialog.notify("warning", "缺少控制权限", "当前账号未被授予该操作所需的权限，请求未发送。", [
        { label: "所需权限", value: PERM_METRICS },
      ]);
      return;
    }
    await command.run({
      path: "/api/admin/v1/observability/alerts/evaluate",
      body: {},
      requireReason: false,
      confirmTitle: "评估 SLO 并写入告警中心？",
      confirmBody: "会把当前 SLO 违约项写入告警中心（含 priority 与 slo_key），已恢复的告警会被解决。",
      confirmLabel: "评估并写入",
      successTitle: "告警评估完成",
      successDetails: (result) => [
        { label: "新增违约写入", value: fmtNumber(result.breaches_written) },
        { label: "已解决", value: fmtNumber(result.resolved) },
        { label: "无变化", value: fmtNumber(result.unchanged) },
        { label: "评估时间", value: fmtDateTime(result.as_of) },
      ],
      onDone: () => {
        slo.reload();
        alerts.reload();
        metrics.reload();
      },
    });
  };

  const queryMetric = async () => {
    if (!can(PERM_METRICS)) {
      dialog.notify("warning", "缺少控制权限", "当前账号未被授予该操作所需的权限，请求未发送。", [
        { label: "所需权限", value: PERM_METRICS },
      ]);
      return;
    }
    try {
      const response = await client.get<MetricsResponse>("/api/admin/v1/observability/metrics", {
        query: { name: metricName },
      });
      const entry = response.data?.items?.[0];
      dialog.notify("info", "指标直读结果", null, [
        { label: "过滤条件", value: text(response.data?.filter) || "（未指定，返回全部）" },
        { label: "指标名", value: text(entry?.name) },
        { label: "类型", value: text(entry?.type) },
        { label: "样本数", value: fmtNumber((entry?.samples ?? []).length) },
        { label: "窗口起点", value: fmtDateTime(response.data?.window_start) },
      ]);
    } catch (error) {
      notifyCommandError(dialog, error, "指标读取失败");
    }
  };

  const showSloDetail = (row: SloRow) => {
    void dialog.open({
      kind: row.status === "breach" ? "warning" : "info",
      title: row.name ?? row.key ?? "SLO",
      details: [
        { label: "SLO key", value: <span className="mono">{row.key ?? "—"}</span> },
        { label: "目标", value: text(row.target) },
        { label: "比较符", value: row.comparator ?? "—" },
        { label: "目标值", value: fmtNumber(row.target_value) },
        { label: "当前值", value: fmtNumber(row.current_value) },
        { label: "当前标签", value: text(row.current_label) },
        { label: "单位", value: row.unit ?? "—" },
        { label: "状态", value: row.status ?? "no_data" },
        { label: "告警优先级", value: row.alert_priority ?? "—" },
        { label: "窗口", value: row.window ?? "—" },
        { label: "样本数", value: fmtNumber(row.samples) },
        { label: "来源", value: row.source ?? "—" },
        { label: "查询状态 / 原因", value: row.status === "ok" ? "query ok" : text(row.query_status) },
      ],
      actions: [{ label: "关闭", variant: "primary", result: "ok" }],
    });
  };

  return (
    <div className="workbench">
      <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: "var(--sp-5)" }}>
        <PageHeader
          title="可观测性"
          subtitle={
            <>
              {"as_of " + fmtDateTime(data?.as_of) + " · window_start " + fmtDateTime(data?.window_start ?? api.window_start)}
            </>
          }
          actions={
            <>
              <select
                className="select"
                aria-label="统计窗口"
                value={windowMinutes}
                onChange={(event) => setWindowMinutes(event.target.value)}
                style={{ width: "auto" }}
              >
                {WINDOW_OPTIONS.map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
              <button type="button" className="btn btn--sm" onClick={showCaliber}>
                口径
              </button>
              <button type="button" className="btn btn--sm" disabled={!can(PERM_METRICS) || command.busy} onClick={() => void evaluateAlerts()}>
                {command.busy ? "评估中…" : "评估并写入告警"}
              </button>
              <button
                type="button"
                className="btn btn--sm"
                onClick={() => {
                  overview.reload();
                  slo.reload();
                  alerts.reload();
                  metrics.reload();
                }}
              >
                刷新
              </button>
            </>
          }
        />

        <Card title="告警优先级（开放中）">
          <div className="metric-grid">
            {(["P0", "P1", "P2", "P3"] as const).map((level) => {
              const count = num(alertsSummary[level]);
              return (
                <div key={level} role="group" aria-label={level + " 未处理告警 " + count + " 条"}>
                  <Metric
                    label={level + " 告警"}
                    value={fmtNumber(count)}
                    tone={priorityMetricTone(level, count)}
                    hint={"status OPEN/ACK · 来源 " + text(alertsSummary.source)}
                  />
                </div>
              );
            })}
            <div role="group" aria-label={"开放告警合计 " + num(alertsSummary.open_total) + " 条"}>
              <Metric
                label="开放合计"
                value={fmtNumber(alertsSummary.open_total)}
                tone={num(alertsSummary.open_total) > 0 ? "warn" : "ok"}
                hint={text(alertsSummary.query_status) === "—" ? "查询正常" : "查询状态 " + text(alertsSummary.query_status)}
              />
            </div>
          </div>
        </Card>

        {!canDashboard ? (
          <Card>
            <NoPermissionState permission={PERM_DASHBOARD} title="没有查看观测总览的权限" />
          </Card>
        ) : overview.loading && !data ? (
          <SkeletonRows rows={4} columns={4} />
        ) : overview.error && !data ? (
          <Card>
            <ErrorState title="观测总览加载失败" message={overview.error} onRetry={overview.reload} />
          </Card>
        ) : (
          <>
            <Card title="健康轨道">
              <div className="toolbar">
                {Object.keys(PROBE_LABELS).map((key) => (
                  <span className="toolbar" key={key} aria-label={PROBE_LABELS[key] + " 探针状态"}>
                    <StatusBadge tone={checkTone(checks[key])}>
                      {PROBE_LABELS[key] + " " + (checks[key] ?? "未探测")}
                    </StatusBadge>
                  </span>
                ))}
                <span className="shell__spacer" />
                <StatusBadge tone={health.healthy ? "ok" : "warn"}>{health.healthy ? "全部正常" : "存在异常"}</StatusBadge>
                <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>
                  {"探针时间 " + fmtDateTime(health.checked_at)}
                </span>
                <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>
                  {"开放告警 " + fmtNumber(health.open_alerts)}
                </span>
              </div>
            </Card>

            <Card title="API 指标">
              {(api.status ?? "") === "unavailable" ? (
                <EmptyState title="指标注册表不可读" body={text(api.reason)} />
              ) : (
                <>
                  <div className="metric-grid">
                    <Metric label="请求总数" value={fmtNumber(api.requests_total)} tone="neutral" hint={text(api.window)} />
                    <Metric
                      label="错误率（5xx）"
                      value={fmtPercent(api.error_rate)}
                      tone={num(api.error_rate) > 0.01 ? "danger" : num(api.error_rate) > 0 ? "warn" : "ok"}
                      hint={"5xx " + fmtNumber(api.errors_5xx) + " · 4xx " + fmtNumber(api.errors_4xx)}
                    />
                    <Metric label="在途请求" value={fmtNumber(api.inflight)} tone="neutral" hint="inflight" />
                    <Metric label="限流次数" value={fmtNumber(api.rate_limited)} tone={num(api.rate_limited) > 0 ? "warn" : "ok"} hint="429" />
                    <Metric label="认证失败" value={fmtNumber(api.auth_failures)} tone={num(api.auth_failures) > 0 ? "warn" : "ok"} hint="auth failures" />
                    <Metric label="窗口起点" value={<span style={{ fontSize: "var(--fs-16)" }}>{fmtDateTime(api.window_start)}</span>} tone="neutral" hint={"重启归零 " + yesNo(api.resets_on_restart)} />
                  </div>
                  <div style={{ marginTop: "var(--sp-4)" }}>
                    <KeyValueList
                      items={[
                        { label: "窗口", value: text(api.window) },
                        { label: "重启归零（resets_on_restart）", value: yesNo(api.resets_on_restart) },
                        { label: "指标来源", value: text(api.source) },
                        ...Object.entries(api.status_classes ?? {}).map(([key, value]) => ({
                          label: "状态类 " + key,
                          value: fmtNumber(value),
                        })),
                      ]}
                    />
                  </div>
                </>
              )}
            </Card>

            <Card title="路由 P95 延迟" flush>
              <DataTable<RouteP95>
                rows={api.p95_by_route ?? []}
                rowKey={(row, index) => (row.route_template ?? "route") + ":" + String(index)}
                empty={<EmptyState title="没有路由延迟样本" />}
                columns={[
                  { key: "route", header: "路由模板", render: (row) => <span className="mono">{row.route_template ?? "—"}</span> },
                  {
                    key: "p95",
                    header: "P95",
                    width: "160px",
                    align: "right",
                    render: (row) => <span className="num">{fmtMillis(num(row.p95_seconds) * 1000)}</span>,
                  },
                ]}
              />
            </Card>

            <Card title="吞吐拆分" flush>
              <DataTable<ThroughputRow>
                rows={throughputRows}
                rowKey={(row) => row.key}
                empty={<EmptyState title="没有吞吐数据" />}
                columns={[
                  { key: "label", header: "环节", width: "180px", render: (row) => row.label },
                  { key: "value", header: "本窗口数值", render: (row) => <span className="num">{row.value}</span> },
                  { key: "source", header: "来源", width: "300px", render: (row) => <span className="mono" style={{ fontSize: "var(--fs-11)" }}>{row.source}</span> },
                ]}
              />
            </Card>

            <Card title="队列与成本">
              <div className="metric-grid">
                <Metric label="运行中任务" value={fmtNumber(num(queue.running))} tone={num(queue.running) > 0 ? "warn" : "neutral"} />
                <Metric label="等待中任务" value={fmtNumber(num(queue.waiting))} tone={num(queue.waiting) > 0 ? "warn" : "neutral"} />
                <Metric label="失败任务" value={fmtNumber(num(queue.failed))} tone={num(queue.failed) > 0 ? "danger" : "ok"} />
                <Metric label="暂停任务" value={fmtNumber(num(queue.paused))} tone={num(queue.paused) > 0 ? "warn" : "neutral"} />
                <Metric label="24 小时成本" value={<span style={{ fontSize: "var(--fs-18)" }}>{fmtUSD(data?.cost?.cost_24h)}</span>} tone="neutral" hint={text(data?.cost?.source)} />
              </div>
            </Card>

            <Card title="近期高风险动作" flush>
              <DataTable<HighRiskRow>
                rows={data?.recent_high_risk ?? []}
                rowKey={(row, index) => (row.trace_id || row.action || "risk") + ":" + String(index)}
                empty={<EmptyState title="没有高风险动作" />}
                columns={[
                  { key: "time", header: "时间", width: "170px", render: (row) => fmtDateTime(row.occurred_at) },
                  { key: "action", header: "动作", render: (row) => <span className="mono">{row.action ?? "—"}</span> },
                  { key: "actor", header: "操作者", width: "200px", render: (row) => row.actor_email || "—" },
                  {
                    key: "resource",
                    header: "资源",
                    width: "200px",
                    render: (row) => <span className="mono" style={{ fontSize: "var(--fs-11)" }}>{(row.resource_type ?? "—") + " " + (row.resource_id ?? "")}</span>,
                  },
                  {
                    key: "risk",
                    header: "风险",
                    width: "110px",
                    render: (row) => <StatusBadge tone={row.risk_level === "critical" ? "danger" : "warn"}>{row.risk_level ?? "—"}</StatusBadge>,
                  },
                  { key: "trace", header: "Trace", width: "200px", render: (row) => <span className="mono" style={{ fontSize: "var(--fs-11)" }}>{row.trace_id || "—"}</span> },
                ]}
              />
            </Card>
          </>
        )}

        <Card title="SLO" actions={
          <>
            <StatusBadge tone={num(slo.data?.breached) > 0 ? "danger" : "ok"}>{"违约 " + fmtNumber(slo.data?.breached)}</StatusBadge>
            <StatusBadge tone="neutral">{"无数据 " + fmtNumber(slo.data?.no_data)}</StatusBadge>
          </>
        } flush>
          <DataTable<SloRow>
            loading={slo.loading}
            error={slo.error}
            onRetry={slo.reload}
            rows={sloItems}
            rowKey={(row) => row.key ?? (row.name ?? "slo")}
            empty={<EmptyState title="没有 SLO 记录" />}
            columns={[
              {
                key: "name",
                header: "SLO",
                width: "220px",
                render: (row) => (
                  <span>
                    <div>{row.name ?? "—"}</div>
                    <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>{row.key ?? "—"}</div>
                  </span>
                ),
              },
              { key: "target", header: "目标", width: "170px", render: (row) => <span className="mono">{text(row.target) === "—" ? (row.comparator ?? "—") + " " + fmtNumber(row.target_value) : text(row.target)}</span> },
              {
                key: "current",
                header: "当前值",
                width: "170px",
                render: (row) => <span className="num">{text(row.current_label) === "—" ? fmtNumber(row.current_value) : text(row.current_label)}</span>,
              },
              {
                key: "status",
                header: "状态",
                width: "120px",
                render: (row) => <StatusBadge tone={sloTone(row.status)}>{row.status ?? "no_data"}</StatusBadge>,
              },
              { key: "window", header: "窗口", width: "150px", render: (row) => row.window ?? "—" },
              { key: "source", header: "来源", width: "230px", render: (row) => <span className="mono" style={{ fontSize: "var(--fs-11)" }}>{row.source ?? "—"}</span> },
              {
                key: "query",
                header: "查询状态 / 原因",
                width: "200px",
                render: (row) => (
                  <span className="mono" style={{ fontSize: "var(--fs-11)" }}>
                    {row.status === "ok" ? "query ok · 样本 " + fmtNumber(row.samples) : text(row.query_status) + " · 样本 " + fmtNumber(row.samples)}
                  </span>
                ),
              },
              {
                key: "actions",
                header: "操作",
                width: "90px",
                align: "right",
                render: (row) => (
                  <button type="button" className="btn btn--sm" onClick={() => showSloDetail(row)}>
                    说明
                  </button>
                ),
              },
            ]}
          />
        </Card>

        <Card title={"告警规则（" + fmtNumber(alertRules.length) + "）"} flush>
          <DataTable<AlertRule>
            loading={alerts.loading}
            error={alerts.error}
            onRetry={alerts.reload}
            rows={alertRules}
            rowKey={(row) => row.priority ?? "rule"}
            empty={<EmptyState title="没有告警规则" />}
            columns={[
              { key: "priority", header: "优先级", width: "110px", render: (row) => <StatusBadge tone={PRIORITY_TONES[row.priority ?? ""] ?? "neutral"}>{row.priority ?? "—"}</StatusBadge> },
              { key: "trigger", header: "触发条件", render: (row) => row.trigger ?? "—" },
              {
                key: "slo",
                header: "关联 SLO",
                width: "320px",
                render: (row) => <span className="mono" style={{ fontSize: "var(--fs-11)" }}>{(row.slo_keys ?? []).join(", ") || "—"}</span>,
              },
            ]}
          />
        </Card>

        <Card title={"当前告警（" + fmtNumber(alertItems.length) + "）"} flush>
          <DataTable<AlertItem>
            loading={alerts.loading}
            error={alerts.error}
            onRetry={alerts.reload}
            rows={alertItems}
            rowKey={(row, index) => row.id ?? row.title ?? "alert:" + String(index)}
            empty={<EmptyState title="没有当前告警" />}
            columns={[
              {
                key: "priority",
                header: "优先级",
                width: "110px",
                render: (row) => <StatusBadge tone={PRIORITY_TONES[row.priority ?? ""] ?? "neutral"}>{row.priority || row.severity || "—"}</StatusBadge>,
              },
              { key: "title", header: "标题", render: (row) => row.title ?? "—" },
              { key: "slo", header: "SLO", width: "170px", render: (row) => <span className="mono">{row.slo_key || "—"}</span> },
              { key: "status", header: "状态", width: "110px", render: (row) => <StatusBadge tone={row.status === "OPEN" ? "warn" : "neutral"}>{row.status ?? "—"}</StatusBadge> },
              { key: "count", header: "次数", width: "80px", align: "right", render: (row) => <span className="num">{fmtNumber(row.occurrence_count)}</span> },
              { key: "first", header: "首次", width: "170px", render: (row) => fmtDateTime(row.first_seen_at) },
              { key: "last", header: "最近", width: "170px", render: (row) => fmtDateTime(row.last_seen_at) },
            ]}
          />
        </Card>

        <Card title="指标直读">
          <div className="toolbar" style={{ alignItems: "flex-end" }}>
            <div style={{ flex: "1 1 220px", minWidth: 0 }}>
              <label className="field__label" htmlFor="metric-name">指标名</label>
              <input
                id="metric-name"
                className="input mono"
                placeholder="例如 cs_http_requests_total"
                value={metricName}
                onChange={(event) => setMetricName(event.target.value)}
              />
            </div>
            <button
              type="button"
              className="btn btn--sm"
              style={{ alignSelf: "flex-end" }}
              disabled={!can(PERM_METRICS) || metrics.loading}
              onClick={() => void queryMetric()}
            >
              查询
            </button>
          </div>
          <div style={{ marginTop: "var(--sp-4)" }}>
            <KeyValueList
              items={[
                { label: "过滤条件", value: text(metrics.data?.filter) || "（未指定）" },
                { label: "返回指标数", value: fmtNumber((metrics.data?.items ?? []).length) },
                { label: "窗口起点", value: fmtDateTime(metrics.data?.window_start) },
                { label: "首条指标", value: (metrics.data?.items ?? [])[0]?.name ?? "—", mono: true },
                { label: "首条样本数", value: fmtNumber(((metrics.data?.items ?? [])[0]?.samples ?? []).length) },
              ]}
            />
          </div>
          {(metrics.data?.items ?? []).length > 0 ? (
            <div style={{ marginTop: "var(--sp-4)" }}>
              <DataTable<MetricSample>
                rows={(metrics.data?.items ?? [])[0]?.samples ?? []}
                rowKey={(row, index) => labelPairs(row.labels) + ":" + String(index)}
                empty={<EmptyState title="没有样本" />}
                columns={[
                  { key: "labels", header: "标签", render: (row) => <span className="mono" style={{ fontSize: "var(--fs-11)" }}>{labelPairs(row.labels)}</span> },
                  { key: "value", header: "值", width: "180px", align: "right", render: (row) => <span className="num">{fmtNumber(row.value)}</span> },
                ]}
              />
            </div>
          ) : null}
        </Card>
      </div>

      <EvidenceRail
        title="观测证据链"
        items={[
          { label: "as_of", value: fmtDateTime(data?.as_of) },
          { label: "窗口", value: fmtNumber(data?.window_minutes ?? Number(windowMinutes)) + " 分钟" },
          { label: "window_start", value: fmtDateTime(data?.window_start ?? api.window_start) },
          { label: "进程窗口重启归零", value: yesNo(api.resets_on_restart) },
          { label: "API 来源", value: text(api.source) },
          { label: "告警来源", value: text(alertsSummary.source) },
          { label: "成本来源", value: text(data?.cost?.source) },
          { label: "SLO 条数", value: fmtNumber(sloItems.length) },
          { label: "SLO 违约", value: fmtNumber(slo.data?.breached) },
          { label: "SLO 无数据", value: fmtNumber(slo.data?.no_data) },
        ]}
      />
    </div>
  );
}
