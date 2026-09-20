import React, { useEffect, useMemo, useRef } from "react";
import { animate, motion, useMotionValue, useTransform } from "motion/react";
import { Check } from "lucide-react";
import { downloadStepIndex, downloadStepState } from "./download.types";
import { DL_EASE_ENTER, DL_RAIL_DOT_SECONDS, downloadMotion, downloadSpring } from "./download.motion";

/**
 * DownloadFlowRail —— 下载页专用 Flowline 数据轨道（规范 §10）
 *
 * 为什么不复用 @chain/ui 的通用 `Stepper`（§10.7）：那条轨道服务"发起调查"等
 * 既有页面，改它会连带改变那些页面的视觉与断言。本组件是页面专用实现，
 * 只消费步骤数组与当前步骤，不持有任何业务状态。
 *
 * 结构：
 *   <ol aria-label="新建数据下载步骤">   每一步一个真实 <button type="button">
 *     · 当前项 aria-current="step"；未到达项 disabled；已完成项可点击回跳
 *     · 装饰性 SVG（轨道 / 渐变 / 光点）全部 aria-hidden="true"
 *
 * 轨道几何（§10.3）：viewBox 0 0 1000 96，节点中心 x = 60 / 280 / 500 / 720 / 940，
 * y 统一为 34（标签基线因此完全对齐），段与段之间用幅度 8px 的二次曲线起伏，
 * 形成"平滑双曲线"，而不是一条直线。
 *
 * 进度（§10.4）：progress = max(0, currentIndex) / max(1, steps.length - 1)，
 * 用 Motion 的 pathLength 表达 —— 不改变 SVG 宽度、不触发布局。
 *
 * 光点（§10.5）：默认 1 个主光点 + 最多 2 个更弱的跟随点，周期 4.2s（3.6–4.8s 区间内），
 * 页面不可见时暂停；`prefers-reduced-motion: reduce` 时完全不渲染移动光点。
 */

export interface DownloadRailStep {
  key: string;
  label: string;
  /** 窄屏（640–899px）标签；缺省时用 label（§7.4）。 */
  shortLabel?: string;
}

export interface DownloadFlowRailProps {
  steps: ReadonlyArray<DownloadRailStep>;
  current: string;
  onJump: (key: string) => void;
  orientation?: "horizontal" | "vertical";
  reducedMotion: boolean;
  /** 正在运行的服务端请求对应的步骤 key（§14.2：第 4 节点显示运行状态）。 */
  running?: string | null;
  /** 是否使用窄标签（640–899px）。 */
  compactLabels?: boolean;
  /** 页面是否可见：不可见时暂停光点循环（§10.5）。 */
  pageVisible?: boolean;
  /**
   * 一次性数据脉冲（整改规范 §8.5）：地址解析成功后，从地址区向第 1 个节点
   * 播放一次 420ms 的数据流。值变化即触发一次，不循环、不驻留。
   */
  pulseToken?: number;
}

/* 节点中心：x 与 y 都在 viewBox 坐标系里，y 统一 34 -> 标签基线一致。 */
const NODE_X = [60, 280, 500, 720, 940] as const;
const NODE_Y = 34;
const VIEW_W = 1000;
const VIEW_H = 96;

/** 段间起伏 ±8px 的平滑二次曲线：控制点上下交替，曲线本身恰好穿过每个节点。 */
const RAIL_PATH = [
  "M " + NODE_X[0] + " " + NODE_Y,
  "Q 170 26 " + NODE_X[1] + " " + NODE_Y,
  "Q 390 42 " + NODE_X[2] + " " + NODE_Y,
  "Q 610 26 " + NODE_X[3] + " " + NODE_Y,
  "Q 830 42 " + NODE_X[4] + " " + NODE_Y,
].join(" ");

const GRADIENT_ID = "dl-rail-gradient";
const SVG_NS = "http://www.w3.org/2000/svg";

