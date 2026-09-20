/**
 * r9-make-fixtures-v5c.mjs — U-DL-01（隔离解析 / 有界超时 / 可取消 / 恶意 ZIP）专用真实夹具。
 *
 * v1..v4 夹具文件全部原样保留；本文件只新增，不覆盖。
 * 所有 ZIP 都是**手工构造的真实字节结构**（不是 mock）：本地头 + 数据 + 中央目录 + EOCD，
 * 以精确命中 §23.7 的每一条防线分支（大 .xls / 大 .xlsx / 炸弹 / 加密 / stored 不符 /
 * 本地头与方法不符 / data descriptor / ZIP64）。
 *
 * 本轮（《新建数据下载页面全量整改实施规范》§13 / §14）新增：
 *   1. 行数语义（§13.3）：manifest 同时记录 requestedRows 与 actualFormatCapacity，
 *      并把写出的 .xls **读回**，报告该格式真实落盘的数据行数（不是我们请求的 200k）。
 *   2. ZIP64 布局机器自检（§14.3）：EOCD 位置 / locator 位于 EOCD 前 20 字节 /
 *      locator signature 0x07064b50 / ZIP64 EOCD signature 0x06064b50，
 *      并核对 locator 里声明的 ZIP64 EOCD 相对偏移确实指向该记录。
 *   3. 自检结果写成 JSON 证据（udl01-fixtures-selfcheck.json），rc 非 0 表示夹具本身不合格。
 */
import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { deflateRawSync } from "node:zlib";

const OUT = process.argv[2] ?? ".";
mkdirSync(OUT, { recursive: true });

/** BIFF8（.xls）单工作表行上限：65536 行（含表头）→ 最多 65535 个数据行。 */
const BIFF8_MAX_ROWS_INCLUDING_HEADER = 65536;
const BIFF8_MAX_DATA_ROWS = BIFF8_MAX_ROWS_INCLUDING_HEADER - 1;

const crc32 = (() => {
  const table = new Uint32Array(256);
  for (let i = 0; i < 256; i++) { let c = i; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; table[i] = c >>> 0; }
  return (buf) => { let c = 0xffffffff; for (const b of buf) c = table[(c ^ b) & 0xff] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0; };
})();

