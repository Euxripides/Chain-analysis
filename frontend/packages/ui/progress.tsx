import React, { useEffect, useId, useMemo, useRef, useState } from "react";
import {
  Check,
  ChevronDown,
  ChevronRight,
  CircleSlash,
  Clock,
  Loader,
  Pause,
  SquareX,
  TriangleAlert,
} from "lucide-react";

/**
 * 进度组件系统（用户端重构蓝图 §9）
 *
 * 覆盖下载、任务、地址、Dataset、Shard、文件解析、调查步骤、导出和报告生成。
 *
 * 三条不可突破的规则：
 *  1. 服务端没有总量时**不得伪造百分比**——用 indeterminate，不拿已完成数去除以猜测总量（§9.1）。
 *  2. 父层数值直接使用服务端聚合，不用前端对子层简单平均（§9.1 HierarchicalProgress）。
 *  3. 所有进度条提供 role="progressbar" + aria-valuemin/max/now 和可理解名称（§9.3）。
 */

export type ProgressState =
  | "queued"
  | "running"
  | "paused"
  | "waiting"
  | "completed"
  | "failed"
  | "cancelled";

export type ProgressTone = "queued" | "running" | "paused" | "waiting" | "completed" | "failed" | "cancelled" | "info";

/** 服务端状态字符串 → 统一进度状态。状态机语义由服务端决定，这里只做展示映射。 */
export function progressStateFor(value: string | null | undefined): ProgressState {
  const v = (value || "").toUpperCase();
  if (["COMPLETED", "SUCCESS", "SUCCEEDED", "DONE", "FINISHED", "VERIFIED", "APPROVED", "SIGNED"].includes(v)) return "completed";
  if (["FAILED", "FAILURE", "ERROR", "DEAD", "REJECTED"].includes(v)) return "failed";
  if (["CANCELLED", "CANCELED", "ABORTED", "REVOKED", "ARCHIVED"].includes(v)) return "cancelled";
  if (["PAUSED", "SUSPENDED", "ON_HOLD"].includes(v)) return "paused";
  if (["WAITING_DATA", "WAITING_REVIEW", "WAITING", "PENDING_REVIEW", "IN_REVIEW", "PENDING", "QUEUED_REVIEW"].includes(v)) return "waiting";
  if (["QUEUED", "CREATED", "SCHEDULED", "DRAFT", "NEW", "INVITED"].includes(v)) return "queued";
  if (["RUNNING", "ACTIVE", "IN_PROGRESS", "PROCESSING", "EXECUTING"].includes(v)) return "running";
  return "queued";
}

/**
 * 状态的中文名。文字是状态的第一通道——颜色和形状只是冗余编码（§9.2 / §14）。
 */
export const PROGRESS_STATE_LABEL: Record<ProgressState, string> = {
  queued: "排队中",
  running: "运行中",
  paused: "已暂停",
  waiting: "等待中",
  completed: "已完成",
  failed: "失败",
  cancelled: "已取消",
};

const GLYPH: Record<ProgressState, React.ComponentType<{ size?: number | string; strokeWidth?: number; "aria-hidden"?: boolean }>> = {
  queued: Clock,
  running: Loader,
  paused: Pause,
  waiting: TriangleAlert,
  completed: Check,
  failed: SquareX,
  cancelled: CircleSlash,
};

/** 状态字形：形状 + 颜色双通道。 */
export function ProgressGlyph({
  state,
  label,
  size = 14,
}: {
  state: ProgressState;
  label?: string;
  size?: number;
}) {
  const Glyph = GLYPH[state];
  return (
    <span className="cs-progressglyph" data-state={state} title={label ?? PROGRESS_STATE_LABEL[state]}>
      <Glyph size={size} strokeWidth={2.4} aria-hidden={true} />
      <span className="visually-hidden">{label ?? PROGRESS_STATE_LABEL[state]}</span>
    </span>
  );
}

/** 服务端值 → 0–100 的安全整数；越界值被夹取，NaN 视为"无进度"。 */
function clampPercent(value: number | null | undefined): number | null {
  if (value === null || value === undefined || Number.isNaN(value)) return null;
  return Math.max(0, Math.min(100, Math.round(value)));
}

function percentLabel(percent: number | null): string {
  return percent === null ? "—" : `${percent}%`;
}

export interface LinearProgressProps {
  /** 0–100。传 null/undefined 表示服务端没有总量 → indeterminate，**不伪造百分比**。 */
  percent?: number | null;
  state?: ProgressState;
  label?: string;
  /** 右侧补充数值，例如 "1,204 / 3,000 rows"。 */
  valueText?: React.ReactNode;
  size?: "md" | "lg";
  className?: string;
}

