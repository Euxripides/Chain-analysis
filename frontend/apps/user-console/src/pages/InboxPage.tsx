import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { FileText, ScanSearch, ShieldCheck, SlidersHorizontal } from "lucide-react";
import { useAuth } from "@chain/api-client";
import {
  Button,
  Card,
  EmptyState,
  ErrorState,
  HashValue,
  InspectorDrawer,
  KeyValueList,
  MetaLine,
  PageHeader,
  PageSection,
  PageSkeleton,
  RelativeTime,
  SegmentedControl,
  SplitPane,
  StatusBadgeForState,
  useDialog,
} from "@chain/ui";
import { formatTime, useAsync } from "../sprint4/common";

/**
 * §12.21 待我处理
 *
 * 数据来自 `GET /api/v2/inbox?status=pending`（服务端按 created_at 倒序，
 * 只返回"指派给我或未指派"的 pending 复核请求），**不新增任何请求参数**。
 *
 * 桌面为"列表 + 右侧快速预览"（SplitPane + InspectorDrawer），
 * <900px 直接进入目标详情（蓝图 §12.21）。
 */

interface InboxItem {
  id: string;
  resource_type: string;
  resource_id: string;
  case_id?: string | null;
  investigation_id?: string | null;
  requested_by?: string | null;
  requester: string;
  reviewer_id?: string | null;
  status: string;
  reason: string;
  revision: number;
  created_at: string;
  title: string;
}

/**
 * 复核请求 → 应用内目标路径。
 *
 * 修正：finding 原先指向 `/findings/{id}`，而 App.tsx 里**没有**这条路由
 * （用户端的 Finding 复核页是 `/investigations/:id/findings`），
 * 点击后会落到"页面不存在"。这里改为与路由表一致的真实目标；
 * HomePage 的待办预览早已使用同一形状，两边现在一致。
 *
 * 当服务端没有给出所需的 `investigation_id` 时返回空串（调用方渲染为不可用按钮），
 * 而不是拼出一个会 404 的假路径。
 */
function targetFor(item: InboxItem): string {
  switch (item.resource_type) {
    case "finding":
      return item.investigation_id ? `/investigations/${item.investigation_id}/findings` : "";
    case "report":
      return `/reports/${item.resource_id}`;
    case "investigation":
      return `/investigations/${item.resource_id}/workbench`;
    case "evidence":
      return item.investigation_id ? `/investigations/${item.investigation_id}/evidence/${item.resource_id}` : "";
    default:
      return "";
  }
}

const RESOURCE_LABEL: Record<string, string> = {
  report: "报告",
  evidence: "Evidence",
  finding: "Finding",
  investigation: "调查",
};

const REVIEW_STATUS_LABEL: Record<string, string> = {
  pending: "待处理",
  approved: "已批准",
  rejected: "已驳回",
  cancelled: "已取消",
};

/**
 * 视图排序用的对象类型顺序。
 *
 * 服务端没有 priority 字段，因此这里**不编造优先级数值**：只有一条显式、
 * 可见的排序规则（下游产出被阻塞的对象优先），规则本身通过"口径说明"Dialog 明示。
 */
const RESOURCE_PRIORITY: Record<string, number> = { report: 0, evidence: 1, finding: 2, investigation: 3 };

function resourceIcon(type: string) {
  if (type === "report") return <FileText size={13} aria-hidden="true" />;
  if (type === "evidence") return <ShieldCheck size={13} aria-hidden="true" />;
  if (type === "finding") return <ScanSearch size={13} aria-hidden="true" />;
  return <SlidersHorizontal size={13} aria-hidden="true" />;
}

/** <900px 直接进入目标详情：桌面才挂右侧快速预览（与 responsive.css 的断点一致）。 */
function useIsNarrow(): boolean {
  const [narrow, setNarrow] = useState(
    () => typeof window !== "undefined" && window.matchMedia("(max-width: 899px)").matches,
  );
  useEffect(() => {
    if (typeof window === "undefined") return;
    const query = window.matchMedia("(max-width: 899px)");
    const update = () => setNarrow(query.matches);
    update();
    query.addEventListener("change", update);
    return () => query.removeEventListener("change", update);
  }, []);
  return narrow;
}

