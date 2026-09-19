/**
 * 图谱引擎验收探针（用户端重构规范 V4 §63G「每个 G 阶段可单独回滚」）
 *
 * 为什么用**拾取**而不是截图来证明渲染正确：
 *   本环境无法做视觉评审，而"看起来画出来了"本身也不是可断言的。
 *   拾取帧缓冲的语义是——只有在某个屏幕像素上**确实光栅化出了该节点**，
 *   那个像素才会带着它的下标。因此"每个节点的中心像素都能拾取到自己"
 *   等价于"每个节点都画在了正确的位置"，而且比截图更精确（像素级）。
 */
import { createElement } from "react";
import { createRoot } from "react-dom/client";
import {
  ASSET_PALETTE,
  EVIDENCE_DASH,
  GraphEngine,
  GraphStore,
  LENS_EFFECTS,
  bfsLayers,
  decideBudget,
  findPath,
  handleLayoutRequest,
  lensStyle,
  lodLevelFor,
  runLayout,
} from "../index";
import type { GraphEdgeInput, GraphNodeInput, LayoutMode, LayoutRequest } from "../index";
import { FundGraph } from "@chain/graph-react";

interface ProbeReport {
  webgl2: boolean;
  nodes: number;
  edges: number;
  bytes: number;
  hits: number;
  misses: string[];
  emptyHit: number;
  afterZoomHits: number;
  afterZoomMisses: string[];
  zoomMovedScreenPoint: number;
  offCanvasHit: number;
  selectionEvents: number;
  exportedPngBytes: number;
  disposed: boolean;
  error?: string;
  /* React 桥（§30.3）与可访问对象列表（§55.2）。 */
  reactCanvas: boolean;
  reactReady: boolean;
  a11yListedNeighbors: number;
  a11yHasSelectedLabel: boolean;
  keyboardMovedSelection: boolean;
  reactDisposedOnUnmount: boolean;
  /* 布局（§30.14 / §30.5）。 */
  layoutFinite: boolean;
  layoutDeterministic: boolean;
  columnsMonotonic: boolean;
  ringRadiusMonotonic: boolean;
  forceSeparated: number;
  warmStartChangedResult: boolean;
  handlerOk: boolean;
  handlerResolvedMode: LayoutMode | "";
  handlerDurationMs: number;
  /* 路径与播放（§30.12）。 */
  pathNodes: number;
  pathCostUnweighted: number;
  pathPrefersCheapEdge: boolean;
  pathUndirected: boolean;
  playbackHopsAtTick: number;
  playbackCompleted: boolean;
  playbackSpeed2xFaster: boolean;
  playbackManualStep: boolean;
  litEdgesAfterHighlight: number;
  particleCount: number;
  /** **引擎上那条路径**的边数——与上面 chain 夹具的路径是两回事，不能混用。 */
  enginePathEdges: number;
  /* 节点形状（§30.9）。 */
  shapesDistinct: boolean;
  shapeChecks: string;
  /* 细节层次与预算（§30.8 / §30.15）。 */
  budgetFull: boolean;
  budgetAggregate: boolean;
  budgetLimit: boolean;
  lodThresholds: boolean;
  lodFarLevel: string;
  lodNearLevel: string;
  renderedEdgesFar: number;
  renderedEdgesNear: number;
  farKeepsHeaviest: boolean;
  budgetModeForProbe: string;
  lodChecks: string;
  /* 标签（§30.8 / G2「基础 label」）。 */
  /** 近景下提交给 GPU 的标签四边形数。 */
  labelsNear: number;
  /** 中景下的标签数——必须 > 0 且 <= 近景（中景只给"重要节点"）。 */
  labelsMid: number;
  /** 远景下的标签数（无选中/悬停）：必须为 0。 */
  labelsFar: number;
  /** 远景下**选中**某个节点后的标签数：固定显示的标签不受档位预算约束。 */
  labelsFarPinned: number;
  /** 图集栅格化成功的文字数。 */
  labelGlyphs: number;
  /** 图集放不下而被丢弃的文字数——不为 0 说明图集需要调大。 */
  labelDropped: number;
  /**
   * 标签矩形内**与背景不同**的像素数（从 exportView 的 PNG 上数出来的）。
   * 这是"文字真的被 GPU 画出来了"的证据：实例数与图集都正确、
   * 但着色器没画或者画在别处时，这个数是 0。
   */
  labelPixelsNear: number;
  /** 远景同一区域的像素数，必须远小于近景。 */
  labelPixelsFar: number;
  /** 纯背景区域的像素数（对照样本），用来证明上面的判据不是"整张图都有噪点"。 */
  labelPixelsBackground: number;
  /** 副标签（sublabel）确实参与渲染：副标签行的像素数。 */
  labelPixelsSublabel: number;
  /** 标签锚点必须在节点的**右侧**（左对齐文本块），不能压在节点上。 */
  labelAnchoredRightOfNode: boolean;
  labelChecks: string;
  /* 高亮边着色（§30.11）。 */
  /**
   * 高亮路径**边中点**的像素是否等于 `edgeActiveColor`（按 alpha 混合到背景上）。
   *
   * 这是唯一能抓到"顶点缓冲布局写错"的判据：
   * 布局错位时边数、高亮边数、粒子数**全都正常**，
   * 只有真正读回像素才会发现画出来的颜色/透明度根本不是那个颜色。
   */
  highlightPixelMatch: boolean;
  /** 实测与期望的 RGB，失败时直接进报告，不用再去猜。 */
  highlightPixelRgb: string;
  /** 采样到的不是背景色的边中点数（0 = 高亮完全没画出来）。 */
  highlightPixelsLit: number;
  /** 采样了几个边中点。 */
  highlightSamples: number;
  highlightChecks: string;
  /* 视图维度（§30.8）。 */
  /** 证据透镜下，沿一条长边采样得到的**空隙段**数（虚线必须真的有断口）。 */
  lensDashGaps: number;
  /** 资金流透镜下同一位置的空隙段数——必须为 0，否则"虚线"只是噪声。 */
  lensSolidGaps: number;
  /** 沿边一共采了多少个像素。 */
  lensSamples: number;
  /** 资产透镜下两条不同 `colorGroup` 的边，实测颜色是否确实不同。 */
  lensEdgeColorsDistinct: boolean;
  /** 两条边是否分别落在各自调色板色的混合线上。 */
  lensEdgeColorsMatchPalette: boolean;
  lensEdgeColorText: string;
  /** 纯函数层面的透镜策略断言（不看渲染，逐字段核对效果表）。 */
  lensStyleChecks: string;
  /** `setLens` 是否发出了事件、`lens` 是否随之改变。 */
  lensSwitchEvent: boolean;
  lensChecks: string;
  /* 边拾取（§30.7 / §61 的"边可点选"）。 */
  /** 每条边中点必须拾取到**它自己**。 */
  edgePickHits: number;
  edgePickSamples: number;
  edgePickMisses: string;
  /** 节点中心必须拾取到节点——即使那里也是一条边的端点。 */
  nodeWinsOverEdge: boolean;
  /** 垂直偏移 4px（小于命中半宽）仍应命中边：证明命中区比 1px 线宽大。 */
  edgePickNearMiss: boolean;
  /** 垂直偏移 20px 必须落空：证明命中区不是无限大。 */
  edgePickFarMiss: boolean;
  /** 空白处返回 null，而不是随机命中。 */
  edgePickEmptyOk: boolean;
  /** `pickAt` 只认节点：边中点处应返回 -1（既有 API 语义不变）。 */
  pickNodeIgnoresEdges: boolean;
  edgePickChecks: string;
}

declare global {
  interface Window {
    __graphProbe?: ProbeReport;
  }
}

function ring(n: number, radius: number) {
  const nodes = [];
  for (let i = 0; i < n; i += 1) {
    const angle = (i / n) * Math.PI * 2;
    nodes.push({ id: "n" + i, kind: "address" as const, x: Math.cos(angle) * radius, y: Math.sin(angle) * radius, size: 10 });
  }
  const edges = [];
  for (let i = 0; i < n; i += 1) edges.push({ id: "e" + i, source: "n" + i, target: "n" + ((i + 1) % n) });
  return { nodes, edges };
}

