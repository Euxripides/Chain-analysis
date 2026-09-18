import React, { useState } from "react";
import { ChevronDown, ChevronRight, Info, Table2 } from "lucide-react";
import { useDialog } from "./Dialog";
import { cx } from "./layout";
import { EmptyState } from "./primitives";

/**
 * 数据展示组件补充（蓝图 §8.3）
 *
 * TreeTable / ChartPanel / LoadMore。
 */

export interface TreeTableColumn<T> {
  key: string;
  header: React.ReactNode;
  width?: string;
  align?: "left" | "right";
  render: (row: T) => React.ReactNode;
}

export interface TreeTableRow<T> {
  key: string;
  data: T;
  children?: Array<TreeTableRow<T>>;
  /** 关键失败路径默认展开：失败必须先被看见（蓝图 §12.16）。 */
  defaultExpanded?: boolean;
}

function hasFailure<T>(row: TreeTableRow<T>): boolean {
  return Boolean((row.children ?? []).some((child) => child.defaultExpanded || hasFailure(child)));
}

/**
 * 树形表格：地址 → Dataset → Shard、调用树（§8.3 TreeTable）。
 *
 * 与 `DataTable` 同样遵守"行点击只负责选择/预览，详情用明确链接或 Enter"的原则。
 */
export function TreeTable<T>({
  columns,
  rows,
  ariaLabel,
  emptyTitle = "没有可展开的记录",
  emptyBody,
  onRowActivate,
}: {
  columns: Array<TreeTableColumn<T>>;
  rows: Array<TreeTableRow<T>>;
  ariaLabel: string;
  emptyTitle?: string;
  emptyBody?: React.ReactNode;
  onRowActivate?: (row: T) => void;
}) {
  if (rows.length === 0) return <EmptyState title={emptyTitle} body={emptyBody} />;
  return (
    <div className="table-scroll">
      <table className="table table--tree" aria-label={ariaLabel}>
        <thead>
          <tr>
            <th scope="col" style={{ width: 40 }}>
              <span className="visually-hidden">展开</span>
            </th>
            {columns.map((column) => (
              <th key={column.key} scope="col" style={{ width: column.width, textAlign: column.align ?? "left" }}>
                {column.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <TreeBranch key={row.key} row={row} columns={columns} depth={0} onRowActivate={onRowActivate} />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function TreeBranch<T>({
  row,
  columns,
  depth,
  onRowActivate,
}: {
  row: TreeTableRow<T>;
  columns: Array<TreeTableColumn<T>>;
  depth: number;
  onRowActivate?: (row: T) => void;
}) {
  const children = row.children ?? [];
  const [expanded, setExpanded] = useState(Boolean(row.defaultExpanded ?? hasFailure(row)));
  return (
    <>
      <tr
        data-depth={depth}
        className={onRowActivate ? "table__row--activatable" : undefined}
        tabIndex={onRowActivate ? 0 : undefined}
        onKeyDown={
          onRowActivate
            ? (event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  onRowActivate(row.data);
                }
              }
            : undefined
        }
      >
        <td style={{ textAlign: "center" }}>
          {children.length > 0 ? (
            <button
              type="button"
              className="cs-hier__toggle"
              aria-expanded={expanded}
              aria-label={expanded ? "折叠该行" : "展开该行"}
              onClick={() => setExpanded((value) => !value)}
            >
              {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
            </button>
          ) : null}
        </td>
        {columns.map((column, index) => (
          <td key={column.key} style={{ textAlign: column.align ?? "left" }}>
            {index === 0 ? (
              <span className="table__treecell" style={{ paddingLeft: depth * 16 }}>
                {column.render(row.data)}
              </span>
            ) : (
              column.render(row.data)
            )}
          </td>
        ))}
      </tr>
      {expanded
        ? children.map((child) => (
            <TreeBranch key={child.key} row={child} columns={columns} depth={depth + 1} onRowActivate={onRowActivate} />
          ))
        : null}
    </>
  );
}

/**
 * 图表容器（§8.3 ChartPanel）
 *
 * 职责边界：标题、legend、tooltip、空状态、数据时间、以及**无障碍替代通道**。
 * 它**不**负责画图——ECharts / XYFlow 实例由调用方按路由懒加载后传入 `children`，
 * 保证重型依赖不进入公共首屏包（§15）。
 *
 * §14 要求"图表提供文字摘要或对应数据表入口"，因此这里强制提供两者之一：
 * 传 `summary`（可读文字结论）或 `tableView`（对应的数据表节点）。
 */
export function ChartPanel({
  title,
  subtitle,
  actions,
  legend,
  dataTime,
  summary,
  tableView,
  onExplain,
  empty,
  emptyBody,
  children,
  className,
}: {
  title: React.ReactNode;
  subtitle?: React.ReactNode;
  actions?: React.ReactNode;
  legend?: React.ReactNode;
  /** 数据时间：图表必须说明自己画的是哪一刻的数据（§8.3）。 */
  dataTime?: React.ReactNode;
  /** 文字摘要（无障碍替代）。 */
  summary?: React.ReactNode;
  /** 对应的数据表节点（无障碍替代）。 */
  tableView?: React.ReactNode;
  /** 计算方式说明；**必须走 Dialog**，不在这里渲染成说明段落。 */
  onExplain?: { title: string; body: React.ReactNode; details?: Array<{ label: string; value: React.ReactNode }> };
  empty?: boolean;
  emptyBody?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}) {
  const dialog = useDialog();
  const [showTable, setShowTable] = useState(false);
  return (
    <section className={cx("card", "cs-chartpanel", className)} aria-label={typeof title === "string" ? title : undefined}>
      <header className="card__head">
        <h2 className="card__title">{title}</h2>
        {subtitle ? <span className="cs-section__count">{subtitle}</span> : null}
        <span style={{ flex: 1 }} />
        {legend}
        {tableView ? (
          <button
            type="button"
            className="btn btn--sm btn--ghost"
            aria-pressed={showTable}
            onClick={() => setShowTable((value) => !value)}
          >
            <Table2 size={14} />
            {showTable ? "返回图表" : "查看数据表"}
          </button>
        ) : null}
        {onExplain ? (
          <button
            type="button"
            className="cs-iconbtn"
            style={{ width: 30, height: 30 }}
            aria-label="图表计算方式说明"
            title="图表计算方式说明"
            onClick={() => dialog.notify("info", onExplain.title, onExplain.body, onExplain.details)}
          >
            <Info size={15} />
          </button>
        ) : null}
        {actions}
      </header>
      <div className="card__body">
        {empty ? (
          <EmptyState title="当前筛选条件下没有可绘制的数据" body={emptyBody ?? "调整筛选条件后重试。"} />
        ) : showTable && tableView ? (
          <div className="cs-chart-frame">{tableView}</div>
        ) : (
          <>
            {summary ? <p className="cs-chartpanel__summary">{summary}</p> : null}
            <div className="cs-chart-frame">{children}</div>
          </>
        )}
        {dataTime ? <div className="cs-chartpanel__time">数据时间 {dataTime}</div> : null}
      </div>
    </section>
  );
}

/**
 * cursor 分页的"加载更多"（§8.2）。
 * 只负责触发；**由调用方提供 next_cursor**，组件不猜后端协议。
 */
export function LoadMore({
  hasMore,
  busy,
  onLoad,
  count,
  label = "加载更多",
  doneLabel = "已加载全部",
}: {
  hasMore: boolean;
  busy?: boolean;
  onLoad: () => void;
  count?: number;
  label?: string;
  doneLabel?: string;
}) {
  return (
    <div className="cs-loadmore">
      {typeof count === "number" ? <span className="cs-section__count">已加载 {count} 条</span> : null}
      {hasMore ? (
        <button type="button" className="btn btn--sm" onClick={onLoad} disabled={busy} aria-busy={busy || undefined}>
          {busy ? "加载中…" : label}
        </button>
      ) : (
        <span className="cs-section__count">{doneLabel}</span>
      )}
    </div>
  );
}

/** 统计条：任务中心顶部"运行 / 排队 / 失败 / 完成"等，数值必须来自接口可证明的数据。 */
export interface StatTile {
  key: string;
  label: string;
  value: React.ReactNode;
  tone?: "neutral" | "ok" | "warn" | "danger" | "info";
  onClick?: () => void;
  active?: boolean;
}

export function StatTiles({ tiles, ariaLabel }: { tiles: StatTile[]; ariaLabel: string }) {
  return (
    <div className="cs-stattiles" role="group" aria-label={ariaLabel}>
      {tiles.map((tile) => {
        const Tag = tile.onClick ? "button" : "div";
        return (
          <Tag
            key={tile.key}
            className="cs-stattile"
            data-tone={tile.tone ?? "neutral"}
            data-active={tile.active ? "true" : undefined}
            {...(tile.onClick ? { type: "button" as const, onClick: tile.onClick, "aria-pressed": Boolean(tile.active) } : {})}
          >
            <span className="cs-stattile__label">{tile.label}</span>
            <span className="cs-stattile__value num">{tile.value}</span>
          </Tag>
        );
      })}
    </div>
  );
}
