import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ApiError } from "@chain/api-client";
import { Card, DataTable, EmptyState, PageHeader, StatusBadge, statusToneFor, useDialog } from "@chain/ui";
import { formatBytes, formatRawAmount, formatTime, shortValue, useApi, useS5Action } from "../sprint5/common";

/* ============================================================================
 * U-JOB-01 /jobs：默认“我的活动任务”，服务端 cursor 分页 + 真实筛选。
 * 批量取消只允许非终态任务，并在 Dialog 中说明影响数量与已完成数据保留规则。
 * ==========================================================================*/

interface JobFailure {
  kind: string;
  message_safe: string;
}

interface JobRow {
  id: string;
  job_no: string;
  status: string;
  chain: string;
  datasets: string[];
  source: string;
  reuse_policy: string;
  range_type: string;
  from_block: number | null;
  to_block: number | null;
  requested_mode: string;
  effective_mode: string;
  mode_reason: string;
  created_at: string;
  updated_at: string;
  progress: number;
  addresses: { total: number; completed: number };
  datasets_progress: { total: number };
  shards: { total: number; completed: number; failed: number };
  downloaded_rows: number;
  downloaded_bytes: number;
  last_success_at: string | null;
  failure: JobFailure;
}

interface JobsListData {
  items: JobRow[];
  scope: Record<string, unknown>;
  filters: Record<string, unknown>;
}

interface ChainMeta {
  slug: string;
  name: string;
  enabled: boolean;
}

interface DatasetMeta {
  id: string;
  label: string;
  status: string;
}

const TERMINAL_STATUSES = new Set(["COMPLETED", "CANCELLED", "FAILED", "PARTIAL_FAILED", "ABORTED_BY_USER"]);

function isCancellable(status: string): boolean {
  return !TERMINAL_STATUSES.has(status) && status !== "CANCEL_REQUESTED";
}

function percent(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) return "—";
  return `${(value * 100).toFixed(value >= 1 ? 0 : 1)}%`;
}

function ProgressBar({ value }: { value: number }) {
  const safe = Math.max(0, Math.min(1, Number.isFinite(value) ? value : 0));
  return (
    <span
      aria-hidden="true"
      style={{
        display: "inline-block",
        width: "100%",
        maxWidth: 120,
        height: 6,
        borderRadius: 999,
        background: "var(--c-neutral-soft)",
        overflow: "hidden",
        verticalAlign: "middle",
      }}
    >
      <span
        style={{
          display: "block",
          height: "100%",
          width: `${Math.round(safe * 100)}%`,
          background: "var(--c-cobalt)",
          borderRadius: "inherit",
        }}
      />
    </span>
  );
}

function rangeText(row: JobRow): string {
  if (row.range_type === "full") return "full";
  if (row.range_type === "time") return "time";
  if (row.from_block !== null && row.to_block !== null) return `${row.from_block} → ${row.to_block}`;
  return row.range_type || "—";
}

