import type { LayoutGraph } from "../model/indices";

/**
 * 布局算法（用户端重构规范 V4 §30.14 / §30.5）
 *
 * 三条共同约定：
 *   1. **只读写 TypedArray**——位置是 `graph.positions`（Float32Array），
 *      算法内部不产生任何 {x,y} 对象，否则十万节点会瞬间制造十万个 JS 对象（§30.4）；
 *   2. **确定性**：随机只来自一个带种子的 LCG（§63C 要求"固定数据集 seed"），
 *      同一个图跑两次得到逐位相同的结果，截图与 benchmark 才可比；
 *   3. **不接触 DOM / WebGL / 消息通道**——所以它们可以在主线程、Worker 或测试里跑。
 *
 * 规范 §30.14 保留 auto / columns / ring，新增 flow / force / entity cluster / timeline。
 * 其中 cluster 需要实体归并信息（G4 的聚合输入），本轮先以"按 kind 分组"的
 * cluster 形式提供，真正的实体聚类留给 G4。
 */

export type LayoutMode = "auto" | "columns" | "ring" | "flow" | "force" | "cluster" | "timeline";

export interface LayoutOptions {
  /** 迭代/收敛参数；不同算法使用其中的一部分。 */
  iterations?: number;
  /** 期望的层间距 / 环间距（世界单位）。 */
  spacing?: number;
  /** 随机种子（默认 1），保证同一图两次布局结果一致。 */
  seed?: number;
  /**
   * 力导向是否以**当前位置**为起点（默认 true）。
   *
   * true 的用处：从 columns 切到 force 时不会重新"炸开"一次，过渡更自然。
   * 代价：结果依赖上一次的布局状态，因此**同一 seed 跑两次结果不同**。
   * 所以 §63C 的基准测试（G10K / G50K / G100K）必须显式传 `warmStart: false`——
   * 否则两次跑出来的图不一样，帧率数字就没有可比性。
   * 这个开关是给"可复现"和"好看"两个目标各留一条路，而不是二选一。
   */
  warmStart?: boolean;
}

export const DEFAULT_LAYOUT_OPTIONS: Required<LayoutOptions> = {
  iterations: 220,
  spacing: 90,
  seed: 1,
  warmStart: true,
};

/** 线性同余伪随机：可复现，且不依赖平台实现（Math.random 不可复现）。 */
export function seededRandom(seed: number): () => number {
  let state = (seed >>> 0) || 1;
  return () => {
    state = (state * 1664525 + 1013904223) >>> 0;
    return state / 4294967296;
  };
}

/** 从每个节点做一次宽度优先，得到它到"根集合"的层号。 */
export function bfsLayers(graph: LayoutGraph): Int32Array {
  const n = graph.nodeCount;
  const layer = new Int32Array(n).fill(-1);
  const queue = new Int32Array(n);
  let head = 0;
  let tail = 0;

  /* 入度为 0 的节点作为根；若整图无根（环形），退化为从 0 号开始。 */
  let roots = 0;
  for (let i = 0; i < n; i += 1) {
    if (graph.inOffset[i + 1] - graph.inOffset[i] === 0) {
      layer[i] = 0;
      queue[tail++] = i;
      roots += 1;
    }
  }
  if (roots === 0 && n > 0) {
    layer[0] = 0;
    queue[tail++] = 0;
  }

  while (head < tail) {
    const node = queue[head++];
    const next = layer[node] + 1;
    for (let i = graph.outOffset[node]; i < graph.outOffset[node + 1]; i += 1) {
      const target = graph.edgeTarget[graph.outEdges[i]];
      if (layer[target] === -1) {
        layer[target] = next;
        queue[tail++] = target;
      }
    }
  }
  /* 不可达节点（孤立点）统一放到最后一层，避免它们堆在原点。 */
  let maxLayer = 0;
  for (let i = 0; i < n; i += 1) maxLayer = Math.max(maxLayer, layer[i]);
  for (let i = 0; i < n; i += 1) if (layer[i] === -1) layer[i] = maxLayer + 1;
  return layer;
}

/** 列布局（columns / flow）：按层号横向分层，层内按度数排序，资金从左流向右。 */
export function applyColumns(graph: LayoutGraph, options: Required<LayoutOptions>, horizontal = true): void {
  const n = graph.nodeCount;
  const layer = bfsLayers(graph);
  const perLayer = new Map<number, number[]>();
  for (let i = 0; i < n; i += 1) {
    const list = perLayer.get(layer[i]) ?? [];
    list.push(i);
    perLayer.set(layer[i], list);
  }
  for (const [depth, list] of perLayer) {
    /* 层内按度数降序：度数高的放中间更容易看清主干（确定性排序，不引入随机）。 */
    list.sort((a, b) => {
      const da = graph.outOffset[a + 1] - graph.outOffset[a] + (graph.inOffset[a + 1] - graph.inOffset[a]);
      const db = graph.outOffset[b + 1] - graph.outOffset[b] + (graph.inOffset[b + 1] - graph.inOffset[b]);
      return db - da || a - b;
    });
    const span = (list.length - 1) * options.spacing;
    list.forEach((node, index) => {
      const offset = index * options.spacing - span / 2;
      if (horizontal) {
        graph.positions[node * 2] = depth * options.spacing * 1.6;
        graph.positions[node * 2 + 1] = offset;
      } else {
        graph.positions[node * 2] = offset;
        graph.positions[node * 2 + 1] = depth * options.spacing * 1.6;
      }
    });
  }
}

