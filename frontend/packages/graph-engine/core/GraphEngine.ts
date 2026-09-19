import { EventBus } from "./EventBus";
import { Camera, type Bounds } from "../interaction/camera";
import { DEFAULT_STYLE, WebGLRenderer, type RenderStyle } from "../renderer/WebGLRenderer";
import { GraphStore, NODE_FLAG, type GraphEdgeInput, type GraphNodeInput } from "../model/GraphStore";
import { runLayout as runLayoutOnGraph, type LayoutMode, type LayoutOptions } from "../layout";
import { layoutTransferables, type LayoutRequest, type LayoutResponse } from "../worker/protocol";
import { findPath, type FindPathOptions, type GraphPath } from "../model/paths";
import { PlaybackController } from "../interaction/playback";
import { decideBudget, type BudgetDecision, type BudgetLimits, type RenderMode } from "../perf/budget";
import { DEFAULT_LOD_THRESHOLDS, lodLevelFor, policyFor, type LodLevel, type LodThresholds } from "../perf/lod";
import { LabelLayer, type LabelLayerOptions } from "../labels/LabelLayer";
import type { LabelInstance } from "../renderer/LabelProgram";
import { lensStyle, type Lens } from "../lenses";

/**
 * 图谱引擎门面（用户端重构规范 V4 §30.2 / §30.3）
 *
 * 这一层是 React 与 GPU 之间**唯一**的边界：
 *   - React 只调用这里的方法、只订阅这里的事件；
 *   - React **不参与**逐帧渲染（§9.6 / §30.1）。
 *
 * 生命周期（§63C）全部由 `dispose()` 收口：停 RAF、解绑指针与键盘、
 * 断开 ResizeObserver、释放 GPU 资源。路由卸载后 20 次进出不应线性增长显存。
 *
 * 本文件**不引入任何 React 依赖**，因此可以被 worker、测试或纯 JS 场景复用。
 */

export interface GraphEngineEvents extends Record<string, unknown> {
  "node:hover": { nodeIndex: number; nodeId: string | null };
  "node:click": { nodeIndex: number; nodeId: string };
  "node:dblclick": { nodeIndex: number; nodeId: string };
  "edge:hover": { edgeIndex: number };
  "selection:change": { nodeIds: string[] };
  "camera:change": { cx: number; cy: number; scale: number };
  "performance:warning": { fps: number; nodeCount: number; edgeCount: number };
  /** 路径播放每前进一步（edgeIndex = -1 表示"起点亮起"）。 */
  "path:hop": { path: { hopIndex: number; nodeIndex: number; edgeIndex: number } };
  "path:complete": { nodeIds: string[] };
  /** 细节层次切换（远景 / 中景 / 近景，§30.8）。 */
  "lod:change": { level: LodLevel; scale: number; renderedEdges: number };
  /** 标签集合被重建（§30.8）。验收用它断言"远景下标签确实消失了"，而不是靠截图。 */
  "labels:change": { count: number; glyphs: number; dropped: number; lod: LodLevel };
  /** 视图维度切换（§30.8）。 */
  "lens:change": { lens: Lens };
}

export interface GraphEngineOptions {
  /** 结构化克隆的图数据（也接受 worker 传来的 TypedArray 版本）。 */
  nodes: GraphNodeInput[];
  edges: GraphEdgeInput[];
  style?: RenderStyle;
  /**
   * 设备像素比上限（§63C "DPR cap"）。
   * 4K + DPR 2 会让一个 1000px 的画布有 4M 像素，粒子与边的填充率会直接压垮中端 GPU。
   */
  maxDpr?: number;
  /** 装饰性动画的最低帧率下限；页面隐藏时降到这个值（业务订阅不受影响）。 */
  hiddenFps?: number;
  /** 覆盖预算阈值（默认取 §30.15 的固定夹具配比）。 */
  budgetLimits?: BudgetLimits;
  /** 标签策略（§30.8）：每档预算 / 重建阈值 / 最小可标注尺寸。 */
  labels?: LabelLayerOptions;
  /** 初始标签开关，默认开。 */
  showLabels?: boolean;
  /** 初始视图维度（§30.8），默认资金流。 */
  lens?: Lens;
  /**
   * 是否由引擎自己接管指针与滚轮（默认 true）。
   *
   * 置为 false 时引擎**只渲染**，不绑定任何指针 / 滚轮 / 双击监听：
   * 宿主页面自己在画布上盖一层透明层来维持它原有的工具语义
   * （选择 / 框选 / 平移这三个工具各自的拖拽含义）。
   *
   * 为什么需要这个开关：引擎默认的"拖拽=平移、点击=选择、滚轮=缩放"
   * 只是**一种**交互约定。把它强加给一个已经有自己工具模型的页面，
   * 等于用迁移渲染器顺手改掉用户的操作习惯——
   * 渲染技术可以换，操作语义不该跟着换。
   */
  interactive?: boolean;
}

