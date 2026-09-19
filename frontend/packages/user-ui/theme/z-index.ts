/*
 * Overlay / Portal / z-index Registry（用户端重构规范 V4 §22.4）
 *
 * 禁止各页面自行写 `z-index: 9999`。所有层级只能引用本表，
 * 并且只能通过 `overlayRootFor()` 决定 Portal 的挂载点。
 */

export const zIndex = {
  /** 普通内容 */
  base: 0,
  /** 吸顶表头 / 表格工具条 */
  stickyTable: 20,
  /** 图谱局部工具条 */
  graphChrome: 40,
  /** 顶栏 / 侧栏 */
  rail: 50,
  topbar: 50,
  /** Dropdown / Tooltip / Select 弹层 */
  dropdown: 100,
  /** Inspector / Drawer */
  inspector: 200,
  /** Modal 遮罩 */
  modalBackdrop: 300,
  /** Modal 本体 */
  modal: 310,
  /** 命令面板 */
  commandPalette: 320,
  /** 一次性敏感内容（恢复码等） */
  critical: 330,
} as const;

export type OverlayLayer = keyof typeof zIndex;

/** 全局 overlay 根节点 id：普通 Dropdown / Tooltip / Modal 默认挂到这里。 */
export const OVERLAY_ROOT_ID = "cs-overlay-root";
/** 图谱全屏态自己的 overlay 根节点：图谱内菜单用它，避免被全屏容器的 overflow 裁掉。 */
export const GRAPH_OVERLAY_ROOT_ID = "cs-graph-overlay-root";

/**
 * Portal 挂载点策略。
 *
 * 规则（§22.4）：
 *   - 普通弹层默认挂全局 overlay root；
 *   - 图谱全屏内的弹层可挂 graph overlay root；
 *   - **不允许**挂到带 `overflow: hidden` 的 Card 上——页面不再自己决定 getPopupContainer。
 */
export function overlayRootFor(scope: "global" | "graph" = "global"): HTMLElement | undefined {
  if (typeof document === "undefined") return undefined;
  const id = scope === "graph" ? GRAPH_OVERLAY_ROOT_ID : OVERLAY_ROOT_ID;
  return document.getElementById(id) ?? undefined;
}

/** Ant Design `getPopupContainer` 的统一实现，业务页面不得重复决定。 */
export function getPopupContainer(triggerNode?: HTMLElement): HTMLElement {
  const graphScoped = triggerNode?.closest?.("[data-overlay-scope='graph']");
  const root = overlayRootFor(graphScoped ? "graph" : "global");
  return root ?? triggerNode?.ownerDocument?.body ?? document.body;
}

/**
 * 在文档里补齐两个 overlay 根节点（幂等）。
 *
 * 由 `<ChainSightProvider>` 挂载时调用；这里保持 DOM 操作，不引入额外依赖。
 */
export function ensureOverlayRoots(): void {
  if (typeof document === "undefined") return;
  for (const [id, attr] of [
    [OVERLAY_ROOT_ID, "global"],
    [GRAPH_OVERLAY_ROOT_ID, "graph"],
  ] as const) {
    if (document.getElementById(id)) continue;
    const node = document.createElement("div");
    node.id = id;
    node.dataset.overlayScope = attr;
    node.style.position = "relative";
    node.style.zIndex = String(attr === "graph" ? zIndex.graphChrome : zIndex.dropdown);
    document.body.appendChild(node);
  }
}