/** 环布局（ring）：按层号放在同心圆上——保留既有的 ring 语义。 */
export function applyRing(graph: LayoutGraph, options: Required<LayoutOptions>): void {
  const n = graph.nodeCount;
  if (n === 0) return;
  const layer = bfsLayers(graph);
  const perLayer = new Map<number, number[]>();
  for (let i = 0; i < n; i += 1) {
    const list = perLayer.get(layer[i]) ?? [];
    list.push(i);
    perLayer.set(layer[i], list);
  }
  for (const [depth, list] of perLayer) {
    const radius = depth === 0 ? 0 : depth * options.spacing * 1.5;
    list.forEach((node, index) => {
      const angle = (index / Math.max(1, list.length)) * Math.PI * 2 + depth * 0.35;
      graph.positions[node * 2] = Math.cos(angle) * radius;
      graph.positions[node * 2 + 1] = Math.sin(angle) * radius;
    });
  }
}

/**
 * 力导向（force）。
 *
 * 斥力用**均匀网格分桶**做近似（只与邻近 9 个格子内的节点互斥），
 * 复杂度从 O(n²) 降到接近 O(n)。规范 §30.15 要求 10k 节点仍能交互，
 * 朴素 O(n²) 在 10k 上就是每轮 1 亿次距离计算，必然超预算。
 * 真正的 Barnes-Hut 与 LOD 聚合属于 G4，这里先给出可用的近似。
 */
export function applyForce(graph: LayoutGraph, options: Required<LayoutOptions>): void {
  const n = graph.nodeCount;
  if (n === 0) return;
  const random = seededRandom(options.seed);
  const positions = graph.positions;

  const cell = options.spacing;
  const buckets = new Map<number, number[]>();
  const keyOf = (x: number, y: number) => {
    const gx = Math.floor(x / cell);
    const gy = Math.floor(y / cell);
    return (gx + 4096) * 8192 + (gy + 4096);
  };

  /*
   * 初始位置：warmStart 时沿用当前位置（例如从 columns 过渡过来），
   * 否则一律回到"种子随机铺开"——后者才是可复现的基准模式（见 LayoutOptions.warmStart）。
   */
  let hasPositions = false;
  if (options.warmStart) {
    for (let i = 0; i < n; i += 1) {
      if (positions[i * 2] !== 0 || positions[i * 2 + 1] !== 0) {
        hasPositions = true;
        break;
      }
    }
  }
  for (let i = 0; i < n; i += 1) {
    if (!hasPositions) {
      positions[i * 2] = (random() - 0.5) * options.spacing * 8;
      positions[i * 2 + 1] = (random() - 0.5) * options.spacing * 8;
    }
  }

  const ideal = options.spacing;
  for (let iteration = 0; iteration < options.iterations; iteration += 1) {
    const temperature = (1 - iteration / options.iterations) * ideal * 0.6;
    buckets.clear();
    for (let i = 0; i < n; i += 1) {
      const key = keyOf(positions[i * 2], positions[i * 2 + 1]);
      const list = buckets.get(key);
      if (list) list.push(i);
      else buckets.set(key, [i]);
    }

    /* 斥力：只和邻近格子里的节点互推。 */
    const dispX = new Float32Array(n);
    const dispY = new Float32Array(n);
    for (let i = 0; i < n; i += 1) {
      const xi = positions[i * 2];
      const yi = positions[i * 2 + 1];
      const gx = Math.floor(xi / cell);
      const gy = Math.floor(yi / cell);
      for (let ox = -1; ox <= 1; ox += 1) {
        for (let oy = -1; oy <= 1; oy += 1) {
          const list = buckets.get((gx + ox + 4096) * 8192 + (gy + oy + 4096));
          if (!list) continue;
          for (const j of list) {
            if (i === j) continue;
            let dx = xi - positions[j * 2];
            let dy = yi - positions[j * 2 + 1];
            let distance = Math.sqrt(dx * dx + dy * dy);
            if (distance < 0.01) {
              /* 完全重合时用确定性偏移拆开，避免除零与"两个点永远粘在一起"。 */
              dx = ((i % 7) - 3) * 0.1;
              dy = ((j % 7) - 3) * 0.1;
              distance = 0.1;
            }
            const force = (ideal * ideal) / distance;
            dispX[i] += (dx / distance) * force;
            dispY[i] += (dy / distance) * force;
          }
        }
      }
    }

    /* 引力：每条边把两端拉近。 */
    for (let e = 0; e < graph.edgeCount; e += 1) {
      const s = graph.edgeSource[e];
      const t = graph.edgeTarget[e];
      if (s === t) continue;
      let dx = positions[s * 2] - positions[t * 2];
      let dy = positions[s * 2 + 1] - positions[t * 2 + 1];
      let distance = Math.sqrt(dx * dx + dy * dy);
      if (distance < 0.01) distance = 0.01;
      const force = (distance * distance) / ideal;
      const fx = (dx / distance) * force;
      const fy = (dy / distance) * force;
      dispX[s] -= fx; dispY[s] -= fy;
      dispX[t] += fx; dispY[t] += fy;
    }

    /* 位移限幅 + 写回（限幅是 FR 算法不发散的关键）。 */
    for (let i = 0; i < n; i += 1) {
      const length = Math.sqrt(dispX[i] * dispX[i] + dispY[i] * dispY[i]);
      if (length < 1e-6) continue;
      const step = Math.min(length, temperature) / length;
      positions[i * 2] += dispX[i] * step;
      positions[i * 2 + 1] += dispY[i] * step;
    }
  }
}

