import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState } from "react";
import { Link } from "react-router-dom";
import { ApiError,
  useAuth } from "@chain/api-client";
import {
  BulkActionBar,
  CompactProgress,
  DataTable,
  DateTimeRange,
  EmptyState,
  FilterBar,
  HashValue,
  InspectorDrawer,
  KeyValueList,
  MetricCard,
  MetaLine,
  PageSection,
  RelativeTime,
  RowCheckbox,
  SplitPane,
  StatusBadgeForState,
  progressStateFor,
  useDialog,
} from "@chain/ui";
import { CsPageHeader } from "@chain/user-ui";
import { formatBytes, formatRawAmount, formatTime, shortValue, useApi, useS5Action } from "../sprint5/common";

/* ============================================================================
 * U-JOB-01 /jobs：默认“我的活动任务”，服务端 cursor 分页 + 真实筛选。
 *
 * 数据层保持不变：请求参数、cursor 分页、批量取消的逐个 POST 与审计原因全部照旧。
 * 本次只改表现层：
 *   - 顶部状态统计来自当前接口真实返回的条目（单一 status 逐条计数，点击即按该状态筛选）。
 *   - 列表换成 DataTable（<900px 换成 .cs-rowcards 任务卡片，进度与主操作始终可见）。
 *   - 点击行开右侧快速预览；点击任务号进入详情。
 *   - 批量取消走 BulkActionBar + 原因与确认 Dialog，只允许选择可取消任务。
 *   - 失败详情、Trace、口径与计算方式只通过 Dialog 呈现。
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

/** 顶部四项统计各自的单一服务端状态：可被 status 过滤参数原样证明。 */
const STAT_DEFS: Array<{ key: string; label: string; status: string; tone: "ok" | "warn" | "danger" | "neutral" }> = [
  { key: "running", label: "运行中", status: "RUNNING", tone: "warn" },
  { key: "queued", label: "排队中", status: "QUEUED", tone: "warn" },
  { key: "failed", label: "失败", status: "FAILED", tone: "danger" },
  { key: "completed", label: "已完成", status: "COMPLETED", tone: "ok" },
];

/** 不计入上面四项的状态（口径说明里逐条列出，避免"数字对不上"的误解）。 */
const OTHER_STATUSES =
  "CREATED / PLANNING / WAITING_USER_CONFIRMATION / PAUSE_REQUESTED / CANCEL_REQUESTED / RESUMING / PAUSED / " +
  "PAUSED_FALLBACK_TOO_EXPENSIVE / CANCELLED / ABORTED_BY_USER / PARTIAL_FAILED";

function isCancellable(status: string): boolean {
  return !TERMINAL_STATUSES.has(status) && status !== "CANCEL_REQUESTED";
}

/**
 * 服务端 progress 是比率（分片完成比，缺分片时按地址完成比）。
 * 两个分母都为 0 时服务端返回 0，但那时并不存在"总量"，因此按 indeterminate 处理，不伪造百分比。
 */
function jobPercent(row: JobRow): number | null {
  if (row.shards.total <= 0 && row.addresses.total <= 0) return null;
  return Number.isFinite(row.progress) ? row.progress * 100 : null;
}

function rangeText(row: JobRow): string {
  if (row.range_type === "full") return "full";
  if (row.range_type === "time") return "time";
  if (row.from_block !== null && row.to_block !== null) return `${row.from_block} → ${row.to_block}`;
  return row.range_type || "—";
}

function jobTitle(row: JobRow): string {
  return row.job_no || shortValue(row.id, 10, 6);
}

/** 断点判定：<900px 使用任务卡片（蓝图 §13，进度与主操作始终可见）。 */
function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() =>
    typeof window === "undefined" ? false : window.matchMedia(query).matches,
  );
  useEffect(() => {
    const list = window.matchMedia(query);
    const sync = () => setMatches(list.matches);
    sync();
    list.addEventListener("change", sync);
    return () => list.removeEventListener("change", sync);
  }, [query]);
  return matches;
}

