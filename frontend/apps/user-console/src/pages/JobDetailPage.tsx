import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import {
  BackLink,
  Button,
  Card,
  EmptyState,
  ErrorState,
  Field,
  PageSkeleton,
  useDialog,
  useThrottledValue,
} from "@chain/ui";
import { CsError, CsPageHeader } from "@chain/user-ui";
import { ApiError, canUseDirectDownload, directTicketUrl, useAuth } from "@chain/api-client";
import { shortValue, useApi, useJobStream, useS5Action } from "../sprint5/common";
import type { JobEvent } from "../sprint5/common";
import {
  TERMINAL_STATUSES,
  formatDuration,
  percent,
  type JobDetailData,
  type JobProgress,
  type JobExport,
} from "./jobs/detail/types";
import { JobPrimaryProgress, jobStageText } from "./jobs/detail/JobPrimaryProgress";
import { JobControlBar } from "./jobs/detail/JobControlBar";
import { JobCompletionPanel } from "./jobs/detail/JobCompletionPanel";
import { JobFailurePanel } from "./jobs/detail/JobFailurePanel";
import { JobTechnicalDetails } from "./jobs/detail/JobTechnicalDetails";

/* ============================================================================
 * U-JOB-02 /jobs/:jobId：下载任务详情
 *
 * 表现层按用户端重构规范 V4 §28 / §47 重构为「主状态摘要 + 技术详情」两层：
 *
 *   首屏（普通下载用户）：状态、总进度、当前速度、预计剩余/预计完成、
 *   当前阶段、暂停/继续/取消/重试，以及完成后的导出与下载入口。
 *
 *   二级入口（支持人员）：Pipeline / 地址进度 / Dataset / Shards / 结构化事件 /
 *   Exports / 证据链 —— 一个都没删，全部收进默认关闭的 JobTechnicalDetails。
 *
 * 保留 SSE 订阅与作用域隔离、节流显示、命令与审计原因、导出票据和直连回退；
 * 完成态可直接调用现有按任务导出接口。
 * ==========================================================================*/

