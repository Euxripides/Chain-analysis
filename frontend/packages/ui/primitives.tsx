import React, { useEffect, useMemo, useState } from "react";
import { AlertTriangle, Check, CircleSlash, Clock, Inbox, RefreshCw, XCircle } from "lucide-react";
import { useDialog } from "./Dialog";

/** §17.3 共享组件：PageHeader / Card / Metric / Button / Field / Badge / EntityLink / Amount / 状态组件。 */

export function PageHeader({
  title,
  subtitle,
  actions,
}: {
  title: string;
  subtitle?: React.ReactNode;
  actions?: React.ReactNode;
}) {
  return (
    <div className="page-head">
      <div>
        <h1 className="page-head__title">{title}</h1>
        {subtitle ? <div className="page-head__sub">{subtitle}</div> : null}
      </div>
      {actions ? <div className="page-head__actions">{actions}</div> : null}
    </div>
  );
}

export function Card({
  title,
  actions,
  flush,
  children,
}: {
  title?: React.ReactNode;
  actions?: React.ReactNode;
  flush?: boolean;
  children: React.ReactNode;
}) {
  return (
    <section className="card">
      {title || actions ? (
        <header className="card__head">
          <h2 className="card__title">{title}</h2>
          <span style={{ flex: 1 }} />
          {actions}
        </header>
      ) : null}
      <div className={`card__body${flush ? " card__body--flush" : ""}`}>{children}</div>
    </section>
  );
}

export function Metric({
  label,
  value,
  hint,
  tone,
}: {
  label: string;
  value: React.ReactNode;
  hint?: React.ReactNode;
  tone?: "ok" | "warn" | "danger" | "neutral";
}) {
  const color =
    tone === "ok" ? "var(--c-green)" : tone === "warn" ? "var(--c-amber)" : tone === "danger" ? "var(--c-danger)" : undefined;
  return (
    <div className="metric">
      <div className="metric__label">{label}</div>
      <div className="metric__value" style={{ color }}>
        {value}
      </div>
      {hint ? <div className="metric__hint">{hint}</div> : null}
    </div>
  );
}

export function Field({
  label,
  hint,
  htmlFor,
  error,
  children,
}: {
  label: string;
  hint?: string;
  htmlFor?: string;
  error?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="field">
      <label className="field__label" htmlFor={htmlFor}>
        {label}
      </label>
      {children}
      {hint ? <span className="field__hint">{hint}</span> : null}
      {error ? (
        <span className="field__hint" style={{ color: "var(--c-danger)" }}>
          {error}
        </span>
      ) : null}
    </div>
  );
}

export type StatusTone = "ok" | "warn" | "danger" | "info" | "neutral";

const TONE_GLYPH: Record<StatusTone, string> = {
  ok: "●",
  warn: "▲",
  danger: "■",
  info: "◆",
  neutral: "○",
};

/** 状态不只靠颜色传达（§27 UX-003）：每种状态都带形状前缀。 */
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
  if (["ACTIVE", "SUCCESS", "OK", "READY", "DONE", "COMPLETED", "VERIFIED", "ALLOW", "RUNNING"].includes(v)) return "ok";
  if (["PENDING", "PAUSED", "INVITED", "DEGRADED", "WARN", "REVIEW", "PARTIAL"].includes(v)) return "warn";
  if (["FAILED", "FAILURE", "ERROR", "DISABLED", "DENIED", "LOCKED", "SUSPENDED", "CANCELLED", "BLOCKED"].includes(v)) return "danger";
  if (["INFO", "NEW"].includes(v)) return "info";
  return "neutral";
}

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

