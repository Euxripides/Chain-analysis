import { useEffect, useRef } from "react";
import { useLocation } from "react-router-dom";
import { useAuth } from "@chain/api-client";

export function pageStateScope(session: ReturnType<typeof useAuth>["session"]): string {
  return [session?.session_id ?? "", session?.organization_id ?? "", session?.workspace_id ?? ""].join(":");
}

export function readPageState<T>(key: string, fallback: T): T {
  try {
    const stored = JSON.parse(sessionStorage.getItem(key) ?? "null") as { at: number; value: T } | null;
    return stored && Date.now() - stored.at < 30 * 60_000 ? stored.value : fallback;
  } catch { return fallback; }
}

export function writePageState(key: string, value: unknown): void {
  try { sessionStorage.setItem(key, JSON.stringify({ at: Date.now(), value })); } catch { /* 浏览器禁用存储时继续正常操作。 */ }
}

/** 只保存本会话、本工作区的位置；等真实数据就绪后再恢复，避免骨架屏截断滚动。 */
export function usePageReturnPosition(ready: boolean): void {
  const { session } = useAuth();
  const location = useLocation();
  const key = `cs.return-position.v1:${pageStateScope(session)}:${location.pathname}${location.search}`;
  const position = useRef(0);
  const restored = useRef<string | null>(null);
  useEffect(() => {
    position.current = window.scrollY;
    const record = () => { position.current = window.scrollY; };
    window.addEventListener("scroll", record, { passive: true });
    return () => {
      window.removeEventListener("scroll", record);
      // 首次挂载尚未恢复的位置不能覆盖已保存的位置（包括 StrictMode 的试运行）。
      if (restored.current === key) writePageState(key, position.current);
    };
  }, [key]);
  useEffect(() => {
    if (!ready || restored.current === key) return;
    const saved = readPageState<number>(key, 0);
    const frame = requestAnimationFrame(() => {
      window.scrollTo({ top: Number.isFinite(saved) ? saved : 0, behavior: "instant" });
      position.current = window.scrollY;
      restored.current = key;
    });
    return () => cancelAnimationFrame(frame);
  }, [ready, key]);
}
