import { useCallback, useEffect, useMemo, useState } from "react";
import { ApiError, useAuth } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  ErrorState,
  PageHeader,
  RelativeTime,
  SkeletonRows,
  StatusBadge,
  statusToneFor,
  useDialog,
} from "@chain/ui";
import "../risk.css";

/** §19.2 A-RISK-01 风险事件：把"谁试图进来、被什么拦下"放在一屏里看清楚。
 *  数据全部来自 login_events 的真实聚合（risk_level / result / reason_code / device / ip），
 *  口径与筛选一律由服务端执行；本页不做任何占位或推测。 */

interface RiskTotals {
  events: number;
  critical: number;
  high: number;
  medium: number;
  blocked: number;
  denied: number;
  failure: number;
  affected_identities: number;
  affected_ips: number;
}

interface RiskReasonRow {
  reason_code: string;
  events: number;
  critical: number;
  blocked: number;
  last_seen_at: string;
}

interface RiskHourRow {
  hour: string;
  events: number;
  blocked: number;
}

interface RiskIPRow {
  client_ip: string;
  events: number;
  critical: number;
  subjects: number;
  last_seen_at: string;
}

interface RiskSummary {
  as_of: string;
  window_hours: number;
  levels: string[];
  totals: RiskTotals;
  by_reason: RiskReasonRow[];
  by_hour: RiskHourRow[];
  top_ips: RiskIPRow[];
}

interface RiskEventRow {
  id: string;
  occurred_at: string;
  risk_level: string;
  result: string;
  reason_code: string;
  risk_action: string;
  subject_masked: string;
  identity_id?: string;
  audience: string;
  auth_method: string;
  client_ip: string;
  entry_host: string;
  device_label: string;
  browser_name: string;
  os_name: string;
  is_new_device: boolean;
  session_id?: string;
  request_id: string;
  trace_id: string;
}

const WINDOWS = [
  { hours: 24, label: "24 小时" },
  { hours: 168, label: "7 天" },
  { hours: 720, label: "30 天" },
];

const LEVELS = [
  { value: "critical", label: "仅危急" },
  { value: "high", label: "高危及以上" },
  { value: "medium", label: "中危及以上" },
  { value: "all", label: "全部等级" },
];

const REASON_LABELS: Record<string, string> = {
  bad_password: "口令错误",
  unknown_account: "账号不存在",
  account_locked: "账号锁定",
  rate_limited: "触发限流",
  token_reuse: "会话令牌重放",
  workspace_access_revoked: "工作区访问被撤销",
  workspace_selection_required: "需显式选择工作区",
  session_revoked: "会话被撤销",
};

function reasonLabel(code: string): string {
  if (!code) return "未标注原因";
  const label = REASON_LABELS[code];
  return label ? label + " · " + code : code;
}

function levelRank(level: string): number {
  switch (level) {
    case "critical":
      return 3;
    case "high":
      return 2;
    case "medium":
      return 1;
    default:
      return 0;
  }
}

function severityTone(level: string): "danger" | "warn" | "info" | "neutral" {
  if (level === "critical") return "danger";
  if (level === "high") return "warn";
  if (level === "medium") return "info";
  return "neutral";
}

/** 把窗口切成等宽小时桶：没有事件的小时也要占位，读数才不会被"看起来连续"骗过去。 */
function hourlyBuckets(rows: RiskHourRow[], windowHours: number) {
  const byHour = new Map<number, RiskHourRow>();
  for (const row of rows) {
    const at = new Date(row.hour);
    at.setMinutes(0, 0, 0);
    byHour.set(at.getTime(), row);
  }
  const now = new Date();
  now.setMinutes(0, 0, 0);
  const buckets: Array<{ at: Date; events: number; blocked: number }> = [];
  for (let i = windowHours - 1; i >= 0; i -= 1) {
    const at = new Date(now.getTime() - i * 3600_000);
    const hit = byHour.get(at.getTime());
    buckets.push({ at, events: hit?.events ?? 0, blocked: hit?.blocked ?? 0 });
  }
  return buckets;
}

