import type { PointerEvent as ReactPointerEvent } from "react";
import { NODE_KIND_LABEL, SVG_H, SVG_W } from "./constants";
import { ellipsize } from "./display";
import type { CanvasTool, Lens, RenderEdge, RenderNode, Selection } from "./types";

/**
 * 画布本体（§61：`CanvasPage.tsx` 只保留 workspace orchestration，图形渲染逻辑全部移出）。
 *
 * 这个组件是**纯呈现**：它不取数、不持状态、不碰路由。
 * 所有派生值（坐标、选中、标签开关、配色、格式化）都由页面算好后传进来，
 * 所有用户操作都通过回调抛回页面。
 *
 * 为什么要这么切：画布是整站唯一一个"渲染技术可能整体替换"的区域
 * （R6 的 WebGL 引擎就是为它准备的）。把渲染隔离在一个只有 props 进、
 * 回调出的组件里，替换渲染器时页面侧一行都不用动。
 *
 * 本文件里的 SVG 结构是从 CanvasPage 逐字搬过来的，没有重排、没有改名，
 * 只把组件内部的两个闭包（setSelection / selectEdgeByKey）换成了 props 回调。
 */
export interface CanvasStageProps {
  /** 画布平移（viewBox 单位）。 */
  pan: { x: number; y: number };
  /** 画布缩放。 */
  zoom: number;
  renderNodes: RenderNode[];
  renderEdges: RenderEdge[];
  positions: Map<string, { x: number; y: number }>;
  selection: Selection;
  /** 框选命中的节点 key 集合（已提交的那一批）。 */
  boxKeySet: Set<string>;
  /** 当前高亮路径上的节点 key 集合。 */
  focusNodeKeys: Set<string>;
  /** 正在拖拽的框选矩形（未提交）。 */
  boxRect: { x0: number; y0: number; x1: number; y1: number } | null;
  lens: Lens;
  tool: CanvasTool;
  showNodeLabels: boolean;
  showEdgeLabels: boolean;
  /** 证据透镜下淡化非证据节点。 */
  mutedNodes: boolean;
  /** 当前聚焦对象对应的渲染 key（节点或边）。 */
  focusRenderKey: string | null;
  /** 正在播放动画的那条边；null = 没有动画。 */
  playingEdgeKey: string | null;
  edgeColor: (edge: RenderEdge) => string;
  edgeLabel: (edge: RenderEdge) => string;
  edgeFullText: (edge: RenderEdge) => string;
  /** 选中节点 / 聚合节点。 */
  onSelect: (selection: Selection) => void;
  /** 点选一条边。 */
  onSelectEdge: (edge: RenderEdge) => void;
  onCanvasPointerDown: (event: ReactPointerEvent<SVGSVGElement>) => void;
  onCanvasPointerMove: (event: ReactPointerEvent<SVGSVGElement>) => void;
  onCanvasPointerUp: () => void;
}

