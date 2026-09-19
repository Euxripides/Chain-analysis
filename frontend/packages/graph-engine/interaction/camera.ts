/**
 * 相机（用户端重构规范 V4 §30.2 `interaction/camera` / §30.13）
 *
 * 纯数学，不接触 DOM 与 WebGL：屏幕坐标 ⇄ 世界坐标的换算必须能被单独测试，
 * 因为 picking（§30.7）与 hover 命中都依赖它，算错会让"鼠标指着的节点"和
 * "实际选中的节点"错位——那种缺陷在视觉上很难发现。
 *
 * 变换定义：`screen = (world - center) * scale + viewport / 2`
 * 即 (cx, cy) 是视口中心对应的世界坐标。
 */
export interface CameraState {
  /** 视口中心对应的世界坐标 X。 */
  cx: number;
  /** 视口中心对应的世界坐标 Y。 */
  cy: number;
  /** 每世界单位对应的 CSS 像素数。 */
  scale: number;
}

export const MIN_SCALE = 0.02;
export const MAX_SCALE = 40;

export class Camera {
  state: CameraState = { cx: 0, cy: 0, scale: 1 };

  constructor(initial?: Partial<CameraState>) {
    if (initial) this.state = { ...this.state, ...initial };
  }

  get scale(): number {
    return this.state.scale;
  }

  worldToScreen(x: number, y: number, width: number, height: number): [number, number] {
    const { cx, cy, scale } = this.state;
    return [(x - cx) * scale + width / 2, (y - cy) * scale + height / 2];
  }

  screenToWorld(sx: number, sy: number, width: number, height: number): [number, number] {
    const { cx, cy, scale } = this.state;
    return [(sx - width / 2) / scale + cx, (sy - height / 2) / scale + cy];
  }

  /** 平移：dx/dy 是**屏幕像素**位移（拖拽手势直接用这个）。 */
  pan(dxScreen: number, dyScreen: number): void {
    const { scale } = this.state;
    this.state.cx -= dxScreen / scale;
    this.state.cy -= dyScreen / scale;
  }

  /** 以某个屏幕点为锚点缩放（滚轮缩放就是这一条）：锚点下的世界坐标保持不动。 */
  zoomAt(sx: number, sy: number, factor: number, width: number, height: number): void {
    const [beforeX, beforeY] = this.screenToWorld(sx, sy, width, height);
    this.state.scale = clamp(this.state.scale * factor, MIN_SCALE, MAX_SCALE);
    const [afterX, afterY] = this.screenToWorld(sx, sy, width, height);
    this.state.cx += beforeX - afterX;
    this.state.cy += beforeY - afterY;
  }

  /** 把世界包围盒铺满视口（§30.13 fit all / fit selection）。 */
  fit(bounds: Bounds, width: number, height: number, padding = 32): void {
    const w = Math.max(bounds.maxX - bounds.minX, 1e-6);
    const h = Math.max(bounds.maxY - bounds.minY, 1e-6);
    const scale = clamp(
      Math.min((width - padding * 2) / w, (height - padding * 2) / h),
      MIN_SCALE,
      MAX_SCALE,
    );
    this.state.scale = scale;
    this.state.cx = (bounds.minX + bounds.maxX) / 2;
    this.state.cy = (bounds.minY + bounds.maxY) / 2;
  }

  /**
   * 列主序 3×3 变换矩阵，直接把世界坐标乘成**裁剪空间**坐标。
   *
   * 只做 2D 仿射，所以第三行恒为 (0,0,1)；着色器里用一个 mat3 就够。
   */
  clipMatrix(width: number, height: number, out = new Float32Array(9)): Float32Array {
    const { cx, cy, scale } = this.state;
    const sx = (2 * scale) / width;
    const sy = (-2 * scale) / height;
    const tx = (-cx * 2 * scale) / width;
    const ty = (cy * 2 * scale) / height;
    out[0] = sx; out[1] = 0; out[2] = 0;
    out[3] = 0; out[4] = sy; out[5] = 0;
    out[6] = tx; out[7] = ty; out[8] = 1;
    return out;
  }
}

export interface Bounds {
  minX: number;
  minY: number;
  maxX: number;
  maxY: number;
}

export function emptyBounds(): Bounds {
  return { minX: Infinity, minY: Infinity, maxX: -Infinity, maxY: -Infinity };
}

function clamp(value: number, min: number, max: number): number {
  return value < min ? min : value > max ? max : value;
}