export class GraphEngine {
  readonly store = new GraphStore();
  readonly camera = new Camera();
  readonly events = new EventBus<GraphEngineEvents>();
  /**
   * 路径播放状态机（§30.12）。
   *
   * 时间由 `loop()` 注入，内部不起定时器：播放与渲染共用同一个 RAF 时钟，
   * 否则"相机在动的同时路径在走"会出现两条时间线，回放速度会随帧率漂移。
   */
  readonly playback = new PlaybackController();
  /** Reduced Motion（§9.5）：关闭粒子，但**不关闭状态**——点亮的边照常显示。 */
  private reducedMotion = false;

  /** 渲染预算判定（§30.15 / §63C）：超出规模时自动降级，而不是卡死。 */
  readonly budget: BudgetDecision;
  /**
   * 当前细节层次，随相机缩放变化。
   *
   * 初值是 `null` 而不是 `"far"` —— 这一点很关键：
   * 如果初始化成 "far"，那么一张**一开始就处于远景**的大图在首帧会命中
   * "档位没变" 的短路，边预算永远不会下发，于是第一次渲染把全部边都画出来——
   * 正是最该被降采样的那一次。用 null 强制首帧一定同步一次。
   */
  private currentLod: LodLevel | null = null;
  /** LOD 阈值；宿主坐标系与引擎默认不同时由 setLodThresholds 校准。 */
  private lodThresholds: LodThresholds = DEFAULT_LOD_THRESHOLDS;

  private renderer: WebGLRenderer | null = null;
  /**
   * 标签层（§30.8）。
   *
   * 它持有自己的图集画布，所以**只能在 mount() 里创建**——
   * 构造 GraphEngine 时不保证有 DOM，而这个类要能在 worker / 测试环境里被 new 出来。
   */
  private labelLayer: LabelLayer | null = null;
  private readonly labelOptions: LabelLayerOptions;
  /** 节点标签开关（页面工具栏的"显示节点标签"）。 */
  private showLabels = true;
  private canvas: HTMLCanvasElement | null = null;
  private container: HTMLElement | null = null;
  private resizeObserver: ResizeObserver | null = null;
  private rafId = 0;
  private disposed = false;
  private frameDirty = true;
  private hoveredNode = -1;
  private selected = new Set<number>();
  /** 引擎是否接管指针（见 GraphEngineOptions.interactive）。 */
  private readonly interactive: boolean;
  private readonly maxDpr: number;
  private readonly hiddenFps: number;
  private lastFrameAt = 0;
  private fpsSamples: number[] = [];
  private warned = false;

  /* 指针手势状态。 */
  private dragging = false;
  private dragLastX = 0;
  private dragLastY = 0;
  private pointerDownAt = 0;

  private readonly onPointerDown = (event: PointerEvent) => this.handlePointerDown(event);
  private readonly onPointerMove = (event: PointerEvent) => this.handlePointerMove(event);
  private readonly onPointerUp = (event: PointerEvent) => this.handlePointerUp(event);
  private readonly onPointerLeave = () => this.handlePointerLeave();
  private readonly onWheel = (event: WheelEvent) => this.handleWheel(event);
  private readonly onDoubleClick = (event: MouseEvent) => this.handleDoubleClick(event);
  private readonly onContextLost = (event: Event) => {
    event.preventDefault();
    this.renderer?.handleContextLost();
  };
  private readonly onContextRestored = () => {
    this.renderer?.handleContextRestored();
    this.renderer?.upload(this.store);
    /* 上下文恢复会丢掉图集纹理，强制下次重建把标签重新传上去。 */
    this.labelLayer?.invalidate(this.store);
    this.frameDirty = true;
  };

  constructor(options: GraphEngineOptions) {
    this.maxDpr = options.maxDpr ?? 2;
    this.hiddenFps = options.hiddenFps ?? 4;
    this.labelOptions = options.labels ?? {};
    this.showLabels = options.showLabels ?? true;
    this.interactive = options.interactive ?? true;
    this.currentLens = options.lens ?? "flow";
    this.baseStyle = options.style ?? DEFAULT_STYLE;
    this.store.load(options.nodes, options.edges);
    if (options.style) this.pendingStyle = options.style;
    this.budget = decideBudget(options.nodes.length, options.edges.length, options.budgetLimits);
    if (typeof window !== "undefined" && typeof window.matchMedia === "function") {
      this.reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    }
    this.playback.onHop = (hopIndex, nodeIndex, edgeIndex) => {
      /* 高亮与播放进度严格同源：播放器是唯一的状态来源，渲染器不自己记进度。 */
      this.renderer?.uploadHighlight(this.store, this.playback.litEdges(), this.highlightColor);
      this.frameDirty = true;
      this.events.emit("path:hop", { path: { hopIndex, nodeIndex, edgeIndex } });
    };
    this.playback.onComplete = () => {
      const path = this.playback.path;
      const nodeIds = path ? [...path.nodes].map((index) => this.store.idAt(index)) : [];
      this.events.emit("path:complete", { nodeIds });
    };
  }

  private highlightColor: [number, number, number] = DEFAULT_STYLE.edgeActiveColor;

