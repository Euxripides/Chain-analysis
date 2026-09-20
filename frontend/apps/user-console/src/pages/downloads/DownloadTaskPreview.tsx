import React, { useEffect } from "react";
import { animate, motion, useMotionValue, useReducedMotion, useTransform } from "motion/react";
import { ArrowRight, CloudDownload, Database, LoaderCircle, Server } from "lucide-react";
import { formatRawAmount, formatTime } from "../../sprint5/common";
import { ChainIcon } from "./components/ChainIcon";
import { DL_EASE_ENTER, downloadMotion } from "./download.motion";
import { percent } from "./download.types";
import type { DownloadPreviewData } from "./download.types";

/**
 * ⑤ 创建任务 / 右侧任务预览（《新建数据下载 Flowline 界面重构实施规范》§15；
 * 布局与信息密度按《全量整改实施规范》§6.10 收紧）
 *
 * 结构（桌面 ≥1200px 是五行 Grid，CTA 独占 footer，永不进入滚动区）：
 *   1 标题 + 副状态
 *   2 Coverage 圆环 + 图例
 *   3 数据获取路径：本地数据 → Provider → 下载任务
 *   4 可滚动正文：配置行 + 四项指标 + 最多 4 条摘要 + "查看完整预览明细"
 *   5 创建任务（100% 宽 48px 主按钮）
 *
 * 未预览时**不渲染一串值为"—"的详细字段**（§6.10）：只保留配置行与四个指标槽位。
 * 完整值不会丢：其余业务字段全部进"查看完整预览明细"Dialog（由页面注入）。
 *
 * Coverage 圆环（§15.3 / §8.9）：使用真实 overall_ratio，只在拿到新预览时从上一真实值
 * 动画到新值（500–700ms），中心文字直接显示最终值；reduced motion 下立即到最终状态。
 */

export interface DownloadTaskPreviewProps {
  preview: DownloadPreviewData | null;
  lastPreview: DownloadPreviewData | null;
  stale: boolean;
  trace: string | null;
  busy: boolean;
  canAttemptCreate: boolean;
  onCreate: () => void;
  /** 打开"查看完整预览明细"Dialog（其余业务字段都在那里，值不丢失）。 */
  onOpenDetails: () => void;
  /** 短屏桌面的右栏次级预览入口（标准桌面 CSS 隐藏）。 */
  onPreview: () => void;
  /** 创建成功的一次性反馈令牌（§8.10）：只在真实成功后自增，不伪造进度。 */
  createdToken?: number;
  /* 页面注入的当前配置（业务字段，属于界面本身；不得显示复用策略内部字段）。 */
  addressCount: number;
  chainName: string | null;
  chainSlug: string | null;
  datasetLabels: string[];
  rangeLabel: string;
  performanceMode: string;
}

/*
 * 圆环几何用 100×100 的 viewBox 表达，实际直径由 CSS 决定
 * （桌面 136px / 短屏桌面 104px / 移动 96px）。
 * 这样"短屏压缩圆环"是纯 CSS 行为，不需要 JS 测量高度（整改规范 §18.1）。
 */
const RING_VIEW = 100;
const RING_TRACK = 7.4;
const RING_RADIUS = (RING_VIEW - RING_TRACK) / 2;
const RING_CIRCUMFERENCE = 2 * Math.PI * RING_RADIUS;

function pathStage(preview: DownloadPreviewData | null): { local: "done" | "partial"; provider: "unused" | "needed"; task: string } {
  if (!preview) return { local: "partial", provider: "unused", task: "待创建" };
  if (!preview.provider_work_required) {
    return {
      local: "done",
      provider: "unused",
      task: preview.reuse.coverage_hit ? "本地已覆盖" : preview.reuse.will_reuse ? "复用现有任务" : "待创建",
    };
  }
  return { local: "partial", provider: "needed", task: "待创建" };
}

