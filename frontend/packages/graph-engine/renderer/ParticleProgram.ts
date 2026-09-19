import type { Camera } from "../interaction/camera";

/**
 * 边上的资金流动粒子（用户端重构规范 V4 §30.11 / §30.2 ParticleProgram）
 *
 * 规范的两条硬约束：
 *   1. **绝不能对全部边播放粒子**——只有"选中的资金路径 / 正在播放的路径 /
 *      实时新增的重点交易"才有；
 *   2. **粒子方向必须与资金方向一致**。
 *
 * 实现要点：粒子的位置**完全在顶点着色器里算**——
 * 每个实例只带 (起点, 终点, 相位)，着色器做 `mix(src, dst, fract(phase + time * speed))`。
 * 因此每帧 CPU 只更新一个 `u_time` uniform，不需要逐粒子重写 buffer。
 * 这是"React 不逐帧驱动图谱"（§9.6 / §30.1）在粒子这一层上的同一原则。
 */

const PARTICLE_VS = `#version 300 es
in vec2 a_quad;
/* 每实例一段：src.xy + dst.xy。方向由 src→dst 的书写顺序决定。 */
in vec4 a_seg;
in float a_phase;
uniform mat3 u_matrix;
uniform float u_time;
uniform float u_speed;
uniform float u_size;
out vec2 v_local;
out float v_alpha;
void main() {
  v_local = a_quad;
  float t = fract(a_phase + u_time * u_speed);
  vec2 world = mix(a_seg.xy, a_seg.zw, t) + a_quad * u_size;
  vec3 clip = u_matrix * vec3(world, 1.0);
  gl_Position = vec4(clip.xy, 0.0, 1.0);
  /* 两端淡出：粒子在节点上突然出现/消失会比流动本身更显眼。 */
  v_alpha = smoothstep(0.0, 0.15, t) * smoothstep(1.0, 0.85, t);
}`;

const PARTICLE_FS = `#version 300 es
precision highp float;
in vec2 v_local;
in float v_alpha;
uniform vec3 u_color;
out vec4 outColor;
void main() {
  float d = length(v_local);
  if (d > 1.0) discard;
  outColor = vec4(u_color, v_alpha * 0.9);
}`;

export interface ParticleAppearance {
  color: [number, number, number];
  /** 粒子半径（世界单位）。 */
  size: number;
  /** 每个实例每秒走过的比例（1 = 每秒走完整条边）。 */
  speed: number;
  /** 每条高亮边放几个粒子。 */
  perEdge: number;
}

export const DEFAULT_PARTICLE_APPEARANCE: ParticleAppearance = {
  color: [0.078, 0.443, 0.569],
  size: 2.4,
  speed: 0.55,
  perEdge: 3,
};

export class ParticleProgram {
  private gl: WebGL2RenderingContext;
  private program: WebGLProgram;
  private quadBuffer: WebGLBuffer | null = null;
  private segmentBuffer: WebGLBuffer | null = null;
  private instanceCount = 0;

