import type { RenderStyle } from "../renderer/WebGLRenderer";

/**
 * 视图维度（用户端重构规范 V4 §30.8 的"看什么"维度；工具栏 `视图维度`）
 *
 * 为什么把这张表放在**引擎**而不是页面：
 * 现有 SVG 画布把同一件事拆在三处写——工具栏的标签在页面常量里、
 * 证据透镜的琥珀色 `#b45309` 硬编码在 `edgeColor()` 里、
 * "只留证据节点一跳邻居"又写在 `useMemo` 里。
 * 三处各自演化，任何一处改动都不会提示另外两处。
 *
 * 这里把它收敛成**一张效果表**：引擎负责样式类效果（虚线 / 淡化 / 分组着色），
 * 页面负责数据类效果（可见性过滤、副标签内容）——
 * 但两者读的是同一份 `LENS_EFFECTS`，不会再各写一套。
 */

export type Lens = "flow" | "time" | "asset" | "evidence";

export interface LensOption {
  key: Lens;
  label: string;
}

/** 工具栏顺序即规范顺序：资金流 / 时间 / 资产 / 证据。 */
export const LENS_OPTIONS: LensOption[] = [
  { key: "flow", label: "资金流" },
  { key: "time", label: "时间" },
  { key: "asset", label: "资产" },
  { key: "evidence", label: "证据" },
];

export interface LensEffect {
  /** 边用虚线画（证据透镜：证据链是"引用"关系，不是资金流）。 */
  dashed: boolean;
  /** 非证据节点淡化（§30.10 select 用同一个 dimmed 通道）。 */
  dimNonEvidence: boolean;
  /** 边按 `colorGroup` 取色（资产透镜：一眼区分不同 token）。 */
  colorByGroup: boolean;
  /** 节点标签带副标签（时间透镜：首次出现 → 最近出现）。 */
  sublabel: boolean;
  /** 只保留证据节点及其一跳邻居（数据类效果，由页面执行）。 */
  evidenceOnly: boolean;
}

export const LENS_EFFECTS: Record<Lens, LensEffect> = {
  flow: { dashed: false, dimNonEvidence: false, colorByGroup: false, sublabel: false, evidenceOnly: false },
  time: { dashed: false, dimNonEvidence: false, colorByGroup: false, sublabel: true, evidenceOnly: false },
  asset: { dashed: false, dimNonEvidence: false, colorByGroup: true, sublabel: false, evidenceOnly: false },
  evidence: { dashed: true, dimNonEvidence: true, colorByGroup: false, sublabel: false, evidenceOnly: true },
};

/**
 * 资产调色板的色相散得开，且**都是有足够对比度的深色**：
 * 它们要画在淡蓝背景上并被区分开，浅色相会在同一屏里糊成一片。
 * 与现有 SVG 画布逐字一致，避免重构后同一份数据换了颜色。
 */
export const ASSET_PALETTE: Array<[number, number, number]> = [
  [0.09, 0.361, 0.827], // #175cd3
  [0.024, 0.463, 0.278], // #067647
  [0.706, 0.325, 0.035], // #b45309
  [0.498, 0.337, 0.851], // #7f56d9
  [0.757, 0.082, 0.455], // #c11574
  [0.031, 0.541, 0.698], // #088ab2
];

/** 各透镜下的基础边色（证据透镜用琥珀色，与 SVG 一致）。 */
export const LENS_EDGE_COLORS: Record<Lens, [number, number, number]> = {
  flow: [0.09, 0.361, 0.827], // #175cd3
  time: [0.278, 0.329, 0.404], // #475467
  asset: [0.09, 0.361, 0.827],
  evidence: [0.706, 0.325, 0.035], // #b45309
};

/** 虚线的开关周期（屏幕像素）：7 on / 5 off，与 SVG 的 `strokeDasharray "7 5"` 一致。 */
export const EVIDENCE_DASH = { on: 7, off: 5 };

/**
 * 由基础样式 + 当前透镜推导出渲染样式。
 *
 * 纯函数：给定同样的输入永远得到同样的样式，因此可以被逐字段断言，
 * 不需要渲染任何东西就能验证"证据透镜确实是虚线 + 琥珀色 + 分组着色关闭"。
 */
export function lensStyle(lens: Lens, base: RenderStyle): RenderStyle {
  const effect = LENS_EFFECTS[lens];
  return {
    ...base,
    edgeColor: LENS_EDGE_COLORS[lens],
    edgeColorMode: effect.colorByGroup ? "group" : "uniform",
    edgeGroupColors: ASSET_PALETTE,
    edgeDash: effect.dashed ? EVIDENCE_DASH : null,
  };
}

/**
 * 资产名 → 调色板下标。
 *
 * 用哈希而不是"按出现顺序编号"：资产到颜色的映射必须**稳定**。
 * 按顺序编号时，图上新增一种 token 就会让已有 token 集体换色，
 * 正在看图的人会以为资产变了。
 */
export function assetColorGroup(asset: string | undefined): number {
  if (!asset) return 0;
  let hash = 0;
  for (const char of asset) hash = (hash * 31 + char.charCodeAt(0)) % 9973;
  return hash % ASSET_PALETTE.length;
}
