import type { Bounds } from "../interaction/camera";
import { emptyBounds } from "../interaction/camera";
import { buildCsr } from "./indices";

/**
 * 图数据存储（用户端重构规范 V4 §30.4）
 *
 * 核心约束：**大数据不能存成几十万个 React object 并频繁 clone。**
 * 因此这里全部是 TypedArray + 字符串字典池：
 *   - 位置 / 尺寸：Float32Array（连续内存，worker 之间可 transfer）
 *   - 类型、标记：Uint8/Uint16Array（枚举索引，不是字符串）
 *   - 边的端点：Uint32Array（节点下标，不是对象引用）
 *   - 字符串（地址、交易哈希、标签）只存**字典下标**，同一字符串只出现一次
 *
 * 为什么元数据按需读：节点上挂着完整地址与金额字符串，
 * 把它们复制成 N 份 JS 字符串会立刻吃掉几十 MB；字典池让"同一地址出现 500 次"
 * 在内存里只占一份。
 */

/** 节点形状（规范 §30.9）：颜色只是辅助，**形状必须区分**。 */
export const NODE_KIND = {
  address: 0,
  transaction: 1,
  contract: 2,
  entity: 3,
  evidence: 4,
  subject: 5,
  cluster: 6,
} as const;
export type NodeKindName = keyof typeof NODE_KIND;
export const NODE_KIND_NAMES: NodeKindName[] = ["address", "transaction", "contract", "entity", "evidence", "subject", "cluster"];

/** 节点标记位（用位段而不是多个布尔数组，缓存友好）。 */
export const NODE_FLAG = {
  selected: 1 << 0,
  hovered: 1 << 1,
  risk: 1 << 2,
  dimmed: 1 << 3,
  inPath: 1 << 4,
} as const;

export interface GraphNodeInput {
  id: string;
  kind?: NodeKindName | string;
  label?: string;
  /**
   * 副标签（§30.8 近景）：时间透镜下的 "首次出现 → 最近出现"、聚类节点下的 "N 个节点" 等。
   * 它和 label 一样只存字典下标——同一句 "12 个节点" 在图上出现上百次是常态。
   */
  sublabel?: string;
  /** 世界坐标；缺省时由布局（worker）填充，未填充前先落在原点。 */
  x?: number;
  y?: number;
  /** 视觉半径（世界单位）。 */
  size?: number;
}

export interface GraphEdgeInput {
  id: string;
  source: string;
  target: string;
  /** 金额/资产在字典池里的下标，仅用于分组与 LOD，不参与几何。 */
  amountIndex?: number;
  /**
   * 颜色分组下标（资产透镜：同一 token 同色）。
   *
   * 与 `amountIndex` 分开：后者是"金额量级分组"，服务于 LOD 与聚合；
   * 把两者合并会让"按金额分层"和"按 token 上色"互相绑架，
   * 任何一边改分组方式都会顺手改掉另一边的颜色。
   */
  colorGroup?: number;
  weight?: number;
}

export class GraphStore {
  private readonly stringToIndex = new Map<string, number>();
  private readonly strings: string[] = [];
  private readonly idToNode = new Map<number, number>();

  private _nodeCount = 0;
  private _edgeCount = 0;

  positions = new Float32Array(0);
  sizes = new Float32Array(0);
  kinds = new Uint8Array(0);
  flags = new Uint16Array(0);
  /** 节点 id 的字典下标。 */
  nodeIds = new Uint32Array(0);
  nodeLabels = new Uint32Array(0);
  /** 副标签的字典下标；没有副标签时与 nodeIds 相同（读取时据此判定为空）。 */
  nodeSublabels = new Uint32Array(0);

  edgeSource = new Uint32Array(0);
  edgeTarget = new Uint32Array(0);
  edgeWeight = new Float32Array(0);
  edgeAmount = new Uint32Array(0);
  /** 每条边的颜色分组下标（资产透镜用）。 */
  edgeColorGroup = new Uint32Array(0);

