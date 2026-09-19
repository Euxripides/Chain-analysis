/**
 * 邻接索引（用户端重构规范 V4 §30.2 `model/indices.ts`）
 *
 * CSR（压缩稀疏行）是这里唯一的数据结构选择：hover 高亮关联边、路径回溯、
 * 层号 BFS 都要按节点枚举邻接，用 `Map<nodeId, edge[]>` 做同一件事
 * 会在十万节点上产生十万个数组对象（§30.4 明令禁止的形态）。
 *
 * 之所以单独成文件：**布局 worker 也要建同一份索引**，
 * 而 worker 里没有 GraphStore（也不该为了布局把整个字符串字典池传过去）。
 */

export interface CsrIndex {
  outOffset: Uint32Array;
  outEdges: Uint32Array;
  inOffset: Uint32Array;
  inEdges: Uint32Array;
}

export function buildCsr(
  nodeCount: number,
  edgeCount: number,
  edgeSource: Uint32Array,
  edgeTarget: Uint32Array,
): CsrIndex {
  const outOffset = new Uint32Array(nodeCount + 1);
  const inOffset = new Uint32Array(nodeCount + 1);
  for (let e = 0; e < edgeCount; e += 1) {
    const s = edgeSource[e];
    const t = edgeTarget[e];
    if (s < nodeCount) outOffset[s + 1] += 1;
    if (t < nodeCount) inOffset[t + 1] += 1;
  }
  for (let i = 0; i < nodeCount; i += 1) {
    outOffset[i + 1] += outOffset[i];
    inOffset[i + 1] += inOffset[i];
  }
  const outEdges = new Uint32Array(edgeCount);
  const inEdges = new Uint32Array(edgeCount);
  const outCursor = outOffset.slice(0, nodeCount);
  const inCursor = inOffset.slice(0, nodeCount);
  for (let e = 0; e < edgeCount; e += 1) {
    const s = edgeSource[e];
    const t = edgeTarget[e];
    if (s < nodeCount) outEdges[outCursor[s]++] = e;
    if (t < nodeCount) inEdges[inCursor[t]++] = e;
  }
  return { outOffset, outEdges, inOffset, inEdges };
}

/** 布局算法需要的最小图视图：只要几何与拓扑，不要字符串、不要标记。 */
export interface LayoutGraph {
  nodeCount: number;
  edgeCount: number;
  positions: Float32Array;
  kinds: Uint8Array;
  edgeSource: Uint32Array;
  edgeTarget: Uint32Array;
  outOffset: Uint32Array;
  outEdges: Uint32Array;
  inOffset: Uint32Array;
}
