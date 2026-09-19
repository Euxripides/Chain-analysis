import type { GraphPath } from "../model/paths";

/**
 * 路径播放状态机（用户端重构规范 V4 §30.12 path playback）
 *
 * 播放序列（规范原文）：
 *   起点亮起 → 边逐段点亮 → 下一节点亮起 → Inspector 同步 → Timeline 游标同步
 * 因此"一步"= 点亮一条边 + 点亮它到达的那个节点。`hopIndex` 就是已走过的边数。
 *
 * **时间由外部注入**（`tick(nowMs)`），内部不读 `performance.now()`、不起定时器。
 * 这是刻意的：播放逻辑必须能在不睡眠的测试里逐帧推进，
 * 否则"0.5x 速度下第 3 跳的时机对不对"这种问题只能靠人眼看着猜。
 */

export type PlaybackState = "idle" | "playing" | "paused" | "completed";

/** §30.12 支持的速度档位。 */
export const PLAYBACK_SPEEDS = [0.5, 1, 2] as const;

export interface PlaybackOptions {
  /** 每一跳的基准时长（1x 时）。 */
  hopDurationMs?: number;
  speed?: number;
}

export class PlaybackController {
  state: PlaybackState = "idle";
  path: GraphPath | null = null;
  /** 已走过的边数：0 = 只有起点亮着。 */
  hopIndex = 0;
  speed: number;

  private readonly hopDurationMs: number;
  private hopStartedAt = 0;

  /** 每跳回调：`edgeIndex` 为 -1 表示"起点亮起"这一步。 */
  onHop: ((hopIndex: number, nodeIndex: number, edgeIndex: number) => void) | null = null;
  onComplete: (() => void) | null = null;
  onStateChange: ((state: PlaybackState) => void) | null = null;

  constructor(options: PlaybackOptions = {}) {
    this.hopDurationMs = options.hopDurationMs ?? 620;
    this.speed = options.speed ?? 1;
  }

  get hopCount(): number {
    return this.path ? this.path.edges.length : 0;
  }

  get currentEdgeIndex(): number {
    return this.path && this.hopIndex > 0 ? this.path.edges[this.hopIndex - 1] : -1;
  }

  get currentNodeIndex(): number {
    if (!this.path) return -1;
    return this.path.nodes[Math.min(this.hopIndex, this.path.nodes.length - 1)];
  }

  /** 已点亮的边下标（渲染器据此决定画哪些高亮段与粒子）。 */
  litEdges(): Uint32Array {
    if (!this.path) return new Uint32Array(0);
    return this.path.edges.subarray(0, this.hopIndex);
  }

  /** 已点亮的节点下标。 */
  litNodes(): Uint32Array {
    if (!this.path) return new Uint32Array(0);
    return this.path.nodes.subarray(0, Math.min(this.hopIndex + 1, this.path.nodes.length));
  }

  play(path: GraphPath, nowMs: number): void {
    this.path = path;
    this.hopIndex = 0;
    this.hopStartedAt = nowMs;
    this.setState(path.edges.length === 0 ? "completed" : "playing");
    this.onHop?.(0, this.currentNodeIndex, -1);
    if (path.edges.length === 0) this.onComplete?.();
  }

  pause(): void {
    if (this.state !== "playing") return;
    this.setState("paused");
  }

  resume(nowMs: number): void {
    if (this.state !== "paused") return;
    this.hopStartedAt = nowMs;
    this.setState("playing");
  }

  stop(): void {
    this.path = null;
    this.hopIndex = 0;
    this.setState("idle");
  }

  setSpeed(speed: number): void {
    /* 只接受规范列出的档位，避免出现 0 速（=永远不会完成）这类状态。 */
    this.speed = (PLAYBACK_SPEEDS as readonly number[]).includes(speed) ? speed : 1;
  }

  /**
   * 推进时间。返回**本次是否发生了跳变**——调用方只需在 true 时重绘高亮，
   * 不必每帧比较 hopIndex。
   */
  tick(nowMs: number): boolean {
    if (this.state !== "playing" || !this.path) return false;
    const interval = this.hopDurationMs / this.speed;
    let changed = false;
    while (this.hopIndex < this.path.edges.length && nowMs - this.hopStartedAt >= interval) {
      this.hopIndex += 1;
      this.hopStartedAt += interval;
      changed = true;
      this.onHop?.(this.hopIndex, this.currentNodeIndex, this.currentEdgeIndex);
    }
    if (changed && this.hopIndex >= this.path.edges.length) {
      this.setState("completed");
      this.onComplete?.();
    }
    return changed;
  }

  /** 手动下一跳（§30.12 next hop）：暂停状态下也能用，用于逐步核对。 */
  nextHop(): boolean {
    if (!this.path || this.hopIndex >= this.path.edges.length) return false;
    this.hopIndex += 1;
    this.onHop?.(this.hopIndex, this.currentNodeIndex, this.currentEdgeIndex);
    if (this.hopIndex >= this.path.edges.length) {
      this.setState("completed");
      this.onComplete?.();
    }
    return true;
  }

  /** 手动上一跳。 */
  previousHop(): boolean {
    if (!this.path || this.hopIndex <= 0) return false;
    this.hopIndex -= 1;
    this.onHop?.(this.hopIndex, this.currentNodeIndex, this.currentEdgeIndex);
    if (this.state === "completed") this.setState("paused");
    return true;
  }

  private setState(next: PlaybackState): void {
    if (this.state === next) return;
    this.state = next;
    this.onStateChange?.(next);
  }
}
