import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { ApiError, canUseDirectDownload, directTicketUrl } from "@chain/api-client";
import {
  BackLink,
  Button,
  Card,
  CompactProgress,
  DataTable,
  EmptyState,
  ErrorState,
  HashValue,
  HierarchicalProgress,
  KeyValueList,
  LinearProgress,
  MetaLine,
  MetricCard,
  PageHeader,
  PageSection,
  PageSkeleton,
  PipelineProgress,
  StatusBadge,
  StatusBadgeForState,
  StreamingRate,
  TabPanel,
  Tabs,
  Timeline,
  progressStateFor,
  statusToneFor,
  useDialog,
  useThrottledValue,
} from "@chain/ui";
import type { HierarchicalNode, PipelineStage, ProgressState, TimelineEntry } from "@chain/ui";
import { formatBytes, formatRawAmount, formatTime, shortValue, useApi, useJobStream, useS5Action } from "../sprint5/common";
import type { JobEvent } from "../sprint5/common";

/* ============================================================================
 * U-JOB-02 /jobs/:jobId：四级进度（Job/地址/Dataset/Shard）、SSE 结构化事件、
 * 状态感知控制、导出票据。说明/口径/错误只通过 Dialog 呈现。
 *
 * 表现层（蓝图 §12.19）：顶部常驻运行概览（总进度 + rows/s 与 Provider rows/s 分列 +
 * 状态感知控制），下方分层 tabs（概览 / Pipeline / 地址进度 / Dataset / Shards /
 * 结构化事件 / Exports / 证据链）。高频数值走 useThrottledValue 节流显示，
 * 最终值一致；请求、SSE、命令与导出票据的语义一律未改。
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

/**
 * 展示用的分层状态归并（Pipeline 阶段字形）。
 *
 * 这里**只把服务端各自返回的 status 归并成一个状态字形**，不产生任何数值：
 * 真实计数一律放在阶段的 meta 里原样呈现，前端不平均、不推算百分比。
 */
function rollupState(values: string[]): ProgressState {
  if (values.length === 0) return "queued";
  const states = values.map((value) => progressStateFor(value));
  if (states.some((state) => state === "running")) return "running";
  if (states.some((state) => state === "failed")) return "failed";
  if (states.some((state) => state === "paused")) return "paused";
  if (states.some((state) => state === "waiting")) return "waiting";
  if (states.every((state) => state === "completed")) return "completed";
  if (states.every((state) => state === "cancelled")) return "cancelled";
  return "running";
}

