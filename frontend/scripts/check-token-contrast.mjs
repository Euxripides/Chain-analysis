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
/**
 * 用户端 ChainSight V4 色板（规范 §4.1）。
 *
 * 为什么不放进 tokens.css：那份文件被用户端与后台端共用，改它等于让
 * admin-console 被动换肤（规范 §1.3 / §2.1）。V4 的取值挂在
 * `html[data-app="user"]` 作用域下的 @chain/user-ui/theme/css-vars.css 里，
 * 因此守卫必须也读这一份，否则新色板完全没有对比度保护。
 */
const userCss = readFileSync(resolve(here, "../packages/user-ui/theme/css-vars.css"), "utf-8");

/**
 * 只收集**直接写成十六进制**的令牌：语义角色层（--bg-* / --text-* 等）
 * 大量使用 var() 间接引用，交给浏览器解析后又绕回了同一组原始色阶，
 * 因此对比度守卫统一在色阶/原始色这一层校验（蓝图 §6.1 的那张表）。
 */
function parseTokens(text) {
  const out = {};
  for (const m of text.matchAll(/--([a-z0-9-]+):\s*(#[0-9a-fA-F]{6})\s*;/g)) {
    if (
      m[1].startsWith("c-") ||
      m[1].startsWith("cs-") ||
      /^(ink|graphite|slate|canvas|indigo|cyan|amber|green|red|chart|state|graph)-/.test(m[1])
    ) {
      out[m[1]] = m[2].toLowerCase();
    }
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

  /* --- 用户端重构蓝图 §6.1 新增组合。前台/背景必须在此登记，否则 CI 不拦。 --- */
  ["slate-600", "canvas-50", 4.5, "画布底次级文字"],
  ["slate-600", "bg-surface-placeholder", 4.5, "占位：白底次级文字"],
  ["graphite-900", "canvas-50", 4.5, "画布底正文"],
  ["graphite-900", "bg-surface-placeholder", 4.5, "白底正文"],
  ["indigo-600", "bg-surface-placeholder", 4.5, "主操作/链接"],
  ["indigo-600", "indigo-soft", 4.5, "信息徽标"],
  ["cyan-text", "bg-surface-placeholder", 4.5, "实时数据文字"],
  ["cyan-text", "cyan-soft", 4.5, "实时数据徽标"],
  ["amber-600", "amber-soft-2", 4.5, "待复核徽标"],
  ["amber-600", "bg-surface-placeholder", 4.5, "注意文字"],
  ["green-700", "green-soft-2", 4.5, "已完成徽标"],
  ["green-700", "bg-surface-placeholder", 4.5, "成功文字"],
  ["red-700", "red-soft-2", 4.5, "失败徽标"],
  ["red-700", "bg-surface-placeholder", 4.5, "危险文字"],
  ["slate-400", "ink-950", 4.5, "深色导航次级文字"],
  ["ink-950", "bg-surface-placeholder", 4.5, "反色正文（哨兵：深底浅面）"],
];

/**
 * 用户端 V4 色板的实际前景/背景组合。
 *
 * 规范 §4.1 的原始色是**填充色**：主色 #3478F6 在白底只有 4.07:1，
 * success/warning/cyan 更低（3.56 / 2.72 / 2.31），都不能做正文。
 * 因此文字一律用同色相的 `-text` 变体，浅底用 `-soft` 变体，
 * 这张表就是这两组配对的可执行契约——任何一方被改坏都会在这里被拦下。
 */
const USER_PAIRS = [
  ["cs-text-primary", "cs-surface", 4.5, "用户端卡片正文"],
  ["cs-text-primary", "cs-canvas", 4.5, "用户端画布正文"],
  ["cs-text-primary", "cs-primary-soft", 4.5, "选中项正文"],
  ["cs-text-secondary", "cs-surface", 4.5, "用户端次级文字"],
  ["cs-text-secondary", "cs-canvas", 4.5, "用户端画布次级文字"],
  ["cs-text-secondary", "cs-surface-rail", 4.5, "浅色侧栏未选中项"],
  ["cs-primary-text", "cs-surface", 4.5, "主操作/链接"],
  ["cs-primary-text", "cs-primary-soft", 4.5, "当前导航项"],
  ["cs-primary-text", "cs-canvas", 4.5, "画布上的链接"],
  ["cs-cyan-text", "cs-cyan-soft", 4.5, "实时数据徽标"],
  ["cs-violet-text", "cs-violet-soft", 4.5, "图谱序列徽标"],
  ["cs-success-text", "cs-success-soft", 4.5, "已完成徽标"],
  ["cs-success-text", "cs-surface", 4.5, "成功文字"],
  ["cs-warning-text", "cs-warning-soft", 4.5, "待复核徽标"],
  ["cs-warning-text", "cs-surface", 4.5, "注意文字"],
  ["cs-danger-text", "cs-danger-soft", 4.5, "失败徽标"],
  ["cs-danger-text", "cs-surface", 4.5, "危险文字"],
  ["cs-text-primary", "cs-surface-rail", 4.5, "侧栏当前项文字"],
];

/** `--bg-surface` 是 #FFFFFF 的语义别名；守卫解析不到 var()，这里补一个等价哨兵。 */
const ALIAS = { "bg-surface-placeholder": "#ffffff" };

const tokens = { ...parseTokens(css), ...parseTokens(userCss) };
for (const [name, value] of Object.entries(ALIAS)) tokens[name] = value;
const failures = [];
console.log("设计令牌对比度检查（WCAG 2.1 AA）");
for (const [fg, bg, need, label] of [...PAIRS, ...USER_PAIRS]) {
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
