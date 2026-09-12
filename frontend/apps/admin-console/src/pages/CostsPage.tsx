import { useEffect, useRef, useState, type ReactNode } from "react";
import { ApiError } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  Field,
  Metric,
  PageHeader,
  RelativeTime,
  StatusBadge,
  useDialog,
} from "@chain/ui";
import { fmtNumber, fmtUSD, useAdminResource } from "../lib/adminApi";

/**
 * A-COST-01：成本（路由 /costs，权限 admin:cost:read）。
 *
 * 数据来源：GET /api/admin/v1/costs/summary?days=30。
 * 金额一律使用后端返回的十进制字符串（amount_type=decimal-string），前端不做浮点再运算。
 * 按日趋势使用 DataTable 呈现，不引入图表库；所有口径说明只通过弹窗呈现。
 */

interface ProviderCost {
  provider?: string;
  cost?: string;
  calls?: number;
  credits?: number;
}

interface UsageCost {
  usage_type?: string;
  cost?: string;
  calls?: number;
}

interface DailyCost {
  day?: string;
  cost?: string;
  calls?: number;
}

interface CostSummary {
  window_days?: number;
  total_cost?: string;
  cost_24h?: string;
  cost_7d?: string;
  calls?: number;
  errors?: number;
  currency?: string;
  amount_type?: string;
  by_provider?: ProviderCost[];
  by_usage_type?: UsageCost[];
  daily?: DailyCost[];
  as_of?: string;
  data_source?: string;
}

const WINDOW_OPTIONS = [7, 30, 90, 365];

function errorDetails(error: unknown): Array<{ label: string; value: ReactNode }> {
  if (!(error instanceof ApiError)) return [];
  const details: Array<{ label: string; value: ReactNode }> = [];
  if (error.status) details.push({ label: "HTTP", value: String(error.status) });
  if (error.code) details.push({ label: "错误码", value: error.code });
  if (error.traceId) details.push({ label: "Trace", value: error.traceId });
  return details;
}

/** toNumeric 只用于计算展示比例，不参与金额本身的展示与比较。 */
function toNumeric(value: unknown): number {
  if (typeof value === "number") return value;
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : 0;
  }
  return 0;
}