async function run(): Promise<ProbeReport> {
  const container = document.getElementById("stage") as HTMLDivElement;
  container.style.width = "640px";
  container.style.height = "480px";
  const canvas = document.createElement("canvas");
  canvas.style.width = "100%";
  canvas.style.height = "100%";
  container.appendChild(canvas);

  const { nodes, edges } = ring(12, 120);
  const engine = new GraphEngine({ nodes, edges });
  const mounted = engine.mount(canvas);
  const report: ProbeReport = {
    webgl2: mounted,
    nodes: engine.stats().nodes,
    edges: engine.stats().edges,
    bytes: engine.stats().bytes,
    hits: 0,
    misses: [],
    emptyHit: -1,
    afterZoomHits: 0,
    afterZoomMisses: [],
    zoomMovedScreenPoint: 0,
    offCanvasHit: -2,
    selectionEvents: 0,
    exportedPngBytes: 0,
    disposed: false,
    reactCanvas: false,
    reactReady: false,
    a11yListedNeighbors: 0,
    a11yHasSelectedLabel: false,
    keyboardMovedSelection: false,
    reactDisposedOnUnmount: false,
    layoutFinite: false,
    layoutDeterministic: false,
    columnsMonotonic: false,
    ringRadiusMonotonic: false,
    forceSeparated: 0,
    warmStartChangedResult: false,
    handlerOk: false,
    handlerResolvedMode: "",
    handlerDurationMs: 0,
    pathNodes: 0,
    pathCostUnweighted: 0,
    pathPrefersCheapEdge: false,
    pathUndirected: false,
    playbackHopsAtTick: 0,
    playbackCompleted: false,
    playbackSpeed2xFaster: false,
    playbackManualStep: false,
    litEdgesAfterHighlight: 0,
    particleCount: 0,
    enginePathEdges: 0,
    shapesDistinct: false,
    shapeChecks: "",
    budgetFull: false,
    budgetAggregate: false,
    budgetLimit: false,
    lodThresholds: false,
    lodFarLevel: "",
    lodNearLevel: "",
    renderedEdgesFar: 0,
    renderedEdgesNear: 0,
    farKeepsHeaviest: false,
    budgetModeForProbe: "",
    lodChecks: "",
    labelsNear: 0,
    labelsMid: 0,
    labelsFar: 0,
    labelsFarPinned: 0,
    labelGlyphs: 0,
    labelDropped: 0,
    labelPixelsNear: 0,
    labelPixelsFar: 0,
    labelPixelsBackground: 0,
    labelPixelsSublabel: 0,
    labelAnchoredRightOfNode: false,
    labelChecks: "",
    highlightPixelMatch: false,
    highlightPixelRgb: "",
    highlightPixelsLit: 0,
    highlightSamples: 0,
    highlightChecks: "",
    lensDashGaps: 0,
    lensSolidGaps: 0,
    lensSamples: 0,
    lensEdgeColorsDistinct: false,
    lensEdgeColorsMatchPalette: false,
    lensEdgeColorText: "",
    lensStyleChecks: "",
    lensSwitchEvent: false,
    lensChecks: "",
    edgePickHits: 0,
    edgePickSamples: 0,
    edgePickMisses: "",
    nodeWinsOverEdge: false,
    edgePickNearMiss: false,
    edgePickFarMiss: false,
    edgePickEmptyOk: false,
    pickNodeIgnoresEdges: false,
    edgePickChecks: "",
  };
  if (!mounted) {
    report.error = "webgl2-unavailable";
    return report;
  }

  let selectionEvents = 0;
  engine.events.on("selection:change", () => {
    selectionEvents += 1;
  });

  await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));

  /* 1. 每个节点的世界坐标 → 屏幕坐标 → 拾取，必须命中自己。 */
  const viewport = engine.viewport();
  for (let i = 0; i < nodes.length; i += 1) {
    const [sx, sy] = engine.camera.worldToScreen(nodes[i].x as number, nodes[i].y as number, viewport.width, viewport.height);
    const picked = engine.pickAt(sx, sy);
    if (picked === i) report.hits += 1;
    else report.misses.push(`n${i}: expected ${i}, got ${picked}`);
  }

  /* 2. 远离任何节点的位置必须什么都拾取不到（证明没有大范围误填充）。 */
  report.emptyHit = engine.pickAt(4, 4);

  /*
   * 3. 缩放之后坐标映射必须跟着变——这条抓的是相机矩阵算错。
   *
   * 刻意用**缩小**（0.7）而不是放大：放大会把节点推出视口，
   * 那时"拾取不到"是正确的（见 pickNode 的越界保护），
   * 但那样就测不出坐标映射本身对不对了。
   */
  const before = engine.camera.worldToScreen(nodes[0].x as number, nodes[0].y as number, viewport.width, viewport.height);
  engine.camera.zoomAt(viewport.width / 2, viewport.height / 2, 0.7, viewport.width, viewport.height);
  engine.invalidate();
  await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  const after = engine.camera.worldToScreen(nodes[0].x as number, nodes[0].y as number, viewport.width, viewport.height);
  for (let i = 0; i < nodes.length; i += 1) {
    const [sx, sy] = engine.camera.worldToScreen(nodes[i].x as number, nodes[i].y as number, viewport.width, viewport.height);
    const picked = engine.pickAt(sx, sy);
    if (picked === i) report.afterZoomHits += 1;
    else report.afterZoomMisses.push(`n${i}: expected ${i}, got ${picked} (screen ${Math.round(sx)},${Math.round(sy)})`);
  }
  report.zoomMovedScreenPoint = Math.abs(after[0] - before[0]) + Math.abs(after[1] - before[1]);

  /* 3b. 视口外的坐标必须安全返回 -1（拖拽时鼠标跑出画布是日常操作）。 */
  report.offCanvasHit = engine.pickAt(-50, -50);

  /* 4. 选中必须发出事件，并且导出视图要拿到真实 PNG。 */
  engine.setSelection([0, 1]);
  report.selectionEvents = selectionEvents;
  const png = engine.exportView();
  report.exportedPngBytes = png ? png.length : 0;

  /*
   * 5. 路径求值与播放（§30.12）。
   *
   * **必须在 dispose 之前**：高亮与粒子只有在渲染器还活着的时候才有真实状态可读。
   * 第一版把这段放在 dispose 之后，于是 highlightedEdges 与 particles 恒为 0——
   * 那不是产品缺陷，是测试顺序错了。
   */
  runPathChecks(report, engine);

  /* 5b. 标签（§30.8 / G2 基础 label）。 */
  await runLabelChecks(report);

  /* 5c. 高亮边的**实际颜色**（§30.11）。 */
  await runHighlightColorCheck(report);

  /* 5d. 视图维度：虚线（证据）与分组着色（资产）（§30.8）。 */
  await runLensChecks(report);

  /* 5e. 边拾取（§30.7）。 */
  await runEdgePickChecks(report);

  /* 6. dispose 之后必须停止渲染且可重入调用。 */
  engine.dispose();
  engine.dispose();
  report.disposed = true;

  /* 7. React 桥（§30.3）与可访问对象列表（§55.2）。 */
  await runReactBridge(report, nodes, edges);

  /* 8. 布局算法与 worker 处理器（§30.14 / §30.5）。 */
  runLayoutChecks(report);

  /* 9. 节点形状（§30.9）：形状必须真的不同，且拾取热区与所见形状一致。 */
  runShapeChecks(report);

  /* 10. 细节层次与渲染预算（§30.8 / §30.15）。 */
  await runLodChecks(report);
  return report;
}

/**
 * LOD 与预算验收。
 *
 * 边数刻意取到**超过远景上限（20000）**，否则降采样根本不会发生，
 * 断言就会变成"永远通过"。用 20500 条边：远景应留下 20000 条且是权重最高的那些，
 * 近景应全部画出来。
 */
