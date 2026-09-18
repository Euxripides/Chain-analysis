import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  Button,
  Card,
  DataTable,
  EmptyState,
  ErrorState,
  FilterBar,
  FilterChip,
  HashValue,
  MetaLine,
  PageHeader,
  PageSection,
  RelativeTime,
  SearchInput,
  SkeletonRows,
  StatusBadgeForState,
} from "@chain/ui";
import { useAsync } from "../sprint4/common";

/**
 * §12.4 案件中心 /cases
 *
 * 接口、权限与请求参数完全不变：`GET /api/v2/cases`（case:list）继续只接受
 * status / q / cursor（见 backend casework.ListCases 签名），因此筛选栏只暴露接口
 * 真正支持的状态与关键字，**不伪造"更新时间 / 负责人"等服务端不存在的筛选参数**。
 *
 * 列表响应（casework.Case）只包含案件自身字段：列表查询不聚合对象数、调查数与成员数，
 * 所以行内不展示这些计数；聚合值只由详情接口 `GET /api/v2/cases/{id}` 返回。
 */
interface CaseRow {
  id: string;
  case_no: string;
  name: string;
  description: string;
  status: string;
  classification: string;
  priority: string;
  revision: number;
  created_at: string;
  updated_at: string;
}

/** 桌面列宽：骨架屏复用同一组宽度，避免加载完成时列宽跳动（蓝图 §8.4）。 */
const COLUMN_WIDTHS = ["12%", "26%", "10%", "10%", "9%", "8%", "15%", "10%"];

/** 状态选项与接口接受的 status 值一一对应，标签保持原样。 */
const STATUS_OPTIONS = [
  { value: "all", label: "全部状态" },
  { value: "draft", label: "draft" },
  { value: "active", label: "active" },
  { value: "under_review", label: "under_review" },
  { value: "closed", label: "closed" },
  { value: "archived", label: "archived" },
];

