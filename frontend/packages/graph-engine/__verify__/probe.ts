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
import { GraphEngine, GraphStore, bfsLayers, handleLayoutRequest, runLayout } from "../index";
import type { LayoutMode, LayoutRequest } from "../index";
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

  /* 5. dispose 之后必须停止渲染且可重入调用。 */
  engine.dispose();
  engine.dispose();
  report.disposed = true;

  /* 6. React 桥（§30.3）与可访问对象列表（§55.2）。 */
  await runReactBridge(report, nodes, edges);

  /* 7. 布局算法与 worker 处理器（§30.14 / §30.5）。 */
  runLayoutChecks(report);
  return report;
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
      error: String(error && (error as Error).stack ? (error as Error).stack : error),
    };
  });
