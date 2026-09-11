import { useCallback, useEffect, useState } from "react";
import { ApiError, useAuth, type LoginEventRow } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  EntityLink,
  Field,
  PageHeader,
  RelativeTime,
  StatusBadge,
  statusToneFor,
  useDialog,
} from "@chain/ui";

/** §21.6 登录记录（A-LOGIN-01）。成功与失败都可见，可按结果与 IP 检索。 */
export default function LoginEventsPage() {
  const { client } = useAuth();
  const dialog = useDialog();
  const [rows, setRows] = useState<LoginEventRow[]>([]);
  const [total, setTotal] = useState(0);
  const [result, setResult] = useState("");
  const [clientIP, setClientIP] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await client.get<LoginEventRow[]>("/api/admin/v1/login-events", {
        query: { limit: 200, result, client_ip: clientIP },
      });
      setRows(res.data ?? []);
      setTotal(res.meta.total ?? res.data?.length ?? 0);
    } catch (err) {
      setError(err instanceof ApiError ? err.userMessage : "无法加载登录记录");
    } finally {
      setLoading(false);
    }
  }, [client, result, clientIP]);

  useEffect(() => {
    void load();
  }, [load]);

  const showDetail = async (row: LoginEventRow) => {
    await dialog.open({
      kind: row.result === "success" ? "info" : "warning",
      title: row.event_type,
      body: null,
      details: [
        { label: "时间（UTC）", value: new Date(row.occurred_at).toISOString() },
        { label: "结果", value: <StatusBadge tone={statusToneFor(row.result)}>{row.result}</StatusBadge> },
        { label: "原因码", value: <span className="mono">{row.reason_code || "—"}</span> },
        { label: "风险等级", value: row.risk_level },
        { label: "账号（掩码）", value: row.subject_masked || "—" },
        { label: "受众 / 认证方式", value: `${row.audience} / ${row.auth_method}` },
        { label: "设备声明", value: `${row.device_label || "未知"} · ${row.browser_name || "—"} · ${row.os_name || "—"}` },
        { label: "新设备", value: row.is_new_device ? "是" : "否" },
        { label: "直连 IP（peer）", value: <span className="mono">{row.peer_ip || "—"}</span> },
        { label: "解析后客户端 IP", value: <span className="mono">{row.client_ip || "—"}</span> },
        { label: "入口域名", value: row.entry_host || "—" },
        { label: "会话 ID", value: <span className="mono">{row.session_id || "—"}</span> },
        { label: "Request ID", value: <span className="mono">{row.request_id || "—"}</span> },
        { label: "链序号", value: String(row.seq) },
        { label: "事件哈希", value: <span className="mono" style={{ fontSize: "var(--fs-11)" }}>{row.event_hash}</span> },
        { label: "前序哈希", value: <span className="mono" style={{ fontSize: "var(--fs-11)" }}>{row.prev_hash}</span> },
      ],
      actions: [{ label: "关闭", variant: "primary", result: "ok" }],
    });
  };

  return (
    <>
      <PageHeader
        title="登录记录"
        subtitle={`共 ${total} 条 · 数据源 postgres:login_events（append-only 哈希链）`}
        actions={
          <button className="btn btn--sm" onClick={load}>
            刷新
          </button>
        }
      />
      <Card>
        <div className="toolbar">
          <Field label="结果" htmlFor="result">
            <select id="result" className="select" value={result} onChange={(e) => setResult(e.target.value)}>
              <option value="">全部</option>
              <option value="success">成功</option>
              <option value="failure">失败</option>
              <option value="denied">拒绝</option>
              <option value="blocked">阻断</option>
            </select>
          </Field>
          <Field label="客户端 IP" htmlFor="client_ip">
            <input id="client_ip" className="input" value={clientIP} onChange={(e) => setClientIP(e.target.value)} placeholder="精确匹配" />
          </Field>
          <button className="btn btn--primary btn--sm" style={{ alignSelf: "flex-end" }} onClick={load}>
            查询
          </button>
        </div>
      </Card>
      <Card flush>
        <DataTable<LoginEventRow>
          loading={loading}
          error={error}
          onRetry={load}
          rows={rows}
          rowKey={(r) => r.id}
          empty={<EmptyState title="没有匹配的登录记录" />}
          columns={[
            { key: "time", header: "时间", width: "140px", render: (r) => <RelativeTime value={r.occurred_at} /> },
            { key: "event", header: "事件", render: (r) => <span className="mono">{r.event_type}</span> },
            { key: "result", header: "结果", width: "110px", render: (r) => <StatusBadge tone={statusToneFor(r.result)}>{r.result}</StatusBadge> },
            { key: "reason", header: "原因码", width: "150px", render: (r) => <span className="mono">{r.reason_code || "—"}</span> },
            { key: "subject", header: "账号（掩码）", width: "200px", render: (r) => <span className="mono">{r.subject_masked || "—"}</span> },
            { key: "ip", header: "客户端 IP", width: "150px", render: (r) => <span className="mono">{r.client_ip || "—"}</span> },
            {
              key: "device",
              header: "设备声明",
              render: (r) => `${r.browser_name || "—"} / ${r.os_name || "—"}${r.is_new_device ? "（新）" : ""}`,
            },
            { key: "hash", header: "事件哈希", width: "150px", render: (r) => <EntityLink value={r.event_hash} /> },
            {
              key: "actions",
              header: "",
              width: "80px",
              align: "right",
              render: (r) => (
                <button className="btn btn--sm" onClick={() => void showDetail(r)}>
                  详情
                </button>
              ),
            },
          ]}
        />
      </Card>
    </>
  );
}
