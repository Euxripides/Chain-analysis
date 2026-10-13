import {
  useCallback,
  useEffect,
  useState } from "react";
import { Link,
  useNavigate,
  useParams } from "react-router-dom";
import { useAuth } from "@chain/api-client";
import {
  Button,
  Card,
  DataTable,
  EmptyState,
  EntityLink,
  ErrorState,
  Field,
  HashValue,
  InspectorDrawer,
  KeyValueList,
  MetaLine,
  PageSection,
  PageSkeleton,
  RelativeTime,
  SkeletonRows,
  SplitPane,
  StatusBadge,
  StatusBadgeForState,
  Timeline,
  dialogInputValue,
  useDialog,
} from "@chain/ui";
import { CsPageHeader } from "@chain/user-ui";
import { DeepTabs, formatTime, useAction, useAsync } from "../sprint4/common";
import { CASE_TABS } from "../app/case-tabs";
import { CaseMembersTab } from "./case/CaseMembersTab";

/**
 * §12.6 案件详情 /cases/:caseId/:tab
 *
 * 全部 tab 保留：概览 / 对象 / 调查 / 证据 / 报告 / 成员 / 动态。tab 由 URL 决定
 * （`/cases/{id}/{tab}`），刷新后保持当前 tab。
 *
 * 数据来源与写操作完全不变：
 *   GET /api/v2/cases/{id} · /members · /subjects · /activity · /reports
 *   GET /api/v2/investigations?case_id= · /api/v2/workspace-members
 *   POST /cases/{id}/commands/{cmd}（If-Match: revision）· members · subjects · reports
 *   DELETE /members/{identity_id} · /subjects/{subject_id}
 * 每个写操作的权限判定、原因输入、revision 与审计链路保持原样。
 *
 * 两栏结构改为 SplitPane（主区 + InspectorDrawer「案件事实」）；Evidence 列表使用真实
 * 的 `/api/v2/investigations?case_id=` 聚合结果——服务端没有"按案件列 Evidence"的端点，
 * 因此这一栏不伪造逐条 Evidence，而是给出每个调查的 Evidence 计数与入口。
 */
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
  object_type?: string;
  object_id?: string;
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

/** URL 中允许的 tab；未知值回落到概览（与重构前一致）。 */
const TAB_KEYS = ["overview", "subjects", "investigations", "evidence", "reports", "members", "activity"] as const;
type TabKey = (typeof TAB_KEYS)[number];

