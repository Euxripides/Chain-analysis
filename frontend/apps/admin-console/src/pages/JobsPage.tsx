import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ApiError, useAuth } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  Field,
  PageHeader,
  StatusBadge,
  statusToneFor,
  useDialog,
} from "@chain/ui";

/**
 * A-JOB-01 / S5-ADMIN-01：全局任务控制台（路由 /jobs）。
 * 跨组织列表：GET /api/admin/v1/jobs?status=&chain=&organization_id=&workspace_id=&cursor=&limit=
 * 列表使用服务端 meta.next_cursor 分页，不使用占位数据。
 * 所有说明 / 错误 / 确认只通过 Dialog 呈现（AGENTS.md）。
 */

interface AdminJobRow {
  id: string;
  job_no?: string;
  status?: string;
  chain?: string;
  datasets?: string[];
  source?: string;
  reuse_policy?: string;
  range_type?: string;
  from_block?: number | null;
  to_block?: number | null;
  requested_mode?: string;
  effective_mode?: string;
  mode_reason?: string;
  created_at?: string;
  updated_at?: string;
  progress?: number;
  addresses?: { total?: number; completed?: number };
  datasets_progress?: { total?: number };
  shards?: { total?: number; completed?: number; failed?: number };
  downloaded_rows?: number;
  downloaded_bytes?: number;
  last_success_at?: string | null;
  failure?: { kind?: string; message_safe?: string };
  organization_id?: string | null;
  workspace_id?: string | null;
  diagnostics_url?: string;
}

interface JobsEnvelope {
  items: AdminJobRow[];
}

const STATUS_OPTIONS = [
  "CREATED",
  "PLANNING",
  "QUEUED",
  "RUNNING",
  "WAITING_USER_CONFIRMATION",
  "PAUSE_REQUESTED",
  "PAUSED",
  "PAUSED_FALLBACK_TOO_EXPENSIVE",
  "RESUMING",
  "CANCEL_REQUESTED",
  "CANCELLED",
  "COMPLETED",
  "PARTIAL_FAILED",
  "FAILED",
  "ABORTED_BY_USER",
];

function shortId(value: string): string {
  return value.length > 18 ? `${value.slice(0, 10)}…${value.slice(-6)}` : value;
}

