/**
 * 二维码（QR Code Model 2）编码：纯算法，无 JSX、无第三方依赖。
 *
 * 范围：
 * - 模式固定为 byte（内容按 UTF-8 编码成字节）；
 * - 纠错等级固定为 M；
 * - 版本 1..20 自动选择能装下数据的最小版本；
 * - 掩码按 ISO/IEC 18004 的四条惩罚规则自动选择（不固定 mask 0）。
 *
 * 为什么细节要与 segno 对齐：
 * frontend/scripts/verify-qr.mjs 用 Python segno 生成的参考矩阵逐模块比对，
 * 所以标准留有解释空间的地方（填充位、惩罚规则的边界、掩码评分时是否先写格式信息）
 * 一律采用与 segno 1.6.6 相同的取舍，并在对应位置写明；这些取舍不影响解码。
 *
 * 语法约束：只使用「可擦除语法」（不用 enum / namespace / 构造函数参数属性 / 装饰器），
 * 这样 Node 的原生 TypeScript 支持可以直接 import 本文件，校验脚本无需先编译。
 */

/** 支持的版本范围（版本越大容量越大，21 以上本项目用不到）。 */
const VERSION_MIN = 1;
const VERSION_MAX = 20;

/** byte 模式的模式指示符。 */
const MODE_BYTE = 0b0100;

/** 纠错等级 M 的格式信息编码位（L=01、M=00、Q=11、H=10）。 */
const EC_LEVEL_BITS_M = 0b00;

/** 格式信息在 BCH 之后的固定异或掩码（ISO/IEC 18004 7.9.1）。 */
const FORMAT_INFO_MASK = 0x5412;

/** 数据填充码字，未填满容量时交替写入。 */
const PAD_CODEWORDS = [0xec, 0x11];

/** 掩码编号数量（ISO/IEC 18004 表 10）。 */
const MASK_COUNT = 8;

/**
 * 纠错等级 M 的分块表（ISO/IEC 18004 表 9），下标 = 版本 - 1。
 * 每行含义：[每块纠错码字数, 组1块数, 组1每块数据码字数, 组2块数, 组2每块数据码字数]。
 */
const ECC_M: number[][] = [
  [10, 1, 16, 0, 0], // v1
  [16, 1, 28, 0, 0], // v2
  [26, 1, 44, 0, 0], // v3
  [18, 2, 32, 0, 0], // v4
  [24, 2, 43, 0, 0], // v5
  [16, 4, 27, 0, 0], // v6
  [18, 4, 31, 0, 0], // v7
  [22, 2, 38, 2, 39], // v8
  [22, 3, 36, 2, 37], // v9
  [26, 4, 43, 1, 44], // v10
  [30, 1, 50, 4, 51], // v11
  [22, 6, 36, 2, 37], // v12
  [22, 8, 37, 1, 38], // v13
  [24, 4, 40, 5, 41], // v14
  [24, 5, 41, 5, 42], // v15
  [28, 7, 45, 3, 46], // v16
  [28, 10, 46, 1, 47], // v17
  [26, 9, 43, 4, 44], // v18
  [26, 3, 44, 11, 45], // v19
  [26, 3, 41, 13, 42], // v20
];

/** 编码结果：modules 为 size*size 的行优先数组，true 表示深色模块。 */
export interface QrMatrix {
  version: number;
  size: number;
  modules: boolean[];
}

/** 版本对应的符号边长（不含静默区）。 */
function symbolSize(version: number): number {
  return version * 4 + 17;
}

/** 版本对应的数据码字总数。 */
function dataCodewordCount(version: number): number {
  const row = ECC_M[version - 1];
  return row[1] * row[2] + row[3] * row[4];
}

/** byte 模式字符计数指示符的位数：版本 1..9 为 8 位，10..26 为 16 位。 */
function charCountBits(version: number): number {
  return version <= 9 ? 8 : 16;
}

