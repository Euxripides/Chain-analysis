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
 *   - 生命周期由 `dispose()` 收口（§63C）。
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
export type { RenderStyle } from "./renderer/WebGLRenderer";
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
export { buildCsr } from "./model/indices";
export type { CsrIndex, LayoutGraph } from "./model/indices";
export { attachLayoutWorker, handleLayoutRequest } from "./worker/handleLayoutRequest";
export { layoutTransferables } from "./worker/protocol";
export type { LayoutDone, LayoutFailed, LayoutRequest, LayoutResponse } from "./worker/protocol";