  /** 当前视图维度（工具栏"视图维度"）。 */
  private currentLens: Lens = "flow";
  /** 页面传入的基础样式；透镜样式由它派生，不反过来污染调用方的对象。 */
  private readonly baseStyle: RenderStyle;

  private pendingStyle: RenderStyle | null = null;

  /** 挂载到画布。返回是否拿到了 WebGL2 上下文（false = 调用方应显示降级 UI，§30.6）。 */
  mount(canvas: HTMLCanvasElement): boolean {
    this.canvas = canvas;
    this.container = canvas.parentElement ?? canvas;
    /* 初始样式也要过一遍透镜：否则"打开页面时就是证据透镜"会漏掉虚线与琥珀色。 */
    const initialStyle = lensStyle(this.currentLens, this.pendingStyle ?? DEFAULT_STYLE);
    this.renderer = new WebGLRenderer(canvas, initialStyle);
    if (!this.renderer.available) {
      /* 不抛异常：调用方负责提示"性能受限模式"，引擎保持可查询但不出图。 */
      return false;
    }
    this.renderer.upload(this.store);
    this.camera.fit(this.store.bounds(), this.cssWidth(), this.cssHeight());
    this.labelLayer = new LabelLayer(this.labelOptions);
    this.labelLayer.invalidate(this.store);
    /* 挂载后立刻按当前缩放定档：首帧就该是正确规模，而不是"等用户缩一下才对"。 */
    this.syncLod();
    this.syncLabels();

    /* interactive=false 时一个监听都不绑：宿主页面自己处理交互（见选项注释）。 */
    if (this.interactive) {
      canvas.addEventListener("pointerdown", this.onPointerDown);
      canvas.addEventListener("pointermove", this.onPointerMove);
      canvas.addEventListener("pointerup", this.onPointerUp);
      canvas.addEventListener("pointercancel", this.onPointerUp);
      canvas.addEventListener("pointerleave", this.onPointerLeave);
      canvas.addEventListener("wheel", this.onWheel, { passive: false });
      canvas.addEventListener("dblclick", this.onDoubleClick);
    }
    canvas.addEventListener("contextlost", this.onContextLost);
    canvas.addEventListener("contextrestored", this.onContextRestored);

    if (typeof ResizeObserver !== "undefined" && this.container) {
      this.resizeObserver = new ResizeObserver(() => {
        this.frameDirty = true;
      });
      this.resizeObserver.observe(this.container);
    }

    this.loop();
    return true;
  }

  private cssWidth(): number {
    return Math.max(1, this.container?.clientWidth ?? this.canvas?.clientWidth ?? 1);
  }

  private cssHeight(): number {
    return Math.max(1, this.container?.clientHeight ?? this.canvas?.clientHeight ?? 1);
  }

  private currentDpr(): number {
    const raw = typeof window === "undefined" ? 1 : window.devicePixelRatio || 1;
    return Math.min(raw, this.maxDpr);
  }

  private loop = (): void => {
    if (this.disposed) return;
    this.rafId = requestAnimationFrame(this.loop);
    const now = performance.now();
    const hidden = typeof document !== "undefined" && document.visibilityState === "hidden";
    const minInterval = hidden ? 1000 / this.hiddenFps : 0;
    if (!this.frameDirty && minInterval === 0) return;
    if (now - this.lastFrameAt < minInterval) return;
    this.lastFrameAt = now;

    /*
     * 播放推进与渲染共用同一个时钟（now 已经在上面取过）。
     * 播放中必须每帧重绘：粒子相位靠 `u_time` 前进，光靠脏标记是动不起来的。
     */
    this.playback.tick(now);
    if (this.playback.state === "playing") this.frameDirty = true;

    /* 细节层次：由相机缩放决定，变化时调整边预算（§30.8）。 */
    this.syncLod();
    /* 标签：只在"看得见的集合"真的变了时才重建，平移本身不产生额外开销。 */
    this.syncLabels();

    const started = performance.now();
    this.renderer?.render(this.store, this.camera, this.cssWidth(), this.cssHeight(), this.currentDpr(), {
      timeSeconds: now / 1000,
      /* Reduced Motion：粒子关闭，但已点亮的边与节点状态照常保留（§9.5）。 */
      particlesEnabled: !this.reducedMotion,
      highlightColor: this.highlightColor,
    });
    this.frameDirty = false;

    /* 帧耗时采样：连续超标时报 performance:warning，让 UI 有机会提示降级。 */
    const cost = performance.now() - started;
    this.fpsSamples.push(cost);
    if (this.fpsSamples.length > 60) this.fpsSamples.shift();
    if (!this.warned && this.fpsSamples.length === 60) {
      const avg = this.fpsSamples.reduce((a, b) => a + b, 0) / this.fpsSamples.length;
      if (avg > 24) {
        this.warned = true;
        this.events.emit("performance:warning", {
          fps: Math.round(1000 / Math.max(avg, 1)),
          nodeCount: this.store.nodeCount,
          edgeCount: this.store.edgeCount,
        });
      }
    }
  };

  /* ------------------------------------------------------------ 公开 API（§30.3） */

