/**
 * Canvas 用到的媒体查询 Hook。
 *
 * 必须用 matchMedia 而不是窗口宽度：折叠面板、弹出层都会改变可用宽度，
 * 只看 window.innerWidth 会在侧栏收起时误判。
 */

import { useEffect, useState } from "react";

export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => typeof window !== "undefined" && window.matchMedia(query).matches);
  useEffect(() => {
    if (typeof window === "undefined") return;
    const list = window.matchMedia(query);
    const update = () => setMatches(list.matches);
    update();
    list.addEventListener("change", update);
    return () => list.removeEventListener("change", update);
  }, [query]);
  return matches;
}

export function useIsMobile(): boolean {
  return useMediaQuery("(max-width: 767px)");
}

/** 蓝图 §7.4：reduced-motion 下关闭自动播放与方向流动，但逐步查看仍然可用。 */
export function usePrefersReducedMotion(): boolean {
  return useMediaQuery("(prefers-reduced-motion: reduce)");
}

