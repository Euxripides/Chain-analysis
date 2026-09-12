import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { ApiError } from "@chain/api-client";
import {
  BackLink,
  Card,
  EmptyState,
  ErrorState,
  EvidenceRail,
  Metric,
  PageHeader,
  SkeletonRows,
  StatusBadge,
  statusToneFor,
  useDialog,
} from "@chain/ui";
import { formatBytes, formatRawAmount, formatTime, shortValue, useApi, useJobStream, useS5Action } from "../sprint5/common";
import type { JobEvent } from "../sprint5/common";

/* ============================================================================
 * U-JOB-02 /jobs/:jobId：四级进度（Job/地址/Dataset/Shard）、SSE 结构化事件、
 * 状态感知控制、导出票据。说明/口径/错误只通过 Dialog 呈现。
 * ==========================================================================*/

interface JobRecord {
  id: string;
  job_no: string;
  status: string;
  source: string;
  requested_mode: string;
  effective_mode: string;
  mode_reason: string;
  chain: string;
  range_type: string;
  from_block: number | null;
  to_block: number | null;
  from_timestamp: string | null;
  to_timestamp: string | null;
  datasets: string[];
  reuse_policy: string;
  head_block: number;
  created_at: string;
  updated_at: string;
  resolved_at: string | null;
  error: string;
  stats: Record<string, unknown>;
}

interface SuccessfulCursor {
  tron_last_timestamp_ms: number | null;
  checkpoint_at: string | null;
  definition: string;
}

interface AddressLayer {
  subject_id: string;
  address: string;
  status: string;
  progress: number;
  updated_at: string;
  successful_cursor: SuccessfulCursor;
}

interface DatasetLayer {
  dataset_id: string;
  subject_id: string;
  dataset: string;
  status: string;
  progress: number;
  shards: { total: number; completed: number; failed: number };
  updated_at: string;
}

interface ShardLayer {
  shard_id: string;
  subject_id: string;
  dataset: string;
  from_block: number;
  to_block: number;
  status: string;
  retries: number;
  downloaded_rows: number;
  downloaded_bytes: number;
  raw_object: string;
  error_safe: string;
  updated_at: string;
  checkpoint?: { next_page_token?: string; page_no?: number; downloaded_rows?: number; downloaded_bytes?: number; last_success_at?: string };
}

interface JobProgress {
  status: string;
  progress: number;
  downloaded_rows: number;
  downloaded_bytes: number;
  api_requests: number;
  api_cost_usd: number;
  total_shards: number;
  done_shards: number;
  elapsed_seconds: number;
  eta_seconds: number;
  ts: string;
  coverage_hit_ratio?: number;
}

interface JobExport {
  id: string;
  format: string;
  zip: boolean;
  status: string;
  rows: number;
  sha256: string;
  error_safe: string;
  object_ref: string;
  expires_at: string | null;
  created_at: string;
}

interface JobDetailData {
  job: JobRecord;
  subjects: unknown[];
  datasets: unknown[];
  shards: unknown[];
  progress: JobProgress;
  performance: { requested_mode: string; effective_mode: string; mode_reason: string };
  layers: {
    job: { status: string; updated_at: string; error_safe: string };
    addresses: AddressLayer[];
    datasets: DatasetLayer[];
    shards: ShardLayer[];
  };
  exports: JobExport[];
  source: { control: string; usage: string; as_of: string };
}

const TERMINAL_STATUSES = new Set(["COMPLETED", "CANCELLED", "FAILED", "PARTIAL_FAILED", "ABORTED_BY_USER"]);

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
        height: 6,
        borderRadius: 999,
        background: "var(--c-neutral-soft)",
        overflow: "hidden",
        verticalAlign: "middle",
      }}
    >
      <span style={{ display: "block", height: "100%", width: `${Math.round(safe * 100)}%`, background: "var(--c-cobalt)", borderRadius: "inherit" }} />
    </span>
  );
}

function rangeText(job: JobRecord): string {
  if (job.range_type === "full") return "full · 全量";
  if (job.range_type === "block") return `${job.from_block ?? "—"} → ${job.to_block ?? "—"}`;
  if (job.range_type === "time") return `${formatTime(job.from_timestamp)} → ${formatTime(job.to_timestamp)}`;
  return job.range_type || "—";
}

