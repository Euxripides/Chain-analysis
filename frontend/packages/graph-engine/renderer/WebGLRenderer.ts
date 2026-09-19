import { Camera } from "../interaction/camera";
import { NODE_FLAG, type GraphStore } from "../model/GraphStore";
import { DEFAULT_PARTICLE_APPEARANCE, ParticleProgram, type ParticleAppearance } from "./ParticleProgram";

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

const EDGE_VS = `#version 300 es
in vec2 a_pos;
in vec4 a_rgba;
uniform mat3 u_matrix;
out vec4 v_rgba;
void main() {
  v_rgba = a_rgba;
  vec3 clip = u_matrix * vec3(a_pos, 1.0);
  gl_Position = vec4(clip.xy, 0.0, 1.0);
}`;

const EDGE_FS = `#version 300 es
precision highp float;
in vec4 v_rgba;
out vec4 outColor;
void main() {
  outColor = v_rgba;
}`;

export interface RenderStyle {
  /** 每种节点类型的填充色，[r,g,b] 0–1。索引与 NODE_KIND 对齐。 */
  kindColors: Array<[number, number, number]>;
  edgeColor: [number, number, number];
  edgeActiveColor: [number, number, number];
  background: [number, number, number];
  dimmedAlpha: number;
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

  private pickFramebuffer: WebGLFramebuffer | null = null;
  private pickTexture: WebGLTexture | null = null;
  private pickWidth = 0;
  private pickHeight = 0;

  /** 高亮边（选中路径 / 播放路径）：单独一个 buffer，只放被点亮的那几段。 */
  private highlightBuffer: WebGLBuffer | null = null;
  private highlightVertexCount = 0;
  private particles: ParticleProgram | null = null;
  private particleAppearance: ParticleAppearance = DEFAULT_PARTICLE_APPEARANCE;

