import React from "react";
import { motion } from "motion/react";
import { cx } from "../utils";
import { CsPressable } from "./motion";

/**
 * 卡片系统（用户端重构规范 V4 §10）
 *
 * **只有六种**，页面不得再发明新的卡片外观：
 *   SurfaceCard / InteractiveCard / MetricCard / ObjectCard / PanelCard / GlassHeroCard
 *
 * 所有卡片都**不自己写动画**（§9.6）：hover/press 由 CSS 过渡或 Motion 统一驱动，
 * 业务侧传入的 children 不得再叠加 transform 动画。
 */

/* ------------------------------------------------------------------ Surface */

export interface SurfaceCardProps extends Omit<React.HTMLAttributes<HTMLElement>, "title"> {
  /** 是否启用 hover 上浮（默认 true）。静态容器传 false，避免整屏乱动。 */
  hoverable?: boolean;
  /** 18px 主面板圆角（默认 16px 普通卡片）。 */
  panel?: boolean;
  /** 无描边无阴影的静态容器。 */
  flat?: boolean;
  /** 当前选中对象。 */
  selected?: boolean;
  as?: "section" | "div" | "article";
}

/**
 * 基础面（§10.2）：白面 + 1px 淡蓝描边 + 16px 圆角 + 很轻阴影。
 * Hover：y -2px、描边提蓝、阴影增强，220ms。**不做** scale 1.03。
 */
export const SurfaceCard = React.forwardRef<HTMLElement, SurfaceCardProps>(function SurfaceCard(
  { hoverable = true, panel, flat, selected, as = "section", className, children, ...rest },
  ref,
) {
  const Tag = as as React.ElementType;
  return (
    <Tag
      ref={ref}
      className={cx(
        "cs-surface",
        hoverable && !flat && "cs-surface--hoverable",
        panel && "cs-surface--panel",
        flat && "cs-surface--flat",
        selected && "cs-surface--selected",
        className,
      )}
      {...rest}
    >
      {children}
    </Tag>
  );
});

/* -------------------------------------------------------------- Interactive */

export interface InteractiveCardProps {
  onClick?: () => void;
  /** 渲染成 <a>：用于真实导航（保持浏览器语义与中键新开标签）。 */
  href?: string;
  to?: never;
  title?: React.ReactNode;
  body?: React.ReactNode;
  icon?: React.ReactNode;
  footer?: React.ReactNode;
  className?: string;
  "data-testid"?: string;
}

/**
 * 可点击卡片（§10.3）：首页快捷入口、最近案件、最近任务。
 * Hover：y -3px、scale 1.004、图标 x +2px、highlight 层淡入；Press：scale .988。
 *
 * 语义上渲染成 **button 或 a**，而不是"div + onClick"——键盘与读屏必须可用（§55）。
 */
export function InteractiveCard({
  onClick,
  href,
  title,
  body,
  icon,
  footer,
  className,
  ...rest
}: InteractiveCardProps) {
  const inner = (
    <>
      {icon ? <span className="cs-interactivecard__icon">{icon}</span> : null}
      {title ? <span className="cs-panelcard__title">{title}</span> : null}
      {body ? <span className="cs-metriccard__hint">{body}</span> : null}
      {footer ? <span className="cs-panelcard__extra">{footer}</span> : null}
    </>
  );
  const content = <CsPressable pressScale={0.988}>{inner}</CsPressable>;

  if (href) {
    return (
      <a className={cx("cs-surface", "cs-interactivecard", className)} href={href} {...rest}>
        {content}
      </a>
    );
  }
  return (
    <button type="button" className={cx("cs-surface", "cs-interactivecard", className)} onClick={onClick} {...rest}>
      {content}
    </button>
  );
}

/* ------------------------------------------------------------------- Metric */

export type CsMetricTone = "primary" | "success" | "warning" | "danger" | "neutral";

export interface MetricCardProps {
  label: React.ReactNode;
  /** 高精度金额/Token raw amount 请传字符串，本组件**不会**把它转成 number（§63B）。 */
  value: React.ReactNode;
  unit?: React.ReactNode;
  hint?: React.ReactNode;
  /** 变化量：只做一次箭头位移，不驱动整卡动画（§10.4）。 */
  delta?: { value: string; direction: "up" | "down" | "flat" };
  icon?: React.ReactNode;
  tone?: CsMetricTone;
  /** 值变化时是否闪一次背景高光（高精度金额用；普通整数用 count 过渡）。 */
  flashOnChange?: boolean;
  className?: string;
}