  /** CSR 邻接（§30.4 adjacency index）：offset[i]..offset[i+1] 是节点 i 的出边下标。 */
  outOffset = new Uint32Array(0);
  outEdges = new Uint32Array(0);
  inOffset = new Uint32Array(0);
  inEdges = new Uint32Array(0);

  get nodeCount(): number {
    return this._nodeCount;
  }

  get edgeCount(): number {
    return this._edgeCount;
  }

  /** 字符串入池：同一个地址/哈希只存一份。 */
  intern(value: string): number {
    const existing = this.stringToIndex.get(value);
    if (existing !== undefined) return existing;
    const index = this.strings.length;
    this.strings.push(value);
    this.stringToIndex.set(value, index);
    return index;
  }

  stringAt(index: number): string {
    return this.strings[index] ?? "";
  }

  idAt(nodeIndex: number): string {
    return this.stringAt(this.nodeIds[nodeIndex] ?? 0);
  }

  labelAt(nodeIndex: number): string {
    const id = this.nodeIds[nodeIndex] ?? 0;
    const label = this.nodeLabels[nodeIndex] ?? 0;
    return label === id ? "" : this.stringAt(label);
  }

  sublabelAt(nodeIndex: number): string {
    const id = this.nodeIds[nodeIndex] ?? 0;
    const sublabel = this.nodeSublabels[nodeIndex] ?? 0;
    return sublabel === id ? "" : this.stringAt(sublabel);
  }

  /** 节点类型名（§30.9）。给渲染层的类型标签用，业务代码不要拿它做判断。 */
  kindLabelAt(nodeIndex: number): string {
    return NODE_KIND_NAMES[this.kinds[nodeIndex] ?? 0] ?? "";
  }

  indexOfNodeId(id: string): number {
    const dict = this.stringToIndex.get(id);
    if (dict === undefined) return -1;
    return this.idToNode.get(dict) ?? -1;
  }

  /**
   * 全量装载。
   *
   * 一次性分配 TypedArray（而不是逐个 push），因为大图下反复扩容会把
   * 峰值内存翻倍——§30.4 的整个意义就在这里。
   */
  load(nodes: GraphNodeInput[], edges: GraphEdgeInput[]): void {
    this._nodeCount = nodes.length;
    this._edgeCount = edges.length;
    this.idToNode.clear();

    this.positions = new Float32Array(nodes.length * 2);
    this.sizes = new Float32Array(nodes.length);
    this.kinds = new Uint8Array(nodes.length);
    this.flags = new Uint16Array(nodes.length);
    this.nodeIds = new Uint32Array(nodes.length);
    this.nodeLabels = new Uint32Array(nodes.length);
    this.nodeSublabels = new Uint32Array(nodes.length);

    for (let i = 0; i < nodes.length; i += 1) {
      const node = nodes[i];
      this.positions[i * 2] = node.x ?? 0;
      this.positions[i * 2 + 1] = node.y ?? 0;
      this.sizes[i] = node.size ?? 6;
      this.kinds[i] = kindIndex(node.kind);
      const idIndex = this.intern(node.id);
      this.nodeIds[i] = idIndex;
      this.nodeLabels[i] = node.label ? this.intern(node.label) : idIndex;
      /* 与 label 同一套规则：缺省填 id 下标，读取时判定为空。 */
      this.nodeSublabels[i] = node.sublabel ? this.intern(node.sublabel) : idIndex;
      this.idToNode.set(idIndex, i);
    }

    this.edgeSource = new Uint32Array(edges.length);
    this.edgeTarget = new Uint32Array(edges.length);
    this.edgeWeight = new Float32Array(edges.length);
    this.edgeAmount = new Uint32Array(edges.length);
    this.edgeColorGroup = new Uint32Array(edges.length);
    for (let i = 0; i < edges.length; i += 1) {
      const edge = edges[i];
      this.edgeSource[i] = Math.max(0, this.indexOfNodeId(edge.source));
      this.edgeTarget[i] = Math.max(0, this.indexOfNodeId(edge.target));
      this.edgeWeight[i] = edge.weight ?? 1;
      this.edgeAmount[i] = edge.amountIndex ?? 0;
      this.edgeColorGroup[i] = edge.colorGroup ?? 0;
    }

    this.buildAdjacency();
  }