function eventFacts(event: JobEvent): Array<{ label: string; value: string }> {
  const facts: Array<{ label: string; value: string }> = [];
  for (const [key, value] of Object.entries(event)) {
    if (key.startsWith("_")) continue;
    if (typeof value === "string" || typeof value === "number" || typeof value === "boolean" || value === null) {
      facts.push({ label: key, value: value === null ? "null" : String(value) });
    }
  }
  return facts;
}

function eventErrorMessage(event: JobEvent): string | null {
  const candidate = event.error_safe ?? event.message_safe ?? event.error;
  return typeof candidate === "string" && candidate.trim() !== "" ? candidate : null;
}

export default function JobDetailPage() {
  const dialog = useDialog();
  const { jobId = "" } = useParams<{ jobId: string }>();
  const navigate = useNavigate();
  const { busy, run, client } = useS5Action();

  const detailApi = useApi<JobDetailData>(jobId ? `/api/v2/jobs/${encodeURIComponent(jobId)}` : null);
  const detail = detailApi.data;
  const reload = detailApi.reload;
  const detailError = detailApi.error;

  const [events, setEvents] = useState<JobEvent[]>([]);
  const [lastTrace, setLastTrace] = useState<string | null>(null);
  const [expandAll, setExpandAll] = useState(false);
  const lastEventIdRef = useRef(0);
  const lastRevisionRef = useRef(0);
  const reloadTimerRef = useRef<number | null>(null);
  const notifiedErrorRef = useRef<string | null>(null);

  const scheduleReload = useCallback(() => {
    if (reloadTimerRef.current !== null) window.clearTimeout(reloadTimerRef.current);
    reloadTimerRef.current = window.setTimeout(() => {
      void reload();
    }, 400);
  }, [reload]);

  useEffect(
    () => () => {
      if (reloadTimerRef.current !== null) window.clearTimeout(reloadTimerRef.current);
    },
    [],
  );

  useEffect(() => {
    setEvents([]);
    setExpandAll(false);
    lastEventIdRef.current = 0;
    lastRevisionRef.current = 0;
  }, [jobId]);

  const jobUpdatedAt = detail?.job.updated_at;

  useEffect(() => {
    if (!jobUpdatedAt) return;
    const parsed = Date.parse(jobUpdatedAt);
    if (Number.isFinite(parsed)) lastRevisionRef.current = Math.max(lastRevisionRef.current, parsed);
  }, [jobUpdatedAt]);

  const stream = useJobStream(jobId || null, (event) => {
    const id = Number(event.event_id ?? event._sse_id ?? 0);
    if (Number.isFinite(id) && id > 0) {
      if (id <= lastEventIdRef.current) return;
      lastEventIdRef.current = id;
    }
    const rawAt = event.ts ?? event.updated_at;
    const at = typeof rawAt === "string" ? Date.parse(rawAt) : Number.NaN;
    if (Number.isFinite(at) && at < lastRevisionRef.current) return;
    if (event.event_type) {
      setEvents((prev) => [...prev, event].slice(-200));
    }
    scheduleReload();
  });

  useEffect(() => {
    if (detailError && notifiedErrorRef.current !== detailError) {
      notifiedErrorRef.current = detailError;
      dialog.notify("error", "任务详情加载失败", detailError);
    }
  }, [detailError, dialog]);

  const job = detail?.job;
  const progress = detail?.progress;
  const status = job?.status ?? "";
  const addressLayers = useMemo(() => detail?.layers.addresses ?? [], [detail]);
  const datasetLayers = useMemo(() => detail?.layers.datasets ?? [], [detail]);
  const shardLayers = useMemo(() => detail?.layers.shards ?? [], [detail]);

  const canPause = ["RUNNING", "QUEUED", "PAUSE_REQUESTED"].includes(status);
  const canResume = ["PAUSED", "PAUSED_FALLBACK_TOO_EXPENSIVE"].includes(status);
  const canCancel = status !== "" && !TERMINAL_STATUSES.has(status) && status !== "CANCEL_REQUESTED";
  const canRetry = ["FAILED", "PARTIAL_FAILED", "PAUSED_FALLBACK_TOO_EXPENSIVE"].includes(status);

  const latestCursor = useMemo(() => {
    let best: string | null = null;
    let bestMs = Number.NEGATIVE_INFINITY;
    for (const layer of addressLayers) {
      const at = layer.successful_cursor?.checkpoint_at;
      if (!at) continue;
      const parsed = Date.parse(at);
      if (Number.isFinite(parsed) && parsed > bestMs) {
        bestMs = parsed;
        best = at;
      }
    }
    return best;
  }, [addressLayers]);

  const elapsedSeconds = progress && progress.elapsed_seconds > 0 ? progress.elapsed_seconds : null;
  const rowsPerSecond = progress && elapsedSeconds !== null ? progress.downloaded_rows / elapsedSeconds : null;
  const rowsBasis = "downloaded_rows ÷ elapsed_seconds（服务端 wall-clock，含排队与空闲时间）";
  const providerBasis = "当前 /api/v2/jobs/{id} 契约未返回 Provider rows/s，页面不推测该指标。";
  const runCommand = useCallback(
    async (command: "pause" | "resume" | "cancel" | "retry") => {
      if (!jobId) return;
      const titles: Record<string, string> = { pause: "暂停任务？", resume: "恢复任务？", cancel: "取消任务？", retry: "重试失败分片？" };
      const bodies: Record<string, string> = {
        pause: "暂停后停止领取新分片；进行中的分页完成后停止，成功游标不会丢失。",
        resume: "恢复后从成功游标继续，只处理未完成分片。",
        cancel: "取消不会回滚已完成数据；已完成分片、成功游标与已落库数据保留，可用于后续复用。",
        retry: "只重置失败分片与失败的 Dataset/地址状态，已完成分片与成功游标原样保留。",
      };
      const labels: Record<string, string> = { pause: "暂停", resume: "恢复", cancel: "取消", retry: "重试失败分片" };
      const answer = await dialog.open({
        kind: "confirm",
        title: titles[command],
        body: bodies[command],
        details: [{ label: "当前状态", value: status || "—" }],
        input: {
          label: command === "retry" ? "重试原因（可选）" : "操作原因（必填，写入审计）",
          placeholder: command === "retry" ? "例如：Provider 已恢复" : "例如：等待案件范围确认",
        },
        actions: [
          { label: "返回", result: "", variant: "default" },
          { label: labels[command], result: command, variant: command === "cancel" ? "danger" : "primary" },
        ],
      });
      if (answer === null) return;
      const reason = answer.startsWith("input:") ? answer.slice("input:".length).trim() : "";
      if (command !== "retry" && !reason) {
        dialog.notify("warning", "请填写操作原因", "暂停/恢复/取消操作必须填写原因并写入审计。");
        return;
      }
      const response = await run(async () => {
        try {
          return await client.post<{ job_id: string; status: string; command: string; reset_shards?: number; note?: string }>(
            `/api/v2/jobs/${encodeURIComponent(jobId)}/commands/${command}`,
            { reason },
          );
        } catch (error) {
          if (error instanceof ApiError && error.traceId) setLastTrace(error.traceId);
          throw error;
        }
      });
      if (!response) return;
      const result = response.data;
      await dialog.open({
        kind: "success",
        title: `${labels[command]}指令已提交`,
        details: [
          { label: "command", value: result.command },
          { label: "status", value: result.status },
          ...(typeof result.reset_shards === "number" ? [{ label: "reset_shards", value: String(result.reset_shards) }] : []),
          ...(result.note ? [{ label: "note", value: result.note }] : []),
        ],
        actions: [{ label: "知道了", result: "ok", variant: "primary" }],
      });
      void reload();
    },
    [client, dialog, jobId, reload, run, status],
  );

  const downloadExport = useCallback(
    async (item: JobExport) => {
      const answer = await dialog.open({
        kind: "confirm",
        title: "创建下载票据？",
        body: "下载票据 60 秒内有效且仅可使用一次；过期或重复使用会返回 410，需要重新创建票据。",
        details: [
          { label: "export_id", value: item.id },
          { label: "格式", value: `${item.format}${item.zip ? " · zip" : ""}` },
          { label: "行数", value: formatRawAmount(String(item.rows), 0) },
        ],
        actions: [
          { label: "取消", result: "", variant: "default" },
          { label: "创建并下载", result: "download", variant: "primary" },
        ],
      });
      if (answer !== "download") return;
      const response = await run(async () => {
        try {
          return await client.post<{ ticket_id: string; url: string; expires_in_seconds: number; single_use: boolean }>(
            `/api/v2/exports/${encodeURIComponent(item.id)}/download-ticket`,
            undefined,
            { idempotencyKey: crypto.randomUUID() },
          );
        } catch (error) {
          if (error instanceof ApiError && error.traceId) setLastTrace(error.traceId);
          throw error;
        }
      });
      if (!response) return;
      window.location.assign(response.data.url);
    },
    [client, dialog, run],
  );

  if (!jobId) {
    return (
      <>
        <PageHeader title="任务详情" />
        <Card>
          <EmptyState title="缺少任务 ID" />
        </Card>
      </>
    );
  }

  if (detailApi.loading && !detail) {
    return (
      <>
        <PageHeader title="任务详情" actions={<BackLink to="/jobs" label="返回任务中心" />} />
        <Card>
          <SkeletonRows rows={6} columns={4} />
        </Card>
      </>
    );
  }

  if (detailError && !detail) {
    return (
      <>
        <PageHeader title="任务详情" actions={<BackLink to="/jobs" label="返回任务中心" />} />
        <Card>
          <ErrorState title="任务详情加载失败" message="加载失败，请重试。" onRetry={() => void reload()} />
        </Card>
      </>
    );
  }

  if (!detail || !job || !progress) {
    return (
      <>
        <PageHeader title="任务详情" actions={<BackLink to="/jobs" label="返回任务中心" />} />
        <Card>
          <EmptyState title="任务不存在或无权查看" />
        </Card>
      </>
    );
  }

  return (
    <>
      <PageHeader
        title={`任务 ${job.job_no || shortValue(job.id, 10, 6)}`}
        actions={
          <span className="toolbar">
            <BackLink to="/jobs" label="返回任务中心" />
            <StatusBadge tone={statusToneFor(job.status)}>{job.status}</StatusBadge>
            <button type="button" className="btn btn--sm" onClick={() => void reload()}>
              刷新详情
            </button>
          </span>
        }
      />

      <Card
        title="运行概览"
        actions={
          <span className="toolbar">
            {detail.layers.job.error_safe ? (
              <button
                type="button"
                className="btn btn--sm btn--danger"
                onClick={() =>
                  dialog.notify("error", "任务失败原因", detail.layers.job.error_safe, [
                    { label: "job_id", value: job.id },
                    { label: "job_no", value: job.job_no },
                    { label: "status", value: job.status },
                    { label: "Trace", value: lastTrace ?? "—" },
                  ])
                }
              >
                失败原因
              </button>
            ) : null}
            <button type="button" className="btn btn--sm" onClick={() => dialog.notify("info", "rows/s 口径", rowsBasis)}>
              rows/s 口径
            </button>
            <button type="button" className="btn btn--sm" onClick={() => dialog.notify("info", "Provider rows/s", providerBasis)}>
              Provider rows/s 说明
            </button>
          </span>
        }
      >
        <div className="metric-grid">
          <Metric label="requested_mode" value={detail.performance.requested_mode || job.requested_mode || "—"} />
          <Metric label="effective_mode" value={detail.performance.effective_mode || job.effective_mode || "—"} />
          <Metric label="mode_reason" value={detail.performance.mode_reason || job.mode_reason || "—"} />
          <Metric label="成功游标时间" value={latestCursor ? formatTime(latestCursor) : "—"} />
          <Metric label="rows/s（wall-clock）" value={rowsPerSecond === null ? "null" : rowsPerSecond.toFixed(2)} />
          <Metric label="Provider rows/s" value="null" />
          <Metric label="已下载行数" value={formatRawAmount(String(progress.downloaded_rows ?? 0), 0)} />
          <Metric label="已下载字节" value={formatBytes(progress.downloaded_bytes ?? 0)} />
          <Metric label="整体进度" value={percent(progress.progress)} />
          <Metric label="Shard 完成" value={`${progress.done_shards ?? 0} / ${progress.total_shards ?? 0}`} />
          <Metric label="最近更新" value={formatTime(job.updated_at)} />
        </div>
      </Card>

      <Card title="任务控制">
        <div className="toolbar">
          <button type="button" className="btn btn--sm" disabled={!canPause || busy} onClick={() => void runCommand("pause")}>
            暂停
          </button>
          <button type="button" className="btn btn--sm" disabled={!canResume || busy} onClick={() => void runCommand("resume")}>
            恢复
          </button>
          <button type="button" className="btn btn--sm btn--danger" disabled={!canCancel || busy} onClick={() => void runCommand("cancel")}>
            取消任务
          </button>
          <button type="button" className="btn btn--sm" disabled={!canRetry || busy} onClick={() => void runCommand("retry")}>
            重试失败分片
          </button>
        </div>
      </Card>

      <div className="workbench">
        <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: "var(--sp-5)" }}>
          <Card
            title="Pipeline"
            actions={
              <span className="toolbar">
                <button type="button" className="btn btn--sm" onClick={() => setExpandAll((value) => !value)}>
                  {expandAll ? "全部收起" : "全部展开"}
                </button>
                <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>
                  地址 {addressLayers.length} · Dataset {datasetLayers.length} · Shard {shardLayers.length}
                </span>
              </span>
            }
          >
            {addressLayers.length === 0 ? (
              <EmptyState title="暂无地址层数据" />
            ) : (
              <div className="s4-list">
                {addressLayers.map((layer, index) => {
                  const subjectDatasets = datasetLayers.filter((item) => item.subject_id === layer.subject_id);
                  const subjectShards = shardLayers.filter((item) => item.subject_id === layer.subject_id);
                  const cursorAt = layer.successful_cursor?.checkpoint_at;
                  return (
                    <div className="s4-list__item" key={layer.subject_id}>
                      <div className="s4-step__status">
                        <StatusBadge tone={statusToneFor(layer.status)}>{layer.status}</StatusBadge>
                      </div>
                      <div className="s4-list__main">
                        <div className="s4-list__title mono" title={layer.address}>
                          #{index + 1} · {shortValue(layer.address, 16, 10)}
                        </div>
                        <div className="s4-list__sub">
                          状态 {layer.status} · 进度 {percent(layer.progress)} · 成功游标 {cursorAt ? formatTime(cursorAt) : "—"} · 更新{" "}
                          {formatTime(layer.updated_at)}
                        </div>
                        <div style={{ marginTop: "var(--sp-2)" }}>
                          <ProgressBar value={layer.progress} />
                        </div>
                        <details key={`${layer.subject_id}-${expandAll ? "open" : "closed"}`} open={expandAll} style={{ marginTop: "var(--sp-3)" }}>
                          <summary style={{ cursor: "pointer", color: "var(--c-cobalt)", fontSize: "var(--fs-12)" }}>
                            展开 Dataset / Shard（Dataset {subjectDatasets.length} · Shard {subjectShards.length}）
                          </summary>
                          {subjectDatasets.length === 0 ? (
                            <div className="s4-step__meta" style={{ marginTop: "var(--sp-2)" }}>
                              暂无 Dataset 层数据
                            </div>
                          ) : (
                            subjectDatasets.map((dataset) => {
                              const datasetShards = subjectShards.filter((item) => item.dataset === dataset.dataset);
                              return (
                                <div className="s4-step" key={dataset.dataset_id}>
                                  <div className="s4-step__status">
                                    <StatusBadge tone={statusToneFor(dataset.status)}>{dataset.status}</StatusBadge>
                                  </div>
                                  <div className="s4-step__body">
                                    <div className="s4-list__title">{dataset.dataset}</div>
                                    <div className="s4-step__meta">
                                      进度 {percent(dataset.progress)} · Shard {dataset.shards.completed}/{dataset.shards.total}
                                      {dataset.shards.failed > 0 ? ` · 失败 ${dataset.shards.failed}` : ""} · 更新 {formatTime(dataset.updated_at)}
                                    </div>
                                    <ProgressBar value={dataset.progress} />
                                    <div className="table-scroll" style={{ marginTop: "var(--sp-2)" }}>
                                      <table className="table">
                                        <thead>
                                          <tr>
                                            <th>Shard</th>
                                            <th>区块</th>
                                            <th>状态</th>
                                            <th>重试</th>
                                            <th>行数</th>
                                            <th>字节</th>
                                            <th>错误</th>
                                          </tr>
                                        </thead>
                                        <tbody>
                                          {datasetShards.length === 0 ? (
                                            <tr>
                                              <td colSpan={7}>暂无 Shard 层数据</td>
                                            </tr>
                                          ) : (
                                            datasetShards.map((shard) => (
                                              <tr key={shard.shard_id}>
                                                <td className="mono" title={shard.shard_id}>
                                                  {shortValue(shard.shard_id, 10, 6)}
                                                </td>
                                                <td className="mono">
                                                  {shard.from_block} → {shard.to_block}
                                                </td>
                                                <td>
                                                  <StatusBadge tone={statusToneFor(shard.status)}>{shard.status}</StatusBadge>
                                                </td>
                                                <td className="mono">{shard.retries}</td>
                                                <td className="mono">{formatRawAmount(String(shard.downloaded_rows), 0)}</td>
                                                <td className="mono">{formatBytes(shard.downloaded_bytes)}</td>
                                                <td>
                                                  {shard.error_safe ? (
                                                    <button
                                                      type="button"
                                                      className="btn btn--sm"
                                                      onClick={() =>
                                                        dialog.notify("error", "Shard 错误", shard.error_safe, [
                                                          { label: "shard_id", value: shard.shard_id },
                                                          { label: "dataset", value: shard.dataset },
                                                          { label: "retries", value: String(shard.retries) },
                                                          { label: "raw_object", value: shard.raw_object || "—" },
                                                          { label: "Trace", value: lastTrace ?? "—" },
                                                        ])
                                                      }
                                                    >
                                                      查看错误
                                                    </button>
                                                  ) : (
                                                    "—"
                                                  )}
                                                </td>
                                              </tr>
                                            ))
                                          )}
                                        </tbody>
                                      </table>
                                    </div>
                                  </div>
                                </div>
                              );
                            })
                          )}
                        </details>
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </Card>

          <Card
            title="结构化事件日志"
            actions={
              <span className="toolbar">
                <StatusBadge tone={stream.connected ? "ok" : "warn"}>{stream.connected ? "SSE 已连接" : "SSE 未连接"}</StatusBadge>
                <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-11)" }}>last_event_id {stream.lastEventID}</span>
                <button type="button" className="btn btn--sm" onClick={() => setEvents([])}>
                  清空
                </button>
                <button type="button" className="btn btn--sm" onClick={() => void reload()}>
                  刷新
                </button>
              </span>
            }
          >
            {events.length === 0 ? (
              <EmptyState
                title="尚未收到结构化任务事件"
                action={
                  <button type="button" className="btn btn--sm" onClick={() => void reload()}>
                    刷新任务
                  </button>
                }
              />
            ) : (
              <div className="table-scroll" style={{ maxHeight: 360, overflowY: "auto" }}>
                <table className="table">
                  <thead>
                    <tr>
                      <th>时间</th>
                      <th>event_type</th>
                      <th>status</th>
                      <th>progress</th>
                      <th>结构化字段</th>
                      <th>详情</th>
                    </tr>
                  </thead>
                  <tbody>
                    {events.map((event, index) => {
                      const errorMessage = eventErrorMessage(event);
                      const compact = ["command", "reason", "reset_shards", "failed_count"]
                        .map((key) => {
                          const value = event[key];
                          if (typeof value === "string" || typeof value === "number") return `${key}=${String(value)}`;
                          return null;
                        })
                        .filter(Boolean)
                        .join(" · ");
                      return (
                        <tr key={`${String(event.event_id ?? event._sse_id ?? "snapshot")}-${index}`}>
                          <td className="mono">{formatTime(typeof event.ts === "string" ? event.ts : null)}</td>
                          <td className="mono">{typeof event.event_type === "string" ? event.event_type : "snapshot"}</td>
                          <td>
                            {typeof event.status === "string" ? (
                              <StatusBadge tone={statusToneFor(event.status)}>{event.status}</StatusBadge>
                            ) : (
                              "—"
                            )}
                          </td>
                          <td className="mono">{typeof event.progress === "number" ? percent(event.progress) : "—"}</td>
                          <td style={{ fontSize: "var(--fs-11)" }}>{compact || "—"}</td>
                          <td>
                            <div className="toolbar">
                              <button
                                type="button"
                                className="btn btn--sm"
                                onClick={() => dialog.notify("info", "结构化事件详情", null, eventFacts(event))}
                              >
                                字段
                              </button>
                              {errorMessage ? (
                                <button
                                  type="button"
                                  className="btn btn--sm btn--danger"
                                  onClick={() =>
                                    dialog.notify("error", "事件错误", errorMessage, [
                                      { label: "Trace", value: lastTrace ?? "—" },
                                      ...eventFacts(event),
                                    ])
                                  }
                                >
                                  错误
                                </button>
                              ) : null}
                            </div>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </Card>

          <Card
            title="Exports"
            actions={
              <span className="toolbar">
                <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>共 {detail.exports.length} 个</span>
                <button type="button" className="btn btn--sm" onClick={() => navigate("/exports")}>
                  导出中心
                </button>
              </span>
            }
          >
            {detail.exports.length === 0 ? (
              <EmptyState
                title="该任务暂无导出"
                action={
                  <button type="button" className="btn btn--sm" onClick={() => navigate("/exports")}>
                    新建导出
                  </button>
                }
              />
            ) : (
              <div className="table-scroll">
                <table className="table">
                  <thead>
                    <tr>
                      <th>格式</th>
                      <th>状态</th>
                      <th>行数</th>
                      <th>SHA-256</th>
                      <th>大小</th>
                      <th>有效期</th>
                      <th>操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {detail.exports.map((item) => {
                      const available = item.status === "COMPLETED" && (!item.expires_at || Date.parse(item.expires_at) > Date.now());
                      return (
                        <tr key={item.id}>
                          <td className="mono">
                            {item.format}
                            {item.zip ? " · zip" : ""}
                          </td>
                          <td>
                            <StatusBadge tone={statusToneFor(item.status)}>{item.status}</StatusBadge>
                          </td>
                          <td className="mono">{formatRawAmount(String(item.rows), 0)}</td>
                          <td className="mono" title={item.sha256}>
                            {item.sha256 ? shortValue(item.sha256, 12, 8) : "—"}
                          </td>
                          <td className="mono" title="API 未返回字节数">{formatBytes(undefined)}</td>
                          <td className="mono">{item.expires_at ? formatTime(item.expires_at) : "—"}</td>
                          <td>
                            <button type="button" className="btn btn--sm" disabled={!available || busy} onClick={() => void downloadExport(item)}>
                              下载票据
                            </button>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </Card>
        </div>

        <EvidenceRail
          title="任务证据链"
          items={[
            { label: "控制面来源", value: detail.source.control || "—" },
            { label: "使用量来源", value: detail.source.usage || "—" },
            { label: "数据时点", value: formatTime(detail.source.as_of) },
            { label: "任务更新时间", value: formatTime(job.updated_at) },
            { label: "Progress 时点", value: formatTime(progress.ts) },
            { label: "last_event_id", value: stream.lastEventID },
            { label: "最近 Trace", value: lastTrace ?? "—" },
            {
              label: "rows/s 口径",
              value: (
                <button type="button" className="btn btn--sm btn--ghost" onClick={() => dialog.notify("info", "rows/s 口径", rowsBasis)}>
                  查看
                </button>
              ),
            },
            {
              label: "Provider rows/s",
              value: (
                <button type="button" className="btn btn--sm btn--ghost" onClick={() => dialog.notify("info", "Provider rows/s", providerBasis)}>
                  查看
                </button>
              ),
            },
          ]}
        />
      </div>
    </>
  );
}
