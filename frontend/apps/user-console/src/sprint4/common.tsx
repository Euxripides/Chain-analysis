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
  /**
   * 多权限入口：**全部满足**才显示（本轮为案件详情 Tab 新增）。
   *
   * 与单权限的 `permission` 是叠加关系而不是替代：其它页面继续用单权限，
   * 行为一字不变。之所以需要多权限，是因为有的 Tab 要同时具备两个权限
   * 才真正可用（见 app/case-tabs.ts），只检查其中一个会让用户点进去吃 403。
   * 判据必须与路由守卫一致，否则会出现"入口看得见、进去被拦"。
   */
  permissions?: string[];
}

/**
 * 深链接分区 tab（业务兼容层，签名与语义不变）。
 *
 * 2026-09 重构：内部换用共享的 `.cs-tabs` / `.cs-tab` 视觉，**API 与行为完全不变**——
 * 仍然是真实路由 `NavLink`（深链接、刷新后保持当前 tab），仍然按权限裁剪。
 * 选中态用 NavLink 自动写入的 `aria-current="page"`，与共享 `<Tabs>` 的 `aria-selected` 等价。
 */
export function DeepTabs({ tabs }: { tabs: TabDef[] }) {
  const { can } = useAuth();
  return (
    <nav className="cs-tabs" aria-label="页面分区">
      {tabs
        .filter((t) => {
          const required = t.permissions ?? (t.permission ? [t.permission] : []);
          return required.every((permission) => can(permission));
        })
        .map((t) => (
          <NavLink key={t.key} to={t.to} end className="cs-tab">
            {t.label}
          </NavLink>
        ))}
    </nav>
  );
}

/**
 * 元数据键值列表（业务兼容层，签名不变）。
 *
 * 内部换用共享的 `.cs-kv` 视觉；取值语义不变——空值仍然原样输出，
 * 由调用方决定显示 `—` 还是别的内容（不在这里替业务做判断）。
 */
export function KeyValues({ items }: { items: Array<{ label: string; value: React.ReactNode }> }) {
  return (
    <dl className="cs-kv">
      {items.map((item) => (
        <div key={item.label} className="cs-kv__row">
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
