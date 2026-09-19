import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { ArrowRight, FolderOpen, Inbox, ListChecks, SlidersHorizontal } from "lucide-react";
import { ApiError, useAuth, type UserDashboard } from "@chain/api-client";
import {
  BentoCell,
  BentoGrid,
  Card,
  CompactProgress,
  EmptyState,
  ErrorState,
  MetaLine,
  PageSection,
  SkeletonRows,
  StatusBadgeForState,
  progressStateFor,
  RelativeTime,
  useDialog,
} from "@chain/ui";
import { CsPage } from "@chain/user-ui";
import { useAsync } from "../sprint4/common";
import { HomeHero } from "./home/HomeHero";
import { KpiStrip } from "./home/KpiStrip";

/**
 * §12.2 我的概览 —— Bento 工作台
 *
 * 2026-09 V4 重构（规范 §32.1）：首屏结构从"Header + 6 张 Bento 卡"改成
 *   Hero / Quick Investigation  →  KPI strip  →  进行中  →  待我处理  →  最近案件  →  快捷操作
 *
 * 三处**只改表现、不改数据**的调整：
 *   1. PageHeader 换成 HomeHero（同一份问候语 + 同样的权限裁剪主操作），
 *      并补上规范 §32.2 要求的全局检索入口——提交后跳 `/search?q=`，
 *      这是 /search **已经支持**的深链接参数，不是新增契约；
 *   2. "关键指标"从 Bento 的 B 区提成独立 KPI 条（§32.3）：不再用 4 张超宽卡，
 *      而是列数随视口自适应的紧凑网格。**没有加 sparkline**——接口的 cards[]
 *      不含时间序列，规范 §64 #1/#25 禁止前端编造趋势数据；
 *   3. 删掉与 Header meta 完全重复的"工作区状态"卡片（§32.4 禁止空壳大卡），
 *      组织 / 工作区 / 统计范围 / 数据源仍然全部可以在"口径说明"弹窗里看到。
 *
 * 数据来源（一字未改，每一块都必须是接口真实返回）：

 * 数据来源说明（每一块都必须是接口真实返回，不写假数据）：
 *   A 问候 / 工作区 / 数据时间 / 主操作 ← GET /api/v2/dashboard（as_of · scope · data_source）
 *   B 关键业务指标                     ← GET /api/v2/dashboard（cards，原样显示，不做前端二次计算）
 *   C 进行中的调查与下载任务           ← GET /api/v2/investigations?status=running · GET /api/v2/jobs
 *   D 待我处理                         ← GET /api/v2/inbox?status=pending
 *   E 最近案件                         ← GET /api/v2/cases?limit=5
 *   F 快捷操作                         ← GET /api/v2/dashboard（quick_actions，逐个按 permission 裁剪）
 *
 * C/D/E 使用**已有接口**并且逐块按权限裁剪：会话缺少对应权限时该块整体不渲染
 * （而不是渲染一个空壳或假数字）。每块独立处理加载/空/错误，单块失败不影响整页。
 */

