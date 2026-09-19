import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { PointerEvent as ReactPointerEvent } from "react";
import { GraphEngine, type GraphEdgeInput, type GraphNodeInput } from "@chain/graph-engine";
import { NODE_KIND_LABEL } from "./constants";
import { cameraForViewport, worldRectToScreen } from "./geometry";
import type { CanvasTool, Lens, RenderEdge, RenderNode, Selection } from "./types";

/**
 * 画布本体（§61 / §65「Canvas 迁移到独立性能引擎」）。
 *
 * 这个组件是**纯呈现 + 一层交互转发**：不取数、不持业务状态、不碰路由。
 * 节点与边由 `@chain/graph-engine` 在 WebGL 上绘制，React 只渲染
 * 容器、交互层与可访问对象列表（§30.1：React 不渲染图谱节点）。
 *
 * 三个刻意的设计：
 *
 * 1. **引擎的指针监听是关掉的**（`interactive: false`）。
 *    页面有自己的一套工具语义（选择 / 框选 / 平移），引擎默认的
 *    "拖拽=平移、滚轮=缩放"只是**另一种**约定。迁移渲染器不应该顺手改掉
 *    用户的操作习惯，所以交互由盖在 canvas 上的透明层维持原样，
 *    命中测试改为调用 `engine.pickAny()`——它同时能认出节点与边。
 *
 * 2. **页面持有的 pan/zoom 仍是真值**，引擎相机由它换算而来
 *    （`cameraForViewport`）。反过来让引擎相机当真的话，
 *    框选、缩放按钮、深链接恢复三处都要跟着改。
 *
 * 3. **无障碍不能退化**（§55.2）：canvas 对读屏隐藏，真实信息由下方的
 *    对象列表承载；列表与画布选中双向同步。
 */

const NODE_RADIUS = (node: RenderNode): number =>
  node.aggregate ? Math.min(34, 22 + node.count / 40) : node.kind === "address" ? 22 : 18;

/** 可访问列表最多暴露多少个对象——大图不能整体倒进 DOM（§55.2）。 */
const A11Y_NEIGHBOR_LIMIT = 20;

/** 证据透镜下非证据节点的淡化值。与迁移前的内联 opacity 一致，避免换渲染器顺带换观感。 */
const EVIDENCE_MUTE_ALPHA = 0.45;

export interface CanvasStageProps {
  pan: { x: number; y: number };
  zoom: number;
  renderNodes: RenderNode[];
  renderEdges: RenderEdge[];
  positions: Map<string, { x: number; y: number }>;
  selection: Selection;
  boxKeySet: Set<string>;
  focusNodeKeys: Set<string>;
  boxRect: { x0: number; y0: number; x1: number; y1: number } | null;
  lens: Lens;
  tool: CanvasTool;
  showNodeLabels: boolean;
  showEdgeLabels: boolean;
  mutedNodes: boolean;
  focusRenderKey: string | null;
  playingEdgeKey: string | null;
  /** 当前路径上的边 key（按顺序）——引擎用它点亮整条链路。 */
  pathEdgeKeys: string[];
  edgeColor: (edge: RenderEdge) => string;
  edgeLabel: (edge: RenderEdge) => string;
  edgeFullText: (edge: RenderEdge) => string;
  onSelect: (selection: Selection) => void;
  onSelectEdge: (edge: RenderEdge) => void;
  onCanvasPointerDown: (event: ReactPointerEvent<HTMLElement>) => void;
  onCanvasPointerMove: (event: ReactPointerEvent<HTMLElement>) => void;
  onCanvasPointerUp: () => void;
  /** WebGL2 不可用时的说明入口（提示只能走弹窗，组件本身不弹）。 */
  onExplainFallback?: () => void;
}

/** 交互层坐标 → 引擎拾取坐标的换算在组件内完成，页面不需要知道引擎的存在。 */

