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

/**
 * 冷白四级明度色板（2026-09-20「清晰度」修订，取代原规范 §4.1 的淡蓝四连白）。
 *
 *   L1 页面底层  layerPage      #E9EDF3
 *   L2 内容层    layerContent   #FFFFFF
 *   L3 输入层    layerField     #F4F7FA
 *   L4 选中层    layerSelected  #DEE9FD
 *
 * 蓝色只用于交互与状态；玻璃拟态（半透明面 + backdrop-filter）已整体退役。
 * 取值必须与 `theme/css-vars.css` 的 `html[data-app="user"]` 块逐项一致。
 */
export const palette = {
  layerPage: "#E9EDF3",
  layerContent: "#FFFFFF",
  layerField: "#F4F7FA",
  layerSelected: "#DEE9FD",

  canvas: "#E9EDF3",
  canvasStrong: "#DFE4EC",
  surface: "#FFFFFF",
  /** 兼容别名：已不透明（玻璃拟态退役），模糊由 `glass.blur = none` 关闭。 */
  surfaceGlass: "#FFFFFF",
  surfaceBlue: "#F1F4F8",
  railSurface: "#FFFFFF",
  surfaceSunken: "#E4E9F0",
  field: "#F4F7FA",

  border: "#CBD3DC",
  borderStrong: "#A3AFBD",
  borderSubtle: "#E0E5EB",
  borderField: "#A3AFBD",
  borderSelected: "#7FA9F5",

  textPrimary: "#101A2B",
  textSecondary: "#48566B",
  textMuted: "#59667A",

  primary: "#2563EB",
  primaryHover: "#1D55DE",
  primaryActive: "#1A4AC4",
  primarySoft: "#DEE9FD",

  cyan: "#1DB8E8",
  violet: "#6A63FF",
  success: "#169B62",
  warning: "#D98C16",
  danger: "#D84A4A",
} as const;

/**
 * 文字安全变体（同色相压暗到 ≥4.5:1）。
 *
 * 计算基准：白底 / 画布 #E9EDF3 / 自身浅底 #DEE9FD（L4 选中层 = Primary Soft）。
 * 括号内为 (白底, 画布底, 浅底) 对比度。
 * warning / danger 比上一版更暗：原取值在 #E9EDF3 画布上只有 4.33 / 4.40。
 */
export const textColors = {
  primary: "#1F5ED6", // 5.78 / 4.92 / 4.73
  cyan: "#0A7191", //    5.59 / 4.72 / 5.01
  violet: "#5A52E8", //  5.52 / 4.69 / 4.82
  success: "#0F7A4C", // 5.37 / 4.57 / 4.86
  warning: "#8F5A08", // 5.78 / 4.92 / 5.26
  danger: "#B73535", //  5.88 / 5.00 / 5.14
} as const;

/** 状态浅底（徽标、行高亮）。与上面的文字变体配对全部 ≥4.5:1。 */
export const softColors = {
  primary: "#DEE9FD",
  cyan: "#E6F6FC",
  violet: "#EFEEFF",
  success: "#E8F7F0",
  warning: "#FDF3E4",
  danger: "#FDECEB",
  neutral: "#E9EDF3",
} as const;

/** 圆角（规范 §4.3）。 */
export const radius = {
  control: 10,
  cardSm: 12,
  card: 16,
  panel: 18,
  modal: 18,
  hero: 24,
  pill: 999,
} as const;

/**
 * 阴影（规范 §4.4，2026-09-20 收紧）。
 * 中性深色 + 短半径：边界靠描边表达，阴影只提示"离地 1–2px"；
 * 只有弹窗/浮层保留大半径（真实层高差）。禁止蓝色柔光。
 */
export const shadow = {
  surface: "0 1px 2px rgba(16, 26, 43, .06), 0 2px 6px rgba(16, 26, 43, .05)",
  surfaceHover: "0 2px 6px rgba(16, 26, 43, .09), 0 10px 22px rgba(16, 26, 43, .08)",
  floating: "0 10px 28px rgba(16, 26, 43, .16), 0 28px 64px rgba(16, 26, 43, .18)",
  ring: "0 0 0 3px rgba(37, 99, 235, .20)",
  railActive: "inset 0 0 0 1px #7FA9F5, 0 1px 2px rgba(16, 26, 43, .05)",
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

/**
 * 模糊与玻璃（规范 §4.5）——**已退役**（2026-09-20）。
 *
 * 保留导出只为兼容旧引用：取值是 `none`，任何仍把它用到 backdrop-filter 的地方
 * 都不再产生模糊。新代码禁止再用玻璃拟态，也不要新增半透明卡片面。
 */
export const glass = {
  blur: "none",
} as const;

/** 布局尺寸（规范 §6.1 / §6.2）。 */
export const layout = {
  railExpanded: 248,
  railCollapsed: 72,
  topbarHeight: 60,
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