export default function HomePage() {
  const { client, session, can } = useAuth();
  const dialog = useDialog();
  const [data, setData] = useState<UserDashboard | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const { data } = await client.get<UserDashboard>("/api/v2/dashboard");
      setData(data);
    } catch (err) {
      setError(err instanceof ApiError ? err.userMessage : "无法加载工作台数据");
    } finally {
      setLoading(false);
    }
  }, [client]);

  useEffect(() => {
    void load();
  }, [load]);

  const toneFor = (key: string): "ok" | "warn" | "danger" | "neutral" => {
    if (key === "jobs_failed") return (data?.cards.find((c) => c.key === key)?.value ?? 0) > 0 ? "danger" : "ok";
    if (key === "active_jobs") return (data?.cards.find((c) => c.key === key)?.value ?? 0) > 0 ? "warn" : "neutral";
    return "neutral";
  };

  /*
   * 口径说明的唯一入口（AGENTS.md：说明性内容只能走弹窗，不得内嵌页面正文）。
   * 重构前 PageHeader 的 subtitle 是一段页面内说明段落，与仓库规范冲突；
   * 那段话的信息量与"统计范围"完全一致，因此并入这个弹窗，
   * 用户能拿到的信息没有减少，页面正文则只剩业务数据与操作控件。
   */
  const explainScope = () =>
    dialog.notify("info", "工作台口径", "当前工作区内未归档对象的实时统计。每个指标的统计范围与来源见各指标卡上的说明按钮。", [
      { label: "组织", value: data?.scope.organization_id ?? "—" },
      { label: "工作区", value: session?.workspace_id ?? data?.scope.workspace_id ?? "—" },
      { label: "统计范围", value: "当前工作区内未归档对象" },
      { label: "数据源", value: data?.data_source ?? "—" },
      { label: "数据截至", value: data ? new Date(data.as_of).toLocaleString("zh-CN") : "—" },
    ]);

  const scopeMeta = data ? (
    <MetaLine
      items={[
        { label: "数据截至", value: new Date(data.as_of).toLocaleString("zh-CN") },
        { label: "数据源", value: data.data_source },
        { label: "工作区", value: session?.workspace_id ?? data.scope.workspace_id },
      ]}
    />
  ) : null;

  return (
    <CsPage kind="dashboard">
      <HomeHero
        displayName={session?.display_name ?? ""}
        canTraceFunds={can("canvas:list")}
        canCreateCase={can("case:create")}
        meta={scopeMeta}
        onExplainScope={explainScope}
      />

      {loading ? (
        <div className="cs-kpistrip">
          {Array.from({ length: 6 }).map((_, index) => (
            <Card key={index}>
              <SkeletonRows rows={2} columns={1} />
            </Card>
          ))}
        </div>
      ) : error ? (
        <Card>
          <ErrorState title="工作台数据加载失败" message={error} onRetry={load} />
        </Card>
      ) : !data ? (
        <Card>
          <EmptyState title="暂无数据" body="当前工作区还没有可统计的对象。" />
        </Card>
      ) : (
        <>
          {/* KPI 条（§32.3）：独立于 Bento，列数随视口自适应，不再用 4 张超宽空卡。 */}
          <KpiStrip
            cards={data.cards}
            toneFor={toneFor}
            noteFor={() => (
              <>
                该数值由服务端在当前工作区范围内聚合，界面不做二次计算。
                统计范围是「当前工作区内未归档对象」；口径变化会随接口响应一并更新。
              </>
            )}
          />

          <BentoGrid>
          {/* C 区：进行中的调查与下载任务。 */}
          <BentoCell area="c">
            <RunningWork canInvestigations={can("investigation:list")} canJobs={can("job:list:self")} />
          </BentoCell>

          {/* D 区：待我处理。 */}
          <BentoCell area="d">
            {can("inbox:read") ? <InboxPreview /> : <SectionUnavailable title="待我处理" permission="inbox:read" />}
          </BentoCell>

          {/* E 区：最近案件。 */}
          <BentoCell area="e">
            {can("case:list") ? <RecentCases /> : <SectionUnavailable title="最近案件" permission="case:list" />}
          </BentoCell>

          {/* F 区：快捷操作，继续由 quick_actions 和权限控制。 */}
          <BentoCell area="f">
            <PageSection title="快捷操作">
              <Card>
                {data.quick_actions.length === 0 ? (
                  <EmptyState title="没有可用的快捷入口" body="当前角色未被授予任何创建类权限。" />
                ) : (
                  <div className="toolbar">
                    {data.quick_actions.map((action) => {
                      if (action.href.startsWith("/admin")) {
                        return (
                          <a key={action.key} className="btn btn--sm" href={action.href}>
                            {action.label}
                          </a>
                        );
                      }
                      return (
                        <Link key={action.key} className="btn btn--sm" to={action.href}>
                          {action.label}
                        </Link>
                      );
                    })}
                  </div>
                )}
              </Card>
            </PageSection>
          </BentoCell>
          </BentoGrid>
        </>
      )}
    </CsPage>
  );
}

/* ------------------------------------------------------------------ C 区：进行中 */

interface InvestigationRow {
  id: string;
  goal: string;
  status: string;
  case_no?: string;
  finding_count: number;
  pending_findings: number;
  updated_at: string;
}

interface JobRow {
  id: string;
  job_no: string;
  status: string;
  chain: string;
  progress: number;
  addresses: { total: number; completed: number };
  downloaded_rows: number;
  updated_at: string;
}

/**
 * 进行中的工作。
 *
 * 两个子分区各自独立挂载：**没有权限的子分区根本不发请求**（而不是发一个用不上的请求
 * 再把结果藏起来），也没有权限时不渲染空壳。
 */
function RunningWork({ canInvestigations, canJobs }: { canInvestigations: boolean; canJobs: boolean }) {
  if (!canInvestigations && !canJobs) {
    return <SectionUnavailable title="进行中的工作" permission="investigation:list" />;
  }
  return (
    <div className="cs-bento-stack">
      {canInvestigations ? <RunningInvestigations /> : null}
      {canJobs ? <RunningJobs /> : null}
    </div>
  );
}

