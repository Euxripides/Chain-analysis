/**
 * 导入文件解析核心（**必须在 Web Worker 里执行**，计划 1852 / §23.7）。
 *
 * 为什么单独成模块：解析本身（SheetJS XLSX.read + sheet_to_json）与格式防线
 * （逐条真实解压、按字节计数）都是 CPU 密集的同步/半同步工作；放在主线程会让
 * 页面在解析期间完全无法响应（v2 轮实测"整轮被拖到超时"）。因此这里只保留
 * **纯逻辑、无 React、无 DOM 副作用**的实现，由 import-parse.worker.ts 调用，
 * 主线程只通过消息与可终止的 Worker 交互（有界超时 + 可取消，见 parse-client.ts）。
 *
 * 语义来源：docs/dual-system-development-plan.md 1042-1043、§23.7、frontend-design plan 1852。
 */

/** 导入文件里的一行；normalized 只在服务端 verdict 之后才可信。 */
export interface ImportedAddress {
  /** 文件/粘贴里的原始值（用于摘要与拒绝原因展示） */
  input: string;
  /** 服务端返回的规范化地址（去重键 chain + normalized address 的后半段） */
  normalized: string;
  chain: string;
  caseName: string;
  tags: string[];
}


/** 文件解析的本地摘要（服务端复核前）。 */
export interface LocalParseSummary {
  rows: number;
  blank: number;
  addressColumn: string | null;
  usedHeaderRow: boolean;
  chainColumn: boolean;
  caseColumn: boolean;
  tagColumn: boolean;
  perChain: Record<string, number>;
  metaRows: number;
  truncated: number;
}

export const IMPORT_FILE_EXTENSIONS = [".csv", ".xlsx", ".xls"];
export const IMPORT_MAX_ADDRESSES = 100;

/* ---- §23.7 导入文件格式与压缩炸弹防线（frontend-design plan 1852：大小/条目/压缩比限制、隔离解析）----
 * 缺陷来源：v2 轮实测把 45 字节的垃圾文件 bad.xlsx 交给 XLSX.read，SheetJS 会做宽松格式嗅探
 * 并把它当文本表读入，于是浏览器弹出“解析摘要”而不是“文件解析失败”。
 * 因此解析前必须先做真实的格式/结构校验：扩展名 + 魔数 + OOXML 必需条目 + 条目数/解压尺寸/压缩比。
 */
const IMPORT_LIMITS = {
  maxFileBytes: 16 * 1024 * 1024,
  maxZipEntries: 512,
  maxUncompressedBytes: 64 * 1024 * 1024,
  maxCompressionRatio: 200,
  maxCsvSampleBytes: 4096,
};

type FileFormatVerdict = { ok: true; kind: "xlsx" | "xls" | "csv" } | { ok: false; reason: string };

/**
 * 真实解压校验：用浏览器原生 DecompressionStream("deflate-raw") 实际解压并**计数真实输出字节**。
 * 只信中央目录声明的大小是不够的——攻击者可以把 uncompressed size 声明得很小，
 * 而真实 deflate 流展开成几十 GB。这里逐条真实解压，超预算立即 cancel（可取消、有硬上限）。
 */
async function inflateActualBytes(file: File, dataOffset: number, compressedSize: number, cap: number): Promise<number | "exceeds" | "error"> {
  try {
    const stream = file.slice(dataOffset, dataOffset + compressedSize).stream().pipeThrough(new DecompressionStream("deflate-raw"));
    const reader = stream.getReader();
    let total = 0;
    for (;;) {
      const chunk = await reader.read();
      if (chunk.done) break;
      total += chunk.value?.byteLength ?? 0;
      if (total > cap) { await reader.cancel(); return "exceeds"; }
    }
    return total;
  } catch {
    return "error";
  }
}

