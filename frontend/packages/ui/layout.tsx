import React, { useCallback, useEffect, useRef, useState } from "react";
import { X } from "lucide-react";

/**
 * 用户端布局组件（蓝图 §8.1）
 *
 * PageSection / SplitPane / InspectorDrawer / StickyActionBar / BentoGrid / PageSkeleton。
 *
 * 这些组件只负责结构与尺寸，不承载任何业务语义，也不在页面里嵌入提示内容
 * （AGENTS.md：说明、警告、结果一律走 Dialog）。
 */

/** 类名合并：避免在 JSX 里到处写模板字符串，也让可选 className 的语义显式。 */
export function cx(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(" ");
}

/** 章节标题 + 计数 + 操作 + 统一内容间距（§8.1 PageSection）。 */
export function PageSection({
  title,
  count,
  actions,
  children,
  id,
  className,
}: {
  title?: React.ReactNode;
  count?: React.ReactNode;
  actions?: React.ReactNode;
  children: React.ReactNode;
  id?: string;
  className?: string;
}) {
  return (
    <section className={cx("cs-section", className)} id={id} aria-label={typeof title === "string" ? title : undefined}>
      {title || count || actions ? (
        <div className="cs-section__head">
          {title ? <h2 className="cs-section__title">{title}</h2> : null}
          {count !== undefined && count !== null ? <span className="cs-section__count">{count}</span> : null}
          {actions ? <div className="cs-section__actions">{actions}</div> : null}
        </div>
      ) : null}
      {children}
    </section>
  );
}

/**
 * 主区 + 检查器，可拖动宽度（§8.1 SplitPane）。
 *
 * 拖动宽度只作为**本地视觉偏好**记忆（localStorage），不进入任何服务端状态、
 * 不影响请求参数——蓝图 §8.1 明确"记忆仅限本地视觉偏好"。
 */
export function SplitPane({
  main,
  aside,
  storageKey,
  defaultAsideWidth = 360,
  minAsideWidth = 280,
  maxAsideWidth = 620,
  asideLabel = "详情",
  className,
}: {
  main: React.ReactNode;
  aside?: React.ReactNode;
  storageKey?: string;
  defaultAsideWidth?: number;
  minAsideWidth?: number;
  maxAsideWidth?: number;
  asideLabel?: string;
  className?: string;
}) {
  const [width, setWidth] = useState(() => {
    if (!storageKey || typeof window === "undefined") return defaultAsideWidth;
    const raw = window.localStorage.getItem(storageKey);
    const parsed = raw ? Number.parseInt(raw, 10) : Number.NaN;
    if (Number.isNaN(parsed)) return defaultAsideWidth;
    return Math.min(maxAsideWidth, Math.max(minAsideWidth, parsed));
  });
  const dragging = useRef(false);

  useEffect(() => {
    const onMove = (event: PointerEvent) => {
      if (!dragging.current) return;
      const next = Math.min(maxAsideWidth, Math.max(minAsideWidth, window.innerWidth - event.clientX));
      setWidth(next);
    };
    const onUp = () => {
      if (!dragging.current) return;
      dragging.current = false;
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    };
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
    return () => {
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
    };
  }, [maxAsideWidth, minAsideWidth]);

  useEffect(() => {
    if (!storageKey) return;
    try {
      window.localStorage.setItem(storageKey, String(width));
    } catch {
      /* 隐私模式下 localStorage 会抛错：宽度记忆是可选增强，失败不影响功能。 */
    }
  }, [storageKey, width]);

  if (!aside) {
    return <div className={cx("cs-split", className)}>{main}</div>;
  }

  return (
    <div className={cx("cs-split", className)}>
      <div className="cs-split__pane cs-split__pane--main">{main}</div>
      <div
        className="cs-split__handle"
        role="separator"
        aria-orientation="vertical"
        aria-label={"调整" + asideLabel + "宽度"}
        aria-valuenow={width}
        aria-valuemin={minAsideWidth}
        aria-valuemax={maxAsideWidth}
        tabIndex={0}
        onPointerDown={(event) => {
          dragging.current = true;
          document.body.style.cursor = "col-resize";
          document.body.style.userSelect = "none";
          event.preventDefault();
        }}
        onKeyDown={(event) => {
          /* 键盘也能调整宽度：所有功能必须可用键盘完成（蓝图 §14）。 */
          const step = event.shiftKey ? 48 : 16;
          if (event.key === "ArrowLeft") {
            event.preventDefault();
            setWidth((w) => Math.min(maxAsideWidth, w + step));
          } else if (event.key === "ArrowRight") {
            event.preventDefault();
            setWidth((w) => Math.max(minAsideWidth, w - step));
          }
        }}
      />
      <div className="cs-split__pane cs-split__pane--aside" style={{ width }}>
        {aside}
      </div>
    </div>
  );
}

/**
 * 地址、交易、Evidence、任务快速预览（§8.1 InspectorDrawer）。
 *
 * 它是一块**内联面板**而不是浮层：宽屏下由 SplitPane 常驻，窄屏由调用方决定
 * 展开/收起，因此不会盖住主内容，也不会与 Dialog 争夺层级。
 */
export function InspectorDrawer({
  title,
  subtitle,
  actions,
  onClose,
  children,
  ariaLabel,
  className,
}: {
  title: React.ReactNode;
  subtitle?: React.ReactNode;
  actions?: React.ReactNode;
  onClose?: () => void;
  children: React.ReactNode;
  ariaLabel?: string;
  className?: string;
}) {
  return (
    <aside
      className={cx("cs-inspector", "cs-drawer-enter", className)}
      aria-label={ariaLabel ?? (typeof title === "string" ? title : "检查器")}
    >
      <header className="cs-inspector__head">
        <span className="cs-inspector__title">{title}</span>
        {subtitle ? <span className="cs-pipeline__meta">{subtitle}</span> : null}
        {onClose ? (
          <button type="button" className="cs-iconbtn" aria-label="关闭检查器" onClick={onClose} style={{ width: 28, height: 28 }}>
            <X size={15} />
          </button>
        ) : null}
      </header>
      <div className="cs-inspector__body">{children}</div>
      {actions ? <div className="cs-inspector__actions">{actions}</div> : null}
    </aside>
  );
}

