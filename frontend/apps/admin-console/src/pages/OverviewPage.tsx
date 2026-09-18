import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { ApiError, useAuth, type AdminOverview } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  ErrorState,
  Metric,
  PageHeader,
  SkeletonRows,
  StatusBadge,
  useDialog,
} from "@chain/ui";
import { fmtBytes, fmtDateTime, fmtNumber, fmtPercent } from "../lib/adminApi";

/** §19.2 A-HOME-01 运营总览 + S6-OBS-01 可观测性区块。所有指标来自真实聚合，无占位值。 */

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

interface ProbeHealth {
  checked_at?: string;
  checks?: Record<string, string>;
  healthy?: boolean;
  open_alerts?: number;
}

interface AlertSummary {
  P0?: number;
  P1?: number;
  P2?: number;
  P3?: number;
  open_total?: number;
  source?: string;
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
  api?: { requests_total?: number; error_rate?: number; inflight?: number; source?: string; window?: string; resets_on_restart?: boolean };
  throughput?: Record<string, ThroughputBucket>;
  queue?: Record<string, unknown>;
  alerts?: AlertSummary;
  recent_high_risk?: HighRiskRow[];
  data_source?: Record<string, string>;
}

interface ThroughputRow {
  key: string;
  label: string;
  value: string;
  source: string;
}

function num(value: unknown): number {
  const parsed = typeof value === "number" ? value : Number(value ?? 0);
  return Number.isFinite(parsed) ? parsed : 0;
}

function text(value: unknown): string {
  return typeof value === "string" && value !== "" ? value : "—";
}

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

