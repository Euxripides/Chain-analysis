import { Button } from "@chain/ui";
import { CsStatusTag } from "@chain/user-ui";
import { formatTime } from "../../../sprint5/common";
import type { JobExport } from "./types";

/**
 * 完成态主卡（用户端重构规范 V4 §28.9）
 *
 * 完成后主卡展示总耗时、下载量、完成时间与导出文件；可在此创建导出。
 *
 * 多个导出时**不把完整 Export 表格铺在首屏**（§28.9 最后一段）：
 * 主区只显示"可下载文件 N 个"，点开后在技术详情的 Exports 面板里逐条选择。
 * 导出字段、SHA-256、有效期等一个都没有丢。
 */

export interface JobCompletionPanelProps {
  elapsedSeconds: number | null;
  downloadedBytes: number;
  resolvedAt: string | null;
  exports: JobExport[];
  busy: boolean;
  canCreateExport: boolean;
  onCreateExport: () => void;
  onDownload: (item: JobExport) => void;
  onShowExportError: (item: JobExport) => void;
  onOpenExports: () => void;
}

export function JobCompletionPanel({
  elapsedSeconds,
  downloadedBytes,
  resolvedAt,
  exports,
  busy,
  canCreateExport,
  onCreateExport,
  onDownload,
  onShowExportError,
  onOpenExports,
}: JobCompletionPanelProps) {
  /* 可下载 = 已完成且未过期。判据与原来的表格按钮完全一致，没有放宽。 */
  const available = exports.filter(
    (item) => item.status === "COMPLETED" && (!item.expires_at || Date.parse(item.expires_at) > Date.now()),
  );
  const first = available[0];
  const pending = exports.filter((item) => item.status === "PENDING" || item.status === "RUNNING");
  const failed = exports.filter((item) => item.status === "FAILED");

  return (
    <section className="cs-surface cs-jobprogress" data-state="completed" data-testid="job-completion-panel">
      <div style={{ display: "flex", alignItems: "center", gap: "var(--sp-3)", flexWrap: "wrap" }}>
        <span className="cs-jobprogress__pct">100%</span>
        <CsStatusTag tone="ok">下载完成</CsStatusTag>
      </div>
      <div className="cs-jobprogress__facts">
        <div className="cs-jobprogress__fact">
          <span className="cs-jobprogress__factlabel">总耗时</span>
          <span className="cs-jobprogress__factvalue">{formatDurationShort(elapsedSeconds)}</span>
        </div>
        <div className="cs-jobprogress__fact">
          <span className="cs-jobprogress__factlabel">文件大小</span>
          <span className="cs-jobprogress__factvalue">{formatBytesShort(downloadedBytes)}</span>
        </div>
        <div className="cs-jobprogress__fact">
          <span className="cs-jobprogress__factlabel">完成时间</span>
          <span className="cs-jobprogress__factvalue">{resolvedAt ? formatTime(resolvedAt) : "—"}</span>
        </div>
        <div className="cs-jobprogress__fact">
          <span className="cs-jobprogress__factlabel">可下载文件</span>
          <span className="cs-jobprogress__factvalue">{available.length} 个</span>
        </div>
        {pending.length > 0 ? (
          <div className="cs-jobprogress__fact" role="status">
            <span className="cs-jobprogress__factlabel">导出状态</span>
            <span className="cs-jobprogress__factvalue">正在生成 {pending.length} 个文件</span>
          </div>
        ) : null}
        {failed.length > 0 ? (
          <div className="cs-jobprogress__fact">
            <span className="cs-jobprogress__factlabel">导出失败</span>
            <span className="cs-jobprogress__factvalue">{failed.length} 个</span>
          </div>
        ) : null}
      </div>
      <div className="toolbar" style={{ flexWrap: "wrap", gap: "var(--sp-2)" }}>
        {canCreateExport ? (
          <Button size="sm" variant="primary" disabled={busy || pending.length > 0} onClick={onCreateExport}>
            {pending.length > 0 ? "文件生成中" : "导出文件"}
          </Button>
        ) : null}
        {first ? (
          <Button size="sm" disabled={busy} onClick={() => onDownload(first)}>
            下载文件（{first.format.toUpperCase()}{first.zip ? " ZIP" : ""}）
          </Button>
        ) : null}
        {failed[0] ? <Button size="sm" onClick={() => onShowExportError(failed[0])}>查看失败原因</Button> : null}
        {exports.length > 1 ? <Button size="sm" onClick={onOpenExports}>查看其他文件</Button> : null}
      </div>
    </section>
  );
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

function formatBytesShort(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value) || value < 0) return "—";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let n = value;
  let unit = 0;
  while (n >= 1024 && unit < units.length - 1) {
    n /= 1024;
    unit += 1;
  }
  return `${n >= 100 || unit === 0 ? Math.round(n) : n.toFixed(1)} ${units[unit]}`;
}
