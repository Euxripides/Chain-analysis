import React from "react";
import { ProTable } from "@ant-design/pro-components";
import { Card, Empty, Skeleton, Typography } from "antd";
import { cx } from "../utils";

/**
 * L1 普通业务表格（用户端重构规范 V4 §17.1 / §17.2 / §1.3）
 *
 * 规范把表格分三层，这一层是给**普通业务表**用的（案件 / 调查 / 报告 / 导出）：
 * 行数有限、列数适中、需要列状态与密度这类"后台成熟能力"。
 * L2 高密度详情表继续用共享的 DataTable，L3 超大交易表用 AG Grid。
 *
 * 为什么必须封装而不是让页面直接用 ProTable（§1.3 原文）：
 *   「把 ProComponents 封装到用户端专属 UI 层；页面只使用语义化业务组件，
 *     不直接堆 ProComponents 参数」
 * 因此本组件的 API 与 DataTable 同构（columns / rows / rowKey / loading / error…），
 * 页面迁移时只换 import，不需要理解任何 ProComponents 的概念。
 *
 * 三个刻意的收敛：
 *   1. **关掉 ProTable 自带的 search**：筛选必须走页面自己的 FilterBar，
 *      因为规范 §33 明确禁止伪造服务端不支持的筛选参数，
 *      而 ProTable 的 search 会按列名自动生成查询表单——那正是"看起来能筛、其实没生效"的来源；
 *   2. **保留移动端卡片形态**：宽表在 390px 上横向滚动比卡片更难用（既有行为，不因换组件而丢）；
 *   3. **刷新不清屏**：与 §17.5 的 DataTable 语义一致（keepPreviousData）。
 */

export interface CsProTableColumn<T> {
  key: string;
  header: React.ReactNode;
  width?: number | string;
  align?: "left" | "right";
  render: (row: T) => React.ReactNode;
  /** 冻结首列（§17.2 frozen primary identity column）。 */
  frozen?: boolean;
}

export interface CsProTableProps<T> {
  columns: Array<CsProTableColumn<T>>;
  rows: T[];
  rowKey: (row: T, index: number) => string;
  loading?: boolean;
  error?: string | null;
  onRetry?: () => void;
  empty?: React.ReactNode;
  caption?: string;
  /** 行激活只负责"选中/预览"；跳转仍由行内明确链接承担（§17.3）。 */
  onRowActivate?: (row: T) => void;
  rowActivateLabel?: (row: T) => string;
  /** 刷新已有数据时保留旧行（§17.5）。 */
  keepPreviousData?: boolean;
  /** <900px 的卡片形态；不传则退化成可横向滚动的表格。 */
  mobileCard?: (row: T) => React.ReactNode;
  pageSize?: number;
  className?: string;
}

export function CsProTable<T extends object>({
  columns,
  rows,
  rowKey,
  loading,
  error,
  onRetry,
  empty,
  caption,
  onRowActivate,
  rowActivateLabel,
  keepPreviousData = true,
  mobileCard,
  pageSize = 20,
  className,
}: CsProTableProps<T>) {
  /* 首次加载才用骨架；刷新时保留旧行（与 DataTable 同一语义，§17.5）。 */
  const firstLoad = Boolean(loading) && (!keepPreviousData || rows.length === 0);

  const isNarrow = useNarrowViewport();

  if (firstLoad) {
    return (
      <Card bordered={false} className={cx("cs-protable", className)}>
        <Skeleton active paragraph={{ rows: 6 }} title={false} />
      </Card>
    );
  }

  if (error) {
    return (
      <Card bordered={false} className={cx("cs-protable", className)}>
        <div className="cs-protable__error" role="alert">
          <Typography.Text type="secondary">{error}</Typography.Text>
          {onRetry ? (
            <button type="button" className="btn btn--sm" onClick={onRetry}>
              重试
            </button>
          ) : null}
        </div>
      </Card>
    );
  }

  if (rows.length === 0) {
    return (
      <Card bordered={false} className={cx("cs-protable", className)}>
        {empty ?? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有符合条件的记录" />}
      </Card>
    );
  }

  /* 移动端卡片形态（§17.2 的 mobile 分支）：与 DataTable 的 .cs-rowcards 一致。 */
  if (isNarrow && mobileCard) {
    return (
      <div className={cx("cs-protable", "cs-protable--cards", className)}>
        <div className="cs-rowcards">
          {rows.map((row, index) => (
            <React.Fragment key={rowKey(row, index)}>{mobileCard(row)}</React.Fragment>
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className={cx("cs-protable", className)} data-refreshing={loading ? "true" : "false"}>
      <ProTable<T>
        rowKey={(row) => rowKey(row as T, 0)}
        dataSource={rows}
        columns={columns.map((column) => ({
          title: column.header,
          dataIndex: column.key,
          key: column.key,
          width: column.width,
          align: column.align,
          fixed: column.frozen ? ("left" as const) : undefined,
          render: (_value: unknown, row: T) => column.render(row),
        }))}
        /* 筛选由页面的 FilterBar 负责：只暴露服务端真正支持的参数（§33）。 */
        search={false}
        /* 分页在客户端做：这一层的行数上限由接口决定（limit ≤ 200），不是 L3 超大表。 */
        pagination={{ pageSize, showSizeChanger: false, size: "small" }}
        options={{ density: true, setting: true, reload: false, fullScreen: false }}
        size="small"
        cardProps={{ bordered: false }}
        tableStyle={{ minWidth: 0 }}
        /* 行激活（§17.3）：行不上浮，只给键盘一个可聚焦、可 Enter 的入口。 */
        onRow={
          onRowActivate
            ? (row) => ({
                tabIndex: 0,
                "aria-label": rowActivateLabel ? rowActivateLabel(row) : undefined,
                onKeyDown: (event: React.KeyboardEvent) => {
                  if (event.key === "Enter") {
                    event.preventDefault();
                    onRowActivate(row);
                  }
                },
              })
            : undefined
        }
        tableAlertRender={false}
        scroll={{ x: "max-content" }}
      />
      {caption ? <span className="visually-hidden">{caption}</span> : null}
    </div>
  );
}

/** 与 responsive.css 中 .cs-bento / SplitPane 一致的单列断点（≤899px）。 */
function useNarrowViewport(): boolean {
  const [narrow, setNarrow] = React.useState(
    () => typeof window !== "undefined" && window.matchMedia("(max-width: 899px)").matches,
  );
  React.useEffect(() => {
    if (typeof window === "undefined") return;
    const query = window.matchMedia("(max-width: 899px)");
    const update = () => setNarrow(query.matches);
    update();
    query.addEventListener("change", update);
    return () => query.removeEventListener("change", update);
  }, []);
  return narrow;
}
