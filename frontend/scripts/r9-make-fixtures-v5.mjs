/**
 * r9-make-fixtures-v5.mjs — U-DL-01（隔离解析 / 有界超时 / 可取消 / 恶意 ZIP）专用真实夹具。
 *
 * v1..v4 夹具文件全部原样保留；本文件只新增，不覆盖。
 * 所有 ZIP 都是**手工构造的真实字节结构**（不是 mock）：本地头 + 数据 + 中央目录 + EOCD，
 * 以精确命中 §23.7 的每一条防线分支（大 .xls / 大 .xlsx / 炸弹 / 加密 / stored 不符 /
 * 本地头与方法不符 / data descriptor / ZIP64）。
 */
import { createHash } from "node:crypto";
import { deflateRawSync } from "node:zlib";
import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";

const OUT = process.argv[2] ?? ".";
mkdirSync(OUT, { recursive: true });

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
  const parts = chunks.concat(cdParts, [eocd]);
  if (options.trailing) parts.push(Buffer.from(options.trailing));
  if (options.zip64Locator) { const loc = Buffer.alloc(20); loc.writeUInt32LE(0x07064b50, 0); parts.push(loc); }
  return Buffer.concat(parts);
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
const ROWS = 200000;
const matrix = [["address", "chain", "tags"]];
for (let i = 0; i < ROWS; i++) matrix.push(["0x" + (i + 1).toString(16).padStart(40, "0"), "bsc", i % 7 === 0 ? "bulk|v5" : ""]);
const wsBig = XLSX.utils.aoa_to_sheet(matrix);
const wbXls = XLSX.utils.book_new();
XLSX.utils.book_append_sheet(wbXls, wsBig, "Sheet1");
write("udl01-big.xls", XLSX.write(wbXls, { type: "buffer", bookType: "xls" }),
  "真实 BIFF8 .xls（" + ROWS + " 行 + 表头；OLE/CFB 魔数；用于大文件 CPU 路径）");

const wbBig = XLSX.utils.book_new();
XLSX.utils.book_append_sheet(wbBig, wsBig, "Sheet1");
write("udl01-big.xlsx", XLSX.write(wbBig, { type: "buffer", bookType: "xlsx" }),
  "真实 OOXML .xlsx（" + ROWS + " 行 + 表头；用于有界超时与取消）");

const bombRaw = Buffer.alloc(200 * 1024 * 1024, 0x41);
write("udl01-bomb.xlsx", buildZip(OOXML_CORE.concat([{ name: "xl/worksheets/sheet1.xml", data: bombRaw }])),
  "真实 OOXML 结构 + 可解压 200MB 条目（命中真实解压上限分支）");
write("udl01-encrypted.xlsx", buildZip(OOXML_CORE.concat([{ name: "xl/worksheets/sheet1.xml", data: "<x/>", flags: 0x1 }])),
  "中央目录与本地头都标记加密（flag 0x1）");
write("udl01-stored-mismatch.xlsx", buildZip(OOXML_CORE.concat([{ name: "xl/worksheets/sheet1.xml", data: "<x/>", method: 0, declaredUncompressed: 999 }])),
  "stored(method 0) 条目声明解压长度与实际存放不一致");
write("udl01-method-mismatch.xlsx", buildZip(OOXML_CORE.concat([{ name: "xl/worksheets/sheet1.xml", data: "<x/>", localMethod: 0 }])),
  "本地头 method=0 而中央目录 method=8");
write("udl01-data-descriptor.xlsx", buildZip(OOXML_CORE.concat([{ name: "xl/worksheets/sheet1.xml", data: "<x/>", flags: 0x8, localFlags: 0x8 }])),
  "条目使用 data descriptor（flag 0x8）");
write("udl01-zip64.xlsx", buildZip(OOXML_CORE.concat([{ name: "xl/worksheets/sheet1.xml", data: "<x/>" }]), { zip64Locator: true }),
  "EOCD 之前存在 ZIP64 结束定位器");

const wbSmall = XLSX.utils.book_new();
XLSX.utils.book_append_sheet(wbSmall, XLSX.utils.aoa_to_sheet([["address", "chain", "tags"], ["0x00000000000000000000000000000000000000d1", "bsc", "v5"]]), "Sheet1");
write("udl01-small-ok.xlsx", XLSX.write(wbSmall, { type: "buffer", bookType: "xlsx" }), "正常小工作簿（隔离解析正向对照）");

writeFileSync(path.join(OUT, "udl01-fixtures-manifest.json"), JSON.stringify({ generatedAt: new Date().toISOString(), rows: ROWS, fixtures: manifest }, null, 2) + "\n", "utf8");
console.log(JSON.stringify({ out: OUT, fixtures: manifest.map((m) => ({ name: m.name, bytes: m.bytes })) }, null, 2));
