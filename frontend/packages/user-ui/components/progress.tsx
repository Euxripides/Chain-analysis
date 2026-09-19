import React from "react";
import { cx } from "../utils";

/**
 * 进度与实时指标（用户端重构规范 V4 §25 / §27 / §28）
 *
 * 与 `@chain/ui/progress` 的分工：
 *   - 状态机、aria 语义、ETA 口径等业务能力仍在 `@chain/ui/progress`（保持兼容）；
 *   - 这里提供**用户端专属的版式组件**——尤其是 Job Detail 首屏的主进度卡，
 *     它按规范 §28.1 的字段层级组织，而不是把服务端字段直接铺开。
 *
 * 数值规则（§28.4 / §28.5 / §63B）：
 *   - ETA 为 null 时显示 `—`，**不在前端预测**；
 *   - 预计完成时刻只是"当前时刻 + 服务端 eta_seconds"的显示换算，不成为新的事实；
 *   - 所有数字用 tabular-nums，位置固定，位数变化不引起抖动。
 */

/** ETA/剩余时间：服务端没给就显示 —，绝不猜（§28.4）。 */
export function formatEta(etaSeconds: number | null | undefined, state?: string): string {
  if (state === "paused") return "已暂停";
  if (etaSeconds === null || etaSeconds === undefined || !Number.isFinite(etaSeconds)) return "—";
  if (etaSeconds < 0) return "—";
  const total = Math.round(etaSeconds);
  if (total < 60) return `${total} 秒`;
  const minutes = Math.floor(total / 60);
  const seconds = total % 60;
  if (minutes < 60) return `${minutes} 分 ${seconds} 秒`;
  const hours = Math.floor(minutes / 60);
  return `${hours} 小时 ${minutes % 60} 分`;
}

/**
 * 预计完成时刻（§28.4）。
 *
 * 只做 `now + etaSeconds` 的显示换算。时区语义沿用全站 formatter（§63B）：
 * 这里只输出 `HH:mm:ss`，**不做跨时区推断**。
 */
export function formatEtaClock(etaSeconds: number | null | undefined, state?: string): string {
  if (state === "paused") return "—";
  if (etaSeconds === null || etaSeconds === undefined || !Number.isFinite(etaSeconds) || etaSeconds < 0) return "—";
  const at = new Date(Date.now() + etaSeconds * 1000);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(at.getHours())}:${pad(at.getMinutes())}:${pad(at.getSeconds())}`;
}

export interface CsJobProgressFact {
  label: string;
  value: React.ReactNode;
}

/**
 * Job 主进度卡（§28.1）——下载任务详情页的**唯一首屏主角**。
 *
 * 只保留规范 §28.2「必须突出」的字段：状态、真实百分比、主进度条、速度、
 * ETA、预计完成时刻、当前阶段、控制与下载入口。
 * Shard / provider / last_event_id / 结构化事件等一律不进这里（进 JobTechnicalDetails）。
 *
 * 尺寸固定：百分比用等宽数字 + 固定行高，数值位数变化不会让卡片抖动（§28.6）。
 */
export function CsJobProgress({
  percent,
  state,
  percentText,
  facts,
  stage,
  etaSeconds,
  indeterminate,
  className,
  children,
}: {
  /** 服务端真实百分比；null 表示服务端没有总量 → 不伪造百分比。 */
  percent: number | null;
  state: string;
  /** 覆盖百分比文案（例如"已暂停"）。 */
  percentText?: React.ReactNode;
  /** 服务端给不出百分比时使用 indeterminate 条纹。 */
  indeterminate?: boolean;
  /** 速度 / 已下载 / 预计剩余等事实项。 */
  facts: CsJobProgressFact[];
  /** 当前阶段的一句自然语言。 */
  stage?: React.ReactNode;
  etaSeconds?: number | null;
  className?: string;
  children?: React.ReactNode;
}) {
  const shown = state === "completed" ? 100 : percent;
  const isIndeterminate = Boolean(indeterminate) && shown === null;
  const pctText =
    percentText ??
    (shown === null ? stateLabelFor(state) : `${Math.round(shown)}%`);
  /* aria-valuetext 只接受字符串：<div> 的 ARIA 属性不接受 ReactNode。 */
  const pctAriaText = typeof pctText === "string" || typeof pctText === "number" ? String(pctText) : stateLabelFor(state);

  return (
    <section className={cx("cs-surface", "cs-jobprogress", className)} data-state={state}>
      <div className="cs-jobprogress__pct" aria-hidden="true">
        {pctText}
      </div>
      <div
        className={cx("cs-progress__track", "cs-progress__track--lg")}
        role="progressbar"
        aria-label="总进度"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={isIndeterminate ? undefined : (shown ?? 0)}
        aria-valuetext={isIndeterminate ? stateLabelFor(state) : pctAriaText}
      >
        <div
          className={cx("cs-progress__fill", isIndeterminate && "cs-progress__fill--indeterminate")}
          data-tone={state}
          style={isIndeterminate ? undefined : { width: `${shown ?? 0}%` }}
        />
      </div>

      <div className="cs-jobprogress__facts">
        {facts.map((fact) => (
          <div className="cs-jobprogress__fact" key={fact.label}>
            <span className="cs-jobprogress__factlabel">{fact.label}</span>
            <span className="cs-jobprogress__factvalue">{fact.value}</span>
          </div>
        ))}
      </div>

      {stage ? <div className="cs-jobprogress__stage">{stage}</div> : null}
      {children}
    </section>
  );
}

function stateLabelFor(state: string): string {
  const map: Record<string, string> = {
    queued: "排队中",
    running: "进行中",
    paused: "已暂停",
    waiting: "等待中",
    completed: "已完成",
    failed: "已失败",
    cancelled: "已取消",
    info: "—",
  };
  return map[state] ?? "—";
}

/** 运行态状态点（§28.6）：低频 pulse，不用于正文。 */
export function CsStateDot({ state }: { state: string }) {
  return <i className={cx("cs-dot", "cs-dot--" + state)} aria-hidden="true" />;
}