export function CanvasStage({
  pan,
  zoom,
  renderNodes,
  renderEdges,
  positions,
  selection,
  focusNodeKeys,
  boxRect,
  lens,
  tool,
  showNodeLabels,
  mutedNodes,
  pathEdgeKeys,
  onSelect,
  onSelectEdge,
  onCanvasPointerDown,
  onCanvasPointerMove,
  onCanvasPointerUp,
  onExplainFallback,
}: CanvasStageProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const engineRef = useRef<GraphEngine | null>(null);
  const [ready, setReady] = useState(0);
  const [webgl2, setWebgl2] = useState(true);
  const [size, setSize] = useState({ width: 0, height: 0 });

  /* 引擎的节点 id 用渲染 key：它对一次渲染唯一，且不依赖 ref（聚合节点没有 ref）。 */
  const graphNodes = useMemo<GraphNodeInput[]>(
    () =>
      renderNodes.map((node) => {
        const position = positions.get(node.key);
        return {
          id: node.key,
          kind: node.kind === "tx" ? "transaction" : node.kind,
          label: node.label,
          sublabel: node.sublabel,
          x: position?.x ?? 0,
          y: position?.y ?? 0,
          size: NODE_RADIUS(node),
        };
      }),
    [renderNodes, positions],
  );

  const graphEdges = useMemo<GraphEdgeInput[]>(
    () => renderEdges.map((edge) => ({ id: edge.key, source: edge.fromKey, target: edge.toKey })),
    [renderEdges],
  );

  const edgeByKey = useMemo(() => new Map(renderEdges.map((edge) => [edge.key, edge])), [renderEdges]);
  const nodeByKey = useMemo(() => new Map(renderNodes.map((node) => [node.key, node])), [renderNodes]);

  /* ---------- 引擎生命周期（§63C：路由卸载必须 dispose） ---------- */
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return undefined;
    const engine = new GraphEngine({
      nodes: graphNodes,
      edges: graphEdges,
      /* 交互由页面的透明层负责，引擎只渲染。 */
      interactive: false,
      showLabels: showNodeLabels,
      lens,
    });
    const mounted = engine.mount(canvas);
    engineRef.current = engine;
    setWebgl2(mounted);
    setReady((value) => value + 1);
    return () => {
      engine.dispose();
      engineRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  /* ---------- 容器尺寸（相机换算与命中测试都要用） ---------- */
  useEffect(() => {
    const container = containerRef.current;
    if (!container) return undefined;
    const measure = () => setSize({ width: container.clientWidth, height: container.clientHeight });
    measure();
    if (typeof ResizeObserver === "undefined") return undefined;
    const observer = new ResizeObserver(measure);
    observer.observe(container);
    return () => observer.disconnect();
  }, [ready]);

  /* ---------- 数据（位置由页面算好，引擎不跑布局） ---------- */
  useEffect(() => {
    const engine = engineRef.current;
    if (!engine) return;
    engine.loadGraph(graphNodes, graphEdges);
  }, [graphNodes, graphEdges, ready]);

  /*
   * ---------- 相机：页面 pan/zoom 是唯一真值 ----------
   *
   * 依赖里带上 graphNodes：装载新数据之后必须**重新下发**一次相机。
   * 即便引擎侧已经不擅自 fit（见 loadGraph），这一步也不能省——
   * 它保证"数据变了的下一帧"画面与页面坐标系仍然一致。
   */
  useEffect(() => {
    const engine = engineRef.current;
    if (!engine || size.width === 0 || size.height === 0) return;
    /*
     * LOD 阈值必须按**本页坐标系的实际比例**校准。
     *
     * 本页的世界是 viewBox 单位（1000×600），缩进 468px 的容器后
     * "世界单位 → 像素"只有约 0.47。引擎默认阈值是按它自己那个量级定的，
     * 直接套用会把正常视图判成远景，标签被整片压掉——
     * 画面本身完全正常，只有标签不见了，是那种最难归因的缺陷。
     */
    const k = Math.min(size.width / 1000, size.height / 600) || 1;
    engine.setLodThresholds({ near: 3 * k, mid: 0.8 * k });
    engine.setCamera(cameraForViewport(pan, zoom, size.width, size.height));
  }, [pan, zoom, size, ready, graphNodes]);

  /* ---------- 选中 ---------- */
  useEffect(() => {
    const engine = engineRef.current;
    if (!engine) return;
    /* 选中一条边时画布不高亮任何节点——边的选中由 Inspector 承载。 */
    if (!selection || selection.kind === "edge") {
      engine.clearSelection();
      return;
    }
    const node =
      selection.kind === "node"
        ? renderNodes.find((item) => item.ref === selection.ref)
        : nodeByKey.get(selection.key);
    const index = node ? graphNodes.findIndex((item) => item.id === node.key) : -1;
    if (index >= 0) engine.setSelection([index]);
    else engine.clearSelection();
  }, [selection, renderNodes, nodeByKey, graphNodes, ready]);

  /* ---------- 视图维度与标签 ---------- */
  useEffect(() => {
    engineRef.current?.setLens(lens);
  }, [lens, ready]);

  /*
   * 证据透镜下弱化非证据节点（迁移前用内联 opacity 0.45 实现）。
   *
   * 这不是装饰：证据透镜的全部意义是"只看证据及其一跳邻居"，
   * 不把其余节点压下去，被保留下来的邻居就和证据本身一样显眼了。
   */
  useEffect(() => {
    const engine = engineRef.current;
    if (!engine) return;
    if (!mutedNodes) {
      engine.clearMutedNodes();
      return;
    }
    engine.setMutedNodes(
      renderNodes.filter((node) => node.kind !== "evidence").map((node) => node.key),
      EVIDENCE_MUTE_ALPHA,
    );
  }, [mutedNodes, renderNodes, ready]);
  useEffect(() => {
    engineRef.current?.setLabelVisibility(showNodeLabels);
  }, [showNodeLabels, ready]);

  /* ---------- 路径高亮（§30.12）：整条链路一次性点亮 ---------- */
  useEffect(() => {
    const engine = engineRef.current;
    if (!engine) return;
    const edgeIndices: number[] = [];
    const nodeIndices = new Set<number>();
    for (const key of pathEdgeKeys) {
      const edge = edgeByKey.get(key);
      if (!edge) continue;
      const index = graphEdges.findIndex((item) => item.id === key);
      if (index < 0) continue;
      edgeIndices.push(index);
      const s = graphNodes.findIndex((item) => item.id === edge.fromKey);
      const t = graphNodes.findIndex((item) => item.id === edge.toKey);
      if (s >= 0) nodeIndices.add(s);
      if (t >= 0) nodeIndices.add(t);
    }
    if (edgeIndices.length === 0) {
      engine.highlightPath(null);
      return;
    }
    engine.highlightPath({
      nodes: Uint32Array.from(nodeIndices),
      edges: Uint32Array.from(edgeIndices),
      cost: edgeIndices.length,
    });
  }, [pathEdgeKeys, edgeByKey, graphEdges, graphNodes, ready]);

  /*
   * 把引擎的**真实状态**暴露到 DOM 上（验收用，不参与业务）。
   *
   * 为什么不能只断言"canvas 元素存在"：一个尺寸正常、但引擎没拿到数据、
   * 或者一帧都没画的 canvas，元素层面完全看不出区别。这里的数字来自
   * `engine.stats()`——已装载的节点/边、实际绘制的边数、LOD 档位、WebGL2 是否可用，
   * 都是引擎自己的账，不是页面算的。
   */
  const [stats, setStats] = useState<Record<string, unknown> | null>(null);
  useEffect(() => {
    const engine = engineRef.current;
    if (!engine) return;
    setStats({ ...engine.stats() });
  }, [graphNodes, graphEdges, pan, zoom, size, lens, ready]);

  /* ---------- 命中测试：节点与边共用一次拾取（§30.7） ---------- */
  const handleClick = useCallback(
    (event: { clientX: number; clientY: number }) => {
      /* 平移工具下拖拽不改变选中：与迁移前的行为一致。 */
      if (tool === "pan") return;
      const engine = engineRef.current;
      const container = containerRef.current;
      if (!engine || !container) return;
      const rect = container.getBoundingClientRect();
      const hit = engine.pickAny(event.clientX - rect.left, event.clientY - rect.top);
      if (!hit) return;
      if (hit.kind === "edge") {
        const edge = edgeByKey.get(engine.store.edgeIdAt(hit.index));
        if (edge) onSelectEdge(edge);
        return;
      }
      const node = nodeByKey.get(engine.store.idAt(hit.index));
      if (!node) return;
      if (node.ref) onSelect({ kind: "node", ref: node.ref });
      else onSelect({ kind: "aggregate", key: node.key });
    },
    [edgeByKey, nodeByKey, onSelect, onSelectEdge, tool],
  );

  return (
    <>
    <div
      className="cs-canvas-stage"
      ref={containerRef}
      data-canvas-stats={stats ? JSON.stringify(stats) : undefined}
    >
      <canvas ref={canvasRef} className="cs-canvas-stage__canvas" aria-hidden="true" tabIndex={-1} role="presentation" />

      {/*
        交互层：盖在 canvas 上，维持页面原有的工具语义。
        touch-action 由样式给出，否则移动端的拖拽会被浏览器当成滚动。
      */}
      <div
        className="cs-canvas-stage__surface"
        role="img"
        aria-label="资金图谱画布"
        onPointerDown={onCanvasPointerDown}
        onPointerMove={onCanvasPointerMove}
        onPointerUp={onCanvasPointerUp}
        onPointerCancel={onCanvasPointerUp}
        onClick={handleClick}
      />

      {/* 框选矩形：迁移到 canvas 之后改由一层 div 画出来（同一套换算）。 */}
      {boxRect && size.width > 0 ? (
        <div className="cs-canvas-stage__box" style={worldRectToScreen(boxRect, pan, zoom, size.width, size.height)} />
      ) : null}

      {!webgl2 ? (
        <div className="cs-fundgraph__fallback" role="status">
          <p className="cs-fundgraph__fallback-title">当前浏览器无法启用 GPU 图谱</p>
          <p className="cs-fundgraph__fallback-body">已切换到性能受限模式：对象仍可通过下方的对象列表浏览与选中。</p>
          {onExplainFallback ? (
            <button type="button" className="btn btn--sm" onClick={onExplainFallback}>
              查看原因
            </button>
          ) : null}
        </div>
      ) : null}

    </div>

    {/*
      可访问对象列表（§55.2）：canvas 对它隐藏，读屏读的是这里。
      与画布选中双向同步；默认视觉隐藏，聚焦后出现。
      放在舞台**外面**：舞台是固定高度 + overflow:hidden 的，
      把面板塞进去会被裁掉。
    */}
    <div className="cs-fundgraph__a11y" role="group" aria-label="资金图谱对象列表" tabIndex={0}>
        <p className="cs-fundgraph__a11y-hint">
          {"共 " + renderNodes.length + " 个节点 · " + renderEdges.length + " 条边"}
        </p>
        <p className="cs-fundgraph__a11y-current">
          {"当前选中：" + describeSelection(selection, renderNodes, nodeByKey)}
        </p>
        <ul className="cs-fundgraph__a11y-list">
          {renderNodes
            .filter((node) => focusNodeKeys.has(node.key))
            .slice(0, A11Y_NEIGHBOR_LIMIT)
            .map((node) => (
              <li key={node.key}>
                <button
                  type="button"
                  className="cs-fundgraph__a11y-item"
                  onClick={() =>
                    node.ref ? onSelect({ kind: "node", ref: node.ref }) : onSelect({ kind: "aggregate", key: node.key })
                  }
                >
                  <span className="mono">{node.label}</span>
                </button>
              </li>
            ))}
        </ul>
    </div>
    </>
  );
}

/** 选中项的可读描述（读屏读的就是这句话）。 */
function describeSelection(
  selection: Selection,
  renderNodes: RenderNode[],
  nodeByKey: Map<string, RenderNode>,
): string {
  if (!selection) return "尚未选中对象";
  if (selection.kind === "edge") return selection.ref;
  const node =
    selection.kind === "node"
      ? renderNodes.find((item) => item.ref === selection.ref)
      : nodeByKey.get(selection.key);
  if (!node) return selection.kind === "node" ? selection.ref : selection.key;
  return NODE_KIND_LABEL[node.kind] + " " + node.label;
}