export default function OverviewPage() {
  const { client } = useAuth();
  const dialog = useDialog();
  const [data, setData] = useState<AdminOverview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [observability, setObservability] = useState<ObservabilityOverview | null>(null);
  const [observabilityError, setObservabilityError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    setObservabilityError(null);
    try {
      const res = await client.get<AdminOverview>("/api/admin/v1/overview");
      setData(res.data);
    } catch (err) {
      setError(err instanceof ApiError ? err.userMessage : "无法加载运营总览");
    } finally {
      setLoading(false);
    }
    try {
      const res = await client.get<ObservabilityOverview>("/api/admin/v1/observability/overview", {
        query: { window_minutes: 60 },
      });
      setObservability(res.data ?? null);
    } catch (err) {
      setObservability(null);
      setObservabilityError(err instanceof ApiError ? err.userMessage : "无法加载可观测性总览");
    }
  }, [client]);

  useEffect(() => {
    void load();
  }, [load]);

  const toneFor = (key: string): "ok" | "warn" | "danger" | "neutral" => {
    const value = data?.cards.find((c) => c.key === key)?.value ?? 0;
    if (key === "jobs_failed" || key === "login_failures_24h") return value > 0 ? "danger" : "ok";
    if (key === "jobs_running") return value > 0 ? "warn" : "neutral";
    return "neutral";
  };

  const health = observability?.health ?? {};
  const checks = health.checks ?? {};
  const alerts = observability?.alerts ?? {};
  const queue = observability?.queue ?? {};
  const throughput = observability?.throughput ?? {};

  const throughputRows: ThroughputRow[] = [
    {
      key: "provider_fetch",
      label: "Provider 拉取",
      value:
        "调用 " + fmtNumber(throughput.provider_fetch?.calls) + " · 失败 " + fmtNumber(throughput.provider_fetch?.errors) +
        " · 行 " + fmtNumber(throughput.provider_fetch?.rows) + " · " + fmtBytes(throughput.provider_fetch?.bytes),
      source: text(throughput.provider_fetch?.source),
    },
    { key: "pipeline", label: "Pipeline 入库", value: "行数 " + fmtNumber(throughput.pipeline?.rows), source: text(throughput.pipeline?.source) },
    {
      key: "job_wall_clock",
      label: "任务端到端",
      value: "平均 " + fmtNumber(throughput.job_wall_clock?.avg_seconds) + " 秒",
      source: text(throughput.job_wall_clock?.completed_jobs_source ?? throughput.job_wall_clock?.source),
    },
    { key: "export", label: "导出任务", value: "任务 " + fmtNumber(throughput.export?.jobs), source: text(throughput.export?.source) },
    { key: "download_events", label: "下载事件", value: "审计事件 " + fmtNumber(throughput.download_events?.audit_events), source: text(throughput.download_events?.source) },
  ];

  const alertDetails: Array<{ label: string; value: ReactNode }> = [
    { label: "P0", value: fmtNumber(alerts.P0) },
    { label: "P1", value: fmtNumber(alerts.P1) },
    { label: "P2", value: fmtNumber(alerts.P2) },
    { label: "P3", value: fmtNumber(alerts.P3) },
    { label: "开放合计", value: fmtNumber(alerts.open_total) },
    { label: "告警来源", value: text(alerts.source) },
    { label: "as_of", value: fmtDateTime(observability?.as_of) },
    { label: "window_start", value: fmtDateTime(observability?.window_start) },
    { label: "API 窗口", value: text(observability?.api?.window) },
    { label: "进程窗口重启归零", value: observability?.api?.resets_on_restart === true ? "是" : "否" },
    { label: "API 来源", value: text(observability?.api?.source ?? observability?.data_source?.api) },
    { label: "健康来源", value: text(observability?.data_source?.health) },
    { label: "审计来源", value: text(observability?.data_source?.audit) },
  ];

  return (
    <>
      <PageHeader
        title="运营总览"
        subtitle={data ? <>数据截至 <span className="mono">{new Date(data.as_of).toLocaleString("zh-CN")}</span></> : "正在读取平台状态"}
        actions={
          <>
            <button type="button" className="btn btn--sm" onClick={() => void load()}>
              刷新
            </button>
            <button
              type="button"
              className="btn btn--sm"
              onClick={() =>
                dialog.notify("info", "指标口径", null, Object.entries(data?.data_source ?? {}).map(([k, v]) => ({ label: k, value: v })))
              }
            >
              指标口径
            </button>
            <Link className="btn btn--sm" to="/observability">
              可观测性
            </Link>
          </>
        }
      />
      {loading ? (
        <SkeletonRows rows={3} columns={4} />
      ) : error ? (
        <Card>
          <ErrorState title="总览加载失败" message={error} onRetry={load} />
        </Card>
      ) : !data ? (
        <Card>
          <EmptyState title="暂无数据" />
        </Card>
      ) : (
        <>
          <div className="metric-grid">
            {data.cards.map((card) => (
              <Metric key={card.key} label={card.label} value={card.value} hint={card.unit} tone={toneFor(card.key)} />
            ))}
          </div>

          <Card
            title="告警优先级（开放中）"
            actions={
              <button
                type="button"
                className="btn btn--sm"
                onClick={() => dialog.notify("info", "告警与窗口口径", null, alertDetails)}
              >
                口径
              </button>
            }
          >
            <div className="metric-grid">
              {(["P0", "P1", "P2", "P3"] as const).map((level) => {
                const count = num(alerts[level]);
                return (
                  <div key={level} role="group" aria-label={level + " 未处理告警 " + count + " 条"}>
                    <Metric
                      label={level + " 告警"}
                      value={fmtNumber(count)}
                      tone={priorityMetricTone(level, count)}
                      hint={"来源 " + text(alerts.source)}
                    />
                  </div>
                );
              })}
              <div role="group" aria-label={"开放告警合计 " + num(alerts.open_total) + " 条"}>
                <Metric
                  label="开放合计"
                  value={fmtNumber(alerts.open_total)}
                  tone={num(alerts.open_total) > 0 ? "warn" : "ok"}
                  hint={"as_of " + fmtDateTime(observability?.as_of)}
                />
              </div>
            </div>
          </Card>

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
              <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>{"探针时间 " + fmtDateTime(health.checked_at)}</span>
              <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>{"开放告警 " + fmtNumber(health.open_alerts)}</span>
            </div>
          </Card>

          {observabilityError ? (
            <Card>
              <ErrorState title="可观测性总览加载失败" message={observabilityError} onRetry={load} />
            </Card>
          ) : (
            <>
              <Card title="队列与请求">
                <div className="metric-grid">
                  <Metric label="运行中任务" value={fmtNumber(num(queue.running))} tone={num(queue.running) > 0 ? "warn" : "neutral"} />
                  <Metric label="等待中任务" value={fmtNumber(num(queue.waiting))} tone={num(queue.waiting) > 0 ? "warn" : "neutral"} />
                  <Metric label="失败任务" value={fmtNumber(num(queue.failed))} tone={num(queue.failed) > 0 ? "danger" : "ok"} />
                  <Metric label="暂停任务" value={fmtNumber(num(queue.paused))} tone={num(queue.paused) > 0 ? "warn" : "neutral"} />
                  <Metric label="请求总数" value={fmtNumber(observability?.api?.requests_total)} tone="neutral" hint={text(observability?.api?.window)} />
                  <Metric
                    label="错误率（5xx）"
                    value={fmtPercent(observability?.api?.error_rate)}
                    tone={num(observability?.api?.error_rate) > 0.01 ? "danger" : num(observability?.api?.error_rate) > 0 ? "warn" : "ok"}
                  />
                  <Metric label="在途请求" value={fmtNumber(observability?.api?.inflight)} tone="neutral" />
                  <Metric
                    label="窗口起点"
                    value={<span style={{ fontSize: "var(--fs-16)" }}>{fmtDateTime(observability?.window_start)}</span>}
                    tone="neutral"
                    hint={"重启归零 " + (observability?.api?.resets_on_restart === true ? "是" : "否")}
                  />
                </div>
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

              <Card title="近期高风险动作" flush>
                <DataTable<HighRiskRow>
                  rows={observability?.recent_high_risk ?? []}
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
                      render: (row) => (
                        <span className="mono" style={{ fontSize: "var(--fs-11)" }}>
                          {(row.resource_type ?? "—") + " " + (row.resource_id ?? "")}
                        </span>
                      ),
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

          <Card title="平台状态">
            <div className="toolbar">
              <StatusBadge tone={data.status === "ok" ? "ok" : "warn"}>{data.status === "ok" ? "正常" : "注意"}</StatusBadge>
              <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>
                当前身份角色：{(data.my_roles ?? []).join(" / ") || "—"}
              </span>
              <span className="shell__spacer" />
              <Link className="btn btn--sm" to="/system">
                系统健康检查
              </Link>
              <Link className="btn btn--sm" to="/audit">
                审计日志
              </Link>
              <Link className="btn btn--sm" to="/login-events">
                登录记录
              </Link>
            </div>
          </Card>
        </>
      )}
    </>
  );
}