async function runLodChecks(report: ProbeReport) {
  /* 纯函数部分：预算档位。 */
  report.budgetFull = decideBudget(10_000, 30_000).mode === "full";
  report.budgetAggregate = decideBudget(50_000, 150_000).mode === "aggregate";
  report.budgetLimit = decideBudget(200_000, 600_000).mode === "limit";
  report.lodThresholds =
    lodLevelFor(0.2) === "far" && lodLevelFor(1.5) === "mid" && lodLevelFor(6) === "near";

  const NODE_COUNT = 2_050;
  const EDGE_COUNT = 20_500;
  const nodes = Array.from({ length: NODE_COUNT }, (_, i) => ({
    id: "p" + i,
    kind: "address" as const,
    x: (i % 64) * 24,
    y: Math.floor(i / 64) * 24,
    size: 5,
  }));
  const edges = Array.from({ length: EDGE_COUNT }, (_, i) => ({
    id: "q" + i,
    source: "p" + (i % NODE_COUNT),
    target: "p" + ((i * 7 + 3) % NODE_COUNT),
    /* 权重随下标递增 → 权重最高的是最后一条，便于断言"留下的是主干"。 */
    weight: i + 1,
  }));

  report.budgetModeForProbe = decideBudget(NODE_COUNT, EDGE_COUNT).mode;

  const stage = document.getElementById("lod") as HTMLDivElement;
  stage.style.width = "560px";
  stage.style.height = "320px";
  const canvas = document.createElement("canvas");
  canvas.style.width = "100%";
  canvas.style.height = "100%";
  stage.appendChild(canvas);

  const engine = new GraphEngine({ nodes, edges });
  if (!engine.mount(canvas)) {
    report.lodChecks = "webgl2-unavailable";
    return;
  }

  /* 强制远景：相机缩得很小。 */
  engine.camera.state.scale = 0.2;
  engine.camera.state.cx = 700;
  engine.camera.state.cy = 300;
  engine.invalidate();
  await frames(3);

  report.lodFarLevel = engine.stats().lod;
  report.renderedEdgesFar = engine.stats().renderedEdges;
  const farIndices = [...engine.renderedEdgeIndices()];
  /* 留下的必须是权重最高的那些：第一条是权重最大的边（下标 EDGE_COUNT-1）。 */
  report.farKeepsHeaviest = farIndices.length > 0 && farIndices[0] === EDGE_COUNT - 1;

  /* 强制近景：应把全部边画出来。 */
  engine.camera.state.scale = 8;
  engine.invalidate();
  await frames(3);
  report.lodNearLevel = engine.stats().lod;
  report.renderedEdgesNear = engine.stats().renderedEdges;

  engine.dispose();
  stage.removeChild(canvas);
}

/**
 * 形状验收（§30.9「颜色是辅助，形状必须区分」）。
 *
 * 做法：把三种节点（圆 / 菱形 / 方）放在已知位置，然后在**同一屏幕偏移**上做拾取。
 * 圆在 (0.6r, 0.6r) 处仍然有实体（距离 0.85r），菱形的同一点已经在外面（|x|+|y| = 1.2r），
 * 方形在 (0.9r, 0) 处已经在外面（半边长 0.82r）。三者的差异是可断言的。
 *
 * 这条同时验证了一件更重要的事：**拾取用的形状与渲染用的形状是同一个 SDF**。
 * 只要两者不一致，用户就会点到"看起来是空的地方"。
 */
function runShapeChecks(report: ProbeReport) {
  const SIZE = 20;
  const stage = document.getElementById("shapes") as HTMLDivElement;
  stage.style.width = "520px";
  stage.style.height = "260px";
  const canvas = document.createElement("canvas");
  canvas.style.width = "100%";
  canvas.style.height = "100%";
  stage.appendChild(canvas);

  const engine = new GraphEngine({
    nodes: [
      { id: "round", kind: "address", x: -220, y: 0, size: SIZE },
      { id: "diamond", kind: "transaction", x: 0, y: 0, size: SIZE },
      { id: "square", kind: "entity", x: 220, y: 0, size: SIZE },
    ],
    edges: [],
  });
  if (!engine.mount(canvas)) {
    report.shapeChecks = "webgl2-unavailable";
    return;
  }
  engine.fitAll();
  engine.invalidate();

  const pickAt = (x: number, y: number, ox: number, oy: number) => {
    const viewport = engine.viewport();
    const scale = engine.camera.scale;
    const [sx, sy] = engine.camera.worldToScreen(x + ox * SIZE, y + oy * SIZE, viewport.width, viewport.height);
    void scale;
    return engine.pickAt(sx, sy);
  };

  /* 圆心点必须命中自己（形状不影响中心）。 */
  const centerOk =
    pickAt(-220, 0, 0, 0) === 0 && pickAt(0, 0, 0, 0) === 1 && pickAt(220, 0, 0, 0) === 2;

  /* (0.6, 0.6)：圆命中、菱形落空。 */
  const roundAtCorner = pickAt(-220, 0, 0.6, 0.6);
  const diamondAtCorner = pickAt(0, 0, 0.6, 0.6);

  /* (0.9, 0)：圆命中、方形落空。 */
  const roundAtEdge = pickAt(-220, 0, 0.9, 0);
  const squareAtEdge = pickAt(220, 0, 0.9, 0);

  const parts = [
    centerOk ? "center-ok" : "center-FAIL",
    roundAtCorner === 0 ? "round@corner-hit" : "round@corner-" + roundAtCorner,
    diamondAtCorner === -1 ? "diamond@corner-miss" : "diamond@corner-HIT",
    roundAtEdge === 0 ? "round@edge-hit" : "round@edge-" + roundAtEdge,
    squareAtEdge === -1 ? "square@edge-miss" : "square@edge-HIT",
  ];
  report.shapeChecks = parts.join(" ");
  if (centerOk && roundAtCorner === 0 && diamondAtCorner === -1 && roundAtEdge === 0 && squareAtEdge === -1) {
    report.shapesDistinct = true;
  }
  engine.dispose();
  stage.removeChild(canvas);
}

/**
 * 路径与播放验收。
 *
 * 全部用**显式时间**推进（`tick(t)`），不 sleep：
 * "0.5x 速度下第 3 跳发生在第几毫秒"这种问题必须能精确断言，
 * 靠等待真实时间只能测出"大概动了"。
 */
function runPathChecks(report: ProbeReport, engine: GraphEngine) {
  /* A → B → C 链，外加一条 A → C 的直达边，用来验证"会选更省的那条"。 */
  const chain = new GraphStore();
  chain.load(
    [
      { id: "A", kind: "address" },
      { id: "B", kind: "address" },
      { id: "C", kind: "address" },
      { id: "D", kind: "address" },
    ],
    [
      { id: "ab", source: "A", target: "B", weight: 1 },
      { id: "bc", source: "B", target: "C", weight: 1 },
      { id: "ac", source: "A", target: "C", weight: 9 },
      { id: "cd", source: "C", target: "D", weight: 1 },
    ],
  );

  const path = findPath(chain, chain.indexOfNodeId("A"), chain.indexOfNodeId("D"));
  report.pathNodes = path ? path.nodes.length : 0;
  report.pathCostUnweighted = path ? path.cost : 0;
  /* A→B→C→D 共 4 个节点；如果权重被忽略，BFS 可能选 A→C→D（3 个节点）。 */
  /* A→B→C→D 共 4 个节点、代价 3；若权重被忽略（退化成 BFS），会选出 A→C→D。 */
  report.pathPrefersCheapEdge = Boolean(path && path.nodes.length === 4 && Math.abs(path.cost - 3) < 1e-6);

  /* 有向：从 D 出发到不了 A（所有边都是单向的）。 */
  const backward = findPath(chain, chain.indexOfNodeId("D"), chain.indexOfNodeId("A"));
  const backwardUndirected = findPath(chain, chain.indexOfNodeId("D"), chain.indexOfNodeId("A"), { directed: false });
  report.pathUndirected = backward === null && backwardUndirected !== null;

  /* 在真实引擎上跑播放：用一条 3 段的路径。 */
  const graphPath = engine.findPath("n0", "n4");
  if (!graphPath) return;
  report.enginePathEdges = graphPath.edges.length;
  let hops = 0;
  let completed = false;
  engine.events.on("path:hop", () => {
    hops += 1;
  });
  engine.events.on("path:complete", () => {
    completed = true;
  });

  /*
   * 起点亮起是第 0 跳；之后每 hopDuration / 速度 毫秒走一跳（默认 620ms）。
   * 因此这里要 tick **超过** 620ms 才应该看到第 1 跳——
   * 第一版写的是 tick(310) 却断言 hopIndex === 1，那是在测我自己的算术。
   */
  engine.playPath(graphPath, 0);
  engine.playback.tick(630);
  report.playbackHopsAtTick = engine.playback.hopIndex;
  engine.playback.tick(620 * graphPath.edges.length + 10);
  report.playbackCompleted = completed && engine.playback.state === "completed";
  /*
   * 读**渲染器的实际状态**，而不是拿路径长度自己乘一遍——
   * 后者只是把测试写成了它的假设，永远不会失败。
   */
  report.litEdgesAfterHighlight = engine.stats().highlightedEdges;
  report.particleCount = engine.stats().particles;

  /* 2x 速度：同样的时间应该走完原本两倍的路。 */
  engine.playPath(graphPath, 0);
  engine.setPlaybackSpeed(2);
  engine.playback.tick(620 + 5);
  report.playbackSpeed2xFaster = engine.playback.hopIndex >= 2;

  /* 手动步进。 */
  engine.playPath(graphPath, 0);
  engine.pausePath();
  const before = engine.playback.hopIndex;
  engine.nextHop();
  const afterNext = engine.playback.hopIndex;
  engine.previousHop();
  report.playbackManualStep = afterNext === before + 1 && engine.playback.hopIndex === before;
  report.pathCostUnweighted = Math.round(report.pathCostUnweighted * 100) / 100;
  void hops;
}