function formatBytes(value: number | null | undefined): string {
  if (value === null || value === undefined) return "—";
  if (value < 1024) return `${value} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let size = value / 1024;
  let index = 0;
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024;
    index += 1;
  }
  return `${size.toFixed(size >= 10 ? 1 : 2)} ${units[index]}`;
}

function formatTime(value?: string | null): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString("zh-CN", { hour12: false });
}

function progressText(value: number | undefined): string {
  if (typeof value !== "number" || Number.isNaN(value)) return "—";
  return `${Math.round(Math.min(1, Math.max(0, value)) * 100)}%`;
}

export default function JobsPage() {
  const navigate = useNavigate();
  const dialog = useDialog();
  const { client } = useAuth();

  const [status, setStatus] = useState("all");
  const [chain, setChain] = useState("");
  const [organizationId, setOrganizationId] = useState("");
  const [workspaceId, setWorkspaceId] = useState("");
  const [limit, setLimit] = useState(20);
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const [cursorHistory, setCursorHistory] = useState<Array<string | undefined>>([]);
  const [rows, setRows] = useState<AdminJobRow[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      setLoading(true);
      setError(null);
      try {
        const response = await client.get<JobsEnvelope>("/api/admin/v1/jobs", {
          query: {
            status: status === "all" ? undefined : status,
            chain: chain.trim() || undefined,
            organization_id: organizationId.trim() || undefined,
            workspace_id: workspaceId.trim() || undefined,
            cursor: cursor || undefined,
            limit,
          },
          signal,
        });
        if (signal?.aborted) return;
        setRows(response.data?.items ?? []);
        setNextCursor(response.meta.next_cursor ?? null);
      } catch (loadError) {
        if (signal?.aborted) return;
        if (loadError instanceof DOMException && loadError.name === "AbortError") return;
        setError(loadError instanceof ApiError ? loadError.userMessage : "读取全局任务失败");
      } finally {
        if (!signal?.aborted) setLoading(false);
      }
    },
    [chain, client, cursor, limit, organizationId, status, workspaceId],
  );

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  const applyFilter = (apply: () => void) => {
    apply();
    setCursor(undefined);
    setCursorHistory([]);
  };

  const goNext = () => {
    if (!nextCursor) return;
    setCursorHistory((history) => [...history, cursor]);
    setCursor(nextCursor);
  };

  const goPrevious = () => {
    setCursorHistory((history) => {
      if (history.length === 0) return history;
      const copy = [...history];
      const previous = copy.pop();
      setCursor(previous);
      return copy;
    });
  };

  const explainFilters = () => {
    dialog.notify("info", "全局任务筛选口径", "列表跨组织与工作区；时间顺序按 updated_at 倒序，cursor 由服务端生成。", [
      { label: "接口", value: "/api/admin/v1/jobs" },
      { label: "作用域", value: "全局（跨 organization / workspace）" },
      { label: "分页", value: "cursor = updated_at + id" },
      { label: "排序", value: "updated_at DESC, id DESC" },
    ]);
  };

  return (
    <>
      <PageHeader
        title="全局任务控制"
        subtitle="跨组织任务视图 · 来源 /api/admin/v1/jobs"
        actions={
          <>
            <button type="button" className="btn btn--sm" onClick={explainFilters}>
              筛选口径
            </button>
            <button type="button" className="btn btn--sm" onClick={() => void load()} disabled={loading}>
              刷新
            </button>
          </>
        }
      />

      <Card title="筛选">
        <div className="toolbar" style={{ alignItems: "flex-end" }}>
          <div style={{ flex: "1 1 170px", minWidth: 0 }}>
            <Field label="状态" htmlFor="jobs-status">
              <select
                id="jobs-status"
                className="select"
                value={status}
                onChange={(event) => applyFilter(() => setStatus(event.target.value))}
              >
                <option value="all">全部状态</option>
                {STATUS_OPTIONS.map((option) => (
                  <option key={option} value={option}>
                    {option}
                  </option>
                ))}
              </select>
            </Field>
          </div>
          <div style={{ flex: "1 1 150px", minWidth: 0 }}>
            <Field label="链" htmlFor="jobs-chain">
              <input
                id="jobs-chain"
                className="input"
                placeholder="tron / bsc / eth"
                value={chain}
                onChange={(event) => applyFilter(() => setChain(event.target.value))}
              />
            </Field>
          </div>
          <div style={{ flex: "1 1 220px", minWidth: 0 }}>
            <Field label="组织 ID" htmlFor="jobs-org">
              <input
                id="jobs-org"
                className="input mono"
                placeholder="organization_id"
                value={organizationId}
                onChange={(event) => applyFilter(() => setOrganizationId(event.target.value))}
              />
            </Field>
          </div>
          <div style={{ flex: "1 1 220px", minWidth: 0 }}>
            <Field label="工作区 ID" htmlFor="jobs-ws">
              <input
                id="jobs-ws"
                className="input mono"
                placeholder="workspace_id"
                value={workspaceId}
                onChange={(event) => applyFilter(() => setWorkspaceId(event.target.value))}
              />
            </Field>
          </div>
          <div style={{ flex: "0 1 120px", minWidth: 0 }}>
            <Field label="每页" htmlFor="jobs-limit">
              <select
                id="jobs-limit"
                className="select"
                value={String(limit)}
                onChange={(event) => applyFilter(() => setLimit(Number(event.target.value)))}
              >
                <option value="20">20</option>
                <option value="50">50</option>
                <option value="100">100</option>
              </select>
            </Field>
          </div>
        </div>
      </Card>

      <Card title={`任务（本页 ${rows.length}）`} flush>
        <DataTable<AdminJobRow>
          loading={loading}
          error={error}
          onRetry={() => void load()}
          rows={rows}
          rowKey={(row) => row.id}
          empty={<EmptyState title="没有符合条件的任务" />}
          columns={[
            {
              key: "job",
              header: "任务",
              render: (row) => (
                <span>
                  <strong>{row.job_no || shortId(row.id)}</strong>
                  <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }} title={row.id}>
                    {shortId(row.id)}
                  </div>
                </span>
              ),
            },
            {
              key: "scope",
              header: "组织 / 工作区",
              width: "230px",
              render: (row) => (
                <span>
                  <div className="mono" style={{ fontSize: "var(--fs-11)" }} title={row.organization_id ?? ""}>
                    {row.organization_id ? shortId(row.organization_id) : "—"}
                  </div>
                  <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }} title={row.workspace_id ?? ""}>
                    {row.workspace_id ? shortId(row.workspace_id) : "—"}
                  </div>
                </span>
              ),
            },
            {
              key: "chain",
              header: "链 / 数据集",
              width: "200px",
              render: (row) => (
                <span>
                  <StatusBadge tone="info">{row.chain ?? "—"}</StatusBadge>
                  <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>
                    {(row.datasets ?? []).join(", ") || "—"}
                  </div>
                </span>
              ),
            },
            {
              key: "status",
              header: "状态",
              width: "160px",
              render: (row) => (
                <StatusBadge tone={statusToneFor(row.status ?? "")}>{row.status ?? "—"}</StatusBadge>
              ),
            },
            {
              key: "progress",
              header: "进度 / 分片",
              width: "150px",
              render: (row) => (
                <span>
                  <div className="num" style={{ fontWeight: 600 }}>
                    {progressText(row.progress)}
                  </div>
                  <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>
                    {row.shards?.completed ?? 0}/{row.shards?.total ?? 0} · 失败 {row.shards?.failed ?? 0}
                  </div>
                </span>
              ),
            },
            {
              key: "volume",
              header: "行数 / bytes",
              width: "150px",
              align: "right",
              render: (row) => (
                <span>
                  <div className="num">{(row.downloaded_rows ?? 0).toLocaleString("zh-CN")}</div>
                  <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>
                    {formatBytes(row.downloaded_bytes)}
                  </div>
                </span>
              ),
            },
            {
              key: "mode",
              header: "模式",
              width: "130px",
              render: (row) => (
                <span>
                  <div>{row.effective_mode ?? row.requested_mode ?? "—"}</div>
                  <div
                    style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}
                    title={[row.requested_mode, row.mode_reason].filter(Boolean).join(" · ")}
                  >
                    {row.requested_mode ? `请求 ${row.requested_mode}` : row.reuse_policy ?? "—"}
                  </div>
                </span>
              ),
            },
            {
              key: "failure",
              header: "Failure",
              width: "200px",
              render: (row) => (
                <span className="mono" style={{ fontSize: "var(--fs-11)" }} title={row.failure?.message_safe ?? ""}>
                  {row.failure?.message_safe || row.failure?.kind || "—"}
                </span>
              ),
            },
            {
              key: "updated",
              header: "更新时间",
              width: "170px",
              render: (row) => <span>{formatTime(row.updated_at)}</span>,
            },
            {
              key: "actions",
              header: "操作",
              width: "110px",
              align: "right",
              render: (row) => (
                <button type="button" className="btn btn--sm" onClick={() => navigate(`/jobs/${row.id}`)}>
                  诊断
                </button>
              ),
            },
          ]}
        />
        <div
          className="toolbar"
          style={{ padding: "var(--sp-3) var(--sp-4)", justifyContent: "space-between" }}
        >
          <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>
            服务端 cursor 分页 · 本页 {rows.length} 条
          </span>
          <span className="toolbar">
            <button type="button" className="btn btn--sm" disabled={cursorHistory.length === 0 || loading} onClick={goPrevious}>
              上一页
            </button>
            <button type="button" className="btn btn--sm" disabled={!nextCursor || loading} onClick={goNext}>
              下一页
            </button>
          </span>
        </div>
      </Card>
    </>
  );
}
