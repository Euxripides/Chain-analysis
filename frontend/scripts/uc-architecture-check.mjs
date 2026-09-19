#!/usr/bin/env node
/**
 * §61 文件级职责断言（本轮新增）
 *
 * §61 对 CanvasPage 的要求是"最终只保留 workspace orchestration，图形渲染逻辑全部移出"。
 * 这是一条**架构**约束，而不是行为约束——渲染结果对不对由 check:uc-render 断言，
 * 但"渲染逻辑有没有搬出去"没有任何运行时检查能发现：把 <svg> 抄回页面里，
 * 所有功能测试照常通过，只是这条约束又悄悄失效了。
 *
 * 所以这里做静态断言：页面里不许出现 SVG 图元，绘制组件里不许出现状态与取数。
 *
 * 用法：node scripts/uc-architecture-check.mjs
 */
import { readFileSync, existsSync } from "node:fs";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = resolve(fileURLToPath(new URL(".", import.meta.url)), "..");
const PAGES = join(here, "apps/user-console/src/pages");
const problems = [];
const notes = [];

const canvasPagePath = join(PAGES, "CanvasPage.tsx");
const stagePath = join(PAGES, "canvas/CanvasStage.tsx");

if (!existsSync(canvasPagePath)) problems.push("找不到 CanvasPage.tsx");
if (!existsSync(stagePath)) problems.push("找不到 canvas/CanvasStage.tsx（§61 要求渲染逻辑在独立模块里）");

if (problems.length === 0) {
  const page = readFileSync(canvasPagePath, "utf8");
  const stage = readFileSync(stagePath, "utf8");
  /* 去掉注释再判断：文档里提到 "<svg" 不算违规。 */
  const strip = (text) => text.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
  const pageCode = strip(page);
  const stageCode = strip(stage);

  /* 1. 页面里不许再有 SVG 图元。 */
  for (const tag of ["<svg", "<circle", "<line", "<polygon", "<rect", "<marker"]) {
    if (pageCode.includes(tag)) {
      problems.push(`CanvasPage.tsx 里仍然出现 ${tag}：图形渲染逻辑没有移出（§61）`);
    }
  }

  /* 2. 绘制组件必须是纯的：不持状态、不取数、不碰路由。 */
  for (const hook of ["useState", "useEffect", "useMemo", "useCallback", "useRef", "useApi", "useParams", "useNavigate"]) {
    if (new RegExp(`\\b${hook}\\s*[(<]`).test(stageCode)) {
      problems.push(`CanvasStage.tsx 使用了 ${hook}：渲染组件应当是纯呈现，状态与取数留在页面`);
    }
  }

  /* 3. 页面必须通过 CanvasStage 渲染，而不是另起炉灶。 */
  if (!page.includes("CanvasStage")) problems.push("CanvasPage.tsx 没有使用 CanvasStage");

  const svgPrimitives = (stageCode.match(/<(svg|circle|line|polygon|rect|marker|text|g)\b/g) ?? []).length;
  notes.push(`CanvasStage.tsx：${svgPrimitives} 个图元 · 无状态 · 无取数；CanvasPage.tsx 已无任何图元`);
}

const report = { ok: problems.length === 0, problems, notes };
console.log(JSON.stringify(report, null, 2));
if (problems.length > 0) process.exit(1);