  constructor(gl: WebGL2RenderingContext) {
    this.gl = gl;
    this.program = build(gl, PARTICLE_VS, PARTICLE_FS);
    this.quadBuffer = gl.createBuffer();
    gl.bindBuffer(gl.ARRAY_BUFFER, this.quadBuffer);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 1, -1, -1, 1, 1, 1]), gl.STATIC_DRAW);
    this.segmentBuffer = gl.createBuffer();
  }

  /**
   * 用**已点亮的那些边**重建粒子实例。
   *
   * 只在路径/选择变化时调用，不在每帧调用——每帧只更新 `u_time`。
   * `direction` 来自边自身的 source→target，因此粒子方向与资金方向天然一致；
   * 调用方不能反转它，否则就违反了 §30.11 的第二条。
   */
  uploadEdges(positions: Float32Array, edgeSource: Uint32Array, edgeTarget: Uint32Array, edges: Uint32Array, appearance: ParticleAppearance): void {
    const gl = this.gl;
    const perEdge = Math.max(1, Math.floor(appearance.perEdge));
    const data = new Float32Array(edges.length * perEdge * 5);
    let cursor = 0;
    for (let i = 0; i < edges.length; i += 1) {
      const edge = edges[i];
      const s = edgeSource[edge];
      const t = edgeTarget[edge];
      for (let p = 0; p < perEdge; p += 1) {
        data[cursor++] = positions[s * 2];
        data[cursor++] = positions[s * 2 + 1];
        data[cursor++] = positions[t * 2];
        data[cursor++] = positions[t * 2 + 1];
        /* 相位按实例错开，粒子才是"一串"而不是一个。 */
        data[cursor++] = p / perEdge;
      }
    }
    this.instanceCount = edges.length * perEdge;
    if (!this.segmentBuffer) this.segmentBuffer = gl.createBuffer();
    gl.bindBuffer(gl.ARRAY_BUFFER, this.segmentBuffer);
    gl.bufferData(gl.ARRAY_BUFFER, data, gl.DYNAMIC_DRAW);
  }

  clear(): void {
    this.instanceCount = 0;
  }

  get count(): number {
    return this.instanceCount;
  }

  draw(camera: Camera, width: number, height: number, timeSeconds: number, appearance: ParticleAppearance): void {
    const gl = this.gl;
    if (this.instanceCount === 0) return;
    gl.useProgram(this.program);
    gl.uniformMatrix3fv(gl.getUniformLocation(this.program, "u_matrix"), false, camera.clipMatrix(width, height));
    gl.uniform1f(gl.getUniformLocation(this.program, "u_time"), timeSeconds);
    gl.uniform1f(gl.getUniformLocation(this.program, "u_speed"), appearance.speed);
    gl.uniform1f(gl.getUniformLocation(this.program, "u_size"), appearance.size);
    gl.uniform3fv(gl.getUniformLocation(this.program, "u_color"), appearance.color);

    const quadLoc = gl.getAttribLocation(this.program, "a_quad");
    gl.bindBuffer(gl.ARRAY_BUFFER, this.quadBuffer);
    gl.enableVertexAttribArray(quadLoc);
    gl.vertexAttribPointer(quadLoc, 2, gl.FLOAT, false, 0, 0);
    gl.vertexAttribDivisor(quadLoc, 0);

    const segLoc = gl.getAttribLocation(this.program, "a_seg");
    gl.bindBuffer(gl.ARRAY_BUFFER, this.segmentBuffer);
    gl.enableVertexAttribArray(segLoc);
    gl.vertexAttribPointer(segLoc, 4, gl.FLOAT, false, 20, 0);
    gl.vertexAttribDivisor(segLoc, 1);

    const phaseLoc = gl.getAttribLocation(this.program, "a_phase");
    gl.enableVertexAttribArray(phaseLoc);
    gl.vertexAttribPointer(phaseLoc, 1, gl.FLOAT, false, 20, 16);
    gl.vertexAttribDivisor(phaseLoc, 1);

    gl.drawArraysInstanced(gl.TRIANGLE_STRIP, 0, 4, this.instanceCount);

    gl.disableVertexAttribArray(quadLoc);
    gl.disableVertexAttribArray(segLoc);
    gl.disableVertexAttribArray(phaseLoc);
    gl.vertexAttribDivisor(quadLoc, 0);
    gl.vertexAttribDivisor(segLoc, 0);
    gl.vertexAttribDivisor(phaseLoc, 0);
  }

  dispose(): void {
    const gl = this.gl;
    if (this.quadBuffer) gl.deleteBuffer(this.quadBuffer);
    if (this.segmentBuffer) gl.deleteBuffer(this.segmentBuffer);
    gl.deleteProgram(this.program);
    this.quadBuffer = null;
    this.segmentBuffer = null;
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
      throw new Error("compileShader failed: " + log);
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
    throw new Error("linkProgram failed: " + gl.getProgramInfoLog(program));
  }
  gl.deleteShader(vs);
  gl.deleteShader(fs);
  return program;
}