/** 光点沿轨道移动（§10.5）：1 个主点 + 2 个跟随点，全部由 Motion value 驱动，不触发 React 重渲染。 */
function RailDots({
  progress,
  reducedMotion,
  pageVisible,
}: {
  progress: number;
  reducedMotion: boolean;
  pageVisible: boolean;
}) {
  /** 与可见 path 完全同一条 d 的测量元素：不挂 pathLength，长度与点位都是原始用户坐标。 */
  const measurePath = useMemo(() => {
    const path = document.createElementNS(SVG_NS, "path");
    path.setAttribute("d", RAIL_PATH);
    return path;
  }, []);

  const total = useMemo(() => {
    try {
      const value = measurePath.getTotalLength();
      return Number.isFinite(value) && value > 0 ? value : 0;
    } catch {
      return 0;
    }
  }, [measurePath]);

  const frac = useMotionValue(0);
  const controlsRef = useRef<ReturnType<typeof animate> | null>(null);
  const at = (value: number) => {
    if (total === 0) return { x: NODE_X[0], y: NODE_Y };
    const length = Math.min(total * progress, total) * value;
    try {
      return measurePath.getPointAtLength(length);
    } catch {
      return { x: NODE_X[0], y: NODE_Y };
    }
  };
  const pct = (value: number) => ((value / VIEW_W) * 100).toFixed(3) + "%";
  const px = (value: number) => value.toFixed(2) + "px";
  const mainX = useTransform(frac, (value) => pct(at(value).x));
  const mainY = useTransform(frac, (value) => px(at(value).y));
  const trailX = useTransform(frac, (value) => pct(at(Math.max(0, value - 0.07)).x));
  const trailY = useTransform(frac, (value) => px(at(Math.max(0, value - 0.07)).y));
  const tailX = useTransform(frac, (value) => pct(at(Math.max(0, value - 0.14)).x));
  const tailY = useTransform(frac, (value) => px(at(Math.max(0, value - 0.14)).y));

  /* 可见性用 ref 读取：动画只在挂载/reducedMotion/轨道长度变化时创建一次，
     否则"页面隐藏"会重建动画并把它从 0 重新播一遍（REM-006 的 pause 语义就失去意义）。 */
  const pageVisibleRef = useRef(pageVisible);
  pageVisibleRef.current = pageVisible;

  useEffect(() => {
    if (reducedMotion || total === 0) {
      frac.set(1);
      return;
    }
    const controls = animate(frac, [0, 1], {
      duration: DL_RAIL_DOT_SECONDS,
      ease: "linear",
      repeat: Infinity,
      repeatType: "loop",
    });
    controlsRef.current = controls;
    if (!pageVisibleRef.current) controls.pause();
    return () => {
      controlsRef.current = null;
      controls.stop();
    };
  }, [frac, reducedMotion, total]);

  /* 页面不可见时暂停（§10.5 / §30）：不是卸载，回来时从当前位置继续。 */
  useEffect(() => {
    const controls = controlsRef.current;
    if (!controls) return;
    if (pageVisible) controls.play();
    else controls.pause();
  }, [pageVisible]);

  if (reducedMotion || total === 0) return null;

  return (
    <div className="dl-rail__dots" aria-hidden="true">
      <motion.span className="dl-rail__dot dl-rail__dot--trail" style={{ left: tailX, top: tailY }} />
      <motion.span className="dl-rail__dot dl-rail__dot--trail" style={{ left: trailX, top: trailY }} />
      <motion.span className="dl-rail__dot dl-rail__dot--main" style={{ left: mainX, top: mainY }} />
    </div>
  );
}

