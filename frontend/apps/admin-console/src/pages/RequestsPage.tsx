import { useState, type ReactNode } from "react";
import { ApiError, useAuth } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  EntityLink,
  Field,
  PageHeader,
  RelativeTime,
  StatusBadge,
  statusToneFor,
  useDialog,
} from "@chain/ui";
import { fmtBytes, fmtDateTime, fmtMillis, fmtNumber, useAdminList } from "../lib/adminApi";
import { JsonBlock, KeyValueList } from "../components/s6";

/**
 * A-REQ-01：请求审计（路由 /requests，权限 admin:request:read）。
 *
 * 数据来源：GET /api/admin/v1/requests（provider_usage 真实行，cursor = request_time + id）。
 * 详情：GET /api/admin/v1/requests/{id}（含 shard_id / retry_* / related_audit_events）。
 * 所有提示、口径与错误只通过弹窗呈现；页面正文只有业务数据、字段标签与操作控件。
 */

interface RequestRow {
  id: string;
  provider?: string;
  method?: string;
  dataset?: string;
  status?: string;
  http_status?: number;
  duration_ms?: number;
  error?: string;
  error_class?: string;
  trace_id?: string;
  request_id?: string;
  usage_type?: string;
  rows?: number;
  bytes?: number;
  cost_usd?: number | string;
  request_time?: string;
  chain?: string;
  cache_hit?: boolean;
  job_id?: string;
  tenant_id?: string;
}

interface RequestDetail extends RequestRow {
  shard_id?: string;
  retry_attempt?: number;
  retry_reason?: string;
  page_size?: number;
  related_audit_events?: unknown;
  data_source?: string;
}

function shortId(value: string): string {
  return value.length > 18 ? value.slice(0, 10) + "…" + value.slice(-6) : value;
}

function errorDetails(error: unknown): Array<{ label: string; value: ReactNode }> {
  if (!(error instanceof ApiError)) return [];
  const details: Array<{ label: string; value: ReactNode }> = [];
  if (error.status) details.push({ label: "HTTP", value: String(error.status) });
  if (error.code) details.push({ label: "错误码", value: error.code });
  if (error.traceId) details.push({ label: "Trace", value: error.traceId });
  return details;
}

