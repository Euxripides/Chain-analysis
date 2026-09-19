import type { Camera } from "../interaction/camera";

/**
 * 标签绘制（用户端重构规范 V4 §30.2 `LabelProgram` / §30.8）
 *
 * 三条设计约束：
 *   1. **标签在屏幕空间的尺寸恒定**（不随缩放变大变小），因此在顶点着色器里
 *      用 `世界尺寸 = 像素尺寸 / u_scale` 换算，而不是在 CPU 上逐帧重算世界坐标；
 *   2. **只画受控窗口内的标签**（§55.2 的精神）：中景/近景由 LOD 策略决定上限，
 *      远景一个都不画——十万节点全画标签会直接把填充率打满；
 *   3. 标签是**增强通道**：它不可用时图谱照常可读（节点形状与颜色已经承载了信息）。
 */

/*
 * 坐标约定（**必须与 camera 保持一致，这里的 y 轴是向下的**）：
 *   `worldToScreen` 与 `clipMatrix` 都满足「世界 y 增大 = 屏幕向下」，
 *   所以实例里的屏幕像素偏移**正值向下**，行偏移不需要在 CPU 侧取反。
 *
 * 标签的横向位置在**着色器里**算，而不是 CPU：
 *   左边缘 = 节点中心 + 节点屏幕半径 + 间隙
 * 节点半径是世界单位、随缩放变化，若在 CPU 上换算，任何一次缩放
 * （在两个重建阈值之间）都会让标签与节点错位。
 */
const LABEL_VS = `#version 300 es
in vec2 a_quad;         // -1..1 单位四边形
in vec2 a_anchor;       // 节点中心的世界坐标
in float a_radius;      // 节点的世界半径
in float a_gapPx;       // 节点边缘到文字左边缘的像素间隙
in float a_rowPx;       // 行偏移（像素，正值向下）
in vec4 a_uv;           // 图集 uv：xy = 起点角，zw = 跨度
in vec2 a_sizePx;       // 文字在屏幕上的像素尺寸
uniform mat3 u_matrix;
uniform float u_scale;  // 世界单位 → CSS 像素
out vec2 v_uv;
void main() {
  vec2 halfSize = a_sizePx / u_scale * 0.5;
  /* 左对齐：每一行的左边缘都贴在同一个 x 上，多行标签看起来是一个文本块。 */
  float left = a_radius + a_gapPx / u_scale;
  vec2 world = a_anchor + vec2(left + halfSize.x, a_rowPx / u_scale) + a_quad * halfSize;
  vec3 clip = u_matrix * vec3(world, 1.0);
  gl_Position = vec4(clip.xy, 0.0, 1.0);
  /* a_quad.y = -1 是**屏幕上方**（世界 y 更小），所以起点角取文字顶部。 */
  v_uv = a_uv.xy + (a_quad * 0.5 + 0.5) * a_uv.zw;
}`;

const LABEL_FS = `#version 300 es
precision highp float;
in vec2 v_uv;
uniform sampler2D u_atlas;
uniform float u_opacity;
out vec4 outColor;
void main() {
  vec4 texel = texture(u_atlas, v_uv);
  /* 图集是"白色文字 + alpha"：颜色由 u_atlas 自身的 RGB 提供，这里只调制透明度。 */
  if (texel.a < 0.05) discard;
  outColor = vec4(texel.rgb, texel.a * u_opacity);
}`;

export interface LabelInstance {
  /** 节点中心的世界坐标（注意：世界 y 增大 = 屏幕向下）。 */
  x: number;
  y: number;
  /** 节点的世界半径；文字贴在它的右侧。 */
  anchorRadius: number;
  /** 节点边缘到文字左边缘的像素间隙。 */
  gapPx: number;
  /** 行偏移（像素，正值向下）。第一行 0，第二行 +行高。 */
  rowPx: number;
  /** 文字在屏幕上的像素尺寸。 */
  widthPx: number;
  heightPx: number;
  /** 图集 uv：`(u0, v0)` 对应文字**左上角**，`(u1, v1)` 对应右下角。 */
  u0: number;
  v0: number;
  u1: number;
  v1: number;
}

/** 单个实例的数据量：2(anchor)+1(radius)+1(gap)+1(row)+4(uv)+2(size) = 11 floats。 */
const FLOATS_PER_INSTANCE = 11;

export class LabelProgram {
  private gl: WebGL2RenderingContext;
  private program: WebGLProgram;
  private quadBuffer: WebGLBuffer | null = null;
  private instanceBuffer: WebGLBuffer | null = null;
  private texture: WebGLTexture | null = null;
  private instanceCount = 0;

