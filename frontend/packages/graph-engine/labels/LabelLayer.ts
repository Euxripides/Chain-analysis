import type { LabelInstance } from "../renderer/LabelProgram";
import { LabelAtlas, type GlyphBox } from "../renderer/LabelAtlas";
import type { GraphStore } from "../model/GraphStore";
import { policyFor, type LodLevel } from "../perf/lod";
import type { Camera } from "../interaction/camera";

/**
 * 标签层（用户端重构规范 V4 §30.8）
 *
 * 职责边界：**决定"哪些节点在什么缩放下配得上一个标签"，并把它们变成 GPU 四边形**。
 * 栅格化交给 `LabelAtlas`，绘制交给 `LabelProgram`，这里只做策略。
 *
 * 三条容易踩空的规则：
 *
 * 1. **标签的世界坐标锚点与相机无关**（屏幕尺寸换算在顶点着色器里做），
 *    所以平移/缩放**不需要**重建实例——只有标签集合变了才重建。
 *    这是"十万节点图平移时不能每帧重算标签"的前提。
 * 2. **重建的触发条件是"相机动得足够多"**，不是"相机动了"。
 *    全量扫描在十万节点上是零点几毫秒级，但乘以每帧就是灾难；
 *    而只要视野没有实质位移，可见标签集合几乎不会变。阈值化把代价摊薄到"每移动半屏一次"。
 * 3. **远景默认不给标签**（§30.8 远景 "不显示 label"），
 *    但**选中/悬停/路径上的节点例外**——那是用户刚刚点过的对象，
 *    把它的名字藏起来是明确的可用性缺陷。现有 SVG 的
 *    `labelVisible = showNodeLabels || selected || boxed || onPath` 也是这个语义。
 */

export type LabelPriority = "degree";

export interface LabelLayerOptions {
  /** 每档 LOD 的标签预算（不含"固定显示"的选中/悬停/路径节点）。 */
  budget?: Partial<Record<LodLevel, number>>;
  /**
   * 重建阈值：视野中心位移超过"视口宽度/高度"的比例时重建。
   * 取 0.25 意味着移动四分之一屏才重算一次可见集合。
   */
  rebuildRatio?: number;
  /** 节点在屏幕上小于这个像素半径就不配标签（否则远景会糊成一片文字）。 */
  minNodePx?: number;
  /** 单次重建最多扫描多少个节点（按度数降序），防止超大图上的一次重建变成全量排序。 */
  scanLimit?: number;
  /** 标签相对节点的水平间隙（像素）。 */
  gapPx?: number;
  primaryColor?: string;
  secondaryColor?: string;
}

const DEFAULT_BUDGET: Record<LodLevel, number> = {
  far: 0,
  mid: 24,
  near: 64,
};

/** 图集里同一条文字只栅格化一次：图上重复的实体名/交易所名很常见。 */
type GlyphCache = Map<string, GlyphBox | null>;

export interface LabelBuildInput {
  lod: LodLevel;
  showLabels: boolean;
  /** 固定显示标签的节点（选中 / 悬停 / 路径）。 */
  pinned: Iterable<number>;
}

export class LabelLayer {
  readonly atlas: LabelAtlas;
  private readonly options: Required<Omit<LabelLayerOptions, "budget">> & { budget: Record<LodLevel, number> };
  /** 度数降序的节点下标，加载后算一次。 */
  private order = new Uint32Array(0);
  private instances: LabelInstance[] = [];
  /** 上次重建时的相机状态，用于判断"动得够不够多"。 */
  private anchor = { cx: Number.NaN, cy: Number.NaN, scale: Number.NaN, lod: null as LodLevel | null, show: false, pinned: "" };

  constructor(options: LabelLayerOptions = {}) {
    this.atlas = new LabelAtlas();
    this.options = {
      budget: { ...DEFAULT_BUDGET, ...(options.budget ?? {}) },
      rebuildRatio: options.rebuildRatio ?? 0.25,
      minNodePx: options.minNodePx ?? 3,
      scanLimit: options.scanLimit ?? 40000,
      gapPx: options.gapPx ?? 7,
      primaryColor: options.primaryColor ?? "#1E293B",
      secondaryColor: options.secondaryColor ?? "#5B6B84",
    };
  }

  /** 图变化后调用：重排优先级并强制下次重建。 */
  invalidate(store: GraphStore): void {
    this.order = orderByDegree(store);
    this.anchor.lod = null;
    this.anchor.cx = Number.NaN;
  }

