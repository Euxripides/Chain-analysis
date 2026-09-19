/**
 * @chain/graph-react —— 图谱的 React 桥（用户端重构规范 V4 §30.3）
 *
 * 边界（§30.1）：**React 不渲染图谱节点。** 本包只做三件事：
 *   1. 把 React 的数据变化推给引擎；
 *   2. 把引擎事件收敛成少量 DOM 状态（选中 / hover / 相机）；
 *   3. 渲染容器、降级提示与可访问对象列表（§55.2）。
 *
 * 尚未实现的桥接方法（属于 R6 后续阶段，**不做空实现**以避免"看起来能用"）：
 *   setFilter / setLens / highlightPath / playPath / pausePath / setTimeline / setLOD
 *   —— 分别对应 §60 的 G3（worker 布局与聚合）、G4（LOD）、G5（路径播放与时间轴）。
 */
export { FundGraph } from "./FundGraph";
export type { FundGraphProps } from "./FundGraph";
export { useGraphEngine } from "./useGraphEngine";
export type { UseGraphEngineOptions, UseGraphEngineResult } from "./useGraphEngine";
