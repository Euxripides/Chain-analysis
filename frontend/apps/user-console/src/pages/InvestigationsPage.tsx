import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Card, DataTable, EntityLink, PageHeader, RelativeTime, StatusBadge, statusToneFor } from "@chain/ui";
import { useAsync } from "../sprint4/common";

interface InvestigationRow {
  id: string;
  case_id?: string | null;
  case_no?: string;
  goal: string;
  status: string;
  subject: Record<string, unknown>;
  revision: number;
  finding_count: number;
  evidence_count: number;
  pending_findings: number;
  updated_at: string;
}

export default function InvestigationsPage() {
  const navigate = useNavigate();
  const [status, setStatus] = useState("all");
  const { data, loading, error, reload } = useAsync<InvestigationRow[]>("/api/v2/investigations", [status], {
    limit: 200,
    status: status === "all" ? undefined : status,
  });
  return (
    <>
      <PageHeader
        title="调查任务"
        subtitle={`${(data ?? []).length} 个调查 · 数据源 postgres:investigations`}
        actions={
          <button type="button" className="btn btn--primary btn--sm" onClick={() => navigate("/investigations/new")}>
            发起调查
          </button>
        }
      />
      <Card
        flush
        actions={
          <select className="select" aria-label="状态筛选" value={status} onChange={(e) => setStatus(e.target.value)}>
            <option value="all">全部状态</option>
            <option value="created">created</option>
            <option value="running">running</option>
            <option value="waiting_data">waiting_data</option>
            <option value="paused">paused</option>
            <option value="waiting_review">waiting_review</option>
            <option value="completed">completed</option>
            <option value="failed">failed</option>
            <option value="cancelled">cancelled</option>
          </select>
        }
      >
        <DataTable<InvestigationRow>
          loading={loading}
          error={error}
          onRetry={reload}
          rows={data ?? []}
          rowKey={(r) => r.id}
          columns={[
            {
              key: "goal",
              header: "调查目的",
              render: (r) => (
                <a
                  href={`/app-v2/investigations/${r.id}/workbench`}
                  onClick={(e) => {
                    e.preventDefault();
                    navigate(`/investigations/${r.id}/workbench`);
                  }}
                >
                  <strong>{r.goal}</strong>
                  <div className="s4-list__sub mono">{String(r.subject?.chain ?? "")} / {String(r.subject?.value ?? "")}</div>
                </a>
              ),
            },
            { key: "case", header: "案件", width: "150px", render: (r) => (r.case_id ? <a href={`/app-v2/cases/${r.case_id}/overview`} onClick={(e) => { e.preventDefault(); navigate(`/cases/${r.case_id}/overview`); }}>{r.case_no || r.case_id.slice(0, 8)}</a> : "—") },
            { key: "status", header: "状态", width: "130px", render: (r) => <StatusBadge tone={statusToneFor(r.status)}>{r.status}</StatusBadge> },
            { key: "findings", header: "Findings", width: "120px", render: (r) => `${r.finding_count} / 待复核 ${r.pending_findings}` },
            { key: "evidence", header: "Evidence", width: "100px", render: (r) => r.evidence_count },
            { key: "revision", header: "revision", width: "90px", render: (r) => `r${r.revision}` },
            { key: "updated", header: "最近更新", width: "140px", render: (r) => <RelativeTime value={r.updated_at} /> },
            { key: "id", header: "调查 ID", width: "180px", render: (r) => <EntityLink value={r.id} /> },
          ]}
        />
      </Card>
    </>
  );
}
