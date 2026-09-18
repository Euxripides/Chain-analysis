// r9-make-fixtures-v2.mjs — v2：修正 v1 的真实缺陷（SheetJS 的 ESM 构建里 writeFile 依赖
// set_fs，未注入时抛 "cannot save file"，导致 v1 夹具生成失败、整套测试 ENOENT）。
// 改用 XLSX.write(type:"buffer") + fs.writeFileSync。v1 未修改，其失败运行原样保留。
// 用生产依赖树里的 SheetJS 0.20.3 真实写盘，保证测的是客户端真实路径。
import { writeFileSync } from "node:fs";
import * as XLSX from "xlsx";

const outDir = process.argv[2];
const address = process.argv[3];
const chain = process.argv[4] ?? "bsc";
const missingCase = process.argv[5] ?? "R9-MISSING-CASE";
const missingTag = process.argv[6] ?? "R9-MISSING-TAG";
if (!outDir || !address) {
  console.error("usage: node r9-make-fixtures-v2.mjs <outDir> <address> [chain] [missingCase] [missingTag]");
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

console.log(JSON.stringify({ outDir, address, chain, files: ["min.xlsx", "min.csv", "mixed.xlsx", "large.xlsx", "bad.xlsx"] }));
