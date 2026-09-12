import { useCallback } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useAuth } from "@chain/api-client";
import { Card, EmptyState, EntityLink, ErrorState, EvidenceRail, PageHeader, SkeletonRows, StatusBadge, statusToneFor, useDialog, dialogInputValue } from "@chain/ui";
import { KeyValues, formatTime, useAction, useAsync } from "../sprint4/common";

interface InvestigationDetail {
  id: string;
  case_id?: string | null;
  case_no?: string;
  goal: string;
  mode: string;
  status: string;
  subject: Record<string, unknown>;
  revision: number;
  finding_count: number;
  evidence_count: number;
  pending_findings: number;
  active_run_id?: string | null;
  created_at: string;
  updated_at: string;
}
interface Run {
  id: string;
  status: string;
  objective: string;
  checkpoint?: Record<string, unknown>;
  pending_data_jobs?: string[];
  steps?: Array<{ id: string; ordinal: number; action: string; capability: string; status: string; external_job_id?: string | null; error?: string; algorithm_version?: string }>;
}
interface FindingRow {
  id: string;
  title: string;
  finding_type: string;
  status: string;
  nature: string;
  confidence: number;
  algorithm_version: string;
}
interface EvidenceRow {
  id: string;
  title: string;
  evidence_type: string;
  status: string;
  content_hash: string;
}

const STEP_GLYPH: Record<string, string> = { completed: "✓", running: "●", pending: "○", failed: "■", waiting: "◐", paused: "▲" };

