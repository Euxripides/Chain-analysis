#!/usr/bin/env node
/**
 * 类型检查门禁的**非空洞性**证明（本轮新增）
 *
 * 起因：仓库根目录的 `tsc -b` 读的是 tsconfig.app.json 的 `include: ["src"]`，
 * 那是 frontend/src 下的一套遗留源码——**一个 apps/ 或 packages/ 的文件都不含**。
 * 于是很长一段时间里，"类型检查通过"这句话对重构真正改动的代码毫无意义，
 * 它还顺手掩盖了两个真实缺陷：
 *   · packages/graph-engine/lenses.ts 把 RenderStyle 从 "../renderer/…" 导入
 *     （应为 "./renderer/…"）——因为那是 type-only import，打包器根本不会解析它；
 *   · __verify__/probe.ts 里 countInk 的形参标注比它实际读取的属性更窄。
 *
 * 光把配置改对是不够的：这类问题的本质是"门禁看起来在跑，但没覆盖到代码"。
 * 所以这里做两件事：
 *   1. 断言 typecheck 脚本指向统一配置，且该配置真的收进了两个 app 的 src 与 packages；
 *   2. **变异测试**——往 apps/user-console/src 里塞一个必然的语法错误，
 *      断言 tsc 确实会报出来。门禁如果再次指向错误的 include，这一步就会失败。
 *
 * 用法：node scripts/typecheck-coverage-check.mjs
 */
import { readFileSync, writeFileSync, unlinkSync, existsSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = resolve(fileURLToPath(new URL(".", import.meta.url)), "..");

/*
 * 直接用编译器入口而不是 npx：Windows 上 npx 是 npx.cmd，
 * `execFileSync` 不经过 shell 时找不到它（spawnSync npx ENOENT），
 * 而那会让整个门禁变成"永远失败"或（更糟）"永远通过"。
 */
const TSC = join(here, "node_modules", "typescript", "bin", "tsc");
function runTsc(args) {
  if (!existsSync(TSC)) throw new Error("找不到本地 typescript：" + TSC);
  return execFileSync(process.execPath, [TSC, ...args], { cwd: here, encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
}
const problems = [];
const notes = [];

/* ---------- 1. 脚本与配置必须对得上 ---------- */
const pkg = JSON.parse(readFileSync(join(here, "package.json"), "utf8"));
const typecheck = pkg.scripts?.typecheck ?? "";
if (!typecheck.includes("tsconfig.typecheck.json")) {
  problems.push(`package.json 的 typecheck 没有指向 tsconfig.typecheck.json（当前：${typecheck || "缺失"}）`);
}
if (/tscs+-b/.test(typecheck)) {
  problems.push("typecheck 仍然使用 tsc -b：它会走 tsconfig.app.json 的 include: [\"src\"]，只检查遗留的 frontend/src");
}

const config = JSON.parse(
  readFileSync(join(here, "tsconfig.typecheck.json"), "utf8").replace(/\/\*[\s\S]*?\*\//g, ""),
);
const include = config.include ?? [];
for (const required of ["apps/user-console/src", "apps/admin-console/src", "packages"]) {
  if (!include.includes(required)) problems.push(`tsconfig.typecheck.json 的 include 缺少 ${required}`);
}
for (const alias of ["@chain/user-ui", "@chain/graph-engine", "@chain/graph-react"]) {
  if (!config.compilerOptions?.paths?.[alias]) problems.push(`tsconfig.typecheck.json 的 paths 缺少 ${alias}`);
}

/* ---------- 2. 变异测试：门禁必须真的看得见 apps/ 里的错误 ---------- */
const canary = join(here, "apps/user-console/src/__typecheck_canary.ts");
let mutationCaught = false;
try {
  /*
   * 故意写一个类型错误（而不是语法错误）：语法错误任何配置都会报，
   * 只有**类型**错误才能证明这个配置是以 strict 模式在检查这个文件。
   */
  writeFileSync(canary, "export const canary: number = \"not a number\";\n", "utf8");
  let output = "";
  try {
    runTsc(["--noEmit", "-p", "tsconfig.typecheck.json"]);
  } catch (error) {
    output = String(error.stdout ?? "") + String(error.stderr ?? "");
  }
  mutationCaught = output.includes("__typecheck_canary");
  if (!mutationCaught) {
    problems.push("变异测试失败：往 apps/user-console/src 写入类型错误后 tsc 没有报出来，说明这个门禁没有覆盖到应用代码");
  }
} finally {
  try {
    unlinkSync(canary);
  } catch {
    /* 已经不存在就算了 */
  }
}

/* ---------- 3. 覆盖率下限：文件数不能悄悄缩水 ---------- */
try {
  const listed = runTsc(["--noEmit", "-p", "tsconfig.typecheck.json", "--listFiles"]);
  const projectFiles = listed
    .split(/\r?\n/)
    .filter((line) => /\.tsx?$/.test(line) && !line.includes("node_modules"));
  const ucFiles = projectFiles.filter((line) => line.includes("apps/user-console/src")).length;
  const adFiles = projectFiles.filter((line) => line.includes("apps/admin-console/src")).length;
  const pkgFiles = projectFiles.filter((line) => line.includes(join("frontend", "packages")) || /packages[\\/](ui|user-ui|api-client|design-tokens|graph-engine|graph-react)[\\/]/.test(line)).length;
  notes.push(`类型检查覆盖：user-console ${ucFiles} 个文件 · admin-console ${adFiles} 个 · packages ${pkgFiles} 个（合计 ${projectFiles.length}）`);
  /*
   * 下限按**实测**设定（user-console 58 个源文件、admin-console 41 个、packages 61 个，
   * 去掉 .d.ts），留一点余量而不是拍脑袋写 100+：
   * 第一步就是量一遍真实文件数，写一个够不着的阈值只会让门禁永远红着。
   */
  if (ucFiles < 50) problems.push(`user-console 只检查到 ${ucFiles} 个文件（实测应有 ~58），覆盖疑似缩水`);
  if (adFiles < 35) problems.push(`admin-console 只检查到 ${adFiles} 个文件（实测应有 ~41），覆盖疑似缩水`);
  if (pkgFiles < 50) problems.push(`packages 只检查到 ${pkgFiles} 个文件（实测应有 ~61），覆盖疑似缩水`);
} catch (error) {
  problems.push("无法列出类型检查覆盖的文件：" + String(error).slice(0, 200));
}

notes.push(mutationCaught ? "变异测试：往应用源码写入类型错误后 tsc 确实报错（门禁非空洞）" : "变异测试未通过");
const report = { ok: problems.length === 0, problems, notes };
console.log(JSON.stringify(report, null, 2));
if (problems.length > 0) process.exit(1);
