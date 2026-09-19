import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { GraphEngine, type GraphEdgeInput, type GraphEngineEvents, type GraphNodeInput, type RenderStyle } from "@chain/graph-engine";

/**
 * React ↔ 图谱引擎的桥（用户端重构规范 V4 §30.3）
 *
 * 这一层要守住的核心边界：**React 不参与逐帧渲染。**
 * 因此 hook 只做四件事：
 *   1. 在挂载时创建引擎、卸载时 `dispose()`；
 *   2. 把图数据/样式的变化**推**给引擎；
 *   3. 把引擎事件**收**成 React 状态——且只收敛"会进 DOM 的那几个"
 *      （选中、hover、相机），绝不把每帧数据塞进 state（§56.3）；
 *   4. 暴露一个稳定的 imperative 句柄给工具条按钮。
 *
 * 明确不做：不在 React 里保存节点位置数组、不用 state 驱动 transform。
 */

export interface UseGraphEngineOptions {
  nodes: GraphNodeInput[];
  edges: GraphEdgeInput[];
  style?: RenderStyle;
  maxDpr?: number;
  /** 引擎就绪回调：工具条等外部 UI 用它拿 imperative 句柄。 */
  onReady?: (engine: GraphEngine | null) => void;
  onSelectionChange?: (nodeIds: string[]) => void;
  onNodeActivate?: (nodeId: string) => void;
}

export interface UseGraphEngineResult {
  containerRef: React.RefObject<HTMLDivElement | null>;
  canvasRef: React.RefObject<HTMLCanvasElement | null>;
  /** WebGL2 是否可用（false 时调用方必须显示性能受限提示，§30.6）。 */
  webgl2: boolean;
  /** 引擎是否已挂载（首帧之前为 false）。 */
  ready: boolean;
  /** 当前选中的节点 id（有序，按点击顺序）。 */
  selection: string[];
  /** 当前 hover 的节点 id；没有则为 null。 */
  hoveredId: string | null;
  /** 相机状态快照——只在 camera:change 事件里更新，不是每帧。 */
  camera: { cx: number; cy: number; scale: number } | null;
  /** 命令式句柄；引擎未就绪时为 null（用 `ready` 判断是否可用）。 */
  engine: GraphEngine | null;
  /** 键盘导航：在选中节点的邻接之间移动（§55.2 键盘可达）。 */
  moveSelectionBy: (delta: number) => void;
}

export function useGraphEngine(options: UseGraphEngineOptions): UseGraphEngineResult {
  const { nodes, edges, style, maxDpr, onReady, onSelectionChange, onNodeActivate } = options;
  const containerRef = useRef<HTMLDivElement | null>(null);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const engineRef = useRef<GraphEngine | null>(null);

  const [webgl2, setWebgl2] = useState(true);
  const [ready, setReady] = useState(false);
  const [selection, setSelection] = useState<string[]>([]);
  const [hoveredId, setHoveredId] = useState<string | null>(null);
  const [camera, setCamera] = useState<{ cx: number; cy: number; scale: number } | null>(null);

  /* 回调放进 ref：避免父组件每次渲染换一个函数就把引擎重建一次。 */
  const callbacks = useRef({ onReady, onSelectionChange, onNodeActivate });
  callbacks.current = { onReady, onSelectionChange, onNodeActivate };

  /* 数据身份：只有在内容真的变了才重新装载，而不是每次渲染都 load。 */
  const dataKey = useMemo(
    () => `${nodes.length}:${edges.length}:${nodes.map((n) => n.id).join(",")}:${edges.map((e) => e.source + ">" + e.target).join(",")}`,
    [nodes, edges],
  );

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const engine = new GraphEngine({ nodes, edges, style, maxDpr });
    engineRef.current = engine;

    const mounted = engine.mount(canvas);
    setWebgl2(mounted);
    setReady(mounted);
    callbacks.current.onReady?.(mounted ? engine : null);

    /* 只订阅会进 DOM 的事件；逐帧的渲染数据永远不进 React state。 */
    const offSelection = engine.events.on("selection:change", ({ nodeIds }) => {
      setSelection(nodeIds);
      callbacks.current.onSelectionChange?.(nodeIds);
    });
    const offHover = engine.events.on("node:hover", ({ nodeId }) => setHoveredId(nodeId));
    const offCamera = engine.events.on("camera:change", (state) => setCamera(state));
    const offClick = engine.events.on("node:click", ({ nodeId }) => callbacks.current.onNodeActivate?.(nodeId));

    return () => {
      offSelection();
      offHover();
      offCamera();
      offClick();
      engine.dispose();
      engineRef.current = null;
      callbacks.current.onReady?.(null);
      setReady(false);
      setSelection([]);
      setHoveredId(null);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dataKey]);

  /**
   * 键盘可达（§55：canvas 不能是唯一的信息通道）。
   *
   * 方向键在**选中节点的邻接**之间移动，而不是遍历全部节点——
   * 十万节点的图上"下一个节点"没有意义，而"顺着资金流走一跳"正是调查动作本身。
   */
  const moveSelectionBy = useCallback((delta: number) => {
    const engine = engineRef.current;
    if (!engine) return;
    const currentId = selection[0];
    if (!currentId) {
      engine.setSelection([0]);
      return;
    }
    const index = engine.store.indexOfNodeId(currentId);
    if (index < 0) return;
    const neighbors: number[] = [];
    engine.store.forEachNeighbor(index, (_edge, other) => neighbors.push(other));
    neighbors.sort((a, b) => a - b);
    if (neighbors.length === 0) return;
    const position = neighbors.indexOf(engine.store.indexOfNodeId(selection[0]));
    const next = neighbors[(Math.max(0, position) + delta + neighbors.length) % neighbors.length];
    engine.setSelection([next]);
  }, [selection]);

  return {
    containerRef,
    canvasRef,
    webgl2,
    ready,
    selection,
    hoveredId,
    camera,
    engine: engineRef.current,
    moveSelectionBy,
  };
}

export type { GraphEngineEvents };