/** 某版本在 byte 模式 / 纠错等级 M 下能容纳的最大字节数。 */
function byteCapacity(version: number): number {
  return Math.floor((dataCodewordCount(version) * 8 - 4 - charCountBits(version)) / 8);
}

/** 选择能容下 byteLength 个字节的最小版本；超出范围时抛出，由调用方决定降级行为。 */
function selectVersion(byteLength: number): number {
  for (let version = VERSION_MIN; version <= VERSION_MAX; version += 1) {
    if (byteLength <= byteCapacity(version)) return version;
  }
  throw new Error(
    "qr: " + byteLength + " bytes exceed the version " + VERSION_MAX + " level-M capacity of " +
    byteCapacity(VERSION_MAX) + " bytes",
  );
}

/** 把字符串编码成 UTF-8 字节；孤立代理项按 U+FFFD 处理（与 TextEncoder 一致）。 */
function utf8Bytes(value: string): number[] {
  const out: number[] = [];
  for (let i = 0; i < value.length; i += 1) {
    let code = value.charCodeAt(i);
    if (code >= 0xd800 && code <= 0xdbff && i + 1 < value.length) {
      const low = value.charCodeAt(i + 1);
      if (low >= 0xdc00 && low <= 0xdfff) {
        code = 0x10000 + ((code - 0xd800) << 10) + (low - 0xdc00);
        i += 1;
      }
    }
    if (code >= 0xd800 && code <= 0xdfff) code = 0xfffd;
    if (code < 0x80) {
      out.push(code);
    } else if (code < 0x800) {
      out.push(0xc0 | (code >> 6), 0x80 | (code & 0x3f));
    } else if (code < 0x10000) {
      out.push(0xe0 | (code >> 12), 0x80 | ((code >> 6) & 0x3f), 0x80 | (code & 0x3f));
    } else {
      out.push(
        0xf0 | (code >> 18),
        0x80 | ((code >> 12) & 0x3f),
        0x80 | ((code >> 6) & 0x3f),
        0x80 | (code & 0x3f),
      );
    }
  }
  return out;
}

/* ------------------------------------------------------------------ *
 * GF(256)：本原多项式 0x11d，用于 Reed-Solomon 纠错码字计算
 * ------------------------------------------------------------------ */

const GF_EXP = new Uint8Array(512);
const GF_LOG = new Uint8Array(256);

(function initGaloisField() {
  let value = 1;
  for (let i = 0; i < 255; i += 1) {
    GF_EXP[i] = value;
    GF_LOG[value] = i;
    value <<= 1;
    if (value & 0x100) value ^= 0x11d;
  }
  // 复制一份指数表，乘法时可直接相加取模而不必再取模运算
  for (let i = 255; i < 512; i += 1) GF_EXP[i] = GF_EXP[i - 255];
})();

/** 伽罗华域乘法。 */
function gfMultiply(a: number, b: number): number {
  if (a === 0 || b === 0) return 0;
  return GF_EXP[GF_LOG[a] + GF_LOG[b]];
}

const generatorCache = new Map<number, number[]>();

/** 生成 degree 次的 RS 生成多项式（首项系数为 1，根为 a^0..a^(degree-1)）。 */
function rsGenerator(degree: number): number[] {
  const cached = generatorCache.get(degree);
  if (cached) return cached;
  let poly = [1];
  for (let i = 0; i < degree; i += 1) {
    const next = new Array<number>(poly.length + 1).fill(0);
    for (let j = 0; j < poly.length; j += 1) {
      next[j] ^= poly[j];
      next[j + 1] ^= gfMultiply(poly[j], GF_EXP[i]);
    }
    poly = next;
  }
  generatorCache.set(degree, poly);
  return poly;
}

