import { CsJobProgress, formatEta, formatEtaClock } from "@chain/user-ui";
import { StatusBadgeForState } from "@chain/ui";
import { formatBytes, formatRawAmount } from "../../../sprint5/common";

/**
 * 主进度区（用户端重构规范 V4 §28.1 / §28.2）
 *
 * 这是下载任务详情页的**首屏主角**，只回答普通用户的四个问题：
 *   1. 下载到多少了？（服务端真实 progress）
 *   2. 现在有多快？（服务端 rate，或已确认口径的 downloaded_rows ÷ elapsed_seconds）
 *   3. 还要多久 / 预计几点完成？（服务端 eta_seconds 的显示换算）
 *   4. 现在是什么阶段？
 *
 * 明确**不做**的事（§28.2「默认隐藏到技术详情」/ §64 #22）：
 *   不显示 last_event_id、provider 原始事件、每个 shard 的 block range、
 *   requested/effective mode 技术说明、200 条结构化事件、多层嵌套进度树。
 *   这些能力一个都没删，全部在 JobTechnicalDetails 里。
 */

export interface JobPrimaryProgressProps {
  status: string;
  /** 服务端真实百分比（0–100，已四舍五入）。 */
  percentValue: number;
  /** 服务端原始 progress（0–1），用于展示更精确的百分比文案。 */
  rawProgress: number;
  downloadedRows: number;
  downloadedBytes: number;
  /** 服务端明确给出的速率；没有就是 null，**不由前端另算**。 */
  rowsPerSecond: number | null;
  etaSeconds: number | null;
  elapsedSeconds: number | null;
  doneShards: number;
  totalShards: number;
  coverageHitRatio?: number;
  /** 当前阶段的一句话（只由服务端状态与数据集名派生，不产生新事实）。 */
  stageText: string;
}

export function JobPrimaryProgress({
  status,
  percentValue,
  rawProgress,
  downloadedRows,
  downloadedBytes,
  rowsPerSecond,
  etaSeconds,
  elapsedSeconds,
  doneShards,
  totalShards,
  coverageHitRatio,
  stageText,
}: JobPrimaryProgressProps) {
  /*
   * ETA 规则（§28.4）：
   *   - 只有服务端给了 eta_seconds 才展示，null 一律显示 —；
   *   - 预计完成时刻只是「当前时刻 + 服务端 ETA」的显示换算，不建立新的预测模型；
   *   - 暂停时不继续倒计时。
   */
  const etaText = formatEta(etaSeconds, status);
  const etaClock = formatEtaClock(etaSeconds, status);

  const speedText =
    rowsPerSecond === null || !Number.isFinite(rowsPerSecond) ? "—" : `${rowsPerSecond.toFixed(0)} rows/s`;

  return (
    <CsJobProgress
      percent={percentValue}
      percentText={percent(rawProgress)}
      state={status}
      facts={[
        { label: "当前速度", value: speedText },
        { label: "已下载行数", value: formatRawAmount(String(downloadedRows ?? 0), 0) },
        { label: "已下载大小", value: formatBytes(downloadedBytes ?? 0) },
        { label: "预计剩余", value: etaText },
        { label: "预计完成", value: etaClock },
        { label: "分片进度", value: `${doneShards} / ${totalShards}` },
        { label: "已用时", value: formatDurationShort(elapsedSeconds) },
        ...(coverageHitRatio === undefined
          ? []
          : [{ label: "Coverage 命中率", value: percent(coverageHitRatio) }]),
      ]}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: "var(--sp-3)",
          flexWrap: "wrap",
          minWidth: 0,
        }}
      >
        <StatusBadgeForState value={status} />
        <span className="cs-jobprogress__stage">{stageText}</span>
      </div>
    </CsJobProgress>
  );
}

function percent(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) return "—";
  return `${(value * 100).toFixed(value >= 1 ? 0 : 1)}%`;
}

function formatDurationShort(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined || !Number.isFinite(seconds) || seconds < 0) return "—";
  const total = Math.round(seconds);
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const rest = total % 60;
  if (hours > 0) return hours + " 小时 " + minutes + " 分";
  if (minutes > 0) return minutes + " 分 " + rest + " 秒";
  return rest + " 秒";
}

/**
 * 当前阶段的一句话（§28.2「当前阶段：正在下载 Token Transfer」）。
 *
 * **只由服务端已有字段拼装**：status 枚举 + 任务声明的 datasets + 分片计数。
 * 不推断 provider、不推断失败原因、不预测下一步会做什么。
 */
export function jobStageText(input: {
  status: string;
  datasets: string[];
  doneShards: number;
  totalShards: number;
}): string {
  const { status, datasets, doneShards, totalShards } = input;
  const datasetText = datasets.length > 0 ? datasets.join("、") : "";
  switch (status) {
    case "QUEUED":
      return "等待调度";
    case "PAUSE_REQUESTED":
      return "已请求暂停，等待当前分页结束";
    case "PAUSED":
    case "PAUSED_FALLBACK_TOO_EXPENSIVE":
      return "已暂停，进度停在当前位置";
    case "CANCEL_REQUESTED":
      return "已请求取消，等待服务端确认";
    case "CANCELLED":
      return "已取消";
    case "ABORTED_BY_USER":
      return "已被用户中止";
    case "COMPLETED":
      return "下载完成";
    case "PARTIAL_FAILED":
      return "部分分片失败，已完成的数据保留";
    case "FAILED":
      return "下载失败";
    case "RUNNING":
      if (totalShards > 0 && doneShards === 0) return datasetText ? `正在准备 ${datasetText} 的分片` : "正在准备分片";
      return datasetText ? `正在下载 ${datasetText}` : "正在下载";
    default:
      return status || "—";
  }
}
