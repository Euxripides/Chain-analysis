import type { Transition, Variants } from "motion/react";

/**
 * 下载页动效令牌与 variants（《新建数据下载 Flowline 界面重构实施规范》§18）
 *
 * 技术选型固定为已安装的 `motion` 12（通过 `motion/react` 引入，§18.1）：
 * 不引入 GSAP / Anime.js / 第二套通用动画库，也不新增加载字体或图片。
 *
 * 分工（§18.1）：
 *   CSS  —— 背景低频装饰、focus/hover 过渡、简单 skeleton；
 *   Motion —— Flowline pathLength、步骤节点状态切换、范围字段进出、
 *             Dataset 选择反馈、Coverage 圆环首次进入、Dialog 空间连续性。
 *
 * 所有数值都写成秒（Motion 的时间单位），并在这里集中定义：
 * 组件里不允许再出现散落的魔法时长。
 */

export const downloadMotion = {
  micro: 0.14,
  fast: 0.18,
  standard: 0.26,
  spatial: 0.42,
  /**
   * Coverage 圆环：500–700ms（整改规范 §8.3）。距离与信息量都比 spatial 大，
   * 因此单独一个令牌，而不是把 spatial 机械复制到圆环上。
   * 注意：Dialog 的进出场时长**不在**这里 —— 那是共享 renderer 的职责（REM-009）。
   */
  ring: 0.6,
  stagger: 0.045,
} as const;

/** 进入缓动（§18.3）。 */
export const DL_EASE_ENTER: [number, number, number, number] = [0.16, 1, 0.3, 1];
/** 退出缓动（§18.3）。 */
export const DL_EASE_EXIT: [number, number, number, number] = [0.4, 0, 1, 1];

/** 恒速光点（§18.3 / §18.5）。 */
export const DL_EASE_LINEAR = "linear" as const;

/** 节点状态切换的 spring（§18.3：stiffness 220 / damping 26 / mass 0.8）。 */
export const downloadSpring: Transition = { type: "spring", stiffness: 220, damping: 26, mass: 0.8 };

/** 退出时长约为进入的 65%（§12.2 范围字段交叉淡入）。 */
export const DL_EXIT_RATIO = 0.65;

/**
 * 光点循环周期（§10.5：3.6–4.8 秒）。
 * 取区间中值，且光点在页面不可见时暂停（组件用 visibilitychange 控制）。
 */
export const DL_RAIL_DOT_SECONDS = 4.2;

/** 减少动效时的零时长：保留最终状态，不保留位移/缩放。 */
export function dlDuration(seconds: number, reducedMotion: boolean): number {
  return reducedMotion ? 0 : seconds;
}

/* ------------------------------------------------------------------ variants */

/**
 * 页面进入（整改规范 §8.4）：只有三组（标题 / Flowline / 工作区），
 * 每组 260ms + 45ms 递延，总时长 350ms < 420ms；不逐卡片 stagger。
 */
export const dlPageEnter: Variants = {
  hidden: { opacity: 0, y: 6 },
  show: { opacity: 1, y: 0 },
};

export function dlPageEnterStep(reducedMotion: boolean, delay: number): Transition {
  return { duration: reducedMotion ? 0 : downloadMotion.standard, ease: DL_EASE_ENTER, delay: reducedMotion ? 0 : delay };
}

/** 页面首次进入（§18.4）：头部、轨道、工作区分组进入，260–420ms。 */
export const dlPageStagger: Variants = {
  hidden: {},
  show: { transition: { staggerChildren: downloadMotion.stagger, delayChildren: 0.02 } },
};

export const dlGroupEnter: Variants = {
  hidden: { opacity: 0, y: 12 },
  show: { opacity: 1, y: 0, transition: { duration: downloadMotion.spatial, ease: DL_EASE_ENTER } },
};

/** 预览返回：圆环与指标分组进入（§18.4，420ms）。 */
export const dlRevealStagger: Variants = {
  hidden: {},
  show: { transition: { staggerChildren: downloadMotion.stagger } },
};

export const dlRevealItem: Variants = {
  hidden: { opacity: 0, y: 8 },
  show: { opacity: 1, y: 0, transition: { duration: downloadMotion.spatial, ease: DL_EASE_ENTER } },
};

/** 范围类型切换（§12.2 / §18.4）：交叉淡入 + 8px 位移，退出约为进入的 65%。 */
export const dlRangeSwap: Variants = {
  hidden: { opacity: 0, y: 8 },
  show: { opacity: 1, y: 0, transition: { duration: downloadMotion.standard, ease: DL_EASE_ENTER } },
  exit: {
    opacity: 0,
    y: -8,
    transition: { duration: downloadMotion.standard * DL_EXIT_RATIO, ease: DL_EASE_EXIT },
  },
};

/** 步骤节点状态切换（§18.4，180ms）。 */
export const dlNodeSwitch: Variants = {
  done: { scale: 1, transition: { duration: downloadMotion.fast, ease: DL_EASE_ENTER } },
  current: { scale: 1.06, transition: { duration: downloadMotion.fast, ease: DL_EASE_ENTER } },
  todo: { scale: 1, transition: { duration: downloadMotion.fast, ease: DL_EASE_ENTER } },
};

/** Dataset 选中反馈（§18.4，180ms）：边框扫光 + 勾选路径出现，不缩放文字。 */
export const dlDatasetSelect: Variants = {
  idle: { borderColor: "var(--dl-border)", transition: { duration: downloadMotion.fast, ease: DL_EASE_ENTER } },
  selected: { borderColor: "var(--dl-primary)", transition: { duration: downloadMotion.fast, ease: DL_EASE_ENTER } },
};

export const dlCheckPath: Variants = {
  hidden: { pathLength: 0, opacity: 0 },
  show: { pathLength: 1, opacity: 1, transition: { duration: downloadMotion.fast, ease: DL_EASE_ENTER } },
};

/**
 * 失效预览的一次性琥珀高光（§14.4，420ms）。
 * 只改边框与状态标识，**不降低容器 opacity**。
 */
export const dlStaleHighlight: Variants = {
  rest: { borderColor: "var(--dl-warning)" },
  flash: {
    borderColor: ["var(--dl-warning)", "rgba(166, 95, 0, .35)", "var(--dl-warning)"],
    transition: { duration: downloadMotion.spatial, ease: DL_EASE_ENTER },
  },
};

/** Dialog 内容的错峰进入（§17.2 / §18.4：Dialog 空间连续性）。 */
export const dlDialogStagger: Variants = {
  hidden: {},
  show: { transition: { staggerChildren: downloadMotion.stagger, delayChildren: 0.04 } },
};

/* Dialog 内容的分组进入：时长用页面级 standard，Dialog 面板本身的进出场由共享 renderer 负责（REM-009）。 */
export const dlDialogItem: Variants = {
  hidden: { opacity: 0, y: 8 },
  show: { opacity: 1, y: 0, transition: { duration: downloadMotion.standard, ease: DL_EASE_ENTER } },
};
