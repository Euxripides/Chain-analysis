import { Camera } from "../interaction/camera";
import { NODE_FLAG, type GraphStore } from "../model/GraphStore";
import { DEFAULT_PARTICLE_APPEARANCE, ParticleProgram, type ParticleAppearance } from "./ParticleProgram";
import { LabelProgram, type LabelInstance } from "./LabelProgram";

/**
 * WebGL2 渲染器（用户端重构规范 V4 §30.6 / §30.7）
 *
 * 两条硬约束：
 *   1. **React 不渲染图谱节点**（§30.1）——节点与边全部由这里的 GPU 绘制；
 *   2. **禁止把每个节点变成 DOM hitbox**（§30.7）——命中测试走"拾取帧缓冲"：
 *      把节点下标编码成颜色画到一张离屏纹理上，再 readPixels 一个像素解回来。
 *
 * 为什么用实例化四边形而不是 `gl.POINTS`：POINTS 的半径上限由驱动决定
 * （部分实现只有 63px），而且**中心点出屏时整个点会消失**，缩放拖拽时节点会闪。
 * 实例化四边形没有这两个问题，代价只是多一个 4 顶点模板。
 *
 * 上下文丢失（§63C）：`contextlost` 时停止绘制并标记失效，
 * `contextrestored` 后重建全部 GPU 资源（shader / buffer / framebuffer）。
 */


const SDF_GLSL = `
/* -------------------------------------------------------------------------
 * 节点形状（规范 §30.9：颜色是辅助，**形状必须区分**）
 *
 * 为什么形状不是"锦上添花"：色觉障碍用户、灰度打印、以及小尺寸下相近的蓝色系，
 * 都会让"这是地址还是交易"退化成猜。形状是与颜色**平行的**一条信息通道。
 *
 * 同时必须保证：**拾取用的形状与渲染用的形状是同一个函数**。
 * 否则热区与肉眼所见不一致——用户点在一个看起来是空的地方却选中了节点，
 * 或者点在菱形的一个角上却点不中。所以下面这段 SDF 被两个片元着色器共享。
 * ------------------------------------------------------------------------- */

float sdBox(vec2 p, vec2 b) {
  vec2 d = abs(p) - b;
  return length(max(d, 0.0)) + min(max(d.x, d.y), 0.0);
}

float sdHexagon(vec2 p, float r) {
  const vec3 k = vec3(-0.8660254038, 0.5, 0.5773502692);
  vec2 q = abs(p);
  q -= 2.0 * min(dot(k.xy, q), 0.0) * k.xy;
  q -= vec2(clamp(q.x, -k.z * r, k.z * r), r);
  return length(q) * sign(q.y);
}

float sdTriangle(vec2 p, float r) {
  const float k = 1.7320508076;
  vec2 q = p;
  q.x = abs(q.x) - r;
  q.y = q.y + r / k;
  if (q.x + k * q.y > 0.0) q = vec2(q.x - k * q.y, -k * q.x - q.y) / 2.0;
  q.x -= clamp(q.x, -2.0 * r, 0.0);
  return -length(q) * sign(q.y);
}

/*
 * kind 与 GraphStore 的 NODE_KIND 一一对应：
 *   0 address · 1 transaction · 2 contract · 3 entity · 4 evidence · 5 subject · 6 cluster
 * 未知 kind 一律退回圆形，而不是不画——"看不见的节点"比"形状不对的节点"更糟。
 */
float nodeShape(vec2 p, float kind) {
  if (kind < 0.5) return length(p) - 1.0;                       /* 地址：圆 */
  if (kind < 1.5) return (abs(p.x) + abs(p.y) - 1.0) * 0.7071;  /* 交易：菱形 */
  if (kind < 2.5) return sdHexagon(p, 1.0);                     /* 合约：六边形 */
  if (kind < 3.5) return sdBox(p, vec2(0.82));                  /* 实体：方形 */
  if (kind < 4.5) return sdTriangle(p, 1.0);                    /* 证据：三角形 */
  if (kind < 5.5) return abs(length(p) - 0.78) - 0.22;          /* 调查对象：圆环 */
  return length(p) - 0.9;                                       /* 聚类：略小的圆 */
}
`;

const NODE_VS = `#version 300 es
in vec2 a_quad;
in vec2 a_pos;
in float a_size;
in vec3 a_color;
in float a_alpha;
in float a_kind;
uniform mat3 u_matrix;
out vec2 v_local;
out vec3 v_color;
out float v_alpha;
flat out float v_kind;
void main() {
  v_local = a_quad;
  v_color = a_color;
  v_alpha = a_alpha;
  v_kind = a_kind;
  vec2 world = a_pos + a_quad * a_size;
  vec3 clip = u_matrix * vec3(world, 1.0);
  gl_Position = vec4(clip.xy, 0.0, 1.0);
}`;

const NODE_FS = `#version 300 es
precision highp float;
in vec2 v_local;
in vec3 v_color;
in float v_alpha;
flat in float v_kind;
out vec4 outColor;
${SDF_GLSL}
void main() {
  float d = nodeShape(v_local, v_kind);
  if (d > 0.0) discard;
  /* 约 1px 的软边，避免小节点在高 DPR 下出现锯齿。 */
  float edge = smoothstep(0.0, -0.14, d);
  outColor = vec4(v_color, v_alpha * edge);
}`;

const PICK_VS = `#version 300 es
in vec2 a_quad;
in vec2 a_pos;
in float a_size;
in float a_kind;
in uint a_pick;
uniform mat3 u_matrix;
flat out uint v_pick;
flat out float v_kind;
out vec2 v_local;
void main() {
  v_local = a_quad;
  v_pick = a_pick;
  v_kind = a_kind;
  vec2 world = a_pos + a_quad * a_size;
  vec3 clip = u_matrix * vec3(world, 1.0);
  gl_Position = vec4(clip.xy, 0.0, 1.0);
}`;

/*
 * 边的拾取（§30.7 / §61 的"边可点选"）
 *
 * 为什么不用 GL_LINES 画拾取用的边：WebGL2 的 `lineWidth` 在绝大多数驱动上
 * 被钳到 1.0，而 1 像素的命中区意味着"必须精确点到那条线上"——现有 SVG 画布
 * 给的是一条 14px 的透明命中线，直接用线段做拾取等于把这个操作弄丢。
 *
 * 所以这里把每条边展开成一个**实例化四边形**：沿线段方向插值，
 * 在垂直方向按屏幕像素宽度扩展。宽度用 uniform 传，与缩放无关，
 * 和肉眼看到的那条 1px 线是两回事——命中区比可视线宽是刻意的。
 */
