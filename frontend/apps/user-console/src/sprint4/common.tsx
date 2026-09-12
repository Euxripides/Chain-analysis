import { useCallback, useEffect, useRef, useState } from "react";
import { NavLink } from "react-router-dom";
import { ApiError, useAuth } from "@chain/api-client";
import { useDialog } from "@chain/ui";

/** Sprint 4 通用异步读取 hook：所有页面都直接读真实 API，不用占位数据。 */
export function useAsync<T>(path: string, deps: unknown[] = [], query?: Record<string, string | number | boolean | undefined | null>) {
  const { client } = useAuth();
  const [data, setData] = useState<T | null>(null);
  const [meta, setMeta] = useState<{ total?: number; next_cursor?: string | null }>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const queryKey = JSON.stringify(query ?? {});

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await client.get<T>(path, query ? { query } : undefined);
      setData(res.data);
      setMeta({ total: res.meta.total, next_cursor: res.meta.next_cursor });
    } catch (err) {
      setError(err instanceof ApiError ? err.userMessage : "加载失败");
    } finally {
      setLoading(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, path, queryKey, ...deps]);

  useEffect(() => {
    void load();
  }, [load]);
  return { data, meta, loading, error, reload: load };
}

export function useAction() {
  const { client } = useAuth();
  const dialog = useDialog();
  const [busy, setBusy] = useState(false);

  const run = useCallback(
    async <T,>(
      fn: () => Promise<T>,
      opts: { successTitle?: string; successBody?: string; onDone?: (v: T) => void } = {},
    ): Promise<T | null> => {
      setBusy(true);
      try {
        const result = await fn();
        if (opts.successTitle) dialog.notify("success", opts.successTitle, opts.successBody);
        opts.onDone?.(result);
        return result;
      } catch (err) {
        if (err instanceof ApiError) {
          dialog.notify("error", "操作失败", err.userMessage, err.traceId ? [{ label: "Trace", value: err.traceId }] : undefined);
        } else {
          dialog.notify("error", "操作失败", err instanceof Error ? err.message : "请稍后重试");
        }
        return null;
      } finally {
        setBusy(false);
      }
    },
    [client, dialog],
  );
  return { busy, run, client, dialog };
}

export interface TabDef {
  key: string;
  label: string;
  to: string;
  permission?: string;
}

export function DeepTabs({ tabs }: { tabs: TabDef[] }) {
  const { can } = useAuth();
  return (
    <nav className="s4-tabs" aria-label="页面分区">
      {tabs
        .filter((t) => !t.permission || can(t.permission))
        .map((t) => (
          <NavLink key={t.key} to={t.to} end className={({ isActive }) => `s4-tab${isActive ? " is-active" : ""}`}>
            {t.label}
          </NavLink>
        ))}
    </nav>
  );
}

export function KeyValues({ items }: { items: Array<{ label: string; value: React.ReactNode }> }) {
  return (
    <dl className="s4-kv">
      {items.map((item) => (
        <div key={item.label} className="s4-kv__row">
          <dt>{item.label}</dt>
          <dd>{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

export function formatTime(value?: string | null): string {
  if (!value) return "—";
  return new Date(value).toLocaleString("zh-CN");
}

export function useDebounced<T>(value: T, ms = 300): T {
  const [v, setV] = useState(value);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => {
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setV(value), ms);
    return () => window.clearTimeout(timer.current);
  }, [value, ms]);
  return v;
}