export default function CostsPage() {
  const dialog = useDialog();
  const [days, setDays] = useState(30);
  const state = useAdminResource<CostSummary>("/api/admin/v1/costs/summary?days=" + days);

  const notified = useRef<string | null>(null);
  useEffect(() => {
    if (!state.error) {
      notified.current = null;
      return;
    }
    if (notified.current === state.error) return;
    notified.current = state.error;
    dialog.notify("error", "成本汇总读取失败", state.error);
  }, [dialog, state.error]);

  const summary = state.data;
  const providers = summary?.by_provider ?? [];
  const usages = summary?.by_usage_type ?? [];
  const daily = summary?.daily ?? [];
  const dailyPeak = daily.reduce((max, row) => Math.max(max, toNumeric(row.cost)), 0);

  const explainScope = () => {
    dialog.notify("info", "成本口径", null, [
      { label: "接口", value: "GET /api/admin/v1/costs/summary" },
      { label: "数据来源", value: summary?.data_source ?? "postgres:provider_usage + provider_pricing" },
      { label: "金额类型", value: summary?.amount_type ?? "decimal-string" },
      { label: "币种", value: summary?.currency ?? "USD" },
      { label: "金额表示", value: "后端返回十进制字符串（例如 0.012345），前端不做浮点运算" },
      { label: "统计窗口", value: (summary?.window_days ?? days) + " 天（含当天）" },
      { label: "错误数口径", value: "窗口内 status 不等于 success 的请求数" },
      { label: "生成时间", value: summary?.as_of ?? "—" },
    ]);
  };

  const openProviderDetail = (row: ProviderCost) => {
    dialog.notify("info", "Provider 成本 · " + (row.provider ?? "—"), null, [
      { label: "Provider", value: row.provider ?? "—" },
      { label: "窗口成本", value: fmtUSD(row.cost) },
      { label: "窗口调用数", value: fmtNumber(row.calls) },
      { label: "Credits", value: fmtNumber(row.credits) },
      { label: "统计窗口", value: (summary?.window_days ?? days) + " 天" },
    ]);
  };

  const openUsageDetail = (row: UsageCost) => {
    dialog.notify("info", "用途成本 · " + (row.usage_type ?? "—"), null, [
      { label: "用途", value: row.usage_type ?? "—" },
      { label: "窗口成本", value: fmtUSD(row.cost) },
      { label: "窗口调用数", value: fmtNumber(row.calls) },
      { label: "统计窗口", value: (summary?.window_days ?? days) + " 天" },
    ]);
  };

  return (
    <>
      <PageHeader
        title="成本"
        subtitle={"窗口 " + (summary?.window_days ?? days) + " 天 · 来源 " + (summary?.data_source ?? "/api/admin/v1/costs/summary")}
        actions={
          <>
            <button type="button" className="btn btn--sm" onClick={explainScope}>
              口径
            </button>
            <button type="button" className="btn btn--sm" onClick={state.reload} disabled={state.loading}>
              刷新
            </button>
          </>
        }
      />

      <Card title="统计窗口">
        <div className="toolbar" style={{ alignItems: "flex-end" }}>
          <div style={{ flex: "0 1 160px", minWidth: 0 }}>
            <Field label="窗口天数" htmlFor="costs-days">
              <select
                id="costs-days"
                className="select"
                value={String(days)}
                onChange={(event) => setDays(Number(event.target.value))}
              >
                {WINDOW_OPTIONS.map((option) => (
                  <option key={option} value={String(option)}>
                    {option} 天
                  </option>
                ))}
              </select>
            </Field>
          </div>
          <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>
            截至 {summary?.as_of ? new Date(summary.as_of).toLocaleString("zh-CN", { hour12: false }) : "—"}
          </span>
        </div>
      </Card>

      <div className="metric-grid">
        <Metric label={"窗口总成本（" + (summary?.window_days ?? days) + " 天）"} value={fmtUSD(summary?.total_cost)} hint={summary?.currency ?? "USD"} />
        <Metric label="24 小时成本" value={fmtUSD(summary?.cost_24h)} />
        <Metric label="7 天成本" value={fmtUSD(summary?.cost_7d)} />
        <Metric label="调用数" value={fmtNumber(summary?.calls)} />
        <Metric
          label="错误数"
          value={fmtNumber(summary?.errors)}
          tone={(summary?.errors ?? 0) > 0 ? "warn" : "ok"}
        />
      </div>

      <Card title={"按 Provider（" + providers.length + "）"} flush>
        <DataTable<ProviderCost>
          loading={state.loading}
          error={state.error}
          onRetry={state.reload}
          rows={providers}
          rowKey={(row) => row.provider ?? "unknown"}
          empty={<EmptyState title="窗口内没有 Provider 成本记录" />}
          columns={[
            {
              key: "provider",
              header: "Provider",
              render: (row) => <StatusBadge tone="info">{row.provider ?? "—"}</StatusBadge>,
            },
            { key: "cost", header: "成本", width: "160px", align: "right", render: (row) => <span className="num">{fmtUSD(row.cost)}</span> },
            { key: "calls", header: "调用数", width: "120px", align: "right", render: (row) => <span className="num">{fmtNumber(row.calls)}</span> },
            {
              key: "credits",
              header: "Credits",
              width: "120px",
              align: "right",
              render: (row) => <span className="num">{fmtNumber(row.credits)}</span>,
            },
            {
              key: "actions",
              header: "操作",
              width: "100px",
              align: "right",
              render: (row) => (
                <button type="button" className="btn btn--sm" onClick={() => openProviderDetail(row)}>
                  明细
                </button>
              ),
            },
          ]}
        />
      </Card>

      <Card title={"按用途（" + usages.length + "）"} flush>
        <DataTable<UsageCost>
          loading={state.loading}
          error={state.error}
          onRetry={state.reload}
          rows={usages}
          rowKey={(row) => row.usage_type ?? "unknown"}
          empty={<EmptyState title="窗口内没有用途成本记录" />}
          columns={[
            {
              key: "usage_type",
              header: "用途",
              render: (row) => <StatusBadge tone="neutral">{row.usage_type ?? "—"}</StatusBadge>,
            },
            { key: "cost", header: "成本", width: "170px", align: "right", render: (row) => <span className="num">{fmtUSD(row.cost)}</span> },
            { key: "calls", header: "调用数", width: "130px", align: "right", render: (row) => <span className="num">{fmtNumber(row.calls)}</span> },
            {
              key: "actions",
              header: "操作",
              width: "100px",
              align: "right",
              render: (row) => (
                <button type="button" className="btn btn--sm" onClick={() => openUsageDetail(row)}>
                  明细
                </button>
              ),
            },
          ]}
        />
      </Card>

      <Card title={"按日趋势（" + daily.length + " 天）"} flush>
        <DataTable<DailyCost>
          loading={state.loading}
          error={state.error}
          onRetry={state.reload}
          rows={daily}
          rowKey={(row) => row.day ?? "unknown"}
          empty={<EmptyState title="窗口内没有按日成本记录" />}
          columns={[
            {
              key: "day",
              header: "日期",
              width: "140px",
              render: (row) => (
                <span>
                  <div className="mono">{row.day ?? "—"}</div>
                  <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>
                    <RelativeTime value={row.day ? row.day + "T00:00:00Z" : null} />
                  </div>
                </span>
              ),
            },
            {
              key: "trend",
              header: "占比",
              render: (row) => {
                const ratio = dailyPeak > 0 ? Math.max(0, Math.min(1, toNumeric(row.cost) / dailyPeak)) : 0;
                return (
                  <span
                    aria-hidden="true"
                    style={{ display: "block", background: "var(--c-surface-sunken)", height: "6px", borderRadius: "3px", maxWidth: "220px" }}
                  >
                    <span
                      style={{
                        display: "block",
                        width: (ratio * 100).toFixed(1) + "%",
                        height: "6px",
                        borderRadius: "3px",
                        background: "var(--c-cobalt)",
                      }}
                    />
                  </span>
                );
              },
            },
            { key: "cost", header: "成本", width: "160px", align: "right", render: (row) => <span className="num">{fmtUSD(row.cost)}</span> },
            { key: "calls", header: "调用数", width: "130px", align: "right", render: (row) => <span className="num">{fmtNumber(row.calls)}</span> },
          ]}
        />
      </Card>
    </>
  );
}