const PICK_EDGE_VS = `#version 300 es
in vec2 a_quad;      // -1..1 单位四边形
in vec2 a_from;      // 线段起点（世界坐标）
in vec2 a_to;        // 线段终点（世界坐标）
in float a_pickId;   // 已编码的拾取 id（节点数 + 1 + 边下标）
uniform mat3 u_matrix;
uniform float u_scale;      // 世界单位 → 设备像素
uniform float u_halfWidth;  // 命中半宽（设备像素）
out float v_pickId;
void main() {
  vec2 dir = a_to - a_from;
  float len = length(dir);
  vec2 unit = len > 0.0 ? dir / len : vec2(1.0, 0.0);
  vec2 normal = vec2(-unit.y, unit.x);
  vec2 base = a_from + dir * (a_quad.x * 0.5 + 0.5);
  /* 退化线段（自环）也要留一个圆形热区，否则点不到。 */
  vec2 world = base + normal * (a_quad.y * u_halfWidth / u_scale) + (len > 0.0 ? vec2(0.0) : unit * (a_quad.x * u_halfWidth / u_scale));
  vec3 clip = u_matrix * vec3(world, 1.0);
  gl_Position = vec4(clip.xy, 0.0, 1.0);
  v_pickId = a_pickId;
}`;

const PICK_EDGE_FS = `#version 300 es
precision highp float;
in float v_pickId;
out vec4 outColor;
void main() {
  float id = v_pickId;
  float r = mod(id, 256.0);
  float g = mod(floor(id / 256.0), 256.0);
  float b = mod(floor(id / 65536.0), 256.0);
  outColor = vec4(r / 255.0, g / 255.0, b / 255.0, 1.0);
}`;

/*
 * 拾取片元着色器用 **整数 + 位运算** 编码，而不是浮点颜色：
 * mediump float 存不下 65536，浮点编码在大图上会把不同节点解成同一个 id。
 * flat + uint 是精确的。
 */
const PICK_FS = `#version 300 es
precision highp float;
precision highp int;
in vec2 v_local;
flat in uint v_pick;
flat in float v_kind;
out vec4 outColor;
${SDF_GLSL}
void main() {
  /*
   * 拾取必须用**与渲染完全相同**的形状函数。
   * 否则热区与肉眼所见不一致：用户点在看起来是空的地方却选中了节点，
   * 或者点在菱形的角上却点不中——那种"手感不对"的缺陷最难排查。
   */
  if (nodeShape(v_local, v_kind) > 0.0) discard;
  uint id = v_pick;
  float r = float(id & 255u) / 255.0;
  float g = float((id >> 8u) & 255u) / 255.0;
  float b = float((id >> 16u) & 255u) / 255.0;
  outColor = vec4(r, g, b, 1.0);
}`;

/*
 * 边着色器（§30.8 远期"次要边弱化" / 证据透镜虚线）
 *
 * `a_t` 与 `a_len` 是**为了虚线**：线宽在 WebGL 里固定为 1 像素，
 * 而且 GL_LINES 只有两个顶点，片元阶段拿不到"沿这条线走了多远"。
 * 把 (0/1, 世界长度) 作为顶点属性传进来，插值后 `v_dist` 就是沿线方向的
 * **世界距离**；乘 `u_scale` 换成屏幕距离，虚线的疏密就与缩放无关
 * （否则放大时虚线会跟着变长，看起来像实线）。
 */
const EDGE_VS = `#version 300 es
in vec2 a_pos;
in vec4 a_rgba;
in vec2 a_dash;   // x = 沿线参数 t(0..1)，y = 该边的世界长度
uniform mat3 u_matrix;
out vec4 v_rgba;
out float v_dist;
void main() {
  v_rgba = a_rgba;
  v_dist = a_dash.x * a_dash.y;
  vec3 clip = u_matrix * vec3(a_pos, 1.0);
  gl_Position = vec4(clip.xy, 0.0, 1.0);
}`;

const EDGE_FS = `#version 300 es
precision highp float;
in vec4 v_rgba;
in float v_dist;
uniform float u_scale;    // 世界单位 → CSS 像素
uniform vec2 u_dash;      // x = 实线长度，y = 空白长度；x <= 0 表示画实线
out vec4 outColor;
void main() {
  if (u_dash.x > 0.0) {
    float period = u_dash.x + u_dash.y;
    /* 屏幕空间的相位：世界距离乘 scale 之后就与缩放无关。 */
    float phase = mod(v_dist * u_scale, period);
    /*
     * 用 discard 而不是把 alpha 置 0：置 0 仍然会参与混合，
     * 虚线之间的空隙会变成"半透明的实线"。断开就必须真的不画。
     * （注意 GLSL 是写在 TS 模板字符串里的，注释里不能出现反引号，
     *   否则模板字符串会提前结束——tsc 能过，打包器会在这一步炸掉。）
     */
    if (phase > u_dash.x) discard;
  }
  outColor = v_rgba;
}`;

/**
 * 拾取结果。
 *
 * `index` 对节点是**节点下标**，对边是**边下标**（都取自 GraphStore，
 * 不是"当前渲染列表里的位置"）——远景降采样之后两者会完全不同，
 * 用渲染位置当 id 会让点击落到另一条边上。
 */
export type PickResult = { kind: "node" | "edge"; index: number } | null;

export interface RenderStyle {
  /** 每种节点类型的填充色，[r,g,b] 0–1。索引与 NODE_KIND 对齐。 */
  kindColors: Array<[number, number, number]>;
  edgeColor: [number, number, number];
  edgeActiveColor: [number, number, number];
  background: [number, number, number];
  dimmedAlpha: number;
  /**
   * `uniform`：所有边同色；`group`：按 `GraphEdgeInput.colorGroup` 从
   * `edgeGroupColors` 取色（资产透镜"一眼区分不同 token"）。
   */
  edgeColorMode?: "uniform" | "group";
  edgeGroupColors?: Array<[number, number, number]>;
  /** 虚线开关（屏幕像素）。null = 实线。 */
  edgeDash?: { on: number; off: number } | null;
}

export const DEFAULT_STYLE: RenderStyle = {
  kindColors: [
    [0.204, 0.471, 0.965], // address   #3478F6
    [0.04, 0.443, 0.569], // transaction #0A7191
    [0.353, 0.322, 0.909], // contract  #5A52E8
    [0.612, 0.38, 0.035], // entity    #9C6109
    [0.059, 0.478, 0.298], // evidence  #0F7A4C
    [0.063, 0.129, 0.239], // subject   #10213D
    [0.529, 0.588, 0.675], // cluster   #8796AC
  ],
  edgeColor: [0.784, 0.851, 0.941],
  edgeActiveColor: [0.078, 0.443, 0.569],
  background: [0.957, 0.973, 0.996],
  /* 选中无关节点的淡化值（§30.10 select：其他无关节点 opacity 0.15–0.3）。 */
  dimmedAlpha: 0.22,
  edgeColorMode: "uniform",
  edgeGroupColors: [],
  edgeDash: null,
};