export function LinearProgress({
  percent,
  state = "running",
  label,
  valueText,
  size = "md",
  className,
}: LinearProgressProps) {
  const known = clampPercent(percent);
  const indeterminate = known === null && (state === "running" || state === "waiting");
  /* 已完成固定 100%：服务端给出 97 也必须按契约显示满格（蓝图 §9.2）。 */
  const shown = state === "completed" ? 100 : known;
  const name = label ?? "进度";
  return (
    <div className={`cs-progress${className ? ` ${className}` : ""}`}>
      {label || valueText ? (
        <div className="cs-progress__meta">
          {label ? <span className="cs-progress__label">{label}</span> : null}
          <span className="cs-progress__value">
            {valueText ?? (indeterminate ? PROGRESS_STATE_LABEL[state] : percentLabel(shown))}
          </span>
        </div>
      ) : null}
      <div
        className={`cs-progress__track${size === "lg" ? " cs-progress__track--lg" : ""}`}
        role="progressbar"
        aria-label={name}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={indeterminate ? undefined : (shown ?? 0)}
        aria-valuetext={indeterminate ? PROGRESS_STATE_LABEL[state] : percentLabel(shown)}
      >
        <div
          className={`cs-progress__fill${indeterminate ? " cs-progress__fill--indeterminate" : ""}`}
          data-tone={state}
          style={indeterminate ? undefined : { width: `${shown ?? 0}%` }}
        />
      </div>
    </div>
  );
}

/**
 * 表格行内的紧凑进度（§9.1 CompactProgress）。
 * 没有服务端百分比时只显示状态字形，不画假进度条。
 */
export function CompactProgress({
  percent,
  state = "running",
  label,
  waitingFor,
}: {
  percent?: number | null;
  state?: ProgressState;
  label?: string;
  /** WAITING_* 必须显示具体等待对象（§9.2）。 */
  waitingFor?: string;
}) {
  const known = clampPercent(percent);
  const indeterminate = known === null && (state === "running" || state === "waiting");
  const shown = state === "completed" ? 100 : known;
  const name = label ?? PROGRESS_STATE_LABEL[state];
  return (
    <span className="cs-compactprogress" title={waitingFor ? `${name} · 等待 ${waitingFor}` : name}>
      <ProgressGlyph state={state} label={name} size={13} />
      {indeterminate || shown !== null ? (
        <span
          className="cs-compactprogress__track"
          role="progressbar"
          aria-label={name}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={indeterminate ? undefined : (shown ?? 0)}
          aria-valuetext={indeterminate ? name : percentLabel(shown)}
        >
          <span
            className={`cs-compactprogress__fill${indeterminate ? " cs-compactprogress__fill--indeterminate" : ""}`}
            data-tone={state}
            style={indeterminate ? undefined : { width: `${shown ?? 0}%` }}
          />
        </span>
      ) : null}
      <span className="cs-compactprogress__pct">{indeterminate ? name : percentLabel(shown)}</span>
    </span>
  );
}

/** 空间受限的总览环（§9.1 ProgressRing）：不用于长数字。 */
export function ProgressRing({
  percent,
  state = "running",
  size = 64,
  thickness = 6,
  label,
}: {
  percent?: number | null;
  state?: ProgressState;
  size?: number;
  thickness?: number;
  label?: string;
}) {
  const known = clampPercent(percent) ?? 0;
  const shown = state === "completed" ? 100 : known;
  const radius = (size - thickness) / 2;
  const circumference = 2 * Math.PI * radius;
  const offset = circumference * (1 - shown / 100);
  const name = label ?? "进度";
  return (
    <span className="cs-ring">
      <svg
        className="cs-ring__svg"
        width={size}
        height={size}
        viewBox={`0 0 ${size} ${size}`}
        role="progressbar"
        aria-label={name}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={shown}
        aria-valuetext={percentLabel(shown)}
      >
        <circle className="cs-ring__track" cx={size / 2} cy={size / 2} r={radius} fill="none" strokeWidth={thickness} />
        <circle
          className="cs-ring__fill"
          data-tone={state}
          cx={size / 2}
          cy={size / 2}
          r={radius}
          fill="none"
          strokeWidth={thickness}
          strokeLinecap="round"
          strokeDasharray={circumference}
          strokeDashoffset={offset}
        />
      </svg>
      <span className="cs-ring__value">{percentLabel(clampPercent(percent))}</span>
    </span>
  );
}

/**
 * Pipeline 阶段（§9.1 PipelineProgress）。
 *
 * 阶段名由调用方给出——不使用固定枚举，因为不同任务链的阶段集合由服务端定义；
 * 这里只负责"完成 / 当前 / 等待 / 失败 / 暂停"的呈现与语义。
 */