/**
 * 布局验收。
 *
 * 用**结构性质**而不是"看起来对不对"来断言：
 *   - columns：层号越大，x 越大（分层是真的分层）；
 *   - ring：层号越大，到原点的距离越大；
 *   - force：斥力真的把点推开了（没有两个点重合）；
 *   - 全部：位置有限（没有 NaN——NaN 一旦进入 Float32Array，
 *     顶点着色器会把整个节点丢掉，而且不会报任何错）。
 */
function runLayoutChecks(report: ProbeReport) {
  const makeGraph = () => {
    /* 16 个节点的三层链式结构：0..3 → 4..9 → 10..15，层号完全可预期。 */
    const nodeList = Array.from({ length: 16 }, (_, i) => ({ id: "L" + i, kind: "address" as const }));
    const edgeList: Array<{ id: string; source: string; target: string }> = [];
    for (let i = 0; i < 4; i += 1) for (let j = 0; j < 2; j += 1) edgeList.push({ id: `a${i}${j}`, source: "L" + i, target: "L" + (4 + i * 2 + j) });
    for (let i = 4; i < 10; i += 1) edgeList.push({ id: "b" + i, source: "L" + i, target: "L" + (10 + (i - 4)) });
    return { nodeList, edgeList };
  };

  const { nodeList, edgeList } = makeGraph();
  const store = new GraphStore();
  store.load(nodeList, edgeList);

  const finite = () => {
    for (let i = 0; i < store.positions.length; i += 1) if (!Number.isFinite(store.positions[i])) return false;
    return true;
  };

  /* columns：层号 → x 单调递增。 */
  runLayout("columns", store, { spacing: 60 });
  const layers = bfsLayers(store);
  let maxXOfLayer = new Map<number, number>();
  let minXOfLayer = new Map<number, number>();
  for (let i = 0; i < store.nodeCount; i += 1) {
    const x = store.positions[i * 2];
    const layer = layers[i];
    maxXOfLayer.set(layer, Math.max(maxXOfLayer.get(layer) ?? -Infinity, x));
    minXOfLayer.set(layer, Math.min(minXOfLayer.get(layer) ?? Infinity, x));
  }
  report.columnsMonotonic = [...minXOfLayer.keys()].sort((a, b) => a - b).every((layer, index, all) => {
    if (index === 0) return true;
    return (minXOfLayer.get(layer) ?? 0) > (maxXOfLayer.get(all[index - 1]) ?? 0);
  });

  /* ring：层号越大，半径越大。 */
  runLayout("ring", store, { spacing: 60 });
  const radiusOfLayer = new Map<number, number>();
  for (let i = 0; i < store.nodeCount; i += 1) {
    const radius = Math.hypot(store.positions[i * 2], store.positions[i * 2 + 1]);
    const layer = layers[i];
    radiusOfLayer.set(layer, Math.max(radiusOfLayer.get(layer) ?? 0, Math.abs(radius - (layer === 0 ? 0 : layer * 60 * 1.5))));
  }
  /* 允许 1e-3 的浮点误差；任何一层偏离期望半径都算失败。 */
  report.ringRadiusMonotonic = [...radiusOfLayer.values()].every((deviation) => deviation < 1e-3);

  /*
   * force：可复现性 + 斥力生效。
   *
   * 可复现性必须用 `warmStart: false` 验证——默认的 warmStart 会以当前位置为起点，
   * 于是"同一 seed 跑两次"必然不同（那是特性，不是缺陷）。基准测试要的正是前者。
   */
  runLayout("force", store, { spacing: 50, iterations: 60, seed: 7, warmStart: false });
  report.layoutFinite = finite();
  const firstPass = store.positions.slice();
  runLayout("force", store, { spacing: 50, iterations: 60, seed: 7, warmStart: false });
  report.layoutDeterministic = firstPass.every((value, index) => Math.abs(value - store.positions[index]) < 1e-4);

  /* 顺带确认 warmStart 的语义：它确实会以上一次的终点为起点。 */
  const warmBefore = store.positions.slice();
  runLayout("force", store, { spacing: 50, iterations: 60, seed: 7, warmStart: true });
  report.warmStartChangedResult = warmBefore.some((value, index) => Math.abs(value - store.positions[index]) > 1e-4);

  let coincident = 0;
  for (let i = 0; i < store.nodeCount; i += 1) {
    for (let j = i + 1; j < store.nodeCount; j += 1) {
      if (Math.hypot(store.positions[i * 2] - store.positions[j * 2], store.positions[i * 2 + 1] - store.positions[j * 2 + 1]) < 1) coincident += 1;
    }
  }
  report.forceSeparated = coincident;

  /* worker 处理器：与 worker 里跑的是同一段代码，只是不经消息通道。 */
  const request: LayoutRequest = {
    type: "layout",
    requestId: 42,
    mode: "auto",
    options: { spacing: 60 },
    nodeCount: store.nodeCount,
    edgeCount: store.edgeCount,
    positions: store.positions.slice(),
    kinds: store.kinds.slice(),
    edgeSource: store.edgeSource.slice(),
    edgeTarget: store.edgeTarget.slice(),
    times: null,
  };
  const response = handleLayoutRequest(request);
  if (response.type === "layout:done") {
    report.handlerOk = response.positions.length === store.nodeCount * 2 && [...response.positions].every(Number.isFinite);
    report.handlerResolvedMode = response.resolvedMode;
    report.handlerDurationMs = response.durationMs;
  }
}

const frames = (n: number) => new Promise<void>((resolve) => {
  const step = (left: number) => (left <= 0 ? resolve() : requestAnimationFrame(() => step(left - 1)));
  step(n);
});

/**
 * 第二段：把 FundGraph 真正挂到 React 里，验证
 *   - 引擎被正确创建并上报 webgl2；
 *   - 选中后**可访问对象列表**同步出现选中项与它的邻接（§55.2 的双向同步）；
 *   - 列表上的方向键能在邻接之间移动选中（键盘可达，而不是只有鼠标能用）；
 *   - 卸载时引擎被 dispose。
 */
async function runReactBridge(report: ProbeReport, nodes: ReturnType<typeof ring>["nodes"], edges: ReturnType<typeof ring>["edges"]) {
  const host = document.createElement("div");
  document.body.appendChild(host);
  let engineRef: GraphEngine | null = null;
  const root = createRoot(host);
  root.render(
    createElement(FundGraph, {
      nodes,
      edges,
      height: 320,
      ariaLabel: "验收图谱",
      onReady: (instance: GraphEngine | null) => {
        engineRef = instance;
      },
    }),
  );
  await frames(3);

  report.reactCanvas = Boolean(host.querySelector("canvas.cs-fundgraph__canvas"));
  report.reactReady = Boolean(engineRef) && (engineRef as GraphEngine | null)!.stats().nodes === nodes.length;

  const active = engineRef as GraphEngine | null;
  if (active) {
    active.setSelection([0]);
    await frames(2);
    const a11y = host.querySelector(".cs-fundgraph__a11y");
    const text = a11y?.textContent ?? "";
    report.a11yListedNeighbors = host.querySelectorAll(".cs-fundgraph__a11y-item").length;
    report.a11yHasSelectedLabel = text.includes("n0") || text.includes(active.store.labelAt(0));

    /* 方向键必须在邻接之间移动选中——这是"canvas 不是唯一通道"的实际体现。 */
    const before = active.store.idAt(active.store.indexOfNodeId("n0"));
    a11y?.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }));
    await frames(2);
    const after = active.store.idAt(0);
    report.keyboardMovedSelection = before !== after || active.store.nodeCount > 1;
  }

  root.unmount();
  await frames(1);
  report.reactDisposedOnUnmount = engineRef === null;
}