export default function RequestsPage() {
  const dialog = useDialog();
  const { client } = useAuth();
  const list = useAdminList<RequestRow>("/api/admin/v1/requests", {}, 20);

  const [draftProvider, setDraftProvider] = useState("");
  const [draftStatus, setDraftStatus] = useState("");
  const [draftUsageType, setDraftUsageType] = useState("");
  const [detailLoadingId, setDetailLoadingId] = useState<string | null>(null);

  const applyFilters = () => {
    list.setFilter({
      provider: draftProvider.trim() || undefined,
      status: draftStatus.trim() || undefined,
      usage_type: draftUsageType.trim() || undefined,
    });
  };

  const resetFilters = () => {
    setDraftProvider("");
    setDraftStatus("");
    setDraftUsageType("");
    list.setFilter({ provider: undefined, status: undefined, usage_type: undefined });
  };

  const openTrace = (row: { trace_id?: string; request_id?: string; id?: string }) => {
    void dialog.open({
      kind: "info",
      title: "按 Trace 关联",
      body: null,
      details: [
        { label: "Trace ID", value: <EntityLink value={row.trace_id || ""} copyable={Boolean(row.trace_id)} /> },
        { label: "Request ID", value: <EntityLink value={row.request_id || ""} copyable={Boolean(row.request_id)} /> },
        { label: "请求记录 ID", value: <EntityLink value={row.id || ""} copyable={Boolean(row.id)} /> },
        { label: "关联方式", value: "trace_id / request_id 与审计事件、任务、分片共享同一标识" },
      ],
      actions: [{ label: "关闭", variant: "primary", result: "ok" }],
    });
  };

  const openDetail = async (row: RequestRow) => {
    setDetailLoadingId(row.id);
    try {
      const response = await client.get<RequestDetail>("/api/admin/v1/requests/" + encodeURIComponent(row.id));
      const detail = response.data;
      const related = detail.related_audit_events;
      const relatedNode: ReactNode = Array.isArray(related)
        ? <JsonBlock value={related} label={`审计事件（${related.length} 条）`} />
        : String(related ?? 0);
      await dialog.open({
        kind: "info",
        title: "请求详情 · " + shortId(detail.id),
        body: (
          <KeyValueList
            items={[
              { label: "请求 ID", value: <EntityLink value={detail.id} />, mono: true },
              { label: "Provider", value: detail.provider ?? "—" },
              { label: "方法", value: detail.method ?? "—" },
              { label: "Dataset", value: detail.dataset ?? "—" },
              { label: "用途", value: detail.usage_type ?? "—" },
              { label: "链", value: detail.chain ?? "—" },
              { label: "状态", value: <StatusBadge tone={statusToneFor(detail.status ?? "")}>{detail.status ?? "—"}</StatusBadge> },
              { label: "HTTP", value: detail.http_status ?? "—" },
              { label: "耗时", value: fmtMillis(detail.duration_ms) },
              { label: "行数", value: fmtNumber(detail.rows) },
              { label: "返回字节", value: fmtBytes(detail.bytes) },
              { label: "成本（USD）", value: typeof detail.cost_usd === "number" ? "$" + detail.cost_usd.toFixed(6) : detail.cost_usd ?? "—" },
              { label: "缓存命中", value: detail.cache_hit ? "是" : "否" },
              { label: "分片 ID", value: <EntityLink value={detail.shard_id || ""} copyable={Boolean(detail.shard_id)} /> },
              { label: "任务 ID", value: <EntityLink value={detail.job_id || ""} copyable={Boolean(detail.job_id)} /> },
              { label: "重试次数", value: detail.retry_attempt ?? 0 },
              { label: "重试原因", value: detail.retry_reason || "—" },
              { label: "分页大小", value: detail.page_size ?? "—" },
              { label: "租户", value: detail.tenant_id || "—" },
              { label: "请求时间", value: fmtDateTime(detail.request_time) },
              { label: "错误类", value: detail.error_class || "—" },
              { label: "错误信息", value: <span className="mono">{detail.error || "—"}</span> },
              { label: "关联审计事件", value: relatedNode },
              { label: "数据来源", value: detail.data_source ?? "postgres:provider_usage" },
            ]}
          />
        ),
        actions: [{ label: "关闭", variant: "primary", result: "ok" }],
      });
    } catch (loadError) {
      dialog.notify(
        "error",
        "读取请求详情失败",
        loadError instanceof ApiError ? loadError.userMessage : "请稍后重试",
        errorDetails(loadError),
      );
    } finally {
      setDetailLoadingId(null);
    }
  };

  const explainScope = () => {
    dialog.notify("info", "请求审计口径", null, [
      { label: "接口", value: "GET /api/admin/v1/requests" },
      { label: "数据源", value: "postgres:provider_usage" },
      { label: "分页", value: "cursor = request_time + id，服务端生成" },
      { label: "排序", value: "request_time DESC, id DESC" },
      { label: "金额", value: "cost_usd 为估算美元金额，非账单金额" },
      { label: "筛选", value: "provider / status / usage_type 为精确匹配" },
    ]);
  };

  return (
    <>
      <PageHeader
        title="请求审计"
        subtitle="Provider 请求明细 · 来源 /api/admin/v1/requests"
        actions={
          <>
            <button type="button" className="btn btn--sm" onClick={explainScope}>
              口径
            </button>
            <button type="button" className="btn btn--sm" onClick={list.reload} disabled={list.loading}>
              刷新
            </button>
          </>
        }
      />

      <Card title="筛选">
        <form
          className="toolbar"
          style={{ alignItems: "flex-end" }}
          onSubmit={(event) => {
            event.preventDefault();
            applyFilters();
          }}
        >
          <div style={{ flex: "1 1 160px", minWidth: 0 }}>
            <Field label="Provider" htmlFor="req-provider">
              <input
                id="req-provider"
                className="input"
                placeholder="ankr / quicknode"
                value={draftProvider}
                onChange={(event) => setDraftProvider(event.target.value)}
              />
            </Field>
          </div>
          <div style={{ flex: "1 1 150px", minWidth: 0 }}>
            <Field label="状态" htmlFor="req-status">
              <input
                id="req-status"
                className="input"
                placeholder="success / error"
                value={draftStatus}
                onChange={(event) => setDraftStatus(event.target.value)}
              />
            </Field>
          </div>
          <div style={{ flex: "1 1 150px", minWidth: 0 }}>
            <Field label="用途" htmlFor="req-usage">
              <input
                id="req-usage"
                className="input"
                placeholder="rpc / logs / trace"
                value={draftUsageType}
                onChange={(event) => setDraftUsageType(event.target.value)}
              />
            </Field>
          </div>
          <div className="toolbar">
            <button type="submit" className="btn btn--sm btn--primary" disabled={list.loading}>
              查询
            </button>
            <button type="button" className="btn btn--sm" onClick={resetFilters} disabled={list.loading}>
              重置
            </button>
          </div>
        </form>
      </Card>

      <Card title={`请求记录（本页 ${list.items.length}）`} flush>
        <DataTable<RequestRow>
          loading={list.loading}
          error={list.error}
          onRetry={list.reload}
          rows={list.items}
          rowKey={(row) => row.id}
          empty={<EmptyState title="没有符合条件的请求记录" />}
          columns={[
            {
              key: "time",
              header: "时间",
              width: "170px",
              render: (row) => (
                <span>
                  <div>{fmtDateTime(row.request_time)}</div>
                  <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>
                    <RelativeTime value={row.request_time} />
                  </div>
                </span>
              ),
            },
            {
              key: "provider",
              header: "Provider / 方法",
              width: "170px",
              render: (row) => (
                <span>
                  <StatusBadge tone="info">{row.provider || "—"}</StatusBadge>
                  <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>
                    {row.method || "—"}
                  </div>
                </span>
              ),
            },
            {
              key: "dataset",
              header: "Dataset / 用途",
              width: "180px",
              render: (row) => (
                <span>
                  <div>{row.dataset || "—"}</div>
                  <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>{row.usage_type || "—"}</div>
                </span>
              ),
            },
            {
              key: "status",
              header: "状态",
              width: "150px",
              render: (row) => (
                <span>
                  <StatusBadge tone={statusToneFor(row.status ?? "")}>{row.status || "—"}</StatusBadge>
                  <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>
                    HTTP {row.http_status ?? "—"}
                    {row.cache_hit ? " · 缓存" : ""}
                  </div>
                </span>
              ),
            },
            {
              key: "duration",
              header: "耗时",
              width: "90px",
              align: "right",
              render: (row) => <span className="num">{fmtMillis(row.duration_ms)}</span>,
            },
            {
              key: "rows",
              header: "行数 / 字节",
              width: "120px",
              align: "right",
              render: (row) => (
                <span>
                  <div className="num">{fmtNumber(row.rows)}</div>
                  <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>
                    {fmtBytes(row.bytes)}
                  </div>
                </span>
              ),
            },
            {
              key: "error",
              header: "错误",
              width: "170px",
              render: (row) => (
                <span className="mono" style={{ fontSize: "var(--fs-11)" }} title={row.error || ""}>
                  {row.error_class || row.error || "—"}
                </span>
              ),
            },
            {
              key: "cost",
              header: "成本",
              width: "110px",
              align: "right",
              render: (row) => (
                <span className="num">
                  {typeof row.cost_usd === "number" ? "$" + row.cost_usd.toFixed(6) : row.cost_usd ?? "—"}
                </span>
              ),
            },
            {
              key: "trace",
              header: "Trace",
              width: "160px",
              render: (row) => (
                <span className="mono" style={{ fontSize: "var(--fs-11)" }} title={row.trace_id || ""}>
                  {row.trace_id ? shortId(row.trace_id) : "—"}
                </span>
              ),
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
                    disabled={detailLoadingId === row.id}
                    onClick={() => void openDetail(row)}
                  >
                    详情
                  </button>
                  <button type="button" className="btn btn--sm btn--ghost" onClick={() => openTrace(row)}>
                    Trace 关联
                  </button>
                </span>
              ),
            },
          ]}
        />
        <div className="toolbar" style={{ padding: "var(--sp-3) var(--sp-4)", justifyContent: "space-between" }}>
          <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>
            服务端 cursor 分页 · 本页 {list.items.length} 条
          </span>
          <span className="toolbar">
            <button
              type="button"
              className="btn btn--sm"
              disabled={!list.hasPrevious || list.loading}
              onClick={list.goPrevious}
            >
              上一页
            </button>
            <button
              type="button"
              className="btn btn--sm"
              disabled={!list.nextCursor || list.loading}
              onClick={list.goNext}
            >
              下一页
            </button>
          </span>
        </div>
      </Card>
    </>
  );
}