async function inspectOoxmlZip(file: File): Promise<FileFormatVerdict> {
  const tailSize = Math.min(file.size, 66 * 1024);
  const tail = new Uint8Array(await file.slice(file.size - tailSize).arrayBuffer());
  let eocd = -1;
  for (let i = tail.length - 22; i >= 0; i -= 1) {
    if (tail[i] === 0x50 && tail[i + 1] === 0x4b && tail[i + 2] === 0x05 && tail[i + 3] === 0x06) { eocd = i; break; }
  }
  if (eocd < 0) return { ok: false, reason: "ZIP 结构损坏：找不到中央目录结束记录（EOCD）" };
  // ZIP64 结束定位器存在即视为不支持（本导入只接受经典 ZIP）。
  if (eocd >= 20 && tail[eocd - 20] === 0x50 && tail[eocd - 19] === 0x4b && tail[eocd - 18] === 0x06 && tail[eocd - 17] === 0x07) {
    return { ok: false, reason: "暂不支持 ZIP64 工作簿（本导入只接受经典 ZIP）" };
  }
  const view = new DataView(tail.buffer, tail.byteOffset, tail.byteLength);
  const diskNumber = view.getUint16(eocd + 4, true);
  const cdDisk = view.getUint16(eocd + 6, true);
  const diskEntries = view.getUint16(eocd + 8, true);
  const entryCount = view.getUint16(eocd + 10, true);
  const cdSize = view.getUint32(eocd + 12, true);
  const cdOffset = view.getUint32(eocd + 16, true);
  if (diskNumber !== 0 || cdDisk !== 0 || diskEntries !== entryCount) return { ok: false, reason: "多磁盘 ZIP 不受支持" };
  if (entryCount === 0xffff || cdSize === 0xffffffff || cdOffset === 0xffffffff) return { ok: false, reason: "暂不支持 ZIP64 工作簿（本导入只接受经典 ZIP）" };
  if (entryCount > IMPORT_LIMITS.maxZipEntries) return { ok: false, reason: `ZIP 条目数 ${entryCount} 超过上限 ${IMPORT_LIMITS.maxZipEntries}` };
  if (cdOffset + cdSize > file.size || cdOffset < 0) return { ok: false, reason: "ZIP 中央目录越界：声明尺寸大于文件本身" };
  const cd = new Uint8Array(await file.slice(cdOffset, cdOffset + cdSize).arrayBuffer());
  if (cd.length !== cdSize) return { ok: false, reason: "ZIP 中央目录不完整（读取长度与声明不一致）" };
  const cdv = new DataView(cd.buffer, cd.byteOffset, cd.byteLength);
  const names = new Set<string>();
  const entries: Array<{ name: string; method: number; flags: number; declared: number; compressed: number; localOffset: number }> = [];
  let p = 0;
  while (p < cd.length) {
    if (p + 46 > cd.length) return { ok: false, reason: "ZIP 中央目录记录被截断（偏移越界）" };
    if (cdv.getUint32(p, true) !== 0x02014b50) return { ok: false, reason: "ZIP 中央目录记录签名非法" };
    const flags = cdv.getUint16(p + 8, true);
    const method = cdv.getUint16(p + 10, true);
    const compressed = cdv.getUint32(p + 20, true);
    const declared = cdv.getUint32(p + 24, true);
    const nameLen = cdv.getUint16(p + 28, true);
    const extraLen = cdv.getUint16(p + 30, true);
    const commentLen = cdv.getUint16(p + 32, true);
    const localOffset = cdv.getUint32(p + 42, true);
    if (p + 46 + nameLen + extraLen + commentLen > cd.length) return { ok: false, reason: "ZIP 中央目录条目字段越界" };
    const name = new TextDecoder().decode(cd.slice(p + 46, p + 46 + nameLen));
    if (names.has(name)) return { ok: false, reason: `ZIP 含重复条目：${name}` };
    names.add(name);
    // 加密 / 强加密 / data descriptor（流式未知长度）一律拒绝，不与解析器博弈。
    if ((flags & 0x1) !== 0 || (flags & 0x40) !== 0) return { ok: false, reason: `ZIP 条目 ${name} 已加密，导入不接受加密工作簿` };
    if ((flags & 0x8) !== 0) return { ok: false, reason: `ZIP 条目 ${name} 使用 data descriptor，本导入不接受` };
    entries.push({ name, method, flags, declared, compressed, localOffset });
    p += 46 + nameLen + extraLen + commentLen;
  }
  if (p !== cd.length) return { ok: false, reason: "ZIP 中央目录未被完整消费（尾部残留字节）" };
  if (entries.length !== entryCount) return { ok: false, reason: `ZIP 条目数不一致：EOCD 声明 ${entryCount}，实际 ${entries.length}` };
  if (entries.length === 0) return { ok: false, reason: "ZIP 中央目录里没有任何条目" };
  const missing = ["[Content_Types].xml", "xl/workbook.xml"].filter((n) => !names.has(n));
  if (missing.length > 0) return { ok: false, reason: `不是合法的 OOXML 工作簿：缺少 ${missing.join("、")}` };
  const declaredTotal = entries.reduce((sum, e) => sum + e.declared, 0);
  if (declaredTotal > IMPORT_LIMITS.maxUncompressedBytes) {
    return { ok: false, reason: `声明解压后总大小 ${declaredTotal} 字节超过上限 ${IMPORT_LIMITS.maxUncompressedBytes}` };
  }
  const declaredRatio = file.size > 0 ? declaredTotal / file.size : 0;
  if (declaredRatio > IMPORT_LIMITS.maxCompressionRatio) {
    return { ok: false, reason: `声明压缩比 ${declaredRatio.toFixed(1)} 超过上限 ${IMPORT_LIMITS.maxCompressionRatio}（疑似压缩炸弹）` };
  }
  // ---- 真实验证：逐条实际解压并计数真实输出字节（声明值只用于交叉核对） ----
  let actualTotal = 0;
  for (const entry of entries) {
    if (entry.method !== 8 && entry.method !== 0) return { ok: false, reason: `不支持的 ZIP 压缩方法 ${entry.method}（仅支持 stored/deflate）` };
    if (entry.localOffset + 30 > file.size) return { ok: false, reason: "ZIP 本地文件头偏移越界" };
    const lh = new Uint8Array(await file.slice(entry.localOffset, entry.localOffset + 30).arrayBuffer());
    const lhv = new DataView(lh.buffer, lh.byteOffset, lh.byteLength);
    if (lhv.getUint32(0, true) !== 0x04034b50) return { ok: false, reason: "ZIP 本地文件头签名非法" };
    const localFlags = lhv.getUint16(6, true);
    const localMethod = lhv.getUint16(8, true);
    const localCompressed = lhv.getUint32(18, true);
    const localDeclared = lhv.getUint32(22, true);
    const localNameLen = lhv.getUint16(26, true);
    const localExtraLen = lhv.getUint16(28, true);
    // 本地头与中央目录必须一致：否则我们按中央目录校验，解析器却按本地头解压。
    if (localMethod !== entry.method) return { ok: false, reason: `条目 ${entry.name} 的本地头压缩方法与中央目录不一致（${localMethod} vs ${entry.method}）` };
    if ((localFlags & 0x1) !== 0 || (localFlags & 0x40) !== 0) return { ok: false, reason: `条目 ${entry.name} 的本地头标记为加密` };
    if ((localFlags & 0x8) !== 0) return { ok: false, reason: `条目 ${entry.name} 的本地头使用 data descriptor` };
    if (localCompressed !== 0 && localCompressed !== entry.compressed) return { ok: false, reason: `条目 ${entry.name} 压缩长度本地头/中央目录不一致` };
    if (localDeclared !== 0 && localDeclared !== entry.declared) return { ok: false, reason: `条目 ${entry.name} 声明的解压长度本地头/中央目录不一致` };
    if (entry.localOffset + 30 + localNameLen + localExtraLen > file.size) return { ok: false, reason: "ZIP 本地文件头名字/扩展字段越界" };
    const localName = new TextDecoder().decode(new Uint8Array(await file.slice(entry.localOffset + 30, entry.localOffset + 30 + localNameLen).arrayBuffer()));
    if (localName !== entry.name) return { ok: false, reason: `条目名字本地头/中央目录不一致（${localName} vs ${entry.name}）` };
    const dataOffset = entry.localOffset + 30 + localNameLen + localExtraLen;
    if (dataOffset + entry.compressed > file.size) return { ok: false, reason: "ZIP 条目数据段越界（声明压缩长度超出文件）" };
    if (entry.compressed === 0 && entry.declared > 0) return { ok: false, reason: `ZIP 条目 ${entry.name} 声明有内容但压缩长度为 0` };
    const remainingBudget = IMPORT_LIMITS.maxUncompressedBytes - actualTotal;
    let actual: number;
    if (entry.method === 0) {
      // stored：真实输出字节数就是压缩数据长度本身。绝不能把声明值当成实际值。
      actual = entry.compressed;
      if (entry.compressed !== entry.declared) {
        return { ok: false, reason: `stored 条目 ${entry.name} 声明 ${entry.declared} 字节与实际存放 ${entry.compressed} 字节不一致` };
      }
    } else {
      const result = await inflateActualBytes(file, dataOffset, entry.compressed, remainingBudget);
      if (result === "exceeds") return { ok: false, reason: `条目 ${entry.name} 真实解压后超过上限 ${IMPORT_LIMITS.maxUncompressedBytes} 字节（声明值不可信/疑似压缩炸弹）` };
      if (result === "error") return { ok: false, reason: `条目 ${entry.name} 解压失败（数据损坏）` };
      actual = result;
      if (actual !== entry.declared) {
        return { ok: false, reason: `条目 ${entry.name} 真实解压 ${actual} 字节与中央目录声明 ${entry.declared} 字节不一致（声明不可信）` };
      }
    }
    actualTotal += actual;
  }
  if (actualTotal > IMPORT_LIMITS.maxUncompressedBytes) {
    return { ok: false, reason: `实际解压总大小 ${actualTotal} 字节超过上限 ${IMPORT_LIMITS.maxUncompressedBytes}` };
  }
  return { ok: true, kind: "xlsx" };
}
/** 扩展名 + 魔数 + 结构三重校验；不做任何宽松嗅探回退。 */
export async function inspectImportFile(file: File): Promise<FileFormatVerdict> {
  const name = file.name.toLowerCase();
  const ext = IMPORT_FILE_EXTENSIONS.find((candidate) => name.endsWith(candidate)) ?? "";
  if (file.size === 0) return { ok: false, reason: "文件为空（0 字节）" };
  if (file.size > IMPORT_LIMITS.maxFileBytes) return { ok: false, reason: `文件 ${file.size} 字节超过上限 ${IMPORT_LIMITS.maxFileBytes} 字节` };
  const head = new Uint8Array(await file.slice(0, 8).arrayBuffer());
  const isZip = head[0] === 0x50 && head[1] === 0x4b && (head[2] === 0x03 || head[2] === 0x05 || head[2] === 0x07);
  const isOle = head[0] === 0xd0 && head[1] === 0xcf && head[2] === 0x11 && head[3] === 0xe0;
  if (ext === ".xlsx") {
    if (!isZip) return { ok: false, reason: "扩展名为 .xlsx 但内容不是 ZIP/OOXML 包（疑似损坏或被改名）" };
    return inspectOoxmlZip(file);
  }
  if (ext === ".xls") {
    if (!isOle) return { ok: false, reason: "扩展名为 .xls 但内容不是 OLE/CFB 复合文档（疑似损坏或被改名）" };
    return { ok: true, kind: "xls" };
  }
  if (ext !== ".csv") return { ok: false, reason: "不支持的文件扩展名（仅接受 .csv / .xlsx / .xls）" };
  const sample = new Uint8Array(await file.slice(0, IMPORT_LIMITS.maxCsvSampleBytes).arrayBuffer());
  for (const byte of sample) if (byte === 0) return { ok: false, reason: "CSV 内容包含 NUL 字节，疑似二进制文件" };
  return { ok: true, kind: "csv" };
}