export default function CaseDetailPage() {
  const { caseId = "", tab: tabParam = "" } = useParams();
  const tab: TabKey = (TAB_KEYS as readonly string[]).includes(tabParam) ? (tabParam as TabKey) : "overview";
  const dialog = useDialog();
  const { can } = useAuth();
  const action = useAction();
  const navigate = useNavigate();
  const { data: detail, loading, error, reload } = useAsync<CaseDetail>(`/api/v2/cases/${caseId}`, [caseId], {});
  const [subjectVersion, setSubjectVersion] = useState(0);

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

  if (loading) return <PageSkeleton shape="detail" />;
  if (error || !detail) {
    return (
      <Card>
        <ErrorState title="案件加载失败" message={error ?? "案件不存在或无权访问"} onRetry={reload} />
      </Card>
    );
  }

  /*
   * Tab 表与**路由守卫共用** app/case-tabs.ts：两处各写一份权限必然漂移，
   * 而漂移的后果正好是这次要修的问题——UI 藏起来了、路由却没拦。
   */
  const tabs = CASE_TABS.map((definition) => ({
    key: definition.key,
    label: definition.label,
    to: `/cases/${caseId}/${definition.key}`,
    /* 入口过滤与路由守卫共用同一份权限列表，都是"全部满足才显示"。 */
    permissions: definition.permissions,
  }));

  return (
    <>
      <CsPageHeader
        title={detail.name}
        breadcrumb={[{ label: "工作台", to: "/home" }, { label: "案件中心", to: "/cases" }, { label: detail.name }]}
        status={<StatusBadgeForState value={detail.status} />}
        meta={
          <MetaLine
            items={[
              { label: "案件号", value: detail.case_no || "无编号" },
              { label: "revision", value: `r${detail.revision}` },
              { label: "密级", value: detail.classification },
              { label: "最近更新", value: <RelativeTime value={detail.updated_at} /> },
              { label: "数据源", value: "postgres:cases" },
            ]}
          />
        }
        primaryAction={
          can("investigation:create") ? (
            <Button variant="primary" size="sm" onClick={() => navigate(`/investigations/new?case_id=${caseId}`)}>
              发起调查
            </Button>
          ) : null
        }
        secondaryAction={
          <span className="s4-button-row">
            {can("case:state:update") && detail.status === "draft" ? (
              <Button size="sm" onClick={() => command("activate")}>
                启用
              </Button>
            ) : null}
            {can("case:state:update") && ["draft", "active", "under_review"].includes(detail.status) ? (
              <Button size="sm" onClick={() => command("close")}>
                关闭
              </Button>
            ) : null}
            {can("case:state:update") && detail.status === "closed" ? (
              <Button size="sm" onClick={() => command("archive")}>
                归档
              </Button>
            ) : null}
            {can("case:state:update") && ["closed", "archived"].includes(detail.status) ? (
              <Button size="sm" onClick={() => command("reopen")}>
                重新打开
              </Button>
            ) : null}
          </span>
        }
      />

      <DeepTabs tabs={tabs} />

      <SplitPane
        storageKey="cs-case-detail-aside"
        asideLabel="案件事实"
        defaultAsideWidth={340}
        minAsideWidth={280}
        maxAsideWidth={520}
        main={
          <>
            {tab === "overview" ? <OverviewTab detail={detail} /> : null}
            {tab === "subjects" ? <SubjectsTab caseId={caseId} version={subjectVersion} onAdd={addSubject} onRemove={removeSubject} /> : null}
            {tab === "investigations" ? <CaseInvestigationsTab caseId={caseId} /> : null}
            {tab === "evidence" ? <CaseEvidenceTab caseId={caseId} /> : null}
            {tab === "reports" ? <CaseReportsTab caseId={caseId} /> : null}
            {tab === "activity" ? <ActivityTab caseId={caseId} /> : null}
            {tab === "members" ? <CaseMembersTab caseId={caseId} /> : null}
          </>
        }
        aside={
          <InspectorDrawer title="案件事实" subtitle={detail.case_no || undefined} ariaLabel="案件事实">
            <KeyValueList
              items={[
                { label: "状态", value: <StatusBadgeForState value={detail.status} /> },
                { label: "优先级", value: detail.priority || "—" },
                { label: "负责人", value: `${detail.lead_count ?? 0} 名 lead` },
                { label: "成员", value: `${detail.member_count ?? 0} 人` },
                { label: "对象", value: `${detail.subject_count ?? 0} 个` },
                { label: "待复核 Finding", value: String(detail.open_findings ?? 0) },
                { label: "未完成报告", value: String(detail.open_reports ?? 0) },
                { label: "进行中调查", value: String(detail.open_investigations ?? 0) },
                { label: "创建时间", value: formatTime(detail.created_at) },
                { label: "最近更新", value: formatTime(detail.updated_at) },
                { label: "案件 ID", value: <HashValue value={detail.id} label="案件 ID" wrap />, wide: true },
              ]}
            />
          </InspectorDrawer>
        }
      />
    </>
  );
}

function OverviewTab({ detail }: { detail: CaseDetail }) {
  return (
    <PageSection title="案件概览">
      <Card>
        <KeyValueList
          items={[
            { label: "案件编号", value: detail.case_no || "—" },
            { label: "名称", value: detail.name },
            { label: "状态", value: <StatusBadgeForState value={detail.status} /> },
            { label: "密级", value: detail.classification },
            { label: "优先级", value: detail.priority },
            { label: "revision", value: `r${detail.revision}` },
            { label: "创建", value: formatTime(detail.created_at) },
            { label: "更新", value: formatTime(detail.updated_at) },
            { label: "关闭时间", value: formatTime(detail.closed_at) },
            { label: "归档时间", value: formatTime(detail.archived_at) },
            { label: "说明", value: detail.description || "—", wide: true },
          ]}
        />
      </Card>
    </PageSection>
  );
}

