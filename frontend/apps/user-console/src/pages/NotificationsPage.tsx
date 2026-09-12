import { useCallback, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Card, EmptyState, ErrorState, PageHeader, SkeletonRows, useDialog } from "@chain/ui";
import { formatTime, useAction, useAsync } from "../sprint4/common";

interface Notification {
  id: string;
  type: string;
  title_key: string;
  title: string;
  body: string;
  object_type: string;
  object_id: string;
  href: string;
  read_at?: string | null;
  created_at: string;
}

export default function NotificationsPage() {
  const [unread, setUnread] = useState(false);
  const { data, loading, error, reload } = useAsync<Notification[]>("/api/v2/notifications", [unread], { limit: 200, unread: unread ? "true" : undefined });
  const action = useAction();
  const dialog = useDialog();
  const navigate = useNavigate();

  const open = useCallback(
    async (n: Notification) => {
      const res = await action.run(() => action.client.post<{ target: string | null }>(`/api/v2/notifications/${n.id}/open`, {}), {});
      if (!res) return;
      if (res.data.target) {
        navigate(res.data.target);
      } else {
        dialog.notify("info", "通知对象已不可用", "对象可能已删除或你已失去访问权限。");
      }
      void reload();
    },
    [action, dialog, navigate, reload],
  );

  return (
    <>
      <PageHeader
        title="通知"
        subtitle={`${(data ?? []).length} 条通知 · 数据源 postgres:notifications`}
        actions={
          <span className="s4-button-row">
            <button type="button" className="btn btn--sm" onClick={() => setUnread((v) => !v)}>
              {unread ? "显示全部" : "只看未读"}
            </button>
            <button
              type="button"
              className="btn btn--sm"
              onClick={() =>
                action.run(() => action.client.post("/api/v2/notifications/read-all", {}), { successTitle: "已全部标记已读", onDone: reload })
              }
            >
              全部标记已读
            </button>
          </span>
        }
      />
      <Card>
        {loading ? <SkeletonRows rows={5} columns={3} /> : null}
        {error ? <ErrorState title="通知加载失败" message={error} onRetry={reload} /> : null}
        {!loading && !error && (data ?? []).length === 0 ? <EmptyState title="没有通知" /> : null}
        <div className="s4-list">
          {(data ?? []).map((n) => (
            <div className="s4-list__item" key={n.id}>
              <div className="s4-list__main">
                <div className="s4-list__title">
                  {n.title || n.title_key}
                  {!n.read_at ? <span className="badge badge--info" style={{ marginLeft: 8 }}>未读</span> : null}
                </div>
                <div className="s4-list__sub">
                  {n.type} · {formatTime(n.created_at)} {n.body ? `· ${n.body}` : ""}
                </div>
              </div>
              <span className="s4-list__actions">
                <button type="button" className="btn btn--sm btn--primary" disabled={!n.href} onClick={() => open(n)}>
                  查看对象
                </button>
                <button type="button" className="btn btn--sm" disabled={Boolean(n.read_at)} onClick={() => action.run(() => action.client.post(`/api/v2/notifications/${n.id}/read`, {}), { successTitle: "已标记已读", onDone: reload })}>
                  标记已读
                </button>
              </span>
            </div>
          ))}
        </div>
      </Card>
    </>
  );
}
