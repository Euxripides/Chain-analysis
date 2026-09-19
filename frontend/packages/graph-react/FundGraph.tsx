import { useMemo, useState } from "react";
import type { GraphEdgeInput, GraphNodeInput, GraphEngine } from "@chain/graph-engine";
import { useGraphEngine } from "./useGraphEngine";

/**
 * 图谱画布（用户端重构规范 V4 §30.1 / §30.6 / §55.2）
 *
 * **React 只渲染容器、工具条与可访问对象列表；节点和边由引擎绘制。**
 * 这个组件刻意不接收"渲染节点"的 children——那正是要拆掉的东西。
 *
 * 无障碍（§55.2）：Canvas/WebGL 本身不能成为唯一信息通道。因此这里同时维护一个
 * **与当前选中同步的 DOM 列表**（选中节点 + 它的直接邻接），
 * 屏幕阅读器读的是它，键盘用户用方向键在邻接之间移动。
 * 列表默认视觉隐藏，聚焦时出现——这样它既不干扰看图，也不是"给读屏看的摆设"。
 *
 * 降级（§30.6）：WebGL2 不可用时显示性能受限提示，而不是白屏。
 * 提示内容按仓库规范走弹窗（由调用方用 useDialog 打开），组件本身只给入口。
 */

export interface FundGraphProps {
  nodes: GraphNodeInput[];
  edges: GraphEdgeInput[];
  /** 画布高度；工作台里通常是"占满剩余高度"，由外部容器决定。 */
  height?: number | string;
  ariaLabel?: string;
  /** 引擎就绪回调：工具条用它拿 imperative 句柄（fitAll / focusNode / exportView…）。 */
  onReady?: (engine: GraphEngine | null) => void;
  onSelectionChange?: (nodeIds: string[]) => void;
  /** 双击节点或按 Enter 时触发，通常用于打开右侧检查器。 */
  onNodeActivate?: (nodeId: string) => void;
  /** 点击"性能受限模式"时打开说明弹窗（提示只能走弹窗）。 */
  onExplainFallback?: () => void;
  className?: string;
}

/** 可访问列表最多暴露多少个邻接——十万节点的图不能整体倒进 DOM。 */
const A11Y_NEIGHBOR_LIMIT = 20;

export function FundGraph({
  nodes,
  edges,
  height = 520,
  ariaLabel = "资金图谱",
  onReady,
  onSelectionChange,
  onNodeActivate,
  onExplainFallback,
  className,
}: FundGraphProps) {
  const graph = useGraphEngine({ nodes, edges, onReady, onSelectionChange, onNodeActivate });
  const [a11yFocused, setA11yFocused] = useState(false);

  /*
   * 邻接摘要：只对**当前选中**的受控窗口做展开（§55.2 明确要求不要暴露数万节点）。
   * 摘要内容全部来自 store 的真实数据，不生成任何"看起来合理"的占位。
   */
  const neighborRows = useMemo(() => {
    const engine = graph.engine;
    if (!engine || graph.selection.length === 0) return [];
    const currentId = graph.selection[0];
    const index = engine.store.indexOfNodeId(currentId);
    if (index < 0) return [];
    const rows: Array<{ id: string; label: string; direction: string }> = [];
    engine.store.forEachNeighbor(index, (edgeIndex, other) => {
      if (rows.length >= A11Y_NEIGHBOR_LIMIT) return;
      const outgoing = engine.store.edgeSource[edgeIndex] === index;
      rows.push({
        id: engine.store.idAt(other),
        label: engine.store.labelAt(other) || engine.store.idAt(other),
        direction: outgoing ? "流出" : "流入",
      });
    });
    return rows;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [graph.engine, graph.selection, graph.ready]);

  const selectedId = graph.selection[0] ?? null;

  return (
    <div className={`cs-fundgraph${className ? " " + className : ""}`} style={{ height }}>
      <div className="cs-fundgraph__stage" ref={graph.containerRef}>
        {/*
          canvas 对读屏隐藏：它的信息由下面的可访问列表承载（§55.2）。
          这样做同时避免了"读屏软件把 canvas 当成一个空元素"的常见问题。
        */}
        <canvas
          ref={graph.canvasRef}
          className="cs-fundgraph__canvas"
          aria-hidden="true"
          tabIndex={-1}
          role="presentation"
        />

        {!graph.webgl2 ? (
          <div className="cs-fundgraph__fallback" role="status">
            <p className="cs-fundgraph__fallback-title">当前浏览器无法启用 GPU 图谱</p>
            <p className="cs-fundgraph__fallback-body">
              已切换到性能受限模式：节点仍可通过下方的对象列表浏览与选中。
            </p>
            {onExplainFallback ? (
              <button type="button" className="btn btn--sm" onClick={onExplainFallback}>
                查看原因
              </button>
            ) : null}
          </div>
        ) : null}
      </div>

      {/*
        可访问对象列表（§55.2）：
          - 与画布中的选中项双向同步（选中节点在上，其邻接在下）；
          - 键盘可在列表项之间移动，并可通过方向键在邻接间跳；
          - 默认视觉隐藏，聚焦后出现。
      */}
      <div
        className={`cs-fundgraph__a11y${a11yFocused ? " is-focused" : ""}`}
        role="group"
        aria-label={ariaLabel + "对象列表"}
        onFocus={() => setA11yFocused(true)}
        onBlur={() => setA11yFocused(false)}
        tabIndex={0}
        onKeyDown={(event) => {
          if (event.key === "ArrowDown") {
            event.preventDefault();
            graph.moveSelectionBy(1);
          } else if (event.key === "ArrowUp") {
            event.preventDefault();
            graph.moveSelectionBy(-1);
          } else if (event.key === "Escape") {
            graph.engine?.clearSelection();
          }
        }}
      >
        <p className="cs-fundgraph__a11y-hint">
          {"共 " + nodes.length + " 个节点 · " + edges.length + " 条边 · ↑↓ 在相邻节点间移动 · Esc 取消选择"}
        </p>
        {selectedId ? (
          <>
            <p className="cs-fundgraph__a11y-current">
              {"当前选中：" + (graph.engine?.store.labelAt(graph.engine.store.indexOfNodeId(selectedId)) || selectedId)}
            </p>
            <ul className="cs-fundgraph__a11y-list">
              {neighborRows.map((row) => (
                <li key={row.id}>
                  <button
                    type="button"
                    className="cs-fundgraph__a11y-item"
                    onClick={() => graph.engine?.focusNode(row.id)}
                  >
                    <span className="cs-fundgraph__a11y-dir">{row.direction}</span>
                    <span className="mono">{row.label}</span>
                  </button>
                </li>
              ))}
              {neighborRows.length === 0 ? <li>该节点没有相邻对象</li> : null}
            </ul>
          </>
        ) : (
          <p className="cs-fundgraph__a11y-current">尚未选中对象</p>
        )}
      </div>
    </div>
  );
}