/** 金额：禁止把大整数放进 JSON number，raw_amount 一律按字符串展示（§21.2）。 */
export function Amount({
  rawAmount,
  decimals,
  displayAmount,
  unit,
}: {
  rawAmount?: string;
  decimals?: number | null;
  displayAmount?: string;
  unit?: string;
}) {
  const text = displayAmount ?? formatRaw(rawAmount ?? "0", decimals ?? 0);
  return (
    <span className="amount">
      {text}
      {unit ? <span className="amount__unit">{unit}</span> : null}
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

export function SkeletonRows({ rows = 6, columns = 5 }: { rows?: number; columns?: number }) {
  return (
    <div aria-busy="true" aria-live="polite">
      <span className="visually-hidden">正在加载数据</span>
      {Array.from({ length: rows }).map((_, r) => (
        <div className="skeleton-row" key={r}>
          {Array.from({ length: columns }).map((__, c) => (
            <div className="skeleton" key={c} style={{ flex: c === 0 ? 2 : 1 }} />
          ))}
        </div>
      ))}
    </div>
  );
}

export function NoPermissionState({ permission, title }: { permission?: string; title?: string }) {
  return (
    <div className="state">
      <CircleSlash size={26} aria-hidden="true" style={{ color: "var(--c-amber)" }} />
      <div className="state__title">{title ?? "没有访问该功能的权限"}</div>
      <div className="state__body">
        当前角色未被授予所需权限{permission ? <>（<span className="mono">{permission}</span>）</> : null}。
        如需访问，请联系组织 Owner 调整角色绑定。
      </div>
    </div>
  );
}

/** §7.1 签名元素：Evidence Rail —— 展示来源、时间、置信度、哈希与审计事件。 */
export interface EvidenceItem {
  label: string;
  value: React.ReactNode;
}

export function EvidenceRail({ items, title = "证据链" }: { items: EvidenceItem[]; title?: string }) {
  return (
    <aside className="card evidence-rail" aria-label={title}>
      <header className="card__head">
        <h2 className="card__title">{title}</h2>
      </header>
      <div className="card__body card__body--flush">
        {items.map((item) => (
          <div className="evidence-rail__item" key={item.label}>
            <span className="evidence-rail__label">{item.label}</span>
            <span className="evidence-rail__value">{item.value}</span>
          </div>
        ))}
      </div>
    </aside>
  );
}

export function DataTable<T>({
  columns,
  rows,
  rowKey,
  loading,
  error,
  empty,
  onRetry,
}: {
  columns: Array<{ key: string; header: React.ReactNode; width?: string; align?: "left" | "right"; render: (row: T) => React.ReactNode }>;
  rows: T[];
  rowKey: (row: T, index: number) => string;
  loading?: boolean;
  error?: string | null;
  empty?: React.ReactNode;
  onRetry?: () => void;
}) {
  if (loading) return <SkeletonRows rows={6} columns={columns.length} />;
  if (error) return <ErrorState title="数据加载失败" message={error} onRetry={onRetry} />;
  if (rows.length === 0) return <>{empty ?? <EmptyState title="没有符合条件的记录" body="调整筛选条件后重试。" />}</>;

  /**
   * §30.1：>100 行必须虚拟化。这里先实现"分页窗口渲染"，
   * 保证不会把 10k 行 DOM 一次性插入；完整虚拟滚动在 S5-FE-02 迁移。
   */
  return <PagedTable columns={columns} rows={rows} rowKey={rowKey} />;
}

function PagedTable<T>({
  columns,
  rows,
  rowKey,
}: {
  columns: Array<{ key: string; header: React.ReactNode; width?: string; align?: "left" | "right"; render: (row: T) => React.ReactNode }>;
  rows: T[];
  rowKey: (row: T, index: number) => string;
}) {
  const pageSize = 100;
  const [page, setPage] = useState(0);
  const pageCount = Math.max(1, Math.ceil(rows.length / pageSize));
  const current = Math.min(page, pageCount - 1);
  useEffect(() => setPage(0), [rows]);
  const slice = rows.slice(current * pageSize, current * pageSize + pageSize);

  return (
    <>
      <div className="table-scroll">
        <table className="table">
          <thead>
            <tr>
              {columns.map((c) => (
                <th key={c.key} style={{ width: c.width, textAlign: c.align ?? "left" }}>
                  {c.header}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {slice.map((row, index) => (
              <tr key={rowKey(row, current * pageSize + index)}>
                {columns.map((c) => (
                  <td key={c.key} style={{ textAlign: c.align ?? "left" }}>
                    {c.render(row)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {pageCount > 1 ? (
        <div className="toolbar" style={{ padding: "var(--sp-3) var(--sp-4)", justifyContent: "space-between" }}>
          <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>
            共 {rows.length} 行 · 第 {current + 1} / {pageCount} 页
          </span>
          <span className="toolbar">
            <button type="button" className="btn btn--sm" disabled={current === 0} onClick={() => setPage(current - 1)}>
              上一页
            </button>
            <button type="button" className="btn btn--sm" disabled={current >= pageCount - 1} onClick={() => setPage(current + 1)}>
              下一页
            </button>
          </span>
        </div>
      ) : null}
    </>
  );
}

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
