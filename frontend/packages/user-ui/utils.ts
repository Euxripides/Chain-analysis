/**
 * 轻量工具。
 *
 * `cx` 与 `@chain/ui/layout` 的实现一致，但对齐在 user-ui 内部定义，
 * 让用户端 UI 包不依赖后台端仍在使用的共享包（规范 §2.1 的隔离要求）。
 */
export function cx(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(" ");
}

/** 是否处于 Reduced Motion（与 CSS 侧 prefers-reduced-motion 是同一判据）。 */
export function prefersReducedMotion(): boolean {
  if (typeof window === "undefined" || !window.matchMedia) return false;
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

/** 是否处于页面可见状态（页面隐藏时只暂停装饰动画，不停业务订阅）。 */
export function pageIsVisible(): boolean {
  if (typeof document === "undefined") return true;
  return document.visibilityState !== "hidden";
}