export default function InvestigationWorkbenchPage() {
  const { id = "" } = useParams();
  const { can } = useAuth();
  const dialog = useDialog();
  const action = useAction();
  const navigate = useNavigate();
  const detail = useAsync<InvestigationDetail>(`/api/v2/investigations/${id}`, [id]);
  const runtime = useAsync<{ runs: Run[] }>(`/api/v2/investigations/${id}/runtime`, [id]);
  const findings = useAsync<FindingRow[]>(`/api/v2/investigations/${id}/findings`, [id], { limit: 200 });
  const evidence = useAsync<EvidenceRow[]>(`/api/v2/investigations/${id}/evidence`, [id], { limit: 200 });

  const reloadAll = useCallback(() => {
    void detail.reload();
    void runtime.reload();
    void findings.reload();
    void evidence.reload();
  }, [detail, runtime, findings, evidence]);

  const command = useCallback(
    async (cmd: string) => {
      if (!detail.data) return;
      const needsReason = ["pause", "cancel", "retry"].includes(cmd);
      let reason = "";
      if (needsReason) {
        const input = dialogInputValue(await dialog.prompt({ title: `调查命令：${cmd}`, input: { label: "原因", pattern: /^.{2,}$/ } }));
        if (!input) return;
        reason = input;
      }
      const answer = await dialog.confirm({
        kind: "confirm",
        title: "确认执行调查命令？",
        details: [
          { label: "当前状态", value: detail.data.status },
          { label: "命令", value: cmd },
          { label: "revision", value: `r${detail.data.revision}` },
          ...(reason ? [{ label: "原因", value: reason }] : []),
        ],
      });
      if (answer !== "confirm") return;
      await action.run(
        () => action.client.post(`/api/v2/investigations/${id}/commands/${cmd}`, { reason }, { ifMatch: String(detail.data!.revision) }),
        { successTitle: "调查状态已更新", onDone: reloadAll },
      );
    },
    [action, detail.data, dialog, id, reloadAll],
  );

  const submitReview = useCallback(async () => {
    if (!detail.data) return;
    const reviewerRaw = await dialog.prompt({
      title: "提交复核",
      body: "可指定 reviewer；留空则通知本组织全部有复核权限的成员。",
      input: { label: "Reviewer identity ID（可留空）", placeholder: "uuid 或留空", pattern: /^$|^[0-9a-f-]{36}$/ },
    });
    if (reviewerRaw === "" || reviewerRaw === null) return;
    const reviewer = reviewerRaw.startsWith("input:") ? reviewerRaw.slice("input:".length).trim() : "";
    const note = dialogInputValue(await dialog.prompt({ title: "复核说明", input: { label: "说明", pattern: /^.{2,}$/ } }));
    if (!note) return;
    await action.run(
      () => action.client.post(`/api/v2/investigations/${id}/commands/submit-review`, { reviewer_id: reviewer ?? "", note }, { ifMatch: String(detail.data!.revision) }),
      { successTitle: "已提交复核", onDone: reloadAll },
    );
  }, [action, detail.data, dialog, id, reloadAll]);

  if (detail.loading) return <SkeletonRows rows={6} columns={4} />;
  if (detail.error || !detail.data) return <Card><ErrorState title="调查加载失败" message={detail.error ?? "不存在或无权访问"} onRetry={detail.reload} /></Card>;
  const inv = detail.data;
  const runs = runtime.data?.runs ?? [];
  const latest = runs[0];

  return (
    <>
      <PageHeader
        title={inv.goal}
        subtitle={
          <span>
            <StatusBadge tone={statusToneFor(inv.status)}>{inv.status}</StatusBadge>
            <span className="mono"> {String(inv.subject?.chain ?? "")} / {String(inv.subject?.value ?? "")} · revision r{inv.revision}</span>
          </span>
        }
        actions={
          <span className="s4-button-row">
            {can("investigation:start") && ["created", "failed"].includes(inv.status) ? <button type="button" className="btn btn--sm btn--primary" onClick={() => command("start")}>启动</button> : null}
            {can("investigation:pause") && ["running", "waiting_data"].includes(inv.status) ? <button type="button" className="btn btn--sm" onClick={() => command("pause")}>暂停</button> : null}
            {can("investigation:resume") && inv.status === "paused" ? <button type="button" className="btn btn--sm btn--primary" onClick={() => command("resume")}>恢复</button> : null}
            {can("investigation:retry") && inv.status === "failed" ? <button type="button" className="btn btn--sm" onClick={() => command("retry")}>重试</button> : null}
            {can("investigation:submit_review") && ["running", "waiting_data", "paused", "waiting_review"].includes(inv.status) ? <button type="button" className="btn btn--sm" onClick={submitReview}>提交复核</button> : null}
            {can("investigation:cancel") && !["cancelled", "completed"].includes(inv.status) ? <button type="button" className="btn btn--sm btn--danger" onClick={() => command("cancel")}>取消</button> : null}
            <button type="button" className="btn btn--sm" onClick={() => navigate(`/investigations/${id}/findings`)}>Findings</button>
          </span>
        }
      />
      <div className="s4-grid">
        <div>
          <Card title="阶段与步骤">
            {runtime.loading ? <SkeletonRows rows={4} columns={3} /> : null}
            {runtime.error ? <ErrorState title="Runtime 加载失败" message={runtime.error} onRetry={runtime.reload} /> : null}
            {!runtime.loading && runs.length === 0 ? <EmptyState title="尚未启动运行" /> : null}
            {latest ? <KeyValues items={[{ label: "Run", value: <EntityLink value={latest.id} /> }, { label: "Run 状态", value: <StatusBadge tone={statusToneFor(latest.status)}>{latest.status}</StatusBadge> }, { label: "Checkpoint", value: <span className="mono">{JSON.stringify(latest.checkpoint ?? {})}</span> }, { label: "Pending data jobs", value: (latest.pending_data_jobs ?? []).join(", ") || "—" }]} /> : null}
            <div style={{ marginTop: 12 }}>
              {(latest?.steps ?? []).map((step) => (
                <div className="s4-step" key={step.id}>
                  <span className="s4-step__status" aria-hidden="true">{STEP_GLYPH[step.status] ?? "•"}</span>
                  <div className="s4-step__body">
                    <div>
                      <strong>{step.action}</strong> <span className="badge badge--neutral">{step.capability}</span>
                    </div>
                    <div className="s4-step__meta">
                      {step.status}
                      {step.algorithm_version ? ` · ${step.algorithm_version}` : ""}
                      {step.error ? ` · ${step.error}` : ""}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          </Card>

          <Card title={`Findings（${findings.data?.length ?? 0}）`}>
            {findings.loading ? <SkeletonRows rows={4} columns={4} /> : null}
            {findings.error ? <ErrorState title="Findings 加载失败" message={findings.error} onRetry={findings.reload} /> : null}
            <div className="s4-list">
              {(findings.data ?? []).slice(0, 8).map((f) => (
                <div className="s4-list__item" key={f.id}>
                  <div className="s4-list__main">
                    <div className="s4-list__title">{f.title}</div>
                    <div className="s4-list__sub">
                      {f.finding_type} · {f.nature} · confidence {(f.confidence * 100).toFixed(0)}% · {f.algorithm_version}
                    </div>
                  </div>
                  <StatusBadge tone={statusToneFor(f.status)}>{f.status}</StatusBadge>
                </div>
              ))}
              {(findings.data ?? []).length === 0 && !findings.loading ? <EmptyState title="暂无 Finding" /> : null}
            </div>
          </Card>
        </div>
        <EvidenceRail
          title="Evidence Rail"
          items={(evidence.data ?? []).slice(0, 10).map((e) => ({
            label: `${e.evidence_type} · ${e.status}`,
            value: (
              <Link to={`/investigations/${id}/evidence/${e.id}`} title={e.title}>
                {e.title.length > 42 ? `${e.title.slice(0, 42)}…` : e.title}
                <div>{e.content_hash.slice(0, 16)}…</div>
              </Link>
            ),
          }))}
        />
      </div>
      {inv.case_id ? (
        <Card title="所属案件">
          <Link to={`/cases/${inv.case_id}/overview`}>{inv.case_no || inv.case_id}</Link>
        </Card>
      ) : null}
      <div className="s4-sticky-actions">
        <span className="s4-list__sub">更新 {formatTime(inv.updated_at)}</span>
        <button type="button" className="btn" onClick={reloadAll}>刷新</button>
      </div>
    </>
  );
}