export interface PipelineStage {
  key: string;
  label: string;
  state: ProgressState;
  /** 阶段补充信息，例如耗时、行数、等待对象。 */
  meta?: React.ReactNode;
  percent?: number | null;
}

export function PipelineProgress({
  stages,
  title,
  ariaLabel = "流水线阶段",
}: {
  stages: PipelineStage[];
  title?: React.ReactNode;
  ariaLabel?: string;
}) {
  const activeKey = stages.find((s) => s.state === "running")?.key;
  return (
    <div className="cs-pipeline-wrap">
      {title ? <div className="cs-section__title" style={{ marginBottom: "var(--sp-3)" }}>{title}</div> : null}
      <ol className="cs-pipeline" aria-label={ariaLabel}>
        {stages.map((stage) => (
          <li className="cs-pipeline__stage" data-state={stage.state} key={stage.key}>
            <span className="cs-pipeline__rail">
              <span className="cs-pipeline__glyph" data-state={stage.state}>
                {stage.state === "completed" ? <Check size={10} strokeWidth={3.4} aria-hidden="true" /> : null}
                {stage.state === "failed" ? <SquareX size={9} strokeWidth={3.2} aria-hidden="true" /> : null}
                {stage.state === "cancelled" ? <CircleSlash size={10} strokeWidth={2.6} aria-hidden="true" /> : null}
              </span>
            </span>
            <div className="cs-pipeline__body">
              <div className="cs-pipeline__label">
                <span>{stage.label}</span>
                <span className="visually-hidden">{PROGRESS_STATE_LABEL[stage.state]}</span>
                {stage.state === "running" ? (
                  <span className="badge badge--info" style={{ marginLeft: "auto" }}>{PROGRESS_STATE_LABEL.running}</span>
                ) : null}
              </div>
              {stage.meta ? <div className="cs-pipeline__meta">{stage.meta}</div> : null}
              {stage.percent !== undefined && stage.percent !== null && stage.state === "running" ? (
                <div style={{ marginTop: "var(--sp-2)" }}>
                  <LinearProgress percent={stage.percent} state={stage.state} label={undefined} />
                </div>
              ) : null}
            </div>
          </li>
        ))}
      </ol>
      {activeKey ? null : null}
    </div>
  );
}

/**
 * 分层进度：Job → Address → Dataset → Shard（§9.1）。
 *
 * 父层数值**直接来自服务端聚合**（`percent` 由调用方从响应里取），
 * 而不是把子层平均一遍——前端平均会在子层数量不均时给出错误结论。
 */
export interface HierarchicalNode {
  key: string;
  label: string;
  /** 服务端给出的聚合百分比；null 表示服务端未给总量。 */
  percent?: number | null;
  state?: ProgressState;
  meta?: React.ReactNode;
  children?: HierarchicalNode[];
  defaultExpanded?: boolean;
}

export function HierarchicalProgress({
  nodes,
  ariaLabel = "分层进度",
}: {
  nodes: HierarchicalNode[];
  ariaLabel?: string;
}) {
  return (
    <div className="cs-hier" role="tree" aria-label={ariaLabel}>
      {nodes.map((node) => (
        <HierNode key={node.key} node={node} depth={0} />
      ))}
    </div>
  );
}

