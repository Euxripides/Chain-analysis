/*
 * ChainSight 令牌（用户端重构规范 V4 §4 / §5）
 *
 * 分三层，组件只允许引用语义层：
 *   1. palette   —— 规范 §4.1 的原始色值，只用于填充、描边、图形和渐变；
 *   2. text      —— 与 palette 同色相的**文字安全**变体；
 *   3. semantic  —— 组件真正引用的角色（surface / border / text / primary ...）。
 *
 * 为什么需要第 2 层：规范 §4.1 的主色 #3478F6 在白底只有 4.07:1，
 * #169B62 / #D98C16 / #1DB8E8 更低（3.56 / 2.72 / 2.31），都不满足 WCAG AA
 * 的 4.5:1 正文门槛。这与仓库既有的 `--c-amber` / `--c-amber-text` 处理方式一致：
 * **保留规范色相用于填充与图形，另给可读的文字变体**，而不是为了视觉稿把可达性改坏。
 * 全部前景/背景组合登记在 `scripts/check-token-contrast.mjs`，不达标的组合无法进入本文件。
 */

/** 规范 §4.1 原始色板（填充 / 描边 / 图形 / 渐变）。 */
export const palette = {
  canvas: "#F5F5F5",
  canvasStrong: "#FAFAFA",
  surface: "#FFFFFF",
  surfaceGlass: "#FFFFFF",
  surfaceBlue: "#FAFAFA",
  railSurface: "#FFFFFF",

  border: "#E8E8E8",
  borderStrong: "#D9D9D9",
  borderSubtle: "#F0F0F0",

  textPrimary: "#141414",
  textSecondary: "#595959",
  textMuted: "#737373",

  primary: "#1677FF",
  primaryHover: "#4096FF",
  primaryActive: "#0958D9",
  primarySoft: "#E6F4FF",

  cyan: "#1DB8E8",
  violet: "#6A63FF",
  success: "#169B62",
  warning: "#D98C16",
  danger: "#D84A4A",
} as const;

/**
 * 文字安全变体（同色相压暗到 ≥4.5:1）。
 *
 * 计算基准：白底 / 画布 #F4F8FF / 自身浅底 #EAF2FF（规范 §4.1 的 Primary Soft）。
 * 括号内为 (白底, 画布底, 浅底) 对比度。
 */
export const textColors = {
  primary: "#0958D9",
  cyan: "#0A7191", //    5.59 / 5.25 / 4.97
  violet: "#5A52E8", //  5.52 / 5.18 / 4.90
  success: "#0F7A4C", // 5.37 / 5.04 / 4.77
  warning: "#9C6109", // 5.09 / 4.78 / 4.52
  danger: "#C43C3C", //  5.17 / 4.86 / 4.59
} as const;

/** 状态浅底（徽标、行高亮）。与上面的文字变体配对全部 ≥4.5:1。 */
export const softColors = {
  primary: "#E6F4FF",
  cyan: "#E6F6FC",
  violet: "#EFEEFF",
  success: "#E8F7F0",
  warning: "#FDF3E4",
  danger: "#FDECEB",
  neutral: "#EEF2F9",
} as const;

/** 圆角（规范 §4.3）。 */
export const radius = {
  control: 6,
  cardSm: 6,
  card: 8,
  panel: 8,
  modal: 8,
  hero: 8,
  pill: 999,
} as const;

/** 阴影（规范 §4.4）。 */
export const shadow = {
  surface: "none",
  surfaceHover: "0 2px 8px rgba(0, 0, 0, .05)",
  floating: "0 8px 30px rgba(20, 52, 100, .14), 0 30px 80px rgba(20, 52, 100, .18)",
  ring: "0 0 0 3px rgba(52, 120, 246, .18)",
  railActive: "0 2px 8px rgba(52, 120, 246, .16)",
} as const;

/** 排版（规范 §5.2）。 */
export const typography = {
  heroTitle: 32,
  pageTitle: 24,
  sectionTitle: 15,
  cardTitle: 14,
  body: 14,
  denseTable: 13,
  secondary: 12,
  tinyMeta: 11,
  metricLarge: 32,
  metricMin: 28,
} as const;

/** 模糊与玻璃（规范 §4.5）——只允许用于顶栏、命令面板、Popover、Modal 上层、Hero 局部浮层。 */
export const glass = {
  blur: "blur(18px) saturate(130%)",
} as const;

/** 布局尺寸（规范 §6.1 / §6.2）。 */
export const layout = {
  railExpanded: 208,
  railCollapsed: 72,
  topbarHeight: 64,
  inspectorWidth: 340,
  inspectorWidthLarge: 420,
  formMaxWidth: 1040,
  formFieldWidth: 840,
  readingWidth: 760,
  gutter: 24,
} as const;

/** 卡片类型（规范 §10.1）。页面只能使用这六种，不得每页重新发明。 */
export type CardKind =
  | "surface"
  | "interactive"
  | "metric"
  | "object"
  | "panel"
  | "glass-hero";
