import { useCallback } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useAuth } from "@chain/api-client";
import { Card, EmptyState, ErrorState, PageHeader, SkeletonRows, StatusBadge, statusToneFor, useDialog, dialogInputValue } from "@chain/ui";
import { EntityLink, EvidenceRail } from "@chain/ui";
import { formatTime, useAction, useAsync } from "../sprint4/common";

interface Approval {
  id: string;
  report_revision: number;
  reviewer_id?: string | null;
  decision: string;
  content_hash: string;
  note: string;
  created_at: string;
}
interface ReportDetail {
  id: string;
  investigation_id: string;
  case_id?: string | null;
  case_no?: string;
  revision: number;
  format: string;
  content_hash: string;
  artifact_sha256: string;
  size_bytes: number;
  status: string;
  status_revision: number;
  generated_at: string;
  submitted_at?: string | null;
  approved_at?: string | null;
  rejected_at?: string | null;
  reject_reason: string;
  signed_at?: string | null;
  signature_hash: string;
  archived_at?: string | null;
  downloadable: boolean;
  approvals: Approval[];
  metadata: Record<string, unknown>;
}

/** 报告详情：审批状态机 + 一次性下载票据。artifact revision 不可覆盖。 */
export default function ReportDetailPage() {
  const { id = "" } = useParams();
  const { can } = useAuth();
  const dialog = useDialog();
  const action = useAction();
  const navigate = useNavigate();
  const { data, loading, error, reload } = useAsync<ReportDetail>(`/api/v2/reports/${id}`, [id]);

  const command = useCallback(
    async (cmd: string) => {
      if (!data) return;
      let note = "";
      let reviewer = "";
      if (cmd === "reject" || cmd === "approve") {
        const raw = await dialog.prompt({
          title: cmd === "reject" ? "驳回原因" : "审批意见（可留空）",
          input: { label: "意见", pattern: cmd === "reject" ? /^.{2,}$/ : /^.*$/ },
        });
        if (raw === "" || raw === null) return;
        note = raw.startsWith("input:") ? raw.slice("input:".length).trim() : "";
        if (cmd === "reject" && !note) return;
      }
      if (cmd === "submit") {
        const raw = await dialog.prompt({
          title: "指定审批人（可留空）",
          body: "留空时通知本组织全部拥有 report:review 权限的成员。",
          input: { label: "Reviewer identity ID", placeholder: "uuid 或留空", pattern: /^$|^[0-9a-f-]{36}$/ },
        });
        if (raw === "" || raw === null) return;
        reviewer = raw.startsWith("input:") ? raw.slice("input:".length).trim() : "";
      }
      const answer = await dialog.confirm({
        kind: "confirm",
        title: `确认执行 ${cmd}？`,
        details: [
          { label: "当前状态", value: data.status },
          { label: "报告 revision", value: `r${data.revision}` },
          { label: "status_revision", value: `r${data.status_revision}` },
          { label: "内容哈希", value: data.content_hash },
          ...(note ? [{ label: "意见", value: note }] : []),
          ...(reviewer ? [{ label: "审批人", value: reviewer }] : []),
        ],
      });
      if (answer !== "confirm") return;
      await action.run(
        () => action.client.post(`/api/v2/reports/${id}/commands/${cmd}`, { note, reason: note, reviewer_id: reviewer }, { ifMatch: String(data.status_revision) }),
        { successTitle: "报告状态已更新", onDone: reload },
      );
    },
    [action, data, dialog, id, reload],
  );

  const download = useCallback(async () => {
    const res = await action.run(() => action.client.post<{ url: string; expires_in_seconds: number; content_hash: string }>(`/api/v2/reports/${id}/download-ticket`, {}), {
      successTitle: "下载票据已签发",
      successBody: "票据 60 秒内有效且只能使用一次。",
      onDone: (r) => window.open(r.data.url, "_blank"),
    });
    if (!res) return;
  }, [action, id]);

  if (loading) return <SkeletonRows rows={6} columns={4} />;
  if (error || !data) return <Card><ErrorState title="报告加载失败" message={error ?? "不存在或无权访问"} onRetry={reload} /></Card>;

  return (
    <>
      <PageHeader
        title={`报告 revision ${data.revision}`}
        subtitle={<span>案件 {data.case_no || data.case_id || "—"} · {data.status} · 数据源 postgres:investigation_reports + MinIO</span>}
        actions={
          <span className="s4-button-row">
            <button type="button" className="btn btn--sm" onClick={() => navigate(-1)}>返回</button>
            {can("report:create") && ["generated", "rejected"].includes(data.status) ? <button type="button" className="btn btn--primary btn--sm" onClick={() => command("submit")}>提交复核</button> : null}
            {can("report:review") && data.status === "in_review" ? <button type="button" className="btn btn--primary btn--sm" onClick={() => command("approve")}>批准</button> : null}
            {can("report:review") && data.status === "in_review" ? <button type="button" className="btn btn--danger btn--sm" onClick={() => command("reject")}>驳回</button> : null}
            {can("report:sign") && data.status === "approved" ? <button type="button" className="btn btn--primary btn--sm" onClick={() => command("sign")}>签署</button> : null}
            {can("report:review") && ["approved", "signed"].includes(data.status) ? <button type="button" className="btn btn--sm" onClick={() => command("archive")}>归档</button> : null}
            {can("report:download") && data.downloadable ? <button type="button" className="btn btn--sm" onClick={download}>下载（票据）</button> : null}
          </span>
        }
      />
      <div className="s4-grid">
        <Card title="审批状态">
          <dl className="s4-kv">
            <div className="s4-kv__row"><dt>状态</dt><dd><StatusBadge tone={statusToneFor(data.status)}>{data.status}</StatusBadge></dd></div>
            <div className="s4-kv__row"><dt>artifact SHA-256</dt><dd><EntityLink value={data.artifact_sha256} /></dd></div>
            <div className="s4-kv__row"><dt>大小</dt><dd>{data.size_bytes} bytes</dd></div>
            <div className="s4-kv__row"><dt>生成时间</dt><dd>{formatTime(data.generated_at)}</dd></div>
            <div className="s4-kv__row"><dt>提交时间</dt><dd>{formatTime(data.submitted_at)}</dd></div>
            <div className="s4-kv__row"><dt>批准时间</dt><dd>{formatTime(data.approved_at)}</dd></div>
            <div className="s4-kv__row"><dt>驳回原因</dt><dd>{data.reject_reason || "—"}</dd></div>
            <div className="s4-kv__row"><dt>签署时间</dt><dd>{formatTime(data.signed_at)}</dd></div>
            <div className="s4-kv__row"><dt>签名哈希</dt><dd className="mono">{data.signature_hash || "—"}</dd></div>
            <div className="s4-kv__row"><dt>归档时间</dt><dd>{formatTime(data.archived_at)}</dd></div>
          </dl>
        </Card>
        <EvidenceRail
          title="Report metadata"
          items={Object.entries(data.metadata ?? {}).map(([k, v]) => ({ label: k, value: <span className="mono">{typeof v === "object" ? JSON.stringify(v) : String(v)}</span> }))}
        />
      </div>
      <Card title="审批记录">
        {data.approvals.length === 0 ? <EmptyState title="尚无审批记录" /> : null}
        <div className="s4-list">
          {data.approvals.map((a) => (
            <div className="s4-list__item" key={a.id}>
              <div className="s4-list__main">
                <div className="s4-list__title">{a.decision} · revision {a.report_revision}</div>
                <div className="s4-list__sub">reviewer {a.reviewer_id ?? "—"} · {formatTime(a.created_at)} · {a.note || "无意见"}</div>
              </div>
            </div>
          ))}
        </div>
      </Card>
      <div className="s4-sticky-actions">
        <Link className="btn" to={`/investigations/${data.investigation_id}/workbench`}>调查工作台</Link>
        {data.case_id ? <Link className="btn" to={`/cases/${data.case_id}/reports`}>案件报告</Link> : null}
      </div>
    </>
  );
}
