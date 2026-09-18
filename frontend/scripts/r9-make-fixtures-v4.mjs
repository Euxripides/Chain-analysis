// r9-make-fixtures-v4.mjs — v2：修正 v1 的真实缺陷（SheetJS 的 ESM 构建里 writeFile 依赖
// set_fs，未注入时抛 "cannot save file"，导致 v1 夹具生成失败、整套测试 ENOENT）。
// 改用 XLSX.write(type:"buffer") + fs.writeFileSync。v1 未修改，其失败运行原样保留。
// 用生产依赖树里的 SheetJS 0.20.3 真实写盘，保证测的是客户端真实路径。
import { writeFileSync } from "node:fs";
import { deflateRawSync } from "node:zlib";
import * as XLSX from "xlsx";

const outDir = process.argv[2];
const address = process.argv[3];
const chain = process.argv[4] ?? "bsc";
const missingCase = process.argv[5] ?? "R9-MISSING-CASE";
const missingTag = process.argv[6] ?? "R9-MISSING-TAG";
if (!outDir || !address) {
  console.error("usage: node r9-make-fixtures-v3.mjs <outDir> <address> [chain] [missingCase] [missingTag]");
  process.exit(2);
}

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

// 1) 最小合法文件：address / chain / case / tags（案件与标签在本工作区都不存在）
writeXlsx(outDir + "/min.xlsx", [{ address, chain, case: missingCase, tags: missingTag }]);
writeCsv(outDir + "/min.csv", [{ address, chain, case: missingCase, tags: missingTag }]);

// 2) 混合文件：1 合法 + 1 非法 + 1 合法重复（大小写不同，用来验证 chain + normalized 去重键）
const upper = address.toUpperCase().replace("0X", "0x");
writeXlsx(outDir + "/mixed.xlsx", [
  { address, chain, case: "", tags: "" },
  { address: "0xNOT-AN-ADDRESS", chain, case: "", tags: "" },
  { address: upper, chain, case: "", tags: "" },
]);

// 3) 大文件：150 行（本地截断到 100，服务端必须显式拒绝其余 50 行）
const large = [];
for (let i = 0; i < 150; i += 1) {
  large.push({ address: "0x" + String(i + 1).padStart(40, "0"), chain, case: "", tags: "" });
}
large[0] = { address, chain, case: "", tags: "" };
writeXlsx(outDir + "/large.xlsx", large);

// 4) 坏文件：不是合法 xlsx（真实坏文件，不是空文件）
writeFileSync(outDir + "/bad.xlsx", Buffer.from("this is definitely not a zip/xlsx payload\n\u0000\u0001\u0002", "utf8"));

// 5) 改名文件：CSV 内容 + .xlsx 扩展名（不是 ZIP）——product 必须按真实格式拒绝，不能宽松嗅探
writeFileSync(outDir + "/renamed.xlsx", "address,chain\n" + address + "," + chain + "\n", "utf8");

// 6) 手写 ZIP 中央目录：声明解压尺寸超限 / 压缩比超限（§23.7 大小、条目、压缩比限制）
function buildZip(name, declaredUncompressed, payload) {
  const nameBuf = Buffer.from(name, "utf8");
  const data = deflateRawSync(payload);
  const local = Buffer.alloc(30);
  local.writeUInt32LE(0x04034b50, 0); local.writeUInt16LE(20, 4); local.writeUInt16LE(0, 6);
  local.writeUInt16LE(8, 8); local.writeUInt16LE(0, 10); local.writeUInt16LE(0, 12);
  local.writeUInt32LE(0, 14); local.writeUInt32LE(data.length, 18);
  local.writeUInt32LE(declaredUncompressed >>> 0, 22);
  local.writeUInt16LE(nameBuf.length, 26); local.writeUInt16LE(0, 28);
  const cdOffset = 30 + nameBuf.length + data.length;
  const cd = Buffer.alloc(46);
  cd.writeUInt32LE(0x02014b50, 0); cd.writeUInt16LE(20, 4); cd.writeUInt16LE(20, 6);
  cd.writeUInt16LE(0, 8); cd.writeUInt16LE(8, 10); cd.writeUInt16LE(0, 12); cd.writeUInt16LE(0, 14);
  cd.writeUInt32LE(0, 16); cd.writeUInt32LE(data.length, 20);
  cd.writeUInt32LE(declaredUncompressed >>> 0, 24);
  cd.writeUInt16LE(nameBuf.length, 28); cd.writeUInt16LE(0, 30); cd.writeUInt16LE(0, 32);
  cd.writeUInt16LE(0, 34); cd.writeUInt16LE(0, 36); cd.writeUInt32LE(0, 38); cd.writeUInt32LE(0, 42);
  const cdSize = 46 + nameBuf.length;
  const eocd = Buffer.alloc(22);
  eocd.writeUInt32LE(0x06054b50, 0); eocd.writeUInt16LE(0, 4); eocd.writeUInt16LE(0, 6);
  eocd.writeUInt16LE(1, 8); eocd.writeUInt16LE(1, 10);
  eocd.writeUInt32LE(cdSize, 12); eocd.writeUInt32LE(cdOffset, 16); eocd.writeUInt16LE(0, 20);
  return Buffer.concat([local, nameBuf, data, cd, nameBuf, eocd]);
}
writeFileSync(outDir + "/bomb-size.xlsx", buildZip("xl/workbook.xml", 100 * 1024 * 1024, Buffer.alloc(1024)));
writeFileSync(outDir + "/bomb-ratio.xlsx", buildZip("xl/workbook.xml", 1024 * 1024, Buffer.alloc(1024)));
// 7) 声明与实际不符：中央目录声明 1 KiB，真实 deflate 流解压约 72 MiB。
//    只信声明大小的 guard 会放行；必须靠"真实解压计数"才能拦住。
writeFileSync(outDir + "/bomb-lie.xlsx", buildZip("xl/workbook.xml", 1024, Buffer.alloc(72 * 1024 * 1024)));

console.log(JSON.stringify({ outDir, address, chain, files: ["min.xlsx", "min.csv", "mixed.xlsx", "large.xlsx", "bad.xlsx", "renamed.xlsx", "bomb-size.xlsx", "bomb-ratio.xlsx", "bomb-lie.xlsx"] }));