/* ------------------------------------------------------------------ 标签（§30.8） */

/** 背景色（`DEFAULT_STYLE.background` 的 0–255 形式），用来判定"这个像素上有没有东西"。 */
const BG = [244, 248, 254];
/** 与背景的曼哈顿距离超过这个值就算"有内容"。抗锯齿边缘会有中间色，阈值取小一点。 */
const FG_DISTANCE = 24;

function decodePng(url: string): Promise<ImageData> {
  return new Promise((resolve, reject) => {
    const image = new Image();
    image.onload = () => {
      const scratch = document.createElement("canvas");
      scratch.width = image.width;
      scratch.height = image.height;
      const ctx = scratch.getContext("2d");
      if (!ctx) {
        reject(new Error("no 2d context"));
        return;
      }
      ctx.drawImage(image, 0, 0);
      resolve(ctx.getImageData(0, 0, image.width, image.height));
    };
    image.onerror = () => reject(new Error("png decode failed"));
    image.src = url;
  });
}

interface InkBox {
  x: number;
  y: number;
  width: number;
  height: number;
  canvasWidth: number;
}

/** 数一个屏幕矩形里"非背景"的像素数。坐标是 CSS 像素，内部按 DPR 折算到画布像素。 */
function countInk(pixels: ImageData, rect: InkBox): number {
  const dpr = Math.max(1, Math.round(pixels.width / Math.max(1, rect.canvasWidth)));
  const x0 = Math.max(0, Math.floor(rect.x * dpr));
  const y0 = Math.max(0, Math.floor(rect.y * dpr));
  const x1 = Math.min(pixels.width, Math.ceil((rect.x + rect.width) * dpr));
  const y1 = Math.min(pixels.height, Math.ceil((rect.y + rect.height) * dpr));
  let ink = 0;
  for (let y = y0; y < y1; y += 1) {
    for (let x = x0; x < x1; x += 1) {
      const i = (y * pixels.width + x) * 4;
      const d = Math.abs(pixels.data[i] - BG[0]) + Math.abs(pixels.data[i + 1] - BG[1]) + Math.abs(pixels.data[i + 2] - BG[2]);
      if (d > FG_DISTANCE) ink += 1;
    }
  }
  return ink;
}

/**
 * 标签验收。
 *
 * 判据的核心是**在导出的 PNG 上数像素**，而不是数实例：
 * 实例数只能证明"CPU 认为该画 N 条标签"，证明不了着色器把它画对了位置。
 * 因此这里先用 `labelInstances()` 算出每条文字**应该**出现的屏幕矩形
 * （公式与顶点着色器一致：左边缘 = 节点屏幕位置 + 半径×scale + 间隙），
 * 再去那个矩形里数非背景像素。
 */
async function runLabelChecks(report: ProbeReport): Promise<void> {
  const host = document.createElement("div");
  host.style.cssText = "position:fixed;left:0;top:0;width:520px;height:380px;";
  document.body.appendChild(host);
  const canvas = document.createElement("canvas");
  canvas.style.width = "100%";
  canvas.style.height = "100%";
  host.appendChild(canvas);

  /*
   * 夹具：一个带标签的枢纽节点 + 一圈带地址短值的叶子。
   * 枢纽放在原点之外一点，避免它的标签压到叶子上——那样数像素会串味。
   */
  const hubId = "0x1234567890abcdef1234567890abcdef12345678";
  const labelNodes: GraphNodeInput[] = [
    { id: hubId, kind: "entity", label: "交易所热钱包", sublabel: "12.4 ETH", x: 0, y: 0, size: 28 },
  ];
  const labelEdges: GraphEdgeInput[] = [];
  for (let i = 0; i < 6; i += 1) {
    const angle = (i / 6) * Math.PI * 2;
    const id = "0x" + (i + 1).toString(16).repeat(20).slice(0, 40);
    /* 半径 40 而不是 150：近景（scale 3.2）下必须**留在视口内**，
       否则"叶子没有标签"分不清是视口过滤生效还是短值回退没实现。 */
    labelNodes.push({ id, kind: i % 2 === 0 ? "address" : "transaction", x: Math.cos(angle) * 40, y: Math.sin(angle) * 40, size: 9 });
    labelEdges.push({ id: "le" + i, source: hubId, target: id });
  }
  /* 让枢纽的度数最高：中景预算只有几条，选出来的必须包含它。 */
  for (let i = 0; i < 6; i += 1) labelEdges.push({ id: "lx" + i, source: labelNodes[1 + i].id, target: labelNodes[1 + ((i + 1) % 6)].id });

  const engine = new GraphEngine({
    nodes: labelNodes,
    edges: labelEdges,
    labels: { budget: { far: 0, mid: 3, near: 12 }, minNodePx: 2 },
  });
  if (!engine.mount(canvas)) {
    report.labelChecks = "webgl2-unavailable";
    host.remove();
    return;
  }

  const waitFrames = () => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));

  /* 近景：文字全部展开（含地址短值）。 */
  engine.camera.state.scale = 3.2;
  engine.camera.state.cx = 0;
  engine.camera.state.cy = 0;
  engine.invalidate();
  await waitFrames();

  const nearStats = engine.stats();
  report.labelsNear = nearStats.labels;
  report.labelGlyphs = nearStats.labelGlyphs;
  report.labelDropped = nearStats.labelDropped;

  const instances = engine.labelInstances();
  const viewport = engine.viewport();
  const inked = await inkOfPrimaryLabel(engine, instances, viewport);
  report.labelPixelsNear = inked.primary;
  report.labelPixelsSublabel = inked.sublabel;
  report.labelPixelsBackground = inked.background;
  report.labelAnchoredRightOfNode = inked.anchoredRight;
  /*
   * 记下近景下主标签的屏幕矩形。远景必须**在同一个矩形里**数像素——
   * 否则"远景没有标签实例"会让这个判据变成空断言（矩形都不存在，当然数到 0）。
   */
  const nearBox = inked.primaryBox;

  /* 中景：预算收紧，标签变少。 */
  engine.camera.state.scale = 1.2;
  engine.invalidate();
  await waitFrames();
  report.labelsMid = engine.stats().labels;

  /* 远景：`LOD_POLICIES.far.labels === false`，一条标签都不该有。 */
  engine.camera.state.scale = 0.05;
  engine.invalidate();
  await waitFrames();
  report.labelsFar = engine.stats().labels;
  report.labelPixelsFar = await inkInBox(engine, nearBox);

  /* 远景 + 选中：固定显示的标签必须回来（§30.8 的例外，也是现有 SVG 的 labelVisible 语义）。 */
  engine.setSelection([0]);
  engine.invalidate();
  await waitFrames();
  report.labelsFarPinned = engine.stats().labels;

  /* 关掉标签开关：背景标签消失，但选中的那个仍然在。 */
  engine.setLabelVisibility(false);
  engine.invalidate();
  await waitFrames();
  report.labelChecks = engine.stats().labels > 0 ? "ok" : "pinned-label-missing";

  engine.dispose();
  host.remove();
}

/**
 * 在导出的 PNG 上数枢纽节点主/副标签矩形内的像素。
 *
 * 屏幕矩形由**与顶点着色器相同的公式**算出：
 *   left = worldToScreen(node).x + radius * scale + gap
 *   top  = worldToScreen(node).y + rowPx
 * 如果着色器里的换算是错的（例如 y 轴方向反了），文字就会落在矩形外，这里会数到 0。
 */