function Ring({ ratio, reducedMotion }: { ratio: number | null; reducedMotion: boolean }) {
  const target = ratio === null ? 0 : Math.max(0, Math.min(1, ratio));
  const progress = useMotionValue(target);
  useEffect(() => {
    if (reducedMotion) {
      progress.set(target);
      return;
    }
    /* 只在真实值变化时动画：从上一真实值到新真实值（500–700ms，§8.9）。 */
    const controls = animate(progress, target, { duration: downloadMotion.ring, ease: DL_EASE_ENTER });
    return () => controls.stop();
  }, [progress, reducedMotion, target]);
  const dashoffset = useTransform(progress, (value) => RING_CIRCUMFERENCE * (1 - value));

  return (
    <div className="dl-ring">
      <svg viewBox={"0 0 " + RING_VIEW + " " + RING_VIEW} aria-hidden="true" focusable="false">
        <defs>
          <linearGradient id="dl-ring-gradient" x1="0" y1="0" x2="1" y2="1">
            <stop offset="0%" stopColor="var(--dl-primary)" />
            <stop offset="100%" stopColor="var(--dl-cyan)" />
          </linearGradient>
        </defs>
        <circle
          cx={RING_VIEW / 2}
          cy={RING_VIEW / 2}
          r={RING_RADIUS}
          fill="none"
          stroke="var(--dl-track)"
          strokeWidth={RING_TRACK}
        />
        <motion.circle
          cx={RING_VIEW / 2}
          cy={RING_VIEW / 2}
          r={RING_RADIUS}
          fill="none"
          stroke="url(#dl-ring-gradient)"
          strokeWidth={RING_TRACK}
          strokeLinecap="round"
          strokeDasharray={RING_CIRCUMFERENCE}
          style={{ strokeDashoffset: dashoffset }}
          transform={"rotate(-90 " + RING_VIEW / 2 + " " + RING_VIEW / 2 + ")"}
        />
      </svg>
      <span className="dl-ring__value">{percent(ratio)}</span>
      <span className="dl-ring__caption">整体覆盖率</span>
    </div>
  );
}