function buildZip(entries, options) {
  options = options ?? {};
  const chunks = [];
  const central = [];
  let offset = 0;
  for (const entry of entries) {
    const raw = Buffer.isBuffer(entry.data) ? entry.data : Buffer.from(entry.data, "utf8");
    const method = entry.method ?? 8;
    const compressed = method === 0 ? raw : deflateRawSync(raw);
    const nameBuf = Buffer.from(entry.name, "utf8");
    const flags = entry.flags ?? 0;
    const crc = crc32(raw);
    const decl = entry.declaredUncompressed ?? raw.length;
    const localComp = entry.localCompressed ?? compressed.length;
    const localMethod = entry.localMethod ?? method;
    const localFlags = entry.localFlags ?? flags;
    const localNameBuf = Buffer.from(entry.localName ?? entry.name, "utf8");
    const lh = Buffer.alloc(30);
    lh.writeUInt32LE(0x04034b50, 0);
    lh.writeUInt16LE(20, 4);
    lh.writeUInt16LE(localFlags, 6);
    lh.writeUInt16LE(localMethod, 8);
    lh.writeUInt32LE(crc, 14);
    lh.writeUInt32LE(localComp, 18);
    lh.writeUInt32LE(entry.localUncompressed ?? decl, 22);
    lh.writeUInt16LE(localNameBuf.length, 26);
    lh.writeUInt16LE(0, 28);
    chunks.push(lh, localNameBuf, compressed);
    central.push({ nameBuf, flags, method, compressed: compressed.length, decl, crc, offset });
    offset += lh.length + localNameBuf.length + compressed.length;
  }
  const cdStart = offset;
  const cdParts = [];
  for (const c of central) {
    const cdh = Buffer.alloc(46);
    cdh.writeUInt32LE(0x02014b50, 0);
    cdh.writeUInt16LE(20, 4);
    cdh.writeUInt16LE(20, 6);
    cdh.writeUInt16LE(c.flags, 8);
    cdh.writeUInt16LE(c.method, 10);
    cdh.writeUInt32LE(c.crc, 16);
    cdh.writeUInt32LE(c.compressed, 20);
    cdh.writeUInt32LE(c.decl, 24);
    cdh.writeUInt16LE(c.nameBuf.length, 28);
    cdh.writeUInt32LE(c.offset, 42);
    cdParts.push(cdh, c.nameBuf);
    offset += cdh.length + c.nameBuf.length;
  }
  const cdSize = offset - cdStart;
  const eocd = Buffer.alloc(22);
  eocd.writeUInt32LE(0x06054b50, 0);
  eocd.writeUInt16LE(central.length, 8);
  eocd.writeUInt16LE(central.length, 10);
  eocd.writeUInt32LE(cdSize, 12);
  eocd.writeUInt32LE(cdStart, 16);
  const parts = chunks.concat(cdParts);
  const zip64 = {};
  if (options.zip64Locator) {
    // 真实 ZIP64 布局：[ZIP64 结束记录][ZIP64 定位器][EOCD]。
    // v5b 把定位器放在 EOCD **之后**，生产解析器检查 EOCD-20 时命不中 ZIP64 分支。
    zip64.zip64EocdOffset = offset;
    const z64eocd = Buffer.alloc(56);
    z64eocd.writeUInt32LE(0x06064b50, 0);
    z64eocd.writeBigUInt64LE(BigInt(56 - 12), 4);
    z64eocd.writeUInt16LE(45, 12); // version made by
    z64eocd.writeUInt16LE(45, 14); // version needed
    z64eocd.writeBigUInt64LE(BigInt(central.length), 32); // total entries (this disk)
    z64eocd.writeBigUInt64LE(BigInt(central.length), 40); // total entries
    z64eocd.writeBigUInt64LE(BigInt(cdSize), 40 + 8); // size of central directory
    z64eocd.writeBigUInt64LE(BigInt(cdStart), 48);
    parts.push(z64eocd);
    offset += z64eocd.length;
    zip64.locatorOffset = offset;
    const loc = Buffer.alloc(20);
    loc.writeUInt32LE(0x07064b50, 0);
    loc.writeUInt32LE(0, 4); // disk with zip64 EOCD
    loc.writeBigUInt64LE(BigInt(zip64.zip64EocdOffset), 8); // relative offset of zip64 EOCD
    loc.writeUInt32LE(1, 16); // total disks
    parts.push(loc);
    offset += loc.length;
  }
  zip64.eocdOffset = offset;
  parts.push(eocd);
  if (options.trailing) parts.push(Buffer.from(options.trailing));
  return { buffer: Buffer.concat(parts), zip64 };
}

const OOXML_CORE = [
  { name: "[Content_Types].xml", data: '<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/></Types>' },
  { name: "xl/workbook.xml", data: '<?xml version="1.0"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>' },
];

const manifest = [];
const write = (name, buf, note) => {
  writeFileSync(path.join(OUT, name), buf);
  manifest.push({ name, bytes: buf.length, sha256: createHash("sha256").update(buf).digest("hex"), note });
};

const XLSX = await import("xlsx");
// .xlsx 必须**小于** IMPORT_LIMITS.maxFileBytes(16MB)，否则超时/取消用例会被"文件过大"
// 分支提前拒绝，测不到解析本身。.xls 用 200k 行请求，但 BIFF8 只能落 65535 个数据行。
const REQUESTED_ROWS_XLS = Number(process.argv[3] ?? 200000);
const ROWS_XLSX = Number(process.argv[4] ?? 60000);
const matrix = [["address", "chain", "tags"]];
for (let i = 0; i < REQUESTED_ROWS_XLS; i++) matrix.push(["0x" + (i + 1).toString(16).padStart(40, "0"), "bsc", i % 7 === 0 ? "bulk|v5" : ""]);
const wsBig = XLSX.utils.aoa_to_sheet(matrix);
const wbXls = XLSX.utils.book_new();
XLSX.utils.book_append_sheet(wbXls, wsBig, "Sheet1");
const bigXlsBytes = XLSX.write(wbXls, { type: "buffer", bookType: "xls" });
write("udl01-big.xls", bigXlsBytes,
  "真实 BIFF8 .xls（请求 " + REQUESTED_ROWS_XLS + " 行 + 表头；BIFF8 上限 " + BIFF8_MAX_DATA_ROWS + " 个数据行；OLE/CFB 魔数）");

