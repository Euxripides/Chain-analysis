import { useCallback, useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ApiError, useAuth } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  EntityLink,
  ErrorState,
  EvidenceRail,
  Field,
  PageHeader,
  RelativeTime,
  SkeletonRows,
  StatusBadge,
  statusToneFor,
  useDialog,
  dialogInputValue,
} from "@chain/ui";
import { DeepTabs, KeyValues, formatTime, useAsync, useAction } from "../sprint4/common";

interface CaseDetail {
  id: string;
  organization_id: string;
  workspace_id: string;
  case_no: string;
  name: string;
  description: string;
  status: string;
  color: string;
  classification: string;
  priority: string;
  revision: number;
  created_at: string;
  updated_at: string;
  archived_at?: string | null;
  closed_at?: string | null;
  lead_count?: number;
  member_count?: number;
  subject_count?: number;
  open_findings?: number;
  open_reports?: number;
  open_investigations?: number;
}

interface Member {
  id: string;
  identity_id: string;
  email: string;
  display_name: string;
  role: string;
  created_at: string;
}
interface Subject {
  id: string;
  subject_type: string;
  chain?: string | null;
  value: string;
  label: string;
  revision: number;
  created_at: string;
}
interface Activity {
  id: string;
  event_type: string;
  actor_display: string;
  summary: string;
  created_at: string;
}
interface InvestigationRow {
  id: string;
  goal: string;
  status: string;
  finding_count: number;
  evidence_count: number;
  pending_findings: number;
  updated_at: string;
}
interface ReportRow {
  id: string;
  revision: number;
  status: string;
  content_hash: string;
  size_bytes: number;
  generated_at: string;
  required_action?: string;
}

const TAB_TO: Record<string, string> = {
  overview: "overview",
  subjects: "subjects",
  investigations: "investigations",
  evidence: "evidence",
  reports: "reports",
  activity: "activity",
};

