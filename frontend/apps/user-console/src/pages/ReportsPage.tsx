import {
  useMemo,
  useState } from "react";
import { Link,
  useNavigate } from "react-router-dom";
import { useAuth } from "@chain/api-client";
import {
  Button,
  Card,
  DataTable,
  DateTimeRange,
  FilterBar,
  HashValue,
  MetaLine,
  PageSection,
  RelativeTime,
  StatusBadge,
  useDialog,
  type StatusTone,
} from "@chain/ui";
import { CsPageHeader } from "@chain/user-ui";
import { useAsync } from "../sprint4/common";

/**
 * §12.22 报告中心
 *
 * 数据全部来自 `GET /api/v2/reports`（服务端作用域查询，按 generated_at 倒序，最多 200 条）：
 * 名称用 `revision` 表达（服务端没有单独的 name 字段，**不编造标题**），
 * 案件用 `case_no`/`case_id`，调查用 `investigation_id`，创建者用服务端真实提供的
 * `submitted_by`（服务端未返回 created_by，因此列名就叫"提交人"）。
 *
 * 状态、案件筛选走接口原有查询参数；生成时间范围是**对已加载结果的视图筛选**，
 * 因此不在页面上写说明段落，而是通过"口径说明"Dialog 明示（AGENTS.md 唯一提示通道）。
 */

/** 服务端 ReportView 在列表接口上返回的字段子集（与 casework.ListReports 的 SELECT 一致）。 */
interface ReportRow {
  id: string;
  investigation_id: string;
  case_id?: string | null;
  case_no?: string;
  revision: number;
  format: string;
  content_hash: string;
  size_bytes: number;
  status: string;
  status_revision: number;
  generated_at: string;
  submitted_at?: string | null;
  submitted_by?: string | null;
  approved_at?: string | null;
  approved_by?: string | null;
  rejected_at?: string | null;
  reject_reason: string;
  signed_at?: string | null;
  signature_hash?: string;
  archived_at?: string | null;
}

interface CaseOption {
  id: string;
  case_no: string;
  name: string;
}

/** 服务端状态 → 中文标签。标签只是可读名，原始状态值始终保留在 title 与详情页上。 */
const REPORT_STATUS_LABEL: Record<string, string> = {
  generated: "草稿",
  in_review: "待审批",
  approved: "已批准",
  rejected: "已驳回",
  signed: "已签署",
  archived: "已归档",
};

const REPORT_STATUS_TONE: Record<string, StatusTone> = {
  generated: "neutral",
  in_review: "warn",
  approved: "ok",
  rejected: "danger",
  signed: "ok",
  archived: "neutral",
};

/**
 * 徽标文字：中文标签 + 服务端原始值。
 *
 * 两者都保留是有意的——原始值是接口/审计里的权威写法（也是既有 E2E 断言的值），
 * 中文标签只是可读名；只显示中文会让界面与接口对不上。
 */
function statusText(status: string): string {
  const label = REPORT_STATUS_LABEL[status];
  return label ? `${label} · ${status}` : status;
}

function statusTone(status: string): StatusTone {
  return REPORT_STATUS_TONE[status] ?? "neutral";
}

/**
 * 当前状态对应的时间戳：**逐个状态直取服务端字段**，不做"取最大时间"之类的推算。
 * 对应字段缺失时显示 —，不用别的字段顶替。
 */
function statusTime(report: ReportRow): string | null {
  switch (report.status) {
    case "in_review":
      return report.submitted_at ?? null;
    case "approved":
      return report.approved_at ?? null;
    case "rejected":
      return report.rejected_at ?? null;
    case "signed":
      return report.signed_at ?? null;
    case "archived":
      return report.archived_at ?? null;
    default:
      return report.generated_at;
  }
}