function RunningInvestigations() {
  const { data, loading, error, reload } = useAsync<InvestigationRow[]>("/api/v2/investigations", [], {
    status: "running",
    limit: 5,
  });
  const rows = data ?? [];
  return (
    <PageSection title="进行中的调查" count={rows.length + " 个"}>
      <Card>
        {loading ? <SkeletonRows rows={2} columns={3} /> : null}
        {!loading && error ? <ErrorState title="调查加载失败" message={error} onRetry={reload} /> : null}
        {!loading && !error && rows.length === 0 ? (
          <EmptyState
            title="当前没有进行中的调查"
            body="调查进入 running 状态后会自动出现在这里。"
            action={
              <Link className="btn btn--sm" to="/investigations">
                <SlidersHorizontal size={14} />
                调查任务
              </Link>
            }
          />
        ) : null}
        {!loading && !error && rows.length > 0 ? (
          <div className="cs-rowcards cs-stagger">
            {rows.map((row) => (
              <div className="cs-rowcard" key={row.id}>
                <div className="cs-rowcard__head">
                  <span className="cs-rowcard__title">{row.goal}</span>
                  <StatusBadgeForState value={row.status} />
                </div>
                <div className="cs-rowcard__meta">
                  {row.case_no ? <span>案件 {row.case_no}</span> : null}
                  <span>Finding {row.finding_count}</span>
                  <span>待复核 {row.pending_findings}</span>
                  <RelativeTime value={row.updated_at} />
                </div>
                <div className="cs-rowcard__actions">
                  <Link className="btn btn--sm" to={"/investigations/" + row.id + "/workbench"}>
                    进入工作台
                  </Link>
                  <Link className="btn btn--sm btn--ghost" to={"/investigations/" + row.id + "/findings"}>
                    Findings
                  </Link>
                </div>
              </div>
            ))}
          </div>
        ) : null}
      </Card>
    </PageSection>
  );
}

function RunningJobs() {
  const { data, loading, error, reload } = useAsync<{ items: JobRow[] }>("/api/v2/jobs", [], { limit: 5 });
  const rows = data?.items ?? [];
  return (
    <PageSection title="进行中的下载任务" count={rows.length + " 个"}>
      <Card>
        {loading ? <SkeletonRows rows={2} columns={3} /> : null}
        {!loading && error ? <ErrorState title="任务加载失败" message={error} onRetry={reload} /> : null}
        {!loading && !error && rows.length === 0 ? (
          <EmptyState
            title="当前没有进行中的下载任务"
            body="创建数据下载后，任务进度会实时出现在这里。"
            action={
              <Link className="btn btn--sm" to="/jobs">
                <ListChecks size={14} />
                任务中心
              </Link>
            }
          />
        ) : null}
        {!loading && !error && rows.length > 0 ? (
          <div className="cs-rowcards cs-stagger">
            {rows.map((row) => (
              <div className="cs-rowcard" key={row.id}>
                <div className="cs-rowcard__head">
                  <Link className="cs-rowcard__title" to={"/jobs/" + row.id}>
                    {row.job_no}
                  </Link>
                  <StatusBadgeForState value={row.status} />
                </div>
                <div className="cs-rowcard__meta">
                  <span>{row.chain}</span>
                  <span>
                    地址 {row.addresses?.completed ?? 0} / {row.addresses?.total ?? 0}
                  </span>
                  <span>rows {row.downloaded_rows ?? 0}</span>
                  <RelativeTime value={row.updated_at} />
                </div>
                {/* 服务端给出 progress 时按真实值显示；缺失时用 indeterminate，不伪造百分比。 */}
                <CompactProgress
                  percent={Number.isFinite(row.progress) ? row.progress * 100 : null}
                  state={progressStateFor(row.status)}
                  label={row.job_no}
                />
                <div className="cs-rowcard__actions">
                  <Link className="btn btn--sm" to={"/jobs/" + row.id}>
                    查看任务
                  </Link>
                </div>
              </div>
            ))}
          </div>
        ) : null}
      </Card>
    </PageSection>
  );
}

/* ------------------------------------------------------------------ D 区：待我处理 */

interface InboxItem {
  id: string;
  resource_type: string;
  resource_id: string;
  case_id?: string | null;
  investigation_id?: string | null;
  requester: string;
  status: string;
  reason: string;
  created_at: string;
  title: string;
}

function inboxTarget(item: InboxItem): string {
  switch (item.resource_type) {
    case "finding":
      return "/investigations/" + item.investigation_id + "/findings";
    case "report":
      return "/reports/" + item.resource_id;
    case "investigation":
      return "/investigations/" + item.resource_id + "/workbench";
    case "evidence":
      return "/investigations/" + item.investigation_id + "/evidence/" + item.resource_id;
    default:
      return "/inbox";
  }
}

