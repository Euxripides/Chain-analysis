import React from "react";
import { Link } from "react-router-dom";
import { Check, ChevronDown } from "lucide-react";
import { cx } from "../utils";

/**
 * 数据展示原语（用户端重构规范 V4 §13 / §17）
 *
 * 这一层**不引入 Ant Table / ProTable**：它是 L2 高密度详情表的底座，
 * 也服务尚未迁移的页面。迁移完成的普通业务表用 CsProTable（data/pro-table）。
 */

/* ---------------------------------------------------------------- StatusTag */

export type CsStatusTone = "ok" | "warn" | "danger" | "info" | "neutral" | "running";

const TONE_CLASS: Record<CsStatusTone, string> = {
  ok: "badge--ok",
  warn: "badge--warn",
  danger: "badge--danger",
  info: "badge--info",
  neutral: "badge--neutral",
  running: "badge--info",
};

/**
 * 状态标签（§25.1 三通道）：
 * 颜色只是辅助——文字始终存在，运行态额外带一个脉冲点（形状通道），
 * 灰度打印或色觉障碍下依然可分辨。
 */
export function CsStatusTag({
  tone = "neutral",
  children,
  pulse,
  className,
}: {
  tone?: CsStatusTone;
  children: React.ReactNode;
  /** 运行态显示低频脉冲点。Reduced Motion 下静态。 */
  pulse?: boolean;
  className?: string;
}) {
  return (
    <span className={cx("badge", TONE_CLASS[tone], className)}>
      {pulse ? <i className={cx("cs-dot", "cs-dot--running")} aria-hidden="true" /> : null}
      {children}
    </span>
  );
}

/* --------------------------------------------------------------------- Tabs */

export interface CsTabItem {
  key: string;
  label: React.ReactNode;
  count?: number;
  /** 深链接：给定时渲染成 react-router 的 Link（尊重 basename，§0.4.1）。 */
  to?: string;
}

/**
 * Tabs（§13.1）。
 *
 * 保留既有可访问键盘语义：容器是 `role="tablist"`（按钮模式）或 `nav`（深链接模式），
 * 选中态**同时**覆盖 `aria-selected` 与 `aria-current` 两种通道——
 * 深链接 Tab 用 NavLink 时只有 aria-current，两者都要能表达"我在哪"。
 *
 * 指示器动画所有权归 Motion shared layout（§9.6）；panel 只做轻 opacity。
 */
export function CsTabs({
  items,
  activeKey,
  onChange,
  ariaLabel = "视图切换",
  className,
}: {
  items: CsTabItem[];
  activeKey: string;
  onChange?: (key: string) => void;
  ariaLabel?: string;
  className?: string;
}) {
  const isNav = items.some((item) => Boolean(item.to));
  const content = items.map((item) => {
    const active = item.key === activeKey;
    const label = (
      <>
        {item.label}
        {item.count !== undefined ? <span className="cs-tab__count">{item.count}</span> : null}
      </>
    );
    if (item.to) {
      return (
        <Link
          key={item.key}
          to={item.to}
          className="cs-tab"
          aria-current={active ? "page" : undefined}
        >
          {label}
        </Link>
      );
    }
    return (
      <button
        key={item.key}
        type="button"
        role="tab"
        className="cs-tab"
        aria-selected={active}
        tabIndex={active ? 0 : -1}
        onClick={() => onChange?.(item.key)}
      >
        {label}
      </button>
    );
  });

  if (isNav) {
    return (
      <nav className={cx("cs-tabs", className)} aria-label={ariaLabel}>
        {content}
      </nav>
    );
  }
  return (
    <div className={cx("cs-tabs", className)} role="tablist" aria-label={ariaLabel}>
      {content}
    </div>
  );
}

/* ------------------------------------------------------------------ FilterBar */

/**
 * 筛选工具条（§13.3）：Search | Main Filters | More Filters | Saved View | Reset。
 *
 * 只提供**排布**与展开容器；筛选字段本身仍由页面按服务端实际支持的参数提供
 * （规范 §33 明确禁止伪造服务端没有的筛选）。
 */
export function CsFilterToolbar({
  search,
  filters,
  more,
  moreLabel = "更多筛选",
  moreOpen,
  onToggleMore,
  savedView,
  onReset,
  className,
}: {
  search?: React.ReactNode;
  filters?: React.ReactNode;
  more?: React.ReactNode;
  moreLabel?: string;
  moreOpen?: boolean;
  onToggleMore?: () => void;
  savedView?: React.ReactNode;
  onReset?: () => void;
  className?: string;
}) {
  return (
    <div className={cx("cs-filterbar", className)}>
      {search}
      {filters}
      {more ? (
        <button
          type="button"
          className="cs-chip"
          aria-expanded={moreOpen ? "true" : "false"}
          onClick={onToggleMore}
        >
          {moreLabel}
          <ChevronDown size={13} aria-hidden="true" style={{ transform: moreOpen ? "rotate(180deg)" : undefined, transition: "transform var(--cs-dur-fast) var(--cs-ease-enter)" }} />
        </button>
      ) : null}
      <span className="cs-filterbar__spacer" />
      {savedView}
      {onReset ? (
        <button type="button" className="cs-chip" onClick={onReset}>
          清除筛选
        </button>
      ) : null}
      {more && moreOpen ? (
        <div style={{ flexBasis: "100%", display: "flex", gap: 8, flexWrap: "wrap", paddingTop: 4 }}>{more}</div>
      ) : null}
    </div>
  );
}

/* -------------------------------------------------------------- Row selection */

/** 表格行选择框：保留原生 checkbox 的键盘行为，只换外观。 */
export function CsRowCheckbox({
  checked,
  onChange,
  label,
}: {
  checked: boolean;
  onChange: (next: boolean) => void;
  label: string;
}) {
  return (
    <label style={{ display: "inline-flex", alignItems: "center", cursor: "pointer" }}>
      <input
        type="checkbox"
        checked={checked}
        aria-label={label}
        onChange={(event) => onChange(event.target.checked)}
      />
      <span className="visually-hidden">{label}</span>
    </label>
  );
}

/** 选中态的对勾图标（用于选中列表项/卡片）。 */
export const CsCheckIcon = Check;
