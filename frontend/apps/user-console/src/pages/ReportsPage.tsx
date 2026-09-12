import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Card, DataTable, PageHeader, RelativeTime, StatusBadge, statusToneFor } from "@chain/ui";
import { useAsync } from "../sprint4/common";

interface ReportRow {
  id: string;
  investigation_id: string;
  case_id?: string | null;
  case_no?: string;
  revision: number;
  status: string;
  content_hash: string;
  size_bytes: number;
  generated_at: string;
  approved_at?: string | null;
  signed_at?: string | null;
}

export default function ReportsPage() {
  const navigate = useNavigate();
  const [status, setStatus] = useState("all");
  const { data, loading, error, reload } = useAsync<ReportRow[]>("/api/v2/reports", [status], {
    limit: 200,
    status: status === "all" ? undefined : status,
  });
  return (
    <>
      <PageHeader title="报告中心" subtitle={`${(data ?? []).length} 份报告 revision · 数据源 postgres:investigation_reports`} />
      <Card
        flush
        actions={
          <select className="select" aria-label="状态筛选" value={status} onChange={(e) => setStatus(e.target.value)}>
            <option value="all">全部状态</option>
            <option value="generated">generated</option>
            <option value="in_review">in_review</option>
            <option value="approved">approved</option>
            <option value="rejected">rejected</option>
            <option value="signed">signed</option>
            <option value="archived">archived</option>
          </select>
        }
      >
        <DataTable<ReportRow>
          loading={loading}
          error={error}
          onRetry={reload}
          rows={data ?? []}
          rowKey={(r) => r.id}
          columns={[
            {
              key: "title",
              header: "报告",
              render: (r) => (
                <a
                  href={`/app-v2/reports/${r.id}`}
                  onClick={(e) => {
                    e.preventDefault();
                    navigate(`/reports/${r.id}`);
                  }}
                >
                  revision {r.revision}
                  <div className="s4-list__sub">案件 {r.case_no || r.case_id || "—"} · 调查 {r.investigation_id.slice(0, 8)}…</div>
                </a>
              ),
            },
            { key: "status", header: "状态", width: "130px", render: (r) => <StatusBadge tone={statusToneFor(r.status)}>{r.status}</StatusBadge> },
            { key: "hash", header: "SHA-256", width: "180px", render: (r) => <span className="mono">{r.content_hash.slice(0, 16)}…</span> },
            { key: "size", header: "大小", width: "90px", render: (r) => `${r.size_bytes} B` },
            { key: "generated", header: "生成时间", width: "140px", render: (r) => <RelativeTime value={r.generated_at} /> },
            { key: "signed", header: "签署", width: "140px", render: (r) => <RelativeTime value={r.signed_at} /> },
          ]}
        />
      </Card>
    </>
  );
}