const matrixXlsx = [["address", "chain", "tags"]];
for (let i = 0; i < ROWS_XLSX; i++) matrixXlsx.push(["0x" + (i + 1).toString(16).padStart(40, "0"), "bsc", i % 7 === 0 ? "bulk|v5" : ""]);
const wbBig = XLSX.utils.book_new();
XLSX.utils.book_append_sheet(wbBig, XLSX.utils.aoa_to_sheet(matrixXlsx), "Sheet1");
write("udl01-big.xlsx", XLSX.write(wbBig, { type: "buffer", bookType: "xlsx" }),
  "真实 OOXML .xlsx（" + ROWS_XLSX + " 行 + 表头；小于 16MB 上限，用于有界超时与取消）");

const bombRaw = Buffer.alloc(200 * 1024 * 1024, 0x41);
write("udl01-bomb.xlsx", buildZip(OOXML_CORE.concat([{ name: "xl/worksheets/sheet1.xml", data: bombRaw }])).buffer,
  "真实 OOXML 结构 + 可解压 200MB 条目（命中声明解压总大小上限分支）");
write("udl01-encrypted.xlsx", buildZip(OOXML_CORE.concat([{ name: "xl/worksheets/sheet1.xml", data: "<x/>", flags: 0x1 }])).buffer,
  "中央目录与本地头都标记加密（flag 0x1）");
write("udl01-stored-mismatch.xlsx", buildZip(OOXML_CORE.concat([{ name: "xl/worksheets/sheet1.xml", data: "<x/>", method: 0, declaredUncompressed: 999 }])).buffer,
  "stored(method 0) 条目声明解压长度与实际存放不一致");
write("udl01-method-mismatch.xlsx", buildZip(OOXML_CORE.concat([{ name: "xl/worksheets/sheet1.xml", data: "<x/>", localMethod: 0 }])).buffer,
  "本地头 method=0 而中央目录 method=8");
write("udl01-data-descriptor.xlsx", buildZip(OOXML_CORE.concat([{ name: "xl/worksheets/sheet1.xml", data: "<x/>", flags: 0x8, localFlags: 0x8 }])).buffer,
  "条目使用 data descriptor（flag 0x8）");
const zip64Built = buildZip(OOXML_CORE.concat([{ name: "xl/worksheets/sheet1.xml", data: "<x/>" }]), { zip64Locator: true });
write("udl01-zip64.xlsx", zip64Built.buffer, "ZIP64 结束记录 + 定位器位于 EOCD 之前（真实 ZIP64 布局）");

const wbSmall = XLSX.utils.book_new();
XLSX.utils.book_append_sheet(wbSmall, XLSX.utils.aoa_to_sheet([["address", "chain", "tags"], ["0x00000000000000000000000000000000000000d1", "bsc", "v5"]]), "Sheet1");
write("udl01-small-ok.xlsx", XLSX.write(wbSmall, { type: "buffer", bookType: "xlsx" }), "正常小工作簿（隔离解析正向对照）");

/* ------------------------------------------------------------ 机器自检（§13.3 / §14.3） */

const problems = [];

// 1) 行数语义：请求行数 vs 该格式真实容量 vs 磁盘文件里真实落盘的行数（读回文件，不靠假设）。
const written = readFileSync(path.join(OUT, "udl01-big.xls"));
const readBack = XLSX.read(written, { type: "buffer" });
const sheet = readBack.Sheets[readBack.SheetNames[0]];
const readBackRows = XLSX.utils.sheet_to_json(sheet, { defval: "", raw: false });
/* sheet_to_json 默认把第一行当表头，返回的已经是**数据行**；不能再减 1。 */
const actualDataRows = readBackRows.length;
const rowSemantics = {
  requestedRows: REQUESTED_ROWS_XLS,
  actualFormatCapacity: { format: "BIFF8", maxRowsIncludingHeader: BIFF8_MAX_ROWS_INCLUDING_HEADER, maxDataRows: BIFF8_MAX_DATA_ROWS },
  actualDataRowsOnDisk: actualDataRows,
  withinFormatCapacity: actualDataRows <= BIFF8_MAX_DATA_ROWS,
  satisfiesFormatCapacity: actualDataRows === BIFF8_MAX_DATA_ROWS,
  note: "BIFF8 .xls 单表最多 65536 行（含表头）；请求 " + REQUESTED_ROWS_XLS + " 行时实际落盘 " + actualDataRows + " 个数据行",
};
if (!rowSemantics.withinFormatCapacity) problems.push("udl01-big.xls 数据行数超过 BIFF8 上限：" + actualDataRows);

