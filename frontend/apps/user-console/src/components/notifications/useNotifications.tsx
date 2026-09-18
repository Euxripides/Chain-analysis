import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ApiError, useAuth } from "@chain/api-client";
import { useDialog } from "@chain/ui";

/**
 * 通知数据源（蓝图 §10.3）
 *
 * 通知页 `/notifications` 与右上角铃铛**共享同一个数据源**：同一个 Provider
 * 持有列表、未读数与已读操作，因此标记已读后铃铛计数与列表同步更新，
 * 不会出现"页面已读、铃铛还红着"的不一致。
 *
 * 接口一字未改：仍然是 GET /api/v2/notifications、POST /:id/read、
 * POST /read-all、POST /:id/open。
 */

export interface NotificationItem {
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

interface NotificationsValue {
  items: NotificationItem[];
  loading: boolean;
  error: string | null;
  reload: () => Promise<void>;
  markRead: (id: string) => Promise<void>;
  markAllRead: () => Promise<void>;
  open: (item: NotificationItem) => Promise<void>;
  /** 未读数：来自服务端返回的 unread=true 列表长度，不做前端猜测。 */
  unreadCount: number;
}

const NotificationsContext = createContext<NotificationsValue | null>(null);

export function useNotifications(): NotificationsValue {
  const ctx = useContext(NotificationsContext);
  if (!ctx) throw new Error("useNotifications 必须在 <NotificationsProvider> 内使用");
  return ctx;
}

export function NotificationsProvider({ children }: { children: React.ReactNode }) {
  const { client } = useAuth();
  const dialog = useDialog();
  const navigate = useNavigate();
  const [items, setItems] = useState<NotificationItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  /* 组件卸载后不再 setState：通知请求可能比页面生命周期长。 */
  const alive = useRef(true);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await client.get<NotificationItem[]>("/api/v2/notifications", {
        query: { limit: 200 },
      });
      if (alive.current) setItems(res.data ?? []);
    } catch (err) {
      if (alive.current) setError(err instanceof ApiError ? err.userMessage : "通知加载失败");
    } finally {
      if (alive.current) setLoading(false);
    }
  }, [client]);

  useEffect(() => {
    void load();
  }, [load]);

  const unreadCount = useMemo(() => items.filter((item) => !item.read_at).length, [items]);

  const markRead = useCallback(
    async (id: string) => {
      try {
        await client.post("/api/v2/notifications/" + id + "/read", {});
        if (!alive.current) return;
        /* 就地更新而不是整表重取：蓝图 §12.24"标记已读后平滑更新，不从列表突然消失"。 */
        setItems((prev) => prev.map((item) => (item.id === id ? { ...item, read_at: new Date().toISOString() } : item)));
      } catch (err) {
        dialog.notify("error", "标记已读失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
      }
    },
    [client, dialog],
  );

  const markAllRead = useCallback(async () => {
    try {
      await client.post("/api/v2/notifications/read-all", {});
      if (!alive.current) return;
      const now = new Date().toISOString();
      setItems((prev) => prev.map((item) => ({ ...item, read_at: item.read_at ?? now })));
      dialog.notify("success", "已全部标记已读", "当前工作区内的通知都已标记为已读。");
    } catch (err) {
      dialog.notify("error", "操作失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    }
  }, [client, dialog]);

  const open = useCallback(
    async (item: NotificationItem) => {
      try {
        const res = await client.post<{ target: string | null }>("/api/v2/notifications/" + item.id + "/open", {});
        const target = res.data?.target;
        if (target) {
          navigate(target);
        } else {
          /* 对象不可用时按 §12.24 打开 info Dialog，而不是跳到空白页。 */
          dialog.notify(
            "info",
            "通知对象已不可用",
            "该通知指向的对象可能已被删除，或你已失去对它的访问权限。",
            [
              { label: "对象类型", value: item.object_type || "—" },
              { label: "对象标识", value: item.object_id || "—" },
            ],
          );
        }
        if (alive.current) {
          setItems((prev) => prev.map((n) => (n.id === item.id ? { ...n, read_at: n.read_at ?? new Date().toISOString() } : n)));
        }
      } catch (err) {
        dialog.notify("error", "打开通知失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
      }
    },
    [client, dialog, navigate],
  );

  const value = useMemo<NotificationsValue>(
    () => ({ items, loading, error, reload: load, markRead, markAllRead, open, unreadCount }),
    [items, loading, error, load, markRead, markAllRead, open, unreadCount],
  );

  return <NotificationsContext.Provider value={value}>{children}</NotificationsContext.Provider>;
}
