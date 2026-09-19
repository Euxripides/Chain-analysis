import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import {
  BackLink,
  Button,
  Card,
  EmptyState,
  ErrorState,
  PageSkeleton,
  useDialog,
  useThrottledValue,
} from "@chain/ui";
import { CsError, CsPageHeader } from "@chain/user-ui";
import { ApiError, canUseDirectDownload, directTicketUrl } from "@chain/api-client";
import { formatRawAmount, shortValue, useApi, useJobStream, useS5Action } from "../sprint5/common";
import type { JobEvent } from "../sprint5/common";
import {
  TERMINAL_STATUSES,
  formatDuration,
  percent,
  type JobDetailData,
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
 *   当前阶段、暂停/继续/取消/重试，以及完成后的下载入口。
 *
 *   二级入口（支持人员）：Pipeline / 地址进度 / Dataset / Shards / 结构化事件 /
 *   Exports / 证据链 —— 一个都没删，全部收进默认关闭的 JobTechnicalDetails。
 *
 * **业务逻辑一行未改**：请求、SSE 订阅与作用域隔离、节流显示、命令与审计原因、
 * 导出票据与直连通道回退、错误 safe message 全部保留原实现。
 * ==========================================================================*/

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
  /* 技术详情默认关闭（§28.3）。不新增 URL key：当前页面没有 tab 深链接契约。 */
  const [technicalOpen, setTechnicalOpen] = useState(false);
  const [technicalTab, setTechnicalTab] = useState("pipeline");
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
    setTechnicalOpen(false);
    lastEventIdRef.current = 0;
    lastRevisionRef.current = 0;
  }, [jobId]);

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
   *   1. 服务端明确提供的 rate —— 当前契约未返回，故为 null；
   *   2. 当前代码已有并确认口径的 downloaded_rows ÷ elapsed_seconds 作为展示派生；
   *   3. provider rate 只放技术详情。
   * 速度为 0 不自动解释为故障。
   */
  const elapsedSeconds = progress.elapsed_seconds > 0 ? progress.elapsed_seconds : null;
  const rowsPerSecond = elapsedSeconds !== null ? progress.downloaded_rows / elapsedSeconds : null;

  const isFailed = ["FAILED", "PARTIAL_FAILED", "ABORTED_BY_USER"].includes(status);
  const isCompleted = status === "COMPLETED";

  const stageText = jobStageText({
    status,
    datasets: job.datasets ?? [],
    doneShards,
    totalShards,
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
          resolvedAt={job.resolved_at}
          exports={detail.exports}
          busy={busy}
          onDownload={(item) => void downloadExport(item)}
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
          etaSeconds={shown.eta_seconds ?? null}
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
        downloadAction={
          isCompleted && detail.exports.length > 0 ? (
            <Button
              size="sm"
              variant="primary"
              disabled={busy}
              onClick={() => {
                setTechnicalTab("exports");
                setTechnicalOpen(true);
              }}
            >
              下载结果（{detail.exports.length}）
            </Button>
          ) : undefined
        }
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