  private instanceCount = 0;
  private edgeVertexCount = 0;
  private styleDirty = true;

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
  }

  private initGL(): void {
    const gl = this.gl;
    if (!gl) return;
    this.nodeProgram = buildProgram(gl, NODE_VS, NODE_FS);
    this.pickProgram = buildProgram(gl, PICK_VS, PICK_FS);
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

  /** 上传图数据（几何 + 每节点实例属性）。 */
  upload(store: GraphStore): void {
    const gl = this.gl;
    if (!gl || this.contextLost) return;

    gl.bindBuffer(gl.ARRAY_BUFFER, this.posBuffer);
    gl.bufferData(gl.ARRAY_BUFFER, store.positions, gl.DYNAMIC_DRAW);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.sizeBuffer);
    gl.bufferData(gl.ARRAY_BUFFER, store.sizes, gl.DYNAMIC_DRAW);
    this.instanceCount = store.nodeCount;

    const edgeData = new Float32Array(store.edgeCount * 2 * 6);
    for (let e = 0; e < store.edgeCount; e += 1) {
      const s = store.edgeSource[e];
      const t = store.edgeTarget[e];
      const base = e * 12;
      edgeData[base] = store.positions[s * 2];
      edgeData[base + 1] = store.positions[s * 2 + 1];
      edgeData[base + 2] = this.style.edgeColor[0];
      edgeData[base + 3] = this.style.edgeColor[1];
      edgeData[base + 4] = this.style.edgeColor[2];
      edgeData[base + 5] = store.edgeSource[e] === t ? 0 : 0.5;
      edgeData[base + 6] = store.positions[t * 2];
      edgeData[base + 7] = store.positions[t * 2 + 1];
      edgeData[base + 8] = this.style.edgeColor[0];
      edgeData[base + 9] = this.style.edgeColor[1];
      edgeData[base + 10] = this.style.edgeColor[2];
      edgeData[base + 11] = store.edgeSource[e] === t ? 0 : 0.5;
    }
    gl.bindBuffer(gl.ARRAY_BUFFER, this.edgeBuffer);
    gl.bufferData(gl.ARRAY_BUFFER, edgeData, gl.DYNAMIC_DRAW);
    this.edgeVertexCount = store.edgeCount * 2;

    this.styleDirty = true;
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
      this.alphaCache[i] = flags & NODE_FLAG.dimmed ? this.style.dimmedAlpha : 1;
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
   * 上传"高亮边"（§30.11：只有选中路径 / 播放路径 / 实时重点交易才有粒子与高亮）。
   *
   * 只传被点亮的那几段，而不是给所有边加一个 visible 标记——
   * 后者会让每帧的顶点量仍然是 O(E)，而高亮路径通常只有几十条边。
   */
  uploadHighlight(store: GraphStore, edges: Uint32Array, color: [number, number, number]): void {
    const gl = this.gl;
    if (!gl || this.contextLost) return;
    const data = new Float32Array(edges.length * 2 * 4);
    let cursor = 0;
    for (let i = 0; i < edges.length; i += 1) {
      const edge = edges[i];
      const s = store.edgeSource[edge];
      const t = store.edgeTarget[edge];
      for (const node of [s, t]) {
        data[cursor++] = store.positions[node * 2];
        data[cursor++] = store.positions[node * 2 + 1];
        data[cursor++] = color[0];
        data[cursor++] = color[1];
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
      gl.bindBuffer(gl.ARRAY_BUFFER, this.edgeBuffer);
      const posLoc = gl.getAttribLocation(this.edgeProgram, "a_pos");
      const rgbaLoc = gl.getAttribLocation(this.edgeProgram, "a_rgba");
      gl.enableVertexAttribArray(posLoc);
      gl.vertexAttribPointer(posLoc, 2, gl.FLOAT, false, 24, 0);
      gl.enableVertexAttribArray(rgbaLoc);
      gl.vertexAttribPointer(rgbaLoc, 4, gl.FLOAT, false, 24, 8);
      gl.drawArrays(gl.LINES, 0, this.edgeVertexCount);
      gl.disableVertexAttribArray(posLoc);
      gl.disableVertexAttribArray(rgbaLoc);
    }

    /* 高亮边画在普通边之上、节点之下。 */
    if (this.highlightVertexCount > 0 && this.edgeProgram) {
      gl.useProgram(this.edgeProgram);
      gl.uniformMatrix3fv(gl.getUniformLocation(this.edgeProgram, "u_matrix"), false, matrix);
      gl.bindBuffer(gl.ARRAY_BUFFER, this.highlightBuffer);
      const hlPos = gl.getAttribLocation(this.edgeProgram, "a_pos");
      const hlRgba = gl.getAttribLocation(this.edgeProgram, "a_rgba");
      gl.enableVertexAttribArray(hlPos);
      gl.vertexAttribPointer(hlPos, 2, gl.FLOAT, false, 16, 0);
      gl.enableVertexAttribArray(hlRgba);
      gl.vertexAttribPointer(hlRgba, 4, gl.FLOAT, false, 16, 8);
      gl.drawArrays(gl.LINES, 0, this.highlightVertexCount);
      gl.disableVertexAttribArray(hlPos);
      gl.disableVertexAttribArray(hlRgba);
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
  }

  /**
   * GPU 拾取（§30.7）。
   *
   * 把节点下标编码成颜色画进一张离屏纹理，然后只读回**一个像素**。
   * 复杂度与节点数无关（一次额外 draw + 1 次 readPixels），
   * 这是"禁止把每个节点变成 DOM hitbox"之后唯一能守住 hover < 8ms 的做法。
   */
  pickNode(store: GraphStore, camera: Camera, cssX: number, cssY: number, dpr: number): number {
    const gl = this.gl;
    if (!gl || this.contextLost || this.instanceCount === 0 || !this.pickProgram) return -1;

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

    gl.useProgram(this.pickProgram);
    gl.uniformMatrix3fv(gl.getUniformLocation(this.pickProgram, "u_matrix"), false, camera.clipMatrix(width, height));
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
      return -1;
    }
    const pixel = new Uint8Array(4);
    gl.readPixels(px, py, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, pixel);
    gl.bindFramebuffer(gl.FRAMEBUFFER, null);
    gl.enable(gl.BLEND);

    const id = pixel[0] + (pixel[1] << 8) + (pixel[2] << 16);
    return id === 0 ? -1 : id - 1;
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
    for (const buffer of [this.quadBuffer, this.posBuffer, this.sizeBuffer, this.colorBuffer, this.alphaBuffer, this.kindBuffer, this.pickBuffer, this.edgeBuffer, this.highlightBuffer]) {
      if (buffer) gl.deleteBuffer(buffer);
    }
    this.kindBuffer = null;
    this.highlightBuffer = null;
    for (const program of [this.nodeProgram, this.pickProgram, this.edgeProgram]) {
      if (program) gl.deleteProgram(program);
    }
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
