import type { LayoutGraph } from "./indices";

/**
 * 资金路径（用户端重构规范 V4 §30.12 path playback）
 *
 * 路径是"一串节点 + 连接它们的边"。这里只做**求路径**这件纯计算，
 * 播放状态机在 `interaction/playback.ts`，绘制在渲染器里——三者分开，
 * 因为它们的失败模式完全不同（找不到路径 / 播放卡住 / 画错方向）。
 *
 * 用 Dijkstra 而不是 BFS：边上带 weight（金额量级），调查里"最短路径"通常指
 * "资金流最大的那条链路"，而不是"跳数最少"。权重语义由调用方决定，
 * 引擎不假设它代表什么业务含义（§64 #1：不发明业务口径）。
 */

export interface GraphPath {
  /** 节点下标，按路径顺序。 */
  nodes: Uint32Array;
  /** 边下标，长度 = nodes.length - 1。 */
  edges: Uint32Array;
  /** 总代价（由边权累加），调用方自行解释。 */
  cost: number;
}

/**
 * 最小二叉堆。
 *
 * 十万节点的图上用数组 + sort 做优先队列会让单次求路径变成 O(E log E) 的排序开销，
 * 而路径播放是**交互动作**（点一下就要出结果），不能有明显停顿。
 */
class MinHeap {
  private keys: Float64Array;
  private values: Uint32Array;
  private size = 0;

  constructor(capacity: number) {
    this.keys = new Float64Array(capacity);
    this.values = new Uint32Array(capacity);
  }

  push(key: number, value: number): void {
    let i = this.size++;
    this.keys[i] = key;
    this.values[i] = value;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (this.keys[parent] <= this.keys[i]) break;
      this.swap(i, parent);
      i = parent;
    }
  }

  pop(): { key: number; value: number } | null {
    if (this.size === 0) return null;
    const key = this.keys[0];
    const value = this.values[0];
    this.size -= 1;
    if (this.size > 0) {
      this.keys[0] = this.keys[this.size];
      this.values[0] = this.values[this.size];
      let i = 0;
      for (;;) {
        const left = i * 2 + 1;
        const right = left + 1;
        let smallest = i;
        if (left < this.size && this.keys[left] < this.keys[smallest]) smallest = left;
        if (right < this.size && this.keys[right] < this.keys[smallest]) smallest = right;
        if (smallest === i) break;
        this.swap(i, smallest);
        i = smallest;
      }
    }
    return { key, value };
  }

  private swap(a: number, b: number): void {
    const k = this.keys[a]; this.keys[a] = this.keys[b]; this.keys[b] = k;
    const v = this.values[a]; this.values[a] = this.values[b]; this.values[b] = v;
  }
}

export interface FindPathOptions {
  /**
   * 有向还是无向。
   *
   * 资金追踪要看**方向**（钱从哪流向哪），所以默认有向；
   * 而"这两个地址之间有没有关系"这类问题需要无向，由调用方显式选择。
   */
  directed?: boolean;
  /** 边权下限，避免 0 权边让 Dijkstra 退化（0 权边等于"免费"，会选出跳数极多的路径）。 */
  minWeight?: number;
}

export function findPath(
  graph: LayoutGraph,
  fromIndex: number,
  toIndex: number,
  options: FindPathOptions = {},
): GraphPath | null {
  const n = graph.nodeCount;
  if (fromIndex < 0 || toIndex < 0 || fromIndex >= n || toIndex >= n) return null;
  if (fromIndex === toIndex) {
    const nodes = new Uint32Array([fromIndex]);
    return { nodes, edges: new Uint32Array(0), cost: 0 };
  }

  const directed = options.directed ?? true;
  const minWeight = options.minWeight ?? 1e-6;

  const dist = new Float64Array(n).fill(Infinity);
  const prevNode = new Int32Array(n).fill(-1);
  const prevEdge = new Int32Array(n).fill(-1);
  const done = new Uint8Array(n);
  dist[fromIndex] = 0;

  const heap = new MinHeap(graph.edgeCount + n + 1);
  heap.push(0, fromIndex);

  while (true) {
    const top = heap.pop();
    if (!top) break;
    const node = top.value;
    if (done[node]) continue;
    done[node] = 1;
    if (node === toIndex) break;

    /*
     * 边权是"代价"，越小越优先。
     * 必须真的读 `edgeWeight`——第一版这里写成了固定 1，
     * 等于把加权最短路退化成 BFS，却仍然对外宣称支持权重：那种"看起来对"的实现最危险。
     */
    const relax = (edgeIndex: number, other: number) => {
      const raw = graph.edgeWeight ? graph.edgeWeight[edgeIndex] : 1;
      const weight = Number.isFinite(raw) && raw > 0 ? raw : 1;
      const next = dist[node] + Math.max(minWeight, weight);
      if (next < dist[other]) {
        dist[other] = next;
        prevNode[other] = node;
        prevEdge[other] = edgeIndex;
        heap.push(next, other);
      }
    };

    for (let i = graph.outOffset[node]; i < graph.outOffset[node + 1]; i += 1) {
      const edge = graph.outEdges[i];
      relax(edge, graph.edgeTarget[edge]);
    }
    if (!directed && graph.inEdges) {
      /*
       * 无向模式沿入边反向走，必须用 CSR 的入边表。
       * 第一版是在这里遍历**全部边**找 target === node —— 那是每个节点 O(E)，
       * 十万条边的图上单次求路径直接变成千万级扫描。有了入边表就是 O(度)。
       */
      for (let i = graph.inOffset[node]; i < graph.inOffset[node + 1]; i += 1) {
        const edge = graph.inEdges[i];
        relax(edge, graph.edgeSource[edge]);
      }
    }
  }

  if (!done[toIndex]) return null;

  /* 回溯：先逆序收集，再翻转。 */
  const nodeChain: number[] = [];
  const edgeChain: number[] = [];
  let cursor = toIndex;
  while (cursor !== -1) {
    nodeChain.push(cursor);
    if (prevEdge[cursor] !== -1) edgeChain.push(prevEdge[cursor]);
    cursor = prevNode[cursor];
  }
  nodeChain.reverse();
  edgeChain.reverse();
  return {
    nodes: Uint32Array.from(nodeChain),
    edges: Uint32Array.from(edgeChain),
    cost: dist[toIndex],
  };
}