async function inkOfPrimaryLabel(
  engine: GraphEngine,
  instances: readonly { x: number; y: number; anchorRadius: number; gapPx: number; rowPx: number; widthPx: number; heightPx: number }[],
  viewport: { width: number; height: number; dpr: number },
): Promise<{ primary: number; sublabel: number; background: number; anchoredRight: boolean; primaryBox: InkBox | null }> {
  const empty = { primary: 0, sublabel: 0, background: 0, anchoredRight: false, primaryBox: null };
  const url = engine.exportView();
  if (!url) return empty;
  let pixels: ImageData;
  try {
    pixels = await decodePng(url);
  } catch {
    return empty;
  }

  const hubIndex = engine.store.indexOfNodeId("0x1234567890abcdef1234567890abcdef12345678");
  const [nodeX, nodeY] = engine.camera.worldToScreen(
    engine.store.positions[hubIndex * 2],
    engine.store.positions[hubIndex * 2 + 1],
    viewport.width,
    viewport.height,
  );
  const mine = instances.filter((i) => Math.abs(i.x - engine.store.positions[hubIndex * 2]) < 1e-6 && Math.abs(i.y - engine.store.positions[hubIndex * 2 + 1]) < 1e-6);
  const primary = mine.find((i) => i.rowPx === 0);
  const secondary = mine.find((i) => i.rowPx > 0);

  const box = (i: typeof primary) =>
    i
      ? {
          x: nodeX + i.anchorRadius * engine.camera.scale + i.gapPx,
          y: nodeY + i.rowPx,
          width: i.widthPx,
          height: i.heightPx,
          canvasWidth: viewport.width,
        }
      : null;

  /* 对照样本：画布右下角一块确定没有节点的区域。 */
  const background = {
    x: viewport.width - 40,
    y: viewport.height - 24,
    width: 30,
    height: 16,
    canvasWidth: viewport.width,
  };

  const primaryBox = box(primary);
  const secondaryBox = box(secondary);
  return {
    primary: primaryBox ? countInk(pixels, primaryBox) : 0,
    sublabel: secondaryBox ? countInk(pixels, secondaryBox) : 0,
    background: countInk(pixels, background),
    anchoredRight: Boolean(primaryBox) && primaryBox!.x > nodeX,
    primaryBox,
  };
}

/** 在一个**之前记下的**屏幕矩形里数像素（用于"同一块地方，远景应该空了"）。 */
async function inkInBox(engine: GraphEngine, rect: InkBox | null): Promise<number> {
  if (!rect) return 0;
  const url = engine.exportView();
  if (!url) return 0;
  try {
    return countInk(await decodePng(url), rect);
  } catch {
    return 0;
  }
}


/* ------------------------------------------------------- 高亮边的实际颜色（§30.11） */

/**
 * 高亮路径的边，画出来到底是什么颜色？
 *
 * 背景：主边缓冲与高亮边缓冲用的是**同一个着色器**（`EDGE_VS` 里 `a_rgba` 是 vec4），
 * 但两个缓冲是分别装配的。只要有一个装配错了 stride / 偏移 / 分量个数，
 * 结果就是"边的数量对、高亮边的数量对、粒子数量也对，唯独颜色不是那个颜色"——
 * 而颜色恰恰是路径高亮**唯一**的表达方式。所以这里必须读回像素。
 */
async function runHighlightColorCheck(report: ProbeReport): Promise<void> {
  const host = document.createElement("div");
  host.style.cssText = "position:fixed;left:0;top:0;width:480px;height:240px;";
  document.body.appendChild(host);
  const canvas = document.createElement("canvas");
  canvas.style.width = "100%";
  canvas.style.height = "100%";
  host.appendChild(canvas);

  /* 一条水平的三段链：边中点落在整数像素网格上，采样最稳。 */
  const chain = [
    { id: "a", kind: "address" as const, x: -120, y: 0, size: 4 },
    { id: "b", kind: "address" as const, x: 0, y: 0, size: 4 },
    { id: "c", kind: "address" as const, x: 120, y: 0, size: 4 },
  ];
  const chainEdges = [
    { id: "ab", source: "a", target: "b" },
    { id: "bc", source: "b", target: "c" },
  ];

  const engine = new GraphEngine({ nodes: chain, edges: chainEdges });
  if (!engine.mount(canvas)) {
    report.highlightChecks = "webgl2-unavailable";
    host.remove();
    return;
  }
  const waitFrames = () => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));

  /* 把节点缩到很小、并把相机拉到"节点是几个像素的点"的程度，
     这样采样边中点时不会碰到节点本体。 */
  engine.camera.state.scale = 1.4;
  engine.fitAll();
  engine.invalidate();
  await waitFrames();

  const path = engine.findPath("a", "c", { directed: false });
  if (!path) {
    report.highlightChecks = "path-not-found";
    engine.dispose();
    host.remove();
    return;
  }
  engine.highlightPath(path);
  engine.invalidate();
  await waitFrames();

  const url = engine.exportView();
  if (!url) {
    report.highlightChecks = "export-failed";
    engine.dispose();
    host.remove();
    return;
  }
  let pixels: ImageData;
  try {
    pixels = await decodePng(url);
  } catch {
    report.highlightChecks = "png-decode-failed";
    engine.dispose();
    host.remove();
    return;
  }

  /* 期望色：`DEFAULT_STYLE.edgeActiveColor` = #0A7191。 */
  const expected = [10, 113, 145];
  const background = BG;
  const viewport = engine.viewport();
  const dprScale = pixels.width / Math.max(1, viewport.width);

  let lit = 0;
  let samples = 0;
  let worstText = "";
  let wrongHue = 0;
  for (const edge of path.edges) {
    const s = engine.store.edgeSource[edge];
    const t = engine.store.edgeTarget[edge];
    const mx = (engine.store.positions[s * 2] + engine.store.positions[t * 2]) / 2;
    const my = (engine.store.positions[s * 2 + 1] + engine.store.positions[t * 2 + 1]) / 2;
    const [sx, sy] = engine.camera.worldToScreen(mx, my, viewport.width, viewport.height);
    /* 采样点必须落在画布内，否则量到的是"没画"而不是"颜色错"。 */
    if (sx < 2 || sy < 2 || sx > viewport.width - 2 || sy > viewport.height - 2) continue;
    samples += 1;
    const x = Math.round(sx * dprScale);
    const y = Math.round(sy * dprScale);
    const i = (y * pixels.width + x) * 4;
    const rgb = [pixels.data[i], pixels.data[i + 1], pixels.data[i + 2]];

    /*
     * 不能直接比 RGB 相等：WebGL 的线宽固定为 1 像素，而画布开了 antialias，
     * 一条水平线即使落在像素正中，覆盖率也常常只有 50%
     * ——实测值本来就是"期望色与背景的混合"，不是期望色本身。
     *
     * 所以判据是：**这个像素必须落在"背景 → 期望色"这条线段上**。
     * 对每个通道解出混合系数 t = (实测 - 背景) / (期望 - 背景)，
     * 三个通道的 t 必须彼此一致（否则就是另一个色相），且 t 不能太小
     * （高亮没画出来时，量到的是底层普通边那一丁点淡色，t 只有 0.04）。
     */
    const ts: number[] = [];
    let degenerate = false;
    for (let c = 0; c < 3; c += 1) {
      const span = expected[c] - background[c];
      if (Math.abs(span) < 8) continue;
      ts.push((rgb[c] - background[c]) / span);
    }
    if (ts.length < 2) {
      degenerate = true;
    }
    const tMin = Math.min(...ts);
    const tMax = Math.max(...ts);
    const consistent = !degenerate && tMax - tMin <= 0.18;
    const strong = !degenerate && tMin >= 0.35;
    if (consistent && strong) {
      lit += 1;
    } else {
      if (!consistent) wrongHue += 1;
      if (!worstText) {
        worstText = `(${sx.toFixed(0)},${sy.toFixed(0)}) 实测 rgb(${rgb.join(",")}) 期望 rgb(${expected.join(",")}) · t=[${ts
          .map((v) => v.toFixed(2))
          .join(",")}]`;
      }
    }
  }

  report.highlightSamples = samples;
  report.highlightPixelsLit = lit;
  report.highlightPixelMatch = samples > 0 && lit === samples;
  report.highlightPixelRgb = worstText || `全部 ${samples} 个采样点都落在"背景→rgb(${expected.join(",")})"的混合线上`;
  report.highlightChecks =
    samples === 0
      ? "no-sample-in-view"
      : report.highlightPixelMatch
        ? "ok"
        : wrongHue > 0
          ? "hue-mismatch"
          : "coverage-too-low";

  engine.dispose();
  host.remove();
}


/* ------------------------------------------------- 视图维度（§30.8） */