export class ImportFormatRejected extends Error {
  constructor(reason: string) {
    super(reason);
    this.name = "ImportFormatRejected";
  }
}

/**
 * §23.7：解析前必须通过真实格式/结构校验，禁止 SheetJS 宽松嗅探把垃圾文件当表格读。
 * 校验不通过抛 ImportFormatRejected（与"解析期异常"区分开，便于 UI 给出不同提示）。
 */
export async function parseAddressFile(
  file: File,
  defaultChain: string,
): Promise<{ rows: ImportedAddress[]; summary: LocalParseSummary }> {
  const verdict = await inspectImportFile(file);
  if (!verdict.ok) throw new ImportFormatRejected(verdict.reason);
  return parseWorkbookChecked(file, defaultChain);
}

/**
 * 解析 CSV/XLSX 为待校验条目。
 * 语义（docs/dual-system-development-plan.md 1042-1043）：
 *   - 有 address 表头：按列取值，可选 chain / case / tags（tags 用 | ; ； 分隔）。
 *   - 无 address 表头：整表当作地址清单，逐单元格扫描。
 * 本地只做拆分与初步去重，最终以服务端 normalized 结果为准。
 */
async function parseWorkbookChecked(file: File, defaultChain: string): Promise<{ rows: ImportedAddress[]; summary: LocalParseSummary }> {
  const lowerName = file.name.toLowerCase();
  const summary: LocalParseSummary = {
    rows: 0, blank: 0, addressColumn: null, usedHeaderRow: false,
    chainColumn: false, caseColumn: false, tagColumn: false, perChain: {}, metaRows: 0, truncated: 0,
  };
  const rows: ImportedAddress[] = [];
  const XLSX = await import("xlsx");
  const isCsv = lowerName.endsWith(".csv");
  const buffer = isCsv ? await file.text() : await file.arrayBuffer();
  const wb = XLSX.read(buffer, { type: isCsv ? "string" : "array" });
  const sheetName = wb.SheetNames[0];
  if (!sheetName) throw new Error("文件里没有可读的工作表");
  const ws = wb.Sheets[sheetName];
  const records = XLSX.utils.sheet_to_json<Record<string, unknown>>(ws, { defval: "", raw: false });
  const headerKeys = records.length > 0 ? Object.keys(records[0]).map((key) => key.trim().toLowerCase()) : [];
  const hasAddressHeader = headerKeys.includes("address");
  summary.usedHeaderRow = hasAddressHeader;
  summary.addressColumn = hasAddressHeader ? "address" : null;
  summary.chainColumn = hasAddressHeader && headerKeys.includes("chain");
  summary.caseColumn = hasAddressHeader && headerKeys.includes("case");
  summary.tagColumn = hasAddressHeader && headerKeys.includes("tags");

  const pushRow = (row: ImportedAddress, chain: string) => {
    summary.rows += 1;
    if (!row.input) { summary.blank += 1; return; }
    summary.perChain[chain || defaultChain] = (summary.perChain[chain || defaultChain] ?? 0) + 1;
    if (row.caseName || row.tags.length > 0) summary.metaRows += 1;
    if (rows.length >= IMPORT_MAX_ADDRESSES) { summary.truncated += 1; return; }
    rows.push(row);
  };

  if (hasAddressHeader) {
    for (const raw of records) {
      const record = Object.fromEntries(Object.entries(raw).map(([key, value]) => [key.trim().toLowerCase(), String(value ?? "").trim()]));
      const rowChain = (record.chain ?? "").toLowerCase();
      const tags = (record.tags ?? "").split(/[|;；]/).map((value) => value.trim()).filter(Boolean);
      pushRow({ input: record.address ?? "", normalized: "", chain: rowChain, caseName: record.case ?? "", tags }, rowChain);
    }
  } else {
    for (const line of XLSX.utils.sheet_to_csv(ws).split(/[\r\n,;；\t]+/)) {
      const value = line.trim();
      if (!value) continue;
      pushRow({ input: value, normalized: "", chain: "", caseName: "", tags: [] }, "");
    }
  }
  return { rows, summary };
}
