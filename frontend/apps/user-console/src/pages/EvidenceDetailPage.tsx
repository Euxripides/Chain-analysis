import { useCallback } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useAuth } from "@chain/api-client";
import { Card, ErrorState, PageHeader, SkeletonRows, StatusBadge, statusToneFor, useDialog, dialogInputValue } from "@chain/ui";
import { EntityLink, EvidenceRail } from "@chain/ui";
import { formatTime, useAction, useAsync } from "../sprint4/common";

interface Evidence {
  id: string;
  investigation_id: string;
  evidence_type: string;
  title: string;
  status: string;
  nature: string;
  source_refs: string[];
  content_hash: string;
  object_ref?: string;
  provenance: Record<string, unknown>;
  version: number;
  revision: number;
  supersedes_id?: string | null;
  superseded_by?: string | null;
  verified_by?: string | null;
  verified_at?: string | null;
  created_at: string;
}

/** §18.1 U-INV-05：Evidence 详情。内容不可覆盖，修正通过 supersede 生成新版本。 */
export default function EvidenceDetailPage() {
  const { id = "", evidenceId = "" } = useParams();
  const { can } = useAuth();
  const dialog = useDialog();
  const action = useAction();
  const navigate = useNavigate();
  const includeSensitive = can("evidence:sensitive:read") ? "?include=sensitive" : "";
  const { data, loading, error, reload } = useAsync<Evidence>(`/api/v2/evidence/${evidenceId}${includeSensitive}`, [evidenceId]);

  const command = useCallback(
    async (cmd: "verify" | "dispute" | "supersede") => {
      let note = "";
      if (cmd === "dispute") {
        const input = dialogInputValue(await dialog.prompt({ title: "质疑 Evidence", input: { label: "原因", pattern: /^.{2,}$/ } }));
        if (!input) return;
        note = input;
      }
      const answer = await dialog.confirm({
        kind: "confirm",
        title: `确认执行 ${cmd}？`,
        details: [
          { label: "Evidence", value: data?.title ?? "" },
          { label: "当前状态", value: data?.status ?? "" },
          { label: "内容哈希", value: data?.content_hash ?? "" },
          ...(note ? [{ label: "原因", value: note }] : []),
        ],
      });
      if (answer !== "confirm") return;
      await action.run(() => action.client.post(`/api/v2/evidence/${evidenceId}/commands/${cmd}`, { note, reason: note }), {
        successTitle: "Evidence 状态已更新",
        onDone: reload,
      });
    },
    [action, data, dialog, evidenceId, reload],
  );

  if (loading) return <SkeletonRows rows={6} columns={4} />;
  if (error || !data) return <Card><ErrorState title="Evidence 加载失败" message={error ?? "不存在或无权访问"} onRetry={reload} /></Card>;

  return (
    <>
      <PageHeader
        title={data.title}
        subtitle={<span className="mono">{data.evidence_type} · v{data.version} · revision r{data.revision}</span>}
        actions={
          <span className="s4-button-row">
            <button type="button" className="btn btn--sm" onClick={() => navigate(`/investigations/${id}/findings`)}>返回 Findings</button>
            {can("evidence:review") ? (
              <>
                <button type="button" className="btn btn--sm" disabled={data.status === "verified"} onClick={() => command("verify")}>Verify</button>
                <button type="button" className="btn btn--sm" disabled={["disputed", "superseded"].includes(data.status)} onClick={() => command("dispute")}>Dispute</button>
                <button type="button" className="btn btn--danger btn--sm" disabled={data.status === "superseded"} onClick={() => command("supersede")}>Supersede</button>
              </>
            ) : null}
          </span>
        }
      />
      <div className="s4-grid">
        <Card title="证据元数据">
          <dl className="s4-kv">
            <div className="s4-kv__row"><dt>状态</dt><dd><StatusBadge tone={statusToneFor(data.status)}>{data.status}</StatusBadge></dd></div>
            <div className="s4-kv__row"><dt>nature</dt><dd>{data.nature}</dd></div>
            <div className="s4-kv__row"><dt>内容 SHA-256</dt><dd><EntityLink value={data.content_hash} /></dd></div>
            <div className="s4-kv__row"><dt>对象引用</dt><dd className="mono">{data.object_ref || "（未授予敏感读取权限，仅显示摘要）"}</dd></div>
            <div className="s4-kv__row"><dt>采集时间</dt><dd>{formatTime(data.created_at)}</dd></div>
            <div className="s4-kv__row"><dt>verified by</dt><dd>{data.verified_by ?? "—"}</dd></div>
            <div className="s4-kv__row"><dt>verified at</dt><dd>{formatTime(data.verified_at)}</dd></div>
            <div className="s4-kv__row"><dt>supersedes</dt><dd>{data.supersedes_id ? <EntityLink value={data.supersedes_id} /> : "—"}</dd></div>
            <div className="s4-kv__row"><dt>superseded by</dt><dd>{data.superseded_by ? <EntityLink value={data.superseded_by} /> : "—"}</dd></div>
          </dl>
          <h3 className="card__title" style={{ marginTop: 16 }}>source_refs</h3>
          <ul className="mono" style={{ fontSize: "var(--fs-12)", overflowWrap: "anywhere" }}>
            {data.source_refs.map((ref) => (
              <li key={ref}>{ref}</li>
            ))}
          </ul>
        </Card>
        <EvidenceRail
          title="Provenance"
          items={Object.entries(data.provenance ?? {}).map(([key, value]) => ({
            label: key,
            value: <span className="mono">{typeof value === "object" ? JSON.stringify(value) : String(value)}</span>,
          }))}
        />
      </div>
      <div className="s4-sticky-actions">
        <Link className="btn" to={`/investigations/${data.investigation_id}/findings`}>查看相关 Findings</Link>
      </div>
    </>
  );
}