  constructor(gl: WebGL2RenderingContext) {
    this.gl = gl;
    this.program = build(gl, LABEL_VS, LABEL_FS);
    this.quadBuffer = gl.createBuffer();
    gl.bindBuffer(gl.ARRAY_BUFFER, this.quadBuffer);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 1, -1, -1, 1, 1, 1]), gl.STATIC_DRAW);
    this.instanceBuffer = gl.createBuffer();
    this.texture = gl.createTexture();
  }

  /** 上传图集纹理。整张重传：只有标签集合变化时才会调用，不在每帧。 */
  uploadAtlas(source: HTMLCanvasElement): void {
    const gl = this.gl;
    if (!this.texture) this.texture = gl.createTexture();
    gl.bindTexture(gl.TEXTURE_2D, this.texture);
    /* UNPACK_FLIP_Y 让 uv 的 v 轴与 canvas 的 y 轴同向，省掉 CPU 侧的翻转换算。 */
    gl.pixelStorei(gl.UNPACK_FLIP_Y_WEBGL, 1);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, source);
    gl.pixelStorei(gl.UNPACK_FLIP_Y_WEBGL, 0);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
  }

  upload(labels: LabelInstance[]): void {
    const gl = this.gl;
    this.instanceCount = labels.length;
    if (labels.length === 0) return;
    const data = new Float32Array(labels.length * FLOATS_PER_INSTANCE);
    let cursor = 0;
    for (const label of labels) {
      data[cursor++] = label.x;
      data[cursor++] = label.y;
      data[cursor++] = label.anchorRadius;
      data[cursor++] = label.gapPx;
      data[cursor++] = label.rowPx;
      data[cursor++] = label.u0;
      data[cursor++] = label.v0;
      data[cursor++] = label.u1 - label.u0;
      data[cursor++] = label.v1 - label.v0;
      data[cursor++] = label.widthPx;
      data[cursor++] = label.heightPx;
    }
    gl.bindBuffer(gl.ARRAY_BUFFER, this.instanceBuffer);
    gl.bufferData(gl.ARRAY_BUFFER, data, gl.DYNAMIC_DRAW);
  }

  clear(): void {
    this.instanceCount = 0;
  }

  get count(): number {
    return this.instanceCount;
  }

  draw(camera: Camera, width: number, height: number, opacity = 1): void {
    const gl = this.gl;
    if (this.instanceCount === 0 || !this.texture) return;
    gl.useProgram(this.program);
    gl.uniformMatrix3fv(gl.getUniformLocation(this.program, "u_matrix"), false, camera.clipMatrix(width, height));
    gl.uniform1f(gl.getUniformLocation(this.program, "u_scale"), camera.scale);
    gl.uniform1f(gl.getUniformLocation(this.program, "u_opacity"), opacity);
    gl.activeTexture(gl.TEXTURE0);
    gl.bindTexture(gl.TEXTURE_2D, this.texture);
    gl.uniform1i(gl.getUniformLocation(this.program, "u_atlas"), 0);

    const quadLoc = gl.getAttribLocation(this.program, "a_quad");
    gl.bindBuffer(gl.ARRAY_BUFFER, this.quadBuffer);
    gl.enableVertexAttribArray(quadLoc);
    gl.vertexAttribPointer(quadLoc, 2, gl.FLOAT, false, 0, 0);
    gl.vertexAttribDivisor(quadLoc, 0);

    const stride = FLOATS_PER_INSTANCE * 4;
    const attach = (name: string, size: number, offsetFloats: number) => {
      const loc = gl.getAttribLocation(this.program, name);
      gl.enableVertexAttribArray(loc);
      gl.vertexAttribPointer(loc, size, gl.FLOAT, false, stride, offsetFloats * 4);
      gl.vertexAttribDivisor(loc, 1);
      return loc;
    };
    gl.bindBuffer(gl.ARRAY_BUFFER, this.instanceBuffer);
    const locs = [
      attach("a_anchor", 2, 0),
      attach("a_radius", 1, 2),
      attach("a_gapPx", 1, 3),
      attach("a_rowPx", 1, 4),
      attach("a_uv", 4, 5),
      attach("a_sizePx", 2, 9),
    ];

    gl.drawArraysInstanced(gl.TRIANGLE_STRIP, 0, 4, this.instanceCount);

    for (const loc of [quadLoc, ...locs]) {
      gl.disableVertexAttribArray(loc);
      gl.vertexAttribDivisor(loc, 0);
    }
  }

  dispose(): void {
    const gl = this.gl;
    if (this.quadBuffer) gl.deleteBuffer(this.quadBuffer);
    if (this.instanceBuffer) gl.deleteBuffer(this.instanceBuffer);
    if (this.texture) gl.deleteTexture(this.texture);
    gl.deleteProgram(this.program);
    this.quadBuffer = null;
    this.instanceBuffer = null;
    this.texture = null;
    this.instanceCount = 0;
  }
}

function build(gl: WebGL2RenderingContext, vsSource: string, fsSource: string): WebGLProgram {
  const compile = (type: number, source: string) => {
    const shader = gl.createShader(type);
    if (!shader) throw new Error("createShader failed");
    gl.shaderSource(shader, source);
    gl.compileShader(shader);
    if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) {
      const log = gl.getShaderInfoLog(shader);
      gl.deleteShader(shader);
      throw new Error("label compileShader failed: " + log);
    }
    return shader;
  };
  const vs = compile(gl.VERTEX_SHADER, vsSource);
  const fs = compile(gl.FRAGMENT_SHADER, fsSource);
  const program = gl.createProgram();
  if (!program) throw new Error("createProgram failed");
  gl.attachShader(program, vs);
  gl.attachShader(program, fs);
  gl.linkProgram(program);
  if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
    throw new Error("label linkProgram failed: " + gl.getProgramInfoLog(program));
  }
  gl.deleteShader(vs);
  gl.deleteShader(fs);
  return program;
}