  loadGraph(nodes: GraphNodeInput[], edges: GraphEdgeInput[]): void {
    this.store.load(nodes, edges);
    this.selected.clear();
    this.renderer?.upload(this.store);
    this.labelLayer?.invalidate(this.store);
    /*
     * 相机归谁管，取决于交互模式（见 GraphEngineOptions.interactive）：
     *
     *   · interactive=true（引擎自己接管指针）——装载后铺满视口是合理的默认；
     *   · interactive=false（宿主自己管 pan/zoom）——**绝不能动相机**。
     *     宿主用自己的坐标系表达 pan/zoom，它会在需要时调用 setCamera；
     *     引擎在这里偷偷 fit 一次，画面就与宿主的坐标系错位了：
     *     渲染看着正常，但点选、框选会整体偏掉——因为两者不再用同一套换算。
     *     数据一变（切换布局、筛选、新增节点）就会重新错位一次。
     */
    if (this.interactive) {
      this.camera.fit(this.store.bounds(), this.cssWidth(), this.cssHeight());
    }
    this.frameDirty = true;
    this.events.emit("selection:change", { nodeIds: [] });
  }

  /** 增量补丁：只追加新点（实时新增交易），不重排已有位置。 */
  patchGraph(nodes: GraphNodeInput[], edges: GraphEdgeInput[]): void {
    const mergedNodes = [...this.nodesSnapshot(), ...nodes];
    const mergedEdges = [...this.edgesSnapshot(), ...edges];
    this.store.load(mergedNodes, mergedEdges);
    this.renderer?.upload(this.store);
    this.labelLayer?.invalidate(this.store);
    this.frameDirty = true;
  }

  private nodesSnapshot(): GraphNodeInput[] {
    const out: GraphNodeInput[] = [];
    for (let i = 0; i < this.store.nodeCount; i += 1) {
      /*
       * 快照必须带上 label / sublabel / kind / size：
       * 增量补丁重建整个 store，少带一个字段就等于**每一次实时新增交易都把全图标签抹掉**。
       */
      out.push({
        id: this.store.idAt(i),
        x: this.store.positions[i * 2],
        y: this.store.positions[i * 2 + 1],
        label: this.store.labelAt(i) || undefined,
        sublabel: this.store.sublabelAt(i) || undefined,
        kind: this.store.kindLabelAt(i),
        size: this.store.sizes[i],
      });
    }
    return out;
  }

  private edgesSnapshot(): GraphEdgeInput[] {
    const out: GraphEdgeInput[] = [];
    for (let e = 0; e < this.store.edgeCount; e += 1) {
      out.push({
        id: "e" + e,
        source: this.store.idAt(this.store.edgeSource[e]),
        target: this.store.idAt(this.store.edgeTarget[e]),
      });
    }
    return out;
  }

  focusNode(nodeId: string, zoom = true): boolean {
    const index = this.store.indexOfNodeId(nodeId);
    if (index < 0) return false;
    this.camera.state.cx = this.store.positions[index * 2];
    this.camera.state.cy = this.store.positions[index * 2 + 1];
    if (zoom) this.camera.state.scale = Math.max(this.camera.state.scale, 2);
    this.setSelection([index]);
    this.frameDirty = true;
    this.emitCamera();
    return true;
  }

  fitSelection(nodeIds: string[]): void {
    const indices = nodeIds.map((id) => this.store.indexOfNodeId(id)).filter((i) => i >= 0);
    if (indices.length === 0) {
      this.camera.fit(this.store.bounds(), this.cssWidth(), this.cssHeight());
    } else {
      const bounds: Bounds = { minX: Infinity, minY: Infinity, maxX: -Infinity, maxY: -Infinity };
      for (const i of indices) {
        const x = this.store.positions[i * 2];
        const y = this.store.positions[i * 2 + 1];
        bounds.minX = Math.min(bounds.minX, x);
        bounds.minY = Math.min(bounds.minY, y);
        bounds.maxX = Math.max(bounds.maxX, x);
        bounds.maxY = Math.max(bounds.maxY, y);
      }
      this.camera.fit(bounds, this.cssWidth(), this.cssHeight(), 80);
    }
    this.frameDirty = true;
    this.emitCamera();
  }

  /**
   * 由宿主页面直接设置相机（interactive=false 时的典型用法）。
   *
   * 页面持有的 pan/zoom 状态是它自己那套坐标（viewBox 单位）下的真值，
   * 这里只做一次换算，不反过来让引擎的相机成为真值——
   * 否则框选、缩放按钮、深链接恢复三处都要跟着改。
   */
  setCamera(state: { cx: number; cy: number; scale: number }): void {
    this.camera.state.cx = state.cx;
    this.camera.state.cy = state.cy;
    this.camera.state.scale = state.scale;
    this.syncLod();
    this.frameDirty = true;
  }

  fitAll(): void {
    this.camera.fit(this.store.bounds(), this.cssWidth(), this.cssHeight());
    this.frameDirty = true;
    this.emitCamera();
  }

  setSelection(nodeIndices: number[]): void {
    this.selected = new Set(nodeIndices);
    this.applySelectionFlags();
    const nodeIds = [...this.selected].map((i) => this.store.idAt(i));
    this.events.emit("selection:change", { nodeIds });
  }