export default function ReportsPage() {
  const navigate = useNavigate();
  const dialog = useDialog();
  const { can } = useAuth();
  const [status, setStatus] = useState("all");
  const [caseId, setCaseId] = useState("all");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");

  /* 状态与案件是**服务端筛选参数**（接口原有能力），不新增任何参数名。 */
  const { data, loading, error, reload } = useAsync<ReportRow[]>("/api/v2/reports", [status, caseId], {
    limit: 200,
    status: status === "all" ? undefined : status,
    case_id: caseId === "all" ? undefined : caseId,
  });

  const loaded = data ?? [];
  const timeFiltered = useMemo(() => {
    const list = data ?? [];
    const fromMs = from ? new Date(from).getTime() : null;
    const toMs = to ? new Date(to).getTime() : null;
    if (fromMs === null && toMs === null) return list;
    return list.filter((row) => {
      const at = new Date(row.generated_at).getTime();
      if (Number.isNaN(at)) return false;
      if (fromMs !== null && at < fromMs) return false;
      if (toMs !== null && at > toMs) return false;
      return true;
    });
  }, [data, from, to]);

  const hasFilters = status !== "all" || caseId !== "all" || from !== "" || to !== "";
  const resetFilters = () => {
    setStatus("all");
    setCaseId("all");
    setFrom("");
    setTo("");
  };

  const openScope = () =>
    dialog.notify("info", "报告列表口径", null, [
      { label: "接口", value: "GET /api/v2/reports" },
      { label: "排序", value: "generated_at 倒序（服务端）" },
      { label: "返回上限", value: "200 条（服务端 limit）" },
      { label: "服务端筛选", value: "status、case_id" },
      { label: "视图筛选", value: "生成时间范围（在当前已加载结果内）" },
      { label: "状态标签", value: "草稿=generated、待审批=in_review、已批准=approved、已驳回=rejected、已签署=signed、已归档=archived" },
      { label: "提交人", value: "服务端 submitted_by；接口未返回 created_by，故不显示创建者" },
      { label: "状态时间", value: "按当前状态直取 submitted_at/approved_at/rejected_at/signed_at/archived_at/generated_at" },
    ]);

  return (
    <>
      <CsPageHeader
        title="报告中心"
        breadcrumb={[{ label: "工作台", to: "/home" }, { label: "报告中心" }]}
        meta={
          <MetaLine
            items={[
              { label: "报告", value: timeFiltered.length === loaded.length ? `${loaded.length} 份` : `${timeFiltered.length} / ${loaded.length} 份` },
              { label: "数据源", value: "postgres:investigation_reports" },
            ]}
          />
        }
        secondaryAction={
          <Button size="sm" onClick={openScope}>
            口径说明
          </Button>
        }
      />

      <PageSection title="报告列表" count={`${timeFiltered.length} 份`}>
        <FilterBar onClear={resetFilters} showClear={hasFilters}>
          <select
            className="select"
            aria-label="状态筛选"
            value={status}
            onChange={(event) => setStatus(event.target.value)}
          >
            <option value="all">全部状态</option>
            <option value="generated">generated</option>
            <option value="in_review">in_review</option>
            <option value="approved">approved</option>
            <option value="rejected">rejected</option>
            <option value="signed">signed</option>
            <option value="archived">archived</option>
          </select>
          {/* 没有 case:list 权限时整块不注册（不发用不上的请求），也不伪造案件选项。 */}
          {can("case:list") ? <CaseFilter value={caseId} onChange={setCaseId} /> : null}
          <DateTimeRange
            label="生成时间"
            from={from}
            to={to}
            onFromChange={setFrom}
            onToChange={setTo}
          />
        </FilterBar>

        <Card flush>
          <DataTable<ReportRow>
            caption="报告列表"
            loading={loading}
            /* §17.5：刷新保留旧行与表头，不清屏。 */
            keepPreviousData
            error={error}
            onRetry={reload}
            rows={timeFiltered}
            rowKey={(row) => row.id}
            onRowActivate={(row) => navigate(`/reports/${row.id}`)}
            rowActivateLabel={(row) => `打开报告 r${row.revision}`}
            emptyAction={
              hasFilters ? (
                <Button size="sm" onClick={resetFilters}>
                  清除筛选
                </Button>
              ) : undefined
            }
            columns={[
              {
                key: "report",
                header: "报告",
                frozen: true,
                render: (row) => (
                  <span style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
                    <Link to={`/reports/${row.id}`}>报告 r{row.revision}</Link>
                    <span style={{ color: "var(--text-secondary)", fontSize: "var(--fs-11)" }} title={row.investigation_id}>
                      调查 {row.investigation_id.slice(0, 8)}…
                    </span>
                  </span>
                ),
              },
              {
                key: "case",
                header: "案件",
                width: "150px",
                render: (row) =>
                  row.case_no || row.case_id ? (
                    <span className="mono" title={row.case_id ?? undefined}>
                      {row.case_no || row.case_id}
                    </span>
                  ) : (
                    "—"
                  ),
              },
              {
                key: "revision",
                header: "revision",
                width: "120px",
                render: (row) => (
                  <span className="mono">
                    r{row.revision}
                    <span style={{ color: "var(--text-secondary)" }}> · s{row.status_revision}</span>
                  </span>
                ),
              },
              {
                key: "status",
                header: "状态",
                width: "180px",
                render: (row) => (
                  <span title={row.status}>
                    <StatusBadge tone={statusTone(row.status)}>{statusText(row.status)}</StatusBadge>
                  </span>
                ),
              },
              {
                key: "submitted_by",
                header: "提交人",
                width: "160px",
                render: (row) => <HashValue value={row.submitted_by ?? ""} label="提交人身份 ID" />,
              },
              {
                key: "status_time",
                header: "状态时间",
                width: "150px",
                render: (row) => <RelativeTime value={statusTime(row)} />,
              },
              {
                key: "hash",
                header: "SHA-256",
                width: "200px",
                render: (row) => <HashValue value={row.content_hash} head={10} tail={8} label="报告内容哈希" />,
              },
              {
                key: "size",
                header: "大小",
                width: "100px",
                align: "right",
                render: (row) => `${row.size_bytes} B`,
              },
            ]}
          />
        </Card>
      </PageSection>
    </>
  );
}

/**
 * 案件筛选：选项来自 `GET /api/v2/cases`（真实案件编号 + 名称），
 * 选中的值作为接口原有 `case_id` 参数提交给服务端。
 */
function CaseFilter({ value, onChange }: { value: string; onChange: (next: string) => void }) {
  const { data } = useAsync<CaseOption[]>("/api/v2/cases", [], { limit: 200 });
  const cases = data ?? [];
  return (
    <select className="select" aria-label="案件筛选" value={value} onChange={(event) => onChange(event.target.value)}>
      <option value="all">全部案件</option>
      {cases.map((item) => (
        <option key={item.id} value={item.id}>
          {[item.case_no, item.name].filter(Boolean).join(" ")}
        </option>
      ))}
    </select>
  );
}