export class WebGLRenderer {
  private gl: WebGL2RenderingContext | null = null;
  private nodeProgram: WebGLProgram | null = null;
  private pickProgram: WebGLProgram | null = null;
  private edgeProgram: WebGLProgram | null = null;

  private quadBuffer: WebGLBuffer | null = null;
  private posBuffer: WebGLBuffer | null = null;
  private sizeBuffer: WebGLBuffer | null = null;
  private colorBuffer: WebGLBuffer | null = null;
  private alphaBuffer: WebGLBuffer | null = null;
  /** 节点类型（形状）。渲染与拾取共用同一份：画什么形状，就按什么形状判热区。 */
  private kindBuffer: WebGLBuffer | null = null;
  private pickBuffer: WebGLBuffer | null = null;
  private edgeBuffer: WebGLBuffer | null = null;

  private pickEdgeProgram: WebGLProgram | null = null;
  private edgePickBuffer: WebGLBuffer | null = null;
  private edgePickInstanceCount = 0;

  private pickFramebuffer: WebGLFramebuffer | null = null;
  private pickTexture: WebGLTexture | null = null;
  private pickWidth = 0;
  private pickHeight = 0;

  /** 高亮边（选中路径 / 播放路径）：单独一个 buffer，只放被点亮的那几段。 */
  private highlightBuffer: WebGLBuffer | null = null;
  private highlightVertexCount = 0;
  private particles: ParticleProgram | null = null;
  private particleAppearance: ParticleAppearance = DEFAULT_PARTICLE_APPEARANCE;
  private labels: LabelProgram | null = null;
  private labelOpacity = 1;

  private instanceCount = 0;
  private edgeVertexCount = 0;
  private styleDirty = true;
  /** 边下标按权重降序排列；降采样取前 N 条。 */
  private edgeOrder = new Uint32Array(0);
  private renderedEdgeOrder = new Uint32Array(0);
  private edgeBudget = Number.POSITIVE_INFINITY;
  private pendingEdgeRebuild = false;

  /** 页面级弱化集合（按节点 id，不是下标：下标会随重载变化）。 */
  private mutedIds = new Set<string>();
  private mutedAlpha = 1;

  private colorCache = new Float32Array(0);
  private alphaCache = new Float32Array(0);
  private pickCache = new Uint32Array(0);
  private kindCache = new Float32Array(0);

  contextLost = false;

  constructor(private canvas: HTMLCanvasElement, private style: RenderStyle = DEFAULT_STYLE) {
    this.gl = canvas.getContext("webgl2", {
      alpha: false,
      antialias: true,
      depth: false,
      stencil: false,
      powerPreference: "high-performance",
      preserveDrawingBuffer: false,
    });
    if (this.gl) this.initGL();
  }

  get available(): boolean {
    return this.gl !== null && !this.contextLost;
  }

  setStyle(style: RenderStyle): void {
    this.style = style;
    this.styleDirty = true;
    /*
     * 边的颜色是**烘焙进顶点缓冲**的（见 rebuildEdges），不是逐帧从 style 读的，
     * 所以换了样式必须重建边缓冲——只置 styleDirty 会让"切换视图维度后颜色没变"，
     * 而且因为节点颜色确实变了，看起来会像是"边忘了改"，极难定位。
     */
    this.pendingEdgeRebuild = true;
  }

  /**
   * 下发虚线参数（证据透镜）。
   *
   * 实线时传 (0, 0)：着色器用 `u_dash.x > 0` 作为"是否虚线"的开关，
   * 这样"关掉虚线"不需要重新编译着色器或换变体。
   */
  private setDashUniform(): void {
    const gl = this.gl;
    if (!gl || !this.edgeProgram) return;
    const dash = this.style.edgeDash ?? null;
    gl.uniform2f(gl.getUniformLocation(this.edgeProgram, "u_dash"), dash ? dash.on : 0, dash ? dash.off : 0);
  }