/** 服务端秒数的可读展示；缺失或非法值显示 —，不猜测。 */
function formatDuration(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined || !Number.isFinite(seconds) || seconds < 0) return "—";
  const total = Math.round(seconds);
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const rest = total % 60;
  if (hours > 0) return hours + " 小时 " + minutes + " 分";
  if (minutes > 0) return minutes + " 分 " + rest + " 秒";
  return rest + " 秒";
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
  /* 分层 tabs 的选中项（§12.19）；只在本地持有，不改路由。 */
  const [tab, setTab] = useState("overview");
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

  /**
   * 高频数值展示节流（蓝图 §9.3）：SSE 重连或高频 update 期间数值不抖动，
   * 但最终值必须一致——useThrottledValue 会把最后一次真实值落地。
   * 状态机判断（暂停/恢复/取消可用性）继续读未节流的 detail，不受节流影响。
   */
  const throttledProgress = useThrottledValue(progress ?? null, 300);

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
      // 直连通道优先（更快，不占用主站隧道带宽）；探测不可达时回退主站隧道 URL。
      const directUrl =
        response.data.ticket_id && (await canUseDirectDownload()) ? directTicketUrl(response.data.ticket_id) : "";
      window.location.assign(directUrl || response.data.url);
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

  const breadcrumb = [{ label: "任务中心", to: "/jobs" }, { label: "任务详情" }];

  if (detailApi.loading && !detail) {
    return (
      <>
        <PageHeader title="任务详情" breadcrumb={breadcrumb} actions={<BackLink to="/jobs" label="返回任务中心" />} />
        <PageSkeleton shape="detail" />
      </>
    );
  }

  if (detailError && !detail) {
    return (
      <>
        <PageHeader title="任务详情" breadcrumb={breadcrumb} actions={<BackLink to="/jobs" label="返回任务中心" />} />
        <Card>
          <ErrorState title="任务详情加载失败" message="加载失败，请重试。" onRetry={() => void reload()} />
        </Card>
      </>
    );
  }

  if (!detail || !job || !progress) {
    return (
      <>
        <PageHeader title="任务详情" breadcrumb={breadcrumb} actions={<BackLink to="/jobs" label="返回任务中心" />} />
        <Card>
          <EmptyState title="任务不存在或无权查看" />
        </Card>
      </>
    );
  }

  /* 节流后的显示值：重连/高频 update 期间保留最后一次已知进度，最终值一致（§9.3）。 */
  const shown = throttledProgress ?? progress;
  const shownState = progressStateFor(job.status);
  const shownPercent = Math.round(Math.max(0, Math.min(1, Number.isFinite(shown.progress) ? shown.progress : 0)) * 100);
  const totalShards = shown.total_shards ?? 0;
  const doneShards = shown.done_shards ?? 0;

  /* 展示用索引与求和：只做分组与加法，不产生任何比率（比率一律取服务端 progress）。 */
  const datasetsBySubject = new Map<string, DatasetLayer[]>();
  const shardsBySubject = new Map<string, ShardLayer[]>();
  const rowsBySubject = new Map<string, number>();
  const addressBySubject = new Map<string, AddressLayer>();
  for (const layer of addressLayers) addressBySubject.set(layer.subject_id, layer);
  for (const layer of datasetLayers) {
    const list = datasetsBySubject.get(layer.subject_id);
    if (list) list.push(layer);
    else datasetsBySubject.set(layer.subject_id, [layer]);
  }
  for (const layer of shardLayers) {
    const list = shardsBySubject.get(layer.subject_id);
    if (list) list.push(layer);
    else shardsBySubject.set(layer.subject_id, [layer]);
    rowsBySubject.set(layer.subject_id, (rowsBySubject.get(layer.subject_id) ?? 0) + (layer.downloaded_rows ?? 0));
  }

  const pipelineStages: PipelineStage[] = [
    {
      key: "job",
      label: "任务受理与调度",
      state: progressStateFor(job.status),
      meta: job.status + " · effective_mode " + (detail.performance.effective_mode || job.effective_mode || "—"),
    },
    {
      key: "addresses",
      label: "地址层",
      state: rollupState(addressLayers.map((layer) => layer.status)),
      meta: addressLayers.length + " 个地址 · " + addressLayers.filter((layer) => progressStateFor(layer.status) === "completed").length + " 个已完成",
    },
    {
      key: "datasets",
      label: "Dataset 层",
      state: rollupState(datasetLayers.map((layer) => layer.status)),
      meta: datasetLayers.length + " 条 Dataset 进度 · " + datasetLayers.filter((layer) => layer.shards.failed > 0).length + " 条存在失败分片",
    },
    {
      key: "shards",
      label: "分片下载",
      state: rollupState(shardLayers.map((layer) => layer.status)),
      meta: "完成 " + doneShards + "/" + totalShards + " · 共 " + shardLayers.length + " 个分片",
    },
    {
      key: "exports",
      label: "导出",
      state: rollupState(detail.exports.map((item) => item.status)),
      meta: detail.exports.length + " 个关联导出",
    },
  ];

  const hierarchicalNodes: HierarchicalNode[] = addressLayers.map((layer, index) => {
    const subjectDatasets = datasetsBySubject.get(layer.subject_id) ?? [];
    const subjectShards = shardsBySubject.get(layer.subject_id) ?? [];
    const cursorAt = layer.successful_cursor?.checkpoint_at;
    return {
      key: layer.subject_id,
      label: layer.address,
      percent: Math.round(Math.max(0, Math.min(1, layer.progress)) * 100),
      state: progressStateFor(layer.status),
      defaultExpanded: expandAll,
      meta:
        "#" + (index + 1) + " · " + layer.status + " · 成功游标 " + (cursorAt ? formatTime(cursorAt) : "—") +
        " · rows " + formatRawAmount(String(rowsBySubject.get(layer.subject_id) ?? 0), 0),
      children: subjectDatasets.map((dataset) => ({
        key: dataset.dataset_id,
        label: dataset.dataset,
        percent: Math.round(Math.max(0, Math.min(1, dataset.progress)) * 100),
        state: progressStateFor(dataset.status),
        defaultExpanded: expandAll,
        meta:
          "Shard " + dataset.shards.completed + "/" + dataset.shards.total +
          (dataset.shards.failed > 0 ? " · 失败 " + dataset.shards.failed : "") +
          " · 更新 " + formatTime(dataset.updated_at),
        children: subjectShards
          .filter((shard) => shard.dataset === dataset.dataset)
          .map((shard) => ({
            key: shard.shard_id,
            label: shard.shard_id,
            percent: progressStateFor(shard.status) === "completed" ? 100 : null,
            state: progressStateFor(shard.status),
            meta:
              shard.from_block + " → " + shard.to_block + " · 重试 " + shard.retries +
              " · rows " + formatRawAmount(String(shard.downloaded_rows), 0) +
              " · " + formatBytes(shard.downloaded_bytes),
          })),
      })),
    };
  });

  const tabItems = [
    { key: "overview", label: "概览" },
    { key: "pipeline", label: "Pipeline" },
    { key: "addresses", label: "地址进度", count: addressLayers.length },
    { key: "datasets", label: "Dataset", count: datasetLayers.length },
    { key: "shards", label: "Shards", count: shardLayers.length },
    { key: "events", label: "结构化事件", count: events.length },
    { key: "exports", label: "Exports", count: detail.exports.length },
    { key: "evidence", label: "证据链" },
  ];

  const addressOf = (subjectId: string) => {
    const layer = addressBySubject.get(subjectId);
    return layer ? <HashValue value={layer.address} head={12} tail={8} label="地址" /> : "—";
  };

  return (
    <>
      <PageHeader
        title={"任务 " + (job.job_no || shortValue(job.id, 10, 6))}
        breadcrumb={[{ label: "任务中心", to: "/jobs" }, { label: job.job_no || shortValue(job.id, 10, 6) }]}
        status={<StatusBadgeForState value={job.status} />}
        meta={
          <MetaLine
            items={[
              { label: "链", value: job.chain || "—" },
              { label: "范围", value: rangeText(job) },
              { label: "创建", value: formatTime(job.created_at) },
              { label: "更新", value: formatTime(job.updated_at) },
              { label: "数据时点", value: formatTime(detail.source.as_of) },
            ]}
          />
        }
        actions={
          <span className="toolbar">
            <BackLink to="/jobs" label="返回任务中心" />
            <Button size="sm" onClick={() => void reload()}>刷新详情</Button>
          </span>
        }
      />

      {/* 顶部常驻运行概览：状态、总进度、两种 rows/s 分列、状态感知控制 */}
      <PageSection
        title="运行概览"
        count={status || "—"}
        actions={
          <span className="toolbar">
            {detail.layers.job.error_safe ? (
              <Button
                size="sm"
                variant="danger"
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
              </Button>
            ) : null}
            <Button size="sm" onClick={() => dialog.notify("info", "rows/s 口径", rowsBasis)}>rows/s 口径</Button>
            <Button size="sm" onClick={() => dialog.notify("info", "Provider rows/s", providerBasis)}>Provider rows/s 说明</Button>
          </span>
        }
      >
        <Card>
          <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-5)", minWidth: 0 }}>
            <LinearProgress
              percent={shownPercent}
              state={shownState}
              label="任务总进度"
              valueText={percent(shown.progress) + " · Shard " + doneShards + "/" + totalShards}
              size="lg"
            />
            <StreamingRate
              ariaLabel="任务速率"
              metrics={[
                {
                  key: "rows",
                  label: "rows/s（wall-clock）",
                  value: rowsPerSecond === null ? "—" : rowsPerSecond.toFixed(2),
                  hint: "downloaded_rows ÷ elapsed_seconds",
                },
                {
                  key: "provider",
                  label: "Provider rows/s",
                  value: "—",
                  hint: "当前接口未返回该指标",
                },
              ]}
            />
            <div className="toolbar">
              <Button size="sm" disabled={!canPause || busy} onClick={() => void runCommand("pause")}>暂停</Button>
              <Button size="sm" disabled={!canResume || busy} onClick={() => void runCommand("resume")}>恢复</Button>
              <Button size="sm" variant="danger" disabled={!canCancel || busy} onClick={() => void runCommand("cancel")}>取消任务</Button>
              <Button size="sm" disabled={!canRetry || busy} onClick={() => void runCommand("retry")}>重试失败分片</Button>
            </div>
          </div>
        </Card>
      </PageSection>

      <Tabs items={tabItems} value={tab} onChange={setTab} ariaLabel="任务详情分层" />

      {tab === "overview" ? (
        <TabPanel tabKey="overview">
          <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-5)", minWidth: 0 }}>
          <PageSection title="指标">
            <div className="metric-grid cs-stagger">
              <MetricCard label="状态" value={<StatusBadgeForState value={job.status} />} />
              <MetricCard label="总进度" value={percent(shown.progress)} tone={shownPercent >= 100 ? "ok" : "neutral"} />
              <MetricCard
                label="rows"
                value={formatRawAmount(String(shown.downloaded_rows ?? 0), 0)}
                source="progress.downloaded_rows"
                sourceNote="服务端进度接口返回的已下载行数，界面不做累加。"
              />
              <MetricCard label="bytes" value={formatBytes(shown.downloaded_bytes ?? 0)} />
              <MetricCard label="请求" value={formatRawAmount(String(shown.api_requests ?? 0), 0)} />
              <MetricCard
                label="API 成本 USD"
                value={typeof shown.api_cost_usd === "number" ? String(shown.api_cost_usd) : "—"}
                source="progress.api_cost_usd"
                sourceNote="服务端返回的成本数值原样展示，界面不做换算或二次计算。"
              />
              <MetricCard label="elapsed" value={formatDuration(shown.elapsed_seconds)} />
              <MetricCard label="ETA" value={formatDuration(shown.eta_seconds)} />
              <MetricCard label="Shard 完成" value={doneShards + " / " + totalShards} />
              {shown.coverage_hit_ratio === undefined ? null : <MetricCard label="Coverage 命中率" value={percent(shown.coverage_hit_ratio)} />}
            </div>
          </PageSection>
          <PageSection title="模式与时间">
            <Card>
              <KeyValueList
                ariaLabel="任务模式与时间"
                items={[
                  { label: "requested_mode", value: detail.performance.requested_mode || job.requested_mode || "—" },
                  { label: "effective_mode", value: detail.performance.effective_mode || job.effective_mode || "—" },
                  { label: "mode_reason", value: detail.performance.mode_reason || job.mode_reason || "—", wide: true },
                  { label: "成功游标时间", value: latestCursor ? formatTime(latestCursor) : "—" },
                  { label: "任务更新时间", value: formatTime(job.updated_at) },
                  { label: "Progress 时点", value: formatTime(shown.ts) },
                  { label: "复用策略", value: job.reuse_policy || "—" },
                  { label: "范围", value: rangeText(job) },
                  { label: "Dataset", value: job.datasets.length > 0 ? job.datasets.join("、") : "—", wide: true },
                ]}
              />
            </Card>
          </PageSection>
          </div>
        </TabPanel>
      ) : null}

      {tab === "pipeline" ? (
        <TabPanel tabKey="pipeline">
          <PageSection title="Pipeline" count={status || "—"}>
            <Card>
              <PipelineProgress stages={pipelineStages} ariaLabel="任务流水线阶段" />
            </Card>
          </PageSection>
        </TabPanel>
      ) : null}

      {tab === "addresses" ? (
        <TabPanel tabKey="addresses">
          <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-5)", minWidth: 0 }}>
          <PageSection
            title="地址进度"
            count={addressLayers.length + " 个地址"}
            actions={
              <Button size="sm" onClick={() => setExpandAll((value) => !value)}>
                {expandAll ? "全部收起" : "全部展开"}
              </Button>
            }
          >
            <Card>
              {addressLayers.length === 0 ? (
                <EmptyState title="暂无地址层数据" />
              ) : (
                <HierarchicalProgress key={expandAll ? "hier-open" : "hier-closed"} nodes={hierarchicalNodes} ariaLabel="地址与 Dataset 分层进度" />
              )}
            </Card>
          </PageSection>
          <PageSection title="按地址核对" count={addressLayers.length + " 行"}>
            <Card>
              <DataTable<AddressLayer>
                rows={addressLayers}
                rowKey={(row) => row.subject_id}
                caption="每个地址的服务端状态、进度与成功游标"
                columns={[
                  { key: "address", header: "地址", width: "220px", frozen: true, render: (row) => <HashValue value={row.address} head={12} tail={8} label="地址" /> },
                  { key: "status", header: "状态", width: "130px", render: (row) => <StatusBadgeForState value={row.status} /> },
                  {
                    key: "progress",
                    header: "进度",
                    width: "200px",
                    render: (row) => (
                      <CompactProgress
                        percent={Math.round(Math.max(0, Math.min(1, row.progress)) * 100)}
                        state={progressStateFor(row.status)}
                        label={"地址进度 " + row.status}
                      />
                    ),
                  },
                  { key: "rows", header: "rows（分片合计）", width: "160px", render: (row) => formatRawAmount(String(rowsBySubject.get(row.subject_id) ?? 0), 0) },
                  { key: "cursor", header: "成功游标", width: "180px", render: (row) => (row.successful_cursor?.checkpoint_at ? formatTime(row.successful_cursor.checkpoint_at) : "—") },
                  { key: "updated", header: "更新", width: "180px", render: (row) => formatTime(row.updated_at) },
                ]}
                empty={<EmptyState title="暂无地址层数据" />}
              />
            </Card>
          </PageSection>
          </div>
        </TabPanel>
      ) : null}

      {tab === "datasets" ? (
        <TabPanel tabKey="datasets">
          <PageSection title="Dataset 进度" count={datasetLayers.length + " 条"}>
            <Card>
              <DataTable<DatasetLayer>
                rows={datasetLayers}
                rowKey={(row) => row.dataset_id}
                caption="每个地址在每个 Dataset 上的状态与进度"
                columns={[
                  { key: "address", header: "地址", width: "220px", frozen: true, render: (row) => addressOf(row.subject_id) },
                  { key: "dataset", header: "Dataset", width: "150px", render: (row) => row.dataset },
                  { key: "status", header: "状态", width: "130px", render: (row) => <StatusBadgeForState value={row.status} /> },
                  {
                    key: "progress",
                    header: "进度",
                    width: "200px",
                    render: (row) => (
                      <CompactProgress
                        percent={Math.round(Math.max(0, Math.min(1, row.progress)) * 100)}
                        state={progressStateFor(row.status)}
                        label={row.dataset + " 进度"}
                      />
                    ),
                  },
                  { key: "shards", header: "Shard 完成 / 总数", width: "170px", render: (row) => row.shards.completed + " / " + row.shards.total },
                  { key: "failed", header: "失败分片", width: "110px", render: (row) => (row.shards.failed > 0 ? <StatusBadge tone="danger">{String(row.shards.failed)}</StatusBadge> : "0") },
                  { key: "updated", header: "更新", width: "180px", render: (row) => formatTime(row.updated_at) },
                ]}
                empty={<EmptyState title="暂无 Dataset 层数据" />}
              />
            </Card>
          </PageSection>
        </TabPanel>
      ) : null}

      {tab === "shards" ? (
        <TabPanel tabKey="shards">
          <PageSection title="Shards" count={shardLayers.length + " 个分片"}>
            <Card>
              <DataTable<ShardLayer>
                rows={shardLayers}
                rowKey={(row) => row.shard_id}
                caption="每个分片的范围、分页游标、重试、行数、原始对象与错误"
                columns={[
                  { key: "shard", header: "分片", width: "180px", frozen: true, render: (row) => <HashValue value={row.shard_id} head={10} tail={6} label="分片 ID" /> },
                  { key: "address", header: "地址", width: "200px", render: (row) => addressOf(row.subject_id) },
                  { key: "dataset", header: "Dataset", width: "140px", render: (row) => row.dataset },
                  { key: "range", header: "区块范围", width: "180px", render: (row) => <span className="mono">{row.from_block + " → " + row.to_block}</span> },
                  { key: "status", header: "状态", width: "130px", render: (row) => <StatusBadgeForState value={row.status} /> },
                  {
                    key: "cursor",
                    header: "页 / cursor",
                    width: "200px",
                    render: (row) => {
                      const page = row.checkpoint?.page_no;
                      const token = row.checkpoint?.next_page_token;
                      return (
                        <span style={{ display: "inline-flex", alignItems: "center", gap: "var(--sp-2)", minWidth: 0 }}>
                          <span className="mono">{page === undefined || page === null ? "—" : "p" + String(page)}</span>
                          {token ? <HashValue value={token} head={8} tail={6} label="cursor" /> : null}
                        </span>
                      );
                    },
                  },
                  { key: "retries", header: "重试", width: "80px", render: (row) => String(row.retries) },
                  { key: "rows", header: "rows", width: "120px", render: (row) => formatRawAmount(String(row.downloaded_rows), 0) },
                  { key: "bytes", header: "字节", width: "120px", render: (row) => formatBytes(row.downloaded_bytes) },
                  { key: "raw", header: "raw object", width: "230px", render: (row) => (row.raw_object ? <HashValue value={row.raw_object} head={14} tail={10} label="raw object" /> : "—") },
                  {
                    key: "error",
                    header: "错误",
                    width: "130px",
                    render: (row) =>
                      row.error_safe ? (
                        <Button
                          size="sm"
                          onClick={() =>
                            dialog.notify("error", "Shard 错误", row.error_safe, [
                              { label: "shard_id", value: row.shard_id },
                              { label: "dataset", value: row.dataset },
                              { label: "retries", value: String(row.retries) },
                              { label: "raw_object", value: row.raw_object || "—" },
                              { label: "Trace", value: lastTrace ?? "—" },
                            ])
                          }
                        >
                          查看错误
                        </Button>
                      ) : (
                        "—"
                      ),
                  },
                ]}
                empty={<EmptyState title="暂无 Shard 层数据" />}
              />
            </Card>
          </PageSection>
        </TabPanel>
      ) : null}

      {tab === "events" ? (
        <TabPanel tabKey="events">
          <PageSection
            title="结构化事件"
            count={events.length + " 条"}
            actions={
              <span className="toolbar">
                <StatusBadge tone={stream.connected ? "ok" : "warn"}>{stream.connected ? "SSE 已连接" : "SSE 未连接"}</StatusBadge>
                <span className="cs-section__count">{`last_event_id ${stream.lastEventID}`}</span>
                <Button size="sm" onClick={() => setEvents([])}>清空</Button>
                <Button size="sm" onClick={() => void reload()}>刷新</Button>
              </span>
            }
          >
            <Card>
              {events.length === 0 ? (
                <EmptyState
                  title="尚未收到结构化任务事件"
                  action={<Button size="sm" onClick={() => void reload()}>刷新任务</Button>}
                />
              ) : (
                <div style={{ maxHeight: 520, overflowY: "auto", minWidth: 0 }}>
                  <Timeline
                    ariaLabel="任务结构化事件"
                    items={events.map((event, index): TimelineEntry => {
                      const errorMessage = eventErrorMessage(event);
                      const compact = ["command", "reason", "reset_shards", "failed_count"]
                        .map((key) => {
                          const value = event[key];
                          if (typeof value === "string" || typeof value === "number") return key + "=" + String(value);
                          return null;
                        })
                        .filter(Boolean)
                        .join(" · ");
                      const statusText = typeof event.status === "string" ? event.status : "";
                      const progressText = typeof event.progress === "number" ? percent(event.progress) : "";
                      const metaParts = [
                        formatTime(typeof event.ts === "string" ? event.ts : null),
                        statusText || null,
                        progressText || null,
                        compact || null,
                      ].filter(Boolean);
                      return {
                        key: `${String(event.event_id ?? event._sse_id ?? "snapshot")}-${index}`,
                        title: typeof event.event_type === "string" ? event.event_type : "snapshot",
                        tone: statusText ? statusToneFor(statusText) : "neutral",
                        meta: metaParts.join(" · "),
                        aside: (
                          <span className="toolbar">
                            <Button size="sm" onClick={() => dialog.notify("info", "结构化事件详情", null, eventFacts(event))}>字段</Button>
                            {errorMessage ? (
                              <Button
                                size="sm"
                                variant="danger"
                                onClick={() =>
                                  dialog.notify("error", "事件错误", errorMessage, [
                                    { label: "Trace", value: lastTrace ?? "—" },
                                    ...eventFacts(event),
                                  ])
                                }
                              >
                                错误
                              </Button>
                            ) : null}
                          </span>
                        ),
                      };
                    })}
                  />
                </div>
              )}
            </Card>
          </PageSection>
        </TabPanel>
      ) : null}

      {tab === "exports" ? (
        <TabPanel tabKey="exports">
          <PageSection
            title="Exports"
            count={detail.exports.length + " 个"}
            actions={<Button size="sm" onClick={() => navigate("/exports")}>导出中心</Button>}
          >
            <Card>
              <DataTable<JobExport>
                rows={detail.exports}
                rowKey={(row) => row.id}
                caption="与该任务关联的导出及下载状态"
                columns={[
                  { key: "format", header: "格式", width: "150px", render: (row) => <span className="mono">{row.format + (row.zip ? " · zip" : "")}</span> },
                  { key: "status", header: "状态", width: "130px", render: (row) => <StatusBadgeForState value={row.status} /> },
                  { key: "rows", header: "行数", width: "120px", render: (row) => formatRawAmount(String(row.rows), 0) },
                  { key: "sha", header: "SHA-256", width: "250px", render: (row) => (row.sha256 ? <HashValue value={row.sha256} head={12} tail={8} label="导出 SHA-256" /> : "—") },
                  { key: "bytes", header: "大小", width: "110px", render: () => <span className="mono" title="API 未返回字节数">{formatBytes(undefined)}</span> },
                  { key: "expires", header: "有效期", width: "180px", render: (row) => (row.expires_at ? formatTime(row.expires_at) : "—") },
                  {
                    key: "actions",
                    header: "操作",
                    width: "140px",
                    render: (row) => {
                      const available = row.status === "COMPLETED" && (!row.expires_at || Date.parse(row.expires_at) > Date.now());
                      return (
                        <Button size="sm" disabled={!available || busy} onClick={() => void downloadExport(row)}>
                          下载票据
                        </Button>
                      );
                    },
                  },
                ]}
                empty={<EmptyState title="该任务暂无导出" action={<Button size="sm" onClick={() => navigate("/exports")}>新建导出</Button>} />}
              />
            </Card>
          </PageSection>
        </TabPanel>
      ) : null}

      {tab === "evidence" ? (
        <TabPanel tabKey="evidence">
          <PageSection title="证据链" count={detail.exports.length + " 个文件"}>
            <Card>
              <KeyValueList
                ariaLabel="任务证据链"
                items={[
                  { label: "任务 ID", value: <HashValue value={job.id} wrap label="任务 ID" />, wide: true },
                  { label: "任务号", value: job.job_no || "—" },
                  { label: "链", value: job.chain || "—" },
                  { label: "范围", value: rangeText(job) },
                  { label: "对象", value: String(detail.subjects.length) + " 个" },
                  { label: "Dataset 层", value: String(datasetLayers.length) + " 条" },
                  { label: "分片", value: String(shardLayers.length) + " 个" },
                  { label: "文件（导出）", value: String(detail.exports.length) + " 个" },
                  { label: "请求（控制面）", value: <span className="mono">{"/api/v2/jobs/" + job.id}</span>, wide: true },
                  { label: "审计关联", value: <span className="mono">{"/api/v2/jobs/" + job.id + "/commands/{command}"}</span>, wide: true },
                  { label: "最近 Trace", value: lastTrace ? <HashValue value={lastTrace} label="Trace" /> : "—" },
                  { label: "控制面来源", value: detail.source.control || "—" },
                  { label: "使用量来源", value: detail.source.usage || "—" },
                  { label: "数据时点", value: formatTime(detail.source.as_of) },
                  { label: "任务更新时间", value: formatTime(job.updated_at) },
                  { label: "Progress 时点", value: formatTime(shown.ts) },
                  { label: "成功游标时间", value: latestCursor ? formatTime(latestCursor) : "—" },
                  { label: "last_event_id", value: stream.lastEventID },
                  { label: "SSE", value: stream.connected ? "已连接" : "未连接" },
                ]}
              />
            </Card>
          </PageSection>
        </TabPanel>
      ) : null}
    </>
  );
}
