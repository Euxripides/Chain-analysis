import React, { useEffect, useState } from "react";
import { motion } from "motion/react";
import { ChartNoAxesColumn, LoaderCircle, TriangleAlert } from "lucide-react";
import { formatRawAmount } from "../../sprint5/common";
import { dlRevealItem, dlRevealStagger, dlStaleHighlight } from "./download.motion";
import { percent } from "./download.types";
import type { DownloadPreviewData, PreviewCoverageItem } from "./download.types";

/**
 * ④ Coverage 与成本预览（规范 §14）
 *
 * 三个状态各自有明确的视觉，不允许出现大面积空白 EmptyState：
 *   未预览 —— 紧凑启动面板：图标 + 一句业务标签 + 右侧"预览 Coverage 与成本"按钮；
 *   运行中 —— 按钮 busy；没有真实百分比就**不显示**伪造进度（§14.2）；
 *   成功   —— 只呈现服务端真实返回的覆盖率 / 缺口 / 成本 / 耗时 / Provider 工作
 *             与每个对象、每个 Dataset 的明细。
 *
 * 失效预览（§14.4）：容器 opacity 必须为 1，用 1px 琥珀边框 + 一次性 420ms 边框高光表达，
 * 并且"创建任务"步骤退回未到达状态（由页面保证）。
 *
 * 估算依据、风险说明属于提示性内容：只在"成本与风险说明"Dialog 中出现（§14.3）。
 */

export interface DownloadCoveragePanelProps {
  preview: DownloadPreviewData | null;
  lastPreview: DownloadPreviewData | null;
  stale: boolean;
  busy: boolean;
  coverageItems: PreviewCoverageItem[];
  onPreview: () => void;
  onOpenDetails: () => void;
}

function Metric({ label, value, strong }: { label: string; value: React.ReactNode; strong?: boolean }) {
  return (
    <div className="dl-metric" data-strong={strong ? "true" : undefined}>
      <span className="dl-metric__label">{label}</span>
      <span className="dl-metric__value">{value}</span>
    </div>
  );
}

/** 请求超过 300ms 再显示加载反馈，避免快速响应时闪一下（§14.2）。 */
function useDelayedBusy(busy: boolean, delay = 300): boolean {
  const [shown, setShown] = useState(false);
  useEffect(() => {
    if (!busy) {
      setShown(false);
      return;
    }
    const timer = setTimeout(() => setShown(true), delay);
    return () => clearTimeout(timer);
  }, [busy, delay]);
  return shown;
}

function numberText(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) return "—";
  return formatRawAmount(String(value), 0);
}

export function DownloadCoveragePanel({
  preview,
  lastPreview,
  stale,
  busy,
  coverageItems,
  onPreview,
  onOpenDetails,
}: DownloadCoveragePanelProps) {
  const busyFeedback = useDelayedBusy(busy);
  const shown = preview ?? lastPreview;

  if (!shown) {
    return (
      <div className="dl-coverage dl-coverage--idle">
        <span className="dl-coverage__idleicon" aria-hidden="true">
          <ChartNoAxesColumn size={20} />
        </span>
        <p className="dl-coverage__idlelabel">配置完成后可预览 Coverage、缺口和成本</p>
        <button type="button" className="dl-btn dl-btn--primary" onClick={onPreview} disabled={busy} aria-busy={busy || undefined}>
          {busyFeedback ? <LoaderCircle className="dl-spin" size={16} aria-hidden="true" /> : null}
          <span>{busyFeedback ? "正在预览…" : "预览 Coverage 与成本"}</span>
        </button>
      </div>
    );
  }

  return (
    <motion.div
      className={stale ? "dl-coverage dl-preview-stale" : "dl-coverage"}
      data-stale={stale ? "true" : undefined}
      initial={false}
      animate={stale ? "flash" : "rest"}
      variants={dlStaleHighlight}
    >
      <div className="dl-coverage__head">
        <span className="dl-coverage__state" data-tone={stale ? "warn" : "ok"}>
          {stale ? <TriangleAlert size={13} aria-hidden="true" /> : null}
          {stale ? "上次预览 · 已失效" : "本次预览"}
        </span>
        <div className="dl-coverage__headactions">
          <button type="button" className="dl-btn dl-btn--ghost dl-btn--sm" onClick={onOpenDetails}>
            成本与风险说明
          </button>
          <button
            type="button"
            className="dl-btn dl-btn--primary-soft dl-btn--sm"
            onClick={onPreview}
            disabled={busy}
            aria-busy={busy || undefined}
          >
            {busyFeedback ? <LoaderCircle className="dl-spin" size={15} aria-hidden="true" /> : null}
            <span>{busyFeedback ? "正在预览…" : stale ? "重新预览" : "重新计算"}</span>
          </button>
        </div>
      </div>

      <motion.div className="dl-coverage__grid" variants={dlRevealStagger} initial="hidden" animate="show">
        <motion.div variants={dlRevealItem}>
          <Metric label="整体覆盖率" value={percent(shown.coverage.overall_ratio)} strong />
        </motion.div>
        <motion.div variants={dlRevealItem}>
          <Metric label="未覆盖区块" value={numberText(shown.missing_blocks)} />
        </motion.div>
        <motion.div variants={dlRevealItem}>
          <Metric label="预估缺失区块" value={numberText(shown.estimate.estimated_missing_blocks)} />
        </motion.div>
        <motion.div variants={dlRevealItem}>
          <Metric label="预估行数" value={numberText(shown.estimate.estimated_rows)} />
        </motion.div>
        <motion.div variants={dlRevealItem}>
          <Metric label="预估成本（USD）" value={shown.estimate.estimated_cost_usd ?? "—"} />
        </motion.div>
        <motion.div variants={dlRevealItem}>
          <Metric label="预估耗时（秒）" value={shown.estimate.estimated_time_seconds === null ? "—" : String(shown.estimate.estimated_time_seconds)} />
        </motion.div>
        <motion.div variants={dlRevealItem}>
          <Metric label="Coverage 命中" value={shown.reuse.coverage_hit ? "是" : "否"} />
        </motion.div>
        <motion.div variants={dlRevealItem}>
          <Metric label="Provider 工作" value={shown.provider_work_required ? "需要新增" : "无需新增"} />
        </motion.div>
      </motion.div>

      {coverageItems.length > 0 ? (
        <div className="dl-coverage__table">
          <table>
            <caption className="visually-hidden">每个对象在每个 Dataset 上的 Coverage 明细</caption>
            <thead>
              <tr>
                <th scope="col">对象</th>
                <th scope="col">Dataset</th>
                <th scope="col">覆盖率</th>
                <th scope="col">已覆盖 / 总区块</th>
                <th scope="col">缺口</th>
              </tr>
            </thead>
            <tbody>
              {coverageItems.map((item, index) => (
                <tr key={item.subject + "|" + item.dataset + "|" + index}>
                  <td className="mono" title={item.subject}>
                    {item.subject.length > 22 ? item.subject.slice(0, 12) + "…" + item.subject.slice(-8) : item.subject}
                  </td>
                  <td>{item.dataset}</td>
                  <td className="dl-num">{percent(item.ratio)}</td>
                  <td className="dl-num">
                    {formatRawAmount(String(item.covered_blocks), 0)} / {formatRawAmount(String(item.total_blocks), 0)}
                  </td>
                  <td className="dl-num">{item.gaps.length}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}

    </motion.div>
  );
}

export default DownloadCoveragePanel;