export function DownloadTaskPreview({
  preview,
  lastPreview,
  stale,
  trace,
  busy,
  canAttemptCreate,
  onCreate,
  onOpenDetails,
  onPreview,
  createdToken = 0,
  addressCount,
  chainName,
  chainSlug,
  datasetLabels,
  rangeLabel,
  performanceMode,
}: DownloadTaskPreviewProps) {
  const reducedMotion = Boolean(useReducedMotion());
  const shown = preview ?? lastPreview;
  const stage = pathStage(shown);
  const status = preview ? "本次预览" : lastPreview ? "上次预览已失效" : "尚未预览";
  const ratio = shown ? shown.coverage.overall_ratio : null;

  return (
    <section className="dl-task-preview" aria-label="任务预览">
      <header className="dl-task-preview__head">
        <h2 className="dl-task-preview__title">任务预览</h2>
        <span className="dl-task-preview__state" data-tone={preview ? "ok" : lastPreview ? "warn" : undefined}>
          {status}
        </span>
      </header>

      <div className="dl-task-preview__ring">
        <Ring ratio={ratio} reducedMotion={reducedMotion} />
        <ul className="dl-legend">
          <li>
            <span className="dl-legend__dot dl-legend__dot--covered" aria-hidden="true" />
            已覆盖区块 <b>{shown ? percent(shown.coverage.overall_ratio) : "—"}</b>
          </li>
          <li>
            <span className="dl-legend__dot dl-legend__dot--open" aria-hidden="true" />
            未覆盖区块 <b>{shown ? percent(1 - shown.coverage.overall_ratio) : "—"}</b>
          </li>
        </ul>
      </div>

      <div className="dl-path" aria-label="数据获取路径">
        <div className="dl-path__node" data-state={stage.local === "done" ? "done" : "partial"}>
          <span className="dl-path__icon" aria-hidden="true">
            <Database size={16} />
          </span>
          <span className="dl-path__label">本地数据</span>
          <span className="dl-path__hint">{stage.local === "done" ? "已命中" : "部分覆盖"}</span>
        </div>
        <ArrowRight className="dl-path__arrow" size={16} aria-hidden="true" />
        <div className="dl-path__node" data-state={stage.provider === "needed" ? "partial" : "unused"}>
          <span className="dl-path__icon" aria-hidden="true">
            <Server size={16} />
          </span>
          <span className="dl-path__label">Provider</span>
          <span className="dl-path__hint">{stage.provider === "needed" ? "需要新增" : "未使用"}</span>
        </div>
        <ArrowRight className="dl-path__arrow" size={16} aria-hidden="true" />
        <motion.div
          className="dl-path__node"
          data-state="todo"
          key={createdToken}
          initial={false}
          animate={createdToken > 0 && !reducedMotion ? { scale: [1, 1.06, 1] } : { scale: 1 }}
          transition={{ duration: downloadMotion.spatial, ease: DL_EASE_ENTER }}
        >
          <span className="dl-path__icon" aria-hidden="true">
            <CloudDownload size={16} />
          </span>
          <span className="dl-path__label">下载任务</span>
          <span className="dl-path__hint">{stage.task}</span>
        </motion.div>
      </div>

      <div className={stale ? "dl-task-preview__body dl-preview-stale" : "dl-task-preview__body"} data-stale={stale ? "true" : undefined}>
        <div className="dl-preview-metrics" data-testid="preview-metrics">
          <div className="dl-preview-metric">
            <span className="dl-preview-metric__label">未覆盖区块</span>
            <span className="dl-preview-metric__value">{shown ? formatRawAmount(String(shown.missing_blocks), 0) : "—"}</span>
          </div>
          <div className="dl-preview-metric">
            <span className="dl-preview-metric__label">预估行数</span>
            <span className="dl-preview-metric__value">
              {shown?.estimate.estimated_rows === null || shown?.estimate.estimated_rows === undefined
                ? "—"
                : formatRawAmount(String(shown.estimate.estimated_rows), 0)}
            </span>
          </div>
          <div className="dl-preview-metric">
            <span className="dl-preview-metric__label">预估成本 USD</span>
            <span className="dl-preview-metric__value">{shown?.estimate.estimated_cost_usd ?? "—"}</span>
          </div>
          <div className="dl-preview-metric">
            <span className="dl-preview-metric__label">预估耗时（秒）</span>
            <span className="dl-preview-metric__value">
              {shown?.estimate.estimated_time_seconds === null || shown?.estimate.estimated_time_seconds === undefined
                ? "—"
                : String(shown.estimate.estimated_time_seconds)}
            </span>
          </div>
        </div>

        {/* 配置行：真实业务数据（不是"—"占位），一行放得下，窄屏自动折行。 */}
        <div className="dl-configline">
          <span className="dl-configline__item">
            <span className="dl-configline__label">链</span>
            {chainSlug ? (
              <span className="dl-summary__chain">
                <ChainIcon chain={chainSlug} size={14} />
                {chainName ?? chainSlug}
              </span>
            ) : (
              "—"
            )}
          </span>
          <span className="dl-configline__item">
            <span className="dl-configline__label">地址</span>
            <b data-testid="inspector-addresses">{addressCount > 0 ? addressCount + " 个" : "0 个"}</b>
          </span>
          <span className="dl-configline__item">
            <span className="dl-configline__label">Dataset</span>
            {datasetLabels.length > 0 ? datasetLabels.length + " 项" : "—"}
          </span>
          <span className="dl-configline__item">
            <span className="dl-configline__label">范围</span>
            {rangeLabel}
          </span>
          <span className="dl-configline__item">
            <span className="dl-configline__label">性能模式</span>
            {performanceMode}
          </span>
        </div>

        {shown ? (
          <>
            <dl className="dl-summary">
              <div className="dl-summary__row">
                <dt>Coverage 命中</dt>
                <dd>{shown.reuse.coverage_hit ? "是" : "否"}</dd>
              </div>
              <div className="dl-summary__row">
                <dt>将复用现有任务</dt>
                <dd>{shown.reuse.will_reuse ? "是" : "否"}</dd>
              </div>
              <div className="dl-summary__row">
                <dt>候选任务</dt>
                <dd className="mono">{shown.reuse.candidate ? shown.reuse.candidate.job_no + " · " + percent(shown.reuse.candidate.coverage) : "无"}</dd>
              </div>
              <div className="dl-summary__row">
                <dt>Provider 工作</dt>
                <dd>{shown.provider_work_required ? "需要新增" : "无需新增"}</dd>
              </div>
            </dl>
            {/*
              其余业务字段（Coverage/Provider 来源、数据时点、最近 Trace、逐项 Coverage 明细、
              估算依据）全部进"查看完整预览明细"Dialog —— 主卡不再堆字段，值也不会丢（§6.10）。
            */}
            <button type="button" className="dl-btn dl-btn--ghost dl-btn--sm dl-task-preview__details" onClick={onOpenDetails}>
              查看完整预览明细
              {trace ? <span className="mono dl-task-preview__trace">{trace.slice(0, 8)}</span> : null}
            </button>
          </>
        ) : null}
      </div>

      <div className="dl-task-preview__actions">
        {/*
          短屏桌面（1200px+ 且 h<900）左栏会局部滚动，预览 CTA 可能落到折叠之外：
          此时在右栏 footer 补一个**次级**预览入口，保证 §7.4 的 CTA 可达性；
          标准桌面保持只有创建一个主 CTA（§16.1）。
        */}
        <button type="button" className="dl-btn dl-btn--secondary dl-btn--block dl-task-preview__preview" onClick={onPreview}>
          预览 Coverage 与成本
        </button>
        <button
          type="button"
          className="dl-btn dl-btn--primary dl-btn--block"
          onClick={onCreate}
          disabled={!canAttemptCreate || busy}
          aria-busy={busy || undefined}
        >
          {busy ? <LoaderCircle className="dl-spin" size={17} aria-hidden="true" /> : null}
          <span>{busy ? "正在创建…" : "创建下载任务"}</span>
        </button>
      </div>
    </section>
  );
}

export default DownloadTaskPreview;
