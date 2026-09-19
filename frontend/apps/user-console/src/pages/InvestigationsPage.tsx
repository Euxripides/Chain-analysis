import { useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  Button,
  CompactProgress,
  EmptyState,
  FilterBar,
  HashValue,
  MetaLine,
  PageSection,
  RelativeTime,
  StatusBadgeForState,
  progressStateFor,
} from "@chain/ui";
import { CsPageHeader, CsRefreshBar } from "@chain/user-ui";
/* 子路径导入：让 ProComponents 只落在这个懒加载路由的 chunk 里（§56.1）。 */
import { CsProTable } from "@chain/user-ui/pro-table";
import { useAsync } from "../sprint4/common";

/**
 * §12.7 调查任务 /investigations
 *
 * 接口与请求参数不变：`GET /api/v2/investigations`（investigation:list）只接受
 * status / case_id / cursor（见 backend casework.ListInvestigationsV2 签名），
 * 因此顶部只提供接口支持的状态筛选，**不伪造"模式 / 关键字"参数**。
 *
 * 进度：列表响应（InvestigationView）不包含任何进度或当前步骤字段，
 * 所以 RUNNING / WAITING_* 行使用 indeterminate 的 CompactProgress（percent={null}），
 * 只表达"仍在运行"，绝不拿已完成数除以猜测总量伪造百分比（蓝图 §9.1）。
 */
interface InvestigationRow {
  id: string;
  case_id?: string | null;
  case_no?: string;
  goal: string;
  mode: string;
  status: string;
  subject: Record<string, unknown>;
  revision: number;
  finding_count: number;
  evidence_count: number;
  pending_findings: number;
  created_at: string;
  updated_at: string;
}

/** 状态选项与接口接受的 status 值一一对应，标签保持原样。 */
const STATUS_OPTIONS = [
  { value: "all", label: "全部状态" },
  { value: "created", label: "created" },
  { value: "running", label: "running" },
  { value: "waiting_data", label: "waiting_data" },
  { value: "paused", label: "paused" },
  { value: "waiting_review", label: "waiting_review" },
  { value: "completed", label: "completed" },
  { value: "failed", label: "failed" },
  { value: "cancelled", label: "cancelled" },
];

/** WAITING_* 必须说明在等什么（蓝图 §9.2）：映射自服务端状态名，不推断额外信息。 */
const WAITING_FOR: Record<string, string> = {
  waiting_data: "数据拉取",
  waiting_review: "人工复核",
};

function subjectText(subject: Record<string, unknown> | null | undefined, key: string): string {
  const value = subject ? subject[key] : undefined;
  return typeof value === "string" ? value : "";
}

export default function InvestigationsPage() {
  const navigate = useNavigate();
  const [status, setStatus] = useState("all");
  const { data, loading, error, reload } = useAsync<InvestigationRow[]>("/api/v2/investigations", [status], {
    limit: 200,
    status: status === "all" ? undefined : status,
  });
  const rows = data ?? [];

  return (
    <>
      <CsPageHeader
        title="调查任务"
        breadcrumb={[{ label: "工作台", to: "/home" }, { label: "调查任务" }]}
        meta={<MetaLine items={[{ label: "数据源", value: "postgres:investigations" }]} />}
        primaryAction={
          <Button variant="primary" size="sm" onClick={() => navigate("/investigations/new")}>
            发起调查
          </Button>
        }
        secondaryAction={
          <Button size="sm" onClick={() => void reload()} busy={loading} busyLabel="刷新中…">
            刷新
          </Button>
        }
      />

      <PageSection title="调查列表" count={`${rows.length} 个`}>
        <FilterBar
          ariaLabel="调查筛选"
          onClear={() => setStatus("all")}
          showClear={status !== "all"}
        >
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
        </FilterBar>

        <div style={{ position: "relative", minWidth: 0 }}>
          <CsRefreshBar active={loading && rows.length > 0} />
          <CsProTable<InvestigationRow>
            loading={loading}
            /* §17.5：刷新保留旧行，不清屏、不丢表头。 */
            keepPreviousData
            error={error}
            onRetry={reload}
            rows={rows}
            rowKey={(row) => row.id}
            caption="调查任务列表"
            onRowActivate={(row) => navigate(`/investigations/${row.id}/workbench`)}
            rowActivateLabel={(row) => `打开调查工作台 ${row.goal}`}
            empty={
              <EmptyState
                title="没有符合条件的调查"
                action={
                  status !== "all" ? (
                    <Button size="sm" onClick={() => setStatus("all")}>
                      清除筛选
                    </Button>
                  ) : null
                }
              />
            }
            columns={[
              {
                key: "goal",
                header: "调查目标",
                frozen: true,
                render: (row) => (
                  <a
                    href={`/app-v2/investigations/${row.id}/workbench`}
                    onClick={(event) => {
                      event.preventDefault();
                      event.stopPropagation();
                      navigate(`/investigations/${row.id}/workbench`);
                    }}
                  >
                    <strong>{row.goal}</strong>
                  </a>
                ),
              },
              {
                key: "case",
                header: "关联案件",
                width: "150px",
                render: (row) =>
                  row.case_id ? (
                    <a
                      href={`/app-v2/cases/${row.case_id}/overview`}
                      onClick={(event) => {
                        event.preventDefault();
                        event.stopPropagation();
                        navigate(`/cases/${row.case_id}/overview`);
                      }}
                    >
                      {row.case_no || row.case_id.slice(0, 8)}
                    </a>
                  ) : (
                    <span>—</span>
                  ),
              },
              {
                key: "subject",
                header: "Subject",
                width: "220px",
                render: (row) => (
                  /* 复制按钮不能顺带触发整行的 Enter/点击激活。 */
                  <span
                    style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}
                    onClick={(event) => event.stopPropagation()}
                  >
                    <span className="s4-list__sub mono">{subjectText(row.subject, "chain") || "—"}</span>
                    <HashValue value={subjectText(row.subject, "value")} label="Subject 值" />
                  </span>
                ),
              },
              { key: "mode", header: "模式", width: "110px", render: (row) => <span className="mono">{row.mode || "—"}</span> },
              {
                key: "status",
                header: "状态",
                width: "170px",
                render: (row) => {
                  const state = progressStateFor(row.status);
                  /* 只有"仍在推进"的状态才画不确定性进度条；其余状态只显示状态本身。 */
                  if (state === "running" || state === "waiting") {
                    return <CompactProgress percent={null} state={state} waitingFor={WAITING_FOR[(row.status || "").toLowerCase()]} />;
                  }
                  return <StatusBadgeForState value={row.status} />;
                },
              },
              {
                key: "findings",
                header: "Findings",
                width: "140px",
                render: (row) => (
                  <span>
                    {row.finding_count}
                    <span className="cs-section__count">{" 待复核 " + row.pending_findings}</span>
                  </span>
                ),
              },
              { key: "evidence", header: "Evidence", width: "100px", render: (row) => <span>{row.evidence_count}</span> },
              { key: "revision", header: "revision", width: "90px", render: (row) => <span className="mono">{"r" + row.revision}</span> },
              { key: "updated", header: "最近更新", width: "140px", render: (row) => <RelativeTime value={row.updated_at} /> },
              {
                key: "id",
                header: "调查 ID",
                width: "180px",
                render: (row) => (
                  <span onClick={(event) => event.stopPropagation()}>
                    <HashValue value={row.id} label="调查 ID" />
                  </span>
                ),
              },
            ]}
          />
        </div>
      </PageSection>
    </>
  );
}
