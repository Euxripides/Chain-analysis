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

  /*
   * 2. 绘制组件**可以**持状态——它要管引擎的挂载与销毁周期，那是渲染自己的事；
   *    但它**不许取数、不许碰路由**：数据从 props 进来，导航由页面负责。
   *    （R6 迁移之前这里是"完全不许有状态"，因为当时的 CanvasStage 只画 SVG；
   *      迁移之后引擎生命周期必须有归宿，把它推到页面反而违反 §61。）
   */
  for (const hook of ["useApi", "useParams", "useNavigate", "useSearchParams", "useLocation"]) {
    if (new RegExp(`\\b${hook}\\s*[(<]`).test(stageCode)) {
      problems.push(`CanvasStage.tsx 使用了 ${hook}：渲染组件不得取数或依赖路由，数据只能从 props 进来`);
    }
  }
  /* 反向也要成立：舞台必须真的管了引擎的生命周期，否则"迁移"只是换了个壳。 */
  if (!/new GraphEngine\(/.test(stageCode)) {
    problems.push("CanvasStage.tsx 没有创建图谱引擎：画布应迁移到 @chain/graph-engine（§65）");
  }
  if (!/engine\.dispose\(\)/.test(stageCode)) {
    problems.push("CanvasStage.tsx 没有在卸载时 dispose 引擎（§63C 生命周期）");
  }

  /* 3. 页面必须通过 CanvasStage 渲染，并且**不得直接依赖引擎**——那正是这条边界。 */
  if (!page.includes("CanvasStage")) problems.push("CanvasPage.tsx 没有使用 CanvasStage");
  if (pageCode.includes("@chain/graph-engine")) {
    problems.push("CanvasPage.tsx 直接导入了 @chain/graph-engine：引擎必须留在 CanvasStage 之后（§61）");
  }

  const svgPrimitives = (stageCode.match(/<(svg|circle|line|polygon|rect|marker|text|g)\b/g) ?? []).length;
  notes.push(
    `CanvasStage.tsx：${svgPrimitives} 个 SVG 图元 · 自带引擎生命周期 · 不取数不碰路由；CanvasPage.tsx 已无任何图元且不依赖引擎`,
  );
}

const report = { ok: problems.length === 0, problems, notes };
console.log(JSON.stringify(report, null, 2));
if (problems.length > 0) process.exit(1);
