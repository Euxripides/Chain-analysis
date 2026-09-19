#!/usr/bin/env node
/**
 * CSS 覆盖遮蔽检查（本轮新增）
 *
 * 背景：用户端有一套新样式层（@chain/user-ui，全部选择器带
 * `html[data-app="user"]` 前缀，权重 0,2,2），用来盖住旧样式表里的桌面取值。
 * 副作用是：**旧文件里 @media 内的同名规则也被一起盖掉了**（权重 0,1,0）。
 * 这是一个真实缺陷——外壳在窄屏丢掉单列，整个手机端内容区只剩 220px，
 * 而不报错、不溢出、没有任何断言看得见。
 *
 * 但**不是所有遮蔽都是缺陷**，两种情况要分开：
 *   · 旧声明带 `!important` → `!important` 胜过权重，**没有被遮蔽**（误报）；
 *   · 新样式层自己也在同一个断点写了等价规则 → 语义被接住了，不算丢失。
 * 只有"既没 !important、新层也没在断点里补"的才是真的丢了。
 *
 * 用法：node scripts/css-shadowing-check.mjs
 */
import { readFileSync, existsSync } from "node:fs";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = resolve(fileURLToPath(new URL(".", import.meta.url)), "..");
const LEGACY = [
  "apps/user-console/src/styles/user-shell.css",
  "apps/user-console/src/styles/components.css",
  "apps/user-console/src/styles/responsive.css",
  "apps/user-console/src/styles/motion.css",
  "apps/user-console/src/sprint4.css",
  "apps/user-console/src/sprint5/sprint5.css",
];
const UI = [
  "packages/user-ui/styles/shell.css",
  "packages/user-ui/styles/surface.css",
  "packages/user-ui/styles/motion.css",
  "packages/user-ui/styles/progress.css",
  "packages/user-ui/styles/states.css",
  "packages/user-ui/styles/graph-shell.css",
  "packages/user-ui/styles/legacy-bridge.css",
  "packages/user-ui/styles/adapter.css",
];

/*
 * 已经确认"新层在同断点写了等价规则"的条目。
 * 保留白名单而不是放宽判据：放宽了这条检查就又开始报一堆噪声，
 * 噪声多了就没人看，最终等于没有检查。
 */
/* 目前不需要白名单：判据已经能自动识别"新层在断点内接住了"。留空占位。 */
const COVERED = new Set();

const strip = (css) => css.replace(/\/\*[\s\S]*?\*\//g, "");

/** 浅解析：返回 {selector, prop, important, media, file}。跳过规则体后必须把 depth 减回去。 */
function rules(css, file) {
  const out = [];
  let depth = 0;
  const mediaStack = [];
  let buf = "";
  for (let i = 0; i < css.length; i += 1) {
    const ch = css[i];
    if (ch === "{") {
      const head = buf.trim();
      buf = "";
      if (head.startsWith("@")) {
        depth += 1;
        if (head.startsWith("@media")) mediaStack.push({ depth, query: head });
        continue;
      }
      depth += 1;
      let d = 1;
      let j = i + 1;
      while (j < css.length && d > 0) {
        if (css[j] === "{") d += 1;
        else if (css[j] === "}") d -= 1;
        j += 1;
      }
      const body = css.slice(i + 1, j - 1);
      const media = mediaStack.length > 0 ? mediaStack[mediaStack.length - 1].query : null;
      for (const decl of body.split(";")) {
        const idx = decl.indexOf(":");
        if (idx < 0) continue;
        const prop = decl.slice(0, idx).trim();
        if (!prop || prop.startsWith("--") || prop.startsWith("@")) continue;
        const important = /!important\s*$/.test(decl.trim());
        for (const sel of head.split(",")) {
          out.push({ selector: sel.trim().replace(/\s+/g, " "), prop, important, media, file });
        }
      }
      i = j - 1;
      depth -= 1;
      while (mediaStack.length > 0 && mediaStack[mediaStack.length - 1].depth > depth) mediaStack.pop();
      continue;
    }
    if (ch === "}") {
      depth -= 1;
      while (mediaStack.length > 0 && mediaStack[mediaStack.length - 1].depth > depth) mediaStack.pop();
      buf = "";
      continue;
    }
    buf += ch;
  }
  return out;
}

/* 新层要**全部**收进来：判断"有没有被接住"靠的正是它自己的断点规则。 */
const uiRules = [];
for (const f of UI) { const p = join(here, f); if (existsSync(p)) uiRules.push(...rules(strip(readFileSync(p, "utf8")), f)); }
const legacyRules = [];
for (const f of LEGACY) { const p = join(here, f); if (existsSync(p)) legacyRules.push(...rules(strip(readFileSync(p, "utf8")), f).filter((r) => r.media)); }

const baseOf = (sel) => sel.replace(/^html\[data-app=["']user["']\]\s*/, "").trim();
const problems = [];
const neutralised = [];
const covered = [];
const seen = new Set();

const topLevelUi = uiRules.filter((r) => !r.media);
const mediaUi = uiRules.filter((r) => r.media);

for (const u of topLevelUi) {
  if (!u.selector.startsWith('html[data-app="user"]')) continue;
  const b = baseOf(u.selector);
  for (const l of legacyRules) {
    if (l.selector !== b || l.prop !== u.prop) continue;
    const key = b + "|" + l.prop + "|" + l.media;
    if (seen.has(key)) continue;
    seen.add(key);
    const entry = b + " 的 " + l.prop;
    if (l.important) {
      neutralised.push(l.media + " " + entry);
      continue;
    }
    /*
     * 关键判据：新层自己有没有在**断点内**重新声明同一个属性。
     * 有，说明这条语义被接住了（可能是等价改写，也可能是有意调整）；
     * 没有，才是真的被无声盖掉——那正是本轮要修的那一类。
     */
    const bridged = mediaUi.find((m) => baseOf(m.selector) === b && m.prop === l.prop);
    if (bridged) {
      covered.push(`${l.media} ${entry} → 已由 ${bridged.file} 的 ${bridged.media} 接住`);
    } else {
      problems.push(
        `${l.media} 下的 ${b} 的 ${l.prop} 被 @chain/user-ui 的 ${u.file} 顶层规则覆盖，旧声明没有 !important，新层也没有在断点内补回来`,
      );
    }
  }
}

const report = { ok: problems.length === 0, problems, notes: [] };
if (neutralised.length > 0) report.notes.push(`带 !important、未被遮蔽：${neutralised.length} 条（reduced-motion 多数属此类）`);
if (covered.length > 0) {
  report.notes.push(`新层已在断点内接住（不是遮蔽）：${covered.length} 条`);
  for (const c of covered.slice(0, 6)) report.notes.push("  · " + c);
}
report.notes.push(`扫描：新层顶层规则 ${uiRules.length} 条 · 旧样式表 @media 内规则 ${legacyRules.length} 条`);
console.log(JSON.stringify(report, null, 2));
if (problems.length > 0) process.exit(1);
