import { useCallback, useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { useAuth } from "@chain/api-client";
import {
  Button,
  Card,
  CompactProgress,
  DataTable,
  EmptyState,
  EntityLink,
  ErrorState,
  EvidenceRail,
  HashValue,
  IconButton,
  InspectorDrawer,
  KeyValueList,
  MetaLine,
  PageHeader,
  PageSection,
  PageSkeleton,
  PipelineProgress,
  SkeletonRows,
  StatusBadge,
  StatusBadgeForState,
  TabPanel,
  Tabs,
  dialogInputValue,
  progressStateFor,
  statusToneFor,
  useDialog,
  type DataTableColumn,
  type PipelineStage,
} from "@chain/ui";
import { formatTime, useAction, useAsync } from "../sprint4/common";

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
interface RunStep {
  id: string;
  ordinal: number;
  action: string;
  capability: string;
  status: string;
  external_job_id?: string | null;
  error?: string;
  algorithm_version?: string;
}
interface Run {
  id: string;
  status: string;
  objective: string;
  checkpoint?: Record<string, unknown>;
  pending_data_jobs?: string[];
  steps?: RunStep[];
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

/** 与 CanvasPage 相同的本地断点读取方式：只在需要切换结构（tab / 三栏）时用 JS。 */
function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(
    () => typeof window !== "undefined" && window.matchMedia(query).matches,
  );
  useEffect(() => {
    if (typeof window === "undefined") return;
    const mql = window.matchMedia(query);
    const update = () => setMatches(mql.matches);
    update();
    mql.addEventListener("change", update);
    return () => mql.removeEventListener("change", update);
  }, [query]);
  return matches;
}

/** §18.1 U-INV-03：调查工作台；桌面三栏（阶段 / 主区 / Evidence Rail）。 */
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

  const isNarrow = useMediaQuery("(max-width: 767px)");
  const isWide = useMediaQuery("(min-width: 1600px)");
  const [railOpen, setRailOpen] = useState(true);
  const [tab, setTab] = useState("progress");
  const [currentFinding, setCurrentFinding] = useState<FindingRow | null>(null);
  const [inspected, setInspected] = useState<EvidenceRow | null>(null);

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

  /* 首次加载用与最终布局同形状的骨架；刷新保留旧数据，不做整页替换（蓝图 §11.1 / §11.2）。 */
  if (detail.loading && !detail.data) return <PageSkeleton shape="workbench" rows={3} />;
  if (detail.error && !detail.data) return <Card><ErrorState title="调查加载失败" message={detail.error} onRetry={detail.reload} /></Card>;
  const inv = detail.data;
  if (!inv) return <PageSkeleton shape="workbench" rows={3} />;

  const runs = runtime.data?.runs ?? [];
  const latest = runs[0];
  const subjectValue = String(inv.subject?.value ?? "");
  const subjectChain = String(inv.subject?.chain ?? "");
  const pendingJobs = latest?.pending_data_jobs ?? [];
  /* pending data jobs 表达的是"正在等数据"：RUNNING/QUEUED 的 run 显示为 waiting，其余状态原样保留。 */
  const runState = latest ? progressStateFor(latest.status) : "queued";
  const pendingState = runState === "running" || runState === "queued" ? "waiting" : runState;
  const refreshing = runtime.loading || findings.loading || evidence.loading;
  const evidenceRows = evidence.data ?? [];
  const findingRows = findings.data ?? [];

  /* 纯计算而不是 useMemo：本组件在上面有"首次加载 / 错误"的提前 return，
     hooks 必须全部位于 return 之前（React hooks 规则）。 */
  const stages: PipelineStage[] = [...(latest?.steps ?? [])]
    .sort((a, b) => a.ordinal - b.ordinal)
    .map((step) => ({
      key: step.id,
      label: `${step.ordinal}. ${step.action}`,
      state: progressStateFor(step.status),
      /* 服务端不返回步骤总量：显式 null（indeterminate），不伪造百分比（蓝图 §9.1）。 */
      percent: null,
      meta: (
        <span>
          <span className="badge badge--neutral">{step.capability}</span>
          {step.status ? ` · ${step.status}` : ""}
          {step.algorithm_version ? ` · ${step.algorithm_version}` : ""}
          {step.external_job_id ? ` · ${step.external_job_id}` : ""}
          {step.error ? ` · ${step.error}` : ""}
        </span>
      ),
    }));

  const findingColumns: Array<DataTableColumn<FindingRow>> = [
    { key: "title", header: "Finding", render: (row) => <span>{row.title}</span> },
    { key: "type", header: "类型", width: "170px", render: (row) => <span className="mono">{row.finding_type}</span> },
    { key: "nature", header: "nature", width: "110px", render: (row) => row.nature },
    {
      key: "confidence",
      header: "confidence",
      width: "110px",
      align: "right",
      render: (row) => <span className="num">{(row.confidence * 100).toFixed(0)}%</span>,
    },
    { key: "algorithm", header: "算法版本", width: "150px", render: (row) => <span className="mono">{row.algorithm_version}</span> },
    { key: "status", header: "状态", width: "140px", render: (row) => <StatusBadge tone={statusToneFor(row.status)}>{row.status}</StatusBadge> },
  ];

  const stagesPanel = (
    <PageSection title="阶段与步骤" count={latest ? `Run ${latest.id.slice(0, 8)}` : undefined}>
      {runtime.loading && !runtime.data ? (
        <Card>
          <SkeletonRows rows={4} columns={2} />
        </Card>
      ) : null}
      {runtime.error && !runtime.data ? (
        <Card>
          <ErrorState title="Runtime 加载失败" message={runtime.error} onRetry={runtime.reload} />
        </Card>
      ) : null}
      {runtime.data && runs.length === 0 ? (
        <Card>
          <EmptyState title="尚未启动运行" />
        </Card>
      ) : null}
      {latest ? (
        <Card>
          <KeyValueList
            items={[
              { label: "Run", value: <EntityLink value={latest.id} /> },
              { label: "Run 状态", value: <StatusBadgeForState value={latest.status} /> },
              { label: "Objective", value: latest.objective, wide: true },
              { label: "Checkpoint", value: <span className="mono">{JSON.stringify(latest.checkpoint ?? {})}</span>, wide: true },
              {
                label: "Pending data jobs",
                value: pendingJobs.length ? (
                  <span style={{ display: "flex", flexWrap: "wrap", gap: "var(--sp-2)" }}>
                    {pendingJobs.map((job) => (
                      <EntityLink key={job} value={job} />
                    ))}
                  </span>
                ) : (
                  "—"
                ),
                wide: true,
              },
            ]}
          />
        </Card>
      ) : null}
      {latest && pendingJobs.length > 0 ? (
        <Card>
          <CompactProgress percent={null} state={pendingState} label="等待数据" waitingFor={pendingJobs.join(", ")} />
        </Card>
      ) : null}
      {latest ? (
        <Card>
          {stages.length > 0 ? (
            <PipelineProgress stages={stages} title="步骤" ariaLabel="运行步骤" />
          ) : (
            <EmptyState title="该 Run 没有步骤" />
          )}
        </Card>
      ) : null}
    </PageSection>
  );

  const findingsPanel = (
    <PageSection title="Findings" count={`${findingRows.length} 条`}>
      <Card flush>
        {findings.loading && !findings.data ? <SkeletonRows rows={5} columns={4} /> : null}
        {findings.error && !findings.data ? (
          <ErrorState title="Findings 加载失败" message={findings.error} onRetry={findings.reload} />
        ) : null}
        {findings.data && findingRows.length === 0 ? <EmptyState title="暂无 Finding" /> : null}
        {findingRows.length > 0 ? (
          <DataTable
            columns={findingColumns}
            rows={findingRows}
            rowKey={(row) => row.id}
            caption="调查 Finding 列表"
            rowActivateLabel={(row) => `查看 Finding ${row.title}`}
            onRowActivate={(row) => setCurrentFinding(row)}
            rowTestId={(row) => `workbench-finding-row-${row.id}`}
          />
        ) : null}
      </Card>
      {currentFinding ? (
        <Card title="当前 Finding" actions={<Button size="sm" variant="ghost" onClick={() => setCurrentFinding(null)}>收起</Button>}>
          <KeyValueList
            items={[
              { label: "标题", value: currentFinding.title, wide: true },
              { label: "类型", value: currentFinding.finding_type },
              { label: "nature", value: currentFinding.nature },
              { label: "confidence", value: <span className="num">{(currentFinding.confidence * 100).toFixed(1)}%</span> },
              { label: "算法版本", value: currentFinding.algorithm_version },
              { label: "状态", value: <StatusBadge tone={statusToneFor(currentFinding.status)}>{currentFinding.status}</StatusBadge> },
            ]}
          />
          <div className="toolbar" style={{ marginTop: "var(--sp-3)" }}>
            <Button size="sm" onClick={() => navigate(`/investigations/${id}/findings`)}>
              Findings 复核
            </Button>
          </div>
        </Card>
      ) : null}
    </PageSection>
  );

  const evidencePanel = (
    <>
      {inspected ? (
        <InspectorDrawer
          title={inspected.title}
          subtitle={`${inspected.evidence_type} · ${inspected.status}`}
          ariaLabel="Evidence 检查器"
          onClose={() => setInspected(null)}
          actions={
            <Link className="btn btn--sm btn--primary" to={`/investigations/${id}/evidence/${inspected.id}`}>
              打开证据详情
            </Link>
          }
        >
          <KeyValueList
            items={[
              { label: "标题", value: inspected.title, wide: true },
              { label: "类型", value: inspected.evidence_type },
              { label: "状态", value: <StatusBadge tone={statusToneFor(inspected.status)}>{inspected.status}</StatusBadge> },
              { label: "内容 SHA-256", value: <HashValue value={inspected.content_hash} wrap label="内容哈希" />, wide: true },
              { label: "Evidence ID", value: <EntityLink value={inspected.id} />, wide: true },
            ]}
          />
        </InspectorDrawer>
      ) : null}
      <EvidenceRail
        title={isNarrow ? "Evidence" : "Evidence Rail"}
        items={evidenceRows.slice(0, 10).map((e) => ({
          label: `${e.evidence_type} · ${e.status} · ${e.id.slice(0, 8)}`,
          value: (
            <span style={{ display: "flex", flexDirection: "column", gap: 4, minWidth: 0 }}>
              <span style={{ display: "flex", alignItems: "center", gap: "var(--sp-2)", minWidth: 0 }}>
                <Link
                  to={`/investigations/${id}/evidence/${e.id}`}
                  title={e.title}
                  style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
                >
                  {e.title.length > 42 ? `${e.title.slice(0, 42)}…` : e.title}
                </Link>
                <IconButton
                  label={`打开证据检查器：${e.title}`}
                  icon={<ChevronRight size={14} />}
                  active={inspected?.id === e.id}
                  onClick={() => setInspected(e)}
                />
              </span>
              <HashValue value={e.content_hash} head={10} tail={8} label="内容哈希" />
            </span>
          ),
        }))}
      />
      {evidence.data && evidenceRows.length === 0 ? (
        <Card>
          <EmptyState title="暂无 Evidence" />
        </Card>
      ) : null}
    </>
  );

  return (
    <>
      <PageHeader
        title={inv.goal}
        breadcrumb={[
          { label: "调查任务", to: "/investigations" },
          ...(inv.case_id ? [{ label: inv.case_no || inv.case_id, to: `/cases/${inv.case_id}/overview` }] : []),
          { label: "调查工作台" },
        ]}
        status={<StatusBadgeForState value={inv.status} />}
        subtitle={subjectValue ? <HashValue value={subjectValue} wrap head={18} tail={12} label="调查对象地址" /> : undefined}
        meta={
          <MetaLine
            items={[
              { label: "链", value: subjectChain || "—" },
              { label: "模式", value: inv.mode },
              { label: "revision", value: `r${inv.revision}` },
              { label: "Findings", value: String(inv.finding_count) },
              { label: "Evidence", value: String(inv.evidence_count) },
              { label: "待复核", value: String(inv.pending_findings) },
              { label: "更新", value: formatTime(inv.updated_at) },
            ]}
          />
        }
        primaryAction={
          <>
            {can("investigation:start") && ["created", "failed"].includes(inv.status) ? (
              <Button size="sm" variant="primary" onClick={() => command("start")}>
                启动
              </Button>
            ) : null}
            {can("investigation:resume") && inv.status === "paused" ? (
              <Button size="sm" variant="primary" onClick={() => command("resume")}>
                恢复
              </Button>
            ) : null}
          </>
        }
        secondaryAction={
          <>
            {can("investigation:pause") && ["running", "waiting_data"].includes(inv.status) ? (
              <Button size="sm" onClick={() => command("pause")}>
                暂停
              </Button>
            ) : null}
            {can("investigation:retry") && inv.status === "failed" ? (
              <Button size="sm" onClick={() => command("retry")}>
                重试
              </Button>
            ) : null}
            {can("investigation:submit_review") && ["running", "waiting_data", "paused", "waiting_review"].includes(inv.status) ? (
              <Button size="sm" onClick={submitReview}>
                提交复核
              </Button>
            ) : null}
            {can("investigation:cancel") && !["cancelled", "completed"].includes(inv.status) ? (
              <Button size="sm" variant="danger" onClick={() => command("cancel")}>
                取消
              </Button>
            ) : null}
            <Button size="sm" onClick={() => navigate(`/investigations/${id}/findings`)}>
              Findings
            </Button>
            <Button size="sm" variant="ghost" busy={refreshing} onClick={reloadAll}>
              刷新
            </Button>
            {!isNarrow ? (
              <IconButton
                label={railOpen ? "折叠 Evidence 栏" : "展开 Evidence 栏"}
                icon={railOpen ? <ChevronRight size={16} /> : <ChevronLeft size={16} />}
                active={railOpen}
                onClick={() => setRailOpen((open) => !open)}
              />
            ) : null}
          </>
        }
      />

      {inv.case_id ? (
        <MetaLine
          items={[
            {
              label: "所属案件",
              value: (
                <Link to={`/cases/${inv.case_id}/overview`}>{inv.case_no || inv.case_id}</Link>
              ),
            },
            { label: "创建", value: formatTime(inv.created_at) },
          ]}
        />
      ) : (
        <MetaLine items={[{ label: "创建", value: formatTime(inv.created_at) }]} />
      )}

      {isNarrow ? (
        <>
          <Tabs
            items={[
              { key: "progress", label: "进度" },
              { key: "findings", label: "Findings", count: findingRows.length },
              /* 第三个 tab 就是 Evidence Rail 本身；沿用其名称，避免与既有桌面断言脱节。 */
              { key: "evidence", label: "Evidence Rail", count: evidenceRows.length },
            ]}
            value={tab}
            onChange={setTab}
            ariaLabel="调查工作台分区"
          />
          <TabPanel tabKey={tab}>
            {tab === "progress" ? stagesPanel : null}
            {tab === "findings" ? findingsPanel : null}
            {tab === "evidence" ? evidencePanel : null}
          </TabPanel>
        </>
      ) : (
        <div
          className="cs-workbench"
          data-evidence={railOpen ? "open" : "collapsed"}
          style={railOpen ? undefined : { gridTemplateColumns: isWide ? "minmax(280px, 340px) minmax(0, 1fr)" : "minmax(0, 1fr)" }}
        >
          <div className="cs-workbench__stages">{stagesPanel}</div>
          <div className="cs-workbench__main">{findingsPanel}</div>
          {railOpen ? <div className="cs-workbench__evidence">{evidencePanel}</div> : null}
        </div>
      )}
    </>
  );
}