/** 簇布局（cluster）：按节点类型分组成列，同类型内部用环排布。 */
export function applyCluster(graph: LayoutGraph, options: Required<LayoutOptions>): void {
  const byKind = new Map<number, number[]>();
  for (let i = 0; i < graph.nodeCount; i += 1) {
    const list = byKind.get(graph.kinds[i]) ?? [];
    list.push(i);
    byKind.set(graph.kinds[i], list);
  }
  const kinds = [...byKind.keys()].sort((a, b) => a - b);
  const columnGap = options.spacing * 3;
  kinds.forEach((kind, column) => {
    const list = byKind.get(kind) ?? [];
    const radius = Math.max(options.spacing * 0.8, (list.length * options.spacing) / (Math.PI * 2));
    list.forEach((node, index) => {
      const angle = (index / Math.max(1, list.length)) * Math.PI * 2;
      graph.positions[node * 2] = (column - (kinds.length - 1) / 2) * columnGap + Math.cos(angle) * radius;
      graph.positions[node * 2 + 1] = Math.sin(angle) * radius;
    });
  });
}

/** 时间轴布局（timeline）：按时间戳（由调用方写进 sizes 之外的元数据）横向排布。 */
export function applyTimeline(graph: LayoutGraph, options: Required<LayoutOptions>, times: Float64Array | null): void {
  const n = graph.nodeCount;
  if (n === 0) return;
  if (!times || times.length !== n) {
    /* 没有时间字段就退化成列布局，**不伪造时间**（§64 #1）。 */
    applyColumns(graph, options, true);
    return;
  }
  const order = Array.from({ length: n }, (_, i) => i).sort((a, b) => times[a] - times[b] || a - b);
  const span = (n - 1) * (options.spacing * 0.6);
  order.forEach((node, index) => {
    graph.positions[node * 2] = index * options.spacing * 0.6 - span / 2;
    /* 纵向按层号散开，避免所有点压在同一条线上。 */
    graph.positions[node * 2 + 1] = (node % 7) * options.spacing * 0.4 - options.spacing * 1.2;
  });
}

/** 统一入口。**只改 positions，不动任何业务字段。** */
export function runLayout(
  mode: LayoutMode,
  graph: LayoutGraph,
  options: Partial<LayoutOptions> = {},
  times: Float64Array | null = null,
): LayoutMode {
  const merged: Required<LayoutOptions> = { ...DEFAULT_LAYOUT_OPTIONS, ...options };
  switch (mode) {
    case "ring":
      applyRing(graph, merged);
      return mode;
    case "columns":
      applyColumns(graph, merged, true);
      return mode;
    case "flow":
      applyColumns(graph, merged, true);
      return mode;
    case "timeline":
      applyTimeline(graph, merged, times);
      return mode;
    case "cluster":
      applyCluster(graph, merged);
      return mode;
    case "force":
      applyForce(graph, merged);
      return mode;
    case "auto":
    default: {
      /*
       * auto：按图的规模选策略，而不是固定一个。
       * 小图用力导向最好看；大图上 O(n²) 的观感收益抵不过交互延迟，
       * 因此直接给分层列布局——这一点在 §30.15 的性能预算下是必须的取舍。
       */
      if (graph.nodeCount <= 400) {
        applyForce(graph, merged);
        return "force";
      }
      applyColumns(graph, merged, true);
      return "columns";
    }
  }
}