  /** 选中后淡化无关节点（§30.10 select）。 */
  private applySelectionFlags(): void {
    this.store.clearFlags();
    if (this.selected.size === 0) {
      this.markStyle();
      return;
    }
    const keep = new Set<number>(this.selected);
    for (const index of this.selected) {
      this.store.forEachNeighbor(index, (_edge, other) => keep.add(other));
    }
    for (let i = 0; i < this.store.nodeCount; i += 1) {
      if (keep.has(i)) this.store.setFlag(i, NODE_FLAG.inPath, true);
      else this.store.setFlag(i, NODE_FLAG.dimmed, true);
    }
    for (const index of this.selected) this.store.setFlag(index, NODE_FLAG.selected, true);
    this.markStyle();
  }

  clearSelection(): void {
    this.selected.clear();
    this.store.clearFlags();
    this.markStyle();
    this.events.emit("selection:change", { nodeIds: [] });
  }

  /** 导出当前视图为 PNG（§30.3 exportView）。会先同步重绘一帧，保证读到的是最新画面。 */
  exportView(): string | null {
    if (!this.canvas || !this.renderer) return null;
    this.renderer.render(this.store, this.camera, this.cssWidth(), this.cssHeight(), this.currentDpr());
    return this.canvas.toDataURL("image/png");
  }

  /**
   * 在**主线程同步**运行布局。
   *
   * 只适用于小图或无 Worker 的环境——规范 §30.5 要求大图布局必须在 worker：
   * 十万节点的力导向在主线程上就是一次几百毫秒的 long task，
   * 直接违反 §30.15 的"main-thread long task 不得持续 > 50ms"。
   * 大图请用 `layoutInWorker`。
   */
  runLayout(mode: LayoutMode, options: LayoutOptions = {}): LayoutMode {
    const resolved = runLayoutOnGraph(mode, this.store, options);
    this.renderer?.upload(this.store);
    this.frameDirty = true;
    return resolved;
  }

  /**
   * 通过 Worker 运行布局（§30.5）。
   *
   * Worker 由**调用方**构造并传入：Vite 需要 `new Worker(new URL("./x.worker.ts", import.meta.url))`
   * 这种静态形态才能识别 worker 边界，而那个 URL 必须写在应用代码里。
   * 引擎只负责协议与所有权管理。
   *
   * 零拷贝的边界要说清楚：**发送前必须复制**，因为 transfer 会把本引擎持有的
   * TypedArray 置空（neutered）；结果回传后再 set 回来。
   * 这次复制的代价是 2N 个 float（10 万节点约 800KB），远小于结构化克隆整个图。
   */
  layoutInWorker(worker: Worker, mode: LayoutMode, options: LayoutOptions = {}): Promise<LayoutMode> {
    const requestId = ++this.layoutRequestId;
    const request: LayoutRequest = {
      type: "layout",
      requestId,
      mode,
      options,
      nodeCount: this.store.nodeCount,
      edgeCount: this.store.edgeCount,
      positions: this.store.positions.slice(),
      kinds: this.store.kinds.slice(),
      edgeSource: this.store.edgeSource.slice(),
      edgeTarget: this.store.edgeTarget.slice(),
      times: this.layoutTimes ? this.layoutTimes.slice() : null,
    };

    return new Promise<LayoutMode>((resolve, reject) => {
      const onMessage = (event: MessageEvent<LayoutResponse>) => {
        const response = event.data;
        if (!response || response.requestId !== requestId) return;
        worker.removeEventListener("message", onMessage);
        if (response.type === "layout:error") {
          reject(new Error(response.message));
          return;
        }
        /* 把 worker 算好的位置写回本引擎的存储，并重绘。 */
        this.store.positions.set(response.positions);
        this.renderer?.upload(this.store);
        this.frameDirty = true;
        resolve(response.resolvedMode);
      };
      worker.addEventListener("message", onMessage);
      worker.postMessage(request, layoutTransferables(request));
    });
  }

  /** 时间轴布局用的时间戳（由调用方从真实业务字段灌入；没有就不传，**不伪造**）。 */
  setLayoutTimes(times: Float64Array | null): void {
    this.layoutTimes = times;
  }

  /* ------------------------------------------------------- 路径与播放（§30.12） */

  /** 求两个节点之间的路径。找不到返回 null（调用方据此提示"无路径"，不猜）。 */
  findPath(fromId: string, toId: string, options: FindPathOptions = {}): GraphPath | null {
    const from = this.store.indexOfNodeId(fromId);
    const to = this.store.indexOfNodeId(toId);
    if (from < 0 || to < 0) return null;
    return findPath(this.store, from, to, options);
  }

  /** 高亮一条路径（不播放）。传 null 清除。 */
  highlightPath(path: GraphPath | null): void {
    if (!path) {
      this.renderer?.clearHighlight();
      this.frameDirty = true;
      return;
    }
    this.renderer?.uploadHighlight(this.store, path.edges, this.highlightColor);
    this.frameDirty = true;
  }

