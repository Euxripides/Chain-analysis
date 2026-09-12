import { useCallback, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useAuth } from "@chain/api-client";
import { Card, EmptyState, ErrorState, PageHeader, SkeletonRows, StatusBadge, statusToneFor, useDialog, dialogInputValue } from "@chain/ui";
import { formatTime, useAction, useAsync } from "../sprint4/common";

interface EvidenceRef {
  id: string;
  title: string;
  status: string;
  content_hash: string;
  evidence_type: string;
}
interface Finding {
  id: string;
  investigation_id: string;
  finding_type: string;
  title: string;
  summary: string;
  confidence: number;
  status: string;
  nature: string;
  algorithm: string;
  algorithm_version: string;
  revision: number;
  review_note: string;
  created_at: string;
  evidence: EvidenceRef[];
}

/** §18.1 U-INV-04：Findings 复核。支持 evidence verify 后再 confirm，算法字段只读。 */
export default function FindingsPage() {
  const { id = "" } = useParams();
  const { can } = useAuth();
  const dialog = useDialog();
  const action = useAction();
  const [status, setStatus] = useState("all");
  const { data, loading, error, reload } = useAsync<Finding[]>(`/api/v2/investigations/${id}/findings`, [id, status], {
    limit: 200,
    status: status === "all" ? undefined : status,
  });
  const [selected, setSelected] = useState<Finding | null>(null);
  const navigate = useNavigate();

  const verifyEvidence = useCallback(
    async (e: EvidenceRef) => {
      const answer = await dialog.confirm({
        kind: "confirm",
        title: "标记 Evidence 为 verified？",
        body: "verified 是确认 Finding 的前置条件；原证据内容不可覆盖。",
        details: [
          { label: "Evidence", value: e.title },
          { label: "类型", value: e.evidence_type },
          { label: "SHA-256", value: e.content_hash },
        ],
      });
      if (answer !== "confirm") return;
      await action.run(() => action.client.post(`/api/v2/evidence/${e.id}/commands/verify`, { note: "Sprint 4 复核通过" }), {
        successTitle: "Evidence 已 verified",
        onDone: async () => {
          const refreshed = await action.client.get<Finding[]>(`/api/v2/investigations/${id}/findings`, { query: { limit: 200 } });
          setSelected(refreshed.data.find((f) => f.id === selected?.id) ?? null);
          void reload();
        },
      });
    },
    [action, dialog, id, reload, selected],
  );

  const decide = useCallback(
    async (finding: Finding, command: "confirm" | "reject" | "supersede") => {
      let note = "";
      if (command === "reject" || command === "supersede") {
        const input = dialogInputValue(await dialog.prompt({ title: command === "reject" ? "驳回 Finding" : "替代 Finding", input: { label: "原因", pattern: /^.{2,}$/ } }));
        if (!input) return;
        note = input;
      }
      const verified = finding.evidence.filter((e) => e.status === "verified").length;
      if (command === "confirm" && verified === 0) {
        const exception = dialogInputValue(await dialog.prompt({
          title: "缺少 verified Evidence",
          body: "确认 Finding 至少需要一条 verified Evidence，或填写经审批的例外原因。",
          input: { label: "例外原因", pattern: /^.{4,}$/ },
        }));
        if (!exception) return;
        note = exception;
      }
      const answer = await dialog.confirm({
        kind: "confirm",
        title: `确认 ${command} Finding？`,
        details: [
          { label: "Finding", value: finding.title },
          { label: "当前状态", value: finding.status },
          { label: "Evidence", value: `${verified}/${finding.evidence.length} verified` },
          { label: "revision", value: `r${finding.revision}` },
          { label: "意见", value: note || "—" },
        ],
      });
      if (answer !== "confirm") return;
      await action.run(
        () => action.client.post(`/api/v2/findings/${finding.id}/commands/${command}`, { note, exception_reason: command === "confirm" && verified === 0 ? note : "" }, { ifMatch: String(finding.revision) }),
        {
          successTitle: "Finding 状态已更新",
          onDone: async () => {
            const refreshed = await action.client.get<Finding[]>(`/api/v2/investigations/${id}/findings`, { query: { limit: 200 } });
            setSelected(refreshed.data.find((f) => f.id === finding.id) ?? null);
            void reload();
          },
        },
      );
    },
    [action, dialog, id, reload],
  );

  return (
    <>
      <PageHeader
        title="Findings 复核"
        subtitle={`调查 ${id} · 数据源 postgres:investigation_findings`}
        actions={
          <span className="s4-button-row">
            <select className="select" aria-label="状态筛选" value={status} onChange={(e) => setStatus(e.target.value)}>
              <option value="all">全部状态</option>
              <option value="candidate">candidate</option>
              <option value="confirmed">confirmed</option>
              <option value="rejected">rejected</option>
              <option value="superseded">superseded</option>
            </select>
            <button type="button" className="btn btn--sm" onClick={() => navigate(`/investigations/${id}/workbench`)}>返回工作台</button>
          </span>
        }
      />
      <div className="s4-grid">
        <Card title="Finding 列表" flush>
          {loading ? <SkeletonRows rows={6} columns={4} /> : null}
          {error ? <ErrorState title="Findings 加载失败" message={error} onRetry={reload} /> : null}
          {!loading && !error && (data ?? []).length === 0 ? <EmptyState title="没有符合筛选条件的 Finding" /> : null}
          <div className="s4-list">
            {(data ?? []).map((f) => (
              <button
                key={f.id}
                type="button"
                data-testid={`finding-row-${f.id}`}
                className="s4-list__item"
                style={{ textAlign: "left", background: selected?.id === f.id ? "var(--c-cobalt-soft)" : "transparent", border: 0 }}
                onClick={() => setSelected(f)}
              >
                <div className="s4-list__main">
                  <div className="s4-list__title">{f.title}</div>
                  <div className="s4-list__sub">
                    {f.finding_type} · {f.nature} · confidence {(f.confidence * 100).toFixed(0)}% · {f.algorithm}@{f.algorithm_version}
                  </div>
                </div>
                <StatusBadge tone={statusToneFor(f.status)}>{f.status}</StatusBadge>
              </button>
            ))}
          </div>
        </Card>
        <div>
          {selected ? (
            <Card title="Finding 详情">
              <div className="s4-list__title">{selected.title}</div>
              <div className="s4-list__sub">{selected.summary}</div>
              <dl className="s4-kv" style={{ marginTop: 12 }}>
                <div className="s4-kv__row"><dt>状态</dt><dd><StatusBadge tone={statusToneFor(selected.status)}>{selected.status}</StatusBadge></dd></div>
                <div className="s4-kv__row"><dt>nature</dt><dd>{selected.nature}</dd></div>
                <div className="s4-kv__row"><dt>confidence</dt><dd>{(selected.confidence * 100).toFixed(1)}%</dd></div>
                <div className="s4-kv__row"><dt>算法</dt><dd>{selected.algorithm}@{selected.algorithm_version}</dd></div>
                <div className="s4-kv__row"><dt>revision</dt><dd>r{selected.revision}</dd></div>
                <div className="s4-kv__row"><dt>创建</dt><dd>{formatTime(selected.created_at)}</dd></div>
                <div className="s4-kv__row"><dt>复核意见</dt><dd>{selected.review_note || "—"}</dd></div>
              </dl>
              <h3 className="card__title" style={{ marginTop: 16 }}>引用 Evidence</h3>
              <div className="s4-list">
                {selected.evidence.length === 0 ? <EmptyState title="没有关联 Evidence" /> : null}
                {selected.evidence.slice(0, 8).map((e) => (
                  <div className="s4-list__item" key={e.id}>
                    <div className="s4-list__main">
                      <div className="s4-list__title">{e.title}</div>
                      <div className="s4-list__sub mono">{e.content_hash}</div>
                    </div>
                    <StatusBadge tone={statusToneFor(e.status)}>{e.status}</StatusBadge>
                    <span className="s4-list__actions">
                      <Link className="btn btn--sm" to={`/investigations/${selected.investigation_id}/evidence/${e.id}`}>详情</Link>
                      {can("evidence:review") && e.status !== "verified" ? (
                        <button type="button" data-testid={`finding-evidence-verify-${e.id}`} className="btn btn--sm" onClick={() => verifyEvidence(e)}>Verify</button>
                      ) : null}
                    </span>
                  </div>
                ))}
              </div>
              {selected.evidence.length > 8 ? (
                <button
                  type="button"
                  className="btn btn--sm"
                  onClick={() =>
                    dialog.notify(
                      "info",
                      `其余 ${selected.evidence.length - 8} 条 Evidence`,
                      null,
                      selected.evidence.slice(8).map((e) => ({ label: `${e.evidence_type} · ${e.status}`, value: e.title })),
                    )
                  }
                >
                  查看其余 {selected.evidence.length - 8} 条
                </button>
              ) : null}
              {can("finding:review") ? (
                <div className="s4-button-row" style={{ marginTop: 16 }}>
                  <button type="button" data-testid="finding-confirm" className="btn btn--primary btn--sm" disabled={["confirmed", "rejected", "superseded"].includes(selected.status)} onClick={() => decide(selected, "confirm")}>Confirm</button>
                  <button type="button" data-testid="finding-reject" className="btn btn--sm" disabled={["confirmed", "rejected", "superseded"].includes(selected.status)} onClick={() => decide(selected, "reject")}>Reject</button>
                  <button type="button" data-testid="finding-supersede" className="btn btn--danger btn--sm" disabled={selected.status === "superseded"} onClick={() => decide(selected, "supersede")}>Supersede</button>
                </div>
              ) : null}
            </Card>
          ) : (
            <Card title="Finding 详情">
              <EmptyState title="选择左侧 Finding" />
            </Card>
          )}
        </div>
      </div>
    </>
  );
}