export default function CaseDetailPage() {
  const { caseId = "" } = useParams();
  const path = window.location.pathname;
  const tab = Object.keys(TAB_TO).find((key) => path.endsWith("/" + key)) ?? "overview";
  const dialog = useDialog();
  const { can } = useAuth();
  const action = useAction();
  const navigate = useNavigate();
  const { data: detail, loading, error, reload } = useAsync<CaseDetail>(`/api/v2/cases/${caseId}`, [caseId], {});
  const { data: members, reload: reloadMembers } = useAsync<Member[]>(`/api/v2/cases/${caseId}/members`, [caseId]);
  const [addingMember, setAddingMember] = useState(false);
  const [subjectVersion, setSubjectVersion] = useState(0);
  const [memberOptions, setMemberOptions] = useState<Array<{ identity_id: string; email: string; display_name: string }>>([]);
  const [memberForm, setMemberForm] = useState({ identity_id: "", role: "investigator" });

  useEffect(() => {
    if (!addingMember) return;
    void action.client
      .get<Array<{ identity_id: string; email: string; display_name: string }>>("/api/v2/workspace-members", { query: { limit: 200 } })
      .then((res) => setMemberOptions(res.data ?? []))
      .catch(() => setMemberOptions([]));
  }, [addingMember, action.client]);

  const command = useCallback(
    async (cmd: string) => {
      if (!detail) return;
      const reason = dialogInputValue(await dialog.prompt({
        title: `案件命令：${cmd}`,
        body: "该操作会推进案件状态并写入审计；请填写原因。",
        input: { label: "原因", placeholder: "例如：初查完成，进入复核", pattern: /^.{2,}$/ },
      }));
      if (!reason) return;
      const answer = await dialog.confirm({
        kind: "confirm",
        title: "确认执行状态命令？",
        details: [
          { label: "当前状态", value: detail.status },
          { label: "命令", value: cmd },
          { label: "revision", value: `r${detail.revision}` },
          { label: "原因", value: reason },
        ],
      });
      if (answer !== "confirm") return;
      await action.run(() => action.client.post(`/api/v2/cases/${caseId}/commands/${cmd}`, { reason }, { ifMatch: String(detail.revision) }), {
        successTitle: "案件状态已更新",
        onDone: () => reload(),
      });
    },
    [action, caseId, detail, dialog, reload],
  );

  const addSubject = useCallback(async () => {
    const type = dialogInputValue(await dialog.prompt({ title: "对象类型", input: { label: "类型", defaultValue: "ADDRESS", pattern: /^(ADDRESS|TRANSACTION|DOMAIN|IP|APK|ENTITY|OTHER)$/ } }));
    if (!type) return;
    const chain = type === "ADDRESS"
      ? dialogInputValue(await dialog.prompt({ title: "链", input: { label: "chain", defaultValue: "tron", pattern: /^[a-z0-9_-]{2,32}$/ } }))
      : null;
    const value = dialogInputValue(await dialog.prompt({ title: "对象值", input: { label: "值", placeholder: "地址 / 交易哈希 / 域名", pattern: /^.{2,512}$/ } }));
    if (!value) return;
    await action.run(
      () => action.client.post(`/api/v2/cases/${caseId}/subjects`, { subject_type: type, chain: chain ?? undefined, value, label: "" }),
      { successTitle: "对象已加入案件", onDone: () => { setSubjectVersion((v) => v + 1); void reload(); } },
    );
  }, [action, caseId, dialog, reload]);

  const removeSubject = useCallback(
    async (subject: Subject) => {
      const answer = await dialog.confirm({
        kind: "confirm",
        title: "从案件移除对象？",
        body: "底层数据仍保留，案件视图不再引用。（删除 UI 执行移除，审计保留）",
        details: [
          { label: "对象", value: subject.value },
          { label: "类型", value: subject.subject_type },
        ],
      });
      if (answer !== "confirm") return;
      await action.run(() => action.client.delete(`/api/v2/cases/${caseId}/subjects/${subject.id}`), { successTitle: "对象已移除", onDone: () => reload() });
    },
    [action, caseId, dialog, reload],
  );

  const removeMember = useCallback(
    async (member: Member) => {
      const answer = await dialog.confirm({
        kind: "confirm",
        title: "移除案件成员？",
        details: [
          { label: "成员", value: member.display_name || member.email },
          { label: "角色", value: member.role },
        ],
      });
      if (answer !== "confirm") return;
      await action.run(() => action.client.delete(`/api/v2/cases/${caseId}/members/${member.identity_id}`), { successTitle: "成员已移除", onDone: () => reloadMembers() });
    },
    [action, caseId, dialog, reloadMembers],
  );

  if (loading) return <SkeletonRows rows={6} columns={4} />;
  if (error || !detail) {
    return (
      <Card>
        <ErrorState title="案件加载失败" message={error ?? "案件不存在或无权访问"} onRetry={reload} />
      </Card>
    );
  }

  const tabs = [
    { key: "overview", label: "概览", to: `/cases/${caseId}/overview` },
    { key: "subjects", label: "调查对象", to: `/cases/${caseId}/subjects`, permission: "subject:list" },
    { key: "investigations", label: "案件调查", to: `/cases/${caseId}/investigations`, permission: "investigation:list" },
    { key: "evidence", label: "证据中心", to: `/cases/${caseId}/evidence`, permission: "evidence:list" },
    { key: "reports", label: "案件报告", to: `/cases/${caseId}/reports`, permission: "report:list" },
    { key: "activity", label: "案件动态", to: `/cases/${caseId}/activity`, permission: "case:activity:read" },
  ];

  return (
    <>
      <PageHeader
        title={detail.name}
        subtitle={
          <span>
            {detail.case_no || "无编号"} · revision r{detail.revision} · 密级 {detail.classification} · 数据源 postgres:cases
          </span>
        }
        actions={
          <span className="s4-button-row">
            {can("case:state:update") && detail.status === "draft" ? (
              <button type="button" className="btn btn--sm" onClick={() => command("activate")}>
                启用
              </button>
            ) : null}
            {can("case:state:update") && ["draft", "active", "under_review"].includes(detail.status) ? (
              <button type="button" className="btn btn--sm" onClick={() => command("close")}>
                关闭
              </button>
            ) : null}
            {can("case:state:update") && detail.status === "closed" ? (
              <button type="button" className="btn btn--sm" onClick={() => command("archive")}>
                归档
              </button>
            ) : null}
            {can("case:state:update") && ["closed", "archived"].includes(detail.status) ? (
              <button type="button" className="btn btn--sm" onClick={() => command("reopen")}>
                重新打开
              </button>
            ) : null}
            {can("investigation:create") ? (
              <button type="button" className="btn btn--primary btn--sm" onClick={() => navigate(`/investigations/new?case_id=${caseId}`)}>
                发起调查
              </button>
            ) : null}
          </span>
        }
      />
      <DeepTabs tabs={tabs} />
      <div className="s4-grid">
        <div>
          {tab === "overview" ? <OverviewTab detail={detail} /> : null}
          {tab === "subjects" ? <SubjectsTab caseId={caseId} version={subjectVersion} onAdd={addSubject} onRemove={removeSubject} /> : null}
          {tab === "investigations" ? <CaseInvestigationsTab caseId={caseId} /> : null}
          {tab === "evidence" ? <CaseEvidenceTab caseId={caseId} /> : null}
          {tab === "reports" ? <CaseReportsTab caseId={caseId} /> : null}
          {tab === "activity" ? <ActivityTab caseId={caseId} /> : null}
        </div>
        <EvidenceRail
          title="案件事实"
          items={[
            { label: "状态", value: <StatusBadge tone={statusToneFor(detail.status)}>{detail.status}</StatusBadge> },
            { label: "优先级", value: detail.priority },
            { label: "负责人", value: `${detail.lead_count ?? 0} 名 lead` },
            { label: "成员", value: `${detail.member_count ?? 0} 人` },
            { label: "对象", value: `${detail.subject_count ?? 0} 个` },
            { label: "待复核 Finding", value: String(detail.open_findings ?? 0) },
            { label: "未完成报告", value: String(detail.open_reports ?? 0) },
            { label: "进行中调查", value: String(detail.open_investigations ?? 0) },
            { label: "创建时间", value: formatTime(detail.created_at) },
            { label: "最近更新", value: formatTime(detail.updated_at) },
            { label: "案件 ID", value: <EntityLink value={detail.id} /> },
          ]}
        />
      </div>

      <Card title="案件成员" actions={<button type="button" className="btn btn--sm" disabled={!can("case:member:manage")} onClick={() => setAddingMember(true)}>添加成员</button>}>
        <DataTable<Member>
          rows={members ?? []}
          rowKey={(m) => m.id}
          columns={[
            { key: "name", header: "成员", render: (m) => <span><strong>{m.display_name || "—"}</strong><div className="s4-list__sub">{m.email}</div></span> },
            { key: "role", header: "角色", width: "120px", render: (m) => <StatusBadge tone={m.role === "lead" ? "ok" : "neutral"}>{m.role}</StatusBadge> },
            { key: "joined", header: "加入时间", width: "160px", render: (m) => formatTime(m.created_at) },
            {
              key: "ops",
              header: "",
              width: "90px",
              render: (m) => (
                <button type="button" className="btn btn--sm" disabled={!can("case:member:manage")} onClick={() => removeMember(m)}>
                  移除
                </button>
              ),
            },
          ]}
        />
      </Card>

      {addingMember ? (
        <Card title="添加案件成员">
          <div className="s4-form-grid">
            <Field label="成员" htmlFor="member-id">
              <select id="member-id" className="select" value={memberForm.identity_id} onChange={(e) => setMemberForm({ ...memberForm, identity_id: e.target.value })}>
                <option value="">选择工作区成员</option>
                {memberOptions.map((m) => (
                  <option key={m.identity_id} value={m.identity_id}>
                    {m.display_name || m.email}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="角色" htmlFor="member-role">
              <select id="member-role" className="select" value={memberForm.role} onChange={(e) => setMemberForm({ ...memberForm, role: e.target.value })}>
                <option value="lead">lead</option>
                <option value="investigator">investigator</option>
                <option value="reviewer">reviewer</option>
                <option value="viewer">viewer</option>
              </select>
            </Field>
            <div className="s4-sticky-actions field--wide">
              <button type="button" className="btn" onClick={() => setAddingMember(false)}>
                取消
              </button>
              <button
                type="button"
                className="btn btn--primary"
                disabled={!memberForm.identity_id || action.busy}
                onClick={() =>
                  action.run(() => action.client.post(`/api/v2/cases/${caseId}/members`, memberForm), {
                    successTitle: "成员已加入",
                    onDone: () => {
                      setAddingMember(false);
                      setMemberForm({ identity_id: "", role: "investigator" });
                      void reloadMembers();
                    },
                  })
                }
              >
                加入
              </button>
            </div>
          </div>
        </Card>
      ) : null}
    </>
  );
}

function OverviewTab({ detail }: { detail: CaseDetail }) {
  return (
    <Card title="案件概览">
      <KeyValues
        items={[
          { label: "案件编号", value: detail.case_no || "—" },
          { label: "名称", value: detail.name },
          { label: "状态", value: detail.status },
          { label: "密级", value: detail.classification },
          { label: "优先级", value: detail.priority },
          { label: "revision", value: `r${detail.revision}` },
          { label: "创建", value: formatTime(detail.created_at) },
          { label: "更新", value: formatTime(detail.updated_at) },
          { label: "关闭时间", value: formatTime(detail.closed_at) },
          { label: "归档时间", value: formatTime(detail.archived_at) },
          { label: "说明", value: detail.description || "—" },
        ]}
      />
    </Card>
  );
}

function SubjectsTab({ caseId, version, onAdd, onRemove }: { caseId: string; version: number; onAdd: () => void; onRemove: (s: Subject) => void }) {
  const { data, loading, error, reload } = useAsync<Subject[]>(`/api/v2/cases/${caseId}/subjects`, [caseId, version]);
  const { can } = useAuth();
  return (
    <Card title="调查对象" actions={<button type="button" className="btn btn--sm" disabled={!can("subject:create")} onClick={onAdd}>新增对象</button>}>
      <DataTable<Subject>
        loading={loading}
        error={error}
        onRetry={reload}
        rows={data ?? []}
        rowKey={(s) => s.id}
        columns={[
          { key: "type", header: "类型", width: "120px", render: (s) => <StatusBadge tone="neutral">{s.subject_type}</StatusBadge> },
          { key: "chain", header: "链", width: "90px", render: (s) => s.chain ?? "—" },
          { key: "value", header: "对象值", render: (s) => <EntityLink value={s.value} /> },
          { key: "revision", header: "revision", width: "90px", render: (s) => `r${s.revision}` },
          { key: "created", header: "加入时间", width: "160px", render: (s) => formatTime(s.created_at) },
          { key: "ops", header: "", width: "90px", render: (s) => <button type="button" className="btn btn--sm" disabled={!can("subject:remove")} onClick={() => onRemove(s)}>移除</button> },
        ]}
      />
    </Card>
  );
}

function CaseInvestigationsTab({ caseId }: { caseId: string }) {
  const navigate = useNavigate();
  const { data, loading, error, reload } = useAsync<InvestigationRow[]>("/api/v2/investigations", [caseId], { case_id: caseId, limit: 200 });
  return (
    <Card title="案件调查" actions={<button type="button" className="btn btn--sm" onClick={() => navigate(`/investigations/new?case_id=${caseId}`)}>发起调查</button>}>
      <DataTable<InvestigationRow>
        loading={loading}
        error={error}
        onRetry={reload}
        rows={data ?? []}
        rowKey={(r) => r.id}
        columns={[
          { key: "goal", header: "目的", render: (r) => <a href={`/app-v2/investigations/${r.id}/workbench`} onClick={(e) => { e.preventDefault(); navigate(`/investigations/${r.id}/workbench`); }}>{r.goal}</a> },
          { key: "status", header: "状态", width: "120px", render: (r) => <StatusBadge tone={statusToneFor(r.status)}>{r.status}</StatusBadge> },
          { key: "findings", header: "Findings", width: "100px", render: (r) => `${r.finding_count}（待复核 ${r.pending_findings}）` },
          { key: "evidence", header: "Evidence", width: "100px", render: (r) => r.evidence_count },
          { key: "updated", header: "更新", width: "140px", render: (r) => <RelativeTime value={r.updated_at} /> },
        ]}
      />
    </Card>
  );
}

function CaseEvidenceTab({ caseId }: { caseId: string }) {
  const investigations = useAsync<InvestigationRow[]>("/api/v2/investigations", [caseId], { case_id: caseId, limit: 200 });
  return (
    <Card title="案件证据">
      {investigations.loading ? <SkeletonRows rows={3} columns={3} /> : null}
      {investigations.error ? <ErrorState title="调查列表加载失败" message={investigations.error} onRetry={investigations.reload} /> : null}
      {!investigations.loading && (investigations.data ?? []).length === 0 ? <EmptyState title="该案件还没有调查" /> : null}
      <div className="s4-list">
        {(investigations.data ?? []).map((inv) => (
          <div className="s4-list__item" key={inv.id}>
            <div className="s4-list__main">
              <div className="s4-list__title">{inv.goal}</div>
              <div className="s4-list__sub">
                {inv.finding_count} Finding · {inv.evidence_count} Evidence · 状态 {inv.status}
              </div>
            </div>
            <span className="s4-list__actions">
              <Link className="btn btn--sm" to={`/investigations/${inv.id}/findings`}>
                Findings
              </Link>
              <Link className="btn btn--sm" to={`/investigations/${inv.id}/workbench`}>
                工作台
              </Link>
            </span>
          </div>
        ))}
      </div>
    </Card>
  );
}

function CaseReportsTab({ caseId }: { caseId: string }) {
  const { data, loading, error, reload } = useAsync<ReportRow[]>(`/api/v2/cases/${caseId}/reports`, [caseId]);
  const investigations = useAsync<InvestigationRow[]>("/api/v2/investigations", [caseId], { case_id: caseId, limit: 200 });
  const navigate = useNavigate();
  const { can } = useAuth();
  const action = useAction();
  const dialog = useDialog();

  const generate = useCallback(async () => {
    const options = investigations.data ?? [];
    if (options.length === 0) {
      dialog.notify("warning", "没有可生成报告的调查");
      return;
    }
    if (options.length === 1) {
      await action.run(() => action.client.post(`/api/v2/cases/${caseId}/reports`, { investigation_id: options[0].id }), {
        successTitle: "报告 revision 已生成",
        onDone: () => reload(),
      });
      return;
    }
    const id = dialogInputValue(await dialog.prompt({
      title: "选择调查",
      input: { label: "调查 ID", defaultValue: options[0].id, pattern: /^[0-9a-f-]{36}$/ },
    }));
    if (!id) return;
    await action.run(() => action.client.post(`/api/v2/cases/${caseId}/reports`, { investigation_id: id }), { successTitle: "报告 revision 已生成", onDone: () => reload() });
  }, [action, caseId, dialog, investigations.data, reload]);

  const download = useCallback(
    async (report: ReportRow) => {
      const res = await action.run(() => action.client.post<{ url: string; expires_in_seconds: number }>(`/api/v2/reports/${report.id}/download-ticket`, {}), {
        successTitle: "下载票据已签发",
        successBody: "票据 60 秒内有效且只能使用一次，请立即保存文件。",
      });
      if (res) window.open(res.data.url, "_blank");
    },
    [action],
  );

  return (
    <Card title="案件报告" actions={<button type="button" className="btn btn--sm" disabled={!can("report:create")} onClick={generate}>生成报告</button>}>
      <DataTable<ReportRow>
        loading={loading}
        error={error}
        onRetry={reload}
        rows={data ?? []}
        rowKey={(r) => r.id}
        columns={[
          { key: "revision", header: "revision", width: "100px", render: (r) => `r${r.revision}` },
          { key: "status", header: "状态", width: "120px", render: (r) => <StatusBadge tone={statusToneFor(r.status)}>{r.status}</StatusBadge> },
          { key: "hash", header: "SHA-256", render: (r) => <EntityLink value={r.content_hash} /> },
          { key: "size", header: "大小", width: "100px", render: (r) => `${r.size_bytes} B` },
          { key: "generated", header: "生成时间", width: "160px", render: (r) => formatTime(r.generated_at) },
          { key: "ops", header: "", width: "160px", render: (r) => <span className="s4-button-row"><button type="button" className="btn btn--sm" onClick={() => navigate(`/reports/${r.id}`)}>打开</button><button type="button" className="btn btn--sm" disabled={!["approved", "signed", "archived"].includes(r.status)} onClick={() => download(r)}>下载</button></span> },
        ]}
      />
    </Card>
  );
}

function ActivityTab({ caseId }: { caseId: string }) {
  const { data, loading, error, reload } = useAsync<Activity[]>(`/api/v2/cases/${caseId}/activity`, [caseId], { limit: 200 });
  return (
    <Card title="案件动态">
      {loading ? <SkeletonRows rows={5} columns={3} /> : null}
      {error ? <ErrorState title="动态加载失败" message={error} onRetry={reload} /> : null}
      {!loading && !error && (data ?? []).length === 0 ? <EmptyState title="暂无业务动态" /> : null}
      <div className="s4-list">
        {(data ?? []).map((a) => (
          <div className="s4-list__item" key={a.id}>
            <div className="s4-list__main">
              <div className="s4-list__title">{a.summary}</div>
              <div className="s4-list__sub">
                {a.event_type} · {a.actor_display || "system"} · {formatTime(a.created_at)}
              </div>
            </div>
          </div>
        ))}
      </div>
    </Card>
  );
}