export function CanvasStage({
  pan,
  zoom,
  renderNodes,
  renderEdges,
  positions,
  selection,
  boxKeySet,
  focusNodeKeys,
  boxRect,
  lens,
  tool,
  showNodeLabels,
  showEdgeLabels,
  mutedNodes,
  focusRenderKey,
  playingEdgeKey,
  edgeColor,
  edgeLabel,
  edgeFullText,
  onSelect,
  onSelectEdge,
  onCanvasPointerDown,
  onCanvasPointerMove,
  onCanvasPointerUp,
}: CanvasStageProps) {
  return (
                <div style={{ padding: "0 var(--sp-2) var(--sp-2)" }}>
                  <svg
                    className="s5-canvas-svg"
                    viewBox={`0 0 ${SVG_W} ${SVG_H}`}
                    preserveAspectRatio="xMidYMid meet"
                    role="img"
                    aria-label="Canvas SVG 图谱"
                    onPointerDown={onCanvasPointerDown}
                    onPointerMove={onCanvasPointerMove}
                    onPointerUp={onCanvasPointerUp}
                    onPointerCancel={onCanvasPointerUp}
                  >
                    <defs>
                      <marker id="canvas-arrow" viewBox="0 0 10 10" refX="18" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
                        <path d="M 0 0 L 10 5 L 0 10 z" fill="#667085" />
                      </marker>
                    </defs>
                    <rect x="0" y="0" width={SVG_W} height={SVG_H} fill="transparent" />
                    <g transform={`translate(${pan.x} ${pan.y}) scale(${zoom})`}>
                      {renderEdges.map((edge) => {
                        const from = positions.get(edge.fromKey);
                        const to = positions.get(edge.toKey);
                        if (!from || !to) return null;
                        const label = edgeLabel(edge);
                        const focused = edge.key === focusRenderKey;
                        const animate = edge.key === playingEdgeKey;
                        const labelVisible = showEdgeLabels || focused;
                        return (
                          <g key={edge.key}>
                            <title>{edgeFullText(edge)}</title>
                            <line
                              className={focused ? "s5-canvas-edge s5-canvas-edge--focus" : "s5-canvas-edge"}
                              x1={from.x}
                              y1={from.y}
                              x2={to.x}
                              y2={to.y}
                              stroke={edgeColor(edge)}
                              strokeWidth={edge.repeated > 1 ? Math.min(5, 1.5 + edge.repeated / 4) : 1.6}
                              strokeOpacity={0.82}
                              strokeDasharray={lens === "evidence" ? "7 5" : undefined}
                              markerEnd="url(#canvas-arrow)"
                            />
                            {animate ? (
                              <line
                                className="s5-canvas-flow-dash"
                                x1={from.x}
                                y1={from.y}
                                x2={to.x}
                                y2={to.y}
                                stroke="var(--c-cobalt, #3b4fe4)"
                                strokeWidth={2.6}
                                markerEnd="url(#canvas-arrow)"
                              />
                            ) : null}
                            {/* 14px 透明点击区：边可被点选，完整值在 <title> 与 Inspector（蓝图 §12.13）。 */}
                            <line
                              x1={from.x}
                              y1={from.y}
                              x2={to.x}
                              y2={to.y}
                              stroke="transparent"
                              strokeWidth={14}
                              pointerEvents="stroke"
                              style={{ cursor: "pointer" }}
                              onClick={(event) => {
                                event.stopPropagation();
                                onSelectEdge(edge);
                              }}
                            />
                            {labelVisible && label ? (
                              <text
                                x={(from.x + to.x) / 2}
                                y={(from.y + to.y) / 2 - 5}
                                textAnchor="middle"
                                fontSize={10}
                                fill="var(--c-slate)"
                                style={{ pointerEvents: "none" }}
                              >
                                {ellipsize(label, 24)}
                              </text>
                            ) : null}
                          </g>
                        );
                      })}
    
                      {renderNodes.map((node) => {
                        const position = positions.get(node.key);
                        if (!position) return null;
                        const selected =
                          selection?.kind === "node"
                            ? selection.ref === node.ref
                            : selection?.kind === "aggregate" && selection.key === node.key;
                        const boxed = boxKeySet.has(node.key);
                        const onPath = focusNodeKeys.has(node.key);
                        const labelVisible = showNodeLabels || selected || boxed || onPath;
                        const fill = node.aggregate
                          ? "var(--c-surface-sunken)"
                          : node.kind === "address"
                            ? "var(--c-cobalt-soft)"
                            : node.kind === "evidence"
                              ? "var(--c-amber-soft)"
                              : "var(--c-neutral-soft)";
                        const stroke = node.aggregate
                          ? "var(--c-neutral)"
                          : node.kind === "address"
                            ? "var(--c-cobalt)"
                            : node.kind === "evidence"
                              ? "var(--c-amber-text)"
                              : "var(--c-neutral)";
                        const radius = node.aggregate ? Math.min(34, 22 + node.count / 40) : node.kind === "address" ? 22 : 18;
                        const isRect = node.kind === "tx" && !node.aggregate;
                        const topOffset = isRect ? 16 : radius;
                        return (
                          <g
                            key={node.key}
                            className={`s5-canvas-node${selected ? " is-selected" : ""}`}
                            transform={`translate(${position.x} ${position.y})`}
                            role="button"
                            tabIndex={0}
                            aria-label={`${NODE_KIND_LABEL[node.kind]}节点 ${node.label}${node.aggregate ? `（聚类 ${node.count} 个）` : ""}`}
                            data-node-ref={node.ref ?? node.key}
                            onPointerDown={(event) => {
                              if (tool === "select") event.stopPropagation();
                            }}
                            onClick={(event) => {
                              event.stopPropagation();
                              if (tool === "pan") return;
                              if (node.ref) onSelect({ kind: "node", ref: node.ref });
                              else onSelect({ kind: "aggregate", key: node.key });
                            }}
                            onKeyDown={(event) => {
                              if (event.key !== "Enter" && event.key !== " ") return;
                              event.preventDefault();
                              if (node.ref) onSelect({ kind: "node", ref: node.ref });
                              else onSelect({ kind: "aggregate", key: node.key });
                            }}
                          >
                            <title>
                              {(node.ref ?? node.key) +
                                (node.aggregate
                                  ? ` · 聚类 ${node.count} 个节点`
                                  : ` · ${NODE_KIND_LABEL[node.kind]}${node.chain ? ` · ${node.chain}` : ""}`)}
                            </title>
                            <g
                              className={node.entering ? "s5-canvas-node--enter" : undefined}
                              style={mutedNodes && node.kind !== "evidence" ? { opacity: 0.45 } : undefined}
                            >
                              {node.kind === "evidence" && !node.aggregate ? (
                                <polygon points="0,-20 20,0 0,20 -20,0" fill={fill} stroke={stroke} strokeWidth={selected ? 3 : 1.5} />
                              ) : isRect ? (
                                <rect x="-26" y="-16" width="52" height="32" rx="7" fill={fill} stroke={stroke} strokeWidth={selected ? 3 : 1.5} />
                              ) : (
                                <circle r={radius} fill={fill} stroke={stroke} strokeWidth={selected ? 3 : 1.5} />
                              )}
                              {/* 类型图标：形状之外的第二通道（蓝图 §12.13"颜色只是辅助"） */}
                              {!node.aggregate && node.kind === "address" ? (
                                <circle cx={0} cy={-8} r={3.2} fill="none" stroke={stroke} strokeWidth={1.5} aria-hidden="true" />
                              ) : null}
                              {!node.aggregate && node.kind === "tx" ? (
                                <path
                                  d="M-8 -8 h12 M2 -11 l3 3 l-3 3"
                                  fill="none"
                                  stroke={stroke}
                                  strokeWidth={1.5}
                                  strokeLinecap="round"
                                  strokeLinejoin="round"
                                  aria-hidden="true"
                                />
                              ) : null}
                              {!node.aggregate && node.kind === "evidence" ? (
                                <path d="M0 -11 l3.4 3.4 l-3.4 3.4 l-3.4 -3.4 z" fill="none" stroke={stroke} strokeWidth={1.5} aria-hidden="true" />
                              ) : null}
                              {labelVisible ? (
                                <text textAnchor="middle" y={6} fontSize={10} fill="var(--c-graphite)">
                                  {node.label}
                                </text>
                              ) : null}
                              {node.sublabel && labelVisible ? (
                                <text textAnchor="middle" y={isRect ? 30 : 36} fontSize={9} fill="var(--c-slate)">
                                  {ellipsize(node.sublabel, 30)}
                                </text>
                              ) : null}
                              {node.aggregate ? (
                                <text textAnchor="middle" y={radius + 14} fontSize={10} fill="var(--c-cobalt)" fontWeight={600}>
                                  ×{node.count}
                                </text>
                              ) : null}
                              {labelVisible && !node.aggregate ? (
                                <text textAnchor="middle" y={-(topOffset + 16)} fontSize={9} fill={stroke}>
                                  {NODE_KIND_LABEL[node.kind]}
                                </text>
                              ) : null}
                            </g>
                            {selected ? <circle className="s5-canvas-halo" r={radius + 7} /> : null}
                            {!selected && boxed ? <circle className="s5-canvas-halo" data-tone="box" r={radius + 7} /> : null}
                            {!selected && !boxed && onPath ? (
                              <circle className="s5-canvas-halo" data-tone="path" r={radius + 7} />
                            ) : null}
                          </g>
                        );
                      })}
    
                      {boxRect ? (
                        <rect
                          x={Math.min(boxRect.x0, boxRect.x1)}
                          y={Math.min(boxRect.y0, boxRect.y1)}
                          width={Math.abs(boxRect.x1 - boxRect.x0)}
                          height={Math.abs(boxRect.y1 - boxRect.y0)}
                          fill="rgba(59,79,228,.08)"
                          stroke="var(--c-cobalt, #3b4fe4)"
                          strokeWidth={1.5}
                          strokeDasharray="5 4"
                          pointerEvents="none"
                        />
                      ) : null}
                    </g>
                  </svg>
                </div>
  );
}