function InboxPreview() {
  const { data, loading, error, reload } = useAsync<InboxItem[]>("/api/v2/inbox", [], { status: "pending", limit: 5 });
  const rows = data ?? [];
  return (
    <PageSection title="待我处理" count={rows.length + " 项"}>
      <Card>
        {loading ? <SkeletonRows rows={3} columns={2} /> : null}
        {!loading && error ? <ErrorState title="待办加载失败" message={error} onRetry={reload} /> : null}
        {!loading && !error && rows.length === 0 ? (
          <EmptyState
            title="没有待处理的复核请求"
            body="需要你确认的 Finding、报告或调查会出现在这里。"
            action={
              <Link className="btn btn--sm" to="/inbox">
                <Inbox size={14} />
                打开待我处理
              </Link>
            }
          />
        ) : null}
        {!loading && !error && rows.length > 0 ? (
          <div className="cs-rowcards">
            {rows.map((item) => (
              <div className="cs-rowcard" key={item.id}>
                <div className="cs-rowcard__head">
                  <span className="cs-rowcard__title">{item.title || item.resource_type}</span>
                  <StatusBadgeForState value={item.status} />
                </div>
                <div className="cs-rowcard__meta">
                  <span>{item.resource_type}</span>
                  <span>请求人 {item.requester || "system"}</span>
                  <RelativeTime value={item.created_at} />
                </div>
                {item.reason ? <div className="cs-rowcard__meta">{item.reason}</div> : null}
                <div className="cs-rowcard__actions">
                  <Link className="btn btn--sm btn--primary" to={inboxTarget(item)}>
                    处理
                    <ArrowRight size={13} />
                  </Link>
                </div>
              </div>
            ))}
          </div>
        ) : null}
        {!loading && !error && rows.length > 0 ? (
          <div className="cs-stickybar" style={{ paddingTop: "var(--sp-2)" }}>
            <span className="cs-stickybar__spacer" />
            <Link className="btn btn--sm btn--ghost" to="/inbox">
              查看全部待办
            </Link>
          </div>
        ) : null}
      </Card>
    </PageSection>
  );
}

/* ------------------------------------------------------------------ E 区：最近案件 */

interface CaseRow {
  id: string;
  case_no: string;
  name: string;
  status: string;
  updated_at: string;
}

function RecentCases() {
  const { data, loading, error, reload } = useAsync<CaseRow[]>("/api/v2/cases", [], { limit: 5 });
  const rows = data ?? [];
  return (
    <PageSection
      title="最近案件"
      count={rows.length + " 个"}
      actions={
        <Link className="btn btn--sm btn--ghost" to="/cases">
          全部案件
          <ArrowRight size={13} />
        </Link>
      }
    >
      <Card>
        {loading ? <SkeletonRows rows={3} columns={3} /> : null}
        {!loading && error ? <ErrorState title="案件加载失败" message={error} onRetry={reload} /> : null}
        {!loading && !error && rows.length === 0 ? (
          <EmptyState
            title="还没有案件"
            body="创建案件后可以在这里快速回到最近处理的对象。"
            action={
              <Link className="btn btn--sm" to="/cases">
                <FolderOpen size={14} />
                打开案件中心
              </Link>
            }
          />
        ) : null}
        {!loading && !error && rows.length > 0 ? (
          <div className="cs-rowcards">
            {rows.map((row) => (
              <div className="cs-rowcard" key={row.id}>
                <div className="cs-rowcard__head">
                  <Link className="cs-rowcard__title" to={"/cases/" + row.id + "/overview"}>
                    {row.name}
                  </Link>
                  <StatusBadgeForState value={row.status} />
                </div>
                <div className="cs-rowcard__meta">
                  <span className="mono">{row.case_no || "—"}</span>
                  <RelativeTime value={row.updated_at} />
                </div>
              </div>
            ))}
          </div>
        ) : null}
      </Card>
    </PageSection>
  );
}

/* ------------------------------------------------------------------ 权限不足的分区 */

/**
 * 无权限的分区占位。
 *
 * 这**不是**"提示横幅"：它表达的是"当前会话缺少某个权限，所以这块内容不存在"，
 * 与 `NoPermissionState` 同一语义（蓝图 §11.6 要求显示缺少的权限名，不伪装成 404）。
 * 如果整块都不该出现，调用方应直接不渲染它。
 */
function SectionUnavailable({ title, permission }: { title: string; permission: string }) {
  return (
    <PageSection title={title}>
      <Card>
        <div className="state">
          <ListChecks size={24} aria-hidden="true" style={{ color: "var(--c-amber-text)" }} />
          <div className="state__title">该分区需要额外权限</div>
          <div className="state__body">
            当前角色未被授予 <span className="mono">{permission}</span>，因此这里不展示对应数据。
          </div>
        </div>
      </Card>
    </PageSection>
  );
}