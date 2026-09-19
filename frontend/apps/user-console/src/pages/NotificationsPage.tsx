import { useMemo, useState } from "react";
import {
  Bell,
  Check,
  CheckCheck,
  ClipboardCheck,
  Download,
  FileText,
  ShieldCheck,
  UserPlus,
  XCircle,
} from "lucide-react";
import {
  Button,
  Card,
  EmptyState,
  ErrorState,
  MetaLine,
  PageSection,
  RelativeTime,
  SkeletonRows,
  TabPanel,
  Tabs,
} from "@chain/ui";
import { CsPageHeader, CsRefreshBar } from "@chain/user-ui";
import { useNotifications, type NotificationItem } from "../components/notifications/useNotifications";

/**
 * §12.24 通知
 *
 * 本页**不再自己请求** `/api/v2/notifications`：数据源来自
 * `NotificationsProvider`（顶栏铃铛与通知页共享同一份 items / 未读数 / 已读操作）。
 * 因此标记已读后列表与铃铛同步更新，不会出现"页面已读、铃铛还红着"。
 *
 * 接口、路径与请求体一字未改：读列表、`:id/read`、`read-all`、`:id/open` 全部由 hook 负责。
 * 类型筛选需要服务端 `type` 参数，而共享数据源不暴露该参数，因此只提供"全部 / 未读"两个 tab。
 */

/** 类型图标：形状通道，颜色缺失时仍可区分（蓝图 §14 不只靠颜色）。 */
function typeIcon(type: string) {
  if (type.startsWith("review.")) return <ClipboardCheck size={15} aria-hidden="true" />;
  if (type === "report.approved") return <Check size={15} aria-hidden="true" />;
  if (type === "report.rejected") return <XCircle size={15} aria-hidden="true" />;
  if (type.startsWith("report.")) return <FileText size={15} aria-hidden="true" />;
  if (type.startsWith("download.") || type.startsWith("job.") || type.startsWith("export.")) {
    return <Download size={15} aria-hidden="true" />;
  }
  if (type.startsWith("evidence.") || type.startsWith("finding.")) return <ShieldCheck size={15} aria-hidden="true" />;
  if (type.startsWith("case.")) return <UserPlus size={15} aria-hidden="true" />;
  return <Bell size={15} aria-hidden="true" />;
}

/**
 * 按**本地自然日**把通知分组（规范 §52：今天 / 昨天 / 更早）。
 *
 * 日界一律用浏览器本地时区，与页面里 `formatTime` / `RelativeTime` 的口径一致——
 * 规范 §63B 要求不得静默改变全站时区语义，所以这里不做任何 UTC 换算。
 * 分组只是展示层：不新增请求参数，也不改变任何条目字段。
 */
const DAY_LABELS: Array<{ key: string; label: string }> = [
  { key: "today", label: "今天" },
  { key: "yesterday", label: "昨天" },
  { key: "earlier", label: "更早" },
];

function dayKeyOf(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return "earlier";
  const startOfToday = new Date();
  startOfToday.setHours(0, 0, 0, 0);
  const startOfYesterday = new Date(startOfToday);
  startOfYesterday.setDate(startOfYesterday.getDate() - 1);
  if (at >= startOfToday) return "today";
  if (at >= startOfYesterday) return "yesterday";
  return "earlier";
}

function groupByDay(items: NotificationItem[]): Array<{ key: string; label: string; items: NotificationItem[] }> {
  const buckets = new Map<string, NotificationItem[]>();
  for (const item of items) {
    const key = dayKeyOf(item.created_at);
    const bucket = buckets.get(key);
    if (bucket) bucket.push(item);
    else buckets.set(key, [item]);
  }
  return DAY_LABELS.filter((day) => (buckets.get(day.key)?.length ?? 0) > 0).map((day) => ({
    key: day.key,
    label: day.label,
    /* 组内保持服务端返回的顺序（列表接口已按时间倒序），前端不再重排。 */
    items: buckets.get(day.key) ?? [],
  }));
}