function SubjectsTab({ caseId, version, onAdd, onRemove }: { caseId: string; version: number; onAdd: () => void; onRemove: (s: Subject) => void }) {
  const { data, loading, error, reload } = useAsync<Subject[]>(`/api/v2/cases/${caseId}/subjects`, [caseId, version]);
  const { can } = useAuth();
  const rows = data ?? [];
  return (
    <PageSection
      title="调查对象"
      count={`${rows.length} 个`}
      actions={
        <Button size="sm" disabled={!can("subject:create")} onClick={onAdd}>
          新增对象
        </Button>
      }
    >
      <Card flush>
        <DataTable<Subject>
          loading={loading}
          error={error}
          onRetry={reload}
          rows={rows}
          rowKey={(subject) => subject.id}
          caption="案件调查对象"
          columns={[
            { key: "type", header: "类型", width: "130px", render: (subject) => <StatusBadge tone="neutral">{subject.subject_type}</StatusBadge> },
            { key: "chain", header: "链", width: "100px", render: (subject) => <span className="mono">{subject.chain ?? "—"}</span> },
            { key: "value", header: "对象值", render: (subject) => <EntityLink value={subject.value} /> },
            { key: "revision", header: "revision", width: "90px", render: (subject) => <span className="mono">{"r" + subject.revision}</span> },
            { key: "created", header: "加入时间", width: "170px", render: (subject) => <span>{formatTime(subject.created_at)}</span> },
            {
              key: "ops",
              header: "操作",
              width: "100px",
              render: (subject) => (
                <Button size="sm" disabled={!can("subject:remove")} onClick={() => onRemove(subject)}>
                  移除
                </Button>
              ),
            },
          ]}
        />
      </Card>
    </PageSection>
  );
}

function CaseInvestigationsTab({ caseId }: { caseId: string }) {
  const { can } = useAuth();
  const navigate = useNavigate();
  const { data, loading, error, reload } = useAsync<InvestigationRow[]>("/api/v2/investigations", [caseId], { case_id: caseId, limit: 200 });
  const rows = data ?? [];
  return (
    <PageSection
      title="案件调查"
      count={`${rows.length} 个`}
      actions={can("investigation:create") ? (
        <Button size="sm" onClick={() => navigate(`/investigations/new?case_id=${caseId}`)}>
          发起调查
        </Button>
      ) : null}
    >
      <Card flush>
        <DataTable<InvestigationRow>
          loading={loading}
          error={error}
          onRetry={reload}
          rows={rows}
          rowKey={(row) => row.id}
          caption="案件调查列表"
          columns={[
            {
              key: "goal",
              header: "目的",
              render: (row) => (
                <Link
                  to={`/investigations/${row.id}/workbench`}
                  onClick={(event) => event.stopPropagation()}
                >
                  {row.goal}
                </Link>
              ),
            },
            { key: "status", header: "状态", width: "140px", render: (row) => <StatusBadgeForState value={row.status} /> },
            { key: "findings", header: "Findings", width: "160px", render: (row) => `${row.finding_count}（待复核 ${row.pending_findings}）` },
            { key: "evidence", header: "Evidence", width: "110px", render: (row) => <span>{row.evidence_count}</span> },
            { key: "updated", header: "更新", width: "140px", render: (row) => <RelativeTime value={row.updated_at} /> },
          ]}
        />
      </Card>
    </PageSection>
  );
}

function CaseEvidenceTab({ caseId }: { caseId: string }) {
  const investigations = useAsync<InvestigationRow[]>("/api/v2/investigations", [caseId], { case_id: caseId, limit: 200 });
  const rows = investigations.data ?? [];
  return (
    <PageSection title="案件证据" count={`${rows.length} 个调查`}>
      <Card flush>
        {investigations.loading ? <SkeletonRows rows={3} columns={5} /> : null}
        {!investigations.loading && investigations.error ? (
          <ErrorState title="调查列表加载失败" message={investigations.error} onRetry={investigations.reload} />
        ) : null}
        {!investigations.loading && !investigations.error && rows.length === 0 ? (
          <EmptyState title="该案件还没有调查" />
        ) : null}
        {!investigations.loading && !investigations.error && rows.length > 0 ? (
          <DataTable<InvestigationRow>
            rows={rows}
            rowKey={(row) => row.id}
            caption="按调查汇总的证据"
            columns={[
              { key: "goal", header: "调查", render: (row) => <span>{row.goal}</span> },
              { key: "status", header: "状态", width: "140px", render: (row) => <StatusBadgeForState value={row.status} /> },
              { key: "evidence", header: "Evidence", width: "110px", render: (row) => <span>{row.evidence_count}</span> },
              { key: "findings", header: "Finding", width: "110px", render: (row) => <span>{row.finding_count}</span> },
              { key: "updated", header: "更新", width: "140px", render: (row) => <RelativeTime value={row.updated_at} /> },
              {
                key: "ops",
                header: "操作",
                width: "190px",
                render: (row) => (
                  <span className="toolbar">
                    <Link className="btn btn--sm" to={`/investigations/${row.id}/findings`}>
                      Findings
                    </Link>
                    <Link className="btn btn--sm" to={`/investigations/${row.id}/workbench`}>
                      工作台
                    </Link>
                  </span>
                ),
              },
            ]}
          />
        ) : null}
      </Card>
    </PageSection>
  );
}

