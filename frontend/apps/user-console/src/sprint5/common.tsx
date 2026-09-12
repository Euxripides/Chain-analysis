import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, useAuth } from "@chain/api-client";
import { useDialog } from "@chain/ui";

export interface S5Meta {
  next_cursor?: string | null;
  total?: number;
  traceId?: string;
}

/** 真实 API 读取 hook：所有页面只消费服务端 envelope，不使用占位数据。 */
export function useApi<T>(
  path: string | null,
  deps: unknown[] = [],
  query?: Record<string, string | number | boolean | undefined | null>,
) {
  const { client } = useAuth();
  const [data, setData] = useState<T | null>(null);
  const [meta, setMeta] = useState<S5Meta>({});
  const [loading, setLoading] = useState(Boolean(path));
  const [error, setError] = useState<string | null>(null);
  const queryKey = JSON.stringify(query ?? {});
  const abortRef = useRef<AbortController | null>(null);

  const load = useCallback(async () => {
    if (!path) {
      setData(null);
      setLoading(false);
      return;
    }
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setLoading(true);
    setError(null);
    try {
      const res = await client.get<T>(path, query ? { query, signal: controller.signal } : { signal: controller.signal });
      if (controller.signal.aborted) return;
      setData(res.data);
      setMeta({ next_cursor: res.meta.next_cursor, total: res.meta.total, traceId: res.meta.trace_id });
    } catch (err) {
      if (controller.signal.aborted) return;
      if (err instanceof ApiError) setError(err.userMessage);
      else if (!(err instanceof DOMException && err.name === "AbortError")) setError("加载失败");
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, path, queryKey, ...deps]);

  useEffect(() => {
    void load();
    return () => abortRef.current?.abort();
  }, [load]);

  return { data, meta, loading, error, reload: load };
}

/** 统一的写操作 hook：错误与成功提示只走 Dialog（AGENTS.md）。 */
export function useS5Action() {
  const { client } = useAuth();
  const dialog = useDialog();
  const [busy, setBusy] = useState(false);
  const run = useCallback(
    async <T,>(fn: () => Promise<T>, opts: { successTitle?: string; successBody?: string; onDone?: (value: T) => void } = {}) => {
      setBusy(true);
      try {
        const value = await fn();
        if (opts.successTitle) dialog.notify("success", opts.successTitle, opts.successBody);
        opts.onDone?.(value);
        return value;
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

export interface JobEvent {
  event_id?: number;
  event_type?: string;
  job_id?: string;
  status?: string;
  progress?: number;
  failed_count?: number;
  _sse_id?: number;
  [key: string]: unknown;
}

/**
 * 任务 SSE：浏览器原生 EventSource 会在断线后自动携带 Last-Event-ID；
 * 同时用 REST 快照校准，旧 revision 事件不覆盖新状态（§18.11）。
 */
export function useJobStream(jobID: string | null, onEvent: (event: JobEvent) => void) {
  const [connected, setConnected] = useState(false);
  const [lastEventID, setLastEventID] = useState("0");
  const handlerRef = useRef(onEvent);
  handlerRef.current = onEvent;

  useEffect(() => {
    if (!jobID) return;
    const source = new EventSource(`/api/v2/jobs/${encodeURIComponent(jobID)}/events`, { withCredentials: true });
    const update = (message: MessageEvent<string>) => {
      if (message.lastEventId) setLastEventID(message.lastEventId);
      try {
        const parsed = JSON.parse(message.data) as JobEvent;
        if (message.lastEventId && !parsed.event_id) parsed.event_id = Number(message.lastEventId);
        handlerRef.current(parsed);
      } catch {
        // 心跳/非法帧忽略，不把原始帧回显给用户。
      }
    };
    const snapshot = (message: MessageEvent<string>) => update(message);
    source.addEventListener("update", update as EventListener);
    source.addEventListener("snapshot", snapshot as EventListener);
    source.onopen = () => setConnected(true);
    source.onerror = () => setConnected(false);
    return () => {
      source.removeEventListener("update", update as EventListener);
      source.removeEventListener("snapshot", snapshot as EventListener);
      source.close();
    };
  }, [jobID]);

  return { connected, lastEventID };
}

/** 大整数/十进制字符串展示：绝不经过 JS number 丢失精度。 */
export function formatRawAmount(raw: string | null | undefined, decimals?: number | null): string {
  if (raw === null || raw === undefined || raw === "") return "—";
  const negative = raw.startsWith("-");
  const digits = negative ? raw.slice(1) : raw;
  if (!/^\d+$/.test(digits)) return raw;
  if (decimals === null || decimals === undefined || decimals < 0) return raw;
  const padded = digits.padStart(decimals + 1, "0");
  const intPart = padded.slice(0, padded.length - decimals) || "0";
  const fracPart = decimals > 0 ? padded.slice(padded.length - decimals).replace(/0+$/, "") : "";
  const grouped = intPart.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  const value = fracPart ? `${grouped}.${fracPart}` : grouped;
  return negative ? `-${value}` : value;
}

export function shortValue(value: string | null | undefined, head = 10, tail = 6): string {
  if (!value) return "—";
  if (value.length <= head + tail + 3) return value;
  return `${value.slice(0, head)}…${value.slice(-tail)}`;
}

export function formatBytes(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined) return "—";
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = bytes / 1024;
  let idx = 0;
  while (value >= 1024 && idx < units.length - 1) {
    value /= 1024;
    idx += 1;
  }
  return `${value.toFixed(value >= 10 ? 1 : 2)} ${units[idx]}`;
}

export function formatTime(value?: string | null): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString("zh-CN", { hour12: false });
}