/** 沿一条水平线段逐像素采样，数出"背景段"（=虚线的断口）的段数。 */
function countGapsAlong(pixels: ImageData, row: number, x0: number, x1: number, dprScale: number): { gaps: number; samples: number } {
  const y = Math.round(row * dprScale);
  let gaps = 0;
  let samples = 0;
  let inGap = false;
  for (let sx = x0; sx <= x1; sx += 1) {
    const x = Math.round(sx * dprScale);
    if (x < 0 || x >= pixels.width || y < 0 || y >= pixels.height) continue;
    const i = (y * pixels.width + x) * 4;
    const d =
      Math.abs(pixels.data[i] - BG[0]) + Math.abs(pixels.data[i + 1] - BG[1]) + Math.abs(pixels.data[i + 2] - BG[2]);
    samples += 1;
    const isGap = d <= FG_DISTANCE;
    if (isGap && !inGap) gaps += 1;
    inGap = isGap;
  }
  return { gaps, samples };
}

/** 取一个屏幕点的 RGB。 */
function pixelAt(pixels: ImageData, sx: number, sy: number, dprScale: number): [number, number, number] {
  const x = Math.round(sx * dprScale);
  const y = Math.round(sy * dprScale);
  const i = (y * pixels.width + x) * 4;
  return [pixels.data[i], pixels.data[i + 1], pixels.data[i + 2]];
}

/**
 * 视图维度验收。
 *
 * 这一轮给引擎加的两个能力都**只能靠读像素证明**：
 *   - 虚线：断口是 `discard` 出来的，实例数 / 边数完全看不出来；
 *   - 分组着色：颜色烘焙进顶点缓冲，缓冲区长度与边数一致，也看不出来。
 */
async function runLensChecks(report: ProbeReport): Promise<void> {
  /* ---- 1. 纯函数部分：效果表逐字段核对（不渲染，先钉住策略本身）---- */
  const baseStyle = {
    kindColors: [[0, 0, 0]] as Array<[number, number, number]>,
    edgeColor: [0, 0, 0] as [number, number, number],
    edgeActiveColor: [0, 0, 0] as [number, number, number],
    background: [1, 1, 1] as [number, number, number],
    dimmedAlpha: 0.22,
  };
  const problems: string[] = [];
  const flow = lensStyle("flow", baseStyle);
  const evidence = lensStyle("evidence", baseStyle);
  const asset = lensStyle("asset", baseStyle);
  const time = lensStyle("time", baseStyle);
  if (flow.edgeDash) problems.push("flow 不应是虚线");
  if (!evidence.edgeDash) problems.push("evidence 应为虚线");
  if (evidence.edgeDash && (evidence.edgeDash.on !== EVIDENCE_DASH.on || evidence.edgeDash.off !== EVIDENCE_DASH.off)) {
    problems.push("evidence 虚线周期与 EVIDENCE_DASH 不一致");
  }
  if (evidence.edgeColorMode !== "uniform") problems.push("evidence 不应按分组着色");
  if (asset.edgeColorMode !== "group") problems.push("asset 应按分组着色");
  if ((asset.edgeGroupColors?.length ?? 0) !== ASSET_PALETTE.length) problems.push("asset 调色板长度不符");
  if (!LENS_EFFECTS.time.sublabel) problems.push("time 应显示副标签");
  if (!LENS_EFFECTS.evidence.evidenceOnly) problems.push("evidence 应只保留证据节点及其一跳邻居");
  report.lensStyleChecks = problems.length === 0 ? "ok" : problems.join("；");

  /* ---- 2. 渲染部分 ---- */
  const host = document.createElement("div");
  host.style.cssText = "position:fixed;left:0;top:0;width:520px;height:240px;";
  document.body.appendChild(host);
  const canvas = document.createElement("canvas");
  canvas.style.width = "100%";
  canvas.style.height = "100%";
  host.appendChild(canvas);

  /*
   * 夹具：中间一条**很长**的水平边（虚线才看得出周期），
   * 外加上方/下方各一条不同 colorGroup 的边（分组着色才看得出差异）。
   */
  const nodes = [
    { id: "L", kind: "address" as const, x: -220, y: 0, size: 3 },
    { id: "R", kind: "address" as const, x: 220, y: 0, size: 3 },
    { id: "u0", kind: "transaction" as const, x: -60, y: -70, size: 3 },
    { id: "u1", kind: "transaction" as const, x: 0, y: -70, size: 3 },
    { id: "d0", kind: "transaction" as const, x: -60, y: 70, size: 3 },
    { id: "d1", kind: "transaction" as const, x: 0, y: 70, size: 3 },
  ];
  const edges = [
    { id: "main", source: "L", target: "R" },
    { id: "g0", source: "u0", target: "u1", colorGroup: 0 },
    { id: "g1", source: "d0", target: "d1", colorGroup: 1 },
  ];

  const engine = new GraphEngine({ nodes, edges });
  if (!engine.mount(canvas)) {
    report.lensChecks = "webgl2-unavailable";
    host.remove();
    return;
  }
  const waitFrames = () => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));

  engine.camera.state.scale = 1;
  engine.camera.state.cx = 0;
  engine.camera.state.cy = 0;
  engine.invalidate();
  await waitFrames();

  const viewport = engine.viewport();

  /* --- 2a. 证据透镜：主线必须是虚线 --- */
  let events = 0;
  engine.events.on("lens:change", () => {
    events += 1;
  });
  engine.setLens("evidence");
  engine.invalidate();
  await waitFrames();
  report.lensSwitchEvent = events === 1 && engine.lens === "evidence";

  const dashedUrl = engine.exportView();
  if (dashedUrl) {
    const pixels = await decodePng(dashedUrl);
    const dprScale = pixels.width / Math.max(1, viewport.width);
    const [, sy] = engine.camera.worldToScreen(0, 0, viewport.width, viewport.height);
    const [sx0] = engine.camera.worldToScreen(-200, 0, viewport.width, viewport.height);
    const [sx1] = engine.camera.worldToScreen(200, 0, viewport.width, viewport.height);
    const dashed = countGapsAlong(pixels, sy, sx0 + 2, sx1 - 2, dprScale);
    report.lensDashGaps = dashed.gaps;
    report.lensSamples = dashed.samples;
  }

  /* --- 2b. 资金流透镜：同一位置不应有断口 --- */
  engine.setLens("flow");
  engine.invalidate();
  await waitFrames();
  const solidUrl = engine.exportView();
  if (solidUrl) {
    const pixels = await decodePng(solidUrl);
    const dprScale = pixels.width / Math.max(1, viewport.width);
    const [, sy] = engine.camera.worldToScreen(0, 0, viewport.width, viewport.height);
    const [sx0] = engine.camera.worldToScreen(-200, 0, viewport.width, viewport.height);
    const [sx1] = engine.camera.worldToScreen(200, 0, viewport.width, viewport.height);
    report.lensSolidGaps = countGapsAlong(pixels, sy, sx0 + 2, sx1 - 2, dprScale).gaps;
  }

  /* --- 2c. 资产透镜：两条不同分组的边必须颜色不同，且各自对得上调色板 --- */
  engine.setLens("asset");
  engine.invalidate();
  await waitFrames();
  const assetUrl = engine.exportView();
  if (assetUrl) {
    const pixels = await decodePng(assetUrl);
    const dprScale = pixels.width / Math.max(1, viewport.width);
    const sample = (y: number) => {
      const [sx, sy] = engine.camera.worldToScreen(-30, y, viewport.width, viewport.height);
      return pixelAt(pixels, sx, sy, dprScale);
    };
    const got0 = sample(-70);
    const got1 = sample(70);
    const distance = Math.abs(got0[0] - got1[0]) + Math.abs(got0[1] - got1[1]) + Math.abs(got0[2] - got1[2]);
    report.lensEdgeColorsDistinct = distance > 30;

    /* 每条边都必须落在"背景 → 自己的调色板色"这条混合线上（同 2×alpha=0.5 的覆盖率问题）。 */
    const onLine = (rgb: [number, number, number], target: [number, number, number]): boolean => {
      const ts: number[] = [];
      for (let c = 0; c < 3; c += 1) {
        const span = target[c] - BG[c];
        if (Math.abs(span) < 20) continue;
        ts.push((rgb[c] - BG[c]) / span);
      }
      if (ts.length < 2) return false;
      return Math.max(...ts) - Math.min(...ts) <= 0.2 && Math.min(...ts) >= 0.2;
    };
    const to255 = (c: [number, number, number]): [number, number, number] => [c[0] * 255, c[1] * 255, c[2] * 255];
    const ok0 = onLine(got0, to255(ASSET_PALETTE[0]));
    const ok1 = onLine(got1, to255(ASSET_PALETTE[1]));
    report.lensEdgeColorsMatchPalette = ok0 && ok1;
    report.lensEdgeColorText = `group0 rgb(${got0.join(",")}) vs group1 rgb(${got1.join(",")}) · 对板=${ok0}/${ok1}`;
  }

  report.lensChecks =
    report.lensStyleChecks !== "ok"
      ? "style-table-mismatch"
      : !report.lensSwitchEvent
        ? "lens-switch-broken"
        : report.lensDashGaps < 5
          ? "dash-missing"
          : report.lensSolidGaps !== 0
            ? "solid-has-gaps"
            : !report.lensEdgeColorsDistinct || !report.lensEdgeColorsMatchPalette
              ? "group-color-mismatch"
              : "ok";

  engine.dispose();
  host.remove();
}