// 2) ZIP64 布局自检：EOCD 位置 / locator 位于 EOCD 前 20 字节 / 两个 signature / locator 指向的偏移。
const zip64Buf = readFileSync(path.join(OUT, "udl01-zip64.xlsx"));
let eocdOffset = -1;
for (let i = zip64Buf.length - 22; i >= 0; i -= 1) {
  if (zip64Buf.readUInt32LE(i) === 0x06054b50) { eocdOffset = i; break; }
}
const locatorOffset = eocdOffset - 20;
const zip64EocdOffset = locatorOffset - 56;
const locatorSignature = eocdOffset >= 20 ? zip64Buf.readUInt32LE(locatorOffset) : 0;
const zip64EocdSignature = zip64EocdOffset >= 0 ? zip64Buf.readUInt32LE(zip64EocdOffset) : 0;
const locatorDeclaredOffset = eocdOffset >= 20 ? Number(zip64Buf.readBigUInt64LE(locatorOffset + 8)) : -1;
const zip64Layout = {
  bytes: zip64Buf.length,
  eocd_offset: eocdOffset,
  eocd_present: eocdOffset >= 0,
  locator_offset: locatorOffset,
  locator_is_20_bytes_before_eocd: locatorOffset === eocdOffset - 20,
  locator_signature: "0x" + locatorSignature.toString(16).padStart(8, "0"),
  locator_signature_ok: locatorSignature === 0x07064b50,
  zip64_eocd_offset: zip64EocdOffset,
  zip64_eocd_signature: "0x" + zip64EocdSignature.toString(16).padStart(8, "0"),
  zip64_eocd_signature_ok: zip64EocdSignature === 0x06064b50,
  locator_declared_zip64_eocd_offset: locatorDeclaredOffset,
  locator_points_at_zip64_eocd: locatorDeclaredOffset === zip64EocdOffset,
  layout: "ZIP64 EOCD -> ZIP64 locator -> classic EOCD",
};
if (!zip64Layout.eocd_present) problems.push("udl01-zip64.xlsx 找不到 EOCD");
if (!zip64Layout.locator_is_20_bytes_before_eocd) problems.push("ZIP64 locator 不在 EOCD 前 20 字节");
if (!zip64Layout.locator_signature_ok) problems.push("ZIP64 locator signature 不是 0x07064b50");
if (!zip64Layout.zip64_eocd_signature_ok) problems.push("ZIP64 EOCD signature 不是 0x06064b50");
if (!zip64Layout.locator_points_at_zip64_eocd) problems.push("ZIP64 locator 未指向 ZIP64 EOCD 记录");
if (!(locatorOffset > 0 && zip64Buf.readUInt32LE(0) === 0x04034b50)) problems.push("ZIP64 夹具的本地头魔数不正确");

const selfCheck = {
  generator: "frontend/scripts/r9-make-fixtures-v5c.mjs",
  generatedAt: new Date().toISOString(),
  out: OUT,
  ok: problems.length === 0,
  problems,
  rowSemantics,
  zip64Layout,
  fixtures: manifest.map((m) => ({ name: m.name, bytes: m.bytes, sha256: m.sha256 })),
};
writeFileSync(path.join(OUT, "udl01-fixtures-selfcheck.json"), JSON.stringify(selfCheck, null, 2) + "\n");
writeFileSync(path.join(OUT, "udl01-fixtures-manifest.json"), JSON.stringify({
  generatedAt: new Date().toISOString(),
  requestedRows: { xls: REQUESTED_ROWS_XLS, xlsx: ROWS_XLSX },
  actualFormatCapacity: rowSemantics.actualFormatCapacity,
  actualDataRowsOnDisk: actualDataRows,
  rowSemantics,
  zip64Layout,
  fixtures: manifest,
}, null, 2) + "\n", "utf8");

console.log(JSON.stringify({
  out: OUT,
  fixtures: manifest.map((m) => ({ name: m.name, bytes: m.bytes })),
  rowSemantics,
  zip64Layout,
  self_check_ok: problems.length === 0,
  problems,
}, null, 2));
if (problems.length > 0) {
  console.error("FIXTURE_SELF_CHECK_FAILED " + JSON.stringify(problems));
  process.exit(1);
}
