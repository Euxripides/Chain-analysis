import { useCallback, useEffect, useRef, useState } from "react";

/**
 * 顶部短提示：出现在页面顶部、**2 秒后自动消失**，fixed 覆盖层，不参与页面布局、
 * 不占位、不阻断操作。
 *
 * 使用范围（重要）：这是需求方 2026-09-18 对**登录失败**这类"看一眼就够"的反馈提出的
 * 明确要求。docs/code-standards.md 的默认口径是"提示一律走弹窗"，两者不同：
 * 请只用于登录失败等瞬时反馈，不要扩散成全局通知条或替代确认类弹窗。
 */
export interface TopToastState {
  message: string | null;
  /** 显示一条短提示（重复调用会重置计时）。 */
  show: (message: string) => void;
  /** 立刻收起（例如用户重新提交时）。 */
  clear: () => void;
}

/** 顶部短提示的状态与计时器管理。 */
export function useTopToast(durationMs = 2000): TopToastState {
  const [message, setMessage] = useState<string | null>(null);
  const timer = useRef<number | undefined>(undefined);

  const clear = useCallback(() => {
    if (timer.current !== undefined) {
      window.clearTimeout(timer.current);
      timer.current = undefined;
    }
    setMessage(null);
  }, []);

  const show = useCallback(
    (next: string) => {
      if (timer.current !== undefined) window.clearTimeout(timer.current);
      setMessage(next);
      timer.current = window.setTimeout(() => {
        timer.current = undefined;
        setMessage(null);
      }, durationMs);
    },
    [durationMs],
  );

  useEffect(
    () => () => {
      if (timer.current !== undefined) window.clearTimeout(timer.current);
    },
    [],
  );

  return { message, show, clear };
}

/** 顶部提示的呈现：role=alert 可被读屏播报；无内容时不占 DOM。 */
export function TopToast({ message }: { message: string | null }) {
  if (!message) return null;
  return (
    <div className="top-toast" role="alert" aria-live="assertive">
      {message}
    </div>
  );
}
