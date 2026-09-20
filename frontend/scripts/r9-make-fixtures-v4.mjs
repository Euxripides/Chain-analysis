// r9-make-fixtures-v4.mjs — R9 v4 夹具生成器。
//
// 历史：v1 用 XLSX.writeFile（ESM 构建未注入 fs → "cannot save file"，整套 ENOENT）；
//       v2 用 XLSX.write(type:"buffer") + fs.writeFileSync，修复写盘；
//       v3 增加改名文件 / 坏文件 / 手写 ZIP 炸弹。
//
// 本轮（《新建数据下载页面全量整改实施规范》§11）修正 v3 的真实缺陷：
//   v3 的手写 ZIP 构造函数只支持**单条目**，且三个炸弹只写 xl/workbook.xml。
//   生产防线（apps/user-console/src/import/parse-core.ts）先校验 OOXML 必需条目
//   [Content_Types].xml 与 xl/workbook.xml，再校验声明解压总大小 / 声明压缩比 / 真实解压，
//   因此 v3 的三个炸弹全部先被"缺少 [Content_Types].xml"拒绝，**没有命中各自的安全分支**。
//
// 修法（§11.2）：构造函数支持多条目；所有炸弹都带完整 OOXML 必需条目，恶意内容放进
// xl/worksheets/sheet1.xml。生产校验顺序一行未改（§11.4 禁止改生产来迎合夹具）。
//
// 生成后执行机器自检（§11.5）并写出 JSON 证据：ZIP 魔数 / EOCD / 中央目录可遍历 /
// 必需条目 / 每个文件 SHA256 / 大小 < 16MiB / 夹具意图与中央目录字段一致。
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { writeFileSync } from "node:fs";
import path from "node:path";
import { deflateRawSync, inflateRawSync } from "node:zlib";
import * as XLSX from "xlsx";

const outDir = process.argv[2];
const address = process.argv[3];
const chain = process.argv[4] ?? "bsc";
const missingCase = process.argv[5] ?? "R9-MISSING-CASE";
const missingTag = process.argv[6] ?? "R9-MISSING-TAG";
if (!outDir || !address) {
  console.error("usage: node r9-make-fixtures-v4.mjs <outDir> <address> [chain] [missingCase] [missingTag]");
  process.exit(2);
}

/* 与生产 IMPORT_LIMITS 逐值一致（只做自检阈值，不参与生产校验）。 */
const IMPORT_LIMITS = {
  maxFileBytes: 16 * 1024 * 1024,
  maxUncompressedBytes: 64 * 1024 * 1024,
  maxCompressionRatio: 200,
};
const MIB = 1024 * 1024;

function writeXlsx(file, rows) {
  const wb = XLSX.utils.book_new();
  const ws = XLSX.utils.json_to_sheet(rows);
  XLSX.utils.book_append_sheet(wb, ws, "Sheet1");
  writeFileSync(file, XLSX.write(wb, { type: "buffer", bookType: "xlsx" }));
}
function writeCsv(file, rows) {
  const wb = XLSX.utils.book_new();
  const ws = XLSX.utils.json_to_sheet(rows);
  XLSX.utils.book_append_sheet(wb, ws, "Sheet1");
  writeFileSync(file, XLSX.utils.sheet_to_csv(ws), "utf8");
}

/* ------------------------------------------------------------------ 手写多条目 ZIP */

const crc32 = (() => {
  const table = new Uint32Array(256);
  for (let i = 0; i < 256; i += 1) {
    let c = i;
    for (let k = 0; k < 8; k += 1) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    table[i] = c >>> 0;
  }
  return (buf) => {
    let c = 0xffffffff;
    for (const b of buf) c = table[(c ^ b) & 0xff] ^ (c >>> 8);
    return (c ^ 0xffffffff) >>> 0;
  };
})();

/**
 * 手工构造经典 ZIP：本地头 + 数据 + 中央目录 + EOCD。
 * 每个条目的 declaredUncompressed 可独立声明（用于虚假声明 / 压缩炸弹意图），
 * 但本地头与中央目录写**同一个**声明值（生产会交叉核对两者）。
 */