  playPath(path: GraphPath, nowMs = performance.now()): void {
    /* 不再手动触发 hop 0：`PlaybackController.play()` 内部已经会发一次，
       手动再来一次会让第一跳的回调与事件各响两遍。 */
    this.playback.play(path, nowMs);
    this.renderer?.uploadHighlight(this.store, this.playback.litEdges(), this.highlightColor);
    this.frameDirty = true;
  }

  pausePath(): void {
    this.playback.pause();
  }

  resumePath(nowMs = performance.now()): void {
    this.playback.resume(nowMs);
  }

  stopPath(): void {
    this.playback.stop();
    this.renderer?.clearHighlight();
    this.frameDirty = true;
  }

  setPlaybackSpeed(speed: number): void {
    this.playback.setSpeed(speed);
  }

  /** §30.12 next hop / previous hop：暂停状态下逐步核对用。 */
  nextHop(): boolean {
    return this.playback.nextHop();
  }

  previousHop(): boolean {
    return this.playback.previousHop();
  }

  private layoutRequestId = 0;
  private layoutTimes: Float64Array | null = null;

  /**
   * 在**画布内的 CSS 坐标**上做一次命中测试，返回节点下标（-1 = 没点到）。
   *
   * 公开它是必要的：React 侧有时需要在引擎之外的坐标上做判定
   * （例如把浏览器原生拖拽的落点映射到节点），而验收也需要一个不依赖
   * "看图"的方式证明几何真的被光栅化了——见 scripts/graph-engine-check.mjs。
   */
  /**
   * 一次拾取，同时判定节点与边（§30.7）。
   *
   * 返回的 `index` 是 **GraphStore 里的下标**；页面再用
   * `store.idAt(index)` / 边 id 映射回业务 ref。不要拿渲染顺序当 id。
   */
  pickAny(cssX: number, cssY: number): { kind: "node" | "edge"; index: number } | null {
    return this.renderer?.pickAt(this.store, this.camera, cssX, cssY, this.currentDpr()) ?? null;
  }

  pickAt(cssX: number, cssY: number): number {
    if (!this.renderer || !this.canvas) return -1;
    return this.renderer.pickNode(this.store, this.camera, cssX, cssY, this.currentDpr());
  }

  /** 画布当前尺寸（CSS 像素）与 DPR，供外部换算坐标。 */
  viewport(): { width: number; height: number; dpr: number } {
    return { width: this.cssWidth(), height: this.cssHeight(), dpr: this.currentDpr() };
  }

  /** 当前细节层次（§30.8）。 */
  get lod(): LodLevel {
    return this.currentLod ?? lodLevelFor(this.camera.scale, this.lodThresholds);
  }

  /**
   * 校准 LOD 阈值（见 perf/lod.ts 的说明）。
   *
   * 宿主用自己的坐标系时，必须按"世界单位 → 像素"的实际比例传入阈值，
   * 否则"眼前这张图"和"引擎以为的观察距离"会对不上——
   * 表现是标签莫名其妙地少了一大片，而画面本身完全正常。
   */
  setLodThresholds(thresholds: LodThresholds): void {
    this.lodThresholds = thresholds;
    /* 强制下一次 syncLod 重新判定档位。 */
    this.currentLod = null;
    this.syncLod();
    this.frameDirty = true;
  }

  /**
   * 把边预算同步到当前缩放对应的档位。
   *
   * 只在档位**真的变了**时才下发，避免每帧重建边缓冲；
   * 而 `currentLod` 初值为 null 保证首帧一定会同步一次。
   */
  private syncLod(): void {
    const level = lodLevelFor(this.camera.scale, this.lodThresholds);
    if (level === this.currentLod) return;
    this.currentLod = level;
    this.renderer?.setEdgeBudget(policyFor(level).maxEdges);
    this.events.emit("lod:change", {
      level,
      scale: this.camera.scale,
      renderedEdges: this.renderer?.renderedEdges ?? 0,
    });
  }

  /**
   * 切换视图维度（§30.8 的"看什么"维度）。
   *
   * 引擎只负责**样式类**效果（虚线 / 分组着色）；数据类效果（证据透镜只留
   * 证据节点及其一跳邻居、时间透镜的副标签）由页面按 `LENS_EFFECTS` 执行。
   * 两条路径读的是同一张效果表，不会再各写一套。
   */
  /**
   * 弱化一组节点（视图维度等页面级语义）。
   *
   * 传 alpha=1 等于取消弱化。与选中淡化互不干扰，见渲染器的说明。
   */
  setMutedNodes(nodeIds: Iterable<string>, alpha: number): void {
    this.renderer?.setMutedNodes(nodeIds, alpha);
    this.frameDirty = true;
  }

  clearMutedNodes(): void {
    this.renderer?.clearMutedNodes();
    this.frameDirty = true;
  }

  /** 当前被弱化的节点数（验收断言用，不做业务判断）。 */
  rendererDebugMutedCount(): number {
    return this.renderer?.mutedNodeCount ?? 0;
  }