  /**
   * 相机是否已经移动得足够多，值得重算一次可见标签集合。
   * **这个方法必须是纯判断**——它决定了每帧的开销上限。
   */
  needsRebuild(camera: Camera, lod: LodLevel, showLabels: boolean, pinned: Iterable<number>): boolean {
    const a = this.anchor;
    if (a.lod !== lod || a.show !== showLabels) return true;
    const pinnedKey = pinnedKeyOf(pinned);
    if (a.pinned !== pinnedKey) return true;
    if (!Number.isFinite(a.cx)) return true;
    /* 缩放变化也会改变"哪些节点够大"，所以用相对变化量而不是绝对像素。 */
    const scaleDelta = Math.abs(camera.scale - a.scale) / Math.max(a.scale, 1e-6);
    if (scaleDelta > this.options.rebuildRatio) return true;
    return false;
  }

  /**
   * 重建标签实例。返回**是否真的重建了**（调用方据此决定要不要重传 GPU 缓冲）。
   */
  build(
    store: GraphStore,
    camera: Camera,
    width: number,
    height: number,
    input: LabelBuildInput,
  ): boolean {
    if (!this.needsRebuild(camera, input.lod, input.showLabels, input.pinned)) return false;
    const pinnedKey = pinnedKeyOf(input.pinned);
    this.anchor = { cx: camera.state.cx, cy: camera.state.cy, scale: camera.scale, lod: input.lod, show: input.showLabels, pinned: pinnedKey };

    const glyphs: GlyphCache = new Map();
    this.atlas.begin(this.options.primaryColor);
    const instances: LabelInstance[] = [];
    const seen = new Set<number>();

    /* 1) 固定显示的节点优先占位：它们是用户刚刚操作过的对象，不能被预算挤掉。 */
    for (const index of input.pinned) {
      if (index < 0 || index >= store.nodeCount || seen.has(index)) continue;
      seen.add(index);
      this.emit(store, index, instances, glyphs, true, input.lod);
    }

    /*
     * 2) 其余标签：仅当标签开关打开、当前档位允许标签、且还有预算时才参与。
     *    档位是否允许标签由 §30.8 的策略表决定（远景 labels=false），
     *    这里不重复判断。" 远景不显示 label" 因此不可能因为改预算数字而被绕过。
     */
    const budget =
      input.showLabels && policyFor(input.lod).labels ? this.options.budget[input.lod] ?? 0 : 0;
    if (budget > 0) {
      if (this.order.length !== store.nodeCount) this.order = orderByDegree(store);
      const [minX, minY] = camera.screenToWorld(0, 0, width, height);
      const [maxX, maxY] = camera.screenToWorld(width, height, width, height);
      const minSizeWorld = this.options.minNodePx / Math.max(camera.scale, 1e-6);
      let placed = 0;
      let scanned = 0;
      for (let k = 0; k < this.order.length && placed < budget && scanned < this.options.scanLimit; k += 1) {
        const index = this.order[k];
        scanned += 1;
        if (seen.has(index)) continue;
        const x = store.positions[index * 2];
        const y = store.positions[index * 2 + 1];
        /* 视口外的节点不占预算：标签数量应当由"看得见的区域"决定，而不是全图。 */
        if (x < minX || x > maxX || y < minY || y > maxY) continue;
        /* 屏幕上太小的节点不给标签——否则远景会变成一片文字墙。 */
        if ((store.sizes[index] ?? 1) < minSizeWorld) continue;
        if (this.emit(store, index, instances, glyphs, false, input.lod)) placed += 1;
      }
    }

    this.instances = instances;
    return true;
  }

  /** 把一个节点的标签（主标签 + 副标签 + 固定节点的类型标签）转成实例。 */
  private emit(
    store: GraphStore,
    index: number,
    out: LabelInstance[],
    glyphs: GlyphCache,
    pinned: boolean,
    lod: LodLevel,
  ): boolean {
    /*
     * 没有显式标签的节点，在近景显示**地址短值**（§30.8 近景 "展示地址短值"）：
     * 地址本身就是节点身份，短值 0x1234…abcd 比"没有标签"有用得多。
     * 中景不这么做——中景只给重要节点，混入一堆短值会把"重要"淹没。
     */
    const primary = store.labelAt(index) || (lod === "near" ? shortId(store.idAt(index)) : "");
    const secondary = store.sublabelAt(index);
    if (!primary && !secondary) return false;
    const radius = store.sizes[index] ?? 1;
    let emitted = false;

    if (primary) {
      const box = this.glyph(glyphs, primary, this.options.primaryColor);
      if (box) {
        /* 文字放在节点右侧、垂直居中：节点尺寸随缩放变化，居中叠字会溢出；
           侧置标签是图分析工具的通行做法，任何缩放下都读得出来。 */
        out.push(this.instance(store, index, box, radius, 0));
        emitted = true;
      }
    }
    if (secondary) {
      const box = this.glyph(glyphs, secondary, this.options.secondaryColor);
      if (box) {
        /* 副标签挂在主标签下方。主标签缺失时它顶到第一行。 */
        const secondary2 = this.instance(store, index, box, radius, primary ? 1 : 0);
        out.push(secondary2);
        emitted = true;
      }
    }
    /* 类型标签只在固定显示（选中/悬停）时出现：全量显示会把三层文字压在同一个节点上。 */
    if (pinned) {
      const kind = store.kindLabelAt(index);
      if (kind) {
        const box = this.glyph(glyphs, kind, this.options.secondaryColor);
        if (box) {
          const inst = this.instance(store, index, box, radius, -1);
          out.push(inst);
        }
      }
    }
    return emitted;
  }

