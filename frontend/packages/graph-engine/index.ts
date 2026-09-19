/**
 * @chain/graph-engine —— 独立的资金图谱性能引擎（用户端重构规范 V4 §30）
 *
 * 与既有 Canvas 页面的关系（规范 §60 Phase R6 / §63G）：
 *   本包是 **G1（Core）+ G2（WebGL2 baseline）** 的交付物，独立可测、可回滚。
 *   它**尚未**接入 `/canvases/:canvasId`：R6 的后续阶段（G3 worker 布局、
 *   G4 LOD、G5 路径播放）完成并通过与现有 SVG 画布的等价性验证之前，
 *   生产页面继续使用原实现——避免"半成品引擎替换掉能用的画布"。
 *
 * 设计要点：
 *   - React 不渲染节点（§30.1）：本包不依赖 React；
 *   - 数据是 TypedArray + 字符串字典池（§30.4），字符串不复制 N 份；
 *   - 命中测试走拾取帧缓冲，不做 DOM hitbox（§30.7）；
 *   - 生命周期由 `dispose()` 收口（§63C）；
 *   - 标签是 GPU 字形图集（§30.2 `LabelProgram`），不是 DOM 覆盖层（§30.1）。
 */
export { GraphEngine } from "./core/GraphEngine";
export type { GraphEngineEvents, GraphEngineOptions } from "./core/GraphEngine";
export { EventBus } from "./core/EventBus";
export type { Listener } from "./core/EventBus";
export { GraphStore, NODE_FLAG, NODE_KIND, NODE_KIND_NAMES, kindIndex } from "./model/GraphStore";
export type { GraphEdgeInput, GraphNodeInput, NodeKindName } from "./model/GraphStore";
export { Camera, MAX_SCALE, MIN_SCALE, emptyBounds } from "./interaction/camera";
export type { Bounds, CameraState } from "./interaction/camera";
export { DEFAULT_STYLE, WebGLRenderer } from "./renderer/WebGLRenderer";
export type { PickResult, RenderStyle } from "./renderer/WebGLRenderer";
export {
  DEFAULT_LAYOUT_OPTIONS,
  applyCluster,
  applyColumns,
  applyForce,
  applyRing,
  applyTimeline,
  bfsLayers,
  runLayout,
  seededRandom,
} from "./layout";
export type { LayoutMode, LayoutOptions } from "./layout";
export { findPath } from "./model/paths";
export type { FindPathOptions, GraphPath } from "./model/paths";
export { PLAYBACK_SPEEDS, PlaybackController } from "./interaction/playback";
export type { PlaybackOptions, PlaybackState } from "./interaction/playback";
export { DEFAULT_PARTICLE_APPEARANCE, ParticleProgram } from "./renderer/ParticleProgram";
export type { ParticleAppearance } from "./renderer/ParticleProgram";
export { LabelAtlas } from "./renderer/LabelAtlas";
export type { AtlasStats, GlyphBox } from "./renderer/LabelAtlas";
export { LabelProgram } from "./renderer/LabelProgram";
export type { LabelInstance } from "./renderer/LabelProgram";
export {
  ASSET_PALETTE,
  EVIDENCE_DASH,
  LENS_EDGE_COLORS,
  LENS_EFFECTS,
  LENS_OPTIONS,
  assetColorGroup,
  lensStyle,
} from "./lenses";
export type { Lens, LensEffect, LensOption } from "./lenses";
export { LabelLayer, orderByDegree, shortId } from "./labels/LabelLayer";
export type { LabelLayerOptions, LabelPriority } from "./labels/LabelLayer";
export {
  LOD_MID_SCALE,
  LOD_NEAR_SCALE,
  LOD_POLICIES,
  lodLevelFor,
  policyFor,
} from "./perf/lod";
export type { LodLevel, LodPolicy, LodThresholds } from "./perf/lod";
export { DEFAULT_BUDGET_LIMITS, decideBudget } from "./perf/budget";
export type { BudgetDecision, BudgetLimits, RenderMode } from "./perf/budget";
export { buildCsr } from "./model/indices";
export type { CsrIndex, LayoutGraph } from "./model/indices";
export { attachLayoutWorker, handleLayoutRequest } from "./worker/handleLayoutRequest";
export { layoutTransferables } from "./worker/protocol";
export type { LayoutDone, LayoutFailed, LayoutRequest, LayoutResponse } from "./worker/protocol";