/**
 * 指标卡（§10.4）。
 *
 * 数字规则：
 *   - 安全整数：允许 count 过渡（由调用方决定是否启用）；
 *   - 高精度金额：**文本交叉淡入 + .cs-flash**，绝不 `Number()` —— 那会丢精度。
 */
export function MetricCard({
  label,
  value,
  unit,
  hint,
  delta,
  icon,
  tone = "primary",
  flashOnChange,
  className,
}: MetricCardProps) {
  const prev = React.useRef(value);
  const [flash, setFlash] = React.useState(false);
  React.useEffect(() => {
    if (!flashOnChange || prev.current === value) return;
    prev.current = value;
    setFlash(true);
    const timer = window.setTimeout(() => setFlash(false), 340);
    return () => window.clearTimeout(timer);
  }, [value, flashOnChange]);

  return (
    <SurfaceCard className={cx("cs-metriccard", className)} hoverable={false}>
      <div className="cs-metriccard__top">
        {icon ? (
          <span className="cs-metriccard__icon" data-tone={tone} aria-hidden="true">
            {icon}
          </span>
        ) : (
          <span />
        )}
        {delta ? (
          <span className="cs-metriccard__delta" data-dir={delta.direction}>
            {delta.direction === "up" ? "▲" : delta.direction === "down" ? "▼" : "—"}
            {delta.value}
          </span>
        ) : null}
      </div>
      <div className="cs-metriccard__label">{label}</div>
      <div className={cx("cs-metriccard__value", flash && "cs-flash")}>
        {value}
        {unit ? <span className="metric__unit">{unit}</span> : null}
      </div>
      {hint ? <div className="cs-metriccard__hint">{hint}</div> : null}
    </SurfaceCard>
  );
}

/* ------------------------------------------------------------------- Object */

/** 对象页顶部容器（地址 / 交易 / Evidence / Job）：身份 + 状态 + 操作（§8.2）。 */
export function ObjectCard({
  className,
  children,
  ...rest
}: React.HTMLAttributes<HTMLElement>) {
  return (
    <SurfaceCard as="div" hoverable={false} className={cx("cs-objectcard", className)} {...rest}>
      {children}
    </SurfaceCard>
  );
}

/* -------------------------------------------------------------------- Panel */

export interface PanelCardProps {
  title?: React.ReactNode;
  extra?: React.ReactNode;
  /** 表格/列表类内容自己去 padding。 */
  flush?: boolean;
  panel?: boolean;
  className?: string;
  id?: string;
  children: React.ReactNode;
}

/** 带标题栏的面板（§8.1 PageSection 的卡片化形态）。 */
export function PanelCard({ title, extra, flush, panel = true, className, id, children }: PanelCardProps) {
  return (
    <SurfaceCard as="section" hoverable={false} panel={panel} className={className} id={id}>
      {title || extra ? (
        <header className="cs-panelcard__header">
          {typeof title === "string" ? <h2 className="cs-panelcard__title">{title}</h2> : title}
          {extra ? <div className="cs-panelcard__extra">{extra}</div> : null}
        </header>
      ) : null}
      <div className={cx("cs-panelcard__body", flush && "cs-panelcard__body--flush")}>{children}</div>
    </SurfaceCard>
  );
}

/* --------------------------------------------------------------- GlassHero */

/**
 * Hero 容器（§32.2）：唯一允许大面积光晕与玻璃的卡片。
 *
 * 装饰层（Aurora）是**纯 CSS 动画**且 Reduced Motion 下静止；
 * 用固定 seed 的静态渐变而不是随机粒子，保证截图测试稳定（§63F）。
 */
export function GlassHeroCard({
  className,
  children,
  auroraDurationS = 22,
}: {
  className?: string;
  children: React.ReactNode;
  auroraDurationS?: number;
}) {
  return (
    <div className={cx("cs-glasshero", className)}>
      <div
        className="cs-glasshero__aurora"
        aria-hidden="true"
        style={{ ["--cs-aurora-dur" as string]: auroraDurationS + "s" }}
      />
      <div className="cs-glasshero__content">{children}</div>
    </div>
  );
}

/* ------------------------------------------------------------------ exports */

/** 供需要"卡片内浮动内容"的页面使用的轻量 motion 包装（不改变布局）。 */
export function CardFloat({ children }: { children: React.ReactNode }) {
  return (
    <motion.div initial={{ opacity: 0, y: 4 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.22 }}>
      {children}
    </motion.div>
  );
}
