#!/usr/bin/env node
/**
 * Bundle 预算检查（用户端重构规范 V4 §56.2 / §63E）
 *
 * §63E 要求"CI 输出 bundle diff，超预算必须在 PR 说明"。这个脚本把预算变成**可执行的断言**，
 * 而不是一段写在文档里、没人核对的数字。
 *
 * 关键点：初始包不是"最大的那个 chunk"，而是 **index.html 实际会请求的那一组**——
 * `<script type="module">` 加上所有 `<link rel="modulepreload">`。
 * 只看文件大小排名会把懒加载的路由 chunk 也算进来，得出完全错误的结论。
 *
 * 还会专门断言：ProComponents / ECharts / AG Grid / 图谱引擎 **不得**出现在初始包里
 * （§56.1 明确要求它们 route lazy-load）。
 *
 * 用法：pnpm build:user && node scripts/bundle-budget-check.mjs
 */
import { readFileSync, existsSync, statSync } from "node:fs";
import { gzipSync } from "node:zlib";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = resolve(fileURLToPath(new URL(".", import.meta.url)), "..");
const DIST = join(here, "apps/user-console/dist");

/** §56.2 的初始建议值。超过必须在 PR 里说明原因，而不是静默放过。 */
const BUDGET_INITIAL_KB = 250;

/** 绝不允许进入初始包的库（§56.1）。 */
const LAZY_ONLY = [
  { name: "@ant-design/pro-components", marker: "pro-table" },
  { name: "echarts", marker: "echarts" },
  { name: "ag-grid", marker: "ag-grid" },
];

function gzipSizeKb(path) {
  return gzipSync(readFileSync(path)).length / 1024;
}

function main() {
  const indexPath = join(DIST, "index.html");
  if (!existsSync(indexPath)) {
    console.error("缺少构建产物，请先运行 pnpm build:user");
    process.exit(2);
  }
  const html = readFileSync(indexPath, "utf8");

  /* 初始包 = 入口脚本 + 所有 modulepreload（浏览器会在首屏并行请求它们）。 */
  const initialAssets = new Set();
  for (const match of html.matchAll(/<(?:script|link)[^>]*?(?:src|href)="([^"]+\.js)"/g)) {
    initialAssets.add(match[1]);
  }
  /* 只统计同源、静态的 js（asset 路径形如 /app-v2/assets/xxx.js）。 */
  const initial = [...initialAssets].filter((url) => url.includes("/assets/"));

  let total = 0;
  const rows = [];
  for (const url of initial) {
    const file = join(DIST, url.replace(/^.*\/assets\//, "assets/"));
    if (!existsSync(file)) continue;
    const kb = gzipSizeKb(file);
    total += kb;
    rows.push({ file: url.split("/").pop(), rawKb: statSync(file).size / 1024, gzipKb: kb });
  }

  const problems = [];
  console.log("初始包（index.html 实际请求的 JS，gzip）：");
  for (const row of rows.sort((a, b) => b.gzipKb - a.gzipKb)) {
    console.log(`  ${row.file.padEnd(42)} ${row.rawKb.toFixed(1).padStart(8)} KB → ${row.gzipKb.toFixed(2).padStart(7)} KB`);
  }
  console.log(`  ${"合计".padEnd(40)} ${" ".padStart(8)}    ${total.toFixed(2).padStart(7)} KB / 预算 ${BUDGET_INITIAL_KB} KB`);

  if (total > BUDGET_INITIAL_KB) {
    problems.push(`初始包 ${total.toFixed(2)} KB 超出预算 ${BUDGET_INITIAL_KB} KB`);
  }

  /* 初始包的文件名里不能出现懒加载库的标记。 */
  const initialNames = rows.map((row) => row.file).join(",");
  for (const lib of LAZY_ONLY) {
    if (initialNames.includes(lib.marker)) {
      problems.push(`${lib.name} 出现在初始包中（${lib.marker}），违反 §56.1`);
    }
  }

  console.log("");
  if (problems.length > 0) {
    console.error("bundle 预算未通过：");
    for (const problem of problems) console.error("  - " + problem);
    process.exit(1);
  }
  console.log("bundle 预算通过。");
}

main();