  setLens(lens: Lens): void {
    if (this.currentLens === lens) return;
    this.currentLens = lens;
    this.renderer?.setStyle(lensStyle(lens, this.baseStyle));
    if (this.pendingStyle) this.pendingStyle = lensStyle(lens, this.baseStyle);
    this.frameDirty = true;
    this.events.emit("lens:change", { lens });
  }

  get lens(): Lens {
    return this.currentLens;
  }

  /**
   * 节点标签开关（§30.8）。
   *
   * 关闭时**选中/悬停/路径节点的标签仍然显示**——这个开关控制的是"背景标签密度"，
   * 不是"能不能看到我正在操作的东西"。
   */
  setLabelVisibility(visible: boolean): void {
    if (this.showLabels === visible) return;
    this.showLabels = visible;
    this.frameDirty = true;
  }

  get labelVisibility(): boolean {
    return this.showLabels;
  }

  /**
   * 重建标签并提交给渲染器。
   *
   * 这个函数在**每帧**都会被调用，所以第一件事必须是廉价的否决：
   * `LabelLayer.build` 内部先比对相机锚点与档位，没变就直接返回 false，
   * 真正的扫描（O(可见节点数)）只在视野实质移动后发生。
   */
  private syncLabels(): void {
    const layer = this.labelLayer;
    const renderer = this.renderer;
    if (!layer || !renderer) return;
    const rebuilt = layer.build(this.store, this.camera, this.cssWidth(), this.cssHeight(), {
      lod: this.lod,
      showLabels: this.showLabels,
      pinned: this.pinnedLabelNodes(),
    });
    if (!rebuilt) return;
    renderer.commitLabels(layer.current, layer.atlas.canvas);
    this.events.emit("labels:change", {
      count: renderer.labelCount,
      glyphs: layer.atlasStats.glyphs,
      dropped: layer.atlasStats.dropped,
      lod: this.lod,
    });
  }

  /**
   * 必须始终带标签的节点：选中 / 悬停 / 路径上的节点。
   *
   * 这些标签不受 LOD 预算约束（远景也不例外）：用户刚刚点过它们，
   * 把名字藏起来是明确的可用性缺陷，而且现有 SVG 的
   * `labelVisible = showNodeLabels || selected || boxed || onPath` 就是这个语义。
   */
  private pinnedLabelNodes(): number[] {
    const pinned = new Set<number>(this.selected);
    if (this.hoveredNode >= 0) pinned.add(this.hoveredNode);
    const path = this.playback.path;
    if (path) for (const index of path.nodes) pinned.add(index);
    return [...pinned];
  }

  /** 当前提交给 GPU 的标签实例数量（不含被渲染器丢弃的情况）。 */
  get labelCount(): number {
    return this.renderer?.labelCount ?? 0;
  }

  /**
   * 当前提交给 GPU 的标签实例。
   *
   * 给验收用：只有拿到`锚点 + 行偏移 + 像素尺寸`才能算出
   * "这条文字应该出现在哪个屏幕矩形里"，进而去导出的 PNG 上数像素——
   * 那是"文字真的被 GPU 光栅化出来了"的直接证据，比断言实例数强得多。
   */
  labelInstances(): readonly LabelInstance[] {
    return this.labelLayer?.current ?? [];
  }

  /** 当前渲染模式（full / aggregate / limit，§30.15）。 */
  get renderMode(): RenderMode {
    return this.budget.mode;
  }

  /**
   * 当前**实际绘制**的边下标（按权重降序）。
   *
   * 公开它有两个用途：验收需要断言"远景下留下的是主干而不是任意前 N 条"；
   * 页面侧的"当前可见边"统计也不能靠前端自己再算一遍——
   * 那会和渲染器的真实行为漂移。
   */
  renderedEdgeIndices(): Uint32Array {
    return this.renderer?.renderedEdgeIndices ?? new Uint32Array(0);
  }

  /** 统计信息（验收与性能报告用，不做业务判断）。 */
  stats(): {
    nodes: number;
    edges: number;
    bytes: number;
    webgl2: boolean;
    renderedEdges: number;
    lod: LodLevel;
    renderMode: RenderMode;
    /** 当前真正会绘制的标签四边形数（含主/副/类型标签）。 */
    labels: number;
    /** 图集里栅格化成功的文字数与"放不下被丢弃"的数——丢弃不为 0 就是需要调大图集。 */
    labelGlyphs: number;
    labelDropped: number;
    /** 当前真正上传到 GPU 的高亮边数——不是算出来的，是渲染器的实际状态。 */
    highlightedEdges: number;
    /** 当前真正会绘制的粒子实例数。 */
    particles: number;
  } {
    return {
      nodes: this.store.nodeCount,
      edges: this.store.edgeCount,
      bytes: this.store.approximateBytes(),
      webgl2: this.renderer?.available ?? false,
      highlightedEdges: this.renderer?.highlightedEdges ?? 0,
      particles: this.renderer?.particleCount ?? 0,
      renderedEdges: this.renderer?.renderedEdges ?? 0,
      lod: this.lod,
      renderMode: this.budget.mode,
      labels: this.renderer?.labelCount ?? 0,
      labelGlyphs: this.labelLayer?.atlasStats.glyphs ?? 0,
      labelDropped: this.labelLayer?.atlasStats.dropped ?? 0,
    };
  }