function buildZip(entries) {
  const local = [];
  const central = [];
  let offset = 0;
  for (const entry of entries) {
    const raw = Buffer.isBuffer(entry.data) ? entry.data : Buffer.from(entry.data, "utf8");
    const method = entry.method ?? 8;
    const compressed = method === 0 ? raw : deflateRawSync(raw);
    const nameBuf = Buffer.from(entry.name, "utf8");
    const flags = entry.flags ?? 0;
    const crc = crc32(raw);
    const declared = entry.declaredUncompressed ?? raw.length;
    const lh = Buffer.alloc(30);
    lh.writeUInt32LE(0x04034b50, 0);
    lh.writeUInt16LE(20, 4);
    lh.writeUInt16LE(flags, 6);
    lh.writeUInt16LE(method, 8);
    lh.writeUInt32LE(crc, 14);
    lh.writeUInt32LE(compressed.length, 18);
    lh.writeUInt32LE(declared >>> 0, 22);
    lh.writeUInt16LE(nameBuf.length, 26);
    lh.writeUInt16LE(0, 28);
    local.push(lh, nameBuf, compressed);
    central.push({ nameBuf, flags, method, compressed: compressed.length, declared: declared >>> 0, crc, offset });
    offset += lh.length + nameBuf.length + compressed.length;
  }
  const cdOffset = offset;
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
    cdh.writeUInt32LE(c.declared, 24);
    cdh.writeUInt16LE(c.nameBuf.length, 28);
    cdh.writeUInt16LE(0, 30);
    cdh.writeUInt16LE(0, 32);
    cdh.writeUInt16LE(0, 34);
    cdh.writeUInt16LE(0, 36);
    cdh.writeUInt32LE(0, 38);
    cdh.writeUInt32LE(c.offset, 42);
    cdParts.push(cdh, c.nameBuf);
    offset += cdh.length + c.nameBuf.length;
  }
  const cdSize = offset - cdOffset;
  const eocd = Buffer.alloc(22);
  eocd.writeUInt32LE(0x06054b50, 0);
  eocd.writeUInt16LE(0, 4);
  eocd.writeUInt16LE(0, 6);
  eocd.writeUInt16LE(central.length, 8);
  eocd.writeUInt16LE(central.length, 10);
  eocd.writeUInt32LE(cdSize, 12);
  eocd.writeUInt32LE(cdOffset, 16);
  eocd.writeUInt16LE(0, 20);
  return Buffer.concat(local.concat(cdParts, [eocd]));
}

/* ------------------------------------------------------- 夹具 1~5（与 v3 相同，未改） */

writeXlsx(outDir + "/min.xlsx", [{ address, chain, case: missingCase, tags: missingTag }]);
writeCsv(outDir + "/min.csv", [{ address, chain, case: missingCase, tags: missingTag }]);

const upper = address.toUpperCase().replace("0X", "0x");
writeXlsx(outDir + "/mixed.xlsx", [
  { address, chain, case: "", tags: "" },
  { address: "0xNOT-AN-ADDRESS", chain, case: "", tags: "" },
  { address: upper, chain, case: "", tags: "" },
]);

const large = [];
for (let i = 0; i < 150; i += 1) {
  large.push({ address: "0x" + String(i + 1).padStart(40, "0"), chain, case: "", tags: "" });
}
large[0] = { address, chain, case: "", tags: "" };
writeXlsx(outDir + "/large.xlsx", large);

writeFileSync(outDir + "/bad.xlsx", Buffer.from("this is definitely not a zip/xlsx payload\n\u0000\u0001\u0002", "utf8"));

// 改名文件：CSV 内容 + .xlsx 扩展名（不是 ZIP）——生产必须按真实魔数拒绝，不能宽松嗅探。
writeFileSync(outDir + "/renamed.xlsx", "address,chain\n" + address + "," + chain + "\n", "utf8");

/* --------------------------------------------------- 夹具 6~8：三个压缩炸弹（本轮修正） */

/**
 * OOXML 必需条目（§11.2）：生产 inspectOoxmlZip 先校验这两项存在，缺一即
 * 「不是合法的 OOXML 工作簿：缺少 …」。三个炸弹都必须带齐，才能进入后续安全分支。
 */
const OOXML_CORE = [
  {
    name: "[Content_Types].xml",
    data:
      '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>' +
      '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">' +
      '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>' +
      '<Default Extension="xml" ContentType="application/xml"/>' +
      '<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>' +
      '<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>' +
      "</Types>",
  },
  {
    name: "xl/workbook.xml",
    data:
      '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>' +
      '<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" ' +
      'xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">' +
      '<sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>',
  },
];
const SHEET_NAME = "xl/worksheets/sheet1.xml";
const SHEET_XML =
  '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>' +
  '<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData/></worksheet>';

/** bomb-size：中央目录声明解压总大小 > 64MiB，文件本身远小于 16MiB。 */
const BOMB_SIZE_DECLARED = 100 * MIB;
/** bomb-ratio：声明总大小 <= 64MiB，但 declaredTotal / file.size > 200。 */
const BOMB_RATIO_DECLARED = 8 * MIB;
/** bomb-lie：声明值低于安全阈值，真实 deflate 解压 72MiB，超过剩余 64MiB 预算。 */
const BOMB_LIE_DECLARED = 1024;
const BOMB_LIE_REAL_BYTES = 72 * MIB;