export default function InboxPage() {
  const { can } = useAuth();
  const dialog = useDialog();
  const narrow = useIsNarrow();
  const [order, setOrder] = useState("priority");
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const { data, loading, error, reload } = useAsync<InboxItem[]>("/api/v2/inbox", [], { status: "pending", limit: 200 });

  const rows = useMemo(() => {
    const list = [...(data ?? [])];
    if (order === "time") return list;
    return list.sort((a, b) => {
      const pa = RESOURCE_PRIORITY[a.resource_type] ?? 9;
      const pb = RESOURCE_PRIORITY[b.resource_type] ?? 9;
      if (pa !== pb) return pa - pb;
      return new Date(b.created_at).getTime() - new Date(a.created_at).getTime();
    });
  }, [data, order]);

  const selected = rows.find((item) => item.id === selectedId) ?? null;

  const openScope = () =>
    dialog.notify("info", "待我处理口径", null, [
      { label: "接口", value: "GET /api/v2/inbox?status=pending" },
      { label: "范围", value: "指派给我或未指派的 pending 复核请求" },
      { label: "服务端排序", value: "created_at 倒序" },
      { label: "优先级顺序", value: "报告 → Evidence → Finding → 调查；同类型按提交时间倒序" },
      { label: "时间顺序", value: "完全采用接口返回顺序（created_at 倒序）" },
    ]);

  const list = (
    <PageSection title="待处理请求" count={rows.length + " 项"}>
      <Card>
        {loading ? <PageSkeleton shape="cards" rows={3} /> : null}
        {!loading && error ? <ErrorState title="Inbox 加载失败" message={error} onRetry={reload} /> : null}
        {!loading && !error && rows.length === 0 ? (
          <EmptyState
            title="没有待处理的复核请求"
            body="需要你确认的 Finding、报告、Evidence 或调查会出现在这里。"
            action={
              <span className="toolbar">
                {can("investigation:list") ? (
                  <Link className="btn btn--sm" to="/investigations">
                    调查任务
                  </Link>
                ) : null}
                {can("report:list") ? (
                  <Link className="btn btn--sm" to="/reports">
                    报告中心
                  </Link>
                ) : null}
              </span>
            }
          />
        ) : null}
        {!loading && !error && rows.length > 0 ? (
          <div className="cs-rowcards cs-stagger">
            {rows.map((item) => {
              const target = targetFor(item);
              const isSelected = item.id === selectedId;
              return (
                <div
                  className="cs-rowcard"
                  key={item.id}
                  data-selected={isSelected ? "true" : undefined}
                  style={isSelected ? { borderColor: "var(--interactive)" } : undefined}
                >
                  <div className="cs-rowcard__head">
                    <span className="cs-objecthead__kind">
                      {resourceIcon(item.resource_type)}
                      {RESOURCE_LABEL[item.resource_type] ?? item.resource_type}
                    </span>
                    <span className="cs-rowcard__title">{item.title || item.resource_type}</span>
                    <span title={item.status}>
                      <StatusBadgeForState value={item.status} label={REVIEW_STATUS_LABEL[item.status] ?? item.status} />
                    </span>
                  </div>
                  <div className="cs-rowcard__meta">
                    <span style={{ overflowWrap: "anywhere", minWidth: 0 }}>发起人 {item.requester || "system"}</span>
                    <RelativeTime value={item.created_at} />
                    {item.investigation_id ? (
                      <span className="mono" title={item.investigation_id}>
                        调查 {item.investigation_id.slice(0, 8)}…
                      </span>
                    ) : null}
                    {item.reason ? <span style={{ overflowWrap: "anywhere", minWidth: 0 }}>{item.reason}</span> : null}
                  </div>
                  <div className="cs-rowcard__meta">
                    <span style={{ overflowWrap: "anywhere", minWidth: 0 }}>
                      进入目标
                      <span className="mono" style={{ marginLeft: 6 }} title={target}>
                        {target || "—"}
                      </span>
                    </span>
                  </div>
                  <div className="cs-rowcard__actions">
                    {!narrow ? (
                      <Button
                        size="sm"
                        variant={isSelected ? "secondary" : "ghost"}
                        aria-pressed={isSelected}
                        onClick={() => setSelectedId(isSelected ? null : item.id)}
                      >
                        快速预览
                      </Button>
                    ) : null}
                    {target ? (
                      <Link className="btn btn--sm btn--primary" to={target}>
                        处理
                      </Link>
                    ) : (
                      <button type="button" className="btn btn--sm" disabled>
                        处理
                      </button>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        ) : null}
      </Card>
    </PageSection>
  );

  return (
    <>
      <PageHeader
        title="待我处理"
        breadcrumb={[{ label: "工作台", to: "/home" }, { label: "待我处理" }]}
        meta={
          <MetaLine
            items={[
              { label: "待办", value: rows.length + " 项" },
              { label: "数据源", value: "postgres:review_requests" },
            ]}
          />
        }
        secondaryAction={
          <Button size="sm" onClick={openScope}>
            口径说明
          </Button>
        }
      />

      {!loading && !error && rows.length > 0 ? (
        <SegmentedControl
          ariaLabel="排序方式"
          value={order}
          onChange={setOrder}
          items={[
            { key: "priority", label: "优先级" },
            { key: "time", label: "时间" },
          ]}
        />
      ) : null}

      {narrow ? (
        list
      ) : (
        <SplitPane
          storageKey="cs-inbox-aside"
          asideLabel="待办预览"
          main={list}
          aside={
            selected ? (
              <InspectorDrawer
                title="待办预览"
                subtitle={RESOURCE_LABEL[selected.resource_type] ?? selected.resource_type}
                ariaLabel="待办快速预览"
                onClose={() => setSelectedId(null)}
                actions={
                  targetFor(selected) ? (
                    <Link className="btn btn--sm btn--primary" to={targetFor(selected)}>
                      处理
                    </Link>
                  ) : undefined
                }
              >
                <PageSection title="请求详情">
                  <KeyValueList
                    ariaLabel="待办请求详情"
                    items={[
                      { label: "对象类型", value: RESOURCE_LABEL[selected.resource_type] ?? selected.resource_type },
                      { label: "标题", value: selected.title || "—", wide: true },
                      { label: "对象标识", value: <HashValue value={selected.resource_id} wrap label="对象标识" />, wide: true },
                      { label: "状态", value: REVIEW_STATUS_LABEL[selected.status] ?? selected.status },
                      { label: "发起人", value: selected.requester || "system" },
                      { label: "发起人 ID", value: <HashValue value={selected.requested_by ?? ""} label="发起人身份 ID" /> },
                      { label: "复核人 ID", value: <HashValue value={selected.reviewer_id ?? ""} label="复核人身份 ID" /> },
                      { label: "案件", value: <HashValue value={selected.case_id ?? ""} label="案件 ID" /> },
                      { label: "调查", value: <HashValue value={selected.investigation_id ?? ""} label="调查 ID" /> },
                      { label: "提交时间", value: formatTime(selected.created_at) },
                      { label: "revision", value: <span className="mono">r{selected.revision}</span> },
                      { label: "进入目标", value: <span className="mono" style={{ overflowWrap: "anywhere" }}>{targetFor(selected) || "—"}</span>, wide: true },
                      { label: "请求原因", value: selected.reason || "—", wide: true },
                    ]}
                  />
                </PageSection>
              </InspectorDrawer>
            ) : undefined
          }
        />
      )}
    </>
  );
}