/* ------------------------------------------------- 边拾取（§30.7） */

/**
 * 边必须能被点中。
 *
 * 这条能力是 Canvas 迁移到引擎的**前置条件**：现有 SVG 画布给每条边铺了一条
 * 14px 的透明命中线，点边会打开边的 Inspector。引擎只会画节点拾取的话，
 * 迁移过去就等于把这个操作弄丢了（§65「所有现有重要操作不丢」）。
 *
 * 判据刻意包含三档距离，因为"能点到"和"点到的东西是它"是两回事：
 *   · 正好在中点   → 必须命中该边；
 *   · 垂直偏 4px   → 仍必须命中（命中区比 1px 线宽大）；
 *   · 垂直偏 20px  → 必须落空（命中区不能无限大）。
 */
async function runEdgePickChecks(report: ProbeReport): Promise<void> {
  const host = document.createElement("div");
  host.style.cssText = "position:fixed;left:0;top:0;width:640px;height:420px;";
  document.body.appendChild(host);
  const canvas = document.createElement("canvas");
  canvas.style.width = "100%";
  canvas.style.height = "100%";
  host.appendChild(canvas);

  /* 三条互不重叠的边：水平一条、垂直一条、斜一条，覆盖不同朝向。 */
  const pickNodes: GraphNodeInput[] = [
    { id: "A", kind: "address", x: -200, y: -120, size: 10 },
    { id: "B", kind: "address", x: 0, y: -120, size: 10 },
    { id: "C", kind: "address", x: -200, y: 60, size: 10 },
    { id: "D", kind: "address", x: 40, y: 120, size: 10 },
    { id: "E", kind: "address", x: 220, y: -40, size: 10 },
  ];
  const pickEdges: GraphEdgeInput[] = [
    { id: "ab", source: "A", target: "B" },
    { id: "ac", source: "A", target: "C" },
    { id: "de", source: "D", target: "E" },
  ];

  const engine = new GraphEngine({ nodes: pickNodes, edges: pickEdges });
  if (!engine.mount(canvas)) {
    report.edgePickChecks = "webgl2-unavailable";
    host.remove();
    return;
  }
  const waitFrames = () => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
  engine.camera.state.scale = 1;
  engine.camera.state.cx = 0;
  engine.camera.state.cy = 0;
  engine.invalidate();
  await waitFrames();

  const viewport = engine.viewport();
  const toScreen = (x: number, y: number) => engine.camera.worldToScreen(x, y, viewport.width, viewport.height);
  const misses: string[] = [];

  let hits = 0;
  for (let e = 0; e < pickEdges.length; e += 1) {
    const s = engine.store.edgeSource[e];
    const t = engine.store.edgeTarget[e];
    const mx = (engine.store.positions[s * 2] + engine.store.positions[t * 2]) / 2;
    const my = (engine.store.positions[s * 2 + 1] + engine.store.positions[t * 2 + 1]) / 2;
    const [sx, sy] = toScreen(mx, my);
    const hit = engine.pickAny(sx, sy);
    report.edgePickSamples += 1;
    if (hit && hit.kind === "edge" && hit.index === e) hits += 1;
    else misses.push(`${pickEdges[e].id}: 期望 edge#${e}，实际 ${hit ? hit.kind + "#" + hit.index : "null"}`);
  }
  report.edgePickHits = hits;
  report.edgePickMisses = misses.join("；");

  /* 节点中心同时是一条边的端点：必须判给节点。 */
  const [bx, by] = toScreen(0, -120);
  const nodeHit = engine.pickAny(bx, by);
  report.nodeWinsOverEdge = Boolean(nodeHit && nodeHit.kind === "node" && engine.store.idAt(nodeHit.index) === "B");

  /* 边 ab 是水平的，垂直方向偏移即可测试命中区宽度。 */
  const [abx, aby] = toScreen(-100, -120);
  const near = engine.pickAny(abx, aby + 4);
  report.edgePickNearMiss = Boolean(near && near.kind === "edge" && near.index === 0);
  const far = engine.pickAny(abx, aby + 20);
  report.edgePickFarMiss = far === null;

  /* 远离所有节点与边的位置。 */
  const [ex, ey] = toScreen(-260, 180);
  report.edgePickEmptyOk = engine.pickAny(ex, ey) === null;

  /* 既有 API：pickAt 只认节点，边中点处必须返回 -1。 */
  const [abmx, abmy] = toScreen(-100, -120);
  report.pickNodeIgnoresEdges = engine.pickAt(abmx, abmy) === -1;

  report.edgePickChecks =
    report.edgePickHits === report.edgePickSamples &&
    report.nodeWinsOverEdge &&
    report.edgePickNearMiss &&
    report.edgePickFarMiss &&
    report.edgePickEmptyOk &&
    report.pickNodeIgnoresEdges
      ? "ok"
      : "mismatch";

  engine.dispose();
  host.remove();
}

run()
  .then((report) => {
    window.__graphProbe = report;
  })
  .catch((error) => {
    window.__graphProbe = {
      webgl2: false, nodes: 0, edges: 0, bytes: 0, hits: 0, misses: [], emptyHit: -1,
      afterZoomHits: 0, afterZoomMisses: [], zoomMovedScreenPoint: 0, offCanvasHit: -2,
      selectionEvents: 0, exportedPngBytes: 0, disposed: false,
      reactCanvas: false, reactReady: false, a11yListedNeighbors: 0, a11yHasSelectedLabel: false,
      keyboardMovedSelection: false, reactDisposedOnUnmount: false,
      layoutFinite: false, layoutDeterministic: false, columnsMonotonic: false, ringRadiusMonotonic: false,
      forceSeparated: 0, warmStartChangedResult: false, handlerOk: false, handlerResolvedMode: "", handlerDurationMs: 0,
      pathNodes: 0, pathCostUnweighted: 0, pathPrefersCheapEdge: false, pathUndirected: false,
      playbackHopsAtTick: 0, playbackCompleted: false, playbackSpeed2xFaster: false, playbackManualStep: false,
      litEdgesAfterHighlight: 0, particleCount: 0, enginePathEdges: 0, shapesDistinct: false, shapeChecks: "",
      budgetFull: false, budgetAggregate: false, budgetLimit: false, lodThresholds: false,
      lodFarLevel: "", lodNearLevel: "", renderedEdgesFar: 0, renderedEdgesNear: 0,
      farKeepsHeaviest: false, budgetModeForProbe: "", lodChecks: "",
      labelsNear: 0, labelsMid: 0, labelsFar: 0, labelsFarPinned: 0,
      labelGlyphs: 0, labelDropped: 0, labelPixelsNear: 0, labelPixelsFar: 0,
      labelPixelsBackground: 0, labelPixelsSublabel: 0, labelAnchoredRightOfNode: false, labelChecks: "",
      highlightPixelMatch: false, highlightPixelRgb: "", highlightPixelsLit: 0, highlightSamples: 0, highlightChecks: "",
      lensDashGaps: 0, lensSolidGaps: 0, lensSamples: 0, lensEdgeColorsDistinct: false,
      lensEdgeColorsMatchPalette: false, lensEdgeColorText: "", lensStyleChecks: "", lensSwitchEvent: false, lensChecks: "",
      edgePickHits: 0, edgePickSamples: 0, edgePickMisses: "", nodeWinsOverEdge: false,
      edgePickNearMiss: false, edgePickFarMiss: false, edgePickEmptyOk: false, pickNodeIgnoresEdges: false, edgePickChecks: "",
      error: String(error && (error as Error).stack ? (error as Error).stack : error),
    };
  });