export default function RiskPage() {
  const { client } = useAuth();
  const dialog = useDialog();
  const [summary, setSummary] = useState<RiskSummary | null>(null);
  const [events, setEvents] = useState<RiskEventRow[]>([]);
  const [total, setTotal] = useState(0);
  const [windowHours, setWindowHours] = useState(168);
  const [level, setLevel] = useState("high");
  const [reason, setReason] = useState("");
  const [hourKey, setHourKey] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [summaryRes, eventRes] = await Promise.all([
        client.get<RiskSummary>("/api/admin/v1/risk/summary", { query: { window_hours: windowHours, level } }),
        client.get<RiskEventRow[]>("/api/admin/v1/risk/events", {
          query: { window_hours: windowHours, level, reason_code: reason, limit: 200 },
        }),
      ]);
      setSummary(summaryRes.data ?? null);
      setEvents(eventRes.data ?? []);
      setTotal(eventRes.meta.total ?? eventRes.data?.length ?? 0);
    } catch (err) {
      setError(err instanceof ApiError ? err.userMessage : "无法加载风险事件");
    } finally {
      setLoading(false);
    }
  }, [client, windowHours, level, reason]);

  useEffect(() => {
    void load();
  }, [load]);

  const buckets = useMemo(
    () => (summary ? hourlyBuckets(summary.by_hour, Math.min(windowHours, 240)) : []),
    [summary, windowHours],
  );

  const hourIndex = useMemo(() => {
    const index = new Map<number, RiskHourRow>();
    for (const row of summary?.by_hour ?? []) {
      const at = new Date(row.hour);
      at.setMinutes(0, 0, 0);
      index.set(at.getTime(), row);
    }
    return index;
  }, [summary]);

  const visibleEvents = useMemo(() => {
    if (hourKey === null) return events;
    return events.filter((row) => {
      const at = new Date(row.occurred_at);
      at.setMinutes(0, 0, 0);
      return at.getTime() === hourKey;
    });
  }, [events, hourKey]);

  const maxReason = summary?.by_reason[0]?.events ?? 1;
  const maxIP = summary?.top_ips[0]?.events ?? 1;
  const totals = summary?.totals;
  const handled = (totals?.blocked ?? 0) + (totals?.denied ?? 0);
  const share = totals && totals.events > 0 ? handled / totals.events : 0;

  const explain = () =>
    dialog.open({
      kind: "info",
      title: "这一页的数字是怎么来的",
      body: null,
      details: [
        { label: "数据来源", value: "login_events（认证链路逐条写入，含风险等级、结果、原因码、设备与来源 IP）" },
        {
          label: "风险事件口径",
          value: "结果不是 success（blocked / denied / failure），或风险等级落在当前所选等级内",
        },
        { label: "时间窗口", value: "按服务端时间回溯，「小时轨」按整点分桶，无事件的小时保留空位" },
        { label: "拦截含义", value: "「已阻断/已拒绝」是系统实际拒绝的次数，不是策略建议次数" },
        { label: "作用域", value: "非平台范围的管理员只能看到本组织的事件" },
      ],
    });

  const showDetail = async (row: RiskEventRow) => {
    await dialog.open({
      kind: severityTone(row.risk_level) === "danger" ? "warning" : "info",
      title: `${reasonLabel(row.reason_code)} · ${row.result}`,
      body: null,
      details: [
        { label: "发生时间（UTC）", value: new Date(row.occurred_at).toISOString() },
        { label: "风险等级", value: <StatusBadge tone={severityTone(row.risk_level)}>{row.risk_level}</StatusBadge> },
        { label: "结果", value: <StatusBadge tone={statusToneFor(row.result)}>{row.result}</StatusBadge> },
        { label: "原因码", value: <span className="mono">{row.reason_code || "—"}</span> },
        { label: "风险动作", value: <span className="mono">{row.risk_action || "—"}</span> },
        { label: "主体", value: <span className="mono">{row.subject_masked || "—"}</span> },
        { label: "身份 ID", value: <span className="mono">{row.identity_id || "—"}</span> },
        { label: "会话受众 / 认证方式", value: `${row.audience || "—"} / ${row.auth_method || "—"}` },
        { label: "来源 IP", value: <span className="mono">{row.client_ip || "—"}</span> },
        { label: "入口", value: <span className="mono">{row.entry_host || "—"}</span> },
        {
          label: "设备",
          value: [row.device_label, row.os_name, row.browser_name].filter(Boolean).join(" · ") || "—",
        },
        { label: "新设备", value: row.is_new_device ? "是" : "否" },
        { label: "会话 ID", value: <span className="mono">{row.session_id || "—"}</span> },
        { label: "请求 ID", value: <span className="mono">{row.request_id || "—"}</span> },
        { label: "Trace ID", value: <span className="mono">{row.trace_id || "—"}</span> },
      ],
    });
  };

  return (
    <div className="risk-page">
      <PageHeader
        title="风险事件"
        subtitle="谁试图进来、被什么拦下：认证链路里的异常与拦截，按小时可下钻。"
        actions={
          <button type="button" className="btn btn--ghost" onClick={explain}>
            口径说明
          </button>
        }
      />

      <div className="risk-toolbar">
        <div>
          <span className="risk-seg__label">时间窗口</span>
          <div className="risk-seg" role="group" aria-label="时间窗口">
            {WINDOWS.map((item) => (
              <button
                key={item.hours}
                type="button"
                aria-pressed={windowHours === item.hours}
                onClick={() => {
                  setHourKey(null);
                  setWindowHours(item.hours);
                }}
              >
                {item.label}
              </button>
            ))}
          </div>
        </div>
        <div>
          <span className="risk-seg__label">风险等级</span>
          <div className="risk-seg" role="group" aria-label="风险等级">
            {LEVELS.map((item) => (
              <button
                key={item.value}
                type="button"
                aria-pressed={level === item.value}
                onClick={() => setLevel(item.value)}
              >
                {item.label}
              </button>
            ))}
          </div>
        </div>
      </div>

      {error ? <ErrorState title="风险事件加载失败" message={error} onRetry={() => void load()} /> : null}

      {loading && !summary ? <SkeletonRows rows={4} columns={4} /> : null}

      {summary ? (
        <>
          <div className="risk-kpis">
            <div className="risk-kpi" data-tone="danger">
              <div className="risk-kpi__label">危急事件</div>
              <div className="risk-kpi__value">{summary.totals.critical}</div>
              <div className="risk-kpi__hint">高危 {summary.totals.high} · 中危 {summary.totals.medium}</div>
            </div>
            <div className="risk-kpi" data-tone="ok">
              <div className="risk-kpi__label">已阻断 / 已拒绝</div>
              <div className="risk-kpi__value">{handled}</div>
              <div className="risk-kpi__hint">占全部事件 {(share * 100).toFixed(0)}%</div>
            </div>
            <div className="risk-kpi" data-tone="info">
              <div className="risk-kpi__label">受影响身份</div>
              <div className="risk-kpi__value">{summary.totals.affected_identities}</div>
              <div className="risk-kpi__hint">失败 {summary.totals.failure} 次</div>
            </div>
            <div className="risk-kpi" data-tone="warn">
              <div className="risk-kpi__label">来源 IP</div>
              <div className="risk-kpi__value">{summary.totals.affected_ips}</div>
              <div className="risk-kpi__hint">窗口 {summary.window_hours} 小时</div>
            </div>
          </div>

          <Card title="严重度时间轨">
            <div className="risk-rail">
              <div className="risk-rail__track" role="group" aria-label="按小时的最高严重度">
                {buckets.map((bucket) => {
                  const hit = hourIndex.get(bucket.at.getTime());
                  const levelForHour =
                    hit && hit.blocked > 0 ? "critical" : hit && hit.events > 0 ? "high" : "low";
                  const height = 6 + Math.min(44, Math.round((hit?.events ?? 0) / 3));
                  const selected = hourKey === bucket.at.getTime();
                  return (
                    <button
                      key={bucket.at.toISOString()}
                      type="button"
                      className="risk-rail__cell"
                      data-level={hit ? levelForHour : "low"}
                      aria-pressed={selected}
                      aria-label={`${bucket.at.toISOString()} 事件 ${hit?.events ?? 0} 次，拦截 ${hit?.blocked ?? 0} 次`}
                      style={{ height: hit ? height : 6 }}
                      onClick={() => setHourKey(selected ? null : bucket.at.getTime())}
                    />
                  );
                })}
              </div>
              <div className="risk-rail__axis">
                <span>{buckets[0] ? buckets[0].at.toISOString().slice(5, 16).replace("T", " ") : ""}</span>
                <span>{new Date().toISOString().slice(5, 16).replace("T", " ")} · UTC</span>
              </div>
              <div className="risk-legend">
                <span><i style={{ background: "var(--c-danger)" }} />该小时有拦截</span>
                <span><i style={{ background: "var(--c-amber)" }} />有事件、无拦截</span>
                <span><i style={{ background: "var(--c-border-strong)" }} />无事件（保留空位）</span>
                <span>柱高 = 该小时事件数</span>
              </div>
            </div>
          </Card>

          <div className="risk-grid">
            <Card title="原因码排行" actions={reason ? <span className="risk-chip">仅看 {reason}<button type="button" aria-label="清除原因筛 选" onClick={() => setReason("")}>×</button></span> : null}>
              <div className="risk-rank">
                {summary.by_reason.map((row) => (
                  <button
                    key={row.reason_code || "unknown"}
                    type="button"
                    className="risk-rank__row"
                    aria-pressed={reason === row.reason_code}
                    onClick={() => setReason(reason === row.reason_code ? "" : row.reason_code)}
                  >
                    <span className="risk-rank__key">{reasonLabel(row.reason_code)}</span>
                    <span className="risk-rank__bar">
                      <span
                        className="risk-rank__fill"
                        data-hot={row.critical > 0}
                        data-warm={row.critical === 0 && row.blocked > 0}
                        style={{ width: `${Math.max(3, Math.round((row.events / maxReason) * 100))}%` }}
                      />
                    </span>
                    <span className="risk-rank__count">
                      {row.events} 次{row.critical > 0 ? ` · 危急 ${row.critical}` : ""}
                    </span>
                  </button>
                ))}
                {summary.by_reason.length === 0 ? (
                  <EmptyState title="该窗口内没有异常认证事件" />
                ) : null}
              </div>
            </Card>

            <Card title="来源排行">
              <div className="risk-rank">
                {summary.top_ips.map((row) => (
                  <div key={row.client_ip || "empty"} className="risk-rank__row" style={{ cursor: "default" }}>
                    <span className="risk-rank__key">{row.client_ip || "—"}</span>
                    <span className="risk-rank__bar">
                      <span
                        className="risk-rank__fill"
                        data-hot={row.critical > 0}
                        style={{ width: `${Math.max(3, Math.round((row.events / maxIP) * 100))}%` }}
                      />
                    </span>
                    <span className="risk-rank__count">
                      {row.events} 次 · {row.subjects} 主体
                    </span>
                  </div>
                ))}
                {summary.top_ips.length === 0 ? <EmptyState title="没有来源数据" /> : null}
              </div>
            </Card>
          </div>

          <Card
            title="事件明细"
            actions={
              <span className="risk-muted">
                共 {hourKey === null ? total : visibleEvents.length} 条
                {hourKey !== null ? "（已按所选小时下钻）" : ""}
                {reason ? ` · 原因 ${reason}` : ""}
              </span>
            }
          >
            <DataTable<RiskEventRow>
              columns={[
                {
                  key: "occurred_at",
                  header: "时间",
                  width: "132px",
                  render: (row) => <RelativeTime value={row.occurred_at} />,
                },
                {
                  key: "risk_level",
                  header: "等级",
                  width: "90px",
                  render: (row) => (
                    <span className="risk-row">
                      <i className="risk-dot" data-level={row.risk_level} />
                      {row.risk_level || "—"}
                    </span>
                  ),
                },
                {
                  key: "result",
                  header: "结果",
                  width: "96px",
                  render: (row) => <StatusBadge tone={statusToneFor(row.result)}>{row.result}</StatusBadge>,
                },
                {
                  key: "reason_code",
                  header: "原因",
                  render: (row) => <span className="mono">{row.reason_code || "—"}</span>,
                },
                {
                  key: "subject_masked",
                  header: "主体",
                  render: (row) => <span className="mono">{row.subject_masked || "—"}</span>,
                },
                {
                  key: "client_ip",
                  header: "来源",
                  width: "168px",
                  render: (row) => (
                    <span className="mono">
                      {row.client_ip || "—"}
                      {row.entry_host ? ` · ${row.entry_host}` : ""}
                    </span>
                  ),
                },
                {
                  key: "detail",
                  header: "取证",
                  width: "84px",
                  render: (row) => (
                    <button type="button" className="btn btn--ghost btn--sm" onClick={() => void showDetail(row)}>
                      详情
                    </button>
                  ),
                },
              ]}
              rows={visibleEvents}
              rowKey={(row) => row.id}
              loading={loading}
              error={null}
              empty={<EmptyState title="没有符合条件的风险事件" />}
              onRetry={() => void load()}
            />
          </Card>
        </>
      ) : null}
    </div>
  );
}
