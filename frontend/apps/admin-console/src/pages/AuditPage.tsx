import { useCallback, useEffect, useState } from "react";
import { ApiError, useAuth, type AuditEventRow, type ChainStatus, type VerifyChainResult } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  EntityLink,
  EvidenceRail,
  Field,
  Metric,
  PageHeader,
  RelativeTime,
  StatusBadge,
  statusToneFor,
  useDialog,
} from "@chain/ui";
import type { StatusTone } from "@chain/ui";

/** §19.8 审计中心（A-AUDIT-01/02）+ §27 AUDIT-004 哈希链校验。 */
export default function AuditPage() {
  const { client, can } = useAuth();
  const dialog = useDialog();
  const [rows, setRows] = useState<AuditEventRow[]>([]);
  const [total, setTotal] = useState(0);
  const [chains, setChains] = useState<ChainStatus[]>([]);
  const [action, setAction] = useState("");
  const [category, setCategory] = useState("");
  const [result, setResult] = useState("");
  const [traceId, setTraceId] = useState("");
  const [loading, setLoading] = useState(true);
  const [verifying, setVerifying] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [events, chainRes] = await Promise.all([
        client.get<AuditEventRow[]>("/api/admin/v1/audit/events", {
          query: { limit: 200, action, category, result, trace_id: traceId },
        }),
        can("admin:audit:verify") ? client.get<{ chains: ChainStatus[] }>("/api/admin/v1/audit/chains") : Promise.resolve({ data: { chains: [] } }),
      ]);
      setRows(events.data ?? []);
      setTotal(events.meta.total ?? events.data?.length ?? 0);
      setChains(chainRes.data?.chains ?? []);
    } catch (err) {
      setError(err instanceof ApiError ? err.userMessage : "无法加载审计日志");
    } finally {
      setLoading(false);
    }
  }, [client, action, category, result, traceId, can]);

  useEffect(() => {
    void load();
  }, [load]);

  const showDetail = async (row: AuditEventRow) => {
    try {
      const res = await client.get<{ event: AuditEventRow; before: Record<string, unknown>; after: Record<string, unknown> }>(
        `/api/admin/v1/audit/events/${row.id}`,
      );
      const ev = res.data.event;
      await dialog.open({
        kind: ev.result === "deny" || ev.result === "failure" || ev.result === "error" ? "warning" : "info",
        title: ev.action,
        body: null,
        details: [
          { label: "时间（UTC）", value: new Date(ev.occurred_at).toISOString() },
          { label: "结果", value: <StatusBadge tone={statusToneFor(ev.result) as StatusTone}>{ev.result}</StatusBadge> },
          { label: "HTTP", value: `${ev.http_method || "—"} ${ev.http_status || ""}` },
          { label: "路由", value: <span className="mono">{ev.route_template || "—"}</span> },
          { label: "操作者", value: ev.actor_email || ev.actor_id || "—" },
          { label: "操作者角色", value: (ev.actor_roles ?? []).join(", ") || "—" },
          { label: "组织 / 工作区", value: `${ev.organization_id || "—"} / ${ev.workspace_id || "—"}` },
          { label: "会话", value: <span className="mono">{ev.session_id || "—"}</span> },
          { label: "资源", value: `${ev.resource_type || "—"} ${ev.resource_id || ""}` },
          { label: "权限键", value: <span className="mono">{ev.permission_key || "—"}</span> },
          { label: "策略判定", value: ev.deny_reason ? `${ev.policy_decision} (${ev.deny_reason})` : ev.policy_decision || "—" },
          { label: "风险等级", value: ev.risk_level },
          { label: "敏感读取", value: ev.sensitive ? "是" : "否" },
          { label: "客户端 IP", value: <span className="mono">{ev.client_ip || "—"}</span> },
          { label: "Request ID", value: <span className="mono">{ev.request_id || "—"}</span> },
          { label: "Trace ID", value: <span className="mono">{ev.trace_id || "—"}</span> },
          { label: "链序号", value: String(ev.seq) },
          { label: "事件哈希", value: <span className="mono" style={{ fontSize: "var(--fs-11)" }}>{ev.event_hash}</span> },
        ],
        actions: [{ label: "关闭", variant: "primary", result: "ok" }],
      });
    } catch (err) {
      dialog.notify("error", "读取审计详情失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    }
  };

  const verify = async () => {
    const answer = await dialog.confirm({
      kind: "confirm",
      title: "校验审计哈希链？",
      body: "系统会从头重算 audit 链的事件哈希，检测插入、删除或改写。数据量大时耗时较长。",
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "开始校验", result: "ok", variant: "primary" },
      ],
    });
    if (answer !== "ok") return;
    setVerifying(true);
    try {
      const res = await client.post<VerifyChainResult>("/api/admin/v1/audit/verify", { chain: "audit" });
      const data = res.data;
      if (data.valid) {
        dialog.notify("success", "哈希链完整", null, [
          { label: "校验条数", value: String(data.checked) },
          { label: "区间", value: `${data.from_seq} → ${data.to_seq}` },
          { label: "链头哈希", value: <span className="mono">{data.head_hash}</span> },
        ]);
      } else {
        dialog.notify("error", "检测到链损坏", null, [
          { label: "原因", value: data.reason },
          { label: "断裂序号", value: String(data.broken_at_seq ?? "—") },
          { label: "说明", value: data.errors.join("；") },
        ]);
      }
      await load();
    } catch (err) {
      dialog.notify("error", "校验失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    } finally {
      setVerifying(false);
    }
  };

  return (
    <div className="workbench">
      <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: "var(--sp-5)" }}>
        <PageHeader
          title="审计日志"
          subtitle={`共 ${total} 条事件 · 数据源 postgres:audit_events_v2（append-only）`}
          actions={
            <>
              <button className="btn btn--sm" onClick={load}>
                刷新
              </button>
              <button className="btn btn--sm" disabled={!can("admin:audit:verify") || verifying} onClick={verify}>
                {verifying ? "校验中…" : "校验哈希链"}
              </button>
            </>
          }
        />

        {chains.length > 0 ? (
          <div className="metric-grid">
            {chains.map((chain) => (
              <Metric key={chain.chain} label={`${chain.chain} 链`} value={chain.last_seq} hint={`head ${chain.last_hash.slice(0, 16)}…`} />
            ))}
          </div>
        ) : null}

        <Card>
          <div className="toolbar">
            <Field label="动作" htmlFor="action">
              <input id="action" className="input" value={action} onChange={(e) => setAction(e.target.value)} placeholder="精确匹配，如 case:read" />
            </Field>
            <Field label="分类" htmlFor="category">
              <input id="category" className="input" value={category} onChange={(e) => setCategory(e.target.value)} placeholder="如 admin.security" />
            </Field>
            <Field label="结果" htmlFor="resultFilter">
              <select id="resultFilter" className="select" value={result} onChange={(e) => setResult(e.target.value)}>
                <option value="">全部</option>
                <option value="success">success</option>
                <option value="deny">deny</option>
                <option value="failure">failure</option>
                <option value="error">error</option>
              </select>
            </Field>
            <Field label="Trace ID" htmlFor="trace">
              <input id="trace" className="input" value={traceId} onChange={(e) => setTraceId(e.target.value)} placeholder="按 Trace 串联" />
            </Field>
            <button className="btn btn--primary btn--sm" style={{ alignSelf: "flex-end" }} onClick={load}>
              查询
            </button>
          </div>
        </Card>

        <Card flush>
          <DataTable<AuditEventRow>
            loading={loading}
            error={error}
            onRetry={load}
            rows={rows}
            rowKey={(r) => r.id}
            empty={<EmptyState title="没有匹配的审计事件" />}
            columns={[
              { key: "time", header: "时间", width: "140px", render: (r) => <RelativeTime value={r.occurred_at} /> },
              { key: "action", header: "动作", render: (r) => <span className="mono">{r.action}</span> },
              { key: "category", header: "分类", width: "150px", render: (r) => <span className="mono">{r.category}</span> },
              { key: "actor", header: "操作者", width: "200px", render: (r) => <span title={r.actor_id}>{r.actor_email || r.actor_type}</span> },
              {
                key: "result",
                header: "结果",
                width: "120px",
                render: (r) => (
                  <span>
                    <StatusBadge tone={statusToneFor(r.result) as StatusTone}>{r.result}</StatusBadge>
                    {r.sensitive ? <span className="badge badge--warn" style={{ marginLeft: 4 }}>敏感</span> : null}
                  </span>
                ),
              },
              { key: "risk", header: "风险", width: "90px", render: (r) => <StatusBadge tone={statusToneFor(r.risk_level.toUpperCase()) as StatusTone}>{r.risk_level}</StatusBadge> },
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
      </div>

      <EvidenceRail
        title="审计链状态"
        items={[
          { label: "写入端", value: "audit_events_v2" },
          { label: "链头数量", value: String(chains.length) },
          ...chains.flatMap((c) => [
            { label: `${c.chain} 序号`, value: String(c.last_seq) },
            { label: `${c.chain} 链头`, value: <span className="mono" style={{ fontSize: "var(--fs-11)" }}>{c.last_hash || "（空）"}</span> },
          ]),
          { label: "不可抵赖", value: "应用账号无 UPDATE/DELETE 权限（见 0042 迁移说明）" },
          { label: "每日锚点", value: "audit_chain_anchors（S6/S7 接入 MinIO 对象锁）" },
        ]}
      />
    </div>
  );
}