export default function CasesPage() {
  const navigate = useNavigate();
  const [status, setStatus] = useState("all");
  const [q, setQ] = useState("");
  const [appliedQ, setAppliedQ] = useState("");
  const { data, loading, error, reload } = useAsync<CaseRow[]>("/api/v2/cases", [status, appliedQ], {
    limit: 200,
    status: status === "all" ? undefined : status,
    q: appliedQ || undefined,
  });
  const rows = data ?? [];
  const narrow = useIsNarrow();
  const hasFilter = status !== "all" || appliedQ !== "";

  const href = (id: string) => `/app-v2/cases/${id}/overview`;
  const open = (id: string) => navigate(`/cases/${id}/overview`);
  const clearFilters = () => {
    setStatus("all");
    setQ("");
    setAppliedQ("");
  };

  return (
    <>
      <PageHeader
        title="案件中心"
        breadcrumb={[{ label: "工作台", to: "/home" }, { label: "案件中心" }]}
        meta={<MetaLine items={[{ label: "数据源", value: "postgres:cases" }]} />}
        primaryAction={
          <Button variant="primary" size="sm" onClick={() => navigate("/cases/new")}>
            新建案件
          </Button>
        }
        secondaryAction={
          <Button size="sm" onClick={() => void reload()} busy={loading} busyLabel="刷新中…">
            刷新
          </Button>
        }
      />

      <PageSection title="案件列表" count={`${rows.length} 个`}>
        <FilterBar ariaLabel="案件筛选" onClear={clearFilters} showClear={hasFilter}>
          <select
            className="select"
            aria-label="状态筛选"
            value={status}
            onChange={(event) => setStatus(event.target.value)}
          >
            {STATUS_OPTIONS.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
          {/* 搜索框在筛选栏里占据剩余宽度；span 作为 flex 项撑开，内部块级 .cs-searchfield 随之铺满。 */}
          <span style={{ flex: "1 1 220px", minWidth: 0 }}>
            <SearchInput
              value={q}
              onChange={setQ}
              onSubmit={setAppliedQ}
              label="搜索案件"
              placeholder="名称 / 编号"
            />
          </span>
          {/* 保留原有的"搜索"按钮：与 Enter 提交走同一条路径，鼠标/触摸用户不必依赖键盘。 */}
          <Button size="sm" onClick={() => setAppliedQ(q.trim())}>
            搜索
          </Button>
          {/* 已应用的关键字筛选：让"当前为什么只有这些结果"可见，并且可以单独移除（§11.4）。 */}
          {appliedQ ? (
            <FilterChip
              onRemove={() => {
                setAppliedQ("");
                setQ("");
              }}
              removeLabel="清除关键字筛选"
            >
              {"关键字：" + appliedQ}
            </FilterChip>
          ) : null}
        </FilterBar>

        <Card flush>
          {loading ? <SkeletonRows rows={6} columns={8} widths={COLUMN_WIDTHS} /> : null}
          {!loading && error ? <ErrorState title="案件加载失败" message={error} onRetry={reload} /> : null}
          {!loading && !error && rows.length === 0 ? (
            <EmptyState
              title="没有符合条件的案件"
              body={
                hasFilter
                  ? "当前筛选条件会保留，方便你修改而不是重新输入。"
                  : "创建案件后即可在这里跟踪对象、调查与报告。"
              }
              action={
                hasFilter ? (
                  <Button size="sm" onClick={clearFilters}>
                    清除筛选
                  </Button>
                ) : null
              }
            />
          ) : null}

          {!loading && !error && rows.length > 0 && narrow ? (
            <div className="cs-rowcards">
              {rows.map((row) => (
                <div className="cs-rowcard" key={row.id}>
                  <div className="cs-rowcard__head">
                    <a
                      className="cs-rowcard__title"
                      href={href(row.id)}
                      onClick={(event) => {
                        event.preventDefault();
                        open(row.id);
                      }}
                    >
                      {row.name}
                    </a>
                    <StatusBadgeForState value={row.status} />
                  </div>
                  <div className="cs-rowcard__meta">
                    <span className="mono">{row.case_no || "—"}</span>
                    <span>密级 {row.classification || "—"}</span>
                    <span>优先级 {row.priority || "—"}</span>
                    <span className="mono">{"r" + row.revision}</span>
                    <RelativeTime value={row.updated_at} />
                  </div>
                  {row.description ? <div className="cs-rowcard__meta">{row.description}</div> : null}
                  <div className="cs-rowcard__actions">
                    <Button size="sm" aria-label={"打开案件 " + row.name} onClick={() => open(row.id)}>
                      打开案件
                    </Button>
                    <HashValue value={row.id} label="案件 ID" />
                  </div>
                </div>
              ))}
            </div>
          ) : null}

          {!loading && !error && rows.length > 0 && !narrow ? (
            <DataTable<CaseRow>
              rows={rows}
              rowKey={(row) => row.id}
              caption="案件列表"
              onRowActivate={(row) => open(row.id)}
              rowActivateLabel={(row) => `打开案件 ${row.name}`}
              columns={[
                {
                  key: "no",
                  header: "案件编号",
                  width: "150px",
                  frozen: true,
                  render: (row) => <span className="mono">{row.case_no || "—"}</span>,
                },
                {
                  key: "name",
                  header: "名称",
                  render: (row) => (
                    <span>
                      <a
                        href={href(row.id)}
                        onClick={(event) => {
                          event.preventDefault();
                          event.stopPropagation();
                          open(row.id);
                        }}
                      >
                        <strong>{row.name}</strong>
                      </a>
                      {row.description ? <div className="s4-list__sub">{row.description}</div> : null}
                    </span>
                  ),
                },
                { key: "status", header: "状态", width: "120px", render: (row) => <StatusBadgeForState value={row.status} /> },
                { key: "class", header: "密级", width: "110px", render: (row) => <span>{row.classification || "—"}</span> },
                { key: "priority", header: "优先级", width: "100px", render: (row) => <span>{row.priority || "—"}</span> },
                { key: "revision", header: "revision", width: "90px", render: (row) => <span className="mono">{"r" + row.revision}</span> },
                {
                  key: "id",
                  header: "案件 ID",
                  width: "190px",
                  /* 复制按钮不能顺带触发整行的 Enter/点击激活。 */
                  render: (row) => (
                    <span onClick={(event) => event.stopPropagation()}>
                      <HashValue value={row.id} label="案件 ID" />
                    </span>
                  ),
                },
                { key: "updated", header: "最近更新", width: "130px", render: (row) => <RelativeTime value={row.updated_at} /> },
              ]}
            />
          ) : null}
        </Card>
      </PageSection>
    </>
  );
}

/**
 * 蓝图 §13：<900px 把宽表格换成 `.cs-rowcards` 卡片列表。
 * 与 CanvasPage 的 `useIsMobile` 同一写法：媒体查询是展示层选择，不进入任何请求参数。
 */
function useIsNarrow(): boolean {
  const [narrow, setNarrow] = useState(
    () => typeof window !== "undefined" && window.matchMedia("(max-width: 899px)").matches,
  );
  useEffect(() => {
    if (typeof window === "undefined") return;
    const query = window.matchMedia("(max-width: 899px)");
    const update = () => setNarrow(query.matches);
    update();
    query.addEventListener("change", update);
    return () => query.removeEventListener("change", update);
  }, []);
  return narrow;
}