export default function NotificationsPage() {
  const { items, loading, error, reload, markRead, markAllRead, open, unreadCount } = useNotifications();
  const [tab, setTab] = useState("all");
  const [markingAll, setMarkingAll] = useState(false);

  /* 未读 tab 是对共享数据源的**视图筛选**，不发新请求；标记已读后该项自然离开未读视图。 */
  const visible = useMemo(() => (tab === "unread" ? items.filter((item) => !item.read_at) : items), [items, tab]);
  const groups = useMemo(() => groupByDay(visible), [visible]);

  /*
   * 首次加载 vs 刷新（§23.3）：共享 provider 的 loading 在每次 reload 都会置真，
   * 但不清空 items，因此 `items.length === 0` 才是"真的还没有数据"。
   */
  const firstLoad = loading && items.length === 0;
  const refreshing = loading && items.length > 0;

  const onMarkAll = async () => {
    setMarkingAll(true);
    try {
      await markAllRead();
    } finally {
      setMarkingAll(false);
    }
  };

  return (
    <>
      <CsPageHeader
        title="通知"
        breadcrumb={[{ label: "工作台", to: "/home" }, { label: "通知" }]}
        meta={
          <MetaLine
            items={[
              { label: "通知", value: items.length + " 条" },
              { label: "未读", value: unreadCount + " 条" },
              { label: "数据源", value: "postgres:notifications" },
            ]}
          />
        }
        primaryAction={
          <Button size="sm" busy={markingAll} busyLabel="标记中…" disabled={unreadCount === 0} onClick={() => void onMarkAll()}>
            <CheckCheck size={14} aria-hidden="true" />
            全部标记已读
          </Button>
        }
      />

      <PageSection title="通知列表" count={visible.length + " 条"}>
        <Tabs
          ariaLabel="通知筛选"
          value={tab}
          onChange={setTab}
          items={[
            { key: "all", label: "全部", count: items.length },
            { key: "unread", label: "未读", count: unreadCount },
          ]}
        />
        <Card>
          <TabPanel tabKey={tab}>
            <CsRefreshBar active={refreshing} />
            {firstLoad ? <SkeletonRows rows={5} columns={3} /> : null}
            {!firstLoad && error ? <ErrorState title="通知加载失败" message={error} onRetry={() => void reload()} /> : null}
            {!firstLoad && !error && visible.length === 0 ? (
              <EmptyState
                title="没有通知"
                body={tab === "unread" ? "当前没有未读通知。" : "当前工作区内还没有通知。"}
                action={
                  tab === "unread" ? (
                    <Button size="sm" onClick={() => setTab("all")}>
                      查看全部
                    </Button>
                  ) : undefined
                }
              />
            ) : null}
            {!error && visible.length > 0 ? (
              /* 按时间分组（§52）。分组标题参与 stagger，但**不逐条**给几百条通知做入场动画。 */
              <div className="cs-notifgroups">
                {groups.map((group) => (
                  <section className="cs-notifgroup" key={group.key} aria-label={group.label}>
                    <h3 className="cs-notifgroup__label">
                      {group.label}
                      <span className="cs-notifgroup__count">{group.items.length}</span>
                    </h3>
                    <div className="cs-rowcards">
                      {group.items.map((item) => (
                        <NotificationRow
                          key={item.id}
                          item={item}
                          onOpen={() => void open(item)}
                          onMarkRead={() => void markRead(item.id)}
                        />
                      ))}
                    </div>
                  </section>
                ))}
              </div>
            ) : null}
          </TabPanel>
        </Card>
      </PageSection>
    </>
  );
}

/**
 * 单条通知。
 *
 * 行本身不是按钮（否则内部按钮会成为嵌套交互元素）：标题按钮与"查看对象"都按现有
 * `target` 跳转；对象不可用时由共享 hook 内的 info Dialog 处理（页面不自己发请求）。
 * 标记已读后就地更新（hook 内部 setItems），不会让条目从"全部"列表里突然消失。
 */
function NotificationRow({
  item,
  onOpen,
  onMarkRead,
}: {
  item: NotificationItem;
  onOpen: () => void;
  onMarkRead: () => void;
}) {
  const unread = !item.read_at;
  return (
    <div className="cs-rowcard" data-unread={unread ? "true" : "false"}>
      <div className="cs-rowcard__head">
        <span className="cs-objecthead__kind">{typeIcon(item.type)}</span>
        <button
          type="button"
          className="cs-rowcard__title"
          style={{ border: 0, background: "transparent", padding: 0, textAlign: "left", cursor: "pointer", color: "inherit" }}
          onClick={onOpen}
          title={item.title || item.title_key}
        >
          {item.title || item.title_key}
        </button>
        {/* 未读是「形状 + 文字」双通道：圆点是给扫读的，徽标文字是给读屏与灰度打印的（§55）。 */}
        <span className="cs-notifgroup__dot" data-unread={unread ? "true" : "false"} aria-hidden="true" />
        {unread ? <span className="badge badge--info">未读</span> : <span className="visually-hidden">已读</span>}
      </div>
      <div className="cs-rowcard__meta">
        <span>{item.type}</span>
        {item.object_type ? (
          <span className="mono" title={item.object_id}>
            {item.object_type} {item.object_id ? item.object_id.slice(0, 8) + "…" : ""}
          </span>
        ) : null}
        <RelativeTime value={item.created_at} />
        {item.body ? <span style={{ overflowWrap: "anywhere", minWidth: 0 }}>{item.body}</span> : null}
      </div>
      <div className="cs-rowcard__actions">
        <Button size="sm" variant="primary" disabled={!item.href} onClick={onOpen}>
          查看对象
        </Button>
        <Button size="sm" disabled={!unread} onClick={onMarkRead}>
          标记已读
        </Button>
      </div>
    </div>
  );
}