  /**
   * CSR 邻接表：hover 高亮关联边、路径回溯都靠它，避免每次 O(E) 扫描。
   *
   * 实现委托给 `model/indices.ts` 的 `buildCsr`——布局 worker 也要建同一份索引，
   * 两处各写一遍必然漂移（worker 里的边界处理一旦和这里不一致，
   * 就会出现"主线程能跑、worker 布局结果不同"这种极难排查的问题）。
   */
  private buildAdjacency(): void {
    const csr = buildCsr(this._nodeCount, this._edgeCount, this.edgeSource, this.edgeTarget);
    this.outOffset = csr.outOffset;
    this.outEdges = csr.outEdges;
    this.inOffset = csr.inOffset;
    this.inEdges = csr.inEdges;
  }

  setFlag(nodeIndex: number, flag: number, on: boolean): void {
    if (nodeIndex < 0 || nodeIndex >= this._nodeCount) return;
    if (on) this.flags[nodeIndex] |= flag;
    else this.flags[nodeIndex] &= ~flag;
  }

  clearFlags(): void {
    this.flags.fill(0);
  }

  /** 只遍历邻接，不分配新数组——hover 每帧都可能调用它。 */
  forEachNeighbor(nodeIndex: number, visit: (edgeIndex: number, other: number) => void): void {
    if (nodeIndex < 0 || nodeIndex >= this._nodeCount) return;
    for (let i = this.outOffset[nodeIndex]; i < this.outOffset[nodeIndex + 1]; i += 1) {
      const e = this.outEdges[i];
      visit(e, this.edgeTarget[e]);
    }
    for (let i = this.inOffset[nodeIndex]; i < this.inOffset[nodeIndex + 1]; i += 1) {
      const e = this.inEdges[i];
      visit(e, this.edgeSource[e]);
    }
  }

  bounds(): Bounds {
    const out = emptyBounds();
    for (let i = 0; i < this._nodeCount; i += 1) {
      const x = this.positions[i * 2];
      const y = this.positions[i * 2 + 1];
      if (x < out.minX) out.minX = x;
      if (y < out.minY) out.minY = y;
      if (x > out.maxX) out.maxX = x;
      if (y > out.maxY) out.maxY = y;
    }
    if (!Number.isFinite(out.minX)) return { minX: -1, minY: -1, maxX: 1, maxY: 1 };
    return out;
  }

  /** 估算常驻内存（只统计 TypedArray，字符串按 UTF-16 两字节粗算）。 */
  approximateBytes(): number {
    const typed =
      this.positions.byteLength + this.sizes.byteLength + this.kinds.byteLength + this.flags.byteLength +
      this.nodeIds.byteLength + this.nodeLabels.byteLength + this.nodeSublabels.byteLength + this.edgeSource.byteLength +
      this.edgeTarget.byteLength + this.edgeWeight.byteLength + this.edgeAmount.byteLength +
      this.edgeColorGroup.byteLength +
      this.outOffset.byteLength + this.outEdges.byteLength + this.inOffset.byteLength + this.inEdges.byteLength;
    let strings = 0;
    for (const value of this.strings) strings += value.length * 2;
    return typed + strings;
  }
}

export function kindIndex(kind?: string): number {
  if (!kind) return NODE_KIND.address;
  const index = NODE_KIND_NAMES.indexOf(kind as NodeKindName);
  return index < 0 ? NODE_KIND.address : index;
}
