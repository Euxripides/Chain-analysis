import { useEffect, useState } from "react";

/**
 * 下载页的轻量浏览器能力 hook。
 *
 * 为什么单独放一层：页面、轨道与各面板都要按同一个断点判断"移动端 / 紧凑桌面"，
 * 各写一份 matchMedia 迟早会出现"轨道按移动端渲染、布局按桌面端渲染"的错配。
 */

/** 媒体查询订阅（SSR 不涉及，首帧同步读取以避免闪一下桌面布局）。 */
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => {
    if (typeof window === "undefined" || !window.matchMedia) return false;
    return window.matchMedia(query).matches;
  });
  useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia) return;
    const mql = window.matchMedia(query);
    const onChange = (event: MediaQueryListEvent) => setMatches(event.matches);
    setMatches(mql.matches);
    mql.addEventListener("change", onChange);
    return () => mql.removeEventListener("change", onChange);
  }, [query]);
  return matches;
}

/**
 * 页面是否可见（§10.5 / §30：页面隐藏时暂停装饰动效）。
 * 返回 true 表示"可以继续播放动效"。
 */
export function usePageVisible(): boolean {
  const [visible, setVisible] = useState(() => (typeof document === "undefined" ? true : !document.hidden));
  useEffect(() => {
    const onChange = () => setVisible(!document.hidden);
    document.addEventListener("visibilitychange", onChange);
    return () => document.removeEventListener("visibilitychange", onChange);
  }, []);
  return visible;
}

/**
 * 弹窗焦点守卫（§17.5 / DL-FLOW-005：Esc 必须能关闭）。
 *
 * 共享 Dialog renderer 把焦点还原交给自己（focusTriggerAfterClose={false}），
 * 但在"系统开启减少动效"等路径下，rc-dialog 的入场动作被跳过，焦点会停回 <body>：
 * 此时 Ant 绑定在弹窗容器上的 keydown 收不到 Escape，弹窗只能靠关闭按钮退出。
 *
 * 这里只做一件最小的事：当本页弹出的 Dialog 出现而焦点不在它内部时，把焦点移到
 * 弹窗容器（tabindex=-1，标准 dialog 行为），Escape / Tab 因此都能正常工作。
 * 不碰共享 renderer 的任何语义，只在下载页挂载期间生效。
 */
export function useDialogFocusGuard(): void {
  useEffect(() => {
    if (typeof document === "undefined") return;
    const root = document.getElementById("cs-overlay-root");
    if (!root) return;
    const ensureFocus = () => {
      const dialog = root.querySelector<HTMLElement>('[role="dialog"]');
      if (!dialog) return;
      const container = dialog.closest<HTMLElement>(".ant-modal-wrap") ?? dialog;
      const active = document.activeElement;
      if (active && active !== document.body && container.contains(active)) return;
      container.focus?.({ preventScroll: true });
    };
    const observer = new MutationObserver(() => ensureFocus());
    observer.observe(root, { childList: true, subtree: true });
    return () => observer.disconnect();
  }, []);
}

/**
 * 延迟置位（§8.5 / §8.9）：请求超过 delay 才显示运行反馈，避免快响应时闪一下。
 * 只做时间语义，不产生任何状态机副作用。
 */
export function useDelayedFlag(active: boolean, delay = 300): boolean {
  const [shown, setShown] = useState(false);
  useEffect(() => {
    if (!active) {
      setShown(false);
      return;
    }
    const timer = setTimeout(() => setShown(true), delay);
    return () => clearTimeout(timer);
  }, [active, delay]);
  return shown;
}

/** 滚动到某个步骤锚点；reduced motion 下不启用平滑滚动（§6.3）。 */
export function scrollToStep(anchorId: string, reducedMotion: boolean): void {
  const target = document.getElementById(anchorId);
  if (!target) return;
  target.scrollIntoView({ behavior: reducedMotion ? "auto" : "smooth", block: "start" });
}
