/**
 * 标签字形图集（用户端重构规范 V4 §30.2 `LabelProgram` 的配套）
 *
 * 为什么不用 DOM 覆盖层画标签：规范 §30.1 明确"React 只渲染 toolbar / filter /
 * inspector / timeline / menus / canvas container"，节点层不得进 DOM。
 * 标签属于节点层，所以必须在 GPU 上画。
 *
 * 为什么是"按需栅格化"而不是预建 ASCII 图集：
 * 节点标签里有中文实体名与任意长度的地址短值，预建图集覆盖不了；
 * 但**可见标签的数量是有上限的**（LOD 只在中景/近景显示，且只给重要节点），
 * 所以每次标签集合变化时重新栅格化一小批字符串，代价可控。
 *
 * 图集用**货架打包**（shelf packing）：按行摆放，行高取该行最高的字形。
 * 比真正的矩形打包浪费一些空间，但对"几百个短字符串"这个规模足够，
 * 而且实现只有几十行、没有需要维护的复杂状态。
 */

export interface GlyphBox {
  /** 图集内的像素矩形。 */
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface AtlasStats {
  width: number;
  height: number;
  /** 本次打包进去的字符串数。 */
  glyphs: number;
  /** 因为放不下而被丢弃的字符串数——**必须暴露**，否则"标签莫名少了一些"无从排查。 */
  dropped: number;
  usedRowsHeight: number;
}

export class LabelAtlas {
  readonly canvas: HTMLCanvasElement;
  private readonly ctx: CanvasRenderingContext2D;
  private cursorX = 0;
  private cursorY = 0;
  private rowHeight = 0;
  private glyphCount = 0;
  private dropped = 0;

  constructor(
    readonly width = 2048,
    readonly height = 512,
    /** 设备像素比；文本按 DPR 放大栅格化，缩回去才清晰。 */
    readonly dpr = 2,
    private readonly font = '600 24px "IBM Plex Sans", "Noto Sans SC", system-ui, sans-serif',
  ) {
    this.canvas = document.createElement("canvas");
    this.canvas.width = width;
    this.canvas.height = height;
    const ctx = this.canvas.getContext("2d", { willReadFrequently: false });
    if (!ctx) throw new Error("label atlas: 2d context unavailable");
    this.ctx = ctx;
    this.ctx.font = this.scaledFont();
    this.ctx.textBaseline = "top";
  }

  private scaledFont(): string {
    return this.font.replace(/(\d+)px/, (_, px: string) => `${Number(px) * this.dpr}px`);
  }

  /** 开始一批标签：清空图集并复位打包游标。 */
  begin(color: string): void {
    this.ctx.clearRect(0, 0, this.width, this.height);
    this.ctx.font = this.scaledFont();
    this.ctx.fillStyle = color;
    this.ctx.textBaseline = "top";
    this.cursorX = 0;
    this.cursorY = 0;
    this.rowHeight = 0;
    this.glyphCount = 0;
    this.dropped = 0;
  }

  /**
   * 把一个字符串放进图集。放不下返回 null——调用方据此决定是丢弃这个标签
   * 还是重开一张更大的图集，而不是静默少画一个。
   */
  add(text: string, padding = 4): GlyphBox | null {
    const metrics = this.ctx.measureText(text);
    const width = Math.ceil(metrics.width) + padding * 2;
    const height = Math.ceil((metrics.actualBoundingBoxAscent || 20 * this.dpr) + (metrics.actualBoundingBoxDescent || 6 * this.dpr)) + padding * 2;

    if (this.cursorX + width > this.width) {
      /* 换行（货架打包：一行放满就开新行）。 */
      this.cursorX = 0;
      this.cursorY += this.rowHeight;
      this.rowHeight = 0;
    }
    if (this.cursorY + height > this.height) {
      this.dropped += 1;
      return null;
    }
    const box: GlyphBox = { x: this.cursorX, y: this.cursorY, width, height };
    this.ctx.fillText(text, box.x + padding, box.y + padding);
    this.cursorX += width;
    this.rowHeight = Math.max(this.rowHeight, height);
    this.glyphCount += 1;
    return box;
  }

  get stats(): AtlasStats {
    return {
      width: this.width,
      height: this.height,
      glyphs: this.glyphCount,
      dropped: this.dropped,
      usedRowsHeight: this.cursorY + this.rowHeight,
    };
  }
}