function CaseReportsTab({ caseId }: { caseId: string }) {
  const { data, loading, error, reload } = useAsync<ReportRow[]>(`/api/v2/cases/${caseId}/reports`, [caseId]);
  const investigations = useAsync<InvestigationRow[]>("/api/v2/investigations", [caseId], { case_id: caseId, limit: 200 });
  const navigate = useNavigate();
  const { can } = useAuth();
  const action = useAction();
  const dialog = useDialog();
  const rows = data ?? [];

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
    <PageSection
      title="案件报告"
      count={`${rows.length} 份`}
      actions={
        <Button size="sm" disabled={!can("report:create")} busy={action.busy} busyLabel="生成中…" onClick={generate}>
          生成报告
        </Button>
      }
    >
      <Card flush>
        <DataTable<ReportRow>
          loading={loading}
          error={error}
          onRetry={reload}
          rows={rows}
          rowKey={(report) => report.id}
          caption="案件报告列表"
          columns={[
            { key: "revision", header: "revision", width: "100px", render: (report) => <span className="mono">{"r" + report.revision}</span> },
            { key: "status", header: "状态", width: "140px", render: (report) => <StatusBadgeForState value={report.status} /> },
            { key: "hash", header: "SHA-256", render: (report) => <HashValue value={report.content_hash} label="报告内容哈希" /> },
            { key: "size", header: "大小", width: "110px", render: (report) => <span>{report.size_bytes + " B"}</span> },
            { key: "generated", header: "生成时间", width: "170px", render: (report) => <span>{formatTime(report.generated_at)}</span> },
            {
              key: "ops",
              header: "操作",
              width: "180px",
              render: (report) => (
                <span className="toolbar">
                  <Button size="sm" onClick={() => navigate(`/reports/${report.id}`)}>
                    打开
                  </Button>
                  <Button
                    size="sm"
                    disabled={!["approved", "signed", "archived"].includes(report.status)}
                    onClick={() => void download(report)}
                  >
                    下载
                  </Button>
                </span>
              ),
            },
          ]}
        />
      </Card>
    </PageSection>
  );
}

function ActivityTab({ caseId }: { caseId: string }) {
  const { data, loading, error, reload } = useAsync<Activity[]>(`/api/v2/cases/${caseId}/activity`, [caseId], { limit: 200 });
  const rows = data ?? [];
  return (
    <PageSection title="案件动态" count={`${rows.length} 条`}>
      <Card>
        {loading ? <SkeletonRows rows={5} columns={3} /> : null}
        {!loading && error ? <ErrorState title="动态加载失败" message={error} onRetry={reload} /> : null}
        {!loading && !error && rows.length === 0 ? <EmptyState title="暂无业务动态" /> : null}
        {!loading && !error && rows.length > 0 ? (
          <Timeline
            ariaLabel="案件动态时间线"
            items={rows.map((item) => ({
              key: item.id,
              title: item.summary,
              meta: (
                <span>
                  {item.event_type} · {item.actor_display || "system"} · {formatTime(item.created_at)}
                  {item.object_type ? " · 对象 " : ""}
                  {item.object_type ? <span className="mono">{item.object_type}</span> : null}
                  {item.object_id ? <HashValue value={item.object_id} label="对象 ID" /> : null}
                </span>
              ),
            }))}
          />
        ) : null}
      </Card>
    </PageSection>
  );
}