export function DownloadFlowRail({
  steps,
  current,
  onJump,
  orientation = "horizontal",
  reducedMotion,
  running = null,
  compactLabels = false,
  pageVisible = true,
  pulseToken = 0,
}: DownloadFlowRailProps) {
  const currentIndex = Math.max(0, downloadStepIndex(current));
  const progress = currentIndex / Math.max(1, steps.length - 1);

  const renderNodeBody = (state: string, index: number) =>
    state === "done" ? <Check size={16} strokeWidth={3} aria-hidden="true" /> : <span className="dl-rail__index">{index + 1}</span>;

  const nodeVariants = {
    done: { scale: 1 },
    current: { scale: 1.06 },
    todo: { scale: 1 },
  };

  if (orientation === "vertical") {
    return (
      <nav className="dl-vrail" aria-label="新建数据下载步骤导航">
        <ol className="dl-vrail__list" aria-label="新建数据下载步骤">
          {steps.map((step, index) => {
            const state = downloadStepState(step.key, current);
            const isRunning = running === step.key;
            return (
              <li className="dl-vrail__item" key={step.key} data-state={state}>
                <button
                  type="button"
                  className="dl-vrail__node"
                  data-state={state}
                  data-running={isRunning ? "true" : undefined}
                  aria-current={state === "current" ? "step" : undefined}
                  disabled={state === "todo"}
                  onClick={() => onJump(step.key)}
                >
                  <motion.span
                    className="dl-vrail__nodebody"
                    initial={false}
                    animate={state === "current" ? "current" : state === "done" ? "done" : "todo"}
                    variants={nodeVariants}
                    transition={reducedMotion ? { duration: 0 } : downloadSpring}
                  >
                    {renderNodeBody(state, index)}
                  </motion.span>
                  <span className="dl-vrail__label">
                    {compactLabels && step.shortLabel ? step.shortLabel : step.label}
                  </span>
                </button>
                {index < steps.length - 1 ? (
                  <span className="dl-vrail__line" data-active={index < currentIndex ? "true" : undefined} aria-hidden="true" />
                ) : null}
              </li>
            );
          })}
        </ol>
      </nav>
    );
  }

  return (
    <nav className="dl-rail" aria-label="新建数据下载步骤导航">
      <div className="dl-flow-rail__inner">
      <svg
        className="dl-rail__track"
        viewBox={"0 0 " + VIEW_W + " " + VIEW_H}
        preserveAspectRatio="none"
        aria-hidden="true"
        focusable="false"
      >
        <defs>
          <linearGradient id={GRADIENT_ID} x1="0" y1="0" x2="1" y2="0">
            <stop offset="0%" stopColor="var(--dl-primary)" />
            <stop offset="100%" stopColor="var(--dl-cyan)" />
          </linearGradient>
        </defs>
        {/* 第二条弱轨道：1px、透明度 .35，形成数据双通道感（§10.3）。 */}
        <path className="dl-rail__echo" d={RAIL_PATH} transform="translate(0 7)" vectorEffect="non-scaling-stroke" />
        {/* 底轨：2px（§10.3）。 */}
        <path className="dl-rail__base" d={RAIL_PATH} vectorEffect="non-scaling-stroke" />
        {/* 激活轨道：蓝青渐变，长度由 pathLength 表达（§10.4）。 */}
        <motion.path
          className="dl-rail__active"
          d={RAIL_PATH}
          stroke={"url(#" + GRADIENT_ID + ")"}
          vectorEffect="non-scaling-stroke"
          strokeDasharray="0 1"
          initial={false}
          animate={{ pathLength: progress }}
          transition={reducedMotion ? { duration: 0 } : { duration: downloadMotion.spatial, ease: DL_EASE_ENTER }}
        />
      </svg>

      <RailDots progress={progress} reducedMotion={reducedMotion} pageVisible={pageVisible} />

      {/* 地址解析的一次性数据脉冲（§8.5）：从左侧入口画到第 1 个节点，只画一次。 */}
      {pulseToken > 0 && !reducedMotion ? (
        <svg
          key={pulseToken}
          className="dl-rail__pulse"
          viewBox={"0 0 " + VIEW_W + " " + VIEW_H}
          preserveAspectRatio="none"
          aria-hidden="true"
          focusable="false"
        >
          <motion.path
            className="dl-rail__pulsepath"
            d={"M 0 " + NODE_Y + " L " + NODE_X[0] + " " + NODE_Y}
            vectorEffect="non-scaling-stroke"
            strokeLinecap="round"
            initial={{ pathLength: 0, opacity: 0 }}
            animate={{ pathLength: 1, opacity: [0, 1, 1, 0] }}
            transition={{ duration: downloadMotion.spatial, ease: DL_EASE_ENTER, times: [0, 0.08, 0.6, 1] }}
          />
        </svg>
      ) : null}

      <ol className="dl-rail__steps" aria-label="新建数据下载步骤">
        {steps.map((step, index) => {
          const state = downloadStepState(step.key, current);
          const isRunning = running === step.key;
          const left = ((NODE_X[Math.min(index, NODE_X.length - 1)] / VIEW_W) * 100).toFixed(3) + "%";
          return (
            <li className="dl-rail__item" key={step.key} data-state={state} style={{ left }}>
              <motion.button
                type="button"
                className="dl-rail__node"
                data-state={state}
                data-running={isRunning ? "true" : undefined}
                aria-current={state === "current" ? "step" : undefined}
                disabled={state === "todo"}
                onClick={() => onJump(step.key)}
                initial={false}
                animate={state === "current" ? "current" : state === "done" ? "done" : "todo"}
                variants={nodeVariants}
                transition={reducedMotion ? { duration: 0 } : downloadSpring}
              >
                <span className="dl-rail__nodebody" aria-hidden="true">
                  {renderNodeBody(state, index)}
                </span>
                <span className="dl-rail__label">
                  {compactLabels && step.shortLabel ? step.shortLabel : step.label}
                </span>
                {/*
                  桌面轨道高度被压到 72–80px（整改规范 §6.5），节点 + 标签已经占满，
                  副标签在桌面不再渲染；步骤状态由节点填充/勾选与 aria-current 表达。
                */}
                <span className="visually-hidden">
                  {state === "done" ? "已完成" : state === "current" ? (isRunning ? "进行中" : "当前步骤") : "未开始"}
                </span>
              </motion.button>
            </li>
          );
        })}
      </ol>
      </div>
    </nav>
  );
}

export default DownloadFlowRail;