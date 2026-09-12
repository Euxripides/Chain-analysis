#!/usr/bin/env node
/**
 * 设计令牌对比度守卫（CI 用）。
 *
 * 背景：`--c-amber` 在 `--c-amber-soft` 上只有 2.90:1，不满足 WCAG AA，
 * 是在 Sprint 1/2 遗留验收的浏览器对比度扫描里才发现的。为了避免以后再引入
 * 同类问题，这里把令牌层面的组合做成可执行检查。
 *
 * 用法：node scripts/check-token-contrast.mjs
 */
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(resolve(here, "../packages/design-tokens/tokens.css"), "utf-8");

function parseTokens(text) {
  const out = {};
  for (const m of text.matchAll(/--(c-[a-z-]+):\s*(#[0-9a-fA-F]{6})/g)) {
    out[m[1]] = m[2].toLowerCase();
  }
  return out;
}

function luminance(hex) {
  const channel = (v) => {
    const c = v / 255;
    return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
  };
  const r = parseInt(hex.slice(1, 3), 16);
  const g = parseInt(hex.slice(3, 5), 16);
  const b = parseInt(hex.slice(5, 7), 16);
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

function ratio(a, b) {
  const la = luminance(a);
  const lb = luminance(b);
  const hi = Math.max(la, lb);
  const lo = Math.min(la, lb);
  return (hi + 0.05) / (lo + 0.05);
}

// 实际会出现在界面上的"前景 / 背景"组合。新增语义色时必须在此登记。
const PAIRS = [
  ["c-amber-text", "c-amber-soft", 4.5, "待复核徽标"],
  ["c-amber-text", "c-surface", 4.5, "正文风险文字"],
  ["c-green", "c-green-soft", 4.5, "已验证徽标"],
  ["c-danger", "c-danger-soft", 4.5, "错误徽标"],
  ["c-cobalt", "c-cobalt-soft", 4.5, "信息徽标"],
  ["c-cobalt", "c-surface", 4.5, "链接"],
  ["c-neutral", "c-neutral-soft", 4.5, "中性徽标"],
  ["c-slate", "c-surface", 4.5, "次级文字"],
  ["c-slate", "c-ice", 4.5, "工作区次级文字"],
  ["c-graphite", "c-ice", 4.5, "工作区正文"],
  ["c-graphite", "c-surface", 4.5, "卡片正文"],
];

const tokens = parseTokens(css);
const failures = [];
console.log("设计令牌对比度检查（WCAG 2.1 AA）");
for (const [fg, bg, need, label] of PAIRS) {
  if (!tokens[fg] || !tokens[bg]) {
    failures.push(`${label}: 令牌缺失（${fg} / ${bg}）`);
    continue;
  }
  const r = ratio(tokens[fg], tokens[bg]);
  const ok = r >= need;
  console.log(`  ${ok ? "OK  " : "FAIL"} ${label.padEnd(16)} ${fg} on ${bg} = ${r.toFixed(2)} (需要 ${need})`);
  if (!ok) failures.push(`${label}: ${fg} on ${bg} = ${r.toFixed(2)} < ${need}`);
}

if (failures.length) {
  console.error("\n对比度检查未通过：");
  for (const f of failures) console.error("  - " + f);
  process.exit(1);
}
console.log("\n全部通过。");
