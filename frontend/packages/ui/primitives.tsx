import React, { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import {
  AlertTriangle,
  Check,
  ChevronRight,
  CircleSlash,
  Clock,
  Inbox,
  RefreshCw,
  XCircle,
} from "lucide-react";
import { useDialog } from "./Dialog";
import { cx, useHorizontalOverflowHint } from "./layout";
import { ProgressGlyph, progressStateFor } from "./progress";
import { RowCheckbox, SelectAllCheckbox } from "./navigation";

/**
 * §17.3 共享组件：PageHeader / Card / Metric / Field / Badge / EntityLink / Amount / 状态组件。
 *
 * 2026-09 用户端重构（蓝图 §8.5 兼容矩阵）：
 *   本文件所有**原有导出名与默认行为保持不变**，新增能力一律通过可选 prop 提供，
 *   因此后台端与未迁移的页面不会被动改变。已迁移页面传入新 prop 才启用新结构：
 *     - PageHeader   + breadcrumb / status / meta / primaryAction / secondaryAction
 *     - Card         + variant
 *     - Metric       → 内部委托给新的 MetricCard（旧 API 继续工作）
 *     - Field        + required / description
 *     - DataTable    + 吸顶表头 / 行激活 / 批量选择 / 冻结首列 / 列宽稳定的 skeleton
 */

/* ---------------------------------------------------------------- PageHeader */

export interface BreadcrumbItem {
  label: string;
  to?: string;
}

export function PageHeader({
  title,
  subtitle,
  actions,
  breadcrumb,
  status,
  meta,
  primaryAction,
  secondaryAction,
}: {
  title: string;
  subtitle?: React.ReactNode;
  actions?: React.ReactNode;
  /** 面包屑：用真实路由表达层级（§8.1）。最后一项为当前页，不加链接。 */
  breadcrumb?: BreadcrumbItem[];
  /** 当前对象的状态徽标，放在标题右侧（§8.1"标题、状态、摘要"）。 */
  status?: React.ReactNode;
  /** 数据时间、来源等界面元信息。 */
  meta?: React.ReactNode;
  /** 主操作：**危险操作不能成为默认主按钮**（§8.1）。 */
  primaryAction?: React.ReactNode;
  secondaryAction?: React.ReactNode;
}) {
  const actionSlot = actions ?? (
    <>
      {secondaryAction}
      {primaryAction}
    </>
  );
  return (
    <div className="page-head">
      <div>
        {breadcrumb && breadcrumb.length > 0 ? (
          <nav className="page-head__crumbs" aria-label="面包屑">
            {breadcrumb.map((crumb, index) => (
              <React.Fragment key={crumb.label + index}>
                {index > 0 ? <ChevronRight size={12} aria-hidden="true" className="page-head__crumb-sep" /> : null}
                {crumb.to && index < breadcrumb.length - 1 ? (
                  /* 用 react-router 的 Link 而不是 <a href>：面包屑是应用内导航，
                     整页刷新会丢掉筛选状态、缓存与滚动位置（后台端同样受益）。 */
                  <Link to={crumb.to}>{crumb.label}</Link>
                ) : (
                  <span aria-current={index === breadcrumb.length - 1 ? "page" : undefined}>{crumb.label}</span>
                )}
              </React.Fragment>
            ))}
          </nav>
        ) : null}
        <div className="page-head__titlerow">
          <h1 className="page-head__title">{title}</h1>
          {status}
        </div>
        {subtitle ? <div className="page-head__sub">{subtitle}</div> : null}
        {meta ? <div className="page-head__meta">{meta}</div> : null}
      </div>
      {actionSlot ? <div className="page-head__actions">{actionSlot}</div> : null}
    </div>
  );
}

/* ---------------------------------------------------------------------- Card */

export type CardVariant = "default" | "interactive" | "selected" | "flat";

export function Card({
  title,
  actions,
  flush,
  variant = "default",
  children,
  className,
  id,
}: {
  title?: React.ReactNode;
  actions?: React.ReactNode;
  flush?: boolean;
  /**
   * flat      无阴影的静态容器（列表页大量使用，避免"每张卡都浮起来"，§4.2）
   * interactive 可点击卡片：hover 只加深边框，不上浮缩放
   * selected   当前选中对象
   */
  variant?: CardVariant;
  children: React.ReactNode;
  className?: string;
  id?: string;
}) {
  return (
    <section className={cx("card", "card--" + variant, className)} id={id}>
      {title || actions ? (
        <header className="card__head">
          <h2 className="card__title">{title}</h2>
          <span style={{ flex: 1 }} />
          {actions}
        </header>
      ) : null}
      <div className={cx("card__body", flush && "card__body--flush")}>{children}</div>
    </section>
  );
}

/* -------------------------------------------------------------------- Metric */

export type MetricTone = "ok" | "warn" | "danger" | "neutral";

/**
 * 指标卡（§8.3 MetricCard）：数值、单位、状态和跳转；数据来源说明由 Dialog 展示。
 * `sourceNote` 只作为按钮的**无障碍名称与弹窗正文**，本身不渲染成页面内说明段落。
 */
export function MetricCard({
  label,
  value,
  hint,
  tone,
  unit,
  status,
  href,
  onActivate,
  source,
  sourceNote,
}: {
  label: string;
  value: React.ReactNode;
  hint?: React.ReactNode;
  tone?: MetricTone;
  unit?: React.ReactNode;
  status?: React.ReactNode;
  href?: string;
  onActivate?: () => void;
  /** "口径说明"弹窗的标题来源，例如 "jobs_failed"。 */
  source?: string;
  sourceNote?: React.ReactNode;
}) {
  const dialog = useDialog();
  const color =
    tone === "ok" ? "var(--c-green)" : tone === "warn" ? "var(--c-amber-text)" : tone === "danger" ? "var(--c-danger)" : undefined;
  const interactive = Boolean(href || onActivate);

  /**
   * 口径说明按钮。
   *
   * 注意嵌套问题：如果指标卡整体是 <button>/<a>，把说明按钮放进卡片内容里会产生
   * **嵌套交互元素**（无效 HTML，屏幕阅读器与键盘行为都会错乱）。
   * 因此这里把它挂在卡片内容之外，作为可点击区域的兄弟节点。
   */
  const info = sourceNote ? (
    <button
      type="button"
      className="metric__info"
      aria-label={label + " 口径说明"}
      title="口径说明"
      onClick={(event) => {
        event.preventDefault();
        event.stopPropagation();
        dialog.notify("info", label + " · 指标口径", sourceNote, source ? [{ label: "指标标识", value: source }] : undefined);
      }}
    >
      i
    </button>
  ) : null;

  const content = (
    <>
      <div className="metric__label">
        {label}
        {interactive ? null : info}
      </div>
      <div className="metric__value" style={{ color }}>
        {value}
        {unit ? <span className="metric__unit">{unit}</span> : null}
      </div>
      {hint ? <div className="metric__hint">{hint}</div> : null}
      {status ? <div className="metric__status">{status}</div> : null}
    </>
  );

  if (interactive) {
    return (
      <div className={cx("metric", "metric--link")}>
        {info ? <span className="metric__infoslot">{info}</span> : null}
        {href ? (
          <a
            className="metric__hit"
            href={href}
            onClick={onActivate ? (event) => { event.preventDefault(); onActivate(); } : undefined}
          >
            {content}
          </a>
        ) : (
          <button type="button" className="metric__hit" onClick={onActivate}>
            {content}
          </button>
        )}
      </div>
    );
  }
  return <div className="metric">{content}</div>;
}

/** 兼容导出：`Metric` 内部迁移到 `MetricCard`，旧调用方参数不变。 */
export function Metric({
  label,
  value,
  hint,
  tone,
}: {
  label: string;
  value: React.ReactNode;
  hint?: React.ReactNode;
  tone?: MetricTone;
}) {
  return <MetricCard label={label} value={value} hint={hint} tone={tone} />;
}

/* --------------------------------------------------------------------- Field */

export function Field({
  label,
  hint,
  htmlFor,
  error,
  required,
  description,
  children,
}: {
  label: string;
  hint?: string;
  htmlFor?: string;
  error?: string;
  /** 必填标记：同时写入 `aria-required` 语义由调用方的输入承担，这里只做视觉与文本。 */
  required?: boolean;
  /** 字段说明：属于字段本身而非提示内容（code-standards.md 末段）。 */
  description?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="field">
      <label className="field__label" htmlFor={htmlFor}>
        {label}
        {required ? (
          <span className="field__required" aria-hidden="true">
            *
          </span>
        ) : null}
      </label>
      {description ? <span className="field__desc">{description}</span> : null}
      {children}
      {hint ? <span className="field__hint">{hint}</span> : null}
      {/*
        字段级错误紧贴输入框展示（蓝图 §12.5"验证错误靠近字段"）。
        **不使用 role="alert"**：仓库测试会把 [role="alert"] 判定为页面内提示条
        （tests/dual-system-sprint4.spec.ts 的 expectNoInlineNotice），
        而字段校验属于界面本身，不是提示通道。真正的错误结果仍走 Dialog。
      */}
      {error ? <span className="field__hint field__hint--error">{error}</span> : null}
    </div>
  );
}

/* --------------------------------------------------------------------- Badge */

export type StatusTone = "ok" | "warn" | "danger" | "info" | "neutral";

const TONE_GLYPH: Record<StatusTone, string> = {
  ok: "●",
  warn: "▲",
  danger: "■",
  info: "◆",
  neutral: "○",
};

/** 状态不只靠颜色传达（§27 UX-003 / 蓝图 §9.2、§14）：每种状态都带形状前缀。 */
export function StatusBadge({ tone, children }: { tone: StatusTone; children: React.ReactNode }) {
  return (
    <span className={`badge badge--${tone}`}>
      <span className="badge__glyph" aria-hidden="true">
        {TONE_GLYPH[tone]}
      </span>
      {children}
    </span>
  );
}

export function statusToneFor(value: string): StatusTone {
  const v = (value || "").toUpperCase();
  if (["ACTIVE", "SUCCESS", "OK", "READY", "DONE", "COMPLETED", "VERIFIED", "ALLOW", "RUNNING", "APPROVED", "SIGNED"].includes(v)) return "ok";
  if (["PENDING", "PAUSED", "INVITED", "DEGRADED", "WARN", "REVIEW", "PARTIAL", "WAITING_DATA", "WAITING_REVIEW", "IN_REVIEW"].includes(v)) return "warn";
  if (["FAILED", "FAILURE", "ERROR", "DISABLED", "DENIED", "LOCKED", "SUSPENDED", "CANCELLED", "BLOCKED", "REJECTED"].includes(v)) return "danger";
  if (["INFO", "NEW", "CREATED", "QUEUED"].includes(v)) return "info";
  return "neutral";
}

/**
 * 进度状态徽标：文字 + 形状图标 + 颜色三通道（§9.2）。
 * 与 `StatusBadge` 的区别是它面向流水线状态机，并显示统一中文名。
 */
export function StatusBadgeForState({ value, label }: { value: string; label?: React.ReactNode }) {
  const state = progressStateFor(value);
  const tone: Record<string, StatusTone> = {
    queued: "neutral",
    running: "info",
    paused: "warn",
    waiting: "warn",
    completed: "ok",
    failed: "danger",
    cancelled: "neutral",
  };
  return (
    <span className={`badge badge--${tone[state]} badge--state`}>
      <ProgressGlyph state={state} size={11} />
      {label ?? value}
    </span>
  );
}

/* ---------------------------------------------------------------- EntityLink */

export function EntityLink({
  value,
  href,
  label,
  copyable = true,
}: {
  value: string;
  href?: string;
  label?: string;
  copyable?: boolean;
}) {
  const dialog = useDialog();
  const short = useMemo(() => (value && value.length > 22 ? `${value.slice(0, 10)}…${value.slice(-8)}` : value), [value]);
  return (
    <span className="entity-link" title={value}>
      {href ? (
        <a href={href} className="mono">
          {label ?? short}
        </a>
      ) : (
        <span className="mono">{label ?? short}</span>
      )}
      {copyable ? (
        <button
          type="button"
          className="entity-link__copy"
          /* 复制的永远是完整值，不是这行显示出来的缩略值（蓝图 §2.5）。 */
          aria-label={`复制 ${label ?? short}`}
          onClick={async () => {
            try {
              await navigator.clipboard.writeText(value);
            } catch {
              dialog.notify("warning", "无法访问剪贴板", "浏览器拒绝了剪贴板访问，请手动选中并复制。");
            }
          }}
        >
          ⧉
        </button>
      ) : null}
    </span>
  );
}

/* -------------------------------------------------------------------- Amount */

/**
 * 金额：禁止把大整数放进 JSON number，raw_amount 一律按字符串展示（§21.2）。
 *
 * 蓝图 §8.3 补充两点，且**不改变原有字符串精度处理**：
 *   - `fiat` 为空时显示 `—` 而不是 0（USD 不可用时不得暗示"价值为零"）；
 *   - `direction` 表达收/付方向，用符号 + 文字双重编码。
 */
export function Amount({
  rawAmount,
  decimals,
  displayAmount,
  unit,
  fiat,
  direction,
}: {
  rawAmount?: string;
  decimals?: number | null;
  displayAmount?: string;
  unit?: string;
  /** 法币估值；`null`/`undefined`/`""` 一律显示 `—`。 */
  fiat?: string | null;
  direction?: "in" | "out";
}) {
  const text = displayAmount ?? formatRaw(rawAmount ?? "0", decimals ?? 0);
  const fiatText = fiat === null || fiat === undefined || fiat === "" ? "—" : fiat;
  return (
    <span className="amount" data-direction={direction}>
      {direction ? (
        <span className="amount__dir" aria-hidden="true">
          {direction === "in" ? "+" : "−"}
        </span>
      ) : null}
      <span className="amount__raw">{text}</span>
      {unit ? <span className="amount__unit">{unit}</span> : null}
      {fiat !== undefined ? <span className="amount__fiat">{fiatText}</span> : null}
    </span>
  );
}

function formatRaw(raw: string, decimals: number): string {
  const negative = raw.startsWith("-");
  const digits = (negative ? raw.slice(1) : raw).padStart(decimals + 1, "0");
  const intPart = digits.slice(0, digits.length - decimals);
  const frac = decimals > 0 ? digits.slice(digits.length - decimals).replace(/0+$/, "") : "";
  return `${negative ? "-" : ""}${intPart.replace(/\B(?=(\d{3})+(?!\d))/g, ",")}${frac ? `.${frac}` : ""}`;
}

/* -------------------------------------------------------------------- States */

export function EmptyState({
  title,
  body,
  action,
  icon,
}: {
  title: string;
  body?: React.ReactNode;
  action?: React.ReactNode;
  icon?: React.ReactNode;
}) {
  return (
    <div className="state">
      {icon ?? <Inbox size={26} aria-hidden="true" />}
      <div className="state__title">{title}</div>
      {body ? <div className="state__body">{body}</div> : null}
      {action}
    </div>
  );
}

export function ErrorState({
  title,
  message,
  onRetry,
}: {
  title: string;
  message: string;
  onRetry?: () => void;
}) {
  return (
    <div className="state">
      <AlertTriangle size={26} aria-hidden="true" style={{ color: "var(--c-danger)" }} />
      <div className="state__title">{title}</div>
      {/* 页面里只放安全文案；Trace ID 与详细错误通过 Dialog（§19.7）。 */}
      <div className="state__body">{message}</div>
      {onRetry ? (
        <button type="button" className="btn btn--sm" onClick={onRetry}>
          <RefreshCw size={14} />
          重试
        </button>
      ) : null}
    </div>
  );
}

/**
 * 骨架屏（§8.4"加载时使用保持列宽的 skeleton，不能整体塌缩"）。
 * 原有两个参数的调用方式保持有效。
 */
export function SkeletonRows({
  rows = 6,
  columns = 5,
  widths,
}: {
  rows?: number;
  columns?: number;
  /** 每列宽度百分比；给出时骨架的列宽与最终表格一致，避免加载完成时列宽跳动。 */
  widths?: string[];
}) {
  return (
    <div aria-busy="true" aria-live="polite">
      <span className="visually-hidden">正在加载数据</span>
      {Array.from({ length: rows }).map((_, r) => (
        <div className="skeleton-row" key={r}>
          {Array.from({ length: columns }).map((__, c) => (
            <div
              className="skeleton"
              key={c}
              style={widths && widths[c] ? { flex: `0 0 ${widths[c]}` } : { flex: c === 0 ? 2 : 1 }}
            />
          ))}
        </div>
      ))}
    </div>
  );
}

export function NoPermissionState({ permission, title }: { permission?: string; title?: string }) {
  return (
    <div className="state">
      <CircleSlash size={26} aria-hidden="true" style={{ color: "var(--c-amber-text)" }} />
      <div className="state__title">{title ?? "没有访问该功能的权限"}</div>
      <div className="state__body">
        当前角色未被授予所需权限{permission ? <>（<span className="mono">{permission}</span>）</> : null}。
        如需访问，请联系组织 Owner 调整角色绑定。
      </div>
    </div>
  );
}

/* --------------------------------------------------------------- EvidenceRail */

/**
 * §7.1 签名元素：Evidence Rail —— 展示来源、时间、置信度、哈希与审计事件。
 *
 * 蓝图 §8.5 要求"升级选中、折叠和检查器联动，保留 `items/title`"：
 * 旧的 `items: {label, value}[]` 调用方式**完全不变**；新能力全部是可选字段，
 * 只有传了 `selectable`/`collapsible` 的页面才会看到选中态与折叠。
 */
export interface EvidenceItem {
  label: string;
  value: React.ReactNode;
  /** 稳定的选择键；缺省时用 label。 */
  key?: string;
  /** 状态色调：用于左侧色条，颜色只是辅助，语义仍由 label/value 承载。 */
  tone?: "neutral" | "info" | "ok" | "warn" | "danger";
  /** 次要说明行。 */
  meta?: React.ReactNode;
  /** 需要跳转到检查器的条目。 */
  href?: string;
}

export function EvidenceRail({
  items,
  title = "证据链",
  selectable,
  selectedKey,
  onSelect,
  collapsible,
  defaultCollapsed,
  actions,
  emptyText = "当前调查还没有可展示的证据条目",
}: {
  items: EvidenceItem[];
  title?: string;
  /** 允许选中；选中项与主区联动（蓝图 §12.9"Evidence Rail 可折叠、可打开检查器"）。 */
  selectable?: boolean;
  selectedKey?: string;
  onSelect?: (item: EvidenceItem) => void;
  collapsible?: boolean;
  defaultCollapsed?: boolean;
  /** 标题右侧操作，例如"打开检查器"。 */
  actions?: React.ReactNode;
  emptyText?: string;
}) {
  const [collapsed, setCollapsed] = useState(Boolean(defaultCollapsed));
  const open = !(collapsible && collapsed);
  return (
    <aside className="card evidence-rail" aria-label={title}>
      <header className="card__head">
        {collapsible ? (
          <button
            type="button"
            className="cs-hier__toggle"
            aria-expanded={open}
            aria-label={open ? "折叠" + title : "展开" + title}
            onClick={() => setCollapsed((value) => !value)}
          >
            {open ? <ChevronRight size={14} style={{ transform: "rotate(90deg)" }} /> : <ChevronRight size={14} />}
          </button>
        ) : null}
        <h2 className="card__title">{title}</h2>
        {items.length > 0 ? <span className="cs-section__count">{items.length} 项</span> : null}
        <span style={{ flex: 1 }} />
        {actions}
      </header>
      {open ? (
        <div className="card__body card__body--flush">
          {items.length === 0 ? (
            <div className="evidence-rail__empty">{emptyText}</div>
          ) : (
            items.map((item) => {
              const itemKey = item.key ?? item.label;
              const isSelected = selectedKey !== undefined && itemKey === selectedKey;
              const interactive = Boolean(selectable && onSelect);
              const body = (
                <>
                  <span className="evidence-rail__label">{item.label}</span>
                  <span className="evidence-rail__value">{item.value}</span>
                  {item.meta ? <span className="evidence-rail__meta">{item.meta}</span> : null}
                </>
              );
              const className = cx("evidence-rail__item", isSelected && "is-selected", interactive && "is-interactive");
              return interactive ? (
                <button
                  type="button"
                  key={itemKey}
                  className={className}
                  data-tone={item.tone ?? "neutral"}
                  aria-pressed={isSelected}
                  onClick={() => onSelect?.(item)}
                >
                  {body}
                </button>
              ) : item.href ? (
                <a key={itemKey} className={className} data-tone={item.tone ?? "neutral"} href={item.href}>
                  {body}
                </a>
              ) : (
                <div key={itemKey} className={className} data-tone={item.tone ?? "neutral"}>
                  {body}
                </div>
              );
            })
          )}
        </div>
      ) : null}
    </aside>
  );
}

/* ------------------------------------------------------------------ DataTable */

export interface DataTableColumn<T> {
  key: string;
  header: React.ReactNode;
  width?: string;
  align?: "left" | "right";
  render: (row: T) => React.ReactNode;
  /** 冻结首列（§8.4）：桌面端横向滚动时保持可见。 */
  frozen?: boolean;
}

export function DataTable<T>({
  columns,
  rows,
  rowKey,
  loading,
  error,
  empty,
  onRetry,
  onRowActivate,
  rowActivateLabel,
  selectable,
  selectedKeys,
  onSelectedChange,
  rowTestId,
  caption,
  emptyAction,
}: {
  columns: Array<DataTableColumn<T>>;
  rows: T[];
  rowKey: (row: T, index: number) => string;
  loading?: boolean;
  error?: string | null;
  empty?: React.ReactNode;
  onRetry?: () => void;
  /**
   * 行激活（§8.4"行点击只负责选择/预览，详情使用明确链接或 Enter"）。
   * 有 `onRowActivate` 时行可聚焦，Enter 触发；否则行不可点击。
   */
  onRowActivate?: (row: T) => void;
  rowActivateLabel?: (row: T) => string;
  /** 批量选择（§8.4）：仅在后端真正支持批量操作时才开启。 */
  selectable?: boolean;
  selectedKeys?: string[];
  onSelectedChange?: (next: string[]) => void;
  rowTestId?: (row: T) => string;
  caption?: string;
  /** 空结果时提供的可执行动作（§11.4"提供清除筛选"）。 */
  emptyAction?: React.ReactNode;
}) {
  if (loading) return <SkeletonRows rows={6} columns={columns.length} />;
  if (error) return <ErrorState title="数据加载失败" message={error} onRetry={onRetry} />;
  if (rows.length === 0) {
    return (
      <>
        {empty ?? (
          <EmptyState
            title="没有符合条件的记录"
            body="调整筛选条件后重试；当前筛选条件会保留，方便你修改而不是重新输入。"
            action={emptyAction}
          />
        )}
      </>
    );
  }

  /**
   * §30.1：>100 行必须虚拟化。保留既有"分页窗口 + 视口虚拟滚动"实现，
   * 保证不会把 10k 行 DOM 一次性插入；DOM 行数上限不变。
   */
  return (
    <PagedTable
      columns={columns}
      rows={rows}
      rowKey={rowKey}
      onRowActivate={onRowActivate}
      rowActivateLabel={rowActivateLabel}
      selectable={selectable}
      selectedKeys={selectedKeys}
      onSelectedChange={onSelectedChange}
      rowTestId={rowTestId}
      caption={caption}
    />
  );
}

function PagedTable<T>({
  columns,
  rows,
  rowKey,
  onRowActivate,
  rowActivateLabel,
  selectable,
  selectedKeys,
  onSelectedChange,
  rowTestId,
  caption,
}: {
  columns: Array<DataTableColumn<T>>;
  rows: T[];
  rowKey: (row: T, index: number) => string;
  onRowActivate?: (row: T) => void;
  rowActivateLabel?: (row: T) => string;
  selectable?: boolean;
  selectedKeys?: string[];
  onSelectedChange?: (next: string[]) => void;
  rowTestId?: (row: T) => string;
  caption?: string;
}) {
  /**
   * §30.1 / UX-004：分页窗口 + 视口虚拟滚动。
   * - 每页 1000 行，页面切换仍是 O(1)；
   * - 当前页只把视口附近的行放进 DOM，10k 行时 DOM 行数保持在几十行；
   * - 保留上一页/下一页，兼容既有后台长表操作习惯。
   */
  const pageSize = 1000;
  const rowHeight = 44;
  const overscan = 8;
  const viewportHeight = 560;
  const [page, setPage] = useState(0);
  const [scrollTop, setScrollTop] = useState(0);
  const pageCount = Math.max(1, Math.ceil(rows.length / pageSize));
  const current = Math.min(page, pageCount - 1);
  const { ref: scrollRef, sync: syncOverflow } = useHorizontalOverflowHint<HTMLDivElement>();

  /*
   * 页首滚动位置与分页状态在数据变化时重置。
   * 依赖数组必须用"长度 + 首页键"而不是 rows 引用：使用 React Query 的页面
   * 每次轮询都会拿到新数组引用，用 rows 做依赖会把用户翻到的第 5 页顶回第 1 页。
   */
  const firstKey = rows.length > 0 ? rowKey(rows[0], 0) : "";
  useEffect(() => {
    setPage(0);
    setScrollTop(0);
  }, [rows.length, firstKey]);

  const slice = useMemo(() => rows.slice(current * pageSize, current * pageSize + pageSize), [rows, current]);
  const start = Math.max(0, Math.floor(scrollTop / rowHeight) - overscan);
  const visibleCount = Math.ceil(viewportHeight / rowHeight) + overscan * 2;
  const end = Math.min(slice.length, start + visibleCount);
  const topPad = start * rowHeight;
  const bottomPad = Math.max(0, (slice.length - end) * rowHeight);
  const visible = slice.slice(start, end);
  const selected = new Set(selectedKeys ?? []);
  const pageKeys = slice.map((row, index) => rowKey(row, current * pageSize + index));
  const allSelected = pageKeys.length > 0 && pageKeys.every((key) => selected.has(key));
  const someSelected = pageKeys.some((key) => selected.has(key));
  const columnCount = columns.length + (selectable ? 1 : 0);

  const toggleAll = (next: boolean) => {
    if (!onSelectedChange) return;
    if (next) onSelectedChange(Array.from(new Set([...(selectedKeys ?? []), ...pageKeys])));
    else onSelectedChange((selectedKeys ?? []).filter((key) => !pageKeys.includes(key)));
  };

  return (
    <>
      <div
        className="table-scroll"
        style={{ maxHeight: viewportHeight, overflowY: "auto" }}
        ref={scrollRef}
        onScroll={(event) => {
          const next = event.currentTarget.scrollTop;
          window.requestAnimationFrame(() => {
            setScrollTop(next);
            syncOverflow();
          });
        }}
        data-virtualized="true"
        data-total-rows={rows.length}
      >
        <table className="table table--virtual">
          {caption ? <caption className="visually-hidden">{caption}</caption> : null}
          <thead>
            <tr>
              {selectable ? (
                <th style={{ width: 44 }}>
                  <SelectAllCheckbox checked={allSelected} indeterminate={someSelected} onChange={toggleAll} />
                </th>
              ) : null}
              {columns.map((c) => (
                <th
                  key={c.key}
                  style={{ width: c.width, textAlign: c.align ?? "left" }}
                  className={c.frozen ? "table__th--frozen" : undefined}
                  scope="col"
                >
                  {c.header}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {topPad > 0 ? (
              <tr aria-hidden="true" style={{ height: topPad }}>
                <td colSpan={columnCount} style={{ padding: 0, border: 0 }} />
              </tr>
            ) : null}
            {visible.map((row, index) => {
              const absolute = current * pageSize + start + index;
              const key = rowKey(row, absolute);
              const isSelected = selected.has(key);
              return (
                <tr
                  key={key}
                  style={{ height: rowHeight }}
                  data-selected={isSelected ? "true" : undefined}
                  data-testid={rowTestId?.(row)}
                  tabIndex={onRowActivate ? 0 : undefined}
                  aria-label={onRowActivate ? rowActivateLabel?.(row) : undefined}
                  onKeyDown={
                    onRowActivate
                      ? (event) => {
                          if (event.key === "Enter") {
                            event.preventDefault();
                            onRowActivate(row);
                          }
                        }
                      : undefined
                  }
                  onClick={onRowActivate ? () => onRowActivate(row) : undefined}
                  className={onRowActivate ? "table__row--activatable" : undefined}
                >
                  {selectable ? (
                    <td onClick={(event) => event.stopPropagation()}>
                      <RowCheckbox
                        checked={isSelected}
                        label={"选择第 " + (absolute + 1) + " 行"}
                        onChange={(next) => {
                          if (!onSelectedChange) return;
                          onSelectedChange(next ? [...(selectedKeys ?? []), key] : (selectedKeys ?? []).filter((k) => k !== key));
                        }}
                      />
                    </td>
                  ) : null}
                  {columns.map((c) => (
                    <td key={c.key} style={{ textAlign: c.align ?? "left" }} className={c.frozen ? "table__td--frozen" : undefined}>
                      {c.render(row)}
                    </td>
                  ))}
                </tr>
              );
            })}
            {bottomPad > 0 ? (
              <tr aria-hidden="true" style={{ height: bottomPad }}>
                <td colSpan={columnCount} style={{ padding: 0, border: 0 }} />
              </tr>
            ) : null}
          </tbody>
        </table>
      </div>
      <div className="toolbar" style={{ padding: "var(--sp-3) var(--sp-4)", justifyContent: "space-between" }}>
        <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }} data-testid="table-summary">
          共 {rows.length} 行 · 第 {current + 1} / {pageCount} 页 · DOM {visible.length} 行（虚拟滚动）
        </span>
        <span className="toolbar">
          <button
            type="button"
            className="btn btn--sm"
            disabled={current === 0}
            onClick={() => {
              setPage(current - 1);
              setScrollTop(0);
            }}
          >
            上一页
          </button>
          <button
            type="button"
            className="btn btn--sm"
            disabled={current >= pageCount - 1}
            onClick={() => {
              setPage(current + 1);
              setScrollTop(0);
            }}
          >
            下一页
          </button>
        </span>
      </div>
    </>
  );
}

/* --------------------------------------------------------------- RelativeTime */

/** 相对时间：**title 保留完整 ISO 时间**（§8.5 兼容矩阵要求）。 */
export function RelativeTime({ value }: { value: string | null | undefined }) {
  if (!value) return <span style={{ color: "var(--c-slate)" }}>—</span>;
  const date = new Date(value);
  const diff = Date.now() - date.getTime();
  const minutes = Math.round(diff / 60000);
  let label: string;
  if (Math.abs(minutes) < 1) label = "刚刚";
  else if (Math.abs(minutes) < 60) label = `${minutes} 分钟前`;
  else if (Math.abs(minutes) < 60 * 24) label = `${Math.round(minutes / 60)} 小时前`;
  else label = `${Math.round(minutes / 1440)} 天前`;
  return (
    <span title={date.toISOString()} className="num">
      <Clock size={12} aria-hidden="true" style={{ marginRight: 4, verticalAlign: -1 }} />
      {label}
    </span>
  );
}

export const icons = { Check, XCircle, AlertTriangle };
