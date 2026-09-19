import React from "react";
import { Button, Tooltip } from "antd";
import type { ButtonProps as AntButtonProps } from "antd";
import { motion } from "motion/react";
import { cx } from "../utils";
import { duration, easing } from "../theme/motion";

/**
 * 按钮（用户端重构规范 V4 §11 / §12）
 *
 * 封装策略：
 *   - 结构、键盘行为、loading/disabled 语义全部交给 Ant Button（成熟能力）；
 *   - 视觉全部来自 `chainSightTheme` 的 Button token，本文件不写 `.ant-*` 覆写；
 *   - **按压反馈是 Motion 独占的**（§9.6 Animation Ownership），因此
 *     adapter.css 关掉了 Ant 的 Wave 涟漪（`.cs-btn .ant-wave { display: none }`），
 *     保证同一状态只有一个动画所有者——不会出现 "Wave + scale + ripple" 三套叠加。
 *
 * 状态完备性（§11.2）：default / hover / focus-visible / active / disabled /
 * loading / success-one-shot / error-one-shot。
 */

/*
 * `motion.create(Button)` 让 Ant Button 成为 Motion 可以驱动的元素。
 *
 * 需要一次显式断言：Motion 的 `onDrag` 签名与 React 的 `DragEventHandler` 不兼容，
 * 而 Ant 的 ButtonProps 里恰好声明了后者。这是**纯类型层面的**冲突——
 * 两边的拖拽事件本来就不会同时被使用（CsButton 从不传 onDrag），
 * 运行时行为正确。断言收窄在这一次转换里，不外泄到调用方。
 */
type MotionButtonProps = AntButtonProps & {
  whileTap?: { scale: number };
  transition?: { duration: number; ease: readonly number[] | number[] };
};
const MotionButton = motion.create(Button) as unknown as React.ComponentType<MotionButtonProps>;

export type CsButtonVariant = "primary" | "secondary" | "ghost" | "text" | "danger" | "success";

export interface CsButtonProps extends Omit<AntButtonProps, "type" | "variant" | "danger" | "className"> {
  variant?: CsButtonVariant;
  /** loading 时显示的真实忙碌文案（§11.3：禁止文字切换导致按钮跳动）。 */
  busyLabel?: string;
  /** 拖尾箭头：hover 时右移 3px（§11.3）。 */
  trailingArrow?: React.ReactNode;
  /** 一次性成功反馈：只在明确同步成功的轻操作上使用（§11.3）。 */
  successOnce?: boolean;
  className?: string;
  children?: React.ReactNode;
}

function antTypeFor(variant: CsButtonVariant): { type: AntButtonProps["type"]; danger?: boolean } {
  switch (variant) {
    case "primary":
      return { type: "primary" };
    case "danger":
      return { type: "primary", danger: true };
    case "success":
      return { type: "primary" };
    case "ghost":
      return { type: "default" };
    case "text":
      return { type: "text" };
    case "secondary":
    default:
      return { type: "default" };
  }
}

export function CsButton({
  variant = "secondary",
  busyLabel,
  trailingArrow,
  successOnce,
  className,
  children,
  loading,
  disabled,
  ...rest
}: CsButtonProps) {
  const { type, danger } = antTypeFor(variant);
  /*
   * 文案规则（§11.3）：
   *   - loading 时换成真实 busyLabel（而不是笼统的"加载中"）；
   *   - 按钮宽度保持——用 visibility 隐藏原文案而不是卸载它，
   *     这样 Ant 的 loading 图标出现时按钮不会因为文字变短而跳动。
   */
  const showBusy = Boolean(loading) && Boolean(busyLabel);
  const label = showBusy ? (
    <>
      <span style={{ visibility: "hidden" }}>{children}</span>
      <span
        style={{
          position: "absolute",
          inset: 0,
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
        }}
      >
        {busyLabel}
      </span>
    </>
  ) : (
    <>
      {children}
      {trailingArrow ? <span className="cs-btn__arrow">{trailingArrow}</span> : null}
    </>
  );

  return (
    <MotionButton
      {...rest}
      type={type}
      danger={danger}
      loading={loading}
      disabled={disabled || successOnce}
      className={cx("cs-btn", successOnce && "cs-btn--success", className)}
      style={{ position: "relative", ...(rest.style ?? {}) }}
      whileTap={disabled || loading ? undefined : { scale: 0.98 }}
      transition={{ duration: duration.micro, ease: easing.enter }}
    >
      {label}
    </MotionButton>
  );
}

/* --------------------------------------------------------------- IconButton */

export interface CsIconButtonProps extends Omit<AntButtonProps, "type" | "className" | "children" | "size"> {
  /** 必填：纯图标按钮必须有无障碍名称，不允许"只有图标但不知道作用"（§12.1）。 */
  label: string;
  /** Tooltip 延时 300ms（§12.1）。 */
  tooltip?: string;
  size?: "sm" | "md";
  className?: string;
  children: React.ReactNode;
}

export function CsIconButton({ label, tooltip, size = "md", className, children, ...rest }: CsIconButtonProps) {
  const button = (
    <MotionButton
      {...rest}
      type="text"
      aria-label={label}
      className={cx("cs-iconbutton", size === "sm" && "cs-iconbutton--sm", className)}
      whileTap={rest.disabled ? undefined : { scale: 0.92 }}
      transition={{ duration: duration.micro, ease: easing.enter }}
    >
      {children}
    </MotionButton>
  );
  return tooltip ? (
    <Tooltip title={tooltip} mouseEnterDelay={0.3}>
      {button}
    </Tooltip>
  ) : (
    button
  );
}