function HierNode({ node, depth }: { node: HierarchicalNode; depth: number }) {
  /*
   * 默认展开规则：有失败子节点的分支默认展开，其余折叠——
   * 与蓝图 §12.16"可折叠但默认展开关键失败路径"同一原则（失败要先被看见）。
   */
  const hasFailure = useMemo(() => {
    const walk = (list: HierarchicalNode[] | undefined): boolean =>
      (list ?? []).some((child) => child.state === "failed" || walk(child.children));
    return walk(node.children);
  }, [node.children]);
  const [expanded, setExpanded] = useState(Boolean(node.defaultExpanded ?? hasFailure));
  const state = node.state ?? progressStateFor(null);
  const id = useId();
  const children = node.children ?? [];
  const canExpand = children.length > 0;

  return (
    <div className="cs-hier__node" role="treeitem" aria-expanded={canExpand ? expanded : undefined} aria-level={depth + 1}>
      <div className="cs-hier__row">
        <button
          type="button"
          className="cs-hier__toggle"
          aria-label={canExpand ? `${expanded ? "折叠" : "展开"} ${node.label}` : `${node.label}（无子项）`}
          aria-controls={canExpand ? id : undefined}
          disabled={!canExpand}
          style={canExpand ? undefined : { visibility: "hidden" }}
          onClick={() => setExpanded((v) => !v)}
        >
          {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
        </button>
        <span className="cs-hier__label" title={node.label}>
          {node.label}
        </span>
        <span className="cs-hier__bar">
          <CompactProgress percent={node.percent ?? null} state={state} label={node.label} />
        </span>
        {node.meta ? <span className="cs-pipeline__meta">{node.meta}</span> : null}
      </div>
      {canExpand && expanded ? (
        <div className="cs-hier__children" id={id} role="group">
          {children.map((child) => (
            <HierNode key={child.key} node={child} depth={depth + 1} />
          ))}
        </div>
      ) : null}
    </div>
  );
}

/**
 * 速率指标（§9.1 StreamingRate）。
 * rows/s、Provider rows/s、bytes/s、ETA 分开展示——不合并成一个"速度"。
 */
export interface RateMetric {
  key: string;
  label: string;
  value: React.ReactNode;
  hint?: React.ReactNode;
}

export function StreamingRate({ metrics, ariaLabel = "实时速率" }: { metrics: RateMetric[]; ariaLabel?: string }) {
  return (
    <div className="cs-rates" aria-label={ariaLabel}>
      {metrics.map((metric) => (
        <div className="cs-rate" key={metric.key}>
          <span className="cs-rate__label">{metric.label}</span>
          <span className="cs-rate__value">{metric.value}</span>
          {metric.hint ? <span className="cs-rate__hint">{metric.hint}</span> : null}
        </div>
      ))}
    </div>
  );
}

/**
 * 高频数值的展示节流（§9.3："UI 对高频数值做展示节流，推荐 200–500ms 合并一次渲染，
 * 但最终值必须一致"）。
 *
 * 实现要点：定时器只做"最后一次值的落地"，不在中间值上渲染；
 * 卸载/依赖变化时用 flush 保证**最终值一定被显示**，不会因为节流丢尾。
 */
export function useThrottledValue<T>(value: T, intervalMs = 300): T {
  const [shown, setShown] = useState(value);
  const pending = useRef(value);
  const timer = useRef<number | null>(null);

  useEffect(() => {
    pending.current = value;
    if (timer.current !== null) return;
    const elapsedOk = () => {
      timer.current = null;
      setShown(pending.current);
    };
    timer.current = window.setTimeout(elapsedOk, intervalMs);
    return () => {
      if (timer.current !== null) {
        window.clearTimeout(timer.current);
        timer.current = null;
      }
    };
  }, [value, intervalMs]);

  /* 卸载前把最后一次真实值落地，避免"进度停在倒数第二个值"。 */
  useEffect(() => {
    return () => {
      if (pending.current !== shown) setShown(pending.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return shown;
}

/**
 * 解析进度弹窗内容（§9.1 ParseProgressDialog 的展示体）。
 *
 * 它**不是**一个自建弹窗——按 AGENTS.md，提示与进度只能走 DialogProvider 队列；
 * 调用方把它作为 `dialog.open({...})` 的 body 传入，从而复用同一套焦点陷阱、
 * Escape、队列与遮罩行为。
 */
export function ParseProgressBody({
  parsedRows,
  totalRows,
  accepted,
  rejected,
  duplicates,
  state = "running",
  onCancel,
  cancelLabel = "取消解析",
}: {
  parsedRows: number;
  totalRows?: number | null;
  accepted: number;
  rejected: number;
  duplicates: number;
  state?: ProgressState;
  onCancel?: () => void;
  cancelLabel?: string;
}) {
  const percent = totalRows && totalRows > 0 ? (parsedRows / totalRows) * 100 : null;
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-4)" }}>
      <LinearProgress
        percent={percent}
        state={state}
        label="解析进度"
        valueText={`${parsedRows.toLocaleString("zh-CN")} 行`}
        size="lg"
      />
      <dl className="cs-kv">
        <div className="cs-kv__row">
          <dt>已解析行</dt>
          <dd className="num">{parsedRows.toLocaleString("zh-CN")}</dd>
        </div>
        <div className="cs-kv__row">
          <dt>有效地址</dt>
          <dd className="num">{accepted.toLocaleString("zh-CN")}</dd>
        </div>
        <div className="cs-kv__row">
          <dt>无效行</dt>
          <dd className="num">{rejected.toLocaleString("zh-CN")}</dd>
        </div>
        <div className="cs-kv__row">
          <dt>重复地址</dt>
          <dd className="num">{duplicates.toLocaleString("zh-CN")}</dd>
        </div>
      </dl>
      {onCancel ? (
        <div>
          <button type="button" className="btn btn--sm" onClick={onCancel}>
            {cancelLabel}
          </button>
        </div>
      ) : null}
    </div>
  );
}
