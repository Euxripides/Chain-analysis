import { useCallback, useEffect, useState } from "react";
import { ApiError, useAuth } from "@chain/api-client";
import { Card, DataTable, EntityLink, PageHeader, RelativeTime, StatusBadge, statusToneFor } from "@chain/ui";

interface CaseRow {
  id: string;
  case_no: string;
  name: string;
  description: string;
  status: string;
  color: string;
  created_at: string;
  updated_at: string;
}

/** §18.4 案件概览（列表部分）。案件详情与状态命令在 S4 补齐。 */
export default function CasesPage() {
  const { client } = useAuth();
  const [rows, setRows] = useState<CaseRow[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await client.get<CaseRow[]>("/api/v2/cases", { query: { limit: 200 } });
      setRows(res.data ?? []);
      setTotal(res.meta.total ?? res.data?.length ?? 0);
    } catch (err) {
      setError(err instanceof ApiError ? err.userMessage : "无法加载案件列表");
    } finally {
      setLoading(false);
    }
  }, [client]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <>
      <PageHeader
        title="案件中心"
        subtitle={`当前工作区共 ${total} 个案件 · 数据源 postgres:cases`}
        actions={
          <button type="button" className="btn btn--sm" disabled title="案件创建在 S4 开放">
            新建案件（S4）
          </button>
        }
      />
      <Card flush>
        <DataTable<CaseRow>
          loading={loading}
          error={error}
          onRetry={load}
          rows={rows}
          rowKey={(row) => row.id}
          columns={[
            { key: "no", header: "案件编号", width: "160px", render: (r) => <span className="mono">{r.case_no || "—"}</span> },
            {
              key: "name",
              header: "名称",
              render: (r) => (
                <span>
                  <strong>{r.name}</strong>
                  {r.description ? <div style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>{r.description}</div> : null}
                </span>
              ),
            },
            { key: "status", header: "状态", width: "120px", render: (r) => <StatusBadge tone={statusToneFor(r.status)}>{r.status}</StatusBadge> },
            { key: "id", header: "案件 ID", width: "190px", render: (r) => <EntityLink value={r.id} /> },
            { key: "updated", header: "最近更新", width: "140px", render: (r) => <RelativeTime value={r.updated_at} /> },
          ]}
        />
      </Card>
    </>
  );
}
