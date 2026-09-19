/**
 * 细节层次（用户端重构规范 V4 §30.8 / §30.15）
 *
 * 规范把图谱分成三个观察距离，每一档**画什么**是不同的：
 *   远景 —— 不显示 label、小节点聚合、**边降采样**、只展示 cluster 与主干资金流；
 *   中景 —— 重要节点 label、主要金额边，次要边弱化；
 *   近景 —— 地址短值、token / amount、entity tag、evidence marker。
 *
 * 本文件只回答"当前是哪一档、这一档该画多少"这两个问题，
 * 不碰 WebGL、不碰 React——因此它可以被纯函数式地断言。
 */

export type LodLevel = "far" | "mid" | "near";

export interface LodPolicy {
  /**
   * 这一档最多画多少条边。
   *
   * 为什么是"绝对上限"而不是"比例"：§30.15 给的是**帧率预算**，
   * 而帧率取决于顶点数而不是图的总规模。一张 100 万条边的图在远景下
   * 只画 2 万条，和一张 3 万条边的图在远景下全画，代价是一样的。
   * 用比例就会让大图永远画得更多，正好和"保帧率"的目标相反。
   */
  maxEdges: number;
  /** 低于该权重的边在这一档直接不画（0 = 不按权重过滤）。 */
  minEdgeWeight: number;
  /** 节点半径的视觉放大（世界单位不变，只在屏幕空间调整观感）。 */
  nodeScale: number;
  /**
   * 这一档是否绘制标签（§30.8）。
   *
   * 由 `LabelLayer` 读取，是"哪一档该有标签"的**唯一**来源——
   * 标签预算在别处再写一遍就会和这里漂移。
   */
  labels: boolean;
}

export const LOD_POLICIES: Record<LodLevel, LodPolicy> = {
  /* 远景：只留主干。20k 条边是"看得清资金走向"与"保得住 60fps"的折中。 */
  far: { maxEdges: 20_000, minEdgeWeight: 0, nodeScale: 0.6, labels: false },
  /* 中景：主要金额边 + 重要节点标签。 */
  mid: { maxEdges: 80_000, minEdgeWeight: 0, nodeScale: 0.85, labels: true },
  /* 近景：全画，进入逐对象核对。 */
  near: { maxEdges: Number.POSITIVE_INFINITY, minEdgeWeight: 0, nodeScale: 1, labels: true },
};

/**
 * 由相机缩放判定 LOD 档位。
 *
 * 阈值用的是 **scale（世界单位 → CSS 像素）**，因此与图本身的大小无关：
 * 一张巨大但被缩到很小的图，和一张本来就小的图，在同一 scale 下看到的细节量是一样的。
 */
export const LOD_NEAR_SCALE = 3;
export const LOD_MID_SCALE = 0.8;

export function lodLevelFor(scale: number): LodLevel {
  if (scale >= LOD_NEAR_SCALE) return "near";
  if (scale >= LOD_MID_SCALE) return "mid";
  return "far";
}

export function policyFor(level: LodLevel): LodPolicy {
  return LOD_POLICIES[level];
}