/** 计算一个数据块的纠错码字（多项式长除法取余）。 */
function rsRemainder(data: readonly number[], degree: number): number[] {
  const gen = rsGenerator(degree);
  const remainder = new Array<number>(degree).fill(0);
  for (let i = 0; i < data.length; i += 1) {
    const factor = data[i] ^ remainder[0];
    for (let j = 0; j < degree - 1; j += 1) remainder[j] = remainder[j + 1];
    remainder[degree - 1] = 0;
    if (factor !== 0) {
      for (let j = 0; j < degree; j += 1) remainder[j] ^= gfMultiply(gen[j + 1], factor);
    }
  }
  return remainder;
}

/* ------------------------------------------------------------------ *
 * 数据编码（ISO/IEC 18004 7.4 / 7.6）
 * ------------------------------------------------------------------ */

/** 生成数据码字：模式指示符 + 字符计数 + 数据 + 终止符 + 填充。 */
function buildDataCodewords(bytes: readonly number[], version: number): number[] {
  const capacityCodewords = dataCodewordCount(version);
  const capacityBits = capacityCodewords * 8;
  const bits: number[] = [];
  const push = (value: number, length: number) => {
    for (let i = length - 1; i >= 0; i -= 1) bits.push((value >>> i) & 1);
  };

  push(MODE_BYTE, 4);
  push(bytes.length, charCountBits(version));
  for (let i = 0; i < bytes.length; i += 1) push(bytes[i], 8);

  // 终止符：最多 4 个 0，剩余容量不足 4 位时截短
  const terminator = Math.min(capacityBits - bits.length, 4);
  for (let i = 0; i < terminator; i += 1) bits.push(0);

  // 补齐到码字边界。注意：这里刻意与 segno 1.6.6 一致——已经对齐时仍再补 8 个 0
  // （ISO/IEC 18004 7.4.10 只要求"未对齐时才补"）。多出的 0 码字是合法填充，
  // 解码器按字符计数读取数据，不受影响；对齐它是为了让参考矩阵逐位相同。
  const padBits = 8 - (bits.length % 8);
  for (let i = 0; i < padBits; i += 1) bits.push(0);

  const codewords: number[] = [];
  for (let i = 0; i < bits.length; i += 8) {
    let value = 0;
    for (let j = 0; j < 8; j += 1) value = (value << 1) | bits[i + j];
    codewords.push(value);
  }

  let padIndex = 0;
  while (codewords.length < capacityCodewords) {
    codewords.push(PAD_CODEWORDS[padIndex % 2]);
    padIndex += 1;
  }
  // 数据刚好占满容量时上面的补位可能多出一个 0 码字，按容量截断
  return codewords.slice(0, capacityCodewords);
}

/** 分块、算纠错、按标准交织，得到最终码字序列。 */
function buildFinalCodewords(dataCodewords: readonly number[], version: number): number[] {
  const row = ECC_M[version - 1];
  const ecPerBlock = row[0];
  const dataBlocks: number[][] = [];
  const ecBlocks: number[][] = [];
  let offset = 0;

  const takeBlocks = (count: number, blockLength: number) => {
    for (let i = 0; i < count; i += 1) {
      const block = dataCodewords.slice(offset, offset + blockLength);
      offset += blockLength;
      dataBlocks.push(block);
      ecBlocks.push(rsRemainder(block, ecPerBlock));
    }
  };
  takeBlocks(row[1], row[2]);
  takeBlocks(row[3], row[4]);

  const out: number[] = [];
  let maxDataLength = 0;
  for (let i = 0; i < dataBlocks.length; i += 1) {
    if (dataBlocks[i].length > maxDataLength) maxDataLength = dataBlocks[i].length;
  }
  for (let i = 0; i < maxDataLength; i += 1) {
    for (let b = 0; b < dataBlocks.length; b += 1) {
      if (i < dataBlocks[b].length) out.push(dataBlocks[b][i]);
    }
  }
  for (let i = 0; i < ecPerBlock; i += 1) {
    for (let b = 0; b < ecBlocks.length; b += 1) out.push(ecBlocks[b][i]);
  }
  return out;
}

/* ------------------------------------------------------------------ *
 * 符号构造（ISO/IEC 18004 6.3 / 7.7）
 * ------------------------------------------------------------------ */

