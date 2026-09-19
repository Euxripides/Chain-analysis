import React from "react";
import { MotionConfig, motion } from "motion/react";
import type { TargetAndTransition, Transition } from "motion/react";
import { duration, easing, pageEnter, spring, stepForward } from "../theme/motion";

/**
 * Motion 封装（用户端重构规范 V4 §9 / §62）
 *
 * 这里是**唯一的动效入口**：业务页面不再自己写 duration / spring / variants。
 * 每个预设都对应规范里的一条动画所有权规则（§9.6），避免同一状态被多套动画驱动。
 */

/** 全局 Motion 配置：`reducedMotion="user"` 与 CSS 的 prefers-reduced-motion 双保险（§3）。 */
export function CsMotionConfig({ children }: { children: React.ReactNode }) {
  return <MotionConfig reducedMotion="user">{children}</MotionConfig>;
}

/** 页面进入（§9.3 Standard Route）：opacity + y 8→0，220ms。 */
export const csPageInitial: TargetAndTransition = pageEnter.initial;
export const csPageAnimate: TargetAndTransition = pageEnter.animate;

export const csPageTransition: Transition = {
  duration: duration.standard,
  ease: easing.enter,
};

/**
 * 重叠式页面切换。
 *
 * **不使用 `AnimatePresence mode="wait"`**（§9.3 明确禁止）：新页面立刻挂载，
 * 旧页面只做极短 opacity 离场，不阻塞新页面的数据获取。
 */
export function CsPageTransition({
  routeKey,
  className,
  children,
  dataPolicy,
  dataRoute,
}: {
  /** 路由标识；变化时触发一次进入动画。 */
  routeKey: string;
  className?: string;
  children: React.ReactNode;
  /** 透出的策略标记，供验收脚本区分"这一页有没有页面级动画"（见 App.tsx 的 RoutedContent）。 */
  dataPolicy?: string;
  dataRoute?: string;
}) {
  return (
    <motion.div
      key={routeKey}
      className={className}
      data-policy={dataPolicy}
      data-route={dataRoute}
      initial={csPageInitial}
      animate={csPageAnimate}
      transition={csPageTransition}
    >
      {children}
    </motion.div>
  );
}

/**
 * 首屏区域 stagger（§9.4）。
 * 只对首屏 6–8 个主要区块使用；表格行绝不逐行 stagger。
 */
export function CsStagger({
  children,
  className,
  step = 0.028,
  max = 8,
}: {
  children: React.ReactNode;
  className?: string;
  /** 每个子项之间的延迟（秒）。 */
  step?: number;
  /** 参与 stagger 的最大子项数，超出的子项不再延迟。 */
  max?: number;
}) {
  const items = React.Children.toArray(children);
  return (
    <div className={className}>
      {items.map((child, index) => (
        <motion.div
          key={index}
          initial={{ opacity: 0, y: 6 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{
            duration: duration.standard,
            ease: easing.enter,
            delay: Math.min(index, max) * step,
          }}
          style={{ minWidth: 0 }}
        >
          {child}
        </motion.div>
      ))}
    </div>
  );
}

/**
 * 步骤切换（§16.2）：前进 old x 0→-8 / new x 8→0，总时长 180–220ms。
 * 用 `mode="wait"` 是**刻意**的——步骤切换必须先卸载旧步骤再挂载新步骤，
 * 且已填数据保存在父级状态里，不会因卸载丢失（§16.2 最后一条）。
 */
export function CsStepTransition({
  stepKey,
  children,
  className,
}: {
  stepKey: string | number;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <motion.div
      key={stepKey}
      className={className}
      initial={stepForward.initial}
      animate={stepForward.animate}
      exit={stepForward.exit}
      transition={stepForward.transition}
    >
      {children}
    </motion.div>
  );
}

/**
 * 按压反馈（§11.3 / §12.1）。
 *
 * 抽成组件而不是让每个按钮自己写：动画所有权必须只有一个（§9.6）。
 * 使用 `scale` 而不是 CSS transform，以便与 Ant 的状态样式解耦。
 */
export function CsPressable({
  children,
  className,
  pressScale = 0.98,
  disabled,
}: {
  children: React.ReactNode;
  className?: string;
  pressScale?: number;
  disabled?: boolean;
}) {
  return (
    <motion.span
      className={className}
      style={{ display: "inline-flex" }}
      whileTap={disabled ? undefined : { scale: pressScale }}
      transition={{ duration: duration.micro, ease: easing.enter }}
    >
      {children}
    </motion.span>
  );
}

/**
 * 对象内容 crossfade（§22.3 / §31.3）。
 *
 * Inspector 切换选中对象时只淡入新内容，不重排整块布局。
 * 仅做 opacity：位移会让"阅读位置"漂移，长时间调查时非常干扰。
 */
export function CsContentFade({
  contentKey,
  children,
  className,
}: {
  contentKey: string;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <motion.div
      key={contentKey}
      className={className}
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      transition={{ duration: duration.fast, ease: easing.enter }}
    >
      {children}
    </motion.div>
  );
}

/** 共享布局的 spring（§9.2 Spring UI）：Tabs 指示器、Segmented active 背景。 */
export const csLayoutSpring: Transition = spring.ui;

export { motion, MotionConfig };
export { spring };
