// Sprint 6 后台控制台的共享数据层。
//
// 约定（与 apps/admin-console/src/pages/* 既有页面一致）：
//   - 所有请求走 useAuth().client（同源 Cookie + X-CSRF-Token + X-Auth-Audience）。
//   - 错误只通过 useDialog() 弹窗呈现，页面正文不出现提示横幅（AGENTS.md / AD-009）。
//   - 列表使用服务端 cursor 分页，客户端只保留当前页与游标历史。

import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, newIdempotencyKey, useAuth } from "@chain/api-client";
import { dialogInputValue, useDialog } from "@chain/ui";

export interface AdminEnvelope<T> {
  items?: T[];
  [key: string]: unknown;
}

export interface AdminListState<T> {
  items: T[];
  extra: Record<string, unknown>;
  loading: boolean;
  error: string | null;
  nextCursor: string | null;
  hasPrevious: boolean;
  reload: () => void;
  goNext: () => void;
  goPrevious: () => void;
  setFilter: (patch: Record<string, string | number | undefined>) => void;
  filters: Record<string, string | number | undefined>;
}

interface QueryValue {
  [key: string]: string | number | boolean | undefined | null;
}

/**
 * useAdminList 负责服务端 cursor 分页列表：加载、错误、筛选变更与前后翻页。
 * 返回的 extra 是 envelope.data 里除 items 之外的字段（例如 open_p0、deletion_execution_enabled）。
 */
export function useAdminList<T>(path: string, initial: QueryValue = {}, pageSize = 20): AdminListState<T> {
  const { client } = useAuth();
  const [filters, setFilters] = useState<QueryValue>(initial);
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const [history, setHistory] = useState<Array<string | undefined>>([]);
  const [items, setItems] = useState<T[]>([]);
  const [extra, setExtra] = useState<Record<string, unknown>>({});
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [nonce, setNonce] = useState(0);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      setLoading(true);
      setError(null);
      try {
        const query: QueryValue = { limit: pageSize };
        Object.entries(filters).forEach(([key, value]) => {
          if (value === undefined || value === null || value === "") return;
          query[key] = value;
        });
        if (cursor) query.cursor = cursor;
        const response = await client.get<Record<string, unknown>>(path, { query, signal });
        if (signal?.aborted) return;
        const data = (response.data ?? {}) as Record<string, unknown>;
        const list = Array.isArray(data.items) ? (data.items as T[]) : [];
        setItems(list);
        const rest: Record<string, unknown> = {};
        Object.entries(data).forEach(([key, value]) => {
          if (key !== "items") rest[key] = value;
        });
        setExtra(rest);
        setNextCursor(response.meta?.next_cursor ?? null);
      } catch (loadError) {
        if (signal?.aborted) return;
        if (loadError instanceof DOMException && loadError.name === "AbortError") return;
        setError(loadError instanceof ApiError ? loadError.userMessage : "读取数据失败");
      } finally {
        if (!signal?.aborted) setLoading(false);
      }
    },
    [client, cursor, filters, pageSize, path],
  );

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [load, nonce]);

  const setFilter = useCallback((patch: QueryValue) => {
    setFilters((current) => ({ ...current, ...patch }));
    setCursor(undefined);
    setHistory([]);
  }, []);

  const goNext = useCallback(() => {
    setNextCursor((current) => {
      if (current) {
        setHistory((h) => [...h, cursor]);
        setCursor(current);
      }
      return current;
    });
  }, [cursor]);

  const goPrevious = useCallback(() => {
    setHistory((h) => {
      if (h.length === 0) return h;
      const copy = [...h];
      const previous = copy.pop();
      setCursor(previous);
      return copy;
    });
  }, []);

  return {
    items,
    extra,
    loading,
    error,
    nextCursor,
    hasPrevious: history.length > 0,
    reload: () => setNonce((n) => n + 1),
    goNext,
    goPrevious,
    setFilter,
    filters: filters as Record<string, string | number | undefined>,
  };
}

export interface AdminResourceState<T> {
  data: T | null;
  loading: boolean;
  error: string | null;
  reload: () => void;
}