  private initGL(): void {
    const gl = this.gl;
    if (!gl) return;
    this.nodeProgram = buildProgram(gl, NODE_VS, NODE_FS);
    this.pickProgram = buildProgram(gl, PICK_VS, PICK_FS);
    this.pickEdgeProgram = buildProgram(gl, PICK_EDGE_VS, PICK_EDGE_FS);
    this.edgePickBuffer = gl.createBuffer();
    this.edgeProgram = buildProgram(gl, EDGE_VS, EDGE_FS);

    this.quadBuffer = gl.createBuffer();
    gl.bindBuffer(gl.ARRAY_BUFFER, this.quadBuffer);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 1, -1, -1, 1, 1, 1]), gl.STATIC_DRAW);

    this.posBuffer = gl.createBuffer();
    this.sizeBuffer = gl.createBuffer();
    this.colorBuffer = gl.createBuffer();
    this.alphaBuffer = gl.createBuffer();
    this.kindBuffer = gl.createBuffer();
    this.pickBuffer = gl.createBuffer();
    this.edgeBuffer = gl.createBuffer();
    this.highlightBuffer = gl.createBuffer();
    try {
      this.particles = new ParticleProgram(gl);
    } catch (error) {
      /* 粒子是增强项：编译失败不应该让整个图谱渲染不出来（§30.6 的降级思路）。 */
      console.error("[graph-engine] particle program unavailable", error);
      this.particles = null;
    }
    /*
     * 标签同样是增强项：节点形状与颜色已经承载了类型信息，
     * 标签程序编译失败时图谱照常可读，只是少了文字（§30.6）。
     */
    try {
      this.labels = new LabelProgram(gl);
    } catch (error) {
      console.error("[graph-engine] label program unavailable", error);
      this.labels = null;
    }
  }

  /** 上下文恢复后重建全部 GPU 资源（§63C contextrestored）。 */
  handleContextRestored(): void {
    this.contextLost = false;
    this.initGL();
    this.styleDirty = true;
    this.pickWidth = 0;
    this.pickHeight = 0;
  }

  handleContextLost(): void {
    this.contextLost = true;
  }

  /**
   * 提交标签（§30.8）。
   *
   * 图集纹理**只在这里重传**——标签集合变化时才调用，不在每帧。
   * 传 `atlas` 为 null 表示"复用上一次的图集纹理，只换实例"。
   */
  commitLabels(instances: readonly LabelInstance[], atlas: HTMLCanvasElement | null, opacity = 1): void {
    if (!this.labels) return;
    this.labelOpacity = opacity;
    if (atlas) this.labels.uploadAtlas(atlas);
    this.labels.upload(instances as LabelInstance[]);
  }

  /** 清空标签（远景无标签 / 关闭标签开关时）。 */
  clearLabels(): void {
    this.labels?.clear();
  }

  /** 当前真正会绘制的标签四边形数（验收断言用，不是估算值）。 */
  get labelCount(): number {
    return this.labels?.count ?? 0;
  }

  /** 上传图数据（几何 + 每节点实例属性）。 */
  upload(store: GraphStore): void {
    const gl = this.gl;
    if (!gl || this.contextLost) return;

    gl.bindBuffer(gl.ARRAY_BUFFER, this.posBuffer);
    gl.bufferData(gl.ARRAY_BUFFER, store.positions, gl.DYNAMIC_DRAW);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.sizeBuffer);
    gl.bufferData(gl.ARRAY_BUFFER, store.sizes, gl.DYNAMIC_DRAW);
    this.instanceCount = store.nodeCount;

    /*
     * 边按**权重降序**排一次序，之后所有降采样都取这个序列的前 N 条。
     * 这样远景下留下的是"主干资金流"，而不是任意前 N 条——
     * 规范 §30.8 要求远景"展示 cluster 与主干资金流"，随便截断会把主干截掉。
     * 排序只在装载时做一次，不在每帧。
     */
    this.edgeOrder = new Uint32Array(store.edgeCount);
    for (let e = 0; e < store.edgeCount; e += 1) this.edgeOrder[e] = e;
    this.edgeOrder.sort((a, b) => store.edgeWeight[b] - store.edgeWeight[a] || a - b);

    this.rebuildEdges(store, this.edgeBudget);
    this.styleDirty = true;
  }

  /**
   * 按预算重建边缓冲（§30.8 远景边降采样）。
   *
   * 只在 LOD 档位变化时调用，不在每帧调用：这是一次 O(N) 的 CPU 写入 +
   * 一次 bufferData 上传，放进 RAF 循环会直接吃掉帧率。
   */
  private rebuildEdges(store: GraphStore, maxEdges: number): void {
    const gl = this.gl;
    if (!gl || this.contextLost) return;
    const total = this.edgeOrder.length;
    const count = Math.max(0, Math.min(total, Number.isFinite(maxEdges) ? maxEdges : total));
    const byGroup = this.style.edgeColorMode === "group" && (this.style.edgeGroupColors?.length ?? 0) > 0;
    /* 每个顶点 8 个 float：(x, y, r, g, b, a, t, 世界长度)。 */
    const edgeData = new Float32Array(count * 2 * 8);
    for (let i = 0; i < count; i += 1) {
      const e = this.edgeOrder[i];
      const s = store.edgeSource[e];
      const t = store.edgeTarget[e];
      const sx = store.positions[s * 2];
      const sy = store.positions[s * 2 + 1];
      const tx = store.positions[t * 2];
      const ty = store.positions[t * 2 + 1];
      /*
       * 资产透镜下每条边按自己的分组取色。
       * 取色在**这里**做而不是在着色器里：分组索引是整数，
       * 在着色器里查调色板要传数组或纹理，而重建边缓冲本来就是 O(N) 的 CPU 写入。
       */
      const groupColors = this.style.edgeGroupColors ?? [];
      const color =
        byGroup && groupColors.length > 0
          ? groupColors[store.edgeColorGroup[e] % groupColors.length] ?? this.style.edgeColor
          : this.style.edgeColor;
      /* 自环长度按 0 处理：虚线相位对退化线段没有意义，而且除零会变成 NaN。 */
      const length = s === t ? 0 : Math.hypot(tx - sx, ty - sy);
      const base = i * 16;
      edgeData[base] = sx;
      edgeData[base + 1] = sy;
      edgeData[base + 2] = color[0];
      edgeData[base + 3] = color[1];
      edgeData[base + 4] = color[2];
      edgeData[base + 5] = 0.5;
      edgeData[base + 6] = 0;
      edgeData[base + 7] = length;
      edgeData[base + 8] = tx;
      edgeData[base + 9] = ty;
      edgeData[base + 10] = color[0];
      edgeData[base + 11] = color[1];
      edgeData[base + 12] = color[2];
      edgeData[base + 13] = 0.5;
      edgeData[base + 14] = 1;
      edgeData[base + 15] = length;
    }
    gl.bindBuffer(gl.ARRAY_BUFFER, this.edgeBuffer);
    gl.bufferData(gl.ARRAY_BUFFER, edgeData, gl.DYNAMIC_DRAW);
    this.edgeVertexCount = count * 2;
    this.renderedEdgeOrder = this.edgeOrder.subarray(0, count);

    /*
     * 边的拾取实例与可见边**同源**（同一份 edgeOrder 前缀），
     * 所以"看得见哪条边"和"点得到哪条边"永远一致——
     * 两者分开构建就会出现"远景降采样后，点到了一条根本没画的边"。
     */
    this.rebuildEdgePickInstances(store, count);
  }

  /** 每条可见边一个实例：(起点, 终点, 编码 id)，拾取时在着色器里展开成四边形。 */
  private rebuildEdgePickInstances(store: GraphStore, count: number): void {
    const gl = this.gl;
    if (!gl || !this.edgePickBuffer) return;
    /* 每个实例 5 个 float：fromX, fromY, toX, toY, pickId。 */
    const data = new Float32Array(count * 5);
    let cursor = 0;
    for (let i = 0; i < count; i += 1) {
      const e = this.edgeOrder[i];
      const s = store.edgeSource[e];
      const t = store.edgeTarget[e];
      data[cursor++] = store.positions[s * 2];
      data[cursor++] = store.positions[s * 2 + 1];
      data[cursor++] = store.positions[t * 2];
      data[cursor++] = store.positions[t * 2 + 1];
      /*
       * id 空间与节点**不重叠**：1..nodeCount 归节点，之后归边。
       * 这样一次 readPixels 就能区分"点到的是节点还是边"，
       * 不需要维护两套拾取目标。
       */
      data[cursor++] = store.nodeCount + 1 + e;
    }
    gl.bindBuffer(gl.ARRAY_BUFFER, this.edgePickBuffer);
    gl.bufferData(gl.ARRAY_BUFFER, data, gl.DYNAMIC_DRAW);
    this.edgePickInstanceCount = count;
  }

  /** 命中半宽（CSS 像素）。比可见的 1px 线宽得多，这是刻意的。 */
  static readonly EDGE_PICK_HALF_WIDTH_CSS = 7;

  /** 设置边预算；返回是否真的发生了变化（没变就不重建）。 */
  setEdgeBudget(maxEdges: number): boolean {
    if (maxEdges === this.edgeBudget) return false;
    this.edgeBudget = maxEdges;
    this.pendingEdgeRebuild = true;
    return true;
  }

  /** 由引擎在渲染前调用：真正执行延迟的边重建。 */
  applyPendingEdgeRebuild(store: GraphStore): void {
    if (!this.pendingEdgeRebuild) return;
    this.pendingEdgeRebuild = false;
    this.rebuildEdges(store, this.edgeBudget);
  }

  /** 当前实际绘制的边数（验收读的是这个真实值，不是算出来的）。 */
  get renderedEdges(): number {
    return this.edgeVertexCount / 2;
  }

  /** 当前实际绘制的边下标（按权重降序），用于断言"留下的是主干"。 */
  get renderedEdgeIndices(): Uint32Array {
    return this.renderedEdgeOrder;
  }

  /**
   * 重算每节点的颜色/透明度/拾取 id。
   *
   * 只在**样式版本变化**时执行（选择、hover、过滤），不是每帧——
   * 这一层是 O(N) CPU 工作，放进 RAF 循环会让大图掉帧。
   */
  private refreshStyleCache(store: GraphStore): void {
    const n = store.nodeCount;
    if (this.colorCache.length < n * 3) {
      this.colorCache = new Float32Array(n * 3);
      this.alphaCache = new Float32Array(n);
      this.pickCache = new Uint32Array(n);
      this.kindCache = new Float32Array(n);
    }
    for (let i = 0; i < n; i += 1) {
      const kind = store.kinds[i];
      const color = this.style.kindColors[kind] ?? this.style.kindColors[0];
      this.colorCache[i * 3] = color[0];
      this.colorCache[i * 3 + 1] = color[1];
      this.colorCache[i * 3 + 2] = color[2];
      const flags = store.flags[i];
      /*
       * 选中淡化优先：两者叠加时取更弱的那个，避免"又被选中又被弱化"
       * 变成一个几乎看不见的点。
       */
      const selectionDimmed = (flags & NODE_FLAG.dimmed) !== 0;
      const muted = this.mutedIds.has(store.idAt(i));
      this.alphaCache[i] = selectionDimmed
        ? Math.min(this.style.dimmedAlpha, muted ? this.mutedAlpha : 1)
        : muted
          ? this.mutedAlpha
          : 1;
      /* 形状通道（§30.9）：与颜色平行的一条独立信息，不是装饰。 */
      this.kindCache[i] = kind;
      /* 拾取 id 用 1 基：0 必须留给"什么都没点到"。 */
      this.pickCache[i] = i + 1;
    }

    const gl = this.gl;
    if (!gl) return;
    gl.bindBuffer(gl.ARRAY_BUFFER, this.colorBuffer);
    gl.bufferData(gl.ARRAY_BUFFER, this.colorCache.subarray(0, n * 3), gl.DYNAMIC_DRAW);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.alphaBuffer);
    gl.bufferData(gl.ARRAY_BUFFER, this.alphaCache.subarray(0, n), gl.DYNAMIC_DRAW);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.pickBuffer);
    gl.bufferData(gl.ARRAY_BUFFER, this.pickCache.subarray(0, n), gl.DYNAMIC_DRAW);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.kindBuffer);
    gl.bufferData(gl.ARRAY_BUFFER, this.kindCache.subarray(0, n), gl.DYNAMIC_DRAW);
    this.styleDirty = false;
  }

  markStyleDirty(): void {
    this.styleDirty = true;
  }

  /**
   * 设置"被弱化的节点"集合（视图维度等页面级语义）。
   *
   * 与选中造成的淡化**分开两套状态**，但共用同一个 alpha 通道：
   * 选中淡化是"你选了一个节点，其他都退后"，
   * 弱化是"这个视图维度下这些节点不是重点"。
   * 两者语义不同、生命周期也不同（选中随时会变，透镜不变），
   * 合成到同一个标志位上会互相清除——切换透镜时选中淡化会莫名消失。
   */
  setMutedNodes(nodeIds: Iterable<string>, alpha: number): void {
    this.mutedIds = new Set(nodeIds);
    this.mutedAlpha = Math.max(0, Math.min(1, alpha));
    this.styleDirty = true;
  }

  clearMutedNodes(): void {
    if (this.mutedIds.size === 0) return;
    this.mutedIds.clear();
    this.styleDirty = true;
  }

  get mutedNodeCount(): number {
    return this.mutedIds.size;
  }

  /**
   * 上传"高亮边"（§30.11：只有选中路径 / 播放路径 / 实时重点交易才有粒子与高亮）。
   *
   * 只传被点亮的那几段，而不是给所有边加一个 visible 标记——
   * 后者会让每帧的顶点量仍然是 O(E)，而高亮路径通常只有几十条边。
   */
  uploadHighlight(store: GraphStore, edges: Uint32Array, color: [number, number, number]): void {
    const gl = this.gl;
    if (!gl || this.contextLost) return;
    /*
     * 顶点布局必须与主边缓冲**逐字节一致**：(x, y, r, g, b, a) 共 6 个 float，
     * 因为两者共用同一个着色器（`EDGE_VS` 里 `a_rgba` 是 vec4）。
     *
     * 这里曾经只按 4 个 float 分配并只写了 (x, y, r, g)：
     * 绘制时按 stride 16 / 偏移 8 读 vec4，于是 **蓝色通道与 alpha 读到了下一个顶点的 x/y**。
     * 典型布局里 y 恒为 0，所以整条高亮路径的 alpha 是 0——
     * 高亮边完全不可见，而"高亮边数量""粒子数量"这些断言**全都照常通过**。
     */
    const data = new Float32Array(edges.length * 2 * 8);
    let cursor = 0;
    for (let i = 0; i < edges.length; i += 1) {
      const edge = edges[i];
      const s = store.edgeSource[edge];
      const t = store.edgeTarget[edge];
      const sx = store.positions[s * 2];
      const sy = store.positions[s * 2 + 1];
      const tx = store.positions[t * 2];
      const ty = store.positions[t * 2 + 1];
      const length = s === t ? 0 : Math.hypot(tx - sx, ty - sy);
      const nodes = [s, t];
      for (let k = 0; k < 2; k += 1) {
        data[cursor++] = store.positions[nodes[k] * 2];
        data[cursor++] = store.positions[nodes[k] * 2 + 1];
        data[cursor++] = color[0];
        data[cursor++] = color[1];
        data[cursor++] = color[2];
        /* 高亮边是**不透明**的：它是"资金实际走了哪条链路"的唯一表达，
           半透明会让它在叠加于普通边之上时几乎看不出来。 */
        data[cursor++] = 1;
        data[cursor++] = k;
        data[cursor++] = length;
      }
    }
    this.highlightVertexCount = edges.length * 2;
    gl.bindBuffer(gl.ARRAY_BUFFER, this.highlightBuffer);
    gl.bufferData(gl.ARRAY_BUFFER, data, gl.DYNAMIC_DRAW);

    /* 粒子与高亮边同源：方向来自 edgeSource→edgeTarget，不由调用方决定。 */
    this.particles?.uploadEdges(store.positions, store.edgeSource, store.edgeTarget, edges, this.particleAppearance);
  }

  clearHighlight(): void {
    this.highlightVertexCount = 0;
    this.particles?.clear();
  }

  get highlightedEdges(): number {
    return this.highlightVertexCount / 2;
  }

  get particleCount(): number {
    return this.particles?.count ?? 0;
  }

  resize(cssWidth: number, cssHeight: number, dpr: number): void {
    const canvas = this.canvas;
    const width = Math.max(1, Math.round(cssWidth * dpr));
    const height = Math.max(1, Math.round(cssHeight * dpr));
    if (canvas.width !== width || canvas.height !== height) {
      canvas.width = width;
      canvas.height = height;
    }
  }

  render(
    store: GraphStore,
    camera: Camera,
    cssWidth: number,
    cssHeight: number,
    dpr: number,
    options: {
      /** 粒子时钟（秒）。只在有粒子时才有意义。 */
      timeSeconds?: number;
      /** Reduced Motion 下由调用方传 false（§9.5：graph particle 关闭）。 */
      particlesEnabled?: boolean;
      highlightColor?: [number, number, number];
    } = {},
  ): void {
    const gl = this.gl;
    if (!gl || this.contextLost) return;
    this.resize(cssWidth, cssHeight, dpr);
    if (this.styleDirty) this.refreshStyleCache(store);
    /* 边预算变化时在这里落地重建（每帧最多一次，且只在档位真的变了才发生）。 */
    this.applyPendingEdgeRebuild(store);

    const width = this.canvas.width;
    const height = this.canvas.height;
    gl.viewport(0, 0, width, height);
    gl.disable(gl.DEPTH_TEST);
    gl.enable(gl.BLEND);
    gl.blendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA);
    gl.clearColor(this.style.background[0], this.style.background[1], this.style.background[2], 1);
    gl.clear(gl.COLOR_BUFFER_BIT);

    const matrix = camera.clipMatrix(width, height);

    if (this.edgeVertexCount > 0 && this.edgeProgram) {
      gl.useProgram(this.edgeProgram);
      gl.uniformMatrix3fv(gl.getUniformLocation(this.edgeProgram, "u_matrix"), false, matrix);
      gl.uniform1f(gl.getUniformLocation(this.edgeProgram, "u_scale"), camera.scale);
      this.setDashUniform();
      gl.bindBuffer(gl.ARRAY_BUFFER, this.edgeBuffer);
      const posLoc = gl.getAttribLocation(this.edgeProgram, "a_pos");
      const rgbaLoc = gl.getAttribLocation(this.edgeProgram, "a_rgba");
      const dashLoc = gl.getAttribLocation(this.edgeProgram, "a_dash");
      gl.enableVertexAttribArray(posLoc);
      gl.vertexAttribPointer(posLoc, 2, gl.FLOAT, false, 32, 0);
      gl.enableVertexAttribArray(rgbaLoc);
      gl.vertexAttribPointer(rgbaLoc, 4, gl.FLOAT, false, 32, 8);
      gl.enableVertexAttribArray(dashLoc);
      gl.vertexAttribPointer(dashLoc, 2, gl.FLOAT, false, 32, 24);
      gl.drawArrays(gl.LINES, 0, this.edgeVertexCount);
      gl.disableVertexAttribArray(posLoc);
      gl.disableVertexAttribArray(rgbaLoc);
      gl.disableVertexAttribArray(dashLoc);
    }

    /* 高亮边画在普通边之上、节点之下。 */
    if (this.highlightVertexCount > 0 && this.edgeProgram) {
      gl.useProgram(this.edgeProgram);
      gl.uniformMatrix3fv(gl.getUniformLocation(this.edgeProgram, "u_matrix"), false, matrix);
      gl.uniform1f(gl.getUniformLocation(this.edgeProgram, "u_scale"), camera.scale);
      /*
       * 高亮边**永远实线**，即使当前是证据透镜：
       * 虚线表达的是"证据引用"关系，而高亮表达的是"资金确实走了这条链路"——
       * 把被点亮的路径也打成虚线，等于同时说了两句矛盾的话。
       */
      gl.uniform2f(gl.getUniformLocation(this.edgeProgram, "u_dash"), 0, 0);
      gl.bindBuffer(gl.ARRAY_BUFFER, this.highlightBuffer);
      const hlPos = gl.getAttribLocation(this.edgeProgram, "a_pos");
      const hlRgba = gl.getAttribLocation(this.edgeProgram, "a_rgba");
      const hlDash = gl.getAttribLocation(this.edgeProgram, "a_dash");
      gl.enableVertexAttribArray(hlPos);
      gl.vertexAttribPointer(hlPos, 2, gl.FLOAT, false, 32, 0);
      gl.enableVertexAttribArray(hlRgba);
      gl.vertexAttribPointer(hlRgba, 4, gl.FLOAT, false, 32, 8);
      gl.enableVertexAttribArray(hlDash);
      gl.vertexAttribPointer(hlDash, 2, gl.FLOAT, false, 32, 24);
      gl.drawArrays(gl.LINES, 0, this.highlightVertexCount);
      gl.disableVertexAttribArray(hlPos);
      gl.disableVertexAttribArray(hlRgba);
      gl.disableVertexAttribArray(hlDash);
    }

    /*
     * 粒子：位置完全由顶点着色器按 u_time 插值，CPU 每帧只更新一个 uniform。
     * Reduced Motion 下不绘制——但**已经点亮的边仍然保留**，
     * 状态依然完整可读（§9.5 要求"所有最终状态仍完整显示"）。
     */
    if (options.particlesEnabled !== false && this.particles && this.particles.count > 0) {
      this.particles.draw(camera, width, height, options.timeSeconds ?? 0, this.particleAppearance);
    }

    if (this.instanceCount > 0 && this.nodeProgram) {
      gl.useProgram(this.nodeProgram);
      gl.uniformMatrix3fv(gl.getUniformLocation(this.nodeProgram, "u_matrix"), false, matrix);
      this.bindInstancedAttributes(this.nodeProgram);
      gl.drawArraysInstanced(gl.TRIANGLE_STRIP, 0, 4, this.instanceCount);
      this.unbindInstancedAttributes(this.nodeProgram);
    }

    /*
     * 标签画在节点之上：它是阅读层，被节点压住就失去意义。
     * 位置换算在顶点着色器里做（屏幕尺寸恒定），所以平移/缩放不影响这里提交的数据。
     */
    if (this.labels && this.labels.count > 0) {
      this.labels.draw(camera, width, height, this.labelOpacity);
    }
  }

  /**
   * GPU 拾取（§30.7）。
   *
   * 把节点下标编码成颜色画进一张离屏纹理，然后只读回**一个像素**。
   * 复杂度与节点数无关（一次额外 draw + 1 次 readPixels），
   * 这是"禁止把每个节点变成 DOM hitbox"之后唯一能守住 hover < 8ms 的做法。
   */
  pickNode(store: GraphStore, camera: Camera, cssX: number, cssY: number, dpr: number): number {
    const hit = this.pickAt(store, camera, cssX, cssY, dpr);
    return hit && hit.kind === "node" ? hit.index : -1;
  }

  /**
   * 一次拾取同时判定**节点与边**。
   *
   * 两者共用同一张拾取纹理与同一个 id 空间（1..nodeCount 是节点，
   * 之后是边），所以"点到节点还是边"是一次 readPixels 就能回答的问题。
   * 分开做两次拾取的话，每次鼠标移动都要多一轮离屏绘制。
   */
  pickAt(store: GraphStore, camera: Camera, cssX: number, cssY: number, dpr: number): PickResult {
    const gl = this.gl;
    if (!gl || this.contextLost || this.instanceCount === 0 || !this.pickProgram) return null;

    const width = this.canvas.width;
    const height = this.canvas.height;
    if (this.pickWidth !== width || this.pickHeight !== height) {
      this.disposePickTarget();
      this.pickTexture = gl.createTexture();
      gl.bindTexture(gl.TEXTURE_2D, this.pickTexture);
      gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, width, height, 0, gl.RGBA, gl.UNSIGNED_BYTE, null);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
      this.pickFramebuffer = gl.createFramebuffer();
      gl.bindFramebuffer(gl.FRAMEBUFFER, this.pickFramebuffer);
      gl.framebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, this.pickTexture, 0);
      this.pickWidth = width;
      this.pickHeight = height;
    }
    if (this.styleDirty) this.refreshStyleCache(store);

    gl.bindFramebuffer(gl.FRAMEBUFFER, this.pickFramebuffer);
    gl.viewport(0, 0, width, height);
    gl.disable(gl.BLEND);
    gl.clearColor(0, 0, 0, 1);
    gl.clear(gl.COLOR_BUFFER_BIT);

    const matrix = camera.clipMatrix(width, height);

    /*
     * 先画边、后画节点：重叠处**节点获胜**。
     * 顺序反过来会让一个正好压在节点上的边抢走点击，
     * 而"点节点"是远比"点边"常用的操作。
     */
    if (this.edgePickInstanceCount > 0 && this.pickEdgeProgram && this.edgePickBuffer) {
      gl.useProgram(this.pickEdgeProgram);
      gl.uniformMatrix3fv(gl.getUniformLocation(this.pickEdgeProgram, "u_matrix"), false, matrix);
      /* clipMatrix 用的是**设备像素**视口，所以这里也要换算到设备像素。 */
      gl.uniform1f(gl.getUniformLocation(this.pickEdgeProgram, "u_scale"), camera.scale * dpr);
      gl.uniform1f(gl.getUniformLocation(this.pickEdgeProgram, "u_halfWidth"), WebGLRenderer.EDGE_PICK_HALF_WIDTH_CSS * dpr);

      const quadLoc = gl.getAttribLocation(this.pickEdgeProgram, "a_quad");
      gl.bindBuffer(gl.ARRAY_BUFFER, this.quadBuffer);
      gl.enableVertexAttribArray(quadLoc);
      gl.vertexAttribPointer(quadLoc, 2, gl.FLOAT, false, 0, 0);
      gl.vertexAttribDivisor(quadLoc, 0);

      const stride = 20;
      const fromLoc = gl.getAttribLocation(this.pickEdgeProgram, "a_from");
      const toLoc = gl.getAttribLocation(this.pickEdgeProgram, "a_to");
      const idLoc = gl.getAttribLocation(this.pickEdgeProgram, "a_pickId");
      gl.bindBuffer(gl.ARRAY_BUFFER, this.edgePickBuffer);
      for (const [loc, size, offset] of [
        [fromLoc, 2, 0],
        [toLoc, 2, 8],
        [idLoc, 1, 16],
      ] as Array<[number, number, number]>) {
        gl.enableVertexAttribArray(loc);
        gl.vertexAttribPointer(loc, size, gl.FLOAT, false, stride, offset);
        gl.vertexAttribDivisor(loc, 1);
      }
      gl.drawArraysInstanced(gl.TRIANGLE_STRIP, 0, 4, this.edgePickInstanceCount);
      for (const loc of [quadLoc, fromLoc, toLoc, idLoc]) {
        gl.disableVertexAttribArray(loc);
        gl.vertexAttribDivisor(loc, 0);
      }
    }

    gl.useProgram(this.pickProgram);
    gl.uniformMatrix3fv(gl.getUniformLocation(this.pickProgram, "u_matrix"), false, matrix);
    this.bindInstancedAttributes(this.pickProgram, true);
    gl.drawArraysInstanced(gl.TRIANGLE_STRIP, 0, 4, this.instanceCount);
    this.unbindInstancedAttributes(this.pickProgram, true);

    /* readPixels 原点在左下角，浏览器事件坐标原点在左上角。 */
    const px = Math.round(cssX * dpr);
    const py = Math.round(height - cssY * dpr);
    /*
     * 越界必须显式返回 -1，不能交给 readPixels 决定。
     * 视口外的坐标会让 readPixels 落到未定义区域（不同驱动行为不一致），
     * 而"拖拽时鼠标跑到画布外"是**日常操作**，不能产生随机命中。
     */
    if (px < 0 || py < 0 || px >= width || py >= height) {
      gl.bindFramebuffer(gl.FRAMEBUFFER, null);
      gl.enable(gl.BLEND);
      return null;
    }
    const pixel = new Uint8Array(4);
    gl.readPixels(px, py, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, pixel);
    gl.bindFramebuffer(gl.FRAMEBUFFER, null);
    gl.enable(gl.BLEND);

    const id = pixel[0] + (pixel[1] << 8) + (pixel[2] << 16);
    if (id === 0) return null;
    if (id <= store.nodeCount) return { kind: "node", index: id - 1 };
    return { kind: "edge", index: id - store.nodeCount - 1 };
  }

  private bindInstancedAttributes(program: WebGLProgram, picking = false): void {
    const gl = this.gl;
    if (!gl) return;
    const quadLoc = gl.getAttribLocation(program, "a_quad");
    gl.bindBuffer(gl.ARRAY_BUFFER, this.quadBuffer);
    gl.enableVertexAttribArray(quadLoc);
    gl.vertexAttribPointer(quadLoc, 2, gl.FLOAT, false, 0, 0);
    gl.vertexAttribDivisor(quadLoc, 0);

    const posLoc = gl.getAttribLocation(program, "a_pos");
    gl.bindBuffer(gl.ARRAY_BUFFER, this.posBuffer);
    gl.enableVertexAttribArray(posLoc);
    gl.vertexAttribPointer(posLoc, 2, gl.FLOAT, false, 0, 0);
    gl.vertexAttribDivisor(posLoc, 1);

    const sizeLoc = gl.getAttribLocation(program, "a_size");
    gl.bindBuffer(gl.ARRAY_BUFFER, this.sizeBuffer);
    gl.enableVertexAttribArray(sizeLoc);
    gl.vertexAttribPointer(sizeLoc, 1, gl.FLOAT, false, 0, 0);
    gl.vertexAttribDivisor(sizeLoc, 1);

    const kindLoc = gl.getAttribLocation(program, "a_kind");
    gl.bindBuffer(gl.ARRAY_BUFFER, this.kindBuffer);
    gl.enableVertexAttribArray(kindLoc);
    gl.vertexAttribPointer(kindLoc, 1, gl.FLOAT, false, 0, 0);
    gl.vertexAttribDivisor(kindLoc, 1);

    if (picking) {
      const pickLoc = gl.getAttribLocation(program, "a_pick");
      gl.bindBuffer(gl.ARRAY_BUFFER, this.pickBuffer);
      gl.enableVertexAttribArray(pickLoc);
      /* 整数属性必须走 vertexAttribIPointer，用 float 通道会被归一化而丢位。 */
      gl.vertexAttribIPointer(pickLoc, 1, gl.UNSIGNED_INT, 0, 0);
      gl.vertexAttribDivisor(pickLoc, 1);
    } else {
      const colorLoc = gl.getAttribLocation(program, "a_color");
      gl.bindBuffer(gl.ARRAY_BUFFER, this.colorBuffer);
      gl.enableVertexAttribArray(colorLoc);
      gl.vertexAttribPointer(colorLoc, 3, gl.FLOAT, false, 0, 0);
      gl.vertexAttribDivisor(colorLoc, 1);

      const alphaLoc = gl.getAttribLocation(program, "a_alpha");
      gl.bindBuffer(gl.ARRAY_BUFFER, this.alphaBuffer);
      gl.enableVertexAttribArray(alphaLoc);
      gl.vertexAttribPointer(alphaLoc, 1, gl.FLOAT, false, 0, 0);
      gl.vertexAttribDivisor(alphaLoc, 1);
    }
  }

  private unbindInstancedAttributes(program: WebGLProgram, picking = false): void {
    const gl = this.gl;
    if (!gl) return;
    const names = picking
      ? ["a_quad", "a_pos", "a_size", "a_kind", "a_pick"]
      : ["a_quad", "a_pos", "a_size", "a_kind", "a_color", "a_alpha"];
    for (const name of names) {
      const loc = gl.getAttribLocation(program, name);
      if (loc >= 0) {
        gl.disableVertexAttribArray(loc);
        gl.vertexAttribDivisor(loc, 0);
      }
    }
  }

  private disposePickTarget(): void {
    const gl = this.gl;
    if (!gl) return;
    if (this.pickFramebuffer) gl.deleteFramebuffer(this.pickFramebuffer);
    if (this.pickTexture) gl.deleteTexture(this.pickTexture);
    this.pickFramebuffer = null;
    this.pickTexture = null;
  }

  /** 完整释放（§63C：route unmount 必须 dispose buffer / VAO / texture / framebuffer）。 */
  dispose(): void {
    const gl = this.gl;
    if (!gl) return;
    this.disposePickTarget();
    this.particles?.dispose();
    this.particles = null;
    this.labels?.dispose();
    this.labels = null;
    for (const buffer of [this.quadBuffer, this.posBuffer, this.sizeBuffer, this.colorBuffer, this.alphaBuffer, this.kindBuffer, this.pickBuffer, this.edgeBuffer, this.highlightBuffer, this.edgePickBuffer]) {
      if (buffer) gl.deleteBuffer(buffer);
    }
    this.kindBuffer = null;
    this.highlightBuffer = null;
    if (this.edgePickBuffer) gl.deleteBuffer(this.edgePickBuffer);
    this.edgePickBuffer = null;
    for (const program of [this.nodeProgram, this.pickProgram, this.pickEdgeProgram, this.edgeProgram]) {
      if (program) gl.deleteProgram(program);
    }
    this.pickEdgeProgram = null;
    this.quadBuffer = null;
    this.posBuffer = null;
    this.sizeBuffer = null;
    this.colorBuffer = null;
    this.alphaBuffer = null;
    this.pickBuffer = null;
    this.edgeBuffer = null;
    this.nodeProgram = null;
    this.pickProgram = null;
    this.edgeProgram = null;
    this.gl = null;
  }
}

function buildProgram(gl: WebGL2RenderingContext, vsSource: string, fsSource: string): WebGLProgram {
  const vs = compile(gl, gl.VERTEX_SHADER, vsSource);
  const fs = compile(gl, gl.FRAGMENT_SHADER, fsSource);
  const program = gl.createProgram();
  if (!program) throw new Error("createProgram failed");
  gl.attachShader(program, vs);
  gl.attachShader(program, fs);
  gl.linkProgram(program);
  if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
    const log = gl.getProgramInfoLog(program);
    throw new Error("linkProgram failed: " + log);
  }
  gl.deleteShader(vs);
  gl.deleteShader(fs);
  return program;
}

function compile(gl: WebGL2RenderingContext, type: number, source: string): WebGLShader {
  const shader = gl.createShader(type);
  if (!shader) throw new Error("createShader failed");
  gl.shaderSource(shader, source);
  gl.compileShader(shader);
  if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) {
    const log = gl.getShaderInfoLog(shader);
    gl.deleteShader(shader);
    throw new Error("compileShader failed: " + log);
  }
  return shader;
}