export default function JobsPage() {
  const dialog = useDialog();
  const { can } = useAuth();
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
  const [previewId, setPreviewId] = useState<string | null>(null);
  const [lastTrace, setLastTrace] = useState<string | null>(null);
  /** 仅作用于"已加载条目"的视图筛选：不改变任何服务端请求参数。 */
  const [localQuery, setLocalQuery] = useState("");
  const [createdFrom, setCreatedFrom] = useState("");
  const [createdTo, setCreatedTo] = useState("");
  const notifiedError = useRef<string | null>(null);
  const wide = useMediaQuery("(min-width: 900px)");

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

  /** 本地视图筛选：只作用于已加载条目，不重新请求服务端（口径在说明 Dialog 中列出）。 */
  const viewRows = useMemo(() => {
    const needle = localQuery.trim().toLowerCase();
    const fromMs = createdFrom ? Date.parse(createdFrom) : Number.NaN;
    const toMs = createdTo ? Date.parse(createdTo) : Number.NaN;
    return rows.filter((row) => {
      if (needle) {
        const haystack = [row.job_no, row.id, row.datasets.join(" "), row.source].join(" ").toLowerCase();
        if (!haystack.includes(needle)) return false;
      }
      if (Number.isFinite(fromMs) || Number.isFinite(toMs)) {
        const at = Date.parse(row.created_at);
        if (!Number.isFinite(at)) return false;
        if (Number.isFinite(fromMs) && at < fromMs) return false;
        if (Number.isFinite(toMs) && at > toMs) return false;
      }
      return true;
    });
  }, [rows, localQuery, createdFrom, createdTo]);

  useEffect(() => {
    setSelected((prev) => {
      const visible = new Set(viewRows.filter((row) => isCancellable(row.status)).map((row) => row.id));
      const next = new Set(Array.from(prev).filter((id) => visible.has(id)));
      return next.size === prev.size ? prev : next;
    });
  }, [viewRows]);

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

  /** 统计只数服务端本次返回的条目，且每项只数单一 status（可用 status 参数复现）。 */
  const stats = useMemo(() => {
    const counts: Record<string, number> = {};
    STAT_DEFS.forEach((def) => {
      counts[def.status] = allRows.filter((row) => row.status === def.status).length;
    });
    return counts;
  }, [allRows]);

  const cancellableRows = useMemo(() => viewRows.filter((row) => isCancellable(row.status)), [viewRows]);
  const allVisibleSelected = cancellableRows.length > 0 && cancellableRows.every((row) => selected.has(row.id));
  const selectedRows = viewRows.filter((row) => selected.has(row.id));
  const selectedCancellable = selectedRows.filter((row) => isCancellable(row.status));
  const previewRow = previewId ? viewRows.find((row) => row.id === previewId) ?? null : null;

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

  const resetFilters = () => {
    setView("active");
    setChainFilter("");
    setDatasetFilter("");
    setSourceInput("");
    setSourceFilter("");
    setLocalQuery("");
    setCreatedFrom("");
    setCreatedTo("");
  };

  const filtersActive =
    view !== "active" ||
    chainFilter !== "" ||
    datasetFilter !== "" ||
    sourceFilter !== "" ||
    localQuery.trim() !== "" ||
    createdFrom !== "" ||
    createdTo !== "";

  const showFailure = (row: JobRow) => {
    dialog.notify("error", "任务失败原因", row.failure.message_safe, [
      { label: "job_id", value: row.id },
      { label: "job_no", value: row.job_no },
      { label: "failure.kind", value: row.failure.kind || "—" },
    ]);
  };

  const explainCaliber = () => {
    dialog.notify(
      "info",
      "任务统计与速率口径",
      "统计按服务端本次返回的条目逐条计数，每项只统计单一 status；表格显示的是应用视图与本地筛选后的子集。",
      [
        { label: "运行中", value: "status=RUNNING" },
        { label: "排队中", value: "status=QUEUED" },
        { label: "失败", value: "status=FAILED" },
        { label: "已完成", value: "status=COMPLETED" },
        { label: "统计范围", value: `本次响应已加载的 ${allRows.length} 条（cursor 分页，每页最多 50 条）` },
        { label: "不计入其它状态", value: OTHER_STATUSES },
        { label: "进度", value: "取服务端 progress（分片完成比，无分片时按地址完成比）；两个分母都为 0 时不画百分比" },
        { label: "速率 rows/s", value: "列表契约 /api/v2/jobs 不返回耗时与速率字段，列表显示 —，页面不做推算；详情页按服务端 elapsed_seconds 展示口径" },
        { label: "本地筛选", value: "任务号 / Dataset / 来源 / 创建时间筛选只作用于已加载条目，不重新请求服务端；列表契约不返回地址明细，因此不能按地址筛选" },
        { label: "数据源", value: "/api/v2/jobs · /api/v2/meta/chains · /api/v2/meta/datasets" },
      ],
    );
  };

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

  const selectionColumn = {
    key: "select",
    width: "40px",
    header: (
      <input
        type="checkbox"
        aria-label="选择本页全部可取消任务"
        checked={allVisibleSelected}
        disabled={cancellableRows.length === 0}
        onClick={(event) => event.stopPropagation()}
        onChange={toggleAllVisible}
      />
    ),
    render: (row: JobRow) => (
      <input
        type="checkbox"
        aria-label={`选择任务 ${row.job_no}`}
        checked={selected.has(row.id)}
        disabled={!isCancellable(row.status)}
        onClick={(event) => event.stopPropagation()}
        onChange={() => toggleRow(row.id)}
      />
    ),
  };

  const pager = (
    <span className="toolbar">
      <span className="cs-section__count">
        已加载 {viewRows.length} / {rows.length} 条
      </span>
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
  );

  const bulkBar = (
    <BulkActionBar count={selectedRows.length} onClear={() => setSelected(new Set())}>
      <button
        type="button"
        className="btn btn--sm btn--danger"
        disabled={selectedCancellable.length === 0 || busy}
        onClick={() => void cancelJobs(selectedRows, true)}
      >
        批量取消（{selectedCancellable.length}）
      </button>
    </BulkActionBar>
  );

  const jobProgress = (row: JobRow, label: string) => (
    <CompactProgress
      percent={jobPercent(row)}
      state={progressStateFor(row.status)}
      label={`${label} ${jobTitle(row)}`}
      waitingFor={progressStateFor(row.status) === "waiting" ? row.status : undefined}
    />
  );

  const table = (
    <DataTable<JobRow>
      loading={jobsApi.loading}
      /* §17.5：任务列表由 SSE 高频触发 reload，刷新时保留旧行尤其重要。 */
      keepPreviousData
      error={jobsApi.error ? "任务列表加载失败，请重试。" : null}
      onRetry={() => void reloadJobs()}
      rows={viewRows}
      rowKey={(row) => row.id}
      onRowActivate={(row) => setPreviewId(row.id)}
      rowActivateLabel={(row) => `预览任务 ${jobTitle(row)}`}
      rowTestId={(row) => "job-row-" + row.id}
      empty={
        <EmptyState
          title="没有符合筛选条件的任务"
          body="筛选条件会保留，方便你修改而不是重新输入。"
          action={
            filtersActive ? (
              <button type="button" className="btn btn--sm" onClick={resetFilters}>
                重置筛选
              </button>
            ) : undefined
          }
        />
      }
      columns={[
        selectionColumn,
        {
          key: "job",
          header: "任务号",
          width: "220px",
          render: (row) => (
            <div style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
              <Link
                to={`/jobs/${row.id}`}
                className="mono"
                style={{ fontWeight: 600 }}
                onClick={(event) => event.stopPropagation()}
              >
                {row.job_no || "（无编号）"}
              </Link>
              <HashValue value={row.id} head={8} tail={6} label="任务 ID" />
              <span style={{ color: "var(--text-secondary)", fontSize: "var(--fs-11)" }}>{row.source || "—"}</span>
            </div>
          ),
        },
        {
          key: "chain",
          header: "链 / 范围",
          width: "150px",
          render: (row) => (
            <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
              <span className="mono">{row.chain}</span>
              <span style={{ color: "var(--text-secondary)", fontSize: "var(--fs-11)" }}>
                {rangeText(row)} · {row.reuse_policy}
              </span>
              <span style={{ color: "var(--text-secondary)", fontSize: "var(--fs-11)" }}>
                {row.requested_mode} → {row.effective_mode}
              </span>
            </div>
          ),
        },
        {
          key: "status",
          header: "状态与进度",
          width: "190px",
          render: (row) => (
            <div style={{ display: "flex", flexDirection: "column", gap: 4, minWidth: 0 }}>
              <StatusBadgeForState value={row.status} />
              {jobProgress(row, "任务进度")}
              {row.failure?.message_safe ? (
                <button
                  type="button"
                  className="btn btn--sm"
                  onClick={(event) => {
                    event.stopPropagation();
                    showFailure(row);
                  }}
                >
                  失败详情
                </button>
              ) : null}
            </div>
          ),
        },
        {
          key: "addresses",
          header: "对象进度",
          width: "160px",
          render: (row) => (
            <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
              <span className="mono">
                {row.addresses.completed} / {row.addresses.total}
              </span>
              <CompactProgress
                percent={row.addresses.total > 0 ? (row.addresses.completed / row.addresses.total) * 100 : null}
                state={progressStateFor(row.status)}
                label={`地址进度 ${jobTitle(row)}`}
              />
            </div>
          ),
        },
        {
          key: "datasets",
          header: "Dataset 进度",
          width: "230px",
          render: (row) => (
            <div style={{ display: "flex", flexDirection: "column", gap: 4, minWidth: 0 }}>
              <span
                title={row.datasets.join("、")}
                style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", maxWidth: 200 }}
              >
                {row.datasets.join("、") || "—"}
              </span>
              <span className="mono" style={{ color: "var(--text-secondary)", fontSize: "var(--fs-11)" }}>
                Dataset {row.datasets_progress.total} · 分片 {row.shards.completed}/{row.shards.total}
                {row.shards.failed > 0 ? ` · 失败 ${row.shards.failed}` : ""}
              </span>
              <CompactProgress
                percent={row.shards.total > 0 ? (row.shards.completed / row.shards.total) * 100 : null}
                state={progressStateFor(row.status)}
                label={`分片进度 ${jobTitle(row)}`}
              />
            </div>
          ),
        },
        {
          key: "downloaded",
          header: "下载量 / 速率",
          width: "150px",
          render: (row) => (
            <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
              <span className="mono">{formatRawAmount(String(row.downloaded_rows), 0)} 行</span>
              <span className="mono" style={{ color: "var(--text-secondary)", fontSize: "var(--fs-11)" }}>
                {formatBytes(row.downloaded_bytes)}
              </span>
              <span
                className="mono"
                style={{ color: "var(--text-secondary)", fontSize: "var(--fs-11)" }}
                title="列表契约 /api/v2/jobs 不返回耗时与速率字段，页面不做推算"
              >
                rows/s —
              </span>
            </div>
          ),
        },
        {
          key: "created",
          header: "创建 / 更新",
          width: "170px",
          render: (row) => (
            <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
              <span className="mono" style={{ fontSize: "var(--fs-11)" }}>
                {formatTime(row.created_at)}
              </span>
              <span style={{ color: "var(--text-secondary)", fontSize: "var(--fs-11)" }}>
                <RelativeTime value={row.updated_at} />
              </span>
              <span style={{ color: "var(--text-secondary)", fontSize: "var(--fs-11)" }}>
                成功 {formatTime(row.last_success_at)}
              </span>
            </div>
          ),
        },
        {
          key: "actions",
          header: "操作",
          width: "150px",
          render: (row) => (
            <div className="toolbar">
              <Link className="btn btn--sm" to={`/jobs/${row.id}`} onClick={(event) => event.stopPropagation()}>
                详情
              </Link>
              <button
                type="button"
                className="btn btn--sm btn--danger"
                disabled={!isCancellable(row.status) || busy}
                onClick={(event) => {
                  event.stopPropagation();
                  void cancelJobs([row]);
                }}
              >
                取消
              </button>
            </div>
          ),
        },
      ]}
    />
  );

  const cards = (
    <div className="cs-rowcards cs-stagger">
      {viewRows.map((row) => (
        <div className="cs-rowcard" key={row.id}>
          <div className="cs-rowcard__head">
            <RowCheckbox
              checked={selected.has(row.id)}
              disabled={!isCancellable(row.status)}
              label={`选择任务 ${row.job_no}`}
              onChange={() => toggleRow(row.id)}
            />
            <Link className="cs-rowcard__title" to={`/jobs/${row.id}`}>
              {row.job_no || "（无编号）"}
            </Link>
            <StatusBadgeForState value={row.status} />
          </div>
          <div className="cs-rowcard__meta">
            <span className="mono">{row.chain}</span>
            <span>{rangeText(row)}</span>
            <span>
              对象 {row.addresses.completed} / {row.addresses.total}
            </span>
            <span>Dataset {row.datasets.join("、") || "—"}</span>
            <span>
              分片 {row.shards.completed}/{row.shards.total}
            </span>
            <RelativeTime value={row.created_at} />
          </div>
          {jobProgress(row, "任务进度")}
          <div className="cs-rowcard__meta">
            <span>{formatRawAmount(String(row.downloaded_rows), 0)} 行</span>
            <span>{formatBytes(row.downloaded_bytes)}</span>
            <span title="列表契约 /api/v2/jobs 不返回耗时与速率字段">rows/s —</span>
          </div>
          <div className="cs-rowcard__actions">
            <Link className="btn btn--sm" to={`/jobs/${row.id}`}>
              详情
            </Link>
            <button
              type="button"
              className="btn btn--sm btn--danger"
              disabled={!isCancellable(row.status) || busy}
              onClick={() => void cancelJobs([row])}
            >
              取消
            </button>
            {row.failure?.message_safe ? (
              <button type="button" className="btn btn--sm btn--ghost" onClick={() => showFailure(row)}>
                失败详情
              </button>
            ) : null}
          </div>
        </div>
      ))}
    </div>
  );

  const aside =
    wide && previewRow ? (
      <InspectorDrawer
        title={jobTitle(previewRow)}
        subtitle={<StatusBadgeForState value={previewRow.status} />}
        ariaLabel="任务快速预览"
        onClose={() => setPreviewId(null)}
        actions={
          <>
            <Link className="btn btn--primary btn--sm" to={`/jobs/${previewRow.id}`}>
              进入详情
            </Link>
            <button
              type="button"
              className="btn btn--sm btn--danger"
              disabled={!isCancellable(previewRow.status) || busy}
              onClick={() => void cancelJobs([previewRow])}
            >
              取消任务
            </button>
          </>
        }
      >
        <KeyValueList
          items={[
            { label: "任务号", value: previewRow.job_no || "（无编号）" },
            { label: "任务 ID", value: <HashValue value={previewRow.id} wrap label="任务 ID" />, wide: true },
            { label: "链", value: previewRow.chain },
            { label: "范围", value: rangeText(previewRow) },
            { label: "复用策略", value: previewRow.reuse_policy },
            { label: "请求模式", value: previewRow.requested_mode },
            { label: "生效模式", value: previewRow.effective_mode },
            { label: "模式原因", value: previewRow.mode_reason, wide: true },
            { label: "来源", value: previewRow.source },
            { label: "Dataset", value: previewRow.datasets.join("、") || "—", wide: true },
            { label: "对象进度", value: `${previewRow.addresses.completed} / ${previewRow.addresses.total}` },
            {
              label: "分片进度",
              value: `${previewRow.shards.completed} / ${previewRow.shards.total}${
                previewRow.shards.failed > 0 ? `（失败 ${previewRow.shards.failed}）` : ""
              }`,
            },
            { label: "下载行数", value: formatRawAmount(String(previewRow.downloaded_rows), 0) },
            { label: "下载字节", value: formatBytes(previewRow.downloaded_bytes) },
            { label: "创建时间", value: formatTime(previewRow.created_at) },
            { label: "更新时间", value: formatTime(previewRow.updated_at) },
            { label: "最后成功", value: formatTime(previewRow.last_success_at) },
            { label: "失败原因", value: previewRow.failure?.message_safe || "—", wide: true },
          ]}
        />
        <div style={{ marginTop: "var(--sp-4)" }}>{jobProgress(previewRow, "任务进度")}</div>
      </InspectorDrawer>
    ) : undefined;

  return (
    <>
      <CsPageHeader
        title="任务中心"
        breadcrumb={[{ label: "工作台", to: "/home" }, { label: "任务中心" }]}
        meta={
          <MetaLine
            items={[
              { label: "范围", value: scope === "workspace" ? "工作区任务" : "我的任务" },
              { label: "已加载", value: `${allRows.length} 条` },
              { label: "数据源", value: "/api/v2/jobs" },
            ]}
          />
        }
        secondaryAction={
          <span className="toolbar">
            <select
              className="select"
              style={{ maxWidth: "100%" }}
              aria-label="任务范围"
              value={scope}
              onChange={(event) => setScope(event.target.value as "self" | "workspace")}
            >
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
            <button type="button" className="btn btn--sm btn--ghost" onClick={explainCaliber}>
              口径说明
            </button>
          </span>
        }
        primaryAction={
          can("download:create") ? (
            <Link className="btn btn--primary btn--sm" to="/downloads/new">
              新建下载任务
            </Link>
          ) : null
        }
      />

      <PageSection title="当前状态" count={`本次响应已加载 ${allRows.length} 条`}>
        <div className="metric-grid cs-stagger">
          {STAT_DEFS.map((def) => (
            <MetricCard
              key={def.key}
              label={def.label}
              value={stats[def.status] ?? 0}
              unit="个任务"
              hint={def.status}
              tone={(stats[def.status] ?? 0) > 0 ? def.tone : "neutral"}
              onActivate={() => setView(def.status)}
            />
          ))}
        </div>
      </PageSection>

      <SplitPane
        main={
          <PageSection title="任务列表" count={`${viewRows.length} 条`} actions={pager}>
            <FilterBar ariaLabel="任务筛选">
              <select
                className="select"
                style={{ maxWidth: "100%" }}
                aria-label="任务视图"
                value={view}
                onChange={(event) => setView(event.target.value)}
              >
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
              <select
                className="select"
                style={{ maxWidth: "100%" }}
                aria-label="链筛选"
                value={chainFilter}
                onChange={(event) => setChainFilter(event.target.value)}
              >
                <option value="">全部链</option>
                {chainOptions.map((item) => (
                  <option key={item} value={item}>
                    {item}
                  </option>
                ))}
              </select>
              <select
                className="select"
                style={{ maxWidth: "100%" }}
                aria-label="Dataset 筛选"
                value={datasetFilter}
                onChange={(event) => setDatasetFilter(event.target.value)}
              >
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
              <input
                className="input"
                style={{ width: 210 }}
                aria-label="按任务号、任务 ID 或 Dataset 筛选已加载任务"
                placeholder="任务号 / 任务 ID / Dataset"
                value={localQuery}
                onChange={(event) => setLocalQuery(event.target.value)}
              />
              <DateTimeRange
                from={createdFrom}
                to={createdTo}
                onFromChange={setCreatedFrom}
                onToChange={setCreatedTo}
                label="创建时间（仅作用于已加载任务）"
              />
              <button type="button" className="btn btn--sm btn--ghost" onClick={resetFilters}>
                重置筛选
              </button>
            </FilterBar>
            {bulkBar}
            {wide ? table : cards}
          </PageSection>
        }
        aside={aside}
        storageKey="cs.jobs.preview.width"
        defaultAsideWidth={380}
        asideLabel="任务快速预览"
      />
    </>
  );
}