/** useAdminResource 加载单个资源（详情页）。 */
export function useAdminResource<T>(path: string | null): AdminResourceState<T> {
  const { client } = useAuth();
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(Boolean(path));
  const [error, setError] = useState<string | null>(null);
  const [nonce, setNonce] = useState(0);

  useEffect(() => {
    if (!path) {
      setLoading(false);
      return;
    }
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    client
      .get<T>(path, { signal: controller.signal })
      .then((response) => {
        if (controller.signal.aborted) return;
        setData(response.data ?? null);
      })
      .catch((loadError: unknown) => {
        if (controller.signal.aborted) return;
        if (loadError instanceof DOMException && loadError.name === "AbortError") return;
        setError(loadError instanceof ApiError ? loadError.userMessage : "读取数据失败");
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [client, path, nonce]);

  return { data, loading, error, reload: () => setNonce((n) => n + 1) };
}

export interface CommandRequest {
  path: string;
  body?: Record<string, unknown>;
  confirmTitle: string;
  confirmBody?: React.ReactNode;
  confirmWord?: string;
  confirmLabel?: string;
  variant?: "primary" | "danger";
  details?: Array<{ label: string; value: React.ReactNode }>;
  requireReason?: boolean;
  reasonLabel?: string;
  requireStepUp?: boolean;
  stepUpLabel?: string;
  successTitle?: string;
  successDetails?: Array<{ label: string; value: React.ReactNode }> | ((data: Record<string, unknown>) => Array<{ label: string; value: React.ReactNode }>);
  onDone?: (data: Record<string, unknown>) => void;
}

export interface CommandRunner {
  run: (request: CommandRequest) => Promise<Record<string, unknown> | null>;
  busy: boolean;
}

/**
 * useAdminCommand 统一高风险命令的前端编排：
 * 确认弹窗 → 原因弹窗 →（可选）step-up 密码弹窗 → 提交 → 成功/失败弹窗。
 * 所有提示都走 useDialog()，页面正文不出现任何提示文案。
 */
export function useAdminCommand(): CommandRunner {
  const { client } = useAuth();
  const dialog = useDialog();
  const [busy, setBusy] = useState(false);
  const busyRef = useRef(false);

  const run = useCallback(
    async (request: CommandRequest): Promise<Record<string, unknown> | null> => {
      if (busyRef.current) return null;
      const confirmed = await dialog.confirm({
        kind: request.variant === "danger" ? "warning" : "confirm",
        title: request.confirmTitle,
        body: request.confirmBody,
        details: request.details,
        confirmWord: request.confirmWord,
        actions: [
          { label: "取消", result: "", variant: "default" },
          { label: request.confirmLabel ?? "确认执行", result: "ok", variant: request.variant ?? "primary" },
        ],
      });
      if (confirmed !== "ok") return null;

      const body: Record<string, unknown> = { ...(request.body ?? {}) };
      if (request.requireReason !== false) {
        const reason = dialogInputValue(
          await dialog.prompt({
            kind: "warning",
            title: "填写操作原因",
            body: "原因会写入审计日志，无法为空。",
            input: {
              label: request.reasonLabel ?? "原因",
              placeholder: "例如：上游返回异常，按预案切换",
              pattern: /^.{1,500}$/,
              patternHint: "1-500 个字符",
            },
            submitLabel: "继续",
          }),
        );
        if (!reason) return null;
        body.reason = reason;
      }
      if (request.requireStepUp) {
        const password = dialogInputValue(
          await dialog.prompt({
            kind: "warning",
            title: "二次身份确认",
            body: "该操作需要在当前会话重新确认身份。密码只用于本次校验，不会写入日志。",
            input: {
              label: request.stepUpLabel ?? "当前账号密码",
              placeholder: "请输入当前账号密码",
              pattern: /^.{1,200}$/,
              patternHint: "请输入当前账号密码",
            },
            submitLabel: "确认身份并提交",
          }),
        );
        if (!password) return null;
        body.step_up_password = password;
      }

      busyRef.current = true;
      setBusy(true);
      try {
        const response = await client.post<Record<string, unknown>>(request.path, body, {
          idempotencyKey: newIdempotencyKey(),
        });
        const data = (response.data ?? {}) as Record<string, unknown>;
        const details =
          typeof request.successDetails === "function" ? request.successDetails(data) : request.successDetails;
        dialog.notify("success", request.successTitle ?? "操作已提交", null, details);
        request.onDone?.(data);
        return data;
      } catch (commandError) {
        const details: Array<{ label: string; value: React.ReactNode }> = [];
        if (commandError instanceof ApiError) {
          if (commandError.traceId) details.push({ label: "Trace", value: commandError.traceId });
          if (commandError.code) details.push({ label: "错误码", value: commandError.code });
          Object.entries(commandError.details ?? {}).forEach(([key, value]) => {
            if (value === null || value === undefined) return;
            details.push({ label: key, value: typeof value === "object" ? JSON.stringify(value) : String(value) });
          });
        }
        dialog.notify(
          "error",
          "命令执行失败",
          commandError instanceof ApiError ? commandError.userMessage : "请稍后重试",
          details.length > 0 ? details : undefined,
        );
        return null;
      } finally {
        busyRef.current = false;
        setBusy(false);
      }
    },
    [client, dialog],
  );

  return { run, busy };
}

/** usePatchedCommand 与 useAdminCommand 类似，但用于 PATCH 语义（配置、路由、预算）。 */
export function useAdminPatch(): {
  run: (request: Omit<CommandRequest, "requireReason"> & { path: string; ifMatch?: number; method?: "patch" | "post" }) => Promise<Record<string, unknown> | null>;
  busy: boolean;
} {
  const { client } = useAuth();
  const dialog = useDialog();
  const [busy, setBusy] = useState(false);

  const run = useCallback(
    async (request: Omit<CommandRequest, "requireReason"> & { path: string; ifMatch?: number; method?: "patch" | "post" }) => {
      const confirmed = await dialog.confirm({
        kind: request.variant === "danger" ? "warning" : "confirm",
        title: request.confirmTitle,
        body: request.confirmBody,
        details: request.details,
        actions: [
          { label: "取消", result: "", variant: "default" },
          { label: request.confirmLabel ?? "保存", result: "ok", variant: request.variant ?? "primary" },
        ],
      });
      if (confirmed !== "ok") return null;
      const reason = dialogInputValue(
        await dialog.prompt({
          kind: "warning",
          title: "填写变更原因",
          body: "变更原因会写入审计日志与配置版本。",
          input: { label: "原因", pattern: /^.{1,500}$/, patternHint: "1-500 个字符" },
          submitLabel: "保存",
        }),
      );
      if (!reason) return null;
      setBusy(true);
      try {
        const body = { ...(request.body ?? {}), reason };
        const options = request.ifMatch === undefined ? {} : { ifMatch: String(request.ifMatch) };
        const response =
          request.method === "post"
            ? await client.post<Record<string, unknown>>(request.path, body, options)
            : await client.patch<Record<string, unknown>>(request.path, body, options);
        const data = (response.data ?? {}) as Record<string, unknown>;
        const details =
          typeof request.successDetails === "function" ? request.successDetails(data) : request.successDetails;
        dialog.notify("success", request.successTitle ?? "已保存", null, details);
        request.onDone?.(data);
        return data;
      } catch (patchError) {
        const details: Array<{ label: string; value: React.ReactNode }> = [];
        if (patchError instanceof ApiError) {
          if (patchError.status === 412) details.push({ label: "冲突", value: "数据已被其他会话修改，请刷新后重试" });
          if (patchError.traceId) details.push({ label: "Trace", value: patchError.traceId });
        }
        dialog.notify(
          "error",
          "保存失败",
          patchError instanceof ApiError ? patchError.userMessage : "请稍后重试",
          details.length > 0 ? details : undefined,
        );
        return null;
      } finally {
        setBusy(false);
      }
    },
    [client, dialog],
  );

  return { run, busy };
}

// ---- 展示辅助 ----

export function fmtDateTime(value: unknown): string {
  if (typeof value !== "string" || value === "") return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString("zh-CN", { hour12: false });
}

export function fmtBytes(value: unknown): string {
  const bytes = typeof value === "number" ? value : Number(value ?? 0);
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  let size = bytes;
  let index = 0;
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024;
    index += 1;
  }
  return size.toFixed(index === 0 ? 0 : 2) + " " + units[index];
}

export function fmtNumber(value: unknown): string {
  const num = typeof value === "number" ? value : Number(value ?? 0);
  if (!Number.isFinite(num)) return "—";
  return num.toLocaleString("zh-CN");
}

export function fmtPercent(value: unknown, digits = 1): string {
  const num = typeof value === "number" ? value : Number(value ?? 0);
  if (!Number.isFinite(num)) return "—";
  return (num * 100).toFixed(digits) + "%";
}

/** fmtUSD 只格式化后端返回的十进制字符串金额，不做浮点再运算。 */
export function fmtUSD(value: unknown): string {
  if (typeof value === "string" && value !== "") return "$" + value;
  if (typeof value === "number") return "$" + value.toFixed(6);
  return "—";
}

export function fmtMillis(value: unknown): string {
  const ms = typeof value === "number" ? value : Number(value ?? 0);
  if (!Number.isFinite(ms) || ms <= 0) return "—";
  if (ms < 1000) return ms.toFixed(0) + " ms";
  return (ms / 1000).toFixed(2) + " s";
}

/** maskSecret 把摘要化后的密钥再做一次展示收敛。 */
export function maskSecret(value: unknown): string {
  const raw = typeof value === "string" ? value : "";
  if (raw === "") return "—";
  if (raw.length <= 12) return raw;
  return raw.slice(0, 12) + "…";
}
