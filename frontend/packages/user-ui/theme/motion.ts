/*
 * 动效令牌（用户端重构规范 V4 §9.1 / §9.2 / §62）
 *
 * 同一状态只能有一个动画所有者（§9.6）。这里的预设只描述"谁负责哪一段"，
 * 组件封装层按下面的表挑选，业务页面不再自己写 duration / spring。
 */

/** 五档时长（§9.1）。 */
export const duration = {
  /** 按压、checkbox、icon toggle */
  micro: 0.09,
  /** hover、tooltip、focus */
  fast: 0.14,
  /** card、tab、popover、filter */
  standard: 0.22,
  /** drawer、page region、layout change */
  spatial: 0.34,
  /** 首页重点视觉 / graph camera focus */
  hero: 0.48,
} as const;

/** 时长（毫秒，供 CSS 变量与 ECharts 复用）。 */
export const durationMs = {
  micro: 90,
  fast: 140,
  standard: 220,
  spatial: 340,
  hero: 480,
} as const;

/** 统一缓动（§9.2）。 */
export const easing = {
  enter: [0.16, 1, 0.3, 1],
  exit: [0.4, 0, 1, 1],
  standard: [0.2, 0, 0, 1],
} as const satisfies Record<string, [number, number, number, number]>;

/** Spring 预设（§9.2）。 */
export const spring = {
  /** 通用 UI：指示器、Tabs、Switch */
  ui: { type: "spring", stiffness: 350, damping: 30, mass: 0.7 },
  /** 图谱：camera、节点 focus、halo */
  graph: { type: "spring", stiffness: 220, damping: 28, mass: 0.9 },
} as const;

/** 页面区域 stagger 步长（§9.4）。只对首屏 6–8 个区块生效。 */
export const staggerStep = 0.028;

/** 页面进入（§9.3 Standard Route）：opacity + y 8→0，180–220ms。 */
export const pageEnter = {
  initial: { opacity: 0, y: 8 },
  animate: { opacity: 1, y: 0 },
  transition: { duration: duration.standard, ease: easing.enter },
} as const;

/** 步骤/容器切换（§16.2 / §13.1）：前进 old x 0→-8，new x 8→0。 */
export const stepForward = {
  initial: { opacity: 0, x: 8 },
  animate: { opacity: 1, x: 0 },
  exit: { opacity: 0, x: -8 },
  transition: { duration: 0.2, ease: easing.enter },
} as const;

/** 命令面板 / Dialog 面板（§18.2 / §21.3）。 */
export const panelIn = {
  initial: { opacity: 0, scale: 0.965, y: -8 },
  animate: { opacity: 1, scale: 1, y: 0 },
  exit: { opacity: 0, scale: 0.98, y: -4 },
  transition: { duration: 0.18, ease: easing.enter },
} as const;