const bombFixtures = [
  {
    file: "bomb-size.xlsx",
    intent: "声明解压后总大小超过 64MiB 上限（中央目录 declared total > 64MiB，文件 < 16MiB）",
    expectedBranch: "声明解压后总大小 + 超过上限",
    buffer: buildZip(OOXML_CORE.concat([{ name: SHEET_NAME, data: SHEET_XML, declaredUncompressed: BOMB_SIZE_DECLARED }])),
  },
  {
    file: "bomb-ratio.xlsx",
    intent: "声明压缩比超过 200（declaredTotal <= 64MiB 且 declaredTotal / file.size > 200）",
    expectedBranch: "声明压缩比 + 超过上限",
    buffer: buildZip(OOXML_CORE.concat([{ name: SHEET_NAME, data: SHEET_XML, declaredUncompressed: BOMB_RATIO_DECLARED }])),
  },
  {
    file: "bomb-lie.xlsx",
    intent: "中央目录声明值不可信：真实 deflate 解压超过剩余 64MiB 预算（声明 1KiB，真实 72MiB）",
    expectedBranch: "真实解压 + 声明值不可信",
    buffer: buildZip(
      OOXML_CORE.concat([
        { name: SHEET_NAME, data: Buffer.alloc(BOMB_LIE_REAL_BYTES, 0x41), declaredUncompressed: BOMB_LIE_DECLARED },
      ]),
    ),
  },
];
for (const fixture of bombFixtures) writeFileSync(path.join(outDir, fixture.file), fixture.buffer);

/* ---------------------------------------------------------------- 机器自检（§11.5） */

const REQUIRED_OOXML = ["[Content_Types].xml", "xl/workbook.xml"];

/** 遍历经典 ZIP 的中央目录（与生产解析器同样的字节语义，独立实现）。 */
function readZipStructure(buf) {
  const out = { zipMagic: buf.length >= 4 && buf[0] === 0x50 && buf[1] === 0x4b && buf[2] === 0x03 && buf[3] === 0x04, eocdOffset: -1, entries: [], cdOffset: -1, cdSize: -1 };
  for (let i = buf.length - 22; i >= 0; i -= 1) {
    if (buf[i] === 0x50 && buf[i + 1] === 0x4b && buf[i + 2] === 0x05 && buf[i + 3] === 0x06) {
      out.eocdOffset = i;
      break;
    }
  }
  if (out.eocdOffset < 0) return out;
  out.entryCount = buf.readUInt16LE(out.eocdOffset + 10);
  out.cdSize = buf.readUInt32LE(out.eocdOffset + 12);
  out.cdOffset = buf.readUInt32LE(out.eocdOffset + 16);
  let p = out.cdOffset;
  for (let i = 0; i < out.entryCount; i += 1) {
    if (buf.readUInt32LE(p) !== 0x02014b50) break;
    const method = buf.readUInt16LE(p + 10);
    const compressed = buf.readUInt32LE(p + 20);
    const declared = buf.readUInt32LE(p + 24);
    const nameLen = buf.readUInt16LE(p + 28);
    const extraLen = buf.readUInt16LE(p + 30);
    const commentLen = buf.readUInt16LE(p + 32);
    const localOffset = buf.readUInt32LE(p + 42);
    const name = buf.slice(p + 46, p + 46 + nameLen).toString("utf8");
    const localNameLen = buf.readUInt16LE(localOffset + 26);
    const localExtraLen = buf.readUInt16LE(localOffset + 28);
    const dataOffset = localOffset + 30 + localNameLen + localExtraLen;
    const raw = method === 8 ? inflateRawSync(buf.slice(dataOffset, dataOffset + compressed)) : buf.slice(dataOffset, dataOffset + compressed);
    out.entries.push({ name, method, compressed, declared, localOffset, dataOffset, realUncompressed: raw.length });
    p += 46 + nameLen + extraLen + commentLen;
  }
  return out;
}

const selfCheckFixtures = [];
let selfCheckOk = true;
const allFiles = ["min.xlsx", "min.csv", "mixed.xlsx", "large.xlsx", "bad.xlsx", "renamed.xlsx", "bomb-size.xlsx", "bomb-ratio.xlsx", "bomb-lie.xlsx"];