export default function JobsPage() {
  const dialog = useDialog();
  const navigate = useNavigate();
  const { busy, run, client } = useS5Action();

  const [view, setView] = useState("active");
  const [scope, setScope] = useState<"self" | "workspace">("self");
  const [chainFilter, setChainFilter] = useState("");
  const [datasetFilter, setDatasetFilter] = useState("");
  const [sourceInput, setSourceInput] = useState("");
  const [sourceFilter, setSourceFilter] = useState("");
  const [page, setPage] = useState(0);
  const [cursors, setCursors] = useState<Array<string | null>>([null]);
  const [selected, setSelected] = useState<Set<string>>(() => new Set());
  const [lastTrace, setLastTrace] = useState<string | null>(null);
  const notifiedError = useRef<string | null>(null);

  const jobsApi = useApi<JobsListData>("/api/v2/jobs", [page, view, scope, chainFilter, datasetFilter, sourceFilter], {
    limit: 50,
    cursor: cursors[page] ?? undefined,
    scope: scope === "workspace" ? "workspace" : undefined,
    status: view === "active" || view === "all" ? undefined : view,
    chain: chainFilter || undefined,
    dataset: datasetFilter || undefined,
    source: sourceFilter || undefined,
  });
  const chainsApi = useApi<{ items: ChainMeta[] }>("/api/v2/meta/chains");
  const datasetsApi = useApi<{ items: DatasetMeta[] }>("/api/v2/meta/datasets");

  const jobsError = jobsApi.error;
  const reloadJobs = jobsApi.reload;

  useEffect(() => {
    if (jobsError && notifiedError.current !== jobsError) {
      notifiedError.current = jobsError;
      dialog.notify("error", "任务列表加载失败", jobsError);
    }
  }, [jobsError, dialog]);

  useEffect(() => {
    setPage(0);
    setCursors([null]);
    setSelected(new Set());
  }, [view, scope, chainFilter, datasetFilter, sourceFilter]);

  const allRows = jobsApi.data?.items ?? [];
  const rows = useMemo(
    () => (view === "active" ? allRows.filter((row) => !TERMINAL_STATUSES.has(row.status)) : allRows),
    [allRows, view],
  );

  useEffect(() => {
    setSelected((prev) => {
      const visible = new Set(rows.filter((row) => isCancellable(row.status)).map((row) => row.id));
      const next = new Set(Array.from(prev).filter((id) => visible.has(id)));
      return next.size === prev.size ? prev : next;
    });
  }, [rows]);

  const chainOptions = useMemo(() => {
    const values = new Set<string>();
    (chainsApi.data?.items ?? []).forEach((item) => values.add(item.slug));
    allRows.forEach((row) => {
      if (row.chain) values.add(row.chain);
    });
    return Array.from(values).filter(Boolean).sort();
  }, [chainsApi.data, allRows]);

  const datasetOptions = useMemo(() => {
    const values = new Set<string>();
    (datasetsApi.data?.items ?? []).forEach((item) => values.add(item.id));
    allRows.forEach((row) => row.datasets.forEach((dataset) => values.add(dataset)));
    return Array.from(values).filter(Boolean).sort();
  }, [datasetsApi.data, allRows]);

  const cancellableRows = useMemo(() => rows.filter((row) => isCancellable(row.status)), [rows]);
  const allVisibleSelected = cancellableRows.length > 0 && cancellableRows.every((row) => selected.has(row.id));
  const selectedRows = rows.filter((row) => selected.has(row.id));
  const selectedCancellable = selectedRows.filter((row) => isCancellable(row.status));

  const toggleRow = useCallback((id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const toggleAllVisible = useCallback(() => {
    setSelected((prev) => {
      if (cancellableRows.every((row) => prev.has(row.id))) {
        const next = new Set(prev);
        cancellableRows.forEach((row) => next.delete(row.id));
        return next;
      }
      const next = new Set(prev);
      cancellableRows.forEach((row) => next.add(row.id));
      return next;
    });
  }, [cancellableRows]);

  const cancelJobs = useCallback(
    async (targets: JobRow[], batch = false) => {
      const cancellable = targets.filter((row) => isCancellable(row.status));
      if (cancellable.length === 0) {
        dialog.notify("warning", "没有可取消的任务", "只有非终态任务可以取消，终态任务的操作已禁用。");
        return;
      }
      const answer = await dialog.open({
        kind: "confirm",
        title: batch ? `批量取消 ${cancellable.length} 个任务？` : "取消该任务？",
        body: "取消只阻止后续执行；已完成分片、成功游标与已落库数据会保留，可用于后续复用。",
        details: [
          { label: "影响数量", value: `${cancellable.length} 个任务` },
          { label: "不可取消（将跳过）", value: String(targets.length - cancellable.length) },
          {
            label: "任务编号",
            value: `${cancellable.slice(0, 8).map((row) => row.job_no).join("、")}${cancellable.length > 8 ? " …" : ""}`,
          },
        ],
        input: { label: "取消原因（必填，写入审计）", placeholder: "例如：案件范围调整" },
        actions: [
          { label: "返回", result: "", variant: "default" },
          { label: "确认取消", result: "cancel", variant: "danger" },
        ],
      });
      if (answer === null) return;
      const reason = answer.startsWith("input:") ? answer.slice("input:".length).trim() : "";
      if (!reason) {
        dialog.notify("warning", "请填写取消原因", "取消原因会写入审计与任务事件，不能为空。");
        return;
      }
      const outcome = await run(async () => {
        let done = 0;
        let failed = 0;
        const traces: string[] = [];
        const failedJobs: string[] = [];
        for (const job of cancellable) {
          try {
            await client.post(`/api/v2/jobs/${encodeURIComponent(job.id)}/commands/cancel`, { reason });
            done += 1;
          } catch (error) {
            failed += 1;
            failedJobs.push(job.job_no);
            if (error instanceof ApiError && error.traceId) traces.push(error.traceId);
          }
        }
        return { done, failed, traces, failedJobs };
      });
      if (!outcome) return;
      if (outcome.traces[0]) setLastTrace(outcome.traces[0]);
      await dialog.open({
        kind: outcome.failed > 0 ? "warning" : "success",
        title: outcome.failed > 0 ? "部分任务取消失败" : "取消指令已提交",
        details: [
          { label: "成功", value: String(outcome.done) },
          { label: "失败", value: String(outcome.failed) },
          ...(outcome.failedJobs.length > 0 ? [{ label: "失败任务", value: outcome.failedJobs.join("、") }] : []),
          ...(outcome.traces[0] ? [{ label: "Trace", value: outcome.traces[0] }] : []),
        ],
        actions: [{ label: "知道了", result: "ok", variant: "primary" }],
      });
      setSelected(new Set());
      void reloadJobs();
    },
    [client, dialog, reloadJobs, run],
  );

  return (
    <>
      <PageHeader
        title="任务中心"
        actions={
          <span className="toolbar">
            <select className="select" aria-label="任务范围" value={scope} onChange={(event) => setScope(event.target.value as "self" | "workspace")}>
              <option value="self">我的任务</option>
              <option value="workspace">工作区任务</option>
            </select>
            <button type="button" className="btn btn--sm" onClick={() => void reloadJobs()}>
              刷新
            </button>
            {lastTrace ? (
              <button
                type="button"
                className="btn btn--sm"
                onClick={() => dialog.notify("info", "最近一次操作 Trace", null, [{ label: "Trace", value: lastTrace }])}
              >
                Trace
              </button>
            ) : null}
          </span>
        }
      />
      <Card
        flush
        actions={
          <span className="toolbar">
            <select className="select" aria-label="任务视图" value={view} onChange={(event) => setView(event.target.value)}>
              <option value="active">我的活动任务</option>
              <option value="all">全部状态</option>
              <option value="RUNNING">RUNNING</option>
              <option value="QUEUED">QUEUED</option>
              <option value="PAUSED">PAUSED</option>
              <option value="PAUSED_FALLBACK_TOO_EXPENSIVE">PAUSED_FALLBACK_TOO_EXPENSIVE</option>
              <option value="FAILED">FAILED</option>
              <option value="PARTIAL_FAILED">PARTIAL_FAILED</option>
              <option value="COMPLETED">COMPLETED</option>
              <option value="CANCELLED">CANCELLED</option>
            </select>
            <select className="select" aria-label="链筛选" value={chainFilter} onChange={(event) => setChainFilter(event.target.value)}>
              <option value="">全部链</option>
              {chainOptions.map((item) => (
                <option key={item} value={item}>
                  {item}
                </option>
              ))}
            </select>
            <select className="select" aria-label="Dataset 筛选" value={datasetFilter} onChange={(event) => setDatasetFilter(event.target.value)}>
              <option value="">全部 Dataset</option>
              {datasetOptions.map((item) => (
                <option key={item} value={item}>
                  {item}
                </option>
              ))}
            </select>
            <input
              className="input"
              style={{ width: 150 }}
              aria-label="来源筛选"
              placeholder="来源，如 user_download"
              value={sourceInput}
              onChange={(event) => setSourceInput(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") setSourceFilter(sourceInput.trim());
              }}
            />
            <button type="button" className="btn btn--sm" onClick={() => setSourceFilter(sourceInput.trim())}>
              应用来源
            </button>
            <button
              type="button"
              className="btn btn--sm btn--ghost"
              onClick={() => {
                setView("active");
                setChainFilter("");
                setDatasetFilter("");
                setSourceInput("");
                setSourceFilter("");
              }}
            >
              重置筛选
            </button>
            <button
              type="button"
              className="btn btn--sm btn--danger"
              disabled={selectedCancellable.length === 0 || busy}
              onClick={() => void cancelJobs(selectedRows, true)}
            >
              批量取消（{selectedCancellable.length}）
            </button>
            <button type="button" className="btn btn--sm" disabled={page === 0} onClick={() => setPage((value) => Math.max(0, value - 1))}>
              上一页
            </button>
            <button
              type="button"
              className="btn btn--sm"
              disabled={!jobsApi.meta.next_cursor}
              onClick={() => {
                const next = jobsApi.meta.next_cursor;
                if (!next) return;
                setCursors((prev) => {
                  const copy = [...prev];
                  copy[page + 1] = next;
                  return copy;
                });
                setPage((value) => value + 1);
              }}
            >
              下一页
            </button>
          </span>
        }
      >
        <DataTable<JobRow>
          loading={jobsApi.loading}
          error={jobsApi.error ? "任务列表加载失败，请重试。" : null}
          onRetry={() => void reloadJobs()}
          rows={rows}
          rowKey={(row) => row.id}
          empty={<EmptyState title="没有符合筛选条件的任务" />}
          columns={[
            {
              key: "select",
              width: "40px",
              header: (
                <input
                  type="checkbox"
                  aria-label="选择本页全部可取消任务"
                  checked={allVisibleSelected}
                  disabled={cancellableRows.length === 0}
                  onChange={toggleAllVisible}
                />
              ),
              render: (row) => (
                <input
                  type="checkbox"
                  aria-label={`选择任务 ${row.job_no}`}
                  checked={selected.has(row.id)}
                  disabled={!isCancellable(row.status)}
                  onChange={() => toggleRow(row.id)}
                />
              ),
            },
            {
              key: "job",
              header: "Job",
              width: "220px",
              render: (row) => (
                <div style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
                  <button
                    type="button"
                    className="btn btn--ghost btn--sm"
                    style={{ justifyContent: "flex-start", paddingLeft: 0 }}
                    onClick={() => navigate(`/jobs/${row.id}`)}
                  >
                    {row.job_no || "（无编号）"}
                  </button>
                  <span className="mono" style={{ color: "var(--c-slate)", fontSize: "var(--fs-11)" }}>
                    {shortValue(row.id, 10, 6)}
                  </span>
                  <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-11)" }}>{row.source || "—"}</span>
                </div>
              ),
            },
            {
              key: "status",
              header: "Job 进度",
              width: "170px",
              render: (row) => (
                <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
                  <StatusBadge tone={statusToneFor(row.status)}>{row.status}</StatusBadge>
                  <ProgressBar value={row.progress} />
                  <span className="mono" style={{ fontSize: "var(--fs-11)" }}>
                    {percent(row.progress)}
                    {row.failure?.message_safe ? " · 失败" : ""}
                  </span>
                  {row.failure?.message_safe ? (
                    <button
                      type="button"
                      className="btn btn--sm"
                      onClick={() =>
                        dialog.notify("error", "任务失败原因", row.failure.message_safe, [
                          { label: "job_id", value: row.id },
                          { label: "job_no", value: row.job_no },
                          { label: "failure.kind", value: row.failure.kind || "—" },
                        ])
                      }
                    >
                      失败详情
                    </button>
                  ) : null}
                </div>
              ),
            },
            {
              key: "addresses",
              header: "地址进度",
              width: "150px",
              render: (row) => (
                <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
                  <span className="mono">
                    {row.addresses.completed} / {row.addresses.total}
                  </span>
                  <ProgressBar value={row.addresses.total > 0 ? row.addresses.completed / row.addresses.total : 0} />
                </div>
              ),
            },
            {
              key: "datasets",
              header: "Dataset 进度",
              width: "210px",
              render: (row) => (
                <div style={{ display: "flex", flexDirection: "column", gap: 4, minWidth: 0 }}>
                  <span title={row.datasets.join("、")} style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", maxWidth: 180 }}>
                    {row.datasets.join("、") || "—"}
                  </span>
                  <span className="mono" style={{ color: "var(--c-slate)", fontSize: "var(--fs-11)" }}>
                    Dataset {row.datasets_progress.total} · 分片 {row.shards.completed}/{row.shards.total}
                    {row.shards.failed > 0 ? ` · 失败 ${row.shards.failed}` : ""}
                  </span>
                  <ProgressBar value={row.shards.total > 0 ? row.shards.completed / row.shards.total : 0} />
                </div>
              ),
            },
            {
              key: "chain",
              header: "链 / 范围",
              width: "130px",
              render: (row) => (
                <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
                  <span className="mono">{row.chain}</span>
                  <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-11)" }}>
                    {rangeText(row)} · {row.reuse_policy}
                  </span>
                  <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-11)" }}>
                    {row.requested_mode} → {row.effective_mode}
                  </span>
                </div>
              ),
            },
            {
              key: "downloaded",
              header: "下载量",
              width: "130px",
              render: (row) => (
                <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
                  <span className="mono">{formatRawAmount(String(row.downloaded_rows), 0)} 行</span>
                  <span className="mono" style={{ color: "var(--c-slate)", fontSize: "var(--fs-11)" }}>
                    {formatBytes(row.downloaded_bytes)}
                  </span>
                </div>
              ),
            },
            {
              key: "updated",
              header: "更新时间",
              width: "150px",
              render: (row) => (
                <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
                  <span className="mono" style={{ fontSize: "var(--fs-11)" }}>{formatTime(row.updated_at)}</span>
                  <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-11)" }}>成功 {formatTime(row.last_success_at)}</span>
                </div>
              ),
            },
            {
              key: "actions",
              header: "操作",
              width: "140px",
              render: (row) => (
                <div className="toolbar">
                  <button type="button" className="btn btn--sm" onClick={() => navigate(`/jobs/${row.id}`)}>
                    详情
                  </button>
                  <button
                    type="button"
                    className="btn btn--sm btn--danger"
                    disabled={!isCancellable(row.status) || busy}
                    onClick={() => void cancelJobs([row])}
                  >
                    取消
                  </button>
                </div>
              ),
            },
          ]}
        />
      </Card>
    </>
  );
}