export default function JobDetailPage() {
  const dialog = useDialog();
  const { can, session } = useAuth();
  const progressScope = `${session?.organization_id ?? ""}:${session?.workspace_id ?? ""}`;
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
  /* 技术详情默认关闭（§28.3）。不新增 URL key：当前页面没有 tab 深链接契约。 */
  const [technicalOpen, setTechnicalOpen] = useState(false);
  const [technicalTab, setTechnicalTab] = useState("pipeline");
  const lastEventIdRef = useRef(0);
  const lastRevisionRef = useRef(0);
  const reloadTimerRef = useRef<number | null>(null);
  const notifiedErrorRef = useRef<string | null>(null);
  const lastStreamAtRef = useRef(Date.now());
  const [streamProgress, setStreamProgress] = useState<{ jobId: string; scope: string; value: JobProgress } | null>(null);

  const scheduleReload = useCallback(() => {
    if (reloadTimerRef.current !== null) return;
    reloadTimerRef.current = window.setTimeout(() => {
      reloadTimerRef.current = null;
      if (document.visibilityState === "visible") void reload();
    }, 1000);
  }, [reload]);

  useEffect(
    () => () => {
      if (reloadTimerRef.current !== null) window.clearTimeout(reloadTimerRef.current);
      reloadTimerRef.current = null;
    },
    [reload],
  );

  useEffect(() => {
    setEvents([]);
    setStreamProgress(null);
    lastStreamAtRef.current = Date.now();
    setExpandAll(false);
    setTechnicalOpen(false);
    lastEventIdRef.current = 0;
    lastRevisionRef.current = 0;
  }, [jobId, progressScope]);

  const jobUpdatedAt = detail?.job.updated_at;

  useEffect(() => {
    if (!jobUpdatedAt) return;
    const parsed = Date.parse(jobUpdatedAt);
    if (Number.isFinite(parsed)) lastRevisionRef.current = Math.max(lastRevisionRef.current, parsed);
  }, [jobUpdatedAt]);

  /*
   * SSE 订阅与作用域隔离逻辑保持原样。
   * 规范 §9.3 Streaming Page：路由离开立即 cleanup；这里由 useJobStream 负责，
   * 本次重构**没有**给页面加任何退出动画，避免延迟连接关闭。
   */
  const stream = useJobStream(jobId || null, (event) => {
    if (event.job_id && event.job_id !== jobId) return;
    const id = Number(event.event_id ?? event._sse_id ?? 0);
    if (Number.isFinite(id) && id > 0) {
      if (id <= lastEventIdRef.current) return;
      lastEventIdRef.current = id;
    }
    const rawAt = event.ts ?? event.updated_at;
    const at = typeof rawAt === "string" ? Date.parse(rawAt) : Number.NaN;
    if (Number.isFinite(at) && at < lastRevisionRef.current) return;
    lastStreamAtRef.current = Date.now();
    const isProgress = typeof event.progress === "number" && typeof event.downloaded_rows === "number";
    if (isProgress) {
      setStreamProgress({ jobId, scope: progressScope, value: event as unknown as JobProgress });
      // Live counters come directly from SSE. Full detail calibration has a
      // separate bounded timer, so every frame does not trigger a heavy GET.
      if (event.status === detail?.job.status) return;
    }
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
  const liveProgress = streamProgress?.jobId === jobId && streamProgress.scope === progressScope ? streamProgress.value : null;
  const progress = liveProgress && liveProgress.status === detail?.job.status &&
    Date.parse(liveProgress.ts) >= Date.parse(detail.progress.ts) ? liveProgress : detail?.progress;
  const status = job?.status ?? "";
  const [lookupParams] = useSearchParams();
  const lookupChain = lookupParams.get("lookup_chain") ?? "";
  const lookupAddress = lookupParams.get("lookup_address") ?? "";
  const completionCoverage = useApi<{ items: Array<{ usable: boolean; history_complete: boolean; reason: string }> }>(
    status === "COMPLETED" && lookupChain && lookupAddress && can("explorer:read")
      ? `/api/v2/explorer/addresses/${encodeURIComponent(lookupChain)}/${encodeURIComponent(lookupAddress)}/history-coverage` : null,
    [jobId, status, lookupChain, lookupAddress],
  );
  const completionShown = useRef("");
  const completionErrorShown = useRef("");
  useEffect(() => {
    if (!completionCoverage.error || completionErrorShown.current === completionCoverage.error) return;
    completionErrorShown.current = completionCoverage.error;
    dialog.notify("error", "覆盖检查未完成", completionCoverage.error);
  }, [completionCoverage.error, dialog]);
  useEffect(() => {
    const key = `${progressScope}/${jobId}/${lookupChain}/${lookupAddress}`;
    if (!completionCoverage.data || completionShown.current === key) return;
    completionShown.current = key;
    const items = completionCoverage.data.items;
    const complete = items.length > 0 && items.every(item => item.usable);
    const review = dialog.openSession({ kind: complete ? "success" : "info", title: complete ? "地址数据补全完成" : "任务完成，覆盖仍需核对",
      body: complete ? "已重新检查覆盖记录，可以返回地址查看数据。" : "下载任务已结束，当前记录尚未证明所有历史区间均已覆盖。",
      details: [{ label: "链", value: lookupChain }, { label: "地址", value: lookupAddress }],
      actions: [{ label: "查看地址数据", result: "view", variant: "primary" }, { label: "留在任务详情", result: "", variant: "default" }],
    });
    void review.result.then(answer => { if (answer === "view") navigate(`/addresses/${encodeURIComponent(lookupChain)}/${encodeURIComponent(lookupAddress)}`); });
    return review.dismiss;
  }, [completionCoverage.data, dialog, jobId, lookupChain, lookupAddress, navigate, progressScope]);
  const hasPendingExport = detail?.exports.some((item) => item.status === "PENDING" || item.status === "RUNNING") ?? false;

  useEffect(() => {
    if (!hasPendingExport) return;
    const timer = window.setInterval(() => { if (document.visibilityState === "visible") void reload(); }, 3000);
    return () => window.clearInterval(timer);
  }, [hasPendingExport, reload]);

  useEffect(() => {
    if (!status || TERMINAL_STATUSES.has(status) || ["PAUSED", "WAITING_USER_CONFIRMATION", "PAUSED_FALLBACK_TOO_EXPENSIVE"].includes(status)) return;
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") void reload();
    }, 15000);
    const onVisible = () => { if (document.visibilityState === "visible") void reload(); };
    document.addEventListener("visibilitychange", onVisible);
    return () => { window.clearInterval(timer); document.removeEventListener("visibilitychange", onVisible); };
  }, [status, stream.connected, reload]);

  /**
   * 高频数值展示节流（蓝图 §9.3）：SSE 重连或高频 update 期间数值不抖动，
   * 但最终值一致——useThrottledValue 会把最后一次真实值落地。
   * 状态机判断（暂停/恢复/取消可用性）继续读未节流的 detail，不受节流影响。
   */
  const throttledProgress = useThrottledValue(progress ?? null, 300);

  const canPause = ["RUNNING", "QUEUED", "PAUSE_REQUESTED"].includes(status);
  const canResume = ["PAUSED", "PAUSED_FALLBACK_TOO_EXPENSIVE"].includes(status);
  const canCancel = status !== "" && !TERMINAL_STATUSES.has(status) && status !== "CANCEL_REQUESTED";
  const canRetry = ["FAILED", "PARTIAL_FAILED", "PAUSED_FALLBACK_TOO_EXPENSIVE"].includes(status);

  const rowsBasis = "服务端实时 rows_per_sec（最近60秒下载记录增量；不足60秒使用已有采样时段）";
  const providerBasis = "实时速率来自任务 SSE；未收到有效速率时显示 —。";

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
    [client, run],
  );

  const createExport = useCallback(async () => {
    if (!jobId || status !== "COMPLETED" || !can("export:create")) return;
    let format = "csv";
    const answer = await dialog.open({
      kind: "confirm",
      title: "选择导出格式",
      body: (
        <Field label="文件格式" htmlFor="job-export-format">
          <select id="job-export-format" className="select" defaultValue="csv" onChange={(event) => { format = event.currentTarget.value; }}>
            <option value="csv">CSV</option>
            <option value="xlsx">XLSX</option>
            <option value="jsonl">JSONL</option>
            <option value="parquet">Parquet</option>
          </select>
        </Field>
      ),
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "开始生成", result: "create", variant: "primary" },
      ],
    });
    if (answer !== "create") return;
    const response = await run(async () => {
      try {
        return await client.post<{ id: string; status: string; format: string }>(
          "/api/v2/exports",
          { job_id: jobId, format },
          { idempotencyKey: crypto.randomUUID() },
        );
      } catch (error) {
        if (error instanceof ApiError && error.traceId) setLastTrace(error.traceId);
        throw error;
      }
    });
    if (!response) return;
    await reload();
  }, [can, client, dialog, jobId, reload, run, status]);

  if (!jobId) {
    return (
      <>
        <CsPageHeader title="任务详情" />
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
        <CsPageHeader title="任务详情" breadcrumb={breadcrumb} actions={<BackLink to="/jobs" label="返回任务中心" />} />
        <PageSkeleton shape="detail" />
      </>
    );
  }

  if (detailError && !detail) {
    return (
      <>
        <CsPageHeader title="任务详情" breadcrumb={breadcrumb} actions={<BackLink to="/jobs" label="返回任务中心" />} />
        <Card>
          <CsError title="任务详情加载失败" body="加载失败，请重试。" onRetry={() => void reload()} />
        </Card>
      </>
    );
  }

  if (!detail || !job || !progress) {
    return (
      <>
        <CsPageHeader title="任务详情" breadcrumb={breadcrumb} actions={<BackLink to="/jobs" label="返回任务中心" />} />
        <Card>
          <EmptyState title="任务不存在或无权查看" />
        </Card>
      </>
    );
  }

  /* 节流后的显示值：重连/高频 update 期间保留最后一次已知进度，最终值一致（§9.3）。 */
  const shown = throttledProgress ?? progress;
  const clamped = Math.max(0, Math.min(1, Number.isFinite(shown.progress) ? shown.progress : 0));
  const shownPercent = Math.round(clamped * 100);
  const totalShards = shown.total_shards ?? 0;
  const doneShards = shown.done_shards ?? 0;

  /*
   * 主速度（§28.5 优先级）：
   * 当前速度使用服务端 SSE 的实时增量速率。缺少有效样本时显示 —，
   * 不把包含排队和尾部等待的全程平均速度标成当前速度。
   * 速度为 0 不自动解释为故障。
   */
  const elapsedSeconds = shown.elapsed_seconds > 0 ? shown.elapsed_seconds : null;
  const rowsPerSecond = typeof shown.rows_per_sec === "number" && Number.isFinite(shown.rows_per_sec) ? shown.rows_per_sec : null;

  const isFailed = ["FAILED", "PARTIAL_FAILED", "ABORTED_BY_USER"].includes(status);
  const isCompleted = status === "COMPLETED";

  const stageText = jobStageText({
    status,
    datasets: job.datasets ?? [],
    doneShards,
    totalShards,
    fetchedShards: shown.fetched_shards,
    fetchedRows: shown.fetched_rows,
    ingestedRows: shown.ingested_rows,
    downloadedRows: shown.downloaded_rows,
  });

  const headerActions = (
    <span className="toolbar">
      <BackLink to="/jobs" label="返回任务中心" />
      <Button size="sm" onClick={() => void reload()}>
        刷新详情
      </Button>
    </span>
  );

  return (
    <>
      <CsPageHeader
        title={"任务 " + (job.job_no || shortValue(job.id, 10, 6))}
        breadcrumb={breadcrumb}
        subtitle={
          [
            job.chain || "—",
            (job.datasets ?? []).length > 0 ? job.datasets.join("、") : null,
            (detail.layers.addresses?.length ?? 0) > 0 ? `${detail.layers.addresses.length} 个地址` : null,
          ]
            .filter(Boolean)
            .join(" · ")
        }
        actions={headerActions}
      />

      {/*
        首屏信息层级（§28.1）：三选一。
          失败 → 一句安全文案 + 重试 + 技术详情（不染红整页、不展开日志）；
          完成 → 总耗时/文件大小/完成时间 + 下载入口；
          其余 → 主进度卡（进度、速度、ETA、预计完成、阶段）。
      */}
      {isFailed ? (
        <JobFailurePanel
          safeMessage={detail.layers.job.error_safe}
          canRetry={canRetry}
          busy={busy}
          onRetry={() => void runCommand("retry")}
          onOpenTechnical={() => {
            setTechnicalTab("shards");
            setTechnicalOpen(true);
          }}
        />
      ) : isCompleted ? (
        <JobCompletionPanel
          elapsedSeconds={elapsedSeconds}
          downloadedBytes={shown.downloaded_bytes ?? 0}
          resolvedAt={job.updated_at}
          exports={detail.exports}
          busy={busy}
          canCreateExport={can("export:create")}
          onCreateExport={() => void createExport()}
          onDownload={(item) => void downloadExport(item)}
          onShowExportError={(item) => dialog.notify("error", "导出失败", item.error_safe || "文件生成失败，请重试。", [
            { label: "导出 ID", value: item.id },
          ])}
          onOpenExports={() => {
            setTechnicalTab("exports");
            setTechnicalOpen(true);
          }}
        />
      ) : (
        <JobPrimaryProgress
          status={status}
          percentValue={shownPercent}
          rawProgress={shown.progress}
          downloadedRows={shown.downloaded_rows ?? 0}
          downloadedBytes={shown.downloaded_bytes ?? 0}
          rowsPerSecond={rowsPerSecond}
          etaSeconds={rowsPerSecond !== null && rowsPerSecond > 0 && shown.tail_eta_seconds && shown.tail_eta_seconds > 0 ? shown.tail_eta_seconds : null}
          elapsedSeconds={elapsedSeconds}
          doneShards={doneShards}
          totalShards={totalShards}
          coverageHitRatio={shown.coverage_hit_ratio}
          stageText={stageText}
        />
      )}

      <JobControlBar
        canPause={canPause}
        canResume={canResume}
        canCancel={canCancel}
        canRetry={canRetry}
        busy={busy}
        onCommand={(command) => void runCommand(command)}
        onOpenTechnical={() => {
          setTechnicalTab("pipeline");
          setTechnicalOpen(true);
        }}
      />

      <JobTechnicalDetails
        open={technicalOpen}
        onClose={() => setTechnicalOpen(false)}
        detail={detail}
        job={job}
        shown={shown}
        status={status}
        events={events}
        stream={{ connected: stream.connected, lastEventID: stream.lastEventID }}
        lastTrace={lastTrace}
        busy={busy}
        expandAll={expandAll}
        onToggleExpandAll={() => setExpandAll((value) => !value)}
        onRefresh={() => void reload()}
        onClearEvents={() => setEvents([])}
        onDownloadExport={(item) => void downloadExport(item)}
        onOpenExports={() => navigate("/exports")}
        initialTab={technicalTab}
        topActions={
          <span className="toolbar" style={{ flexWrap: "wrap", gap: "var(--sp-2)" }}>
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
            <Button size="sm" onClick={() => dialog.notify("info", "rows/s 口径", rowsBasis)}>
              rows/s 口径
            </Button>
            <Button size="sm" onClick={() => dialog.notify("info", "Provider rows/s", providerBasis)}>
              Provider rows/s 说明
            </Button>
            <span className="cs-section__count">
              总进度 {percent(shown.progress)} · Shard {doneShards}/{totalShards} · 已用时 {formatDuration(shown.elapsed_seconds)}
            </span>
          </span>
        }
      />
    </>
  );
}