for (const name of allFiles) {
  const buf = readFileSync(path.join(outDir, name));
  const sha256 = createHash("sha256").update(buf).digest("hex");
  const row = { file: name, bytes: buf.length, sha256, under_max_file_bytes: buf.length < IMPORT_LIMITS.maxFileBytes };
  if (name.endsWith(".xlsx") && !["bad.xlsx", "renamed.xlsx"].includes(name)) {
    const zip = readZipStructure(buf);
    const declaredTotal = zip.entries.reduce((sum, e) => sum + e.declared, 0);
    const realTotal = zip.entries.reduce((sum, e) => sum + e.realUncompressed, 0);
    const ratio = buf.length > 0 ? declaredTotal / buf.length : 0;
    const names = zip.entries.map((e) => e.name);
    Object.assign(row, {
      zip_magic: zip.zipMagic,
      eocd_offset: zip.eocdOffset,
      eocd_present: zip.eocdOffset >= 0,
      central_directory_offset: zip.cdOffset,
      central_directory_size: zip.cdSize,
      entry_count: zip.entries.length,
      entries: zip.entries,
      required_ooxml_entries: REQUIRED_OOXML.every((n) => names.includes(n)),
      declared_total: declaredTotal,
      real_total: realTotal,
      declared_ratio: Number(ratio.toFixed(3)),
    });
  }
  selfCheckFixtures.push(row);
}

const byName = Object.fromEntries(selfCheckFixtures.map((r) => [r.file, r]));
const bombIntent = [];
for (const fixture of bombFixtures) {
  const row = byName[fixture.file];
  const intentOk =
    row.zip_magic === true &&
    row.eocd_present === true &&
    row.central_directory_offset > 0 &&
    row.central_directory_size > 0 &&
    row.entry_count === 3 &&
    row.required_ooxml_entries === true &&
    row.bytes < IMPORT_LIMITS.maxFileBytes &&
    row.entries.some((e) => e.name === SHEET_NAME);
  let targetOk = false;
  let targetDetail = {};
  if (fixture.file === "bomb-size.xlsx") {
    targetOk = row.declared_total > IMPORT_LIMITS.maxUncompressedBytes && row.bytes < IMPORT_LIMITS.maxFileBytes;
    targetDetail = { declared_total: row.declared_total, limit: IMPORT_LIMITS.maxUncompressedBytes };
  } else if (fixture.file === "bomb-ratio.xlsx") {
    targetOk =
      row.declared_total <= IMPORT_LIMITS.maxUncompressedBytes &&
      row.declared_ratio > IMPORT_LIMITS.maxCompressionRatio &&
      row.bytes < IMPORT_LIMITS.maxFileBytes;
    targetDetail = { declared_total: row.declared_total, declared_ratio: row.declared_ratio, limit: IMPORT_LIMITS.maxCompressionRatio };
  } else if (fixture.file === "bomb-lie.xlsx") {
    const sheet = row.entries.find((e) => e.name === SHEET_NAME);
    targetOk =
      row.declared_total <= IMPORT_LIMITS.maxUncompressedBytes &&
      row.declared_ratio <= IMPORT_LIMITS.maxCompressionRatio &&
      sheet.realUncompressed > IMPORT_LIMITS.maxUncompressedBytes &&
      sheet.declared < sheet.realUncompressed &&
      row.bytes < IMPORT_LIMITS.maxFileBytes;
    targetDetail = { declared_total: row.declared_total, declared_ratio: row.declared_ratio, sheet_declared: sheet.declared, sheet_real: sheet.realUncompressed };
  }
  if (!intentOk || !targetOk) selfCheckOk = false;
  bombIntent.push({
    file: fixture.file,
    intent: fixture.intent,
    expected_branch: fixture.expectedBranch,
    structure_ok: intentOk,
    intent_matches_central_directory: targetOk,
    detail: targetDetail,
  });
}
/* bad.xlsx / renamed.xlsx 必须**不是** ZIP（改名文件不是 ZIP，坏文件也不是）。 */
for (const name of ["bad.xlsx", "renamed.xlsx"]) {
  const buf = readFileSync(path.join(outDir, name));
  const isZip = buf.length >= 4 && buf[0] === 0x50 && buf[1] === 0x4b;
  byName[name].is_zip = isZip;
  if (isZip) selfCheckOk = false;
}
if (selfCheckFixtures.some((r) => !r.under_max_file_bytes)) selfCheckOk = false;

const selfCheck = {
  generator: "frontend/scripts/r9-make-fixtures-v4.mjs",
  generated_at: new Date().toISOString(),
  out_dir: outDir,
  address,
  chain,
  limits: IMPORT_LIMITS,
  required_ooxml_entries: REQUIRED_OOXML,
  malicious_entry: SHEET_NAME,
  ok: selfCheckOk,
  fixtures: selfCheckFixtures,
  bomb_intents: bombIntent,
};
writeFileSync(path.join(outDir, "r9-07-fixture-selfcheck.json"), JSON.stringify(selfCheck, null, 2) + "\n");

console.log(
  JSON.stringify(
    {
      outDir,
      address,
      chain,
      files: allFiles,
      self_check: path.join(outDir, "r9-07-fixture-selfcheck.json"),
      self_check_ok: selfCheckOk,
      bomb_intents: bombIntent,
    },
    null,
    2,
  ),
);
if (!selfCheckOk) {
  console.error("FIXTURE_SELF_CHECK_FAILED");
  process.exit(1);
}