interface QrGrid {
  size: number;
  modules: boolean[];
  /** 功能图形占位（定位/校正/时序/格式/版本信息与固定深色模块），不参与数据与掩码。 */
  isFunction: boolean[];
}

/** 校正图形中心坐标（ISO/IEC 18004 附录 E 的取值规律）。 */
function alignmentPositions(version: number): number[] {
  if (version === 1) return [];
  const count = Math.floor(version / 7) + 2;
  const step = Math.ceil((version * 4 + 4) / (count * 2 - 2)) * 2;
  const positions = [6];
  for (let pos = version * 4 + 10; positions.length < count; pos -= step) positions.splice(1, 0, pos);
  return positions;
}

/** 建立功能图形与数据区，并把码字按标准折线填入。 */
function buildGrid(version: number, codewordBits: readonly number[]): QrGrid {
  const size = symbolSize(version);
  const modules = new Array<boolean>(size * size).fill(false);
  const isFunction = new Array<boolean>(size * size).fill(false);
  const set = (x: number, y: number, dark: boolean) => {
    modules[y * size + x] = dark;
    isFunction[y * size + x] = true;
  };

  // 时序图形（先画，随后被定位/校正图形覆盖，取值一致）
  for (let i = 8; i < size - 8; i += 1) {
    set(i, 6, i % 2 === 0);
    set(6, i, i % 2 === 0);
  }

  // 定位图形与分隔符（4 个模块外为分隔符，故 dist 为 2 或 4 时为浅色）
  const centers = [[3, 3], [size - 4, 3], [3, size - 4]];
  for (let c = 0; c < centers.length; c += 1) {
    const cx = centers[c][0];
    const cy = centers[c][1];
    for (let dy = -4; dy <= 4; dy += 1) {
      for (let dx = -4; dx <= 4; dx += 1) {
        const x = cx + dx;
        const y = cy + dy;
        if (x < 0 || y < 0 || x >= size || y >= size) continue;
        const dist = Math.max(Math.abs(dx), Math.abs(dy));
        set(x, y, dist !== 2 && dist !== 4);
      }
    }
  }

  // 校正图形（跳过三个定位图形角落；中心 5x5，dist 为 1 时为浅色）
  const positions = alignmentPositions(version);
  for (let a = 0; a < positions.length; a += 1) {
    for (let b = 0; b < positions.length; b += 1) {
      const cx = positions[b];
      const cy = positions[a];
      if ((cx === 6 && cy === 6) || (cx === 6 && cy === size - 7) || (cx === size - 7 && cy === 6)) continue;
      for (let dy = -2; dy <= 2; dy += 1) {
        for (let dx = -2; dx <= 2; dx += 1) set(cx + dx, cy + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
      }
    }
  }

  // 格式信息区与版本信息区：先占位为浅色，掩码评分之后才写入真实比特。
  // 第 8 行 / 第 8 列与第 6 行 / 第 6 列的交点属于时序图形（已在上方画好），不能覆盖成浅色。
  for (let i = 0; i <= 8; i += 1) {
    if (i === 6) continue;
    set(8, i, false);
    set(i, 8, false);
  }
  for (let i = 0; i < 8; i += 1) {
    set(8, size - 1 - i, false);
    set(size - 1 - i, 8, false);
  }
  if (version >= 7) {
    for (let i = 0; i < 6; i += 1) {
      for (let k = 0; k < 3; k += 1) {
        set(i, size - 11 + k, false);
        set(size - 11 + k, i, false);
      }
    }
  }

  // 数据放置：自右下角起，两列一组上下折返；列 6（纵向时序图形）整列跳过
  let bitIndex = 0;
  for (let right = size - 1; right >= 1; right -= 2) {
    if (right === 6) right = 5; // 跳过纵向时序图形所在列
    for (let vertical = 0; vertical < size; vertical += 1) {
      for (let z = 0; z < 2; z += 1) {
        const x = right - z;
        const upward = ((right & 2) === 0) !== (x < 6);
        const y = upward ? size - 1 - vertical : vertical;
        const index = y * size + x;
        if (!isFunction[index] && bitIndex < codewordBits.length) {
          modules[index] = codewordBits[bitIndex] === 1;
          bitIndex += 1;
        }
      }
    }
  }

  return { size, modules, isFunction };
}

/* ------------------------------------------------------------------ *
 * 掩码（ISO/IEC 18004 7.8）
 * ------------------------------------------------------------------ */

/** 第 mask 号掩码在 (x=列, y=行) 处是否翻转。 */
function maskCondition(mask: number, x: number, y: number): boolean {
  switch (mask) {
    case 0:
      return (x + y) % 2 === 0;
    case 1:
      return y % 2 === 0;
    case 2:
      return x % 3 === 0;
    case 3:
      return (x + y) % 3 === 0;
    case 4:
      return (Math.floor(y / 2) + Math.floor(x / 3)) % 2 === 0;
    case 5:
      return ((x * y) % 2) + ((x * y) % 3) === 0;
    case 6:
      return (((x * y) % 2) + ((x * y) % 3)) % 2 === 0;
    default:
      return (((x + y) % 2) + ((x * y) % 3)) % 2 === 0;
  }
}

/** 对数据区就地异或掩码（再调用一次即还原）。 */
function applyMask(grid: QrGrid, mask: number): void {
  const { size, modules, isFunction } = grid;
  for (let y = 0; y < size; y += 1) {
    for (let x = 0; x < size; x += 1) {
      const index = y * size + x;
      if (!isFunction[index] && maskCondition(mask, x, y)) modules[index] = !modules[index];
    }
  }
}

/** 惩罚规则 1：行/列上连续同色 5 个模块起，每个计 (长度 - 2) 分。 */
function penaltyRule1(modules: readonly boolean[], size: number): number {
  let score = 0;
  for (let i = 0; i < size; i += 1) {
    let rowPrev = -1;
    let rowRun = 0;
    let colPrev = -1;
    let colRun = 0;
    for (let j = 0; j < size; j += 1) {
      const rowBit = modules[i * size + j] ? 1 : 0;
      const colBit = modules[j * size + i] ? 1 : 0;
      if (rowBit === rowPrev) rowRun += 1;
      else {
        if (rowRun >= 5) score += rowRun - 2;
        rowRun = 1;
        rowPrev = rowBit;
      }
      if (colBit === colPrev) colRun += 1;
      else {
        if (colRun >= 5) score += colRun - 2;
        colRun = 1;
        colPrev = colBit;
      }
    }
    if (rowRun >= 5) score += rowRun - 2;
    if (colRun >= 5) score += colRun - 2;
  }
  return score;
}

/** 惩罚规则 2：每个 2x2 同色块计 3 分。 */
function penaltyRule2(modules: readonly boolean[], size: number): number {
  let score = 0;
  for (let i = 1; i < size; i += 1) {
    for (let j = 1; j < size; j += 1) {
      const current = modules[i * size + j];
      if (
        current === modules[i * size + j - 1] &&
        current === modules[(i - 1) * size + j] &&
        current === modules[(i - 1) * size + j - 1]
      ) {
        score += 3;
      }
    }
  }
  return score;
}

/** 惩罚规则 3 要找的 1:1:3:1:1 图形（深 浅 深 深 深 浅 深）。 */
const FINDER_LIKE = [1, 0, 1, 1, 1, 0, 1];

/** 序列 [from, to) 是否全为浅色；空区间视为"没有深色"，与参考实现一致。 */
function allLight(sequence: readonly number[], from: number, to: number): boolean {
  for (let i = from; i < to; i += 1) {
    if (sequence[i] !== 0) return false;
  }
  return true;
}

/**
 * 统计一条扫描线上满足"两侧至少有一侧有 4 个模块宽的浅色区"的 1:1:3:1:1 图形个数，每个 40 分。
 * 边界处理与 segno 完全一致：图形落在符号首尾时视为外侧（静默区）是浅色。
 */
function countFinderLike(sequence: readonly number[]): number {
  const length = sequence.length;
  let score = 0;
  let start = 0;
  for (;;) {
    let index = -1;
    for (let i = start; i + 7 <= length && index === -1; i += 1) {
      let matched = true;
      for (let k = 0; k < 7; k += 1) {
        if (sequence[i + k] !== FINDER_LIKE[k]) {
          matched = false;
          break;
        }
      }
      if (matched) index = i;
    }
    if (index === -1) return score;
    const end = index + 7;
    const lightBefore = allLight(sequence, Math.max(index - 4, 0), Math.min(index, length));
    const lightAfter = allLight(sequence, Math.max(end, 0), Math.min(end + 4, length));
    if (index === 0 || index === length - 7 || lightBefore || lightAfter) {
      score += 40;
      start = end;
    } else {
      start = index + 4;
    }
  }
}

/** 惩罚规则 3：逐行逐列扫描 1:1:3:1:1 图形。 */
function penaltyRule3(modules: readonly boolean[], size: number): number {
  const line: number[] = new Array<number>(size).fill(0);
  let score = 0;
  for (let i = 0; i < size; i += 1) {
    for (let j = 0; j < size; j += 1) line[j] = modules[i * size + j] ? 1 : 0;
    score += countFinderLike(line);
    for (let j = 0; j < size; j += 1) line[j] = modules[j * size + i] ? 1 : 0;
    score += countFinderLike(line);
  }
  return score;
}

/** 惩罚规则 4：深色模块占比每偏离 50% 满 5% 计 10 分。 */
function penaltyRule4(modules: readonly boolean[], size: number): number {
  let dark = 0;
  for (let i = 0; i < modules.length; i += 1) {
    if (modules[i]) dark += 1;
  }
  const percent = dark / (size * size);
  return 10 * Math.trunc(Math.abs(percent * 100 - 50) / 5);
}

/** 四条惩罚规则合计得分，用于比较掩码优劣。 */
function penaltyScore(modules: readonly boolean[], size: number): number {
  return penaltyRule1(modules, size) + penaltyRule2(modules, size) +
    penaltyRule3(modules, size) + penaltyRule4(modules, size);
}

/**
 * 选出惩罚得分最低的掩码。
 * 评分时格式信息与版本信息尚未写入（保持浅色），这与 segno 的处理一致；
 * 得分相同时取编号较小的掩码。
 */
function selectMask(grid: QrGrid): number {
  let best = 0;
  let bestScore = Number.POSITIVE_INFINITY;
  for (let mask = 0; mask < MASK_COUNT; mask += 1) {
    applyMask(grid, mask);
    const score = penaltyScore(grid.modules, grid.size);
    applyMask(grid, mask);
    if (score < bestScore) {
      bestScore = score;
      best = mask;
    }
  }
  return best;
}

/* ------------------------------------------------------------------ *
 * 格式信息与版本信息（ISO/IEC 18004 7.9 / 7.10）
 * ------------------------------------------------------------------ */

/** 15 位格式信息：5 位数据 + BCH(15,5) 校验，再异或固定掩码。 */
function formatInfoBits(mask: number): number {
  const data = (EC_LEVEL_BITS_M << 3) | mask;
  let remainder = data;
  for (let i = 0; i < 10; i += 1) remainder = (remainder << 1) ^ ((remainder >>> 9) * 0x537);
  return ((data << 10) | remainder) ^ FORMAT_INFO_MASK;
}

/** 18 位版本信息：6 位版本号 + BCH(18,6) 校验（版本 < 7 不需要）。 */
function versionInfoBits(version: number): number {
  let remainder = version;
  for (let i = 0; i < 12; i += 1) remainder = (remainder << 1) ^ ((remainder >>> 11) * 0x1f25);
  return (version << 12) | remainder;
}

/** 把格式信息写入两个副本区域，并点亮固定深色模块。 */
function drawFormatInfo(grid: QrGrid, mask: number): void {
  const bits = formatInfoBits(mask);
  const size = grid.size;
  const set = (x: number, y: number, dark: boolean) => {
    grid.modules[y * size + x] = dark;
    grid.isFunction[y * size + x] = true;
  };
  for (let i = 0; i < 8; i += 1) {
    const verticalBit = ((bits >>> i) & 1) === 1;
    const horizontalBit = ((bits >>> (14 - i)) & 1) === 1;
    const offset = i >= 6 ? 1 : 0; // i 为 6 时让开时序图形
    set(8, i + offset, verticalBit); // 左上：第 8 列
    set(i + offset, 8, horizontalBit); // 左上：第 8 行
    set(size - 1 - i, 8, verticalBit); // 右上：第 8 行
    set(8, size - 1 - i, horizontalBit); // 左下：第 8 列
  }
  set(8, size - 8, true); // 固定深色模块
}

/** 把版本信息写入左下与右上两块区域（两处互为转置）。 */
function drawVersionInfo(grid: QrGrid, version: number): void {
  if (version < 7) return;
  const bits = versionInfoBits(version);
  const size = grid.size;
  const set = (x: number, y: number, dark: boolean) => {
    grid.modules[y * size + x] = dark;
    grid.isFunction[y * size + x] = true;
  };
  for (let i = 0; i < 6; i += 1) {
    for (let k = 0; k < 3; k += 1) {
      const dark = ((bits >>> (i * 3 + k)) & 1) === 1;
      set(i, size - 11 + k, dark); // 左下 3x6
      set(size - 11 + k, i, dark); // 右上 6x3
    }
  }
}

/* ------------------------------------------------------------------ *
 * 对外接口
 * ------------------------------------------------------------------ */

/**
 * 把字符串编码成二维码矩阵。
 * 失败（空内容或超出纠错等级 M 的版本 1..20 容量）时抛出 Error，由调用方决定如何降级。
 */
export function encodeQr(value: string): QrMatrix {
  const bytes = utf8Bytes(value);
  if (bytes.length === 0) throw new Error("qr: empty value");
  const version = selectVersion(bytes.length);
  const dataCodewords = buildDataCodewords(bytes, version);
  const finalCodewords = buildFinalCodewords(dataCodewords, version);

  const bits: number[] = [];
  for (let i = 0; i < finalCodewords.length; i += 1) {
    for (let shift = 7; shift >= 0; shift -= 1) bits.push((finalCodewords[i] >>> shift) & 1);
  }
  // 剩余位（版本 2..6 为 7 位、14..20 为 3 位）保持浅色，由数据区填充逻辑自然留空

  const grid = buildGrid(version, bits);
  const mask = selectMask(grid);
  applyMask(grid, mask);
  drawFormatInfo(grid, mask);
  drawVersionInfo(grid, version);

  return { version, size: grid.size, modules: grid.modules };
}

/**
 * 生成 SVG path 的 d 属性：每个深色模块输出一个 1x1 方块，同行相邻模块合并成一条横线。
 * quietZone 为静默区模块数（标准要求 4），坐标原点已包含静默区偏移。
 */
export function qrPathData(modules: boolean[], size: number, quietZone: number): string {
  if (!Number.isFinite(size) || size <= 0) return "";
  const offset = Math.max(0, Math.floor(quietZone));
  const parts: string[] = [];
  for (let y = 0; y < size; y += 1) {
    let x = 0;
    while (x < size) {
      if (!modules[y * size + x]) {
        x += 1;
        continue;
      }
      let run = 1;
      while (x + run < size && modules[y * size + x + run]) run += 1;
      parts.push("M" + (x + offset) + " " + (y + offset) + "h" + run + "v1h-" + run + "z");
      x += run;
    }
  }
  return parts.join("");
}
