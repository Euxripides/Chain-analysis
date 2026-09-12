import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Card, DataTable, EntityLink, PageHeader, RelativeTime, StatusBadge, statusToneFor } from "@chain/ui";
import { useAsync } from "../sprint4/common";

interface CaseRow {
  id: string;
  case_no: string;
  name: string;
  description: string;
  status: string;
  classification: string;
  priority: string;
  revision: number;
  updated_at: string;
  member_count?: number;
  open_findings?: number;
}

/** §18.1 U-CASE-01：案件中心。筛选/排序条件进入 URL 查询，数据全部来自 /api/v2/cases。 */
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

  return (
    <>
      <PageHeader
        title="案件中心"
        subtitle={`共 ${rows.length} 个案件 · 数据源 postgres:cases`}
        actions={
          <button type="button" className="btn btn--primary btn--sm" onClick={() => navigate("/cases/new")}>
            新建案件
          </button>
        }
      />
      <Card
        flush
        actions={
          <span className="toolbar">
            <select className="select" aria-label="状态筛选" value={status} onChange={(e) => setStatus(e.target.value)}>
              <option value="all">全部状态</option>
              <option value="draft">draft</option>
              <option value="active">active</option>
              <option value="under_review">under_review</option>
              <option value="closed">closed</option>
              <option value="archived">archived</option>
            </select>
            <input
              className="input"
              aria-label="搜索案件"
              placeholder="名称 / 编号"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") setAppliedQ(q.trim());
              }}
            />
            <button type="button" className="btn btn--sm" onClick={() => setAppliedQ(q.trim())}>
              搜索
            </button>
          </span>
        }
      >
        <DataTable<CaseRow>
          loading={loading}
          error={error}
          onRetry={reload}
          rows={rows}
          rowKey={(row) => row.id}
          columns={[
            { key: "no", header: "案件编号", width: "150px", render: (r) => <span className="mono">{r.case_no || "—"}</span> },
            {
              key: "name",
              header: "名称",
              render: (r) => (
                <span>
                  <a
                    href={`/app-v2/cases/${r.id}/overview`}
                    onClick={(e) => {
                      e.preventDefault();
                      navigate(`/cases/${r.id}/overview`);
                    }}
                  >
                    <strong>{r.name}</strong>
                  </a>
                  {r.description ? <div className="s4-list__sub">{r.description}</div> : null}
                </span>
              ),
            },
            { key: "status", header: "状态", width: "110px", render: (r) => <StatusBadge tone={statusToneFor(r.status)}>{r.status}</StatusBadge> },
            { key: "class", header: "密级", width: "110px", render: (r) => <span>{r.classification || "—"}</span> },
            { key: "priority", header: "优先级", width: "100px", render: (r) => <span>{r.priority || "—"}</span> },
            { key: "revision", header: "revision", width: "90px", render: (r) => <span className="mono">r{r.revision}</span> },
            { key: "id", header: "案件 ID", width: "190px", render: (r) => <EntityLink value={r.id} /> },
            { key: "updated", header: "最近更新", width: "130px", render: (r) => <RelativeTime value={r.updated_at} /> },
          ]}
        />
      </Card>
    </>
  );
}