/**
 * 长表单与批量操作的吸底操作栏（§8.1 StickyActionBar）。
 *
 * 移动端必须避开 iOS safe area（§13），由 .cs-stickybar 的 env(safe-area-inset-bottom) 负责。
 */
export function StickyActionBar({
  children,
  leading,
  className,
}: {
  children: React.ReactNode;
  leading?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cx("cs-stickybar", className)} role="group">
      {leading}
      <span className="cs-stickybar__spacer" />
      {children}
    </div>
  );
}

/**
 * Bento 首页网格（§12.2）。
 * 区域划分表达真实业务优先级，不做装饰性拼图——每个 cell 都必须渲染真实数据或真实操作。
 */
export function BentoGrid({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cx("cs-bento", className)}>{children}</div>;
}

export function BentoCell({
  area,
  children,
  className,
}: {
  area: "a" | "b" | "c" | "d" | "e" | "f";
  children: React.ReactNode;
  className?: string;
}) {
  return <div className={cx("cs-bento__" + area, className)}>{children}</div>;
}

/**
 * 路由懒加载骨架（§11.9）：使用与目标页面相似的 PageSkeleton，不能是空白页面。
 * 形状（表 / 卡片 / 详情 / 工作台）由调用方按目标页面选择，避免"骨架跳成另一种布局"。
 */
export function PageSkeleton({
  shape = "table",
  rows = 6,
}: {
  shape?: "table" | "cards" | "detail" | "workbench";
  rows?: number;
}) {
  const bar = (width: string, height = 14) => (
    <span className="skeleton" style={{ display: "block", width, height, borderRadius: 6 }} />
  );
  return (
    <div aria-busy="true" aria-live="polite" style={{ display: "flex", flexDirection: "column", gap: "var(--sp-5)" }}>
      <span className="visually-hidden">正在加载页面</span>
      <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-2)" }}>
        {bar("220px", 22)}
        {bar("340px", 12)}
      </div>
      {shape === "table" ? (
        <div className="card">
          <div className="card__head">{bar("180px")}</div>
          <div className="card__body" style={{ display: "flex", flexDirection: "column", gap: "var(--sp-3)" }}>
            {Array.from({ length: rows }).map((_, index) => (
              <div key={index} style={{ display: "flex", gap: "var(--sp-4)" }}>
                <span className="skeleton" style={{ flex: 2, height: 16 }} />
                <span className="skeleton" style={{ flex: 1, height: 16 }} />
                <span className="skeleton" style={{ flex: 1, height: 16 }} />
                <span className="skeleton" style={{ flex: 1, height: 16 }} />
              </div>
            ))}
          </div>
        </div>
      ) : null}
      {shape === "cards" ? (
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(260px, 1fr))", gap: "var(--sp-4)" }}>
          {Array.from({ length: rows }).map((_, index) => (
            <div className="card" key={index}>
              <div className="card__body" style={{ display: "flex", flexDirection: "column", gap: "var(--sp-3)" }}>
                {bar("60%", 16)}
                {bar("90%", 12)}
                {bar("40%", 12)}
              </div>
            </div>
          ))}
        </div>
      ) : null}
      {shape === "detail" ? (
        <div className="card">
          <div className="card__body" style={{ display: "flex", flexDirection: "column", gap: "var(--sp-4)" }}>
            {bar("45%", 18)}
            <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))", gap: "var(--sp-4)" }}>
              {Array.from({ length: 6 }).map((_, index) => (
                <div key={index} style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                  {bar("50%", 10)}
                  {bar("85%", 14)}
                </div>
              ))}
            </div>
          </div>
        </div>
      ) : null}
      {shape === "workbench" ? (
        <div className="cs-workbench cs-workbench--skeleton">
          {Array.from({ length: 3 }).map((_, index) => (
            <div className="card" key={index}>
              <div className="card__body" style={{ display: "flex", flexDirection: "column", gap: "var(--sp-3)" }}>
                {bar("70%", 14)}
                {bar("100%", 12)}
                {bar("85%", 12)}
                {bar("60%", 12)}
              </div>
            </div>
          ))}
        </div>
      ) : null}
    </div>
  );
}

/**
 * 表格横向滚动的左右边缘提示（§8.4）。
 * 把滚动容器交给调用方，本 hook 只负责打上 data-overflow；
 * 用一次 ResizeObserver 而不是为每行建观察器（§15）。
 */
export function useHorizontalOverflowHint<T extends HTMLElement>() {
  const ref = useRef<T | null>(null);
  const sync = useCallback(() => {
    const node = ref.current;
    if (!node) return;
    const max = node.scrollWidth - node.clientWidth;
    if (max <= 1) {
      delete node.dataset.overflow;
      return;
    }
    const atStart = node.scrollLeft <= 1;
    const atEnd = node.scrollLeft >= max - 1;
    node.dataset.overflow = atStart ? "start" : atEnd ? "end" : "both";
  }, []);

  useEffect(() => {
    const node = ref.current;
    if (!node) return;
    sync();
    node.addEventListener("scroll", sync, { passive: true });
    const observer = new ResizeObserver(sync);
    observer.observe(node);
    return () => {
      node.removeEventListener("scroll", sync);
      observer.disconnect();
    };
  }, [sync]);

  return { ref, sync };
}