  private glyph(cache: GlyphCache, text: string, color: string): GlyphBox | null {
    const hit = cache.get(text);
    if (hit !== undefined) return hit;
    /*
     * 颜色不同的文字必须分开栅格化（图集是"直接可采样的彩色位图"，不是遮罩），
     * 所以缓存键要带上颜色——否则副标签会拿到主标签的颜色。
     */
    const key = color + "\u0000" + text;
    const cached = cache.get(key);
    if (cached !== undefined) return cached;
    const box = this.atlas.add(text);
    cache.set(key, box);
    /* 让 `cache.get(text)` 这种"无颜色查询"也能命中主标签色。 */
    if (color === this.options.primaryColor && !cache.has(text)) cache.set(text, box);
    return box;
  }

  private instance(store: GraphStore, index: number, box: GlyphBox, radius: number, row: number): LabelInstance {
    const dpr = this.atlas.dpr;
    const gap = this.options.gapPx;
    return {
      x: store.positions[index * 2],
      y: store.positions[index * 2 + 1],
      /* 半径留在世界单位：换算成像素是着色器的事，否则两次重建之间缩放会让标签与节点错位。 */
      anchorRadius: radius,
      gapPx: gap,
      /*
       * 行偏移按屏幕像素给，**正值向下**——本引擎的世界 y 轴是向下的
       * （`camera.worldToScreen` 与 `clipMatrix` 同向），所以副标签是 +行高，类型标签是 -行高。
       */
      rowPx: row * (this.rowHeightPx + 3),
      widthPx: box.width / dpr,
      heightPx: box.height / dpr,
      /* 图集上传时做了 UNPACK_FLIP_Y，所以 v=1 是画布顶部：
         起点角 `(u0, v0)` 必须是文字**左上角**，才和 quad.y=-1（屏幕上方）对得上。 */
      u0: box.x / this.atlas.width,
      v0: 1 - box.y / this.atlas.height,
      u1: (box.x + box.width) / this.atlas.width,
      v1: 1 - (box.y + box.height) / this.atlas.height,
    };
  }

  /** 主标签行高（像素）。取图集字体的标称高度，避免依赖具体文字。 */
  private get rowHeightPx(): number {
    return 24;
  }

  /** 当前提交给 GPU 的标签实例（验收断言用）。 */
  get current(): readonly LabelInstance[] {
    return this.instances;
  }

  get count(): number {
    return this.instances.length;
  }

  get atlasStats() {
    return this.atlas.stats;
  }
}

/**
 * 地址/哈希短值：`0x1234…abcd`。
 *
 * 短值的**两端保留长度**是有讲究的：地址的前 6 位是链与类型信息，
 * 后 4 位才是肉眼区分的依据；只截前面会让一屏地址看起来一模一样。
 */
export function shortId(id: string, head = 6, tail = 4): string {
  if (id.length <= head + tail + 1) return id;
  return `${id.slice(0, head)}…${id.slice(-tail)}`;
}

/** 固定标签集合的指纹：内容或顺序变了就重建。 */
function pinnedKeyOf(pinned: Iterable<number>): string {
  const values = [...pinned];
  values.sort((a, b) => a - b);
  return values.join(",");
}

/**
 * 节点按度数（出边 + 入边）降序排列，只算一次。
 *
 * 布局与图结构在会话内基本稳定，而"哪个节点更重要"不会因为缩放而改变，
 * 所以把排序结果缓存下来，重建时就变成一次有序扫描。
 */
export function orderByDegree(store: GraphStore): Uint32Array {
  const n = store.nodeCount;
  const degree = new Float64Array(n);
  for (let i = 0; i < n; i += 1) {
    degree[i] = (store.outOffset[i + 1] - store.outOffset[i]) + (store.inOffset[i + 1] - store.inOffset[i]);
  }
  const order = new Uint32Array(n);
  for (let i = 0; i < n; i += 1) order[i] = i;
  /* TypedArray 的 sort 不接受比较器闭包外的状态，这里直接用普通数组排序再回填，
     索引数在一百万以内时内存开销是可控的。 */
  const indices = Array.from(order);
  indices.sort((a, b) => degree[b] - degree[a] || a - b);
  order.set(indices);
  return order;
}