  /** 主动请求下一帧（React 侧状态变化后调用）。 */
  invalidate(): void {
    this.frameDirty = true;
  }

  private markStyle(): void {
    this.renderer?.markStyleDirty();
    this.frameDirty = true;
  }

  private emitCamera(): void {
    this.events.emit("camera:change", {
      cx: this.camera.state.cx,
      cy: this.camera.state.cy,
      scale: this.camera.state.scale,
    });
  }

  /* ------------------------------------------------------------------ 指针 */

  private localPoint(event: PointerEvent | MouseEvent | WheelEvent): [number, number] {
    const rect = this.canvas?.getBoundingClientRect();
    if (!rect) return [0, 0];
    return [event.clientX - rect.left, event.clientY - rect.top];
  }

  private handlePointerDown(event: PointerEvent): void {
    this.dragging = true;
    this.pointerDownAt = performance.now();
    this.dragLastX = event.clientX;
    this.dragLastY = event.clientY;
    this.canvas?.setPointerCapture?.(event.pointerId);
  }

  private handlePointerMove(event: PointerEvent): void {
    if (this.dragging) {
      const dx = event.clientX - this.dragLastX;
      const dy = event.clientY - this.dragLastY;
      this.dragLastX = event.clientX;
      this.dragLastY = event.clientY;
      if (dx !== 0 || dy !== 0) {
        this.camera.pan(dx, dy);
        this.frameDirty = true;
        this.emitCamera();
      }
      return;
    }
    const [x, y] = this.localPoint(event);
    const index = this.renderer?.pickNode(this.store, this.camera, x, y, this.currentDpr()) ?? -1;
    if (index !== this.hoveredNode) {
      this.hoveredNode = index;
      this.store.clearFlags();
      if (index >= 0) this.store.setFlag(index, NODE_FLAG.hovered, true);
      for (const selected of this.selected) this.store.setFlag(selected, NODE_FLAG.selected, true);
      this.applySelectionFlags();
      this.markStyle();
      this.events.emit("node:hover", { nodeIndex: index, nodeId: index >= 0 ? this.store.idAt(index) : null });
    }
  }

  private handlePointerUp(event: PointerEvent): void {
    const wasDragging = this.dragging;
    this.dragging = false;
    this.canvas?.releasePointerCapture?.(event.pointerId);
    /* 只有"短按且没怎么移动"才算点击，否则拖拽结束时会误选节点。 */
    if (!wasDragging || performance.now() - this.pointerDownAt > 400) return;
    const [x, y] = this.localPoint(event);
    const index = this.renderer?.pickNode(this.store, this.camera, x, y, this.currentDpr()) ?? -1;
    if (index >= 0) {
      this.setSelection([index]);
      this.events.emit("node:click", { nodeIndex: index, nodeId: this.store.idAt(index) });
    } else {
      this.clearSelection();
    }
  }

  private handlePointerLeave(): void {
    this.dragging = false;
    if (this.hoveredNode >= 0) {
      this.hoveredNode = -1;
      this.applySelectionFlags();
      this.events.emit("node:hover", { nodeIndex: -1, nodeId: null });
    }
  }

  private handleWheel(event: WheelEvent): void {
    event.preventDefault();
    const [x, y] = this.localPoint(event);
    const factor = Math.exp(-event.deltaY * 0.0015);
    this.camera.zoomAt(x, y, factor, this.cssWidth(), this.cssHeight());
    this.frameDirty = true;
    this.emitCamera();
  }

  private handleDoubleClick(event: MouseEvent): void {
    const [x, y] = this.localPoint(event);
    const index = this.renderer?.pickNode(this.store, this.camera, x, y, this.currentDpr()) ?? -1;
    if (index >= 0) {
      this.focusNode(this.store.idAt(index), false);
      this.events.emit("node:dblclick", { nodeIndex: index, nodeId: this.store.idAt(index) });
    } else {
      this.fitAll();
    }
  }

  /* --------------------------------------------------------------- 生命周期 */

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    cancelAnimationFrame(this.rafId);
    this.resizeObserver?.disconnect();
    this.resizeObserver = null;
    const canvas = this.canvas;
    if (canvas) {
      canvas.removeEventListener("pointerdown", this.onPointerDown);
      canvas.removeEventListener("pointermove", this.onPointerMove);
      canvas.removeEventListener("pointerup", this.onPointerUp);
      canvas.removeEventListener("pointercancel", this.onPointerUp);
      canvas.removeEventListener("pointerleave", this.onPointerLeave);
      canvas.removeEventListener("wheel", this.onWheel);
      canvas.removeEventListener("dblclick", this.onDoubleClick);
      canvas.removeEventListener("contextlost", this.onContextLost);
      canvas.removeEventListener("contextrestored", this.onContextRestored);
    }
    this.renderer?.dispose();
    this.renderer = null;
    this.canvas = null;
    this.container = null;
    this.events.clear();
  }
}